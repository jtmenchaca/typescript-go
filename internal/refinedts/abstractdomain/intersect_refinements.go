// Intersecting refinement forms onto an abstract value — the
// lattice's other tightening, named for the operation so it does not
// read as a twin of the narrowing/ folder. Guard narrowing's
// defining equation lives here: narrowed = known ∩ guard forms.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// NarrowKnown is narrowKnown in the TS source.
func NarrowKnown(k AbstractValue, forms []refinementsets.Refinement) AbstractValue {
	if len(forms) == 0 {
		return k
	}
	// the intersection is POSED folded: each conjunct stacks a ray,
	// and only the tightest per class constrains — same set, far
	// cheaper question
	switch k.Kind {
	case KindValues, KindObject, KindList, KindCollection, KindPromise,
		KindDate, KindSymbol, KindHostFunction, KindBigints, KindRegex:
		return k
	case KindSet:
		combined := append(append([]refinementsets.Refinement{}, k.Set.Forms...), forms...)
		return KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.FoldRayForms(combined)...),
			nil,
			TrustLevelOf(k),
			SetKindTagNone,
		)
	case KindVariable:
		// a guard on a T-typed value: what is known is bound ∩ forms
		// (the variable identity does not survive a narrowing)
		if k.StarDepth > 0 {
			return k
		}
		combined := append(append([]refinementsets.Refinement{}, k.Bound.Forms...), forms...)
		return KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.FoldRayForms(combined)...),
			nil,
			TrustLevelOf(k),
			SetKindTagNone,
		)
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
		return KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.FoldRayForms(forms)...),
			nil,
			TrustProved,
			SetKindTagNone,
		)
	default:
		return k
	}
}
