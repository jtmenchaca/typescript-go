// ReadThroughMaybeReceiver's own absent-flavor claim, pinned against
// sec-optional-chaining-evaluation (tmp/ecma262/spec.html): an
// optional chain's short-circuit ("If baseValue is either undefined
// or null, then Return undefined") answers EXACTLY undefined
// regardless of which one the receiver held — the built wrapper's
// AbsentSide must be UndefOnly, never the pre-flavor conflated claim.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestReadThroughMaybeReceiver_ShortCircuitIsExactlyUndefOnly(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	receiver := abstractdomain.PossiblyUndefined(five, "", false, false)
	identityRead := func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue { return inner }
	got := ReadThroughMaybeReceiver(receiver, identityRead)
	if got == nil {
		t.Fatalf("ReadThroughMaybeReceiver(maybe receiver) = nil, want a wrapped result")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("ReadThroughMaybeReceiver(maybe receiver).Kind = %v, want KindPossiblyUndefined", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("ReadThroughMaybeReceiver(maybe receiver).AbsentSide = %v, want AbsentFlavorUndefOnly (the short-circuit is always exactly undefined)", got.AbsentSide)
	}
}

// A receiver whose OWN absent side was already NullOnly (proved
// through typeof x === "object" upstream, say) still short-circuits
// to exactly undefined on the RESULT — the optional chain's own
// short-circuit value does not inherit the receiver's flavor, since
// the spec step returns the literal value undefined outright.
func TestReadThroughMaybeReceiver_ResultFlavorIsUndefOnlyRegardlessOfReceiverFlavor(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	receiver := abstractdomain.PossiblyAbsent(five, abstractdomain.AbsentFlavorNullOnly, "", false, false)
	identityRead := func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue { return inner }
	got := ReadThroughMaybeReceiver(receiver, identityRead)
	if got == nil {
		t.Fatalf("ReadThroughMaybeReceiver(NullOnly receiver) = nil, want a wrapped result")
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("ReadThroughMaybeReceiver(NullOnly receiver).AbsentSide = %v, want AbsentFlavorUndefOnly", got.AbsentSide)
	}
}

func TestReadThroughMaybeReceiver_BareUndefShortCircuitsUnchanged(t *testing.T) {
	identityRead := func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue { return inner }
	got := ReadThroughMaybeReceiver(abstractdomain.Undef, identityRead)
	if got == nil || got.Kind != abstractdomain.KindUndef {
		t.Errorf("ReadThroughMaybeReceiver(Undef) = %+v, want KindUndef", got)
	}
}

func TestReadThroughMaybeReceiver_NeitherAbsentDeclines(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	identityRead := func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue { return inner }
	got := ReadThroughMaybeReceiver(five, identityRead)
	if got != nil {
		t.Errorf("ReadThroughMaybeReceiver(a present-only receiver) = %+v, want nil (the link reads normally)", got)
	}
}
