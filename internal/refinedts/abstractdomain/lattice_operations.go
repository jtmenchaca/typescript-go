// The LATTICE operations over abstract values — join, meet,
// truthiness, structural equality, the denoted set, and provenance-
// carrying unknowns. "Join" and "meet" are the lattice-theory
// standard; the join is EXACT (union forms the kernel decides), and
// conformance holds it to the kernel's joinState.

package abstractdomain

import (
	"math"

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
			return KnownWithMeasures(KnownSet(met, nil, grade, SetKindTagNone), measures)
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

// Truthiness is truthiness in the TS source.
//
// The three-valued result (true / false / unknown) is (bool, bool):
// (value, known).
func Truthiness(k AbstractValue) (bool, bool) {
	switch k.Kind {
	case KindValues:
		if k.KindTag == PrimitiveString {
			return len(k.Values) > 0, true
		}
		if k.KindTag == PrimitiveArray {
			return true, true // arrays are objects
		}
		if len(k.Values) == 1 {
			return k.Values[0] != 0, true
		}
		return false, false
	case KindObject, KindList, KindCollection, KindPromise, KindDate, KindRegex:
		return true, true // an object — always truthy
	case KindObjectStar, KindArrayHoles:
		// an Array is an Object, and ToBoolean maps every Object to true
		// (sec-toboolean: only undefined, null, false, ±0, NaN, "" and 0n
		// are false). An EMPTY array is still an object, so the unstated
		// (or stated-zero) length changes nothing here.
		return true, true
	case KindSymbol:
		return true, true // every symbol is truthy
	case KindHostFunction:
		return true, true // every function object is truthy
	case KindBigints:
		// 0n is the one falsy bigint
		if len(k.BigintValues) == 1 {
			return k.BigintValues[0] != 0, true
		}
		return false, false
	case KindNaN:
		return false, true
	case KindUndef:
		return false, true
	case KindNull:
		// sec-toboolean: null is one of the seven falsy values
		return false, true
	case KindKindUnion:
		// decided only when EVERY arm agrees. A kindUnion with zero arms
		// does not occur in practice (KindUnionOf collapses that case to
		// Unknown before construction); this guard keeps the function
		// total (the TS source's `verdicts[0]` on an empty array reads
		// as `undefined`, which the surrounding ternary then treats as
		// non-null and returns as-is — behavior this port does not
		// reproduce since it is unreached).
		if len(k.Arms) == 0 {
			return false, false
		}
		first, firstKnown := Truthiness(k.Arms[0])
		if !firstKnown {
			return false, false
		}
		for _, arm := range k.Arms {
			v, known := Truthiness(arm)
			if !known || v != first {
				return false, false
			}
		}
		return first, true
	case KindSet, KindVariable, KindPossiblyUndefined, KindPossiblyNaN, KindUnknown:
		return false, false
	default:
		return false, false
	}
}

// SameKnown is sameKnown in the TS source: structural sameness. (Not
// JSON: an object's stated annotation can carry compiler symbols; it
// compares by identity.)
func SameKnown(a, b AbstractValue) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindValues:
		return a.KindTag == b.KindTag && sameFloats(a.Values, b.Values)
	case KindSet:
		return sameRefinedSet(a.Set, b.Set) &&
			sameTemporal(a.Temporal, b.Temporal) &&
			a.SetKindTag == b.SetKindTag
	case KindObject:
		if a.Stated != b.Stated {
			return false
		}
		if a.Complete != b.Complete {
			return false
		}
		if a.BareProto != b.BareProto {
			return false
		}
		if len(a.Keys) != len(b.Keys) {
			return false
		}
		for _, key := range a.Keys {
			other, ok := lookupKey(b.Keys, key.Name)
			if !ok || !SameKnown(key.Value, other) {
				return false
			}
		}
		return true
	case KindVariable:
		// the variable, by identity — its compiler symbol
		return a.Symbol == b.Symbol && a.StarDepth == b.StarDepth
	case KindList:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i, item := range a.Items {
			if !SameKnown(item, b.Items[i]) {
				return false
			}
		}
		return true
	case KindObjectStar:
		// the element is the whole claim — neither side states a length,
		// so two stars over the same element say the same thing
		if a.Inner == nil || b.Inner == nil {
			return a.Inner == b.Inner
		}
		return SameKnown(*a.Inner, *b.Inner)
	case KindArrayHoles:
		// the length (wrapped in Inner, {n}) and the always-∅ element
		// set say the same thing for a dense and a sparse array-holes
		// alike, but Dense/DenseKnown is a real observable difference
		// (Object.keys answers [] for one and n index strings for the
		// other, and answers nothing at all where density was never
		// established) — two array-holes compare equal only when the
		// length AND the density claim (proved-or-not, and if proved,
		// which way) agree.
		if a.DenseKnown != b.DenseKnown || (a.DenseKnown && a.Dense != b.Dense) {
			return false
		}
		if a.Inner == nil || b.Inner == nil {
			return a.Inner == b.Inner
		}
		return SameKnown(*a.Inner, *b.Inner)
	case KindCollection:
		if a.CollectionFlavor != b.CollectionFlavor || a.Complete != b.Complete {
			return false
		}
		if len(a.Entries) != len(b.Entries) {
			return false
		}
		for i, e := range a.Entries {
			if !SameKnown(e.Key, b.Entries[i].Key) || !SameKnown(e.Value, b.Entries[i].Value) {
				return false
			}
		}
		return true
	case KindUndef:
		return true
	case KindNull:
		return true
	case KindNaN:
		return true
	case KindPossiblyUndefined:
		return a.AbsentSide == b.AbsentSide && SameKnown(*a.Inner, *b.Inner)
	case KindPromise:
		return SameKnown(*a.Inner, *b.Inner)
	case KindPossiblyNaN:
		return SameKnown(*a.Inner, *b.Inner)
	case KindDate:
		return SameKnown(*a.Millis, *b.Millis)
	case KindSymbol:
		return a.HasSymbolKey == b.HasSymbolKey && a.SymbolKey == b.SymbolKey &&
			a.HasDescription == b.HasDescription && a.Description == b.Description
	case KindHostFunction:
		return true // the sort is the whole claim
	case KindBigints:
		if len(a.BigintValues) != len(b.BigintValues) {
			return false
		}
		for i, v := range a.BigintValues {
			if v != b.BigintValues[i] {
				return false
			}
		}
		return true
	case KindRegex:
		return a.Source == b.Source && a.Flags == b.Flags
	case KindKindUnion:
		if len(a.Arms) != len(b.Arms) {
			return false
		}
		for i, arm := range a.Arms {
			if !SameKnown(arm, b.Arms[i]) {
				return false
			}
		}
		return true
	case KindUnknown:
		// the opaque marker is provenance, not the same fact — joining
		// an opaque path with a plain-unknown one stays plain
		return a.Opaque == b.Opaque
	default:
		return false
	}
}

func lookupKey(keys []ObjectKey, name string) (AbstractValue, bool) {
	for _, key := range keys {
		if key.Name == name {
			return key.Value, true
		}
	}
	return AbstractValue{}, false
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// sameTemporal compares the TS source's `JSON.stringify(a.temporal ??
// null) === JSON.stringify(b.temporal ?? null)` — a deep-equality
// fallback the TS source reaches for because TemporalAnnotation has no
// hand-written equality. refinementsets.TemporalAnnotation is a plain
// value struct (refinement_sets/calendar_interpreter.go), so a Go value
// comparison over the dereferenced structs is the same deep-equality
// check without a JSON round trip.
func sameTemporal(a, b *refinementsets.TemporalAnnotation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameRefinedSet compares the TS source's `JSON.stringify(a.set) ===
// JSON.stringify(b.set)` structurally, since refinementsets.RefinedSet
// holds pointer fields (A_, B) that a Go `==` cannot compare.
func sameRefinedSet(a, b refinementsets.RefinedSet) bool {
	if len(a.Forms) != len(b.Forms) {
		return false
	}
	for i, f := range a.Forms {
		if !sameRefinement(f, b.Forms[i]) {
			return false
		}
	}
	return true
}

func sameRefinement(a, b refinementsets.Refinement) bool {
	if a.Form != b.Form {
		return false
	}
	if a.A != b.A {
		return false
	}
	if !sameFloats(a.W, b.W) {
		return false
	}
	if (a.A_ == nil) != (b.A_ == nil) {
		return false
	}
	if a.A_ != nil && !sameRefinedSet(*a.A_, *b.A_) {
		return false
	}
	if (a.B == nil) != (b.B == nil) {
		return false
	}
	if a.B != nil && !sameRefinedSet(*a.B, *b.B) {
		return false
	}
	if a.Lo != b.Lo {
		return false
	}
	if (a.Hi == nil) != (b.Hi == nil) {
		return false
	}
	if a.Hi != nil && *a.Hi != *b.Hi {
		return false
	}
	return true
}

// SetOfKnown is setOfKnown in the TS source: the refined set a
// knowledge state denotes: a single value is its singleton, a tuple of
// values the concatenation of singletons. A variable denotes its BOUND
// (T ⊆ bound, and every finite subset of the bound is an admissible T,
// so the bound is exact for the universal reading).
func SetOfKnown(k AbstractValue) (refinementsets.RefinedSet, bool) {
	switch k.Kind {
	case KindObject:
		return refinementsets.RefinedSet{}, false // objects live in the graph, not the tuple layer
	case KindObjectStar:
		// the star of an object element: the elements are graph values,
		// so there is no member set to put at a position and no tuple the
		// kernel could decide. This refusal is what keeps the form off
		// every kernel question — the wire carries sets, and this states
		// none.
		return refinementsets.RefinedSet{}, false
	case KindList, KindCollection, KindPromise, KindDate:
		return refinementsets.RefinedSet{}, false // nested structure the tuple layer cannot formatAt
	case KindArrayHoles:
		// every slot is the absent value, and undefined leaves ℝ̄ — no
		// tuple-layer set holds a hole, the same refusal KindUndef makes.
		// This deliberately does NOT return k.ElementSet (∅): ElementSet
		// is a claim about which VALUES are present at some position, not
		// a claim that the array itself is a member of that set — SetOfKnown
		// asks the latter, and answering ∅ here would let an assignability
		// check see ∅ ⊆ every target and wrongly accept the array against
		// any annotation (known_constructors.go's KnownArrayHoles doc).
		return refinementsets.RefinedSet{}, false
	case KindVariable:
		set := k.Bound
		for i := 0; i < k.StarDepth; i++ {
			set = refinementsets.MakeRefinedSet(refinementsets.Star(set))
		}
		return set, true
	case KindUndef, KindNull, KindPossiblyUndefined, KindPossiblyNaN, KindSymbol,
		KindHostFunction, KindBigints, KindRegex:
		// absence (undefined or null), NaN, symbols, functions, bigints
		// leave ℝ̄
		return refinementsets.RefinedSet{}, false
	case KindKindUnion:
		// a union of SAME-SORT value tuples denotes the union of its
		// arms' sets — `"axis" | "item"` is the two-word set, `1 | 2`
		// the two-number one. Arms of DIFFERENT sorts stay refused: a
		// one-letter word and a number spell the same double, so one
		// untagged set would let each arm read the other's members.
		if len(k.Arms) == 0 {
			return refinementsets.RefinedSet{}, false
		}
		tag := k.Arms[0].KindTag
		var union *refinementsets.RefinedSet
		for _, arm := range k.Arms {
			if arm.Kind != KindValues || arm.KindTag != tag {
				return refinementsets.RefinedSet{}, false
			}
			armSet, armOk := SetOfKnown(arm)
			if !armOk {
				return refinementsets.RefinedSet{}, false
			}
			if union == nil {
				first := armSet
				union = &first
			} else {
				combined := refinementsets.MakeRefinedSet(refinementsets.Union(*union, armSet))
				union = &combined
			}
		}
		return *union, true
	case KindNaN:
		return refinementsets.RefinedSet{}, false // NaN is not an element of ℝ̄ — no set holds it
	case KindSet:
		// a worn set's members are not doubles — it stays out of ℝ̄
		if k.SetKindTag == SetKindTagNone {
			return k.Set, true
		}
		return refinementsets.RefinedSet{}, false
	case KindValues:
		if len(k.Values) == 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[0]})), true
		}
		var set *refinementsets.RefinedSet
		for i := len(k.Values) - 1; i >= 0; i-- {
			singleton := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[i]}))
			if set == nil {
				set = &singleton
			} else {
				combined := refinementsets.MakeRefinedSet(refinementsets.Concatenation(singleton, *set))
				set = &combined
			}
		}
		// the empty tuple has no 1-tuple spelling; leave it to membership
		if set == nil {
			return refinementsets.RefinedSet{}, false
		}
		return *set, true
	case KindUnknown:
		return refinementsets.RefinedSet{}, false
	default:
		return refinementsets.RefinedSet{}, false
	}
}

// UnknownOver is unknownOver in the TS source: the unknown result of an
// operation, wearing outside provenance when every operand that failed
// to pin it is itself opaque: a computation over only outside-determined
// inputs is determined outside too. One PLAIN unknown operand keeps the
// result plain — the walk's own gap stays visible as one.
func UnknownOver(operands []AbstractValue) AbstractValue {
	sawOpaque := false
	for _, operand := range operands {
		if operand.Kind == KindUnknown {
			if !operand.Opaque {
				return Unknown
			}
			sawOpaque = true
		}
	}
	if sawOpaque {
		return Opaque
	}
	return Unknown
}

// noScalarReread is the reread-safety gate the string-word join rests
// on: does the set's language MISS the 1-tuple layer entirely, so no
// member can be reread as a bare number at a scalar position?
//
// The KERNEL answers first (refined_seq_no_scalar_reread, proved by
// noScalarRereadF_sound). Only where it refuses does the local
// recursion below stand in — and the local one is strictly weaker in
// one direction that matters: it admits a Concatenation WITHOUT
// inspecting its operands, so `Concatenation (OneOf [w]) EmptyTuple` —
// a genuine 1-tuple — passes locally and is refused by the kernel. The
// kernel is therefore never weaker here, and asking it first can only
// tighten the gate.
func noScalarReread(set refinementsets.RefinedSet) bool {
	if safe, ok := kernelNoScalarReread(set); ok {
		return safe
	}
	return statesOnlyLongSequences(set)
}

// statesOnlyLongSequences is the TS source's statesOnlyLongSequences:
// whether every member the set admits is a sequence of length two or
// more — every form spells tuples, and no tuple is short enough to
// double as a scalar.
//
// THE DECLINE FALLBACK ONLY — noScalarReread above asks the kernel
// first and reaches this recursion only where the kernel refused. It
// walks the kernel's own grammar by hand and admits a concatenation
// without checking its operands (see noScalarReread's note), which the
// proved decider does not.
func statesOnlyLongSequences(set refinementsets.RefinedSet) bool {
	if len(set.Forms) == 0 {
		return false
	}
	for _, f := range set.Forms {
		if f.Form == refinementsets.FormConcatenation {
			continue
		}
		// the empty tuple is the concatenation identity — no scalar
		// tuple at all, so there is no length to reread as a scalar,
		// the same argument stringWordSet's own len==0 gate makes for
		// the empty word on the KindValues side
		if f.Form == refinementsets.FormEmptyTuple {
			continue
		}
		if f.Form == refinementsets.FormUnion &&
			statesOnlyLongSequences(*f.A_) && statesOnlyLongSequences(*f.B) {
			continue
		}
		return false
	}
	return true
}

// stringWordSet is the TS source's stringWordSet: the side's strings as
// a set, where the side is DEMONSTRABLY a string-sorted word of length
// two or more (its tuple can never be reread as a scalar), the EMPTY
// word (the concatenation identity — no tuple at all, so there is no
// value to misread as a scalar; the admitted-language rule guards
// against a 1-tuple rereading, which the empty tuple cannot do), or an
// untagged set all of whose members are such sequences. (nil, false)
// anywhere else. A length-one word is still refused: THAT tuple is the
// one a scalar position could reread.
func stringWordSet(k AbstractValue) (refinementsets.RefinedSet, bool) {
	if k.Kind == KindValues && k.KindTag == PrimitiveString {
		if len(k.Values) >= 2 || len(k.Values) == 0 {
			return refinementsets.StringTuple(stringOf(k.Values)), true
		}
		return refinementsets.RefinedSet{}, false
	}
	if k.Kind == KindSet && k.SetKindTag == SetKindTagNone && noScalarReread(k.Set) {
		return k.Set, true
	}
	return refinementsets.RefinedSet{}, false
}

// stringSideSet reads a value's plain RefinedSet where the value is
// DEMONSTRABLY string-sorted — an exact string word (any length,
// including a single codepoint) or an untagged set whose forms state a
// sequence (refinementsets.StatesSequence — the SAME structural test
// fact_export.go's caseOfSet already reads a string case off of).
// Unlike stringWordSet, this carries NO 1-tuple-reread restriction: it
// exists only for STRING-GROUND ABSORPTION below, which returns one
// side's set UNCHANGED (never builds a new word-union set), so a
// 1-character member already stated by that side introduces no fresh
// scalar-reread risk the side did not already carry on its own.
// (nil, false) for anything else — a number-sorted set, a tagged set,
// a non-string KindValues word.
func stringSideSet(k AbstractValue) (refinementsets.RefinedSet, bool) {
	if k.Kind == KindValues && k.KindTag == PrimitiveString {
		return refinementsets.StringTuple(stringOf(k.Values)), true
	}
	if k.Kind == KindSet && k.SetKindTag == SetKindTagNone && refinementsets.StatesSequence(k.Set) {
		return k.Set, true
	}
	return refinementsets.RefinedSet{}, false
}

// absentFlavorOf reads the AbsentFlavor a value's own absent side
// carries, and whether the value HAS an absent side to carry at all
// (ok=false for a plain present value — that side contributes no
// absent admission to a join, so it must not enter joinAbsentFlavor,
// whose own zero value already means something else: "either
// admission").
func absentFlavorOf(k AbstractValue) (flavor AbsentFlavor, ok bool) {
	switch k.Kind {
	case KindUndef:
		return AbsentFlavorUndefOnly, true
	case KindNull:
		return AbsentFlavorNullOnly, true
	case KindPossiblyUndefined:
		return k.AbsentSide, true
	default:
		return AbsentFlavorConflated, false
	}
}

// joinAbsentFlavor is the flavor lattice JoinKnown's wrapper-building
// arms thread: the same flavor joined with itself stays that flavor,
// UndefOnly joined with NullOnly (either order) is conflated — the
// joined value's absent side may be either admission now. A side with
// no absent side of its own (ok=false — a plain present value) does
// not enter the join at all: the OTHER side's flavor is the whole
// answer, since only one side is actually contributing an absence.
func joinAbsentFlavor(a AbsentFlavor, aOK bool, b AbsentFlavor, bOK bool) AbsentFlavor {
	if !aOK {
		return b
	}
	if !bOK {
		return a
	}
	if a == b {
		return a
	}
	return AbsentFlavorConflated
}

// JoinKnown is joinKnown in the TS source: the looser of two facts
// about one name — exact where both sides are known (the union form for
// sets, key-wise for objects), unknown where either is.
func JoinKnown(a, b AbstractValue) AbstractValue {
	// either path may run, so the joined grade is the weaker one
	grade := MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b))
	if SameKnown(a, b) {
		return AtTrustLevel(a, grade)
	}
	// null joined with undefined (either order): a branch that returns
	// null and a branch that returns undefined join to "null, or
	// undefined" — which is exactly the maybe wrapper around Null, so
	// neither exact flavor is ever claimed. Must be checked BEFORE the
	// plain KindUndef arms below, or the Null side would be wrapped as
	// the value part of the wrong branch.
	if (a.Kind == KindUndef && b.Kind == KindNull) || (a.Kind == KindNull && b.Kind == KindUndef) {
		return AtTrustLevel(PossiblyAbsent(Null, AbsentFlavorConflated, "", false, false), grade)
	}
	// absence joins as the maybe wrapper — a branch returning undefined
	// and a branch returning a value join to "that value, or absent".
	// NO provenance bit here: the walk has not proved the undefined
	// branch reachable, so the joined maybe is not a derived absence.
	// The undef side alone pins the flavor UndefOnly, UNLESS the other
	// side is itself a wrapper whose own absent side must join in too.
	if a.Kind == KindUndef {
		otherFlavor, otherOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(AbsentFlavorUndefOnly, true, otherFlavor, otherOK)
		inner := b
		if b.Kind == KindPossiblyUndefined {
			inner = *b.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	if b.Kind == KindUndef {
		otherFlavor, otherOK := absentFlavorOf(a)
		flavor := joinAbsentFlavor(otherFlavor, otherOK, AbsentFlavorUndefOnly, true)
		inner := a
		if a.Kind == KindPossiblyUndefined {
			inner = *a.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	// null joins the same way as undefined does: a branch returning null
	// and a branch returning a value join to "that value, or absent" —
	// the wrapper's own absent side is exactly NullOnly, unless the
	// other side is itself a wrapper whose flavor must join in too.
	if a.Kind == KindNull {
		otherFlavor, otherOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(AbsentFlavorNullOnly, true, otherFlavor, otherOK)
		inner := b
		if b.Kind == KindPossiblyUndefined {
			inner = *b.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	if b.Kind == KindNull {
		otherFlavor, otherOK := absentFlavorOf(a)
		flavor := joinAbsentFlavor(otherFlavor, otherOK, AbsentFlavorNullOnly, true)
		inner := a
		if a.Kind == KindPossiblyUndefined {
			inner = *a.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	// NaN joins the same way on its own wrapper
	if a.Kind == KindNaN {
		return AtTrustLevel(PossiblyNaN(b), grade)
	}
	if b.Kind == KindNaN {
		return AtTrustLevel(PossiblyNaN(a), grade)
	}
	if a.Kind == KindPossiblyNaN || b.Kind == KindPossiblyNaN {
		innerA := a
		if a.Kind == KindPossiblyNaN {
			innerA = *a.Inner
		}
		innerB := b
		if b.Kind == KindPossiblyNaN {
			innerB = *b.Inner
		}
		return AtTrustLevel(PossiblyNaN(JoinKnown(innerA, innerB)), grade)
	}
	if a.Kind == KindPossiblyUndefined || b.Kind == KindPossiblyUndefined {
		innerA := a
		if a.Kind == KindPossiblyUndefined {
			innerA = *a.Inner
		}
		innerB := b
		if b.Kind == KindPossiblyUndefined {
			innerB = *b.Inner
		}
		// the provenance bit survives the join: a proved-absent side
		// keeps the joined value provably maybe-absent
		proved := (a.Kind == KindPossiblyUndefined && a.ProvedAbsent) ||
			(b.Kind == KindPossiblyUndefined && b.ProvedAbsent)
		// the flavor lattice: a side that is not itself a wrapper
		// contributes no absent side of its own (its whole claim is
		// PRESENT knowledge — the OTHER side's flavor is the join's
		// whole answer); where both sides are wrappers, their own
		// flavors join per joinAbsentFlavor
		aFlavor, aOK := absentFlavorOf(a)
		bFlavor, bOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(aFlavor, aOK, bFlavor, bOK)
		return AtTrustLevel(
			PossiblyAbsent(JoinKnown(innerA, innerB), flavor, "", false, proved),
			grade,
		)
	}
	// an unknown side degrades the join in two steps: a PLAIN unknown
	// is the walk's own gap and stays visible as one; an OPAQUE side
	// joined with anything still means the type is everything this
	// file determines — the outside provenance survives
	if a.Kind == KindUnknown || b.Kind == KindUnknown {
		plain := (a.Kind == KindUnknown && !a.Opaque) || (b.Kind == KindUnknown && !b.Opaque)
		if plain {
			return Unknown
		}
		return Opaque
	}
	// a SORT UNION joins arm-wise: a sorted claim lands on the arm its
	// sort names (number and boolean share a bucket — their words
	// share the ground), and two unions merge arm against arm.
	// Anything unplaceable degrades to unknown, the visible gap.
	if a.Kind == KindKindUnion || b.Kind == KindKindUnion {
		armsOf := func(k AbstractValue) []AbstractValue {
			if k.Kind == KindKindUnion {
				return k.Arms
			}
			return []AbstractValue{k}
		}
		bucket := func(k AbstractValue) (ClaimSort, bool) {
			s := KindOfClaim(k)
			if s == ClaimSortNone {
				return ClaimSortNone, false
			}
			if s == ClaimSortBoolean {
				return ClaimSortNumber, true
			}
			return s, true
		}
		merged := append([]AbstractValue{}, armsOf(a)...)
		for _, incoming := range armsOf(b) {
			key, ok := bucket(incoming)
			if !ok {
				return Unknown
			}
			idx := -1
			for i, held := range merged {
				if heldKey, heldOK := bucket(held); heldOK && heldKey == key {
					idx = i
					break
				}
			}
			if idx < 0 {
				merged = append(merged, incoming)
				continue
			}
			joined := JoinKnown(merged[idx], incoming)
			if joined.Kind == KindUnknown {
				return Unknown
			}
			merged[idx] = joined
		}
		for _, arm := range merged {
			if _, ok := bucket(arm); !ok {
				return Unknown
			}
		}
		return AtTrustLevel(KindUnionOf(merged), grade)
	}
	if a.Kind == KindValues && b.Kind == KindValues &&
		a.KindTag == PrimitiveBoolean && b.KindTag == PrimitiveBoolean {
		// boolean words keep their sort through a join — a union SET
		// would reread the words as numbers
		merged := append([]float64{}, a.Values...)
		for _, v := range b.Values {
			if !floatsInclude(merged, v) {
				merged = append(merged, v)
			}
		}
		return KnownValues(merged, PrimitiveBoolean, grade)
	}
	if a.Kind == KindCollection && b.Kind == KindCollection {
		// the same keys in the same insertion order join per entry;
		// anything else claims nothing — no partial-collection
		// membership (a key present on one side only may or may not
		// exist after either path)
		sameKeys := a.CollectionFlavor == b.CollectionFlavor && a.Complete && b.Complete &&
			len(a.Entries) == len(b.Entries)
		if sameKeys {
			for i, e := range a.Entries {
				if !SameKnown(e.Key, b.Entries[i].Key) {
					sameKeys = false
					break
				}
			}
		}
		if sameKeys {
			entries := make([]CollectionEntry, len(a.Entries))
			for i, e := range a.Entries {
				entries[i] = CollectionEntry{Key: e.Key, Value: JoinKnown(e.Value, b.Entries[i].Value)}
			}
			return AtTrustLevel(AbstractValue{
				Kind:             KindCollection,
				CollectionFlavor: a.CollectionFlavor,
				Entries:          entries,
				Complete:         true,
			}, grade)
		}
		return Unknown
	}
	if a.Kind == KindBigints && b.Kind == KindBigints {
		merged := append([]int64{}, a.BigintValues...)
		for _, v := range b.BigintValues {
			found := false
			for _, m := range merged {
				if m == v {
					found = true
					break
				}
			}
			if !found {
				merged = append(merged, v)
			}
		}
		return AtTrustLevel(AbstractValue{Kind: KindBigints, BigintValues: merged}, grade)
	}
	// two OBJECT-STARS join element-wise: the runtime value came through
	// one arm or the other, and each arm's positions hold its own
	// element, so every position of the joined value holds one of the
	// two — their join. Neither side stated a length, so the joined
	// value states none either and nothing is lost there.
	if a.Kind == KindObjectStar && b.Kind == KindObjectStar {
		elementA, okA := ElementOfObjectStar(a)
		elementB, okB := ElementOfObjectStar(b)
		if okA && okB {
			if joined, ok := KnownObjectStar(JoinKnown(elementA, elementB), grade); ok {
				return joined
			}
		}
		// the elements joined to something the graph does not hold (two
		// unrelated shapes whose join keeps no key): no position claim
		// survives, and the visible gap is the honest answer
		return Unknown
	}
	// an OBJECT-STAR joined with an exact LIST: the list's own count
	// does not survive (the star side states none), but every position
	// of either arm holds a value admitted by the star's element joined
	// with all of the list's items — so the joined value is the star of
	// that join. An EMPTY list contributes no item and joins as the
	// star unchanged: a zero-length array vacuously satisfies every
	// per-position claim.
	if a.Kind == KindObjectStar && b.Kind == KindList {
		return joinObjectStarWithList(a, b, grade)
	}
	if b.Kind == KindObjectStar && a.Kind == KindList {
		return joinObjectStarWithList(b, a, grade)
	}
	// an object-star beside anything else — a set, a bare object, a
	// collection — shares no position claim with it: one is a sequence
	// and the other is not, and there is no reading true of both. The
	// walk's own gap is the answer, and it stays visible as one.
	if a.Kind == KindObjectStar || b.Kind == KindObjectStar {
		return Unknown
	}
	if a.Kind == KindList && b.Kind == KindList {
		if len(a.Items) != len(b.Items) {
			return Unknown
		}
		items := make([]AbstractValue, len(a.Items))
		for i, item := range a.Items {
			items[i] = JoinKnown(item, b.Items[i])
		}
		return KnownList(items, grade)
	}
	// two array-holes of the SAME length: every slot is a hole on both
	// arms, so the joined value states the same length and the same
	// hole claim. A length mismatch loses the exactness — the walk's
	// own gap, same as two mismatched KindLists. Density: the
	// Length/ElementSet claim (every read except Object.keys/values/
	// entries) survives regardless of whether the two arms agree on
	// density, so a density disagreement (or either side's density
	// being unestablished) only drops the Dense claim itself, via
	// DenseKnown — it does NOT degrade the whole join to Unknown the
	// way a length mismatch does, because every OTHER claim the kind
	// carries is still exactly true of both arms. (The length-AND-
	// density-agreeing case never reaches here: SameKnown's own
	// KindArrayHoles arm already requires DenseKnown/Dense agreement
	// alongside the length, so JoinKnown's SameKnown(a, b) fast path
	// above returns first whenever both would hold.)
	if a.Kind == KindArrayHoles && b.Kind == KindArrayHoles {
		aLength, aOk := LengthOfArrayHoles(a)
		bLength, bOk := LengthOfArrayHoles(b)
		if !aOk || !bOk || aLength != bLength {
			return Unknown
		}
		lengthClaim := KnownValues([]float64{float64(aLength)}, PrimitiveNumber, grade)
		out := AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet}
		if grade != TrustProved {
			out.Grade = grade
		}
		return out
	}
	// an array-holes side beside an exact KindList of the SAME length:
	// every KindList slot already claims undefined-or-more than a hole
	// (KnownList's own Items may hold non-undef knowledge), so the join
	// is holes at every position both arms could differ on — which is
	// exactly the array-holes claim, unless the list's own item at a
	// position is provably NOT undef, in which case that slot's join
	// with a hole is "that value, or absent" and the whole sequence is
	// no longer a pure hole claim. Conservative: only a list whose every
	// item IS a hole itself joins cleanly to array-holes; anything else
	// is the walk's own gap.
	//
	// Density: a plain KindList's KindUndef items carry no own-property
	// fact at all (ReadArrayConstruction's below-ceiling `new Array(n)`
	// and readArrayFrom's below-ceiling `Array.from({length:n})` both
	// build the identical n-slot list of Undef — a pre-existing gap
	// this join inherits, not one it introduces). Neither Dense=true
	// nor Dense=false is provable from the list side, so the joined
	// value carries DenseKnown=false — the Length/ElementSet claim
	// (every pre-existing read except Object.keys/values/entries)
	// stays exactly as determined as it already was; only the new
	// Object.keys-relevant claim declines.
	joinArrayHolesWithList := func(holes, list AbstractValue) AbstractValue {
		length, ok := LengthOfArrayHoles(holes)
		if !ok || length != len(list.Items) {
			return Unknown
		}
		for _, item := range list.Items {
			if item.Kind != KindUndef {
				return Unknown
			}
		}
		lengthClaim := KnownValues([]float64{float64(length)}, PrimitiveNumber, grade)
		out := AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet}
		if grade != TrustProved {
			out.Grade = grade
		}
		return out
	}
	if a.Kind == KindArrayHoles && b.Kind == KindList {
		return joinArrayHolesWithList(a, b)
	}
	if b.Kind == KindArrayHoles && a.Kind == KindList {
		return joinArrayHolesWithList(b, a)
	}
	// an array-holes side beside anything else that is not itself a
	// sequence of the exact same shape: no position claim survives —
	// the same refusal an object-star beside a non-sequence makes
	if a.Kind == KindArrayHoles || b.Kind == KindArrayHoles {
		return Unknown
	}
	if a.Kind == KindObject && b.Kind == KindObject {
		bByName := make(map[string]AbstractValue, len(b.Keys))
		for _, key := range b.Keys {
			bByName[key.Name] = key.Value
		}
		var keys []ObjectKey
		for _, key := range a.Keys {
			if other, ok := bByName[key.Name]; ok {
				keys = append(keys, ObjectKey{Name: key.Name, Value: JoinKnown(key.Value, other)})
			}
		}
		// completeness survives only when both sides are complete AND
		// name the same keys — a key present on one side only may exist
		sameNames := len(a.Keys) == len(b.Keys)
		if sameNames {
			for _, key := range a.Keys {
				if _, ok := bByName[key.Name]; !ok {
					sameNames = false
					break
				}
			}
		}
		// bare prototype survives only when BOTH sides are bare; a MIXED
		// join drops completeness outright — a complete claim would let
		// a missing-key read pick one arm's prototype story (inherited
		// function vs exact undefined), and neither alone is sound
		bareAgree := a.BareProto == b.BareProto
		stated := a.Stated
		if a.Stated != b.Stated {
			stated = nil
		}
		joint := KnownObject(
			keys,
			stated,
			a.Complete && b.Complete && sameNames && bareAgree,
			grade,
			a.BareProto && b.BareProto,
		)
		// two joined shapes stay selectable: the runtime value came
		// through one arm or the other, so the arms ride as variants
		// (bounded — past four the correlation stops paying for itself)
		var arms []AbstractValue
		sides := append(append([]AbstractValue{}, sidesOf(a)...), sidesOf(b)...)
		for _, side := range sides {
			found := false
			for _, held := range arms {
				if SameKnown(held, side) {
					found = true
					break
				}
			}
			if !found {
				arms = append(arms, side)
			}
		}
		if len(arms) >= 2 && len(arms) <= 4 {
			return KnownWithVariants(joint, arms)
		}
		return joint
	}
	// A JOIN THAT WOULD STACK PAST THE WIDENING BOUND answers the
	// string ground directly instead of building the deeper
	// Concatenation/Union/Star the arms below would produce. A loop
	// body that reassigns a string across repeated derivations (a
	// `.replace()`/`+` chain re-joined every iteration, with nothing to
	// fold the accumulated structure back down) stacks one more
	// Concatenation/Star layer onto the candidate each round; past
	// sequenceConcatenationWidenBound layers deep, the JOIN ITSELF
	// widens to Strings (C*, the sound "any string" claim every string
	// value already sits inside) rather than handing the kernel's
	// seqSubset decider an ever-deeper term to walk — the
	// ReduceCSSCalc.ts hang (sequence_concatenation_widen.go's file
	// comment measures it: the kernel's own derivative-based deciders
	// grow the term on every nullable-left step with nothing
	// collapsing the repeated substructure, so a moderately-nested
	// tree turns into an unbounded question). Checked ahead of the
	// union-building and absorption arms below, on either operand
	// independently, so this widening triggers before either arm's own
	// Concatenation/Union construction would compound it further.
	if aSet, aIsSet := SetOfKnown(a); aIsSet && refinementsets.StatesSequence(aSet) &&
		refinementsets.SequenceNestingDepth(aSet) > sequenceConcatenationWidenBound {
		return KnownSet(refinementsets.Strings, nil, grade, SetKindTagNone)
	}
	if bSet, bIsSet := SetOfKnown(b); bIsSet && refinementsets.StatesSequence(bSet) &&
		refinementsets.SequenceNestingDepth(bSet) > sequenceConcatenationWidenBound {
		return KnownSet(refinementsets.Strings, nil, grade, SetKindTagNone)
	}
	// two STRING-SORTED sides join into the union of their tuples —
	// but only when every member is at least two characters long, so
	// every member is unambiguously sequence-shaped and no scalar
	// position can reread a 1-tuple as a number (the admitted-language
	// rule). A one-character or empty word declines; so does any set
	// holding a non-sequence form.
	{
		words, wordsOK := stringWordSet(a)
		if wordsOK {
			other, otherOK := stringWordSet(b)
			if otherOK {
				return KnownSet(refinementsets.MakeRefinedSet(refinementsets.Union(words, other)), nil, grade, SetKindTagNone)
			}
		}
	}
	// STRING-GROUND ABSORPTION: one side may fail stringWordSet's own
	// 1-tuple-reread gate (the whole string ground C* — Star(Codepoints)
	// — genuinely admits 1-character words, so it can never pass that
	// gate) while still PROVABLY containing the other side outright —
	// `"xxx"` (a 2+-char word) union Strings is Strings itself, no new
	// 1-tuple risk introduced, since Strings already stated every 1-char
	// word before this join ever ran. The kernel's own seqSubset ask
	// (kernel_seq_subset — a proved theorem in the TRUE direction,
	// seqSubsetB_true) certifies containment; this never DECIDES
	// syntactically which side is bigger. A refused question (no
	// kernel, or a non-sequence shape on either side) falls through to
	// the unchanged path below — the deliberately pinned two-1-codepoint
	// refusal (empty_word_join_test.go) stays exactly as strict, since
	// neither "a" nor "b" contains the other.
	if aSet, aOK := stringSideSet(a); aOK {
		if bSet, bOK := stringSideSet(b); bOK {
			if contains, ok := kernelSeqSubset(bSet, aSet); ok && contains {
				return KnownSet(aSet, nil, grade, SetKindTagNone)
			}
			if contains, ok := kernelSeqSubset(aSet, bSet); ok && contains {
				return KnownSet(bSet, nil, grade, SetKindTagNone)
			}
		}
	}
	// a string- or array-sorted word does not build a union SET: the
	// set is sortless, and its 1-tuple members would be reread as
	// numbers at a scalar position (the admitted-language rule)
	if !IsNumericKind(a) || !IsNumericKind(b) {
		return Unknown
	}
	left, leftOK := SetOfKnown(a)
	right, rightOK := SetOfKnown(b)
	if !leftOK || !rightOK {
		return Unknown
	}
	// canonical SPELLING first (CanonicalScalarForms — a structural
	// equality, never an approximation): a meet-concatenated side wears
	// duplicate conjuncts and vacuous ±inf bounds that blind the run
	// collapse below, and a blinded collapse stacks a union form per
	// walk pass — the same unbounded growth the collapse exists to
	// stop, resurfacing one spelling away (createCategoricalInverse.ts
	// hung a kernel invariant ask on exactly that stack)
	left = refinementsets.CanonicalScalarForms(left)
	right = refinementsets.CanonicalScalarForms(right)
	// measures both sides share survive the join: the runtime value
	// came through one arm or the other, and each arm wore them
	var sharedMeasures *Measures
	if a.Kind == KindSet && b.Kind == KindSet && a.Measures != nil && b.Measures != nil &&
		a.Measures.HasSum == b.Measures.HasSum && a.Measures.Sum == b.Measures.Sum &&
		a.Measures.Sorted == b.Measures.Sorted {
		sharedMeasures = a.Measures
	}
	// two contiguous-integer readings that overlap or adjoin union to
	// their hull EXACTLY — the count-loop's join ({0} ∪ [1,∞) ∩ ℤ)
	// stays one window instead of stacking a union form per walk pass,
	// which grew the kernel's questions without bound until the wasm
	// heap died on them
	leftRun, leftRunOK := integerRunOf(left)
	rightRun, rightRunOK := integerRunOf(right)
	// exact-values sides stay ENUMERABLE (a window decides fewer
	// questions than a small finite set); the collapse serves the
	// window-bearing joins, where the union form would otherwise
	// stack without bound
	if leftRunOK && rightRunOK && !(leftRun.fromValues && rightRun.fromValues) {
		// b begins inside a, or immediately after it — the +1 is asked
		// only where it is exact
		touches := func(a, b integerRun) bool {
			if b.lo <= a.hi {
				return true
			}
			return isSafeInteger(a.hi) && b.lo <= a.hi+1
		}
		if touches(leftRun, rightRun) && touches(rightRun, leftRun) {
			lo := math.Min(leftRun.lo, rightRun.lo)
			hi := math.Max(leftRun.hi, rightRun.hi)
			var infs []float64
			if leftRun.negInf || rightRun.negInf {
				infs = append(infs, math.Inf(-1))
			}
			if leftRun.posInf || rightRun.posInf {
				infs = append(infs, math.Inf(1))
			}
			var forms []refinementsets.Refinement
			if lo != math.Inf(-1) {
				forms = append(forms, refinementsets.AtLeast(lo))
			}
			if hi != math.Inf(1) {
				forms = append(forms, refinementsets.AtMost(hi))
			}
			if len(infs) == 0 {
				forms = append(forms, refinementsets.Integer)
			} else {
				forms = append(forms, refinementsets.Union(
					refinementsets.MakeRefinedSet(refinementsets.Integer),
					refinementsets.MakeRefinedSet(refinementsets.OneOf(infs)),
				))
			}
			return KnownWithMeasures(
				KnownSet(refinementsets.MakeRefinedSet(forms...), nil, grade, SetKindTagNone),
				sharedMeasures,
			)
		}
	}
	return KnownWithMeasures(
		KnownSet(
			refinementsets.CanonicalScalarForms(
				refinementsets.MakeRefinedSet(refinementsets.Union(left, right))),
			nil, grade, SetKindTagNone),
		sharedMeasures,
	)
}

// joinObjectStarWithList joins an object-star with an exact list: the
// element claim has to hold at every position of EITHER arm, so it is
// the star's element joined with each of the list's items. The list's
// count does not survive — the star side states none.
func joinObjectStarWithList(star, list AbstractValue, grade TrustLevel) AbstractValue {
	element, ok := ElementOfObjectStar(star)
	if !ok {
		return Unknown
	}
	for _, item := range list.Items {
		element = JoinKnown(element, item)
	}
	if joined, ok := KnownObjectStar(element, grade); ok {
		return joined
	}
	// the list held a non-graph item (a number, a set), so no ONE
	// position claim covers both arms
	return Unknown
}

// sidesOf is the TS source's `a.variants ?? [a]` read at each join call
// site.
func sidesOf(k AbstractValue) []AbstractValue {
	if k.Variants != nil {
		return k.Variants
	}
	return []AbstractValue{k}
}

func floatsInclude(values []float64, x float64) bool {
	for _, v := range values {
		if v == x {
			return true
		}
	}
	return false
}

func isSafeInteger(x float64) bool {
	return x == math.Trunc(x) && math.Abs(x) <= 9007199254740991 // Number.MAX_SAFE_INTEGER
}

// integerRun is IntegerRun in the TS source: an exact contiguous-integer
// reading of a scalar set: the integer members are exactly the window's,
// and negInf/posInf say whether the matching infinity is also a member
// (the integers-or-±∞ mark with an unbounded window admits it; a bounded
// window shuts it out). Read from a pure non-strict window wearing the
// integer form or the mark, or from a oneOf whose integers tile an
// interval — the shapes the loop walk joins. (integerRun{}, false)
// anywhere else.
type integerRun struct {
	lo, hi     float64
	negInf     bool
	posInf     bool
	fromValues bool // the reading came from an exact oneOf, not a window
}

// integerRunOf is integerRunOf in the TS source.
func integerRunOf(set refinementsets.RefinedSet) (integerRun, bool) {
	lo := math.Inf(-1)
	hi := math.Inf(1)
	isInt := false
	intOrInf := false
	var values []float64
	haveValues := false
	isIntegerAlone := func(s refinementsets.RefinedSet) bool {
		return len(s.Forms) == 1 && s.Forms[0].Form == refinementsets.FormInteger
	}
	// isInfinities recognizes the mark's ±∞ arm. The flag derivation
	// below (negInf/posInf) reads a match as "BOTH infinities are
	// members" — that is the only shape any producer emits: the
	// kernel's intOrInfForm hardcodes w: ["-inf", "+inf"]
	// (encode_sets.lean, the authority), and the Go-side mirror in
	// coercion_models.go emits OneOf{+Inf, -Inf} the same way; nothing
	// narrows one side off a mark once built. A one-sided or empty
	// all-infinities list is not that shape, so it must decline here
	// rather than let the caller assert an infinity the set does not
	// hold.
	isInfinities := func(s refinementsets.RefinedSet) bool {
		if len(s.Forms) != 1 || s.Forms[0].Form != refinementsets.FormOneOf {
			return false
		}
		hasNegInf := false
		hasPosInf := false
		for _, v := range s.Forms[0].W {
			if v == math.Inf(-1) {
				hasNegInf = true
			} else if v == math.Inf(1) {
				hasPosInf = true
			} else {
				return false
			}
		}
		return hasNegInf && hasPosInf
	}
	for _, form := range set.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast:
			lo = math.Max(lo, form.A)
		case refinementsets.FormAtMost:
			hi = math.Min(hi, form.A)
		case refinementsets.FormInteger:
			isInt = true
		case refinementsets.FormOneOf:
			if haveValues {
				return integerRun{}, false
			}
			values = form.W
			haveValues = true
		case refinementsets.FormUnion:
			// the integers-or-±∞ mark (the arithmetic transfers emit it)
			if (isIntegerAlone(*form.A_) && isInfinities(*form.B)) ||
				(isIntegerAlone(*form.B) && isInfinities(*form.A_)) {
				intOrInf = true
				break
			}
			return integerRun{}, false
		default:
			return integerRun{}, false
		}
	}
	if haveValues {
		if lo != math.Inf(-1) || hi != math.Inf(1) || isInt || intOrInf {
			return integerRun{}, false
		}
		if len(values) == 0 {
			return integerRun{}, false
		}
		for _, v := range values {
			if !isSafeInteger(v) {
				return integerRun{}, false
			}
		}
		min := values[0]
		max := values[0]
		seen := map[float64]bool{}
		for _, v := range values {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			seen[v] = true
		}
		if max-min+1 != float64(len(seen)) {
			return integerRun{}, false
		}
		return integerRun{lo: min, hi: max, negInf: false, posInf: false, fromValues: true}, true
	}
	if (!isInt && !intOrInf) || lo > hi {
		return integerRun{}, false
	}
	return integerRun{
		lo:         lo,
		hi:         hi,
		negInf:     intOrInf && lo == math.Inf(-1),
		posInf:     intOrInf && hi == math.Inf(1),
		fromValues: false,
	}, true
}
