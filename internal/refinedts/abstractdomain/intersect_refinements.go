// Intersecting refinement forms onto an abstract value — the
// lattice's other tightening, named for the operation so it does not
// read as a twin of the narrowing/ folder. Guard narrowing's
// defining equation lives here: narrowed = known ∩ guard forms.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// exactScalarValues reads a folded form list that is exactly one
// finite scalar list — the one set shape whose canonical spelling is
// KindValues, so an equality guard's narrow answers the same exact
// form a literal does.
func exactScalarValues(forms []refinementsets.Refinement) ([]float64, bool) {
	if len(forms) != 1 || forms[0].Form != refinementsets.FormOneOf || len(forms[0].W) == 0 {
		return nil, false
	}
	return forms[0].W, true
}

// narrowedSet spells a narrow's folded intersection: the canonical
// exact form where the fold landed on one finite scalar list, the
// set otherwise.
func narrowedSet(folded []refinementsets.Refinement, trust TrustLevel) AbstractValue {
	if values, ok := exactScalarValues(folded); ok {
		return KnownValues(append([]float64{}, values...), PrimitiveNumber, trust)
	}
	return KnownSet(refinementsets.MakeRefinedSet(folded...), nil, trust, SetKindTagNone)
}

// NarrowKnown is narrowKnown in the TS source.
func NarrowKnown(k AbstractValue, forms []refinementsets.Refinement) AbstractValue {
	if len(forms) == 0 {
		return k
	}
	// the intersection is POSED folded: each conjunct stacks a ray,
	// and only the tightest per class constrains — same set, far
	// cheaper question
	switch k.Kind {
	case KindValues, KindObject, KindObjectStar, KindList, KindArrayHoles, KindCollection, KindPromise,
		KindDate, KindSymbol, KindHostFunction, KindBigints, KindRegex:
		// a set guard says something about a SCALAR; none of these is one,
		// and the form the guard carries names no position of a sequence.
		// The value stands unchanged rather than absorbing a claim that
		// was never about it.
		return k
	case KindSet:
		combined := append(append([]refinementsets.Refinement{}, k.Set.Forms...), forms...)
		return narrowedSet(refinementsets.FoldRayForms(combined), TrustLevelOf(k))
	case KindVariable:
		// a guard on a T-typed value: what is known is bound ∩ forms
		// (the variable identity does not survive a narrowing)
		if k.StarDepth > 0 {
			return k
		}
		combined := append(append([]refinementsets.Refinement{}, k.Bound.Forms...), forms...)
		return narrowedSet(refinementsets.FoldRayForms(combined), TrustLevelOf(k))
	case KindUndef:
		return k // a set claim on the absent value: a dead branch
	case KindNaN:
		// no comparison or equality holds on NaN: the guarded branch
		// is unreachable, so keeping the fact is sound
		return k
	case KindPossiblyUndefined:
		// a held set-comparison proves the value was PRESENT
		return NarrowKnown(*k.Inner, forms)
	case KindPossiblyNaN:
		// a held set-comparison proves the value is REAL — NaN fails
		// every comparison — so the wrapper strips
		return NarrowKnown(*k.Inner, forms)
	case KindKindUnion:
		// the guard leaves the union standing: which arm the runtime
		// value inhabits is undecided here, and narrowing the wrong
		// arm would claim too much
		return k
	case KindUnknown:
		return narrowedSet(refinementsets.FoldRayForms(forms), TrustProved)
	default:
		return k
	}
}
