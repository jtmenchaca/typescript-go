// CompareKnown's null/undefined flavored rows, pinned against the
// AbsentMark split: KindUndef means exactly-undefined, KindNull exactly-
// null, and a KindPossiblyUndefined wrapper still admits both flavors at
// once. Every row cites its ECMA-262 clause (tmp/ecma262/spec.html) —
// IsStrictlyEqual (sec-isstrictlyequal), IsLooselyEqual
// (sec-islooselyequal), SameValueNonNumber (sec-samevaluenonnumber).

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// wantTrue/wantFalse read a decided CompareKnown boolean answer; wantUnknown
// pins that the row stays undecided (silence.Residue(), KindUnknown).

func wantTrue(t *testing.T, got abstractdomain.AbstractValue, label string) {
	t.Helper()
	if got.Kind != abstractdomain.KindValues || got.KindTag != abstractdomain.PrimitiveBoolean || len(got.Values) != 1 {
		t.Fatalf("%s = %+v, want a decided boolean", label, got)
	}
	if got.Values[0] != 1 {
		t.Errorf("%s = false, want true", label)
	}
}

func wantFalse(t *testing.T, got abstractdomain.AbstractValue, label string) {
	t.Helper()
	if got.Kind != abstractdomain.KindValues || got.KindTag != abstractdomain.PrimitiveBoolean || len(got.Values) != 1 {
		t.Fatalf("%s = %+v, want a decided boolean", label, got)
	}
	if got.Values[0] != 0 {
		t.Errorf("%s = true, want false", label)
	}
}

func wantUnknown(t *testing.T, got abstractdomain.AbstractValue, label string) {
	t.Helper()
	if got.Kind != abstractdomain.KindUnknown {
		t.Errorf("%s = %+v, want KindUnknown (undecided)", label, got)
	}
}

// TestCompareKnownStrictUndefUndef pins sec-isstrictlyequal: SameType is
// true (both Undefined), so IsStrictlyEqual defers to SameValueNonNumber,
// whose step 2 ("If x is either undefined or null, return true") decides
// === true.
func TestCompareKnownStrictUndefUndef(t *testing.T) {
	ctx := &FlowContext{}
	wantTrue(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Undef, abstractdomain.Undef), "Undef === Undef")
	wantFalse(t, CompareKnown(ctx, CompareNe, true, abstractdomain.Undef, abstractdomain.Undef), "Undef !== Undef")
}

// TestCompareKnownStrictNullNull mirrors the Undef/Undef row for Null/Null
// — same clause, same SameType/SameValueNonNumber path.
func TestCompareKnownStrictNullNull(t *testing.T) {
	ctx := &FlowContext{}
	wantTrue(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Null, abstractdomain.Null), "Null === Null")
	wantFalse(t, CompareKnown(ctx, CompareNe, true, abstractdomain.Null, abstractdomain.Null), "Null !== Null")
}

// TestCompareKnownStrictUndefNull pins sec-isstrictlyequal step 1: SameType
// is false (Undefined vs Null are different Types), so IsStrictlyEqual
// returns false before any value is read — checked both operand orders.
func TestCompareKnownStrictUndefNull(t *testing.T) {
	ctx := &FlowContext{}
	wantFalse(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Undef, abstractdomain.Null), "Undef === Null")
	wantTrue(t, CompareKnown(ctx, CompareNe, true, abstractdomain.Undef, abstractdomain.Null), "Undef !== Null")
	wantFalse(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Null, abstractdomain.Undef), "Null === Undef")
	wantTrue(t, CompareKnown(ctx, CompareNe, true, abstractdomain.Null, abstractdomain.Undef), "Null !== Undef")
}

// TestCompareKnownLooseUndefNull pins sec-islooselyequal steps 2-3: "If x
// is null and y is undefined, return true" / "If x is undefined and y is
// null, return true" — both orders loose-equal despite differing Types.
func TestCompareKnownLooseUndefNull(t *testing.T) {
	ctx := &FlowContext{}
	wantTrue(t, CompareKnown(ctx, CompareEq, false, abstractdomain.Undef, abstractdomain.Null), "Undef == Null")
	wantTrue(t, CompareKnown(ctx, CompareEq, false, abstractdomain.Null, abstractdomain.Undef), "Null == Undef")
	wantFalse(t, CompareKnown(ctx, CompareNe, false, abstractdomain.Undef, abstractdomain.Null), "Undef != Null")
	wantFalse(t, CompareKnown(ctx, CompareNe, false, abstractdomain.Null, abstractdomain.Undef), "Null != Undef")
}

// TestCompareKnownLooseSameFlavor pins sec-islooselyequal step 1: SameType
// true defers to IsStrictlyEqual, so undefined == undefined and null ==
// null decide true under == exactly as they do under ===.
func TestCompareKnownLooseSameFlavor(t *testing.T) {
	ctx := &FlowContext{}
	wantTrue(t, CompareKnown(ctx, CompareEq, false, abstractdomain.Undef, abstractdomain.Undef), "Undef == Undef")
	wantTrue(t, CompareKnown(ctx, CompareEq, false, abstractdomain.Null, abstractdomain.Null), "Null == Null")
}

// TestCompareKnownWrapperVsUndefStaysUnknown pins the conflation this unit
// leaves alone on purpose: a KindPossiblyUndefined wrapper admits both
// null and undefined (PossiblyUndefined's own doc comment), so a strict or
// loose test against an exact KindUndef must not decide through it.
func TestCompareKnownWrapperVsUndefStaysUnknown(t *testing.T) {
	ctx := &FlowContext{}
	wrapper := abstractdomain.PossiblyUndefined(abstractdomain.Null, "", false, false)
	wantUnknown(t, CompareKnown(ctx, CompareEq, true, wrapper, abstractdomain.Undef), "wrapper === Undef")
	wantUnknown(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Undef, wrapper), "Undef === wrapper")
	wantUnknown(t, CompareKnown(ctx, CompareEq, false, wrapper, abstractdomain.Undef), "wrapper == Undef")
	wantUnknown(t, CompareKnown(ctx, CompareNe, true, wrapper, abstractdomain.Null), "wrapper !== Null")
}

// TestCompareKnownExactAbsentVsNonAbsentStrictFalse extends the existing
// non-absent-value row (already decided for KindUndef before this unit) to
// KindNull too: sec-isstrictlyequal step 1, SameType(absent, non-absent)
// is false, so === is false / !== is true regardless of strict/loose.
func TestCompareKnownExactAbsentVsNonAbsentStrictFalse(t *testing.T) {
	ctx := &FlowContext{}
	num := abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wantFalse(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Null, num), "Null === 40")
	wantTrue(t, CompareKnown(ctx, CompareNe, true, abstractdomain.Null, num), "Null !== 40")
	wantFalse(t, CompareKnown(ctx, CompareEq, true, num, abstractdomain.Null), "40 === Null")
}
