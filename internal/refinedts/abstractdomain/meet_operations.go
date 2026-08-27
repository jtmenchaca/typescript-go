// The MEET operation over abstract values: two claims both true of the
// same runtime value collapse to their conjunction — exact where both
// sides are known, keeping repetition claims as one repetition form
// rather than a raw form concatenation.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MeetKnown is meetKnown in the TS source.
func MeetKnown(a, b AbstractValue) AbstractValue {
	if a.Kind == KindUnknown {
		return b
	}
	if b.Kind == KindUnknown {
		return a
	}
	// a claim that excludes absence drops the other side's absent
	// half: defined ∧ (X ∨ absent) is X
	if a.Kind == KindPossiblyUndefined && b.Kind != KindPossiblyUndefined {
		return MeetKnown(*a.Inner, b)
	}
	if b.Kind == KindPossiblyUndefined && a.Kind != KindPossiblyUndefined {
		return MeetKnown(a, *b.Inner)
	}
	// a claim that is a plain SET drops the other side's NaN half, for
	// the same reason the absence arm above drops the absent half: no
	// refinement set holds NaN (refinement_forms.go), so a side that
	// states a set states "not NaN" along with it, and the conjunction
	// of "X, or NaN" with a set is that set met with X.
	//
	// This is what lets a guard on a possibly-NaN read narrow it:
	// `d.getTime()` reads as "an integer time value, or NaN", and
	// `if (d.getTime() === 100)` proves the run is one where it is 100
	// — NaN passes no comparison (sec-strict-equality-comparison), so
	// the guard's own set is the whole answer on that arm.
	if a.Kind == KindPossiblyNaN && a.Inner != nil && (b.Kind == KindSet || b.Kind == KindValues) {
		return MeetKnown(*a.Inner, b)
	}
	if b.Kind == KindPossiblyNaN && b.Inner != nil && (a.Kind == KindSet || a.Kind == KindValues) {
		return MeetKnown(a, *b.Inner)
	}
	if a.Kind == KindValues {
		return a
	}
	if b.Kind == KindValues {
		return b
	}
	// a PINNED NaN met against a plain KindSet: the set is a refinement
	// (refinementsets.RefinedSet's ray/star forms never mention NaN —
	// see refinement_forms.go's IsNumberGround comment), so no bare
	// KindSet EVER admits NaN as a member. A call-site join that
	// contributes pinned NaN here is an ILL-TYPED caller (entryStateMeet's
	// own doc: "a caller handing a value the annotation excludes is
	// tsc's own error, never a fact this walk inherits" — the same rule
	// the KindKindUnion arm above already applies arm-by-arm), so the
	// declared side stands alone, exactly as an excluded union arm
	// already drops out above. Without this arm the meet fell through
	// to the bottom `return a`, which handed the well-typed body an
	// entry value of PINNED NaN sourced from a DIFFERENT, ill-typed
	// call site -- surfacing as a false "returned value of type NaN"
	// refutation on a position no real caller of this function ever
	// sends NaN through.
	if a.Kind == KindNaN && b.Kind == KindSet {
		return b
	}
	if b.Kind == KindNaN && a.Kind == KindSet {
		return a
	}
	// two object-stars over the same runtime value: both claims hold of
	// every position, so the position holds their MEET. The length is
	// unstated on both sides, so there is nothing to reconcile there.
	if a.Kind == KindObjectStar && b.Kind == KindObjectStar {
		elementA, okA := ElementOfObjectStar(a)
		elementB, okB := ElementOfObjectStar(b)
		if okA && okB {
			if met, ok := KnownObjectStar(MeetKnown(elementA, elementB), MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b))); ok {
				return met
			}
		}
		return a
	}
	// an object-star met with an exact LIST: the list already states
	// both the count and each slot, which is everything the star says
	// and more, so the list is the meet outright. (The star's element
	// claim is true of the list's slots by hypothesis — both describe
	// the same runtime value.)
	if a.Kind == KindObjectStar && b.Kind == KindList {
		return b
	}
	if b.Kind == KindObjectStar && a.Kind == KindList {
		return a
	}
	// array-holes met with an exact LIST of the SAME length: the list
	// already states every slot (holes and all), which is everything
	// array-holes says and no less — both describe the same runtime
	// value by hypothesis, so the list is the meet outright, keeping
	// whichever slot knowledge it carries.
	if a.Kind == KindArrayHoles && b.Kind == KindList {
		if length, ok := LengthOfArrayHoles(a); ok && length == len(b.Items) {
			return b
		}
	}
	if b.Kind == KindArrayHoles && a.Kind == KindList {
		if length, ok := LengthOfArrayHoles(b); ok && length == len(a.Items) {
			return a
		}
	}
	if a.Kind == KindSet && b.Kind == KindSet &&
		a.Temporal == nil && b.Temporal == nil &&
		a.SetKindTag == SetKindTagNone && b.SetKindTag == SetKindTagNone {
		measures := a.Measures
		if measures == nil {
			measures = b.Measures
		}
		grade := MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b))
		if met, ok := metRepetition(a.Set, b.Set); ok {
			out := KnownWithMeasures(KnownSet(met, nil, grade, SetKindTagNone), measures)
			// the meet is two claims BOTH true of the same runtime value
			// (this function's own header), so a density proof either side
			// carries stands on its own regardless of the other side's
			// silence — the same "either side" reading this function
			// already gives Measures two lines up. A window EITHER side
			// proved dense meets to a NARROWER (or equal) window over the
			// same physical indices, which the proving side already
			// affirmed present.
			if (a.SeqDenseKnown && a.SeqDense) || (b.SeqDenseKnown && b.SeqDense) {
				out = KnownSetDense(out)
			}
			return out
		}
		combined := append(append([]refinementsets.Refinement{}, a.Set.Forms...), b.Set.Forms...)
		// CANONICALIZE the combined conjunction before it rides onward —
		// the same hygiene JoinKnown's own scalar fallback already runs
		// (line ~1145, "CanonicalScalarForms first: a meet-concatenated
		// side wears duplicate conjuncts and vacuous +-inf bounds that
		// blind the run collapse"). A caller's exact recovered value
		// ({200}) met with a GROUNDED declared return type (R-bar,
		// AtLeast(-Inf)) built this exact two-conjunct shape for the
		// first time once bare `number` began grounding
		// (annotations/type_node_sets.go's primitive-keyword arm):
		// {200} ∩ R-bar denotes exactly {200}, but the uncanonicalized
		// spelling kept AtLeast(-Inf) riding as a second, vacuous
		// conjunct — a value a reader checking len(Forms)==1 for
		// exactness then read as no longer exact, though nothing about
		// the DENOTED set had widened. CanonicalScalarForms drops a
		// vacuous bound beside any other conjunct (canonical_forms.go's
		// isVacuousBound), restoring the single-form spelling without
		// changing which values the set holds.
		return KnownWithMeasures(
			KnownSet(
				refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(combined...)),
				nil,
				grade,
				SetKindTagNone,
			),
			measures,
		)
	}
	return a
}

// metRepetition is the meet of two SEQUENCE claims that both hold of the
// same runtime value, kept as ONE repetition form instead of the plain
// form concatenation the scalar arm above uses.
//
// Concatenating is sound but unreadable: every repetition reader in the
// tree (refinementsets.AsRepetition, and so ElementOf, the `.length`
// read, MapOutcome's window carry) requires the set to hold exactly one
// form, and declines a two-form conjunction outright. So a computed
// sequence met with its own declared type — the inline parameter binding
// meets the caller's value with the parameter's annotation
// (BoundParameterKnown) — lost every length fact it arrived with the
// moment `xs: number[]` restated it as a star. That is a claim DROPPED by
// the meet, which entryStateMeet's contract says never happens: the
// annotation is a ceiling and a value inside it passes through whole.
//
// Both sides describe the same value, so: every position satisfies both
// element claims (the elements MEET, their forms conjoined the same way
// the scalar arm conjoins), and the length satisfies both windows (the
// windows INTERSECT). An empty intersection means no such value exists,
// which is a contradiction this layer does not spell — the caller keeps
// the plain concatenation there, exactly as before.
//
// (zero, false) whenever either side is not a lone repetition, so every
// shape but this one takes the unchanged path.
func metRepetition(a, b refinementsets.RefinedSet) (refinementsets.RefinedSet, bool) {
	repA, okA := refinementsets.AsRepetition(a)
	repB, okB := refinementsets.AsRepetition(b)
	if !okA || !okB {
		return refinementsets.RefinedSet{}, false
	}
	lo := repA.Lo
	if repB.Lo > lo {
		lo = repB.Lo
	}
	var hi *int
	switch {
	case repA.Hi == nil:
		hi = repB.Hi
	case repB.Hi == nil:
		hi = repA.Hi
	case *repA.Hi <= *repB.Hi:
		hi = repA.Hi
	default:
		hi = repB.Hi
	}
	// an empty window states that no value is on either side at once;
	// this layer has no spelling for that, so the caller's plain
	// concatenation stands
	if hi != nil && *hi < lo {
		return refinementsets.RefinedSet{}, false
	}
	// CANONICALIZE the merged element before it becomes the repetition's
	// own element — the same hygiene the sibling combined-forms path
	// below already runs (its own comment on line ~128), and for the
	// identical reason: repA.Element and repB.Element are very often
	// the SAME set arriving from two separate walk passes (a codepoint
	// alphabet met with itself, say), and appending their forms
	// unconditionally wears a duplicate conjunct every time this meet
	// runs. A loop whose body re-derives the same star-of-codepoints
	// claim on every iteration (ReduceCSSCalc.ts's evaluateExpression:
	// calculateParentheses's own while-loop output fed through a SECOND
	// calculateArithmetic call) then meets that claim against itself
	// repeatedly, and without this fold the element list grows by one
	// duplicate conjunct pair PER MEET with nothing folding it back down
	// — measured directly: the minimized reproducer's kernel seqSubset
	// ask carries a Star whose element repeats the same (integer, union)
	// pair two, then four, then six times across three successive asks,
	// and the kernel's own derivative engine, which walks every node in
	// that list once per step (refined_sets/automata.lean), never
	// returns on the third. CanonicalScalarForms drops the repeated
	// conjunct without changing which values the merged element admits.
	element := refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(
		append(append([]refinementsets.Refinement{}, repA.Element.Forms...),
			repB.Element.Forms...)...,
	))
	built, ok := repetitionOrNothing(element, lo, hi)
	if !ok {
		return refinementsets.RefinedSet{}, false
	}
	return built, true
}

// repetitionOrNothing wraps refinementsets.Repetition's construction
// refusals (it panics on a window it cannot build) as a (value, ok) pair
// — the same reading TightenRepetition's own repetitionSafe takes.
func repetitionOrNothing(element refinementsets.RefinedSet, lo int, hi *int) (result refinementsets.RefinedSet, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return refinementsets.Repetition(element, lo, hi), true
}
