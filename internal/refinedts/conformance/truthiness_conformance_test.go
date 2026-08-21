// OPERATION 1 of the differential harness (see
// differential_harness_test.go for the three-verdict frame).
//
// abstractdomain.Truthiness — the adapter's own ToBoolean verdict —
// held to the kernel's proved truthiness narrowings, NarrowState with
// op "js.truthyNum" (the number sort) and "js.truthyStr" (the string
// sort). The kernel does not answer a VERDICT; it answers the two
// FILTERED SIDES, and the verdict is read off them: a state whose
// whenFalse side admits nothing is TRUE outright, a state whose
// whenTrue side admits nothing is FALSE outright, and a state where
// both sides admit something is undecided. That reading is the
// comparison this file makes.
//
// THE DETERMINATION-GAP LEDGER (slice 9's "Truthiness answers
// (false,false) for KindSet outright — never asks the kernel's truthy
// narrowing; a concrete B row"). The ledger's determining rows are
// CLOSED: abstractdomain.TruthinessDecided (lattice_kernel.go) asks the
// kernel's js.truthyNum / js.truthyStr narrowing wherever the local
// Truthiness declines on a set- or multi-value-shaped operand, and
// reads the verdict off the two filtered sides the way kernelTruthiness
// below does. The rows that used to be asserted DECLINES are asserted
// AGREEMENTS now, per this file's own flip discipline.
//
//   gap-1  KindSet [1, 10]          CLOSED → TRUE
//          — every member is nonzero, so the falsy side is empty.
//   gap-2  KindSet {0}              CLOSED → FALSE
//          — the one member is zero, so the truthy side is empty.
//   gap-3  KindSet [1, ∞)           CLOSED → TRUE
//          — an unbounded window, still wholly nonzero.
//   gap-4  KindSet [-10, -1]        CLOSED → TRUE
//          — negatives are truthy; the sign is not the question.
//   gap-5  KindSet [0, 10]          OPEN, and not a gap: the kernel
//          declines here too (the window holds 0 and nonzero alike), so
//          both routes are honestly undecided. It stays asserted as a
//          decline — it pins that the remaining silence is the
//          QUESTION's and not the adapter's.
//   gap-6  KindValues 2+ numbers    CLOSED → TRUE
//          — Truthiness's KindValues arm answers only for a SINGLE
//          value; a multi-value word declined even when every member
//          agreed. TruthinessDecided sends it as a OneOf set.
//
// Truthiness ITSELF is unchanged: every arm it already decided still
// decides locally, with no question asked. Only the declining arms
// reach the kernel, and only through TruthinessDecided.
//
// THE SCRUTINY CLASS. Truthiness ANSWERS, unconditionally and without
// asking, for every object-ish kind: KindObject, KindList, KindObjectStar,
// KindArrayHoles, KindCollection, KindPromise, KindDate, KindRegex,
// KindSymbol, KindHostFunction — all `return true, true`. Those are
// SCRUTINY rows by the frame's definition (the adapter claims what the
// proof does not), and they are INCOMPARABLE: the kernel's value
// vocabulary stops at scalars and sequences (THIN-WALK-AUDIT.md's
// headline), so there is no NarrowState question that takes an object at
// all — SetOfKnown itself refuses every one of these kinds
// (lattice_operations.go:391-412). They are therefore NOT asserted here.
// The claim they rest on is ECMA sec-toboolean's "every Object is true",
// which is a spec transcription (TRUST.md's D residue), not a kernel
// answer — and it stays unchecked by this gate until the kernel's value
// vocabulary reaches object shapes. Named loudly rather than skipped
// silently.

package conformance

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// scalarEmptyRecovered asks kernel.ScalarEmpty(set) and turns a refusal
// panic (a non-scalar-shaped set) into ok=false, rather than letting it
// crash the test — the same deferred-recover idiom
// differential_harness_test.go's scalarSubsetRecovered/
// seqSubsetRecovered hold every kernel ask in this package to (itself
// mirroring walk/nan_wrapper.go's checkPossiblyNaNSubset and
// abstractdomain/lattice_kernel.go's kernelNoScalarReread).
func scalarEmptyRecovered(kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet) (empty bool, ok bool) {
	defer func() {
		if recover() != nil {
			empty, ok = false, false
		}
	}()
	return kernel.ScalarEmpty(set), true
}

// seqEmptyRecovered is scalarEmptyRecovered's SeqEmpty twin — asks
// kernel.SeqEmpty(set) and turns a refusal panic ("the set is not a
// recognized sequence shape") into ok=false.
func seqEmptyRecovered(kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet) (empty bool, ok bool) {
	defer func() {
		if recover() != nil {
			empty, ok = false, false
		}
	}()
	return kernel.SeqEmpty(set), true
}

// kernelTruthiness reads the kernel's proved verdict for a state under
// one truthiness sort: (value, known), the same three-valued shape
// abstractdomain.Truthiness answers in. A side that admits nothing at
// all decides the verdict outright; two live sides are undecided.
//
// "Admits nothing" is the kernel's own emptiness decider on the side's
// set, AND the side carrying no absent/NaN admission either — a state
// whose set is empty but whose undef flag is up still admits the
// undefined value, which is falsy, so the set alone is not the whole
// question.
//
// The set a narrowed side carries is not always scalar-shaped:
// ScalarEmpty refuses (panics through the bridge) on a sequence/tuple
// spelling, so this tries it under recover first and, on a refusal,
// tries SeqEmpty the same way — the same routing-by-shape and
// recover discipline differential_harness_test.go's sameSet holds
// every kernel ask in this package to. A double refusal means neither
// decider spoke to this set's shape: read as "not dead" (live), the
// same reading a genuinely live side gets — a refusal is never a claim
// that a side is empty.
func kernelTruthiness(
	kernel *kernelbridge.RefinedTSKernel,
	state kernelbridge.KnownStateWire,
	op string,
) (value bool, known bool) {
	whenTrue, whenFalse := kernel.NarrowState(state, op, 0, false)
	dead := func(s kernelbridge.KnownStateWire) bool {
		if s.Top {
			return false
		}
		if s.Undef || s.Null || s.Nan {
			return false
		}
		if empty, ok := scalarEmptyRecovered(kernel, s.Set); ok {
			return empty
		}
		empty, ok := seqEmptyRecovered(kernel, s.Set)
		return ok && empty
	}
	trueDead := dead(whenTrue)
	falseDead := dead(whenFalse)
	if falseDead && !trueDead {
		return true, true
	}
	if trueDead && !falseDead {
		return false, true
	}
	// both dead is an impossible state (nothing admitted at all) and
	// both live is the honest undecided — neither decides a verdict
	return false, false
}

// numberState is the wire state a number-sorted adapter value denotes,
// for the rows this file poses. Exact values and plain sets only —
// the arms Truthiness's own KindValues/KindSet cases cover.
func numberState(set refinementsets.RefinedSet) kernelbridge.KnownStateWire {
	return kernelbridge.KnownStateWire{Set: set, Undef: false, Null: false, Nan: false}
}

// TestTruthinessAgreesWithTheKernelWhereTheAdapterAnswers is the
// AGREEMENT half: every row where abstractdomain.Truthiness DOES decide
// a numeric verdict LOCALLY, held to the kernel's js.truthyNum
// narrowing. Drift is a failure.
func TestTruthinessAgreesWithTheKernelWhereTheAdapterAnswers(t *testing.T) {
	kernel := differentialKernel(t)

	rows := []struct {
		name  string
		value abstractdomain.AbstractValue
		set   refinementsets.RefinedSet
	}{
		// the exact-value arm, which the audit does NOT name as declining
		// — these are the real comparisons
		{"exact 1", exactValue(1), exactSet(1)},
		{"exact 0", exactValue(0), exactSet(0)},
		{"exact -0", exactValue(negZero()), exactSet(negZero())},
		{"exact -1", exactValue(-1), exactSet(-1)},
		{"exact 0.5", exactValue(0.5), exactSet(0.5)},
		{"exact +inf", exactValue(posInf()), exactSet(posInf())},
		{"exact -inf", exactValue(negInf()), exactSet(negInf())},
		{"exact 2^53", exactValue(9007199254740992), exactSet(9007199254740992)},
		// the NaN arm: Truthiness answers (false, true) directly, and the
		// kernel's state carries NaN as a FLAG, not a set member
		{"NaN", abstractdomain.NaNValue, refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))},
	}

	for _, row := range rows {
		adapterValue, adapterKnown := abstractdomain.Truthiness(row.value)
		if !adapterKnown {
			t.Errorf("Truthiness(%s) declined — this row belongs in the determination-gap ledger, not here", row.name)
			continue
		}
		state := numberState(row.set)
		if row.value.Kind == abstractdomain.KindNaN {
			state = kernelbridge.KnownStateWire{Set: row.set, Nan: true}
		}
		kernelValue, kernelKnown := kernelTruthiness(kernel, state, "js.truthyNum")
		if !kernelKnown {
			t.Errorf("kernel js.truthyNum declined on %s while the adapter answered %v — SCRUTINY: the adapter claims what the proof does not", row.name, adapterValue)
			continue
		}
		if adapterValue != kernelValue {
			t.Errorf("Truthiness(%s) = %v, kernel js.truthyNum = %v — the two routes disagree", row.name, adapterValue, kernelValue)
		}
	}
}

// TestTruthinessDecidedClosesTheLedgerGaps is the flipped half: each
// ledger row the kernel decides, asserted as an AGREEMENT now.
// TruthinessDecided must answer, and answer what the kernel answers.
// The local Truthiness is asserted to still decline on the same row —
// that is what makes each row a genuine kernel determination rather
// than a local one, and it keeps the two functions' division honest.
func TestTruthinessDecidedClosesTheLedgerGaps(t *testing.T) {
	kernel := differentialKernel(t)
	abstractdomain.SetLatticeKernel(kernel)
	t.Cleanup(func() { abstractdomain.SetLatticeKernel(nil) })

	rows := []struct {
		ledger string
		value  abstractdomain.AbstractValue
		set    refinementsets.RefinedSet
		want   bool
	}{
		{
			ledger: "gap-1 KindSet [1, 10]",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10)),
			want: true,
		},
		{
			ledger: "gap-2 KindSet {0}",
			value: abstractdomain.KnownSet(exactSet(0), nil, abstractdomain.TrustProved,
				abstractdomain.SetKindTagNone),
			set:  exactSet(0),
			want: false,
		},
		{
			ledger: "gap-3 KindSet [1, inf)",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(1)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(1)),
			want: true,
		},
		{
			ledger: "gap-4 KindSet [-10, -1]",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(-10), refinementsets.AtMost(-1)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(-10),
				refinementsets.AtMost(-1)),
			want: true,
		},
		{
			ledger: "gap-6 KindValues {1, 2} — a multi-value word",
			value: abstractdomain.KnownValues([]float64{1, 2},
				abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
			set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})),
			want: true,
		},
	}

	for _, row := range rows {
		if _, localKnown := abstractdomain.Truthiness(row.value); localKnown {
			t.Errorf("%s: the LOCAL Truthiness now answers — this row is no longer a kernel determination and the ledger's split is stale", row.ledger)
			continue
		}
		value, known := abstractdomain.TruthinessDecided(row.value)
		if !known {
			t.Errorf("%s: TruthinessDecided declined — the gap the kernel closes is open again", row.ledger)
			continue
		}
		if value != row.want {
			t.Errorf("%s: TruthinessDecided = %v, want %v", row.ledger, value, row.want)
		}
		kernelValue, kernelKnown := kernelTruthiness(kernel, numberState(row.set), "js.truthyNum")
		if !kernelKnown {
			t.Errorf("%s: the kernel declined while the adapter answered %v — SCRUTINY", row.ledger, value)
			continue
		}
		if kernelValue != value {
			t.Errorf("%s: TruthinessDecided = %v, kernel js.truthyNum = %v — the two routes disagree", row.ledger, value, kernelValue)
		}
	}
}

// TestTruthinessStillDeclinesWhereTheKernelDoesToo is the ledger's one
// remaining OPEN row (gap-5). Both routes are honestly undecided on a
// window holding 0 and nonzero alike, so this is not a determination
// gap at all — it pins that the silence left is the question's.
func TestTruthinessStillDeclinesWhereTheKernelDoesToo(t *testing.T) {
	kernel := differentialKernel(t)
	abstractdomain.SetLatticeKernel(kernel)
	t.Cleanup(func() { abstractdomain.SetLatticeKernel(nil) })

	rows := []struct {
		ledger      string
		value       abstractdomain.AbstractValue
		set         refinementsets.RefinedSet
		kernelValue bool
		kernelKnown bool
	}{
		{
			ledger: "gap-5 KindSet [0, 10] — the kernel declines here too",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0),
				refinementsets.AtMost(10)),
			kernelValue: false, kernelKnown: false,
		},
	}

	for _, row := range rows {
		// BOTH routes must stay silent: the local one because it never
		// looks at a set, the kernel-asking one because the question
		// itself has no verdict here
		if _, localKnown := abstractdomain.Truthiness(row.value); localKnown {
			t.Errorf("%s: the local Truthiness now ANSWERS — the ledger is stale", row.ledger)
			continue
		}
		if _, decidedKnown := abstractdomain.TruthinessDecided(row.value); decidedKnown {
			t.Errorf("%s: TruthinessDecided now ANSWERS — the kernel gained a verdict here and this row belongs in TestTruthinessDecidedClosesTheLedgerGaps", row.ledger)
			continue
		}
		kernelValue, kernelKnown := kernelTruthiness(kernel, numberState(row.set), "js.truthyNum")
		if kernelKnown != row.kernelKnown {
			t.Errorf("%s: kernel js.truthyNum known = %v, want %v — the ledger records the wrong gap size", row.ledger, kernelKnown, row.kernelKnown)
			continue
		}
		if kernelKnown && kernelValue != row.kernelValue {
			t.Errorf("%s: kernel js.truthyNum = %v, want %v — the ledger records the wrong verdict", row.ledger, kernelValue, row.kernelValue)
		}
	}
}

// TestStringTruthinessAgreesWhereBothRoutesSpeak holds the string sort
// the same way. The adapter reads a string-sorted word's truthiness off
// its LENGTH (lattice_operations.go:104-106); the kernel filters the
// tuple layer with js.truthyStr, where truth is every nonempty tuple.
//
// Only the two ENDS are comparable here: the empty word (both say
// false) and a word the adapter reads as nonempty. A string-sorted
// KindSet declined for the same reason the numeric ones did —
// Truthiness's KindSet arm never looks at a set at all — and
// TruthinessDecided closes it the same way, routing a sequence-shaped
// set to js.truthyStr (lattice_kernel.go's truthinessOperand).
func TestStringTruthinessAgreesWhereBothRoutesSpeak(t *testing.T) {
	kernel := differentialKernel(t)

	rows := []struct {
		name  string
		value abstractdomain.AbstractValue
		set   refinementsets.RefinedSet
		want  bool
	}{
		{
			name: "the empty word",
			value: abstractdomain.KnownValues(nil, abstractdomain.PrimitiveString,
				abstractdomain.TrustProved),
			set:  refinementsets.StringTuple(""),
			want: false,
		},
		{
			name: "one code unit",
			value: abstractdomain.KnownValues([]float64{97},
				abstractdomain.PrimitiveString, abstractdomain.TrustProved),
			set:  refinementsets.StringTuple("a"),
			want: true,
		},
		{
			name: "two code units",
			value: abstractdomain.KnownValues([]float64{97, 98},
				abstractdomain.PrimitiveString, abstractdomain.TrustProved),
			set:  refinementsets.StringTuple("ab"),
			want: true,
		},
	}

	for _, row := range rows {
		adapterValue, adapterKnown := abstractdomain.Truthiness(row.value)
		if !adapterKnown {
			t.Errorf("Truthiness(%s) declined on a string word — the KindValues string arm is supposed to answer", row.name)
			continue
		}
		if adapterValue != row.want {
			t.Errorf("Truthiness(%s) = %v, want %v", row.name, adapterValue, row.want)
		}
		// the kernel's own reading of the same word, through the string
		// narrowing: truth admits it iff it is nonempty
		whenTrue, whenFalse := kernel.NarrowState(
			kernelbridge.KnownStateWire{Set: row.set}, "js.truthyStr", 0, false)
		if whenTrue.Top || whenFalse.Top {
			t.Fatalf("%s: kernel js.truthyStr answered a top side on a concrete state", row.name)
		}
		empty, ok := seqEmptyRecovered(kernel, whenTrue.Set)
		if !ok {
			t.Errorf("%s: kernel SeqEmpty refused whenTrue.Set=%s — the row is unverifiable, not silently skipped",
				row.name, refinementsets.FormatForDiagnostics(whenTrue.Set))
			continue
		}
		kernelValue := !empty
		if adapterValue != kernelValue {
			t.Errorf("Truthiness(%s) = %v, kernel js.truthyStr admits-on-truth = %v — the two routes disagree",
				row.name, adapterValue, kernelValue)
		}
	}
}
