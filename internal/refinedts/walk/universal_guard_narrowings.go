// A universal guard over a sequence: what `xs.every(x => P(x))` says
// about xs when it holds.
//
// sec-array.prototype.every runs the predicate at every present index
// and answers true only when none of them answered falsy. So a held
// `xs.every(p)` is exactly the claim "every element of xs satisfies
// p" — the element set narrows to whatever p's own lift proves, and
// the length is untouched (every() over an empty array answers true
// vacuously, which states nothing about the count).
//
// PLACEMENT mirrors inverse_factor_narrowings.go: the natural home is
// dataflowfacts beside LengthGuardNarrowings and MapPresenceNarrowings,
// whose shape this follows row for row, but lifting the predicate
// needs narrowing.Narrowings, and narrowing already imports
// dataflowfacts (a cycle Go bans). walk imports both one-way, so the
// function lands here.
//
// The DUAL, `xs.some(p)`, narrows nothing and has no row here: some()
// proves that AN element satisfies p, never which one, so no
// individual element read gains a window from it (A7.xfer.some's own
// claim states exactly this).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// UniversalGuardNarrowing is one row UniversalGuardNarrowings answers:
// a sequence binding's element-narrowed knowledge.
type UniversalGuardNarrowing struct {
	Binding string
	Known   abstractdomain.AbstractValue
}

// UniversalGuardNarrowings is what `xs.every(x => P(x))` says when it
// holds of a sequence binding, in either of the two shapes a sequence
// is held in: a REPETITION's element set meets P's lift while the
// counting window stays exactly as it was, and an EXACT LIST's every
// position meets the lift on its own while the length and order stay
// exactly as they were. Both read the identical fact — the predicate
// held at every present index — off the shape that fact lands in.
//
// Only an ARROW predicate with a single element parameter and an
// EXPRESSION body is read — the same shape FilterOutcome lifts
// (callback_element_outcome.go), and for the same reason: the lift
// asks narrowing.Narrowings what the body proves about that one
// parameter, which a block body's statements do not present.
//
// A predicate the lift cannot read narrows nothing and emits no row:
// every() never removes members from the element set it is asked
// about, so the unnarrowed element is always the sound answer.
//
// negated mirrors LengthGuardNarrowings and MapPresenceNarrowings:
// false reads the condition's HELD side, true its REFUTED side (the
// exit-guard shape). A REFUTED every() proves only that SOME element
// failed p — never which — so a negated leaf emits no row, exactly as
// a negated `m.has(k)` does.
func UniversalGuardNarrowings(
	ctx *FlowContext,
	held func(name string) (abstractdomain.AbstractValue, bool),
	condition *ast.Node,
	negated bool,
) []UniversalGuardNarrowing {
	var rows []UniversalGuardNarrowing

	guardRow := func(receiver *ast.Node, predicate *ast.Node) {
		if !ast.IsIdentifier(receiver) {
			return
		}
		binding := receiver.Text()
		heldValue, ok := held(binding)
		if !ok {
			return
		}
		// the two receiver shapes a universal guard can tighten. A
		// REPETITION states one element set across an unstated count; an
		// EXACT LIST states each position's own value. every() narrows
		// both the same way — the predicate held of every present index
		// (sec-array.prototype.every) — but the list keeps its positions
		// apart, so each item meets the lift on its own rather than
		// through one shared element set.
		var rep refinementsets.Repeated
		isRepetition := false
		if heldValue.Kind == abstractdomain.KindSet {
			var repOk bool
			rep, repOk = refinementsets.AsRepetition(heldValue.Set)
			if !repOk {
				return
			}
			isRepetition = true
		} else if heldValue.Kind != abstractdomain.KindList || len(heldValue.Items) == 0 {
			return
		}
		arrow := predicate.AsArrowFunction()
		if arrow == nil || arrow.Parameters == nil || len(arrow.Parameters.Nodes) == 0 {
			return
		}
		parameter0 := arrow.Parameters.Nodes[0]
		if !ast.IsIdentifier(parameter0.Name()) {
			return
		}
		elementParameter := parameter0.Name().Text()
		body := arrow.Body
		if body == nil || ast.IsBlock(body) {
			return
		}
		// the predicate's own lift, read ONCE off the body and then
		// applied to whichever element the receiver shape presents — the
		// same two steps FilterOutcome takes. narrowedAny reports whether
		// the body proved anything at all about the parameter: a
		// predicate the lift cannot read narrows nothing, and every()
		// never removes members, so the unnarrowed value stands.
		lifted := narrowing.Narrowings(ctx.P.Checker, body, func(name string) bool {
			return name == elementParameter
		}, nil, narrowing.GuardReadNowhere)
		applyLift := func(element abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
			narrowedAny := false
			for _, n := range lifted.WhenTrue {
				if n.Binding == elementParameter && len(n.Path) == 0 {
					element = narrowing.ApplyNarrowed(element, n)
					narrowedAny = true
				}
			}
			return element, narrowedAny
		}

		// an EXACT LIST narrows position by position: every present index
		// ran the predicate (sec-array.prototype.every), so each item
		// meets the lift on its own and the length and order are
		// untouched. A position the lift leaves unnarrowed keeps exactly
		// what it held — no position is ever widened here.
		if !isRepetition {
			items := make([]abstractdomain.AbstractValue, len(heldValue.Items))
			narrowedAny := false
			for i, item := range heldValue.Items {
				narrowedItem, itemNarrowed := applyLift(item)
				items[i] = narrowedItem
				if itemNarrowed {
					narrowedAny = true
				}
			}
			if !narrowedAny {
				return
			}
			tightened := abstractdomain.KnownList(items,
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(heldValue), abstractdomain.TrustSpec))
			for i := range rows {
				if rows[i].Binding == binding {
					rows[i].Known = tightened
					return
				}
			}
			rows = append(rows, UniversalGuardNarrowing{Binding: binding, Known: tightened})
			return
		}

		element := abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustLevelOf(heldValue), abstractdomain.SetKindTagNone)
		element, narrowedAny := applyLift(element)
		if !narrowedAny {
			return
		}
		elementSet, hasElementSet := abstractdomain.SetOfKnown(element)
		if !hasElementSet {
			return
		}
		// the WINDOW is every()'s own untouched fact: the predicate says
		// nothing about how many elements there are (an empty array
		// answers true vacuously), so the rebuilt repetition keeps the
		// held lo and hi exactly
		rebuilt, rebuiltOk := refinementsets.Repetition(elementSet, rep.Lo, rep.Hi), true
		if !rebuiltOk {
			return
		}
		tightened := abstractdomain.KnownWithMeasures(
			abstractdomain.KnownSet(rebuilt, nil,
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(heldValue), abstractdomain.TrustSpec),
				abstractdomain.SetKindTagNone),
			heldValue.Measures,
		)
		// narrowing the ELEMENT set never unmakes a density proof: the
		// same physical positions are present, each now known to satisfy
		// the predicate as well — the identical reasoning
		// LengthGuardNarrowings applies when it tightens the window.
		if heldValue.SeqDenseKnown && heldValue.SeqDense {
			tightened = abstractdomain.KnownSetDense(tightened)
		}
		// one row per binding — a later leaf tightens what an earlier one
		// proved, the same running-state rule LengthGuardNarrowings uses
		for i := range rows {
			if rows[i].Binding == binding {
				rows[i].Known = tightened
				return
			}
		}
		rows = append(rows, UniversalGuardNarrowing{Binding: binding, Known: tightened})
	}

	readLeaf := func(e *ast.Node, leafNegated bool) {
		if leafNegated {
			return
		}
		if !ast.IsCallExpression(e) {
			return
		}
		call := e.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			return
		}
		pa := call.Expression.AsPropertyAccessExpression()
		if pa.Name().Text() != "every" {
			return
		}
		if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
			return
		}
		predicate := call.Arguments.Nodes[0]
		if !ast.IsArrowFunction(predicate) {
			return
		}
		guardRow(pa.Expression, predicate)
	}

	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}
