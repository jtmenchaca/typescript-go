// from conformance/lattice_conformance.test.ts
//
// The checker's own join, held to the kernel's proved one: every
// scalar knowledge pair joins the same way through joinKnown and
// through the kernel's join_state entry (exact by `join_exact`,
// set_functions/known_state.lean). The sets are compared by mutual
// subset — the two routes may formatAt one set two ways.

package conformance

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// emptySet is EMPTY in the TS source.
var emptySet = refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))

// stateOfKnown is stateOfKnown in the TS source: the kernel state a
// scalar knowledge state denotes; (zero value, false) where the
// knowledge leaves the scalar world (objects, sorts, sequences).
func stateOfKnown(k abstractdomain.AbstractValue) (kernelbridge.KnownStateWire, bool) {
	switch k.Kind {
	case abstractdomain.KindUnknown:
		return kernelbridge.KnownStateWire{Top: true}, true
	case abstractdomain.KindUndef:
		// mirrors production's walk/kernel_delegation.go StateOfKnown: the
		// producer audit closed — every exact KindUndef site means
		// exactly the undefined value, never null — so the wire claims
		// Undef alone.
		return kernelbridge.KnownStateWire{Set: emptySet, Undef: true, Null: false, Nan: false}, true
	case abstractdomain.KindNaN:
		return kernelbridge.KnownStateWire{Set: emptySet, Undef: false, Null: false, Nan: true}, true
	case abstractdomain.KindPossiblyUndefined:
		inner, ok := stateOfKnown(*k.Inner)
		if !ok {
			return kernelbridge.KnownStateWire{}, false
		}
		// a wrapper around ⊤ denotes ⊤ — top already admits absence
		if inner.Top {
			return kernelbridge.KnownStateWire{Top: true}, true
		}
		// mirrors production's by-flavor send: an UndefOnly/NullOnly
		// wrapper's own absent side admits exactly the one flag its
		// flavor names; AbsentFlavorConflated (the zero value) keeps
		// sending both, unchanged. The bare-KindUndef arm above no
		// longer sends both flags on its own, so a row whose join
		// produces a FLAVORED wrapper (e.g. abstractdomain.Undef joined
		// with a plain value, AbsentFlavorUndefOnly) must read its own
		// flavor here too, or the checker's re-encoded join would claim
		// an admission the kernel's own JoinState never granted.
		switch k.AbsentSide {
		case abstractdomain.AbsentFlavorUndefOnly:
			inner.Undef = true
		case abstractdomain.AbsentFlavorNullOnly:
			inner.Null = true
		default:
			inner.Undef = true
			inner.Null = true
		}
		return inner, true
	case abstractdomain.KindPossiblyNaN:
		inner, ok := stateOfKnown(*k.Inner)
		if !ok {
			return kernelbridge.KnownStateWire{}, false
		}
		if inner.Top {
			return kernelbridge.KnownStateWire{Top: true}, true
		}
		inner.Nan = true
		return inner, true
	case abstractdomain.KindValues, abstractdomain.KindSet:
		if k.Kind == abstractdomain.KindValues && k.KindTag != abstractdomain.PrimitiveNumber {
			return kernelbridge.KnownStateWire{}, false
		}
		if k.Kind == abstractdomain.KindSet && k.SetKindTag != abstractdomain.SetKindTagNone {
			return kernelbridge.KnownStateWire{}, false
		}
		set, ok := abstractdomain.SetOfKnown(k)
		if !ok {
			return kernelbridge.KnownStateWire{}, false
		}
		return kernelbridge.KnownStateWire{Set: set, Undef: false, Null: false, Nan: false}, true
	default:
		return kernelbridge.KnownStateWire{}, false
	}
}

// sameState is sameState in the TS source: the two states admit the
// same outcomes: both top, or equal flags and mutually contained sets.
func sameState(kernel *kernelbridge.RefinedTSKernel, a, b kernelbridge.KnownStateWire) bool {
	if a.Top || b.Top {
		return a.Top && b.Top
	}
	return a.Undef == b.Undef && a.Null == b.Null && a.Nan == b.Nan &&
		kernel.ScalarSubset(a.Set, b.Set) && kernel.ScalarSubset(b.Set, a.Set)
}

func TestJoinKnownAgreesWithTheKernelsProvedJoinOnEveryScalarPair(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	rows := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtMost(10), refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.Undef,
		abstractdomain.NaNValue,
		abstractdomain.PossiblyUndefined(abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), "", false, false),
		abstractdomain.PossiblyUndefined(abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(5)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone), "", false, false),
		abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)),
		abstractdomain.PossiblyUndefined(abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)), "", false, false),
		abstractdomain.Unknown,
	}

	compared := 0
	for _, a := range rows {
		for _, b := range rows {
			sa, saOK := stateOfKnown(a)
			sb, sbOK := stateOfKnown(b)
			if !saOK {
				t.Fatalf("stateOfKnown(a) = false, want true")
			}
			if !sbOK {
				t.Fatalf("stateOfKnown(b) = false, want true")
			}
			joined, joinedOK := stateOfKnown(abstractdomain.JoinKnown(a, b))
			if !joinedOK {
				t.Fatalf("stateOfKnown(joinKnown(a, b)) = false, want true")
			}
			kernelJoined := kernel.JoinState(sa, sb)
			if !sameState(kernel, joined, kernelJoined) {
				t.Errorf("sameState(joined, kernelJoined) = false, want true (a=%+v, b=%+v)", a, b)
			}
			compared++
		}
	}
	if compared != len(rows)*len(rows) {
		t.Errorf("compared = %d, want %d", compared, len(rows)*len(rows))
	}
}
