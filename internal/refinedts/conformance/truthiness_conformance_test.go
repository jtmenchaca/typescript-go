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
// narrowing; a concrete B row"). Each row below is the adapter
// DECLINING where the kernel decides. None is a failure; each is a
// named migration target, and each is ASSERTED as a decline, so a row
// that starts answering fails this file and forces the ledger current.
//
//   gap-1  KindSet [1, 10]          adapter (false,false)  kernel TRUE
//          — every member is nonzero, so the falsy side is empty.
//   gap-2  KindSet {0}              adapter (false,false)  kernel FALSE
//          — the one member is zero, so the truthy side is empty.
//   gap-3  KindSet [1, ∞)           adapter (false,false)  kernel TRUE
//          — an unbounded window, still wholly nonzero.
//   gap-4  KindSet [-10, -1]        adapter (false,false)  kernel TRUE
//          — negatives are truthy; the sign is not the question.
//   gap-5  KindSet [0, 10]          adapter (false,false)  kernel UNDECIDED
//          — the only ledger row where the kernel ALSO declines, and it
//          is here on purpose: it pins that the gap is the adapter's
//          silence and not the kernel's, everywhere ELSE in this table.
//   gap-6  KindValues 2+ numbers    adapter (false,false)  kernel TRUE
//          — Truthiness's KindValues arm answers only for a SINGLE
//          value (lattice_operations.go:110); a multi-value word
//          declines even when every member agrees. The audit names the
//          KindSet arm; this row is the same silence one arm over.
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
		return kernel.ScalarEmpty(s.Set)
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
// a numeric verdict, held to the kernel's js.truthyNum narrowing. Drift
// is a failure.
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

// TestTruthinessDeclinesWhereTheKernelDecides is the DETERMINATION-GAP
// half: each ledger row above, asserted as a decline. A row that starts
// answering fails here — which is the point: the ledger cannot go stale
// without this file going red.
func TestTruthinessDeclinesWhereTheKernelDecides(t *testing.T) {
	kernel := differentialKernel(t)

	rows := []struct {
		ledger      string
		value       abstractdomain.AbstractValue
		set         refinementsets.RefinedSet
		kernelValue bool
		kernelKnown bool
	}{
		{
			ledger: "gap-1 KindSet [1, 10]",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10)),
			kernelValue: true, kernelKnown: true,
		},
		{
			ledger: "gap-2 KindSet {0}",
			value: abstractdomain.KnownSet(exactSet(0), nil, abstractdomain.TrustProved,
				abstractdomain.SetKindTagNone),
			set:         exactSet(0),
			kernelValue: false, kernelKnown: true,
		},
		{
			ledger: "gap-3 KindSet [1, inf)",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(1)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set:         refinementsets.MakeRefinedSet(refinementsets.AtLeast(1)),
			kernelValue: true, kernelKnown: true,
		},
		{
			ledger: "gap-4 KindSet [-10, -1]",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(-10), refinementsets.AtMost(-1)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(-10),
				refinementsets.AtMost(-1)),
			kernelValue: true, kernelKnown: true,
		},
		{
			ledger: "gap-5 KindSet [0, 10] — the kernel declines here too",
			value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0),
				refinementsets.AtMost(10)),
			kernelValue: false, kernelKnown: false,
		},
		{
			ledger: "gap-6 KindValues {1, 2} — a multi-value word",
			value: abstractdomain.KnownValues([]float64{1, 2},
				abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
			set:         refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})),
			kernelValue: true, kernelKnown: true,
		},
	}

	for _, row := range rows {
		_, adapterKnown := abstractdomain.Truthiness(row.value)
		if adapterKnown {
			t.Errorf("%s: Truthiness now ANSWERS — the determination gap closed and this file's ledger is stale; move the row to TestTruthinessAgreesWithTheKernelWhereTheAdapterAnswers", row.ledger)
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
// Only the two ENDS are comparable: the empty word (both say false) and
// a word the adapter reads as nonempty. A string-sorted KindSet is a
// SCRUTINY-adjacent gap — Truthiness's KindSet arm declines for strings
// exactly as it does for numbers (it never looks at SetKindTag at all),
// and the kernel answers, so the gap-1..gap-5 ledger above covers the
// string sort too, one narrowing op over.
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
		kernelValue := !kernel.SeqEmpty(whenTrue.Set)
		if adapterValue != kernelValue {
			t.Errorf("Truthiness(%s) = %v, kernel js.truthyStr admits-on-truth = %v — the two routes disagree",
				row.name, adapterValue, kernelValue)
		}
	}
}
