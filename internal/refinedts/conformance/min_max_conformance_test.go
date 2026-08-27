// OPERATION 4 of the differential harness (see
// differential_harness_test.go for the three-verdict frame).
//
// walk.TransferMathCall's ALL-EXACT min/max fast path — the audit's W1
// row, "the all-exact min/max ... fast path" — held to the
// binary64.min / binary64.max pairwise fold the SAME function performs
// one branch over. This is a real fast path, unlike operation 3's
// arithmetic: math_transfer.go:329-358 detects that every argument is a
// single exact value and folds them with Go's math.Min / math.Max,
// posing NO kernel question at all. The very next branch (:360-389)
// folds the same arguments through kernel.Transfer pairwise. Two
// routes, one function, and only one of them is proved.
//
// WHY THIS IS THE SHARPEST ROW IN THE HARNESS. ECMA's min/max are not
// the ordinary ones. sec-math.max step 4.b: "If _number_ is +0𝔽 and
// _highest_ is -0𝔽, set _highest_ to +0𝔽", with the note "the
// comparison ... is done using the IsLessThan algorithm except that +0𝔽
// is considered to be larger than -0𝔽." sec-math.min step 4.b is the
// mirror. A local fold that used a plain `<` would answer -0 for
// Math.max(-0, +0) — IsLessThan says neither is less than the other, so
// `highest` would never be updated off the first argument — and the
// zeros are indistinguishable under `==`, so no ordinary equality
// assertion would catch it. That is exactly the drift this operation
// exists to detect, and sameFloatBits (differential_harness_test.go)
// compares the SIGN BIT so it cannot be hidden.
//
// The rows below therefore walk every ordered pair over ±0, ±∞, the
// 2^53 boundaries and the plain values, in BOTH argument orders (the
// spec's fold is order-sensitive at the zeros by construction — the
// asymmetry between step 4.b and step 4.c is the whole -0 rule).
//
// THE DETERMINATION-GAP LEDGER.
//
//   gap-1  A multi-value exact word breaks `allExact`
//          (math_transfer.go:331) and falls to the fold, where
//          SetOfKnownForTransfer refuses it too (:99-104) and the whole
//          call answers silence.Residue(). Math.min(x, 1) with x known
//          to be 1-or-2 determines nothing, while the kernel answers
//          the same question sharply when the operand is spelled as a
//          OneOf set. Asserted as a decline below with the kernel's
//          answer recorded beside it.
//   gap-2  CLOSED. Math.min() / Math.max() with ZERO arguments answer
//          the spec's own initial values: max() is -∞ (step 3, the
//          initial `highest`, with no argument to change it) and min()
//          is +∞. Both exact, answered ahead of the kernel gate in
//          math_transfer.go — no kernel question is even needed.
//
// THE SCRUTINY CLASS — flagged loudly.
//
//   scrutiny-1  The all-exact arm answers at grade
//          MinTrustLevel(operandTrustLevel, TrustSpec) — "the host row"
//          per its own comment — meaning it claims a SPEC
//          transcription, not a proved answer, while the fold one
//          branch over claims TrustProved through the kernel. The
//          adapter is claiming with a weaker warrant what the proof
//          would have given it outright. Not an unsoundness; a
//          trust-grade downgrade taken for speed, and named here
//          because the migration retires it.
//   scrutiny-2  A BOOLEAN-tagged exact word passes the allExact gate
//          (:332 excludes only PrimitiveString and PrimitiveArray), so
//          Math.max(true, 0) folds `1` and `0` locally. The kernel is
//          never asked, and the boolean-to-number reading is the
//          adapter's own (ToNumber, sec-tonumber). Comparable in
//          principle — 1 IS what ToNumber(true) gives — but the fold
//          reaches the kernel's vocabulary only after that reading is
//          already done, so it is the ADAPTER's coercion under test,
//          not the kernel's. Asserted below against the spec cell and
//          named as a transcription, not a kernel agreement.

package conformance

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// minMaxOps pairs the Math name with the kernel's fold op.
var minMaxOps = []struct {
	name string
	wire kernelbridge.TransferQuestionOp
}{
	{"min", kernelbridge.TransferOpMin},
	{"max", kernelbridge.TransferOpMax},
}

// TestTheAllExactMinMaxFastPathAgreesWithTheKernelFold is the
// AGREEMENT half, and the harness's sharpest row: every ordered pair
// over the edge values, folded locally by the fast path and folded
// pairwise by the kernel, must answer the same float INCLUDING its
// sign bit. Drift is a failure.
func TestTheAllExactMinMaxFastPathAgreesWithTheKernelFold(t *testing.T) {
	kernel := arithmeticKernel(t)

	compared := 0
	for _, op := range minMaxOps {
		for _, a := range edgeValues {
			for _, b := range edgeValues {
				// the FAST PATH: two single exact values, so allExact holds
				adapter, matched := walk.TransferMathCall(op.name,
					[]abstractdomain.AbstractValue{exactValue(a), exactValue(b)})
				if !matched {
					t.Fatalf("TransferMathCall(%q) is not transferred at all", op.name)
				}
				// the KERNEL FOLD: the same two operands as singleton sets
				answer := kernel.Transfer(kernelbridge.TransferQuestion{
					Op: op.wire, A: exactSet(a), B: exactSet(b),
				})
				compared++

				adapterValue, adapterPinned := answeredExactly(adapter)
				kernelValue, kernelPinned := kernelExactly(answer)
				if !kernelPinned {
					if answer.Kind == kernelbridge.TransferAnswerUnknown {
						// the kernel declined on a pair the fast path answered:
						// SCRUTINY, and loud — the adapter is claiming what the
						// proof does not
						t.Errorf("Math.%s(%v, %v): the fast path answered %v, the kernel DECLINED — SCRUTINY: an unproved claim",
							op.name, a, b, adapterValue)
					}
					continue
				}
				if !adapterPinned {
					t.Errorf("Math.%s(%v, %v): the kernel pinned %v, the fast path answered a %v it does not pin",
						op.name, a, b, kernelValue, adapter.Kind)
					continue
				}
				if !sameFloatBits(adapterValue, kernelValue) {
					t.Errorf("Math.%s(%v, %v): fast path = %v (signbit %v), kernel = %v (signbit %v) — the two routes disagree",
						op.name, a, b,
						adapterValue, math.Signbit(adapterValue),
						kernelValue, math.Signbit(kernelValue))
				}
			}
		}
	}
	if want := len(minMaxOps) * len(edgeValues) * len(edgeValues); compared != want {
		t.Errorf("compared = %d, want %d", compared, want)
	}
}

// TestTheMinMaxZeroOrderingFollowsTheSpecInBothArgumentOrders pins the
// -0 rule by itself, against the spec cell, in both orders — the one
// place a plain `<` fold and the spec part company, and the reason
// sameFloatBits compares sign bits.
//
// sec-math.max step 4.b + its note: +0𝔽 is considered larger than -0𝔽.
// sec-math.min step 4.b: the mirror.
func TestTheMinMaxZeroOrderingFollowsTheSpecInBothArgumentOrders(t *testing.T) {
	arithmeticKernel(t)

	rows := []struct {
		name     string
		op       string
		a, b     float64
		want     float64
		wantSign bool // the sign bit the spec pins
	}{
		{"max(-0, +0)", "max", negZero(), 0, 0, false},
		{"max(+0, -0)", "max", 0, negZero(), 0, false},
		{"min(-0, +0)", "min", negZero(), 0, negZero(), true},
		{"min(+0, -0)", "min", 0, negZero(), negZero(), true},
		{"max(-0, -0)", "max", negZero(), negZero(), negZero(), true},
		{"min(+0, +0)", "min", 0, 0, 0, false},
	}

	for _, row := range rows {
		answer, matched := walk.TransferMathCall(row.op,
			[]abstractdomain.AbstractValue{exactValue(row.a), exactValue(row.b)})
		if !matched {
			t.Fatalf("TransferMathCall(%q) is not transferred at all", row.op)
		}
		value, pinned := answeredExactly(answer)
		if !pinned {
			t.Errorf("Math.%s: the fast path did not pin an answer (%v)", row.name, answer.Kind)
			continue
		}
		if !sameFloatBits(value, row.want) {
			t.Errorf("Math.%s = %v (signbit %v), want %v (signbit %v) — sec-math.%s step 4.b",
				row.name, value, math.Signbit(value), row.want, row.wantSign, row.op)
		}
	}
}

// TestTheMinMaxFastPathAndTheKernelFoldAgreeOverThreeArguments extends
// the comparison past the pairwise case: the fast path folds all three
// at once, the kernel folds them two at a time, and the two must land
// on the same float. Three-argument folds are where an order-sensitive
// -0 rule compounds.
func TestTheMinMaxFastPathAndTheKernelFoldAgreeOverThreeArguments(t *testing.T) {
	kernel := arithmeticKernel(t)

	triples := [][]float64{
		{negZero(), 0, 1},
		{1, negZero(), 0},
		{0, 1, negZero()},
		{negZero(), negZero(), 0},
		{math.Inf(-1), negZero(), 0},
		{math.Inf(1), 9007199254740992, 9007199254740991},
		{-1, negZero(), 1},
		{9007199254740991, 9007199254740992, -9007199254740992},
	}

	for _, op := range minMaxOps {
		for _, triple := range triples {
			args := make([]abstractdomain.AbstractValue, len(triple))
			for i, v := range triple {
				args[i] = exactValue(v)
			}
			adapter, matched := walk.TransferMathCall(op.name, args)
			if !matched {
				t.Fatalf("TransferMathCall(%q) is not transferred at all", op.name)
			}
			// the kernel's own pairwise fold, left to right
			held := exactSet(triple[0])
			folded := true
			for i := 1; i < len(triple); i++ {
				answer := kernel.Transfer(kernelbridge.TransferQuestion{
					Op: op.wire, A: held, B: exactSet(triple[i]),
				})
				next, ok := setOfTransferAnswer(answer)
				if !ok {
					folded = false
					break
				}
				held = next
			}
			if !folded {
				t.Errorf("Math.%s%v: the kernel fold declined mid-way while the fast path answered — SCRUTINY",
					op.name, triple)
				continue
			}
			adapterValue, adapterPinned := answeredExactly(adapter)
			if !adapterPinned {
				t.Errorf("Math.%s%v: the fast path did not pin an answer (%v)",
					op.name, triple, adapter.Kind)
				continue
			}
			if !kernel.Member(held, []float64{adapterValue}) {
				t.Errorf("Math.%s%v: the fast path answered %v, which the kernel's fold does not admit — the two routes disagree",
					op.name, triple, adapterValue)
			}
		}
	}
}

// TestMinMaxReturnsTheSpecsNaNOnANaNArgument holds the NaN gate. The
// adapter answers NaN before the fast path is reached
// (math_transfer.go:313-317, the NaN-in gate every transferred built-in
// passes through); the spec pins the same at step 4.a of both
// algorithms. Asserted against the SPEC — the kernel's set vocabulary
// cannot hold NaN, so there is no kernel question here.
func TestMinMaxReturnsTheSpecsNaNOnANaNArgument(t *testing.T) {
	arithmeticKernel(t)

	for _, op := range minMaxOps {
		for _, other := range []float64{0, negZero(), 1, math.Inf(1), math.Inf(-1)} {
			left, matched := walk.TransferMathCall(op.name,
				[]abstractdomain.AbstractValue{abstractdomain.NaNValue, exactValue(other)})
			if !matched {
				t.Fatalf("TransferMathCall(%q) is not transferred at all", op.name)
			}
			if !isNaNAnswer(left) {
				t.Errorf("Math.%s(NaN, %v) = %v, want the pinned NaN (sec-math.%s step 4.a)",
					op.name, other, left.Kind, op.name)
			}
			right, _ := walk.TransferMathCall(op.name,
				[]abstractdomain.AbstractValue{exactValue(other), abstractdomain.NaNValue})
			if !isNaNAnswer(right) {
				t.Errorf("Math.%s(%v, NaN) = %v, want the pinned NaN (sec-math.%s step 4.a)",
					op.name, other, right.Kind, op.name)
			}
		}
	}
}

// TestMinMaxDeclinesOnAMultiValueWordAndOnNoArguments is the
// DETERMINATION-GAP half: gap-1 asserted as a decline, gap-2 asserted
// as the spec's exact answer now that it is closed. A row that changes
// its answer fails here and forces the ledger current.
func TestMinMaxDeclinesOnAMultiValueWordAndOnNoArguments(t *testing.T) {
	kernel := arithmeticKernel(t)

	// gap-1: a multi-value word breaks allExact AND the fold
	twoValues := abstractdomain.KnownValues([]float64{1, 2},
		abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	for _, op := range minMaxOps {
		answer, matched := walk.TransferMathCall(op.name,
			[]abstractdomain.AbstractValue{twoValues, exactValue(1)})
		if !matched {
			t.Fatalf("TransferMathCall(%q) is not transferred at all", op.name)
		}
		if !declined(answer) {
			t.Errorf("gap-1: Math.%s now determines something for a multi-value word (%v) — the determination gap closed and this file's ledger is stale",
				op.name, answer.Kind)
		}
		// the size of the gap: the same operands as a SET answer sharply
		sharp := kernel.Transfer(kernelbridge.TransferQuestion{
			Op: op.wire,
			A:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})),
			B:  exactSet(1),
		})
		if sharp.Kind == kernelbridge.TransferAnswerUnknown {
			t.Errorf("gap-1: the kernel declined the set-spelled Math.%s too — the ledger records a gap that is not there", op.name)
		}
	}

	// gap-2, closed: zero arguments. The spec pins max() = -inf and
	// min() = +inf (step 3 in each, with no argument to change the
	// initial value); the adapter answers those exact values.
	for _, op := range minMaxOps {
		answer, matched := walk.TransferMathCall(op.name, nil)
		if !matched {
			t.Fatalf("TransferMathCall(%q) is not transferred at all", op.name)
		}
		want := math.Inf(-1)
		if op.name == "min" {
			want = math.Inf(1)
		}
		value, pinned := answeredExactly(answer)
		if !pinned {
			t.Errorf("Math.%s() with no arguments answered %v, want the spec's exact %v (sec-math.%s step 3)",
				op.name, answer.Kind, want, op.name)
		} else if !sameFloatBits(value, want) {
			t.Errorf("Math.%s() = %v, want the spec's initial %v (sec-math.%s step 3)",
				op.name, value, want, op.name)
		}
	}
}

// TestTheAllExactArmReadsBooleanWordsAsToNumberDoes is scrutiny-2,
// asserted against the spec cell it transcribes: the allExact gate
// admits a BOOLEAN-tagged word, so Math.max(true, 0) folds locally on
// the adapter's own ToNumber reading (sec-tonumber: true is 1𝔽, false
// is +0𝔽). Named as a transcription, not a kernel agreement — the
// kernel is never asked, because the coercion happens before its
// vocabulary is reached.
func TestTheAllExactArmReadsBooleanWordsAsToNumberDoes(t *testing.T) {
	arithmeticKernel(t)

	boolTrue := abstractdomain.KnownValues([]float64{1},
		abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)
	boolFalse := abstractdomain.KnownValues([]float64{0},
		abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)

	rows := []struct {
		name string
		op   string
		args []abstractdomain.AbstractValue
		want float64
	}{
		{"max(true, 0)", "max", []abstractdomain.AbstractValue{boolTrue, exactValue(0)}, 1},
		{"min(true, 0)", "min", []abstractdomain.AbstractValue{boolTrue, exactValue(0)}, 0},
		{"max(false, -1)", "max", []abstractdomain.AbstractValue{boolFalse, exactValue(-1)}, 0},
		{"min(false, -1)", "min", []abstractdomain.AbstractValue{boolFalse, exactValue(-1)}, -1},
	}

	for _, row := range rows {
		answer, matched := walk.TransferMathCall(row.op, row.args)
		if !matched {
			t.Fatalf("TransferMathCall(%q) is not transferred at all", row.op)
		}
		value, pinned := answeredExactly(answer)
		if !pinned {
			t.Errorf("Math.%s: the fast path did not pin an answer (%v) — SCRUTINY row unverified",
				row.name, answer.Kind)
			continue
		}
		if !sameFloatBits(value, row.want) {
			t.Errorf("Math.%s = %v, want %v (sec-tonumber: true is 1𝔽, false is +0𝔽)",
				row.name, value, row.want)
		}
	}
}
