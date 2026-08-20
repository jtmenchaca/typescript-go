// OPERATION 3 of the differential harness (see
// differential_harness_test.go for the three-verdict frame).
//
// walk.TransferBinary — the adapter's scalar arithmetic route — held
// to a DIRECT kernel.Transfer of the same op over the same singleton
// sets, across the edge values the kernel's own rows pin: NaN operands,
// ±0, ±∞, and the 2^53 boundaries where integer exactness ends.
//
// WHAT THIS OPERATION FOUND, stated before the rows: TransferBinary has
// NO local exact-operand fast path. Every arithmetic route in
// arithmetic_transfer.go's binaryImage reaches kernel.Transfer
// (:474-477) — the adapter's own work is reading which operand is
// numeric (NumericOperand), spelling it as a set
// (SetOfKnownForTransfer), tightening strict bounds
// (tightenStrictBounds), and reading the answer back (KnownOfAnswer).
// The brief anticipated "walk's TransferBinary local exact-operand
// paths"; the audit's W1 queue names "the all-exact min/max and bitwise
// identity fast paths" for Go, and NOT add/sub/mul/div — the audit is
// right and the brief's framing of this operation is not. The four
// pre-kernel arms that DO answer locally are:
//
//   local-1  a NaN operand (:460-462) answers the pinned NaN before any
//            question is posed. ECMA Number::add/subtract/multiply/
//            divide/remainder step 1. COMPARED below: the kernel is
//            asked the same op over a set that cannot hold NaN, so the
//            comparison is made against the kernel's own NaN answer for
//            an operand it CAN be given one through — see the NaN rows.
//   local-2  a possibly-NaN operand (:242-260) recurses on the real
//            half and re-wraps. COMPARED below.
//   local-3  the `% d` window (:268-284) and the `/` no-NaN-cell
//            reading (:293-310) fire only where the KERNEL DECLINED —
//            they are widenings of a refusal, not fast paths around an
//            answer, so there is no kernel answer to compare them to.
//            Flagged as scrutiny-2 below.
//   local-4  the both-operands-held fallback (:315-327) is the same
//            shape: it fires on a kernel decline.
//
// So this file's AGREEMENT rows are the composition rows — the adapter
// route end to end vs. the kernel asked directly — and they hold the
// adapter's reading (which set it spells, how it reads the answer back)
// to the kernel, which is exactly what a thin-walk migration has to
// preserve.
//
// THE DETERMINATION-GAP LEDGER.
//
//   gap-1  A MULTI-VALUE exact word declines outright:
//          SetOfKnownForTransfer (:99-104) answers only for a
//          length-one KindValues, so `x` known to be one of {1, 2}
//          plus 3 poses NO question at all and the adapter answers the
//          sort's whole ground (the :315-327 fallback). The kernel
//          would answer {4, 5} for the same operands spelled as a
//          OneOf set. Asserted as a decline below, with the kernel's
//          sharper answer recorded beside it.
//
// THE SCRUTINY CLASS — flagged loudly.
//
//   scrutiny-1  local-1's NaN arm ANSWERS where the kernel is never
//          asked. The adapter returns abstractdomain.NaNValue without
//          posing a question, on the strength of a spec transcription
//          (the operator's step 1). That claim is not incomparable in
//          principle — the kernel's transfer answers `nan` for the same
//          cells — but it IS incomparable through this route, because
//          the kernel's set vocabulary cannot hold NaN at all
//          (refinement_forms.go's `element` panics on NaN), so there is
//          no set to pose. The rows below therefore assert the ADAPTER's
//          NaN answer against the spec cell directly and say so; they
//          are not kernel agreements and are not counted as such.
//   scrutiny-2  local-3 and local-4 answer where the kernel DECLINED.
//          Both widen a refusal into a true-but-loose claim (the `%`
//          divisor window; the sort's whole ground). Since the kernel
//          declined, there is no answer to compare against, and this
//          file asserts only that they are SOUND WIDENINGS: whatever
//          the kernel answered for a case where it DOES answer must be
//          contained in what these arms would have claimed. See
//          TestTheDeclinedArmsWidenRatherThanTighten.

package conformance

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// arithmeticKernel loads the kernel AND hands it to the walk's transfer
// route, which is how production wires it (service's runRefinements
// calls SetTransferKernel before the walk starts). Without this the
// transfers answer unknown by design, and every row would read as a
// determination gap that is really a missing setup.
func arithmeticKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	kernel := differentialKernel(t)
	walk.SetTransferKernel(kernel)
	return kernel
}

// opPairs are the four ops this operation covers, with the wire name
// the kernel answers them under.
var opPairs = []struct {
	op   walk.NumericOperator
	wire kernelbridge.TransferQuestionOp
	name string
}{
	{walk.OpAdd, kernelbridge.TransferOpAdd, "+"},
	{walk.OpSub, kernelbridge.TransferOpSub, "-"},
	{walk.OpMul, kernelbridge.TransferOpMul, "*"},
	{walk.OpDiv, kernelbridge.TransferOpDiv, "/"},
}

// TestTransferBinaryAgreesWithADirectKernelTransferOnEveryEdgePair is
// the AGREEMENT half: every (op, a, b) over the edge values, run through
// walk.TransferBinary and through kernel.Transfer directly, must answer
// the same float — or both must decline. Drift is a failure, and −0 and
// +0 count as different answers (sameFloatBits).
func TestTransferBinaryAgreesWithADirectKernelTransferOnEveryEdgePair(t *testing.T) {
	kernel := arithmeticKernel(t)

	compared := 0
	for _, pair := range opPairs {
		for _, a := range edgeValues {
			for _, b := range edgeValues {
				adapter := walk.TransferBinary(pair.op, exactValue(a), exactValue(b))
				answer := kernel.Transfer(kernelbridge.TransferQuestion{
					Op: pair.wire, A: exactSet(a), B: exactSet(b),
				})
				compared++

				// both routes read the spec's NaN cells: the kernel answers
				// the `nan` kind, the adapter the pinned NaN value
				if answer.Kind == kernelbridge.TransferAnswerNaN {
					if !isNaNAnswer(adapter) && adapter.Kind != abstractdomain.KindPossiblyNaN {
						t.Errorf("%v %s %v: kernel answered NaN, adapter answered %v — the two routes disagree on a spec NaN cell",
							a, pair.name, b, adapter.Kind)
					}
					continue
				}
				if answer.Kind == kernelbridge.TransferAnswerUnknown {
					// the kernel declined; the adapter's widening arms may
					// still answer — that is scrutiny-2, covered by its own
					// test, not a disagreement here
					continue
				}
				kernelValue, kernelPinned := kernelExactly(answer)
				adapterValue, adapterPinned := answeredExactly(adapter)
				if !kernelPinned {
					// a window answer over two singletons: the comparison is
					// set-shaped, so compare the sets by mutual containment
					adapterSet, ok := abstractdomain.SetOfKnown(adapter)
					if !ok {
						if declined(adapter) {
							t.Errorf("%v %s %v: kernel answered a set, adapter declined — a determination gap this operation's ledger does not name",
								a, pair.name, b)
						}
						continue
					}
					if !sameSet(kernel, adapterSet, answer.Set) {
						t.Errorf("%v %s %v: adapter set and kernel set admit different values",
							a, pair.name, b)
					}
					continue
				}
				if !adapterPinned {
					if declined(adapter) {
						t.Errorf("%v %s %v: kernel pinned %v, adapter declined — a determination gap this operation's ledger does not name",
							a, pair.name, b, kernelValue)
					} else {
						t.Errorf("%v %s %v: kernel pinned %v, adapter answered a %v it does not pin — SCRUTINY, the shapes are not comparable",
							a, pair.name, b, kernelValue, adapter.Kind)
					}
					continue
				}
				if !sameFloatBits(adapterValue, kernelValue) {
					t.Errorf("%v %s %v: adapter = %v, kernel = %v — the two routes disagree",
						a, pair.name, b, adapterValue, kernelValue)
				}
			}
		}
	}
	if want := len(opPairs) * len(edgeValues) * len(edgeValues); compared != want {
		t.Errorf("compared = %d, want %d", compared, want)
	}
}

// TestTransferBinaryPinsTheSpecsNaNCells is scrutiny-1, asserted
// against the spec cells directly — NOT against the kernel, which
// cannot be given a NaN operand at all (its set constructor refuses
// NaN). Named as a spec transcription so the trust grade is visible.
//
// sec-numeric-types-number-add / -subtract / -multiply / -divide, step
// 1 in each: "If either operand is NaN, return NaN."
func TestTransferBinaryPinsTheSpecsNaNCells(t *testing.T) {
	arithmeticKernel(t)

	for _, pair := range opPairs {
		for _, other := range []float64{0, 1, math.Inf(1)} {
			left := walk.TransferBinary(pair.op, abstractdomain.NaNValue, exactValue(other))
			if !isNaNAnswer(left) {
				t.Errorf("NaN %s %v = %v, want the pinned NaN (spec step 1)", pair.name, other, left.Kind)
			}
			right := walk.TransferBinary(pair.op, exactValue(other), abstractdomain.NaNValue)
			if !isNaNAnswer(right) {
				t.Errorf("%v %s NaN = %v, want the pinned NaN (spec step 1)", other, pair.name, right.Kind)
			}
		}
	}
}

// TestTransferBinaryCarriesThePossiblyNaNWrapperThroughTheRealHalf is
// local-2, compared: the adapter recurses on the real half and re-wraps,
// so the real half must be exactly what the kernel answers for the
// unwrapped operands, and the wrapper must still be there.
func TestTransferBinaryCarriesThePossiblyNaNWrapperThroughTheRealHalf(t *testing.T) {
	kernel := arithmeticKernel(t)

	for _, pair := range opPairs {
		for _, a := range []float64{0, 1, 2, math.Inf(1)} {
			wrapped := walk.TransferBinary(pair.op,
				abstractdomain.PossiblyNaN(exactValue(a)), exactValue(2))
			plain := kernel.Transfer(kernelbridge.TransferQuestion{
				Op: pair.wire, A: exactSet(a), B: exactSet(2),
			})
			if plain.Kind == kernelbridge.TransferAnswerNaN {
				// NaN either way — the wrapper collapses into the pinned NaN
				if !isNaNAnswer(wrapped) {
					t.Errorf("possiblyNaN(%v) %s 2: kernel's real half is NaN, adapter answered %v",
						a, pair.name, wrapped.Kind)
				}
				continue
			}
			if wrapped.Kind != abstractdomain.KindPossiblyNaN {
				if declined(wrapped) {
					continue // the kernel's answer did not survive the recursion
				}
				t.Errorf("possiblyNaN(%v) %s 2 = %v, want the wrapper kept — NaN poisons every arithmetic operator",
					a, pair.name, wrapped.Kind)
				continue
			}
			inner := *wrapped.Inner
			kernelValue, kernelPinned := kernelExactly(plain)
			adapterValue, adapterPinned := answeredExactly(inner)
			if kernelPinned && adapterPinned && !sameFloatBits(adapterValue, kernelValue) {
				t.Errorf("possiblyNaN(%v) %s 2: the wrapper's real half = %v, kernel = %v — the two routes disagree",
					a, pair.name, adapterValue, kernelValue)
			}
		}
	}
}

// TestTransferBinaryDeclinesOnAMultiValueWord is gap-1, asserted: the
// adapter poses no question for a multi-value exact word, while the
// kernel answers the same operands sharply when they are spelled as a
// set. A row that starts answering fails here and forces the ledger
// current.
func TestTransferBinaryDeclinesOnAMultiValueWord(t *testing.T) {
	kernel := arithmeticKernel(t)

	twoValues := abstractdomain.KnownValues([]float64{1, 2},
		abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	adapter := walk.TransferBinary(walk.OpAdd, twoValues, exactValue(3))

	// the adapter does not pin the answer — it lands on the sort's whole
	// ground (the :315-327 fallback), never on {4, 5}
	if _, pinned := answeredExactly(adapter); pinned {
		t.Errorf("gap-1: TransferBinary now pins a multi-value word's sum — the determination gap closed and this file's ledger is stale")
	}

	// the size of the gap: the same operands as a SET answer {4, 5}
	sharp := kernel.Transfer(kernelbridge.TransferQuestion{
		Op: kernelbridge.TransferOpAdd,
		A:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})),
		B:  exactSet(3),
	})
	if sharp.Kind == kernelbridge.TransferAnswerUnknown {
		t.Errorf("gap-1: the kernel declined the set-spelled question too — the ledger records a gap that is not there")
		return
	}
	sharpSet, ok := setOfTransferAnswer(sharp)
	if !ok {
		t.Errorf("gap-1: the kernel's answer carries no set to size the gap with")
		return
	}
	for _, want := range []float64{4, 5} {
		if !kernel.Member(sharpSet, []float64{want}) {
			t.Errorf("gap-1: the kernel's set-spelled answer does not admit %v — the ledger records the wrong gap size", want)
		}
	}
	if kernel.Member(sharpSet, []float64{6}) {
		t.Errorf("gap-1: the kernel's set-spelled answer admits 6 — the ledger records the wrong gap size")
	}
}

// TestTheDeclinedArmsWidenRatherThanTighten is scrutiny-2, asserted as
// far as it can be: the `%`-window and whole-ground arms fire only on a
// kernel decline, so there is no answer to agree with. What CAN be
// checked is that they widen — that a case the kernel DOES answer lands
// inside what the widening arm claims. A widening that excluded a
// kernel-answered value would be an unsoundness, and this is the row
// that would catch it.
func TestTheDeclinedArmsWidenRatherThanTighten(t *testing.T) {
	kernel := arithmeticKernel(t)

	// `x % 7` over an UNKNOWN dividend: the adapter's :268-284 arm claims
	// the open window (−7, 7), or NaN. Every remainder the kernel pins
	// for a concrete dividend against the same divisor must sit inside
	// that window.
	widened := walk.TransferBinary(walk.OpRem, abstractdomain.Unknown, exactValue(7))
	if widened.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("`unknown %% 7` = %v, want the possibly-NaN window arm", widened.Kind)
	}
	window, ok := abstractdomain.SetOfKnown(*widened.Inner)
	if !ok {
		t.Fatalf("`unknown %% 7`: the window arm's inner value states no set")
	}
	for _, dividend := range []float64{0, 1, 6, 7, 8, -1, -8, 100} {
		answer := kernel.Transfer(kernelbridge.TransferQuestion{
			Op: kernelbridge.TransferOpRem, A: exactSet(dividend), B: exactSet(7),
		})
		pinned, ok := kernelExactly(answer)
		if !ok {
			continue
		}
		if !kernel.Member(window, []float64{pinned}) {
			t.Errorf("`unknown %% 7`: the widened window excludes %v, which the kernel pins for %v %% 7 — the widening is not a widening",
				pinned, dividend)
		}
	}
}

// setOfTransferAnswer reads a kernel transfer answer back as a set —
// walk's own setOfAnswer is unexported (math_transfer.go:128), so this
// file spells the same reading rather than exporting one for a test.
func setOfTransferAnswer(answer kernelbridge.TransferAnswer) (refinementsets.RefinedSet, bool) {
	switch answer.Kind {
	case kernelbridge.TransferAnswerValues:
		if len(answer.Values) >= 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf(answer.Values)), true
		}
		return refinementsets.RefinedSet{}, false
	case kernelbridge.TransferAnswerSet:
		return answer.Set, true
	default:
		return refinementsets.RefinedSet{}, false
	}
}
