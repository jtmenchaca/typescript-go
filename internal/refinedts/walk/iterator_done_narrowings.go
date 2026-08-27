// ITERATOR RESULT PRESENCE: what `!r.done` says about `r.value`.
//
// `next()` hands back a record with exactly a value property and a
// done property (sec-createiterresultobject). The two are correlated,
// and the correlation is fixed by the specification rather than by
// anything the walk infers: a yield suspends with
// CreateIteratorResultObject(value, *false*) (sec-yield), and only the
// finishing resumption builds one with done *true* — which is the one
// whose value is the generator's return value rather than an element.
// readIteratorNext builds the record accordingly: `value` carries the
// element BESIDE absence, because the walk usually cannot order a
// given next() against the iterator's exhaustion, and its own comment
// already states the rest of the law — "the record's own `done` is
// what tells a caller which it holds — a caller that checks it narrows
// back to the element". This file is that narrowing.
//
// The row is written as a DOTTED PLACE ENTRY on `r.value`, the memory
// evaluate_property_access.go already reads and meets at every
// property read, and that ForgetPlaceEntriesEnv already sweeps on
// every write to `r`. So the fact needs no channel of its own: it is
// the same place-value memory every other in-place narrowing uses, and
// it goes stale on exactly the same events.
//
// Only a done test on a place whose held `value` wears the maybe
// wrapper writes anything. Where `value` carries no absence — the
// first-next route already answered the element outright — there is
// nothing to strip and no row is written.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
)

// IteratorDoneNarrowing is one row IteratorDoneNarrowings answers: the
// dotted place key for a result record's `value`, and the present half
// the done-false test proved of it.
type IteratorDoneNarrowing struct {
	Place string
	Known abstractdomain.AbstractValue
}

// IteratorDoneNarrowings answers what a held condition proves about
// the `value` of any iterator result it tests for NOT-done. The
// recognized leaves, both of which prove done false on the side being
// read:
//
//	!r.done          r.done === false
//
// negated mirrors the other guard readers: pass false for the
// condition's held side, true for its refuted side, so the exit shape
// (`if (r.done) return;`) reaches the same rows through the same
// conjunctive-leaf walk.
//
// held answers a binding's current knowledge — the caller hands its
// Env's own Get, so no environment copy crosses this boundary.
func IteratorDoneNarrowings(
	held func(name string) (abstractdomain.AbstractValue, bool),
	condition *ast.Node,
	negated bool,
) []IteratorDoneNarrowing {
	var rows []IteratorDoneNarrowing
	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		record := doneFalseRecordName(leaf)
		if record == "" {
			continue
		}
		value, ok := heldResultValue(held, record)
		if !ok {
			continue
		}
		rows = append(rows, IteratorDoneNarrowing{Place: record + ".value", Known: value})
	}
	return rows
}

// doneFalseRecordName reads one leaf into the record binding it proves
// NOT-done of, or "" for any other leaf.
//
// A leaf the shared tree marked negated is the `!r.done` spelling: the
// tree's own De Morgan push turns the prefix `!` into the leaf's
// polarity, so `r.done` arriving NEGATED is exactly the done-false
// proof. A leaf arriving held proves done-false only when it is
// written as an explicit comparison against false.
func doneFalseRecordName(leaf conditiontree.ConditionLeaf) string {
	test := leaf.Test
	if test == nil {
		return ""
	}
	for ast.IsParenthesizedExpression(test) {
		test = test.AsParenthesizedExpression().Expression
	}
	if leaf.Negated {
		return donePropertyRecordName(test)
	}
	if !ast.IsBinaryExpression(test) {
		return ""
	}
	binary := test.AsBinaryExpression()
	switch binary.OperatorToken.Kind {
	case ast.KindEqualsEqualsEqualsToken, ast.KindEqualsEqualsToken:
	default:
		return ""
	}
	if binary.Right.Kind == ast.KindFalseKeyword {
		return donePropertyRecordName(binary.Left)
	}
	if binary.Left.Kind == ast.KindFalseKeyword {
		return donePropertyRecordName(binary.Right)
	}
	return ""
}

// donePropertyRecordName is the binding of a `<name>.done` access, or
// "" for anything else. Only a plain identifier receiver answers: the
// row's whole staleness discipline is the sweep of one root name.
func donePropertyRecordName(e *ast.Node) string {
	bare := e
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if !ast.IsPropertyAccessExpression(bare) {
		return ""
	}
	access := bare.AsPropertyAccessExpression()
	if access.Name().Text() != "done" {
		return ""
	}
	if !ast.IsIdentifier(access.Expression) {
		return ""
	}
	return access.Expression.Text()
}

// heldResultValue is the PRESENT half of a held record's `value` key,
// where that record is an iterator result whose value wears the maybe
// wrapper. Answers ok=false where the binding holds no object, holds
// no `value` key, or holds one carrying no absence — in the last case
// the read already answers the element and there is nothing to strip.
func heldResultValue(
	held func(name string) (abstractdomain.AbstractValue, bool),
	record string,
) (abstractdomain.AbstractValue, bool) {
	known, ok := held(record)
	if !ok || known.Kind != abstractdomain.KindObject {
		return abstractdomain.AbstractValue{}, false
	}
	// the record the specification builds carries exactly `value` and
	// `done`; a record without both is not one this row speaks about
	var value *abstractdomain.AbstractValue
	sawDone := false
	for index := range known.Keys {
		switch known.Keys[index].Name {
		case "value":
			value = &known.Keys[index].Value
		case "done":
			sawDone = true
		}
	}
	if value == nil || !sawDone {
		return abstractdomain.AbstractValue{}, false
	}
	if value.Kind != abstractdomain.KindPossiblyUndefined || value.Inner == nil {
		return abstractdomain.AbstractValue{}, false
	}
	return *value.Inner, true
}
