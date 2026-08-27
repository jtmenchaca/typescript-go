// from evaluation/arithmetic_transfer.ts
//
// The operator transfers POSE QUESTIONS: the float image of every
// JavaScript operation is computed inside the kernel wasm
// (set_functions/transfer.lean) on its own verified rounder
// (ground/rounding.lean — roundDN_isDN / roundUP_isUP /
// roundNE_isNearest, the ECMA-262 rounding executably), from the
// operands' sets read into certified enclosures
// (set_functions/enclosure.lean, RefinedSet.encl_sound). This file's
// job is reading the program: which operands are numeric, which sets
// they carry, and what the kernel's answer means to the walk. The
// operator soundness ledger (transfers/transfer_correct.lean) is
// COMPLETE: every operator behind these questions carries its
// machine-checked soundness theorem.
//
// A transfer reads its operand NUMERICALLY: a string- or
// array-sorted word here would be the cross-sort reread, so it
// degrades to unknown — the honest alert downstream, never a number
// that the runtime's coercion contradicts. A NaN operand answers the
// spec's pinned NaN before any question is posed.

package walk

import (
	"math"
	"sort"
	"sync"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// NumericOperand is numericOperand in the TS source: a transfer reads
// its operand NUMERICALLY: a string- or array-sorted word here would
// be the cross-sort reread (a "a" carried through `any` computing as
// 97), so it degrades to unknown — the honest alert downstream, never
// a number that the runtime's coercion contradicts.
//
// An absent operand (KindUndef/KindNull) IS numeric under ToNumber
// (sec-tonumber steps 3-4: undefined pins exactly NaN, null pins
// exactly +0) — reading it through unchanged left it a Kind neither
// binaryImage's NaN check nor SetOfKnownForTransfer's KindValues/
// KindSet/KindVariable arms recognize (IsNumericKind's own `Kind !=
// KindValues` test passes it through as "numeric" without converting
// it), so a cast-carried `undefined + 1` posed no question the
// transfer could answer and landed undetermined instead of the
// spec's pinned {NaN}. Converting here, before IsNumericKind's own
// gate, makes both absent kinds read as the exact number ToNumber
// specifies — the same exact-value shape every other pinned operand
// already carries into binaryImage.
func NumericOperand(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if k.Kind == abstractdomain.KindUndef {
		return abstractdomain.NaNValue
	}
	if k.Kind == abstractdomain.KindNull {
		return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	}
	if abstractdomain.IsNumericKind(k) {
		return k
	}
	return silence.Residue()
}

// NumericOperator is the "+" | "-" | "*" | "/" | "%" union.
type NumericOperator string

const (
	OpAdd NumericOperator = "+"
	OpSub NumericOperator = "-"
	OpMul NumericOperator = "*"
	OpDiv NumericOperator = "/"
	OpRem NumericOperator = "%"
)

// transferKernel is transferKernel in the TS source: the kernel the
// transfers pose their questions through — set by the checker's run()
// before any walk. Without one (a unit test that skipped setup),
// every transfer answers unknown — degraded loudly by the alerts it
// causes, never wrong.
//
// Already goroutine-safe: one process loads one kernel dylib handle,
// so this is a SINGLETON, not per-check state (see PORT.md's
// parallel-sweep audit) — the RWMutex here already lets concurrent
// checks read it while a write is serialized. SetTransferKernel is
// called once per check today (service's runRefinements, before the
// walk starts); the parallel-sweep coordinator hoists that call
// before goroutines spawn, after which every read here (RLock) sees
// the same already-loaded kernel and no further write races it.
var (
	transferKernelMu sync.RWMutex
	transferKernel   *kernelbridge.RefinedTSKernel
)

// SetTransferKernel is setTransferKernel in the TS source.
func SetTransferKernel(kernel *kernelbridge.RefinedTSKernel) {
	transferKernelMu.Lock()
	transferKernel = kernel
	transferKernelMu.Unlock()
}

func currentTransferKernel() *kernelbridge.RefinedTSKernel {
	transferKernelMu.RLock()
	defer transferKernelMu.RUnlock()
	return transferKernel
}

// SetOfKnownForTransfer is setOfKnown in the TS source (arithmetic_transfer.ts):
// a AbstractValue as the set a question carries; (RefinedSet{}, false)
// where it holds no poseable set. Multi-value words stay unposed — the
// old rules read only singletons, and parity is the contract.
//
// Named distinctly from abstractdomain.SetOfKnown (a different
// function of the same TS name in a different TS file — lattice_operations.ts's
// setOfKnown reads exact tuples as concatenations; this one reads only
// SINGLETON values, per this file's own comment).
func SetOfKnownForTransfer(k abstractdomain.AbstractValue) (refinementsets.RefinedSet, bool) {
	if k.Kind == abstractdomain.KindValues {
		if len(k.Values) == 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[0]})), true
		}
		return refinementsets.RefinedSet{}, false
	}
	if k.Kind == abstractdomain.KindSet {
		return k.Set, true
	}
	if k.Kind == abstractdomain.KindVariable && k.StarDepth == 0 {
		return k.Bound, true
	}
	return refinementsets.RefinedSet{}, false
}

// KnownOfAnswer is knownOfAnswer in the TS source: a kernel answer
// back to the walk's knowledge.
func KnownOfAnswer(answer kernelbridge.TransferAnswer) abstractdomain.AbstractValue {
	switch answer.Kind {
	case kernelbridge.TransferAnswerNaN:
		return abstractdomain.NaNValue
	case kernelbridge.TransferAnswerUnknown:
		return silence.Residue()
	case kernelbridge.TransferAnswerValues:
		if len(answer.Values) == 1 {
			return abstractdomain.KnownValues([]float64{answer.Values[0]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		}
		return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf(answer.Values)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	case kernelbridge.TransferAnswerSet:
		return abstractdomain.KnownSet(answer.Set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	default:
		return silence.Residue()
	}
}

// opWire: an immutable op-wire table, written once at load and never
// after — safe under concurrent reads with no lock (see PORT.md's
// parallel-sweep audit).
var opWire = map[NumericOperator]kernelbridge.TransferQuestionOp{
	OpAdd: kernelbridge.TransferOpAdd,
	OpSub: kernelbridge.TransferOpSub,
	OpMul: kernelbridge.TransferOpMul,
	OpDiv: kernelbridge.TransferOpDiv,
	OpRem: kernelbridge.TransferOpRem,
}

// OrUnknown is orUnknown in the TS source: a refused question is
// SILENCE, never a verdict — and never the end of the check. The
// kernel declines a transfer whose cost bound exceeds its budget
// (boundary/cost.ts pre-gates the same measure), and an operand
// nothing is known about is exactly what an unreadable operand
// already produces here: unknown, which alerts downstream rather
// than asserting anything.
//
// Every route from this file into the kernel goes through this. It
// was a throw before, and it ended the WHOLE FILE's check on the
// first real module whose arithmetic outgrew the budget — two of 629
// files in one library, each losing all of its diagnostics over one
// expression.
//
// Stack exhaustion is not a refusal and is not swallowed — the TS
// `if (declined instanceof RangeError) throw declined` rethrows a
// stack overflow; a Go goroutine stack overflow crashes the process
// unrecoverably (there is no catchable RangeError twin), so this
// recover only ever catches an intentional kernel-question panic
// (kernelbridge's questions panic on a refused question, per
// PORT.md) — the same set of cases the TS catch narrows to in
// practice.
func OrUnknown(ask func() abstractdomain.AbstractValue) (result abstractdomain.AbstractValue) {
	defer func() {
		if recover() != nil {
			result = silence.Residue()
		}
	}()
	return ask()
}

// reachesValue is the TS source's reachesValue: whether the operand's
// set can reach one of the named values — the kernel's membership
// answer on set knowledge, the word list on exact knowledge, (false,
// false) (undecided) anywhere else or on a refusal.
func reachesValue(raw abstractdomain.AbstractValue, values []float64) (reaches bool, decided bool) {
	if raw.Kind == abstractdomain.KindValues && raw.KindTag == abstractdomain.PrimitiveNumber {
		for _, v := range values {
			for _, held := range raw.Values {
				if held == v {
					return true, true
				}
			}
		}
		return false, true
	}
	if raw.Kind == abstractdomain.KindSet && raw.SetKindTag == abstractdomain.SetKindTagNone {
		kernel := currentTransferKernel()
		if kernel == nil {
			return false, false
		}
		root := raw.Set
		if len(root.Forms) == 0 {
			root = refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1)))
		}
		sawTrue := false
		for _, v := range values {
			verdict, ok := callValidateChain(kernel, kernelbridge.Chain{
				Root: root,
				Ops:  []kernelbridge.ChainOp{{Op: kernelbridge.ChainOpMemberQ, Tuple: []float64{v}}},
			})
			if !ok {
				return false, false
			}
			if verdict.Kind != kernelbridge.ValidateChainAnswer {
				return false, false
			}
			if verdict.Answer {
				sawTrue = true
			}
		}
		return sawTrue, true
	}
	return false, false
}

func callValidateChain(kernel *kernelbridge.RefinedTSKernel, chain kernelbridge.Chain) (result kernelbridge.ValidateChainResult, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return kernel.ValidateChain(chain), true
}

func reachesEither(raw abstractdomain.AbstractValue, first, second float64) (bool, bool) {
	return reachesValue(raw, []float64{first, second})
}

// TransferBinary is transferBinary in the TS source: the float image
// of a binary operation — the kernel's answer. Exact on exact values
// (the very float the runtime returns, the spec's NaN cells pinned);
// certified endpoint bounds on ranges.
func TransferBinary(op NumericOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// THE ARITHMETIC TRANSFER DISPATCH SEAM of the derivation trace: one
	// span per operator image, naming the operator and what each operand
	// held. The transfer is handed VALUES rather than a node, so the span
	// inherits the range of the evaluateExpression span that called it —
	// the sub-expression this transfer is the image of. Off is one atomic
	// load inside Active().
	if derivation.Active() {
		return transferBinaryRecorded(op, rawA, rawB)
	}
	return transferBinaryOf(op, rawA, rawB)
}

// transferBinaryRecorded opens the transfer's span around
// transferBinaryOf and states its answer or its decline.
func transferBinaryRecorded(op NumericOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	span := derivation.Begin("arithmeticTransfer", string(op), "")
	image := transferBinaryOf(op, rawA, rawB)
	if image.Kind == abstractdomain.KindUnknown {
		gate := image.ResidueReason
		if gate == "" {
			gate = "the operator has no image for these operands"
		}
		span.Decline(gate, "", spellValue(rawA)+" "+string(op)+" "+spellValue(rawB))
	} else {
		span.Answer(spellValue(image))
	}
	span.End()
	return image
}

// transferBinaryOf is TransferBinary's body once the derivation span is
// handled.
func transferBinaryOf(op NumericOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// NaN poisons every arithmetic operator (NaN-in, NaN-out), so a
	// possibly-NaN operand makes a possibly-NaN result whose real
	// half transfers as usual
	if rawA.Kind == abstractdomain.KindPossiblyNaN || rawB.Kind == abstractdomain.KindPossiblyNaN {
		innerA := rawA
		if rawA.Kind == abstractdomain.KindPossiblyNaN {
			innerA = *rawA.Inner
		}
		innerB := rawB
		if rawB.Kind == abstractdomain.KindPossiblyNaN {
			innerB = *rawB.Inner
		}
		inner := TransferBinary(op, innerA, innerB)
		if inner.Kind == abstractdomain.KindUnknown {
			return silence.Residue()
		}
		wrapped := inner
		if inner.Kind != abstractdomain.KindPossiblyNaN {
			wrapped = abstractdomain.PossiblyNaN(inner)
		}
		return abstractdomain.AtTrustLevel(wrapped, abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, rawA, rawB))
	}
	image := OrUnknown(func() abstractdomain.AbstractValue { return binaryImage(op, rawA, rawB) })
	// `x % d` with an exactly known finite nonzero divisor: WHATEVER
	// the dividend — unknown, unbounded, even infinite — the result is
	// a number strictly inside the open divisor window, or NaN
	// (sec-numeric-types-number-remainder: NaN and ±∞ numerators give
	// NaN, ±0 gives itself, and a finite remainder's magnitude sits
	// under the divisor's)
	if image.Kind == abstractdomain.KindUnknown && op == OpRem {
		var d float64
		hasD := false
		if rawB.Kind == abstractdomain.KindValues && rawB.KindTag == abstractdomain.PrimitiveNumber && len(rawB.Values) == 1 {
			d, hasD = rawB.Values[0], true
		}
		if hasD && isFinite(d) && d != 0 {
			window := refinementsets.MakeRefinedSet(
				refinementsets.Above(-math.Abs(d)),
				refinementsets.Below(math.Abs(d)),
			)
			return abstractdomain.AtTrustLevel(
				abstractdomain.PossiblyNaN(abstractdomain.KnownSet(window, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)),
				abstractdomain.DerivedTrustLevel(abstractdomain.TrustSpec, rawB),
			)
		}
	}
	// `a / b` the kernel declined: the spec's NaN cells are 0/0 and
	// ±∞/±∞ (sec-numeric-types-number-divide steps 2-6) — every other
	// pair divides to a real or ±∞. When one side provably cannot be
	// zero AND one side provably cannot be infinite, no NaN cell is
	// reachable, so the quotient is the sort's whole ground WITHOUT
	// the NaN arm — what lets a `b !== 0` guard plus finiteness prove
	// a stated no-NaN position. Membership is the kernel's own answer,
	// never the range's approximation.
	if image.Kind == abstractdomain.KindUnknown && op == OpDiv {
		zeroA, zeroAOk := reachesValue(rawA, []float64{0})
		zeroB, zeroBOk := reachesValue(rawB, []float64{0})
		infA, infAOk := reachesEither(rawA, math.Inf(1), math.Inf(-1))
		infB, infBOk := reachesEither(rawB, math.Inf(1), math.Inf(-1))
		zeroAFalse := zeroAOk && !zeroA
		zeroBFalse := zeroBOk && !zeroB
		infAFalse := infAOk && !infA
		infBFalse := infBOk && !infB
		if (zeroAFalse || zeroBFalse) && (infAFalse || infBFalse) {
			// the whole ground, spelled with a poseable form so downstream
			// subset questions stay askable
			return abstractdomain.AtTrustLevel(
				abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone),
				abstractdomain.DerivedTrustLevel(abstractdomain.TrustSpec, rawA, rawB),
			)
		}
	}
	// both operands HELD number-sorted knowledge the transfer could
	// not tighten: the result is still a number or NaN — the sort's
	// whole ground, held rather than dropped, so the position can say
	// it states nothing beyond the type. Spelled as the explicit
	// AtLeast(-Infinity) ray (never the bare zero-value RefinedSet{}) —
	// a formless set fails OnOneTupleLayer downstream
	// (nan_wrapper.go's checkPossiblyNaNSubset gate,
	// refinement_forms.go's OnOneTupleLayer), so a checked position
	// could never even POSE the subset question against it. Graded
	// TrustSpec, not TrustProved, for the same reason the OpDiv arm
	// above is: TrustProved collapses to an EMPTY Grade field
	// (KnownSet's own TrustProved-means-unset rule), and an ungraded
	// "adds nothing beyond the ground" wrapper is exactly the shape
	// AfterReaders' own never-examined seed wears
	// (silence/after_readers.go's typereading.NumberWithNaN)  — so
	// nan_wrapper.go's CheckPossiblyNaN treats an ungraded wrapper as
	// that seed and declines to 7002 rather than posing the subset
	// question. A graded wrapper is unambiguously a DERIVED claim
	// (this operator's own image), so it takes the subset path and
	// can refute a sink too narrow for the whole ground.
	if image.Kind == abstractdomain.KindUnknown {
		held := func(k abstractdomain.AbstractValue) bool {
			return (k.Kind == abstractdomain.KindSet && k.SetKindTag == abstractdomain.SetKindTagNone) ||
				(k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveNumber) ||
				k.Kind == abstractdomain.KindNaN ||
				(k.Kind == abstractdomain.KindVariable && k.StarDepth == 0)
		}
		if held(rawA) && held(rawB) {
			return abstractdomain.AtTrustLevel(
				abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)),
				abstractdomain.DerivedTrustLevel(abstractdomain.TrustSpec, rawA, rawB),
			)
		}
	}
	return image
}

// TransferOrderedSub is transferOrderedSub in the TS source: `a − b`
// when the walk carries a guard's strict order b < a: the plain
// difference window, floored at 1 by the kernel when both operands
// are integer-marked and at 0 always (proved: transferSubOrd_sound).
// The guard passing at runtime is the premise — a comparison is true
// only of non-NaN numbers, and the ledger records only never-written
// bindings, so the compared values are the ones subtracted. Nil where
// the operands hold no poseable number-sorted sets.
func TransferOrderedSub(rawA, rawB abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	return orderedQuestion(rawA, rawB, func(a, b refinementsets.RefinedSet) kernelbridge.TransferQuestion {
		return kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpSubOrd, A: a, B: b}
	})
}

// TransferOrderedSubGap is transferOrderedSubGap in the TS source:
// `a − b` under the vouched difference bound b ≤ a + c: the kernel
// floors the difference at −c (proved: transferSubOrdGap_sound).
func TransferOrderedSubGap(rawA, rawB abstractdomain.AbstractValue, c float64) *abstractdomain.AbstractValue {
	if !isFinite(c) {
		return nil
	}
	return orderedQuestion(rawA, rawB, func(a, b refinementsets.RefinedSet) kernelbridge.TransferQuestion {
		return kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpSubOrdGap, A: a, B: b, C: c}
	})
}

func orderedQuestion(rawA, rawB abstractdomain.AbstractValue, pose func(a, b refinementsets.RefinedSet) kernelbridge.TransferQuestion) *abstractdomain.AbstractValue {
	kernel := currentTransferKernel()
	if kernel == nil {
		return nil
	}
	// the callers' premise is a HELD order row: the comparison passed
	// at runtime, and a comparison is true only of non-NaN, PRESENT
	// numbers — so the maybe wrappers shed here, and an operand the
	// walk holds nothing about poses as the whole real line (spelled
	// as the vacuous ray, which the enclosure reading accepts). The
	// proved answer is then the row's own floor: max > min gives
	// max − min at least zero even when neither side carries a window.
	var undressed func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue
	undressed = func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		if k.Kind == abstractdomain.KindPossiblyNaN || k.Kind == abstractdomain.KindPossiblyUndefined {
			return undressed(*k.Inner)
		}
		return k
	}
	a := NumericOperand(undressed(rawA))
	b := NumericOperand(undressed(rawB))
	if a.Kind == abstractdomain.KindNaN || b.Kind == abstractdomain.KindNaN {
		return nil
	}
	universal := func(k abstractdomain.AbstractValue) (refinementsets.RefinedSet, bool) {
		if k.Kind == abstractdomain.KindUnknown {
			return refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), true
		}
		return refinementsets.RefinedSet{}, false
	}
	// a formless set is the same whole line, spelled in the one way
	// the wire's enclosure reading refuses — respell it as the ray
	poseable := func(s refinementsets.RefinedSet) refinementsets.RefinedSet {
		if len(s.Forms) == 0 {
			return refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1)))
		}
		return s
	}
	A, aOk := SetOfKnownForTransfer(a)
	if !aOk {
		A, aOk = universal(a)
	}
	B, bOk := SetOfKnownForTransfer(b)
	if !bOk {
		B, bOk = universal(b)
	}
	if !aOk || !bOk {
		return nil
	}
	answer := OrUnknown(func() abstractdomain.AbstractValue {
		return abstractdomain.AtTrustLevel(
			KnownOfAnswer(kernel.Transfer(pose(poseable(A), poseable(B)))),
			abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, a, b),
		)
	})
	if answer.Kind == abstractdomain.KindUnknown {
		return nil
	}
	return &answer
}

// tightenStrictBounds rewrites each top-level strict ray of a posed
// operand set to the closed ray at its neighboring float: `above a`
// becomes `>= nextafter(a, +Inf)` and `below b` becomes
// `<= nextafter(b, -Inf)`. A runtime double strictly past a float
// bound sits at or past that bound's format neighbor — the kernel's
// own proved fact (pred_bounds_strict, transfers/strict_precision.lean)
// and the same step its floor/ceil/round/trunc transfer takes
// (tightLo/tightHi, set_functions/transfer.lean). The kernel's binary
// transfers (transferAdd, transferMul, ...) read only CLOSED corners
// and drop the strict flags, so without this step Math.random()'s
// [0, 1) — "greater than or equal to +0𝔽 but strictly less than 1𝔽",
// vendored spec sec-math.random — multiplies as if 1 were reachable,
// and Math.floor(Math.random() * 121) reads as reaching 121. Infinite
// bounds stay as they are, mirroring the kernel's tightLo/tightHi,
// which step only finite ones.
func tightenStrictBounds(set refinementsets.RefinedSet) refinementsets.RefinedSet {
	changed := false
	forms := make([]refinementsets.Refinement, len(set.Forms))
	for i, f := range set.Forms {
		switch {
		case f.Form == refinementsets.FormAbove && isFinite(f.A):
			forms[i] = refinementsets.AtLeast(math.Nextafter(f.A, math.Inf(1)))
			changed = true
		case f.Form == refinementsets.FormBelow && isFinite(f.A):
			forms[i] = refinementsets.AtMost(math.Nextafter(f.A, math.Inf(-1)))
			changed = true
		default:
			forms[i] = f
		}
	}
	if !changed {
		return set
	}
	return refinementsets.MakeRefinedSet(forms...)
}

func binaryImage(op NumericOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	a := NumericOperand(rawA)
	b := NumericOperand(rawB)
	grade := abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, a, b)
	// a NaN operand: every exact operator is specified to return NaN
	// (Number::add/subtract/multiply/divide/remainder, step 1)
	if a.Kind == abstractdomain.KindNaN || b.Kind == abstractdomain.KindNaN {
		return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
	}
	// EXACT VALUES on both sides: the image is the UNION of the pairs'
	// own images, and every pair is a singleton×singleton question the
	// kernel already answers exactly on its verified rounder — the same
	// question the conformance battery poses directly. So a scattered
	// set (`{0, 1} * 200`), which the kernel's corner-reading transfer
	// declines as ONE question, is asked one pair at a time and the
	// answers union: exact `{0, 200}` out, every signed-zero and NaN
	// cell spelled exactly as the kernel's own transfer spells it,
	// because it IS the kernel's own transfer. A pair the kernel still
	// declines drops the whole arm to the ordinary route below.
	if a.Kind == abstractdomain.KindValues && a.KindTag == abstractdomain.PrimitiveNumber &&
		b.Kind == abstractdomain.KindValues && b.KindTag == abstractdomain.PrimitiveNumber &&
		len(a.Values) > 0 && len(b.Values) > 0 && (len(a.Values) > 1 || len(b.Values) > 1) {
		if exact := pointwisePairImages(op, a, b, grade); exact != nil {
			return *exact
		}
	}
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	A, aOk := SetOfKnownForTransfer(a)
	B, bOk := SetOfKnownForTransfer(b)
	if !aOk || !bOk {
		return silence.Residue()
	}
	A = tightenStrictBounds(A)
	B = tightenStrictBounds(B)
	return abstractdomain.AtTrustLevel(
		KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: opWire[op], A: A, B: B})),
		grade,
	)
}

// pointwisePairImages is the exact-values transfer: the union of the
// kernel's own singleton×singleton images over every operand pair,
// spelled exactly as the kernel spells them (signed zeros and NaN
// cells included — no host arithmetic reimplements a cell). A NaN
// image rides out as the possibly-NaN wrapper around the real
// results; all-NaN answers the NaN value itself. Nil when the kernel
// is absent or any pair's question comes back unreadable — the caller
// then falls to the whole-set route, which answers or declines as it
// always did.
func pointwisePairImages(op NumericOperator, a, b abstractdomain.AbstractValue,
	grade abstractdomain.TrustLevel) *abstractdomain.AbstractValue {
	kernel := currentTransferKernel()
	if kernel == nil {
		return nil
	}
	values := make([]float64, 0, len(a.Values)*len(b.Values))
	// keyed by bits, not value: -0 and 0 compare equal as floats, and
	// the kernel's spelling distinguishes them
	seen := map[uint64]struct{}{}
	sawNaN := false
	keep := func(z float64) {
		if math.IsNaN(z) {
			sawNaN = true
			return
		}
		bits := math.Float64bits(z)
		if _, held := seen[bits]; held {
			return
		}
		seen[bits] = struct{}{}
		values = append(values, z)
	}
	for _, x := range a.Values {
		for _, y := range b.Values {
			A, aOk := SetOfKnownForTransfer(abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
			B, bOk := SetOfKnownForTransfer(abstractdomain.KnownValues([]float64{y}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
			if !aOk || !bOk {
				return nil
			}
			answer := OrUnknown(func() abstractdomain.AbstractValue {
				return KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: opWire[op], A: A, B: B}))
			})
			switch answer.Kind {
			case abstractdomain.KindNaN:
				sawNaN = true
			case abstractdomain.KindValues:
				for _, z := range answer.Values {
					keep(z)
				}
			case abstractdomain.KindPossiblyNaN:
				sawNaN = true
				if answer.Inner == nil || answer.Inner.Kind != abstractdomain.KindValues {
					return nil
				}
				for _, z := range answer.Inner.Values {
					keep(z)
				}
			default:
				return nil
			}
		}
	}
	if len(values) == 0 {
		out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
		return &out
	}
	sort.Float64s(values)
	exact := abstractdomain.KnownValues(values, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	if sawNaN {
		exact = abstractdomain.PossiblyNaN(exact)
	}
	out := abstractdomain.AtTrustLevel(exact, grade)
	return &out
}

// TransferNegate is transferNegate in the TS source.
func TransferNegate(rawA abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	return OrUnknown(func() abstractdomain.AbstractValue { return negateImage(rawA) })
}

func negateImage(rawA abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	a := NumericOperand(rawA)
	if a.Kind == abstractdomain.KindNaN {
		return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, abstractdomain.TrustLevelOf(a)) // −NaN is NaN
	}
	// an exact singleton negates to the exact float −v: this is plain
	// arithmetic on a value the walk already holds outright, not a
	// range/set question — so it answers without the kernel, the same
	// way a literal `2.5` reads without one. Every other numeric shape
	// (a range, a variable-bound set) still needs the kernel's own
	// enclosure-sound negation question below.
	if a.Kind == abstractdomain.KindValues && a.KindTag == abstractdomain.PrimitiveNumber && len(a.Values) == 1 {
		return abstractdomain.AtTrustLevel(
			abstractdomain.KnownValues([]float64{-a.Values[0]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
			abstractdomain.TrustLevelOf(a),
		)
	}
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	A, ok := SetOfKnownForTransfer(a)
	if !ok {
		return silence.Residue()
	}
	return abstractdomain.AtTrustLevel(
		KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpNeg, A: A})),
		abstractdomain.TrustLevelOf(a),
	)
}
