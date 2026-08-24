// A Map presence guard: what `m.has(k)` says when it holds (or its
// negation, refuted) of a tracked Map — the key it names becomes a
// PROVEN entry, so a later `m.get(k)` no longer wears the absence a
// plain read carries. Mirrors length_guard_narrowings.go's own shape:
// a fact family with no home in narrowing's Narrowed vocabulary
// (that channel narrows the TESTED place's own scalar set; this one
// adds an entry to a SIBLING binding's collection), read straight off
// the held Env and applied by the caller's own Set, bypassing
// ApplyNarrowed entirely.
package dataflowfacts

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MapPresenceNarrowing is one row MapPresenceNarrowings answers: a
// Map binding's presence-guard-tightened knowledge.
type MapPresenceNarrowing struct {
	Binding string
	Known   abstractdomain.AbstractValue
}

// MapPresenceNarrowings is what `m.has(k)` says when it holds of a
// tracked Map with a LITERAL string key: the key names a PROVEN
// entry, so a later read of it no longer needs to wear the absence a
// plain `.get` carries. Only a literal key is read — collectionKey's
// own exact-value gate (walk/collection_models.go) means a symbolic
// key finds no entry back by SameKnown either, so a guard naming one
// would prove a fact no later read could ever match; a literal key
// is exactly the shape both sides agree on.
//
// negated mirrors LengthGuardNarrowings: pass false to read the
// condition's HELD (true) side, true to read its REFUTED (false)
// side — the exit-guard shape (`if (!m.has(k)) return; …after: k
// present`) refutes `!m.has(k)`, which is the same as holding
// `m.has(k)`, so the caller negates the SOURCE condition and this
// function reads its conjunctive leaves at the requested polarity
// exactly as LengthGuardNarrowings does.
//
// Only a MISSING entry is added — an entry already present (whatever
// its value) is left as it stood, so a guard naming an already-proven
// key never re-widens a sharper held value back to Unknown. Adding
// the entry never flips the collection's own Complete bit: an
// incomplete record with one more named key is still incomplete about
// every OTHER key, and Complete is the SEPARATE proof that no untracked
// call could have added one — Complete flips only through the
// construction/write routes that already own it
// (walk/collection_models.go's refresh helpers).
func MapPresenceNarrowings(
	held func(name string) (abstractdomain.AbstractValue, bool),
	condition *ast.Node,
	negated bool,
) []MapPresenceNarrowing {
	var rows []MapPresenceNarrowing

	guardRow := func(receiver *ast.Node, key string) {
		if !ast.IsIdentifier(receiver) {
			return
		}
		binding := receiver.Text()
		heldValue, ok := held(binding)
		if !ok || heldValue.Kind != abstractdomain.KindCollection || heldValue.CollectionFlavor != abstractdomain.FlavorMap {
			return
		}
		keyValue := abstractdomain.KnownValues(refinementsets.CodepointsOf(key), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
		for _, entry := range heldValue.Entries {
			if abstractdomain.SameKnown(entry.Key, keyValue) {
				// already named — nothing to add, and nothing to re-widen
				return
			}
		}
		entries := append(append([]abstractdomain.CollectionEntry{}, heldValue.Entries...),
			abstractdomain.CollectionEntry{Key: keyValue, Value: abstractdomain.Unknown})
		tightened := heldValue
		tightened.Entries = entries
		rows = append(rows, MapPresenceNarrowing{Binding: binding, Known: tightened})
	}

	readLeaf := func(e *ast.Node, leafNegated bool) {
		if !ast.IsCallExpression(e) {
			return
		}
		call := e.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			return
		}
		pa := call.Expression.AsPropertyAccessExpression()
		if pa.Name().Text() != "has" {
			return
		}
		if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
			return
		}
		key := StringLiteralOf(call.Arguments.Nodes[0])
		if key == nil {
			return
		}
		// a HELD (non-negated) `m.has(k)` proves presence; a held `!m.has(k)`
		// (leafNegated true, from the shared tree's De Morgan push) proves
		// the opposite and adds nothing
		if leafNegated {
			return
		}
		guardRow(pa.Expression, *key)
	}

	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}
