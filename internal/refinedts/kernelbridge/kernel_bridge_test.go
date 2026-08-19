// The round trip on the proved code itself: sets encoded here, decided
// inside the kernel dylib, answers read back. Skipped (reported as
// ignored, never a faked pass) when the artifact is absent — build the
// native kernel to produce it.
//
// NOT PORTED: the specification-shaped subtests — "the judgment:
// structural conditions and theorem-backed faults", "every fault class
// fires through the indexed listings", "sequence-shaped nodes are
// witnessable; sizes are per node" — Specification has no Go twin yet
// (object_graphs is unported). Blocked; reported.
package kernelbridge

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func loadRoundTripKernel(t *testing.T) *RefinedTSKernel {
	t.Helper()
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

// countLike is z.number().min(0).int().
func countLikeSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
}

// impossible is z.intersection(z.number().max(5), z.number().min(10)) — ∅.
//
// Spelled as the empty OneOf, not as the crossed bound pair
// (AtMost(5) ∧ AtLeast(10)) that its comment describes: the kernel's
// bottom intercept (boundary/exports.lean's kernelTransfer, gated on
// readEnclosure/oneOfEnc) only ever sets Enclosure.bot from
// `OneOf []` (set_functions/enclosure.lean's oneOfEnc, "that branch is
// bottom's ONLY caller"). `Refinement.encl`'s AtLeast/AtMost arms feed
// Enclosure.and, which merges bounds with no emptiness check at all —
// so the crossed-bound spelling denotes ∅ semantically (scalarEmptyB
// proves it, see TestEmptinessAndDisjointnessOnTheOneTupleLayer) but
// reaches the transfer boundary as an ordinary (if crossed) window,
// never as Enclosure.bottom. This is a named, documented precision
// gap: a semantically-empty-but-not-OneOf-spelled set is NOT
// bottom-detected at the transfer boundary today.
func impossibleSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))
}

// ints is the integers, bare.
func intsSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.Integer)
}

func TestMembershipTheRuntimeCheckOverTheWireRoundTrip(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	countLike := countLikeSet()
	if got := kernel.Member(countLike, []float64{3}); !got {
		t.Errorf("member(countLike, [3]) = %v, want true", got)
	}
	if got := kernel.Member(countLike, []float64{-1}); got {
		t.Errorf("member(countLike, [-1]) = %v, want false", got)
	}
	if got := kernel.Member(countLike, []float64{0.5}); got {
		t.Errorf("member(countLike, [0.5]) = %v, want false", got)
	}
	if got := kernel.Member(countLike, []float64{}); got {
		t.Errorf("member(countLike, []) = %v, want false", got)
	}
	if got := kernel.Member(countLike, []float64{1, 2}); got {
		t.Errorf("member(countLike, [1, 2]) = %v, want false", got)
	}
	// +∞ is an element of ℝ̄ ∖ {0}
	nonZero := refinementsets.MakeRefinedSet(refinementsets.Difference(
		refinementsets.Numbers, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
	))
	if got := kernel.Member(nonZero, []float64{math.Inf(1)}); !got {
		t.Errorf("member(ℝ̄∖{0}, [+∞]) = %v, want true", got)
	}
	// 0·2¹⁷ means 0 — value equality, not structure — and the wire
	// canonicalizes, so this crosses as the pair {0, 0}
	if got := kernel.Member(nonZero, []float64{math.Copysign(0, -1)}); got {
		t.Errorf("member(ℝ̄∖{0}, [-0]) = %v, want false", got)
	}
}

func TestEmptinessAndDisjointnessOnTheOneTupleLayer(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	if got := kernel.ScalarEmpty(impossibleSet()); !got {
		t.Errorf("scalarEmpty(impossible) = %v, want true", got)
	}
	if got := kernel.ScalarEmpty(refinementsets.MakeRefinedSet(refinementsets.AtMost(10), refinementsets.AtLeast(5))); got {
		t.Errorf("scalarEmpty([5,10]) = %v, want false", got)
	}
	if got := kernel.ScalarDisjoint(
		refinementsets.MakeRefinedSet(refinementsets.Below(0)),
		refinementsets.MakeRefinedSet(refinementsets.Above(0)),
	); !got {
		t.Errorf("scalarDisjoint(below 0, above 0) = %v, want true", got)
	}
	if got := kernel.ScalarDisjoint(
		refinementsets.MakeRefinedSet(refinementsets.AtMost(0)),
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0)),
	); got {
		t.Errorf("scalarDisjoint(atMost 0, atLeast 0) = %v, want false", got)
	}
}

func TestTheLoopInvariantCertificateBothPremisesInOneAnswer(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	candidate := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10), refinementsets.Integer)
	// a concrete entry inside, a step image inside: certified
	if got := kernel.Invariant(
		candidate,
		InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{0}},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10), refinementsets.Integer)},
	); !got {
		t.Errorf("invariant(entry inside, step inside) = %v, want true", got)
	}
	// the entry escapes the candidate: declined
	if got := kernel.Invariant(
		candidate,
		InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{-1}},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(10), refinementsets.Integer)},
	); got {
		t.Errorf("invariant(entry escapes) = %v, want false", got)
	}
	// the step's image escapes the candidate: declined
	if got := kernel.Invariant(
		candidate,
		InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{0}},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(1), refinementsets.AtMost(11), refinementsets.Integer)},
	); got {
		t.Errorf("invariant(step escapes) = %v, want false", got)
	}
	// both premises as sets, the scalar subset route
	if got := kernel.Invariant(
		candidate,
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10), refinementsets.Integer)},
	); !got {
		t.Errorf("invariant(both sets, inside) = %v, want true", got)
	}
	// the sequence route: a star of digits inside a star of a wider
	// scalar set, entry pinned to a concrete word
	digits := refinementsets.MakeRefinedSet(refinementsets.AtLeast(48), refinementsets.AtMost(57), refinementsets.Integer)
	wide := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(127), refinementsets.Integer)
	if got := kernel.Invariant(
		refinementsets.MakeRefinedSet(refinementsets.Star(wide)),
		InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{49, 50}},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.Star(digits))},
	); !got {
		t.Errorf("invariant(digits inside wide star) = %v, want true", got)
	}
	if got := kernel.Invariant(
		refinementsets.MakeRefinedSet(refinementsets.Star(digits)),
		InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{49, 50}},
		InvariantPremise{Kind: InvariantPremiseSet, Set: refinementsets.MakeRefinedSet(refinementsets.Star(wide))},
	); got {
		t.Errorf("invariant(wide inside digits star) = %v, want false", got)
	}
}

func TestTheLoopSolverIterateWidenCertifyInTheKernel(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// for (let i = 0; i < 10; i++) — entry 0, condition below 10,
	// body i + 1: certified inside the nonnegative integers
	counted := kernel.SolveLoop(LoopQuestion{
		Entry: []*InvariantPremise{{Kind: InvariantPremiseValues, Values: []float64{0}}},
		Cond:  []*refinementsets.RefinedSet{setPtr(refinementsets.MakeRefinedSet(refinementsets.Below(10)))},
		Body: []LoopEffect{{
			Kind: LoopEffectBinary, Op: LoopOpAdd,
			A: &LoopEffect{Kind: LoopEffectVar, Index: 0},
			B: &LoopEffect{Kind: LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
		}},
	})
	if len(counted) != 1 {
		t.Fatalf("counted: len = %d, want 1", len(counted))
	}
	if counted[0].Kind != LoopVarAnswerSet {
		t.Fatalf("counted[0].Kind = %v, want set", counted[0].Kind)
	}
	if !kernel.ScalarSubset(counted[0].Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)) {
		t.Errorf("counted[0].Set ⊆ nonneg ints = false, want true")
	}

	// an unbounded integer walk certifies inside the nonnegative
	// integers even without a condition: the overflow corner rounds
	// back to maxFloat, so the step image stays inside
	stepped := kernel.SolveLoop(LoopQuestion{
		Entry: []*InvariantPremise{{Kind: InvariantPremiseValues, Values: []float64{0}}},
		Cond:  []*refinementsets.RefinedSet{nil},
		Body: []LoopEffect{{
			Kind: LoopEffectBinary, Op: LoopOpAdd,
			A: &LoopEffect{Kind: LoopEffectVar, Index: 0},
			B: &LoopEffect{Kind: LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
		}},
	})
	if stepped[0].Kind != LoopVarAnswerSet {
		t.Fatalf("stepped[0].Kind = %v, want set", stepped[0].Kind)
	}
	if !kernel.ScalarSubset(stepped[0].Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)) {
		t.Errorf("stepped[0].Set ⊆ nonneg ints = false, want true")
	}

	// an oscillating product whose growth outruns the iterate window
	// fails its own certification — declined, never guessed
	doubled := kernel.SolveLoop(LoopQuestion{
		Entry: []*InvariantPremise{{Kind: InvariantPremiseValues, Values: []float64{1}}},
		Cond:  []*refinementsets.RefinedSet{nil},
		Body: []LoopEffect{{
			Kind: LoopEffectBinary, Op: LoopOpMul,
			A: &LoopEffect{Kind: LoopEffectVar, Index: 0},
			B: &LoopEffect{Kind: LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-2}))},
		}},
	})
	if doubled[0].Kind != LoopVarAnswerUnknown {
		t.Errorf("doubled[0].Kind = %v, want unknown", doubled[0].Kind)
	}

	// an unreadable body answers unknown — never a guess
	opaque := kernel.SolveLoop(LoopQuestion{
		Entry: []*InvariantPremise{{Kind: InvariantPremiseValues, Values: []float64{0}}},
		Cond:  []*refinementsets.RefinedSet{nil},
		Body:  []LoopEffect{{Kind: LoopEffectUnknown}},
	})
	if opaque[0].Kind != LoopVarAnswerUnknown {
		t.Errorf("opaque[0].Kind = %v, want unknown", opaque[0].Kind)
	}

	// withdrawals cascade: the second binding copies the first, and
	// the first cannot certify (its entry is unknown), so the second's
	// certificate must not survive on the withdrawn claim
	leaning := kernel.SolveLoop(LoopQuestion{
		Entry: []*InvariantPremise{nil, {Kind: InvariantPremiseValues, Values: []float64{0}}},
		Cond:  []*refinementsets.RefinedSet{nil, nil},
		Body: []LoopEffect{
			{Kind: LoopEffectVar, Index: 0},
			{Kind: LoopEffectVar, Index: 0},
		},
	})
	if leaning[0].Kind != LoopVarAnswerUnknown {
		t.Errorf("leaning[0].Kind = %v, want unknown", leaning[0].Kind)
	}
	if leaning[1].Kind != LoopVarAnswerUnknown {
		t.Errorf("leaning[1].Kind = %v, want unknown", leaning[1].Kind)
	}
}

func setPtr(s refinementsets.RefinedSet) *refinementsets.RefinedSet { return &s }

func TestSubsetTheTypeCheckStrictnessRespected(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.AtLeast(10)), refinementsets.MakeRefinedSet(refinementsets.AtLeast(5))); !got {
		t.Errorf("[10,∞) ⊆ [5,∞) = %v, want true", got)
	}
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.AtLeast(5)), refinementsets.MakeRefinedSet(refinementsets.AtLeast(10))); got {
		t.Errorf("[5,∞) ⊆ [10,∞) = %v, want false", got)
	}
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.Above(5)), refinementsets.MakeRefinedSet(refinementsets.AtLeast(5))); !got {
		t.Errorf("(5,∞) ⊆ [5,∞) = %v, want true", got)
	}
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.AtLeast(5)), refinementsets.MakeRefinedSet(refinementsets.Above(5))); got {
		t.Errorf("[5,∞) ⊆ (5,∞) = %v, want false", got)
	}
}

func TestCongruencesWithExclusionsTheCompletenessHasNoFragmentGuard(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// multiples of 4 ⊆ multiples of 2 (the difference carries a
	// positive congruence AND a negated one); not conversely
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.MultipleOf(4)), refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2))); !got {
		t.Errorf("4ℤ ⊆ 2ℤ = %v, want true", got)
	}
	if got := kernel.ScalarSubset(refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2)), refinementsets.MakeRefinedSet(refinementsets.MultipleOf(4))); got {
		t.Errorf("2ℤ ⊆ 4ℤ = %v, want false", got)
	}
	// multiples of 2 strictly between 0 and 4, minus {2}: nothing left;
	// widen to (0, 6) and 4 remains
	if got := kernel.ScalarEmpty(refinementsets.MakeRefinedSet(refinementsets.Difference(
		refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2), refinementsets.Above(0), refinementsets.Below(4)),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
	))); !got {
		t.Errorf("2ℤ ∩ (0,4) ∖ {2} empty = %v, want true", got)
	}
	if got := kernel.ScalarEmpty(refinementsets.MakeRefinedSet(refinementsets.Difference(
		refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2), refinementsets.Above(0), refinementsets.Below(6)),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
	))); got {
		t.Errorf("2ℤ ∩ (0,6) ∖ {2} empty = %v, want false", got)
	}
	// dyadic endpoints: no multiple of 0.75 strictly inside (0.125, 0.625)
	if got := kernel.ScalarEmpty(refinementsets.MakeRefinedSet(
		refinementsets.MultipleOf(0.75), refinementsets.Above(0.125), refinementsets.Below(0.625),
	)); !got {
		t.Errorf("0.75ℤ ∩ (0.125,0.625) empty = %v, want true", got)
	}
}

func TestSequenceShapesTuplesAndArrays(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	ints := intsSet()
	coordinates := refinementsets.MakeRefinedSet(refinementsets.Concatenation(ints, ints))
	pair := refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.Numbers, refinementsets.Numbers))
	if got := kernel.SeqSubset(coordinates, pair); !got {
		t.Errorf("coordinates ⊆ pair = %v, want true", got)
	}
	if got := kernel.SeqSubset(pair, coordinates); got {
		t.Errorf("pair ⊆ coordinates = %v, want false", got)
	}
	if got := kernel.SeqSubset(coordinates, refinementsets.MakeRefinedSet(refinementsets.Star(ints))); !got {
		t.Errorf("coordinates ⊆ ints* = %v, want true", got)
	}
	atLeastZero := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	if got := kernel.SeqSubset(refinementsets.MakeRefinedSet(refinementsets.Star(atLeastZero)), refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Numbers))); !got {
		t.Errorf("[0,∞)* ⊆ numbers* = %v, want true", got)
	}
	if got := kernel.SeqEmpty(refinementsets.MakeRefinedSet(refinementsets.Concatenation(impossibleSet(), ints))); !got {
		t.Errorf("concat(∅, ints) empty = %v, want true", got)
	}
	if got := kernel.SeqEmpty(coordinates); got {
		t.Errorf("coordinates empty = %v, want false", got)
	}
}

func TestASingletonLeftSideIsPosedAsMembershipWEqualsBIffWInB(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// the case the subset cost measure declined outright: a 200-char
	// literal against a pattern — as membership it is exact and linear
	literal := repeatChar("a", 199) + "@"
	if got := kernel.SeqSubset(refinementsets.StringTuple(literal), refinementsets.IncludesSet("@")); !got {
		t.Errorf("199 a's + @ ⊆ includes(@) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.StringTuple(repeatChar("a", 200)), refinementsets.IncludesSet("@")); got {
		t.Errorf("200 a's ⊆ includes(@) = %v, want false", got)
	}
	// the empty-string literal, and a short exact refutation
	if got := kernel.SeqSubset(refinementsets.StringTuple(""), refinementsets.MakeRefinedSet(refinementsets.Star(intsSet()))); !got {
		t.Errorf(`"" ⊆ ints* = %v, want true`, got)
	}
	if got := kernel.SeqSubset(refinementsets.StringTuple("x"), refinementsets.IncludesSet("@")); got {
		t.Errorf(`"x" ⊆ includes(@) = %v, want false`, got)
	}
}

func repeatChar(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestPatternSubsetPatternThroughThePlacementReading(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	if got := kernel.SeqSubset(refinementsets.EndsWithSet(".ts"), refinementsets.IncludesSet(".")); !got {
		t.Errorf("endsWith(.ts) ⊆ includes(.) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.StartsWithSet("ab"), refinementsets.IncludesSet("a")); !got {
		t.Errorf("startsWith(ab) ⊆ includes(a) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.StartsWithSet("ab"), refinementsets.StartsWithSet("a")); !got {
		t.Errorf("startsWith(ab) ⊆ startsWith(a) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.EndsWithSet("x.ts"), refinementsets.EndsWithSet(".ts")); !got {
		t.Errorf("endsWith(x.ts) ⊆ endsWith(.ts) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.IncludesSet("abc"), refinementsets.IncludesSet("abc")); !got {
		t.Errorf("includes(abc) ⊆ includes(abc) = %v, want true", got)
	}
	// every pattern sits inside the strings
	if got := kernel.SeqSubset(refinementsets.IncludesSet("@"), refinementsets.Strings); !got {
		t.Errorf("includes(@) ⊆ strings = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.EndsWithSet(".ts"), refinementsets.Strings); !got {
		t.Errorf("endsWith(.ts) ⊆ strings = %v, want true", got)
	}
	// overlapping-substring widenings place correctly
	if got := kernel.SeqSubset(refinementsets.IncludesSet("aa"), refinementsets.IncludesSet("a")); !got {
		t.Errorf("includes(aa) ⊆ includes(a) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.IncludesSet("aba"), refinementsets.IncludesSet("b")); !got {
		t.Errorf("includes(aba) ⊆ includes(b) = %v, want true", got)
	}
	if got := kernel.SeqSubset(refinementsets.StartsWithSet("a"), refinementsets.IncludesSet("a")); !got {
		t.Errorf("startsWith(a) ⊆ includes(a) = %v, want true", got)
	}
	// no placement, but a SEPARATING WORD: the refuter answers false
	// with a rechecked witness (separatingWitness_refutes) — ".ts"
	// lacks an "x", "." alone ends with no ".ts", "a" alone lacks the
	// "ab" prefix — so the pattern route decides both ways now
	if got := kernel.SeqSubset(refinementsets.EndsWithSet(".ts"), refinementsets.IncludesSet("x")); got {
		t.Errorf("endsWith(.ts) ⊆ includes(x) = %v, want false", got)
	}
	if got := kernel.SeqSubset(refinementsets.IncludesSet("."), refinementsets.EndsWithSet(".ts")); got {
		t.Errorf("includes(.) ⊆ endsWith(.ts) = %v, want false", got)
	}
	if got := kernel.SeqSubset(refinementsets.StartsWithSet("a"), refinementsets.StartsWithSet("ab")); got {
		t.Errorf("startsWith(a) ⊆ startsWith(ab) = %v, want false", got)
	}
	// a union of words (a string enum) reads through the union route:
	// the left union decomposes arm by arm, and "cd" — the sets' own
	// spelled word, rechecked by the membership decider — separates
	abOrCd := refinementsets.MakeRefinedSet(refinementsets.Union(refinementsets.StringTuple("ab"), refinementsets.StringTuple("cd")))
	if got := kernel.SeqSubset(abOrCd, refinementsets.IncludesSet("a")); got {
		t.Errorf("(ab|cd) ⊆ includes(a) = %v, want false", got)
	}
	if got := kernel.SeqSubset(abOrCd, refinementsets.Strings); !got {
		t.Errorf("(ab|cd) ⊆ strings = %v, want true", got)
	}
}

func TestAContainsComplementReadsAsTheStarOfTheScalarDifference(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// what `if (url.includes("#")) return` leaves on the fall-through:
	// strings ∖ contains("#") — and the declared target star(C ∖ {#})
	noHash := refinementsets.MakeRefinedSet(refinementsets.Difference(refinementsets.Strings, refinementsets.IncludesSet("#")))
	hash := float64('#')
	starWithout := func(point float64) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.MakeRefinedSet(
			refinementsets.Difference(refinementsets.Codepoints, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{point}))),
		)))
	}
	if got := kernel.SeqSubset(noHash, starWithout(hash)); !got {
		t.Errorf("noHash ⊆ starWithout(#) = %v, want true", got)
	}
	// the enlargement stays an upper bound: against a star missing a
	// DIFFERENT point, the witness search refutes ("\0" is in the left
	// and out of the right)
	if got := kernel.SeqSubset(noHash, starWithout(0)); got {
		t.Errorf("noHash ⊆ starWithout(0) = %v, want false", got)
	}
	// a multi-point word complement is not the single-point rewrite:
	// strings ∖ contains("ab") still holds "a" itself — and the
	// boundary always tries the witness search, so that very word
	// refutes the inclusion instead of refusing
	noAb := refinementsets.MakeRefinedSet(refinementsets.Difference(refinementsets.Strings, refinementsets.IncludesSet("ab")))
	if got := kernel.SeqSubset(noAb, starWithout(float64('a'))); got {
		t.Errorf("noAb ⊆ starWithout(a) = %v, want false", got)
	}
}

func TestAStartsWithComplementReadsInTheCompanyOfANonEmptySibling(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// what `if (id.startsWith("\0")) return` leaves on the fall-through
	// of a NON-EMPTY id: min(1) ∧ (strings ∖ startsWith("\0")) — and
	// the declared target (C ∖ {0}) · C*
	noVirtual := refinementsets.MakeRefinedSet(append(
		refinementsets.Repetition(refinementsets.Codepoints, 1, nil).Forms,
		refinementsets.Difference(refinementsets.Strings, refinementsets.StartsWithSet("\x00")),
	)...)
	headedTarget := refinementsets.MakeRefinedSet(refinementsets.Concatenation(
		refinementsets.MakeRefinedSet(refinementsets.Difference(refinementsets.Codepoints, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})))),
		refinementsets.Strings,
	))
	if got := kernel.SeqSubset(noVirtual, headedTarget); !got {
		t.Errorf("noVirtual ⊆ headedTarget = %v, want true", got)
	}
	// WITHOUT the non-empty sibling the empty string genuinely
	// separates the sides — and the always-tried witness search finds
	// exactly it, so the answer is a refutation, never a
	// partial-reading claim
	maybeEmpty := refinementsets.MakeRefinedSet(refinementsets.Difference(refinementsets.Strings, refinementsets.StartsWithSet("\x00")))
	if got := kernel.SeqSubset(maybeEmpty, headedTarget); got {
		t.Errorf("maybeEmpty ⊆ headedTarget = %v, want false", got)
	}
}

func TestTheCertifyingSeamReplaysANarrowingChain(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// a REAL narrowing chain: the declared set 0..100, narrowed by a
	// guard's atLeast(10) (narrowing = intersection), then the checked
	// membership of 50 — every step replayed by the kernel; the true
	// answer PROVES membership in the derived set (eval_member_sound)
	declared := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(100))
	chain := Chain{
		Root: declared,
		Ops: []ChainOp{
			{Op: ChainOpIntersectWith, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(10))},
			{Op: ChainOpMemberQ, Tuple: []float64{50}},
		},
	}
	got := kernel.ValidateChain(chain)
	if got.Kind != ValidateChainAnswer || !got.Answer {
		t.Errorf("validateChain(50 in [10,100]) = %+v, want answer true", got)
	}
	// the refuted side: 5 left the narrowed set
	got = kernel.ValidateChain(Chain{
		Root: declared,
		Ops: []ChainOp{
			{Op: ChainOpIntersectWith, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(10))},
			{Op: ChainOpMemberQ, Tuple: []float64{5}},
		},
	})
	if got.Kind != ValidateChainAnswer || got.Answer {
		t.Errorf("validateChain(5 in [10,100]) = %+v, want answer false", got)
	}
	// a union step, then emptiness: derived sets keep answering
	got = kernel.ValidateChain(Chain{
		Root: refinementsets.MakeRefinedSet(refinementsets.Below(0)),
		Ops: []ChainOp{
			{Op: ChainOpUnionWith, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(10))},
			{Op: ChainOpMemberQ, Tuple: []float64{12}},
		},
	})
	if got.Kind != ValidateChainAnswer || !got.Answer {
		t.Errorf("validateChain(12 in below(0) ∪ [10,∞)) = %+v, want answer true", got)
	}
	// a mid-chain question refuses — typing, not silence
	got = kernel.ValidateChain(Chain{
		Root: declared,
		Ops: []ChainOp{
			{Op: ChainOpMemberQ, Tuple: []float64{50}},
			{Op: ChainOpStarOf},
		},
	})
	if got.Kind != ValidateChainDeclined {
		t.Errorf("validateChain(memberQ then starOf) = %+v, want declined", got)
	}
}

func TestCanonicalKeysIntersectionOrderAndUnionBranchesCollapse(t *testing.T) {
	// permuted forms lists — one key
	a := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer, refinementsets.AtMost(9))))
	b := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.AtMost(9), refinementsets.Integer, refinementsets.AtLeast(0))))
	if a == nil || b == nil || *a != *b {
		t.Errorf("permuted intersection keys differ: %v vs %v", a, b)
	}

	// swapped union branches, each branch itself respelled — one key
	gapped := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.AtMost(-5)),
	))
	respelled := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.AtMost(-5)),
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
	))
	ag := CanonicalKeyOf(wireSet(gapped))
	ar := CanonicalKeyOf(wireSet(respelled))
	if ag == nil || ar == nil || *ag != *ar {
		t.Errorf("swapped union keys differ: %v vs %v", ag, ar)
	}

	// ORDERED structure must NOT collapse: concatenation and
	// difference read left to right
	setA := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	setB := refinementsets.MakeRefinedSet(refinementsets.AtLeast(2), refinementsets.AtMost(3))
	concatAB := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.Concatenation(setA, setB))))
	concatBA := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.Concatenation(setB, setA))))
	if concatAB == nil || concatBA == nil || *concatAB == *concatBA {
		t.Errorf("concatenation order collapsed: %v == %v", concatAB, concatBA)
	}
	diffAB := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.Difference(setA, setB))))
	diffBA := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.Difference(setB, setA))))
	if diffAB == nil || diffBA == nil || *diffAB == *diffBA {
		t.Errorf("difference order collapsed: %v == %v", diffAB, diffBA)
	}

	// different sets keep different keys
	k1 := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(1))))
	k2 := CanonicalKeyOf(wireSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(2))))
	if k1 == nil || k2 == nil || *k1 == *k2 {
		t.Errorf("different sets share a key: %v == %v", k1, k2)
	}
}

func TestRespelledQuestionsTheKernelAnswersTheSameBitCacheCleared(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	// each ask computes IN THE KERNEL: the question cache is emptied
	// before each, so agreement here is the kernel's own, not a
	// canonical-key cache hit handing one answer to both spellings
	evens := refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2), refinementsets.Integer, refinementsets.AtLeast(0))
	evensRespelled := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer, refinementsets.MultipleOf(2))
	wide := refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1))
	ClearQuestionCache()
	first := kernel.ScalarSubset(evens, wide)
	ClearQuestionCache()
	if got := kernel.ScalarSubset(evensRespelled, wide); got != first {
		t.Errorf("respelled scalarSubset disagrees: got %v, want %v", got, first)
	}

	clash := refinementsets.MakeRefinedSet(
		refinementsets.Union(
			refinementsets.MakeRefinedSet(refinementsets.Above(0)),
			refinementsets.MakeRefinedSet(refinementsets.Below(0)),
		),
		refinementsets.OneOf([]float64{0}),
	)
	clashSwapped := refinementsets.MakeRefinedSet(
		refinementsets.OneOf([]float64{0}),
		refinementsets.Union(
			refinementsets.MakeRefinedSet(refinementsets.Below(0)),
			refinementsets.MakeRefinedSet(refinementsets.Above(0)),
		),
	)
	ClearQuestionCache()
	empty := kernel.ScalarEmpty(clash)
	ClearQuestionCache()
	if got := kernel.ScalarEmpty(clashSwapped); got != empty {
		t.Errorf("respelled scalarEmpty disagrees: got %v, want %v", got, empty)
	}
	if !empty {
		t.Errorf("scalarEmpty(clash) = %v, want true", empty)
	}

	ClearQuestionCache()
	member := kernel.Member(evens, []float64{4})
	ClearQuestionCache()
	if got := kernel.Member(evensRespelled, []float64{4}); got != member {
		t.Errorf("respelled member disagrees: got %v, want %v", got, member)
	}
	if !member {
		t.Errorf("member(evens, [4]) = %v, want true", member)
	}
}

func TestToInt32TheBitwiseOperatorsAndTheCountProduct(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	single := func(v float64) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v}))
	}

	// toInt32: truncation toward zero, the modular wrap, ±∞ → 0
	wantValues := func(t *testing.T, got TransferAnswer, want ...float64) {
		t.Helper()
		if got.Kind != TransferAnswerValues {
			t.Fatalf("Kind = %v, want values", got.Kind)
		}
		if len(got.Values) != len(want) {
			t.Fatalf("Values = %v, want %v", got.Values, want)
		}
		for i := range want {
			if got.Values[i] != want[i] {
				t.Fatalf("Values = %v, want %v", got.Values, want)
			}
		}
	}
	wantValues(t, kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: single(3.7)}), 3)
	wantValues(t, kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: single(-3.7)}), -3)
	wantValues(t, kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: single(4294967301)}), 5)
	wantValues(t, kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: single(2147483648)}), -2147483648)
	wantValues(t, kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: single(math.Inf(1))}), 0)

	// the range rule: in-window bounds truncate, integrality lands
	ranged := kernel.Transfer(TransferQuestion{
		Op: TransferOpInt32Wrap,
		A:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(0.5), refinementsets.AtMost(10.9)),
	})
	if ranged.Kind != TransferAnswerSet {
		t.Fatalf("ranged.Kind = %v, want set", ranged.Kind)
	}
	if !kernel.ScalarSubset(ranged.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10), refinementsets.Integer)) {
		t.Errorf("ranged.Set ⊆ [0,10]∩ℤ = false, want true")
	}
	// out of the window: still inside int32, always
	wide := kernel.Transfer(TransferQuestion{Op: TransferOpInt32Wrap, A: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))})
	if wide.Kind != TransferAnswerSet {
		t.Fatalf("wide.Kind = %v, want set", wide.Kind)
	}
	if !kernel.ScalarSubset(wide.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2147483648), refinementsets.AtMost(2147483647), refinementsets.Integer)) {
		t.Errorf("wide.Set ⊆ int32 = false, want true")
	}

	// the bitwise operators: exactly specified integer functions
	bit := func(op TransferQuestionOp, a, b float64) TransferAnswer {
		return kernel.Transfer(TransferQuestion{Op: op, A: single(a), B: single(b)})
	}
	wantValues(t, bit(TransferOpBitAnd, 5, 3), 1)
	wantValues(t, bit(TransferOpBitOr, 5, 3), 7)
	wantValues(t, bit(TransferOpBitXor, 5, 3), 6)
	wantValues(t, bit(TransferOpShl, 1, 4), 16)
	wantValues(t, bit(TransferOpSar, -8, 1), -4)
	wantValues(t, bit(TransferOpShr, -1, 0), 4294967295)

	// the MASK rule: an AND with one nonnegative singleton side
	// answers [0, mask] whatever the other side holds
	masked := kernel.Transfer(TransferQuestion{
		Op: TransferOpBitAnd,
		A:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(7)),
		B:  single(3),
	})
	if masked.Kind != TransferAnswerSet {
		t.Fatalf("masked.Kind = %v, want set", masked.Kind)
	}
	if !kernel.ScalarSubset(masked.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)) {
		t.Errorf("masked.Set ⊆ [0,3]∩ℤ = false, want true")
	}
	// no mask side (a NEGATIVE singleton, or none): unknown — never a
	// guess
	if got := kernel.Transfer(TransferQuestion{
		Op: TransferOpBitAnd,
		A:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(7)),
		B:  single(-1),
	}).Kind; got != TransferAnswerUnknown {
		t.Errorf("bitAnd with negative mask = %v, want unknown", got)
	}
	if got := kernel.Transfer(TransferQuestion{
		Op: TransferOpBitOr,
		A:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(7)),
		B:  single(3),
	}).Kind; got != TransferAnswerUnknown {
		t.Errorf("bitOr = %v, want unknown", got)
	}

	// the count product: exact ℕ composition — 0·unbounded is 0, where
	// the float transfer's indeterminate corner would refuse
	counts := kernel.Transfer(TransferQuestion{
		Op: TransferOpCountProduct,
		A:  refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
		B:  refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
	})
	if counts.Kind != TransferAnswerSet {
		t.Fatalf("counts.Kind = %v, want set", counts.Kind)
	}
	if !kernel.ScalarSubset(counts.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)) {
		t.Errorf("counts.Set ⊆ ℕ = false, want true")
	}
	bounded := kernel.Transfer(TransferQuestion{
		Op: TransferOpCountProduct,
		A:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
		B:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3})),
	})
	if bounded.Kind != TransferAnswerSet {
		t.Fatalf("bounded.Kind = %v, want set", bounded.Kind)
	}
	if !kernel.ScalarSubset(bounded.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(6), refinementsets.AtMost(6), refinementsets.Integer)) {
		t.Errorf("bounded.Set ⊆ {6} = false, want true")
	}
}

// TestTransferOverAnImpossibleOperandAnswersTheEmptySet exercises the
// real kernel boundary the way boundary/exports.lean's kernelTransfer
// takes it: an operand spelled as the empty OneOf (impossibleSet — see
// its own doc comment for why the crossed-bound spelling this test
// once used does NOT reach the intercept) decodes to Enclosure.bottom
// before any arithmetic runs (kernelTransfer's own comment: "A BOTTOM
// operand … is caught HERE, before any proved transfer function
// runs"), and the answer crosses back as encodeEnclosure's bottom
// spelling — {"kind":"set","set":{"forms":[{"form":"oneOf","w":[]}]}} —
// which this test's own ask1/Answered/DecodeTransferAnswer/DecodeWireSet
// chain must read back as the same empty OneOf the Go encoder sends.
// This is the empty-set wire round-trip proven against the live
// kernel, not merely against the Go encoder's own output.
func TestTransferOverAnImpossibleOperandAnswersTheEmptySet(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	five := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))
	got := kernel.Transfer(TransferQuestion{Op: TransferOpAdd, A: impossibleSet(), B: five})
	if got.Kind != TransferAnswerSet {
		t.Fatalf("add(impossible, {5}).Kind = %v, want set", got.Kind)
	}
	want := refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))
	if !kernel.ScalarEmpty(got.Set) {
		t.Errorf("add(impossible, {5}).Set is not scalarEmpty, want the empty set")
	}
	if len(got.Set.Forms) != 1 || got.Set.Forms[0].Form != refinementsets.FormOneOf || len(got.Set.Forms[0].W) != 0 {
		t.Errorf("add(impossible, {5}).Set = %+v, want the empty OneOf %+v", got.Set, want)
	}
}

func TestKernelIssuedNarrowingTheConditionsSetsBothSidesStrengths(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	sameSet := func(a, b refinementsets.RefinedSet) bool {
		ka := CanonicalKeyOf(wireSet(a))
		kb := CanonicalKeyOf(wireSet(b))
		return ka != nil && kb != nil && *ka == *kb
	}

	// x >= 5: truth proves real-and-at-least (strong); falsity only
	// narrows what is already real (weak) — NaN compares false
	ge := kernel.Narrow(NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpGe, K: 5})
	if ge.WhenTrue == nil || !ge.WhenTrue.Strong {
		t.Fatalf("ge.WhenTrue = %+v, want strong", ge.WhenTrue)
	}
	if !sameSet(ge.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(5))) {
		t.Errorf("ge.WhenTrue.Set mismatch")
	}
	if ge.WhenFalse == nil || ge.WhenFalse.Strong {
		t.Fatalf("ge.WhenFalse = %+v, want weak", ge.WhenFalse)
	}
	if !sameSet(ge.WhenFalse.Set, refinementsets.MakeRefinedSet(refinementsets.Below(5))) {
		t.Errorf("ge.WhenFalse.Set mismatch")
	}

	// a conjunction: truth intersects (strong); falsity is the union
	// of the refutations (weak — which conjunct failed is unknown)
	geZero := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpGe, K: 0}
	ltTen := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpLt, K: 10}
	band := kernel.Narrow(NarrowTree{Kind: NarrowKindAnd, A: &geZero, B: &ltTen})
	if !sameSet(band.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Below(10))) {
		t.Errorf("band.WhenTrue.Set mismatch")
	}
	if !band.WhenTrue.Strong {
		t.Errorf("band.WhenTrue.Strong = false, want true")
	}
	if !sameSet(band.WhenFalse.Set, refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Below(0)), refinementsets.MakeRefinedSet(refinementsets.AtLeast(10)),
	))) {
		t.Errorf("band.WhenFalse.Set mismatch")
	}
	if band.WhenFalse.Strong {
		t.Errorf("band.WhenFalse.Strong = true, want false")
	}

	// negation swaps the sides AND the strengths: !(x < 0) true is
	// NaN-satisfiable, so its claim is the weak one
	ltZero := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpLt, K: 0}
	bang := kernel.Narrow(NarrowTree{Kind: NarrowKindNot, A: &ltZero})
	if !sameSet(bang.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))) {
		t.Errorf("bang.WhenTrue.Set mismatch")
	}
	if bang.WhenTrue.Strong {
		t.Errorf("bang.WhenTrue.Strong = true, want false")
	}
	if !bang.WhenFalse.Strong {
		t.Errorf("bang.WhenFalse.Strong = false, want true")
	}

	// an equality disjunction: both branches strong, so the union is
	eqThree := NarrowTree{Kind: NarrowKindEq, K: 3}
	eqFive := NarrowTree{Kind: NarrowKindEq, K: 5}
	either := kernel.Narrow(NarrowTree{Kind: NarrowKindOr, A: &eqThree, B: &eqFive})
	if !either.WhenTrue.Strong {
		t.Errorf("either.WhenTrue.Strong = false, want true")
	}
	if !sameSet(either.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3})), refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5})),
	))) {
		t.Errorf("either.WhenTrue.Set mismatch")
	}
	// the refuted side: real and different from both
	if either.WhenFalse.Strong {
		t.Errorf("either.WhenFalse.Strong = true, want false")
	}
	if !sameSet(either.WhenFalse.Set, refinementsets.MakeRefinedSet(
		refinementsets.Difference(refinementsets.Numbers, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))),
		refinementsets.Difference(refinementsets.Numbers, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))),
	)) {
		t.Errorf("either.WhenFalse.Set mismatch")
	}

	// Number.isNaN: truth claims no set; falsity claims ℝ̄ STRONGLY —
	// the one falsity that itself proves the value real
	nanAnswer := kernel.Narrow(NarrowTree{Kind: NarrowKindIsNaN})
	if nanAnswer.WhenTrue != nil {
		t.Errorf("nan.WhenTrue = %+v, want nil", nanAnswer.WhenTrue)
	}
	if !nanAnswer.WhenFalse.Strong {
		t.Errorf("nan.WhenFalse.Strong = false, want true")
	}
	if !sameSet(nanAnswer.WhenFalse.Set, refinementsets.Numbers) {
		t.Errorf("nan.WhenFalse.Set mismatch")
	}

	// an unreadable conjunct: truth still narrows through the read
	// side; falsity claims nothing (the failure may be the other's)
	geZero2 := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpGe, K: 0}
	other := NarrowTree{Kind: NarrowKindOther}
	mixed := kernel.Narrow(NarrowTree{Kind: NarrowKindAnd, A: &geZero2, B: &other})
	if !sameSet(mixed.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))) {
		t.Errorf("mixed.WhenTrue.Set mismatch")
	}
	if mixed.WhenFalse != nil {
		t.Errorf("mixed.WhenFalse = %+v, want nil", mixed.WhenFalse)
	}

	// x % 2 === 0: the exact-remainder test
	even := kernel.Narrow(NarrowTree{Kind: NarrowKindModZero, D: 2})
	if !even.WhenTrue.Strong {
		t.Errorf("even.WhenTrue.Strong = false, want true")
	}
	if !sameSet(even.WhenTrue.Set, refinementsets.MakeRefinedSet(refinementsets.MultipleOf(2))) {
		t.Errorf("even.WhenTrue.Set mismatch")
	}

	// a string equality: the tuple singleton and its difference,
	// decided by membership on the answered sets themselves
	on := codePoints("on")
	seq := kernel.Narrow(NarrowTree{Kind: NarrowKindEqSeq, Points: on})
	if !seq.WhenTrue.Strong {
		t.Errorf("seq.WhenTrue.Strong = false, want true")
	}
	if !sameSet(seq.WhenTrue.Set, refinementsets.StringTuple("on")) {
		t.Errorf("seq.WhenTrue.Set mismatch")
	}
	if !sameSet(seq.WhenFalse.Set, refinementsets.MakeRefinedSet(refinementsets.Difference(refinementsets.Strings, refinementsets.StringTuple("on")))) {
		t.Errorf("seq.WhenFalse.Set mismatch")
	}
	if !kernel.Member(seq.WhenTrue.Set, on) {
		t.Errorf("member(seq.WhenTrue.Set, on) = false, want true")
	}
	if kernel.Member(seq.WhenFalse.Set, on) {
		t.Errorf("member(seq.WhenFalse.Set, on) = true, want false")
	}
	off := codePoints("off")
	if !kernel.Member(seq.WhenFalse.Set, off) {
		t.Errorf("member(seq.WhenFalse.Set, off) = false, want true")
	}

	// the worlds do not mix: a tree testing one place both numerically
	// and as a string is declined, never guessed at
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("mixed-world narrow did not panic")
			}
		}()
		geZero3 := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpGe, K: 0}
		eqSeqOn := NarrowTree{Kind: NarrowKindEqSeq, Points: on}
		kernel.Narrow(NarrowTree{Kind: NarrowKindAnd, A: &geZero3, B: &eqSeqOn})
	}()
}

func codePoints(s string) []float64 {
	runes := []rune(s)
	out := make([]float64, len(runes))
	for i, r := range runes {
		out[i] = float64(r)
	}
	return out
}
