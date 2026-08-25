// The refined-set denotation of a knowledge state (SetOfKnown), the
// unknown-with-provenance builder (UnknownOver), and the scalar-reread
// gates a string-word join checks before treating a value's members as
// tuple-layer set members.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

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
		// a STRING's multi-codepoint tuple wears the SAME Word-leaf
		// spelling refinementsets.StringTuple already builds for a
		// literal read off a type annotation (codepoint_sets.go's own
		// doc: "the Word leaf replaces what used to be a chain of
		// one-codepoint Concatenation nodes -- the SAME literal, one
		// node instead of one node per character"). Spelling a VALUE's
		// own string differently from a DECLARED string of the same
		// text builds two wire-distinct sets for one literal — the
		// gap CheckWornSet's identity shortcut (sameSetJSON) and the
		// kernel's own wire-equality check both rely on to skip a real
		// question, and a union combining one of each shape here
		// produces a mixed Word/Concatenation union no kernel decider
		// recognizes as one sequence family (`"both sets must be
		// recognized sequence shapes"`). Every other sort (a plain
		// number/boolean tuple, an array-of-numbers) keeps the
		// per-value Concatenation chain — only a string's OWN spelling
		// changes here.
		if k.KindTag == PrimitiveString {
			switch len(k.Values) {
			case 0:
				return refinementsets.MakeRefinedSet(refinementsets.EmptyTuple), true
			case 1:
				return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[0]})), true
			default:
				return refinementsets.MakeRefinedSet(refinementsets.Word(k.Values)), true
			}
		}
		if len(k.Values) == 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[0]})), true
		}
		// A BOOLEAN's words are a DISJUNCTION, never a tuple. `false | true`
		// is the two-member scatter {0, 1} — one value that is either code —
		// where a multi-value NUMBER tuple (an array-of-numbers value, [1, 2])
		// really is a positional chain. The concatenation reading below spells
		// the scatter as a two-character word (the codepoints U+0000 and
		// U+0001), which is right for that tuple and wrong here; it began
		// mattering once booleans arrived as KindValues rather than as a
		// KindSet oneOf.
		if k.KindTag == PrimitiveBoolean {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf(append([]float64{}, k.Values...))), true
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
		// a bare Word leaf is a fixed literal of len(f.W) codepoints --
		// long enough to never be reread as a scalar only when it holds
		// two or more; a single-codepoint Word is exactly the 1-tuple
		// rereading this test exists to catch, the same restriction a
		// one-element OneOf would fail.
		if f.Form == refinementsets.FormWord && len(f.W) >= 2 {
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
