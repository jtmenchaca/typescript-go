// CompareKnown's null/undefined flavored rows, pinned against the
// AbsentMark split: KindUndef means exactly-undefined, KindNull exactly-
// null, and a KindPossiblyUndefined wrapper still admits both flavors at
// once. Every row cites its ECMA-262 clause (specifications/javascript/spec.html) —
// IsStrictlyEqual (sec-isstrictlyequal), IsLooselyEqual
// (sec-islooselyequal), SameValueNonNumber (sec-samevaluenonnumber).

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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

// stringOfCompareKnown builds a known string AbstractValue the way the
// walk's own literal evaluation does — codepoints in, PrimitiveString
// tagged.
func stringOfCompareKnown(s string) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues(refinementsets.CodepointsOf(s), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
}

// TestCompareKnownStringOrderAAndAB pins cmp.7's prefix rule: "a" < "ab"
// — equal prefix, the shorter word is less (lexLtB_prefix).
func TestCompareKnownStringOrderAAndAB(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	a, ab := stringOfCompareKnown("a"), stringOfCompareKnown("ab")
	wantTrue(t, CompareKnown(ctx, CompareLt, true, a, ab), `"a" < "ab"`)
	wantFalse(t, CompareKnown(ctx, CompareGt, true, a, ab), `"a" > "ab"`)
	wantTrue(t, CompareKnown(ctx, CompareLe, true, a, ab), `"a" <= "ab"`)
	wantFalse(t, CompareKnown(ctx, CompareGe, true, a, ab), `"a" >= "ab"`)
}

// TestCompareKnownStringOrderBBeforeA pins the first-differing-unit rule:
// "b" < "a" is false (98 > 97).
func TestCompareKnownStringOrderBBeforeA(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	b, a := stringOfCompareKnown("b"), stringOfCompareKnown("a")
	wantFalse(t, CompareKnown(ctx, CompareLt, true, b, a), `"b" < "a"`)
	wantTrue(t, CompareKnown(ctx, CompareGt, true, b, a), `"b" > "a"`)
}

// TestCompareKnownStringOrderEqualWords pins irreflexivity (lexLtB_irrefl):
// "a" < "a" is false, and both non-strict orderings hold at once (a <= a,
// a >= a) — the equal-words corner of the total order.
func TestCompareKnownStringOrderEqualWords(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	a1, a2 := stringOfCompareKnown("a"), stringOfCompareKnown("a")
	wantFalse(t, CompareKnown(ctx, CompareLt, true, a1, a2), `"a" < "a"`)
	wantFalse(t, CompareKnown(ctx, CompareGt, true, a1, a2), `"a" > "a"`)
	wantTrue(t, CompareKnown(ctx, CompareLe, true, a1, a2), `"a" <= "a"`)
	wantTrue(t, CompareKnown(ctx, CompareGe, true, a1, a2), `"a" >= "a"`)
}

// TestCompareKnownStringEqualityPair pins cmp.3's word case riding the
// existing Member ask (eqWords is reserved for the wire's op-name family
// but string equality itself already routes through kernel_member, per
// SetOfKnown — this test pins that equality still decides correctly
// alongside the newly wired ordering rows in the same file).
func TestCompareKnownStringEqualityPair(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	abc1, abc2, abd := stringOfCompareKnown("abc"), stringOfCompareKnown("abc"), stringOfCompareKnown("abd")
	wantTrue(t, CompareKnown(ctx, CompareEq, true, abc1, abc2), `"abc" === "abc"`)
	wantFalse(t, CompareKnown(ctx, CompareNe, true, abc1, abc2), `"abc" !== "abc"`)
	wantFalse(t, CompareKnown(ctx, CompareEq, true, abc1, abd), `"abc" === "abd"`)
	wantTrue(t, CompareKnown(ctx, CompareNe, true, abc1, abd), `"abc" !== "abd"`)
}

// TestCompareKnownStringOrderEmptyOperand pins the empty-string corner
// decided natively (lexLtB [] l = (l ≠ [])), without an ask: "" is the
// least word, so "" < "a" and "" <= "" hold, and "" > "a" fails.
func TestCompareKnownStringOrderEmptyOperand(t *testing.T) {
	ctx := &FlowContext{}
	empty, a := stringOfCompareKnown(""), stringOfCompareKnown("a")
	wantTrue(t, CompareKnown(ctx, CompareLt, true, empty, a), `"" < "a"`)
	wantFalse(t, CompareKnown(ctx, CompareGt, true, empty, a), `"" > "a"`)
	wantTrue(t, CompareKnown(ctx, CompareLe, true, empty, empty), `"" <= ""`)
	wantTrue(t, CompareKnown(ctx, CompareGe, true, empty, empty), `"" >= ""`)
}
