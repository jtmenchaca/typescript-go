// A zero-exclusion guard: what `x !== 0` says when it holds (or its
// negation, `x === 0` refuted) of a tracked place already known to be
// a closed integer window or an exact values list: the window's own
// zero ENDPOINT retreats by one, or the exact zero is dropped from
// the list. Mirrors length_guard_narrowings.go's and
// map_presence_narrowings.go's own shape: a held-based rewrite of the
// TESTED place's own AbstractValue, read straight off the Env and
// applied by the caller's own Set — the DifferenceConstraint ledger
// (linear_constraints.go) cannot carry this fact at all, because
// "not equal to 0" is an EXCLUSION (x > 0 ∨ x < 0), not an order
// bound between two real tracked places (JT's ruling on the
// mechanism-2 report: DifferenceConstraint's two-real-places shape
// stays untouched; no synthetic-zero PlaceKey is invented).
//
// The domain states WINDOWS, not punctured intervals — a set with an
// interior hole (`a < 0 < b`) has no form to spell "this range minus
// one interior point" as a single window, so that shape narrows
// nothing here; a weaker true claim (the ORIGINAL window, unpunctured)
// stays exactly as sound as it was, which is the domain's ordinary
// decline discipline, never a wrong answer. Only the two ENDPOINT
// cases (a = 0 or b = 0) rebuild as a plain window, because retreating
// a closed bound by one integer step is still a window.
package dataflowfacts

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ZeroExclusionNarrowing is one row ZeroExclusionNarrowings answers: a
// binding's zero-guard-tightened knowledge.
type ZeroExclusionNarrowing struct {
	Binding string
	Known   abstractdomain.AbstractValue
}

// closedIntegerEdges reads a RefinedSet's own closed integer window
// edges — nil, nil where the set carries no Integer form, or where
// either edge is a STRICT bound (`above`/`below`) rather than a
// closed one (`atLeast`/`atMost`): a strict edge is not itself an
// integer of the window (`above 0` already excludes 0 — nothing for
// a zero guard to retreat), so only the closed forms are read here.
// Either edge may be nil (an unbounded ray) without refusing the
// other.
func closedIntegerEdges(set refinementsets.RefinedSet) (lo *float64, hi *float64, isInteger bool) {
	for _, f := range set.Forms {
		switch f.Form {
		case refinementsets.FormInteger:
			isInteger = true
		case refinementsets.FormAtLeast:
			v := f.A
			lo = &v
		case refinementsets.FormAtMost:
			v := f.A
			hi = &v
		}
	}
	return lo, hi, isInteger
}

// retreatedWindow rebuilds set's own forms with an AtLeast/AtMost edge
// replaced by the retreated bound — every other form (Integer
// included) carries over unchanged, so a MultipleOf or a second
// window form stacked alongside stays exactly as it was.
func retreatedWindow(set refinementsets.RefinedSet, replaceLo, replaceHi *float64) refinementsets.RefinedSet {
	forms := make([]refinementsets.Refinement, 0, len(set.Forms))
	for _, f := range set.Forms {
		switch {
		case f.Form == refinementsets.FormAtLeast && replaceLo != nil:
			forms = append(forms, refinementsets.AtLeast(*replaceLo))
		case f.Form == refinementsets.FormAtMost && replaceHi != nil:
			forms = append(forms, refinementsets.AtMost(*replaceHi))
		default:
			forms = append(forms, f)
		}
	}
	return refinementsets.MakeRefinedSet(forms...)
}

// ZeroExclusionNarrowings is what `x !== 0` says when it holds of a
// tracked place: an integer-sorted CLOSED window touching 0 at
// exactly one endpoint retreats that endpoint by one integer step
// (`[0, b]` narrows to `[1, b]`; `[a, 0]` narrows to `[a, -1]`); an
// exact values list drops the zero entry outright. A non-integer
// sort, a window with no edge AT zero, or a window straddling zero
// on both sides (`a < 0 < b`, no single window spells the puncture)
// narrows nothing — the weaker, unpunctured claim stays exactly as
// sound as it already was.
//
// negated mirrors LengthGuardNarrowings/MapPresenceNarrowings: pass
// false to read the condition's HELD (true) side, true to read its
// REFUTED (false) side — `x === 0` refuted is the same fact as
// holding `x !== 0`, read at the leaf's own negated polarity exactly
// as the two sibling functions already do.
func ZeroExclusionNarrowings(
	held func(name string) (abstractdomain.AbstractValue, bool),
	condition *ast.Node,
	negated bool,
) []ZeroExclusionNarrowing {
	var rows []ZeroExclusionNarrowing

	guardRow := func(binding string) {
		heldValue, ok := held(binding)
		if !ok {
			return
		}
		switch heldValue.Kind {
		case abstractdomain.KindSet:
			if heldValue.SetKindTag != abstractdomain.SetKindTagNone {
				return
			}
			lo, hi, isInteger := closedIntegerEdges(heldValue.Set)
			if !isInteger {
				return
			}
			loAtZero := lo != nil && *lo == 0
			hiAtZero := hi != nil && *hi == 0
			// both edges at zero is the singleton {0} -- excluding it empties
			// the window entirely, which this function does not attempt to
			// spell (emptiness is a different claim than a narrower window);
			// straddling on both sides (neither edge at zero, lo < 0 < hi, or
			// an unbounded ray on the far side) has no single-window puncture
			// either -- only exactly one edge at zero narrows
			if loAtZero == hiAtZero {
				return
			}
			var replaceLo, replaceHi *float64
			if loAtZero {
				v := *lo + 1
				replaceLo = &v
				if hi != nil && v > *hi {
					return // the retreat would empty the window
				}
			} else {
				v := *hi - 1
				replaceHi = &v
				if lo != nil && v < *lo {
					return
				}
			}
			tightened := heldValue
			tightened.Set = retreatedWindow(heldValue.Set, replaceLo, replaceHi)
			rows = append(rows, ZeroExclusionNarrowing{Binding: binding, Known: tightened})
		case abstractdomain.KindValues:
			if heldValue.KindTag != abstractdomain.PrimitiveNumber {
				return
			}
			hasZero := false
			for _, v := range heldValue.Values {
				if v == 0 {
					hasZero = true
					break
				}
			}
			if !hasZero {
				return
			}
			kept := make([]float64, 0, len(heldValue.Values))
			for _, v := range heldValue.Values {
				if v != 0 {
					kept = append(kept, v)
				}
			}
			tightened := heldValue
			tightened.Values = kept
			rows = append(rows, ZeroExclusionNarrowing{Binding: binding, Known: tightened})
		}
	}

	readLeaf := func(e *ast.Node, leafNegated bool) {
		if !ast.IsBinaryExpression(e) {
			return
		}
		bin := e.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		isNotEquals := kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken
		isEquals := kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken
		if !isNotEquals && !isEquals {
			return
		}
		// a HELD (non-negated) `x !== 0` proves exclusion directly; a
		// REFUTED `x === 0` (leafNegated true — the exit-guard shape,
		// `if (x === 0) return; …after: x !== 0`) proves the same fact
		// through the opposite operator. The other two combinations —
		// a held `x === 0` or a refuted `x !== 0` — both prove the
		// OPPOSITE (x could be 0) and exclude nothing.
		holds := (isNotEquals && !leafNegated) || (isEquals && leafNegated)
		if !holds {
			return
		}
		var binding string
		if ast.IsIdentifier(bin.Left) && ast.IsNumericLiteral(bin.Right) && numericLiteralValue(bin.Right) == 0 {
			binding = bin.Left.Text()
		} else if ast.IsIdentifier(bin.Right) && ast.IsNumericLiteral(bin.Left) && numericLiteralValue(bin.Left) == 0 {
			binding = bin.Right.Text()
		} else {
			return
		}
		guardRow(binding)
	}

	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}
