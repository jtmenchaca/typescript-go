// ReadThroughMaybeReceiver's own absent-flavor claim, pinned against
// sec-optional-chaining-evaluation (specifications/javascript/spec.html): an
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

// TestA5_xfer_chain_APlainKeyReadOffAMaybeReceiverReadsThePresentSide
// pins the arm A5.xfer.chain needs, and its boundary against the
// optional-chain rule above.
//
// sec-property-accessors evaluates `MemberExpression . IdentifierName`
// through EvaluatePropertyAccessWithIdentifierKey, whose step 3 is
// "? RequireObjectCoercible(baseValue)" ahead of the [[Get]], and
// sec-requireobjectcoercible's table throws a TypeError for undefined
// and for null. So on a PLAIN access the absent side never yields a
// value at all — it leaves through the throw — and only the present
// side reaches the property. The read is therefore the present side's,
// with no wrapper: `o!.a` and `(o as {a: number}).a` both determine.
//
// The optional-chain arm keeps its precedence: `o?.a` short-circuits to
// undefined rather than throwing, so it still wears the wrapper —
// TestReadThroughMaybeReceiver_ShortCircuitIsExactlyUndefOnly above is
// that half, and this row must not have moved it.
func TestA5_xfer_chain_APlainKeyReadOffAMaybeReceiverReadsThePresentSide(t *testing.T) {
	seven := abstractdomain.KnownValues([]float64{7}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	present := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "a", Value: seven}},
		nil, true, abstractdomain.TrustProved, false,
	)
	maybe := abstractdomain.PossiblyUndefined(present, "", false, false)

	// the peel's own condition, stated the way object_key_access.go
	// states it: a maybe receiver whose present side is a known object
	// ALREADY CARRYING the named key
	if maybe.Kind != abstractdomain.KindPossiblyUndefined || maybe.Inner == nil {
		t.Fatalf("PossiblyUndefined(object) = %+v, want a wrapper with an Inner", maybe)
	}
	inner := *maybe.Inner
	if inner.Kind != abstractdomain.KindObject {
		t.Fatalf("the wrapper's present side .Kind = %v, want KindObject", inner.Kind)
	}
	idx, carriesKey := objectKeyIndex(inner, "a")
	if !carriesKey {
		t.Fatalf("objectKeyIndex(present side, \"a\") reported no key, want the key the peel reads")
	}
	if got := inner.Keys[idx].Value; got.Kind != abstractdomain.KindValues || len(got.Values) != 1 || got.Values[0] != 7 {
		t.Errorf("the peeled key's value = %+v, want the exact 7 the present side holds", got)
	}

	// the NARROWNESS guard: a present side that does NOT carry the name
	// is left wrapped, so every other reader keeps owning its own case
	// (element_in_bounds.go's proved-in-bounds arm, whose wrapper is a
	// claim about the RESULT rather than the receiver, must survive)
	if _, carriesOther := objectKeyIndex(inner, "b"); carriesOther {
		t.Errorf("objectKeyIndex(present side, \"b\") reported a key, want none — the peel must not fire for a name the object does not carry")
	}
}
