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

// wantBoolGround reads a decided-but-either-way boolean answer: the
// {0, 1} set boolGround returns for a real comparison whose specific
// truth value the operands don't pin — never KindUnknown (a decline)
// and never a single exact value (over-claiming precision the
// operands don't carry).
func wantBoolGround(t *testing.T, got abstractdomain.AbstractValue, label string) {
	t.Helper()
	if got.Kind != abstractdomain.KindValues || got.KindTag != abstractdomain.PrimitiveBoolean || len(got.Values) != 2 {
		t.Fatalf("%s = %+v, want the boolean ground {0, 1}", label, got)
	}
}

// nonNegativeIntegerSetOfCompareKnown builds the KindSet shape
// `.length`/`.size` read on an object-star or unread-collection receiver
// (evaluate_property_access.go's own KindObjectStar `.length` row, and this
// unit's new mapOrSetReceiver `.size` row): integers at least 0, no upper
// bound. Named distinctly from ir_callback_array_statements.go's own
// nonNegativeIntegerSet (package walk, returns a bare refinementsets.RefinedSet
// for a LoopEffect.Set) — this one wraps the same set as a KindSet
// AbstractValue, the shape CompareKnown's operands need.
func nonNegativeIntegerSetOfCompareKnown() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
}

// TestCompareKnownSetAgainstNumberBothArmsPossibleIsBoolGround pins
// this unit's fix: `arr.length > 0` over length ∈ [0, ∞) admits BOTH
// outcomes (an empty array reads false, a nonempty one true) — the
// shape matches compareSetAgainstNumber's set-vs-number row, but
// neither arm is impossible, so the comparison determines the
// boolean ground rather than declining (A7.guard.lt's own
// `nonEmpty` row, this unit's fix).
func TestCompareKnownSetAgainstNumberBothArmsPossibleIsBoolGround(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	length := nonNegativeIntegerSetOfCompareKnown()
	zero := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wantBoolGround(t, CompareKnown(ctx, CompareGt, true, length, zero), "length > 0")
}

// TestCompareKnownSetAgainstNumberImpossibleArmStaysExact pins that
// the fix above does not touch the ALREADY-EXACT case:
// `.length` ∈ [0, ∞) compared `< 0` is false on every run (the true
// arm's meet with the set is empty), so this still decides a single
// boolean, never the ground.
func TestCompareKnownSetAgainstNumberImpossibleArmStaysExact(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	length := nonNegativeIntegerSetOfCompareKnown()
	zero := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wantFalse(t, CompareKnown(ctx, CompareLt, true, length, zero), "length < 0")
}

// TestCompareKnownPossiblyNaNUnwrapsAndJoins pins this unit's other
// fix: Date.getTime() on an unknown receiver reads KindPossiblyNaN
// (date_models.go's readDateMethods, the spec's [[DateValue]] window
// riding NaN beside it for an unvalidated date) — CompareKnown now
// unwraps that wrapper, compares the inner value as usual, and joins
// with the NaN corner (NaN fails every comparison, passes every
// disequality) instead of falling to the "not a plain known value"
// residue A6.guard.eq/lt/ne were hitting before this fix.
//
// Both === and !== decide in two arms that DISAGREE here: the operand
// is {1000} ∪ NaN, so 1000 === 1000 is true while NaN === 1000 is
// false (=== fails whenever either side is NaN), and symmetrically
// 1000 !== 1000 is false while NaN !== 1000 is true (NaN passes every
// disequality). Neither comparison is pinned to one outcome by the
// operands — both are the honest boolean ground, not a decline and
// not a single exact value.
func TestCompareKnownPossiblyNaNUnwrapsAndJoins(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	// an exact epoch-ms instant, possibly NaN (the getTime() shape)
	instant := abstractdomain.PossiblyNaN(abstractdomain.KnownValues([]float64{1000}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	sameInstant := abstractdomain.KnownValues([]float64{1000}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wantBoolGround(t, CompareKnown(ctx, CompareEq, true, instant, sameInstant), "possiblyNaN(1000) === 1000")
	wantBoolGround(t, CompareKnown(ctx, CompareNe, true, instant, sameInstant), "possiblyNaN(1000) !== 1000")
}

// TestCompareKnownReferenceKindIdentityIsBoolGround pins this unit's
// identity-comparison row: two INDEPENDENT object-star receivers (an
// array of RECORDS the tuple layer cannot hold — recipes.go's
// StarOfElementAtLeast routes an object-shaped element here, e.g. a
// bare `Point[]` parameter) compare by REFERENCE, which no alias
// graph here proves or refutes — but the comparison is still a real
// === that returns true or false on every run, so the determination
// is the boolean ground, never the "arrays compare by reference"
// decline this row replaces.
func TestCompareKnownReferenceKindIdentityIsBoolGround(t *testing.T) {
	ctx := &FlowContext{}
	element := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
	star, ok := abstractdomain.KnownObjectStar(element, abstractdomain.TrustSpec)
	if !ok {
		t.Fatalf("KnownObjectStar(object) = _, false, want a built object-star")
	}
	wantBoolGround(t, CompareKnown(ctx, CompareEq, true, star, star), "a === b (two object-star parameters)")
	wantBoolGround(t, CompareKnown(ctx, CompareNe, true, star, star), "a !== b (two object-star parameters)")
}

// TestCompareKnownMapIdentityIsBoolGround pins the SAME row for the
// shape A8.guard.eq/A8.guard.ne actually carry: a bare `m: Map<string,
// number>` parameter reads as an INCOMPLETE KindObject (Map's own
// prototype method keys — Map itself is a default-lib interface no
// declared-type reader recognizes by name, confirmed by tracing
// entry_env.go's InitialStateOfPlainParameter through typereading's
// syntax/host adapters), which is a reference-compared sort exactly
// like a plain object literal's type would be.
func TestCompareKnownMapIdentityIsBoolGround(t *testing.T) {
	ctx := &FlowContext{}
	m := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
	n := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
	wantBoolGround(t, CompareKnown(ctx, CompareEq, true, m, n), "m === n (two Map parameters)")
	wantBoolGround(t, CompareKnown(ctx, CompareNe, true, m, n), "m !== n (two Map parameters)")
}

// TestCompareKnownUnboundedStringPairIsBoolGround pins the string/
// boolean same-sort row: two unbounded `string` parameters (E1.sink's
// `secret === guess`, both read as KindSet over refinementsets.
// Strings) compare as a real === on every run, but neither side is
// pinned to one exact word — the boolean ground, not the "not a
// plain known value" decline this row replaces.
func TestCompareKnownUnboundedStringPairIsBoolGround(t *testing.T) {
	ctx := &FlowContext{}
	secret := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	guess := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	wantBoolGround(t, CompareKnown(ctx, CompareEq, true, secret, guess), "secret === guess")
}

// TestA5_sink_dead_ANumericSetAgainstUndefinedIsDecided pins the
// KindSet arm of the absent-side row (A5.sink.dead's own shape).
//
// sec-isstrictlyequal step 1: SameType(Undefined, Number) is false, so
// === is false and !== true. A refined set is a set of NUMBERS or
// STRINGS and carries no undefined and no null member at all — absence
// rides the PossiblyUndefined WRAPPER, never the set inside it — so the
// verdict is exact on every run, exactly as it is for a KindValues side.
// Before this arm the row declined, and `y === undefined` on a plainly
// numeric y decided nothing.
func TestA5_sink_dead_ANumericSetAgainstUndefinedIsDecided(t *testing.T) {
	ctx := &FlowContext{}
	age := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(150)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
	wantFalse(t, CompareKnown(ctx, CompareEq, true, age, abstractdomain.Undef), "age === undefined")
	wantTrue(t, CompareKnown(ctx, CompareNe, true, age, abstractdomain.Undef), "age !== undefined")
	// the absent side on the LEFT reads the same way
	wantFalse(t, CompareKnown(ctx, CompareEq, true, abstractdomain.Undef, age), "undefined === age")
	// and null decides identically — SameType(Null, Number) is false too
	wantFalse(t, CompareKnown(ctx, CompareEq, true, age, abstractdomain.Null), "age === null")
}

// TestA5_sink_dead_AStringSetAgainstUndefinedIsDecided is the string
// twin of the row above — the same clause, the same reasoning, the
// other sort a refined set can be grounded on.
func TestA5_sink_dead_AStringSetAgainstUndefinedIsDecided(t *testing.T) {
	ctx := &FlowContext{}
	text := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	wantFalse(t, CompareKnown(ctx, CompareEq, true, text, abstractdomain.Undef), "text === undefined")
	wantTrue(t, CompareKnown(ctx, CompareNe, true, text, abstractdomain.Undef), "text !== undefined")
}

// TestA5_sink_dead_AMaybeWrapperAgainstUndefinedStaysUndecided is the
// GUARD on the arm above: the wrapper is the one value that genuinely
// still admits absence, so it must fall through the exact-kind gate
// undecided rather than being read through to the set inside it. A
// wrapper deciding "not absent" here would be the unsound direction.
func TestA5_sink_dead_AMaybeWrapperAgainstUndefinedStaysUndecided(t *testing.T) {
	ctx := &FlowContext{}
	age := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(150)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
	maybe := abstractdomain.PossiblyUndefined(age, "", false, false)
	wantUnknown(t, CompareKnown(ctx, CompareEq, true, maybe, abstractdomain.Undef), "maybe === undefined")
}
