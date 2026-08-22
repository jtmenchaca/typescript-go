// TERMS.md terms 4-5: the grammar of refinement forms, and a
// refined set as the intersection of R-bar* with zero or more of them.
// This is the checker's working value for "a set" -- the same grammar
// the kernel decodes, over ordinary JavaScript numbers (+-infinity are
// elements of R-bar and are admitted; NaN is not an element and is
// refused at construction, the boundary ruling).

package refinementsets

import (
	"fmt"
	"math"
)

// Form is the tag of a Refinement -- one of the grammar's named shapes.
type Form string

const (
	FormAtLeast       Form = "atLeast"
	FormAbove         Form = "above"
	FormAtMost        Form = "atMost"
	FormBelow         Form = "below"
	FormInteger       Form = "integer"
	FormMultipleOf    Form = "multipleOf"
	FormOneOf         Form = "oneOf"
	FormEmptyTuple    Form = "emptyTuple"
	FormConcatenation Form = "concatenation"
	FormStar          Form = "star"
	FormRepeat        Form = "repeat"
	FormRepeatWord    Form = "repeatWord"
	FormUnion         Form = "union"
	FormDifference    Form = "difference"
)

// Refinement is the TS discriminated union
//
//	{ form: "atLeast"; a: number } | { form: "above"; a: number } | ...
//
// collapsed to one struct with a Form tag, per the port's convention for
// discriminated unions that carry pure data. Not every field is
// meaningful for every Form; each constructor below sets only the
// fields its form uses.
type Refinement struct {
	Form Form

	// atLeast / above / atMost / below: the bound. multipleOf: the divisor d.
	A float64

	// oneOf: the admitted values w.
	W []float64

	// concatenation / union / difference: the left/first operand.
	// star: the element set. repeat / repeatWord: the element set.
	A_ *RefinedSet

	// concatenation / union / difference: the right/second operand.
	B *RefinedSet

	// repeat / repeatWord: the bounds. lo a natural, hi a natural >= lo
	// or nil for unbounded.
	Lo int
	Hi *int
}

// RefinedSet is the intersection of ℝ̄* with the forms it carries.
type RefinedSet struct {
	Forms []Refinement
}

// UnreachedForm is unreachedForm in the TS source: the never-reached arm
// of an exhaustive form switch. Adding a new form makes every such site
// fail at compile time in TS (pnpm check enumerates the missing arms the
// way the Lean build's total matches do); Go has no exhaustiveness
// check on a string tag, so this panics instead -- the same "impossible
// state, loudly" intent.
func UnreachedForm(form Refinement) {
	panic(fmt.Sprintf("unreached form: %+v", form))
}

// element is the element helper in the TS source: an ℝ̄ member, refusing
// NaN at construction (the boundary ruling -- NaN is not an element of
// ℝ̄). The TS throws Error; this panics, mirroring throw-on-impossibility
// at a value smart constructor.
func element(x float64, where string) float64 {
	if math.IsNaN(x) {
		panic(fmt.Sprintf("NaN is not an element of ℝ̄ (%s)", where))
	}
	return x
}

func AtLeast(a float64) Refinement {
	return Refinement{Form: FormAtLeast, A: element(a, "at least a")}
}

func Above(a float64) Refinement {
	return Refinement{Form: FormAbove, A: element(a, "above a")}
}

func AtMost(a float64) Refinement {
	return Refinement{Form: FormAtMost, A: element(a, "at most a")}
}

func Below(a float64) Refinement {
	return Refinement{Form: FormBelow, A: element(a, "below a")}
}

var Integer = Refinement{Form: FormInteger}

// MultipleOf is "multiple of d" -- d is a finite nonzero number
// (TERMS d in R-bar minus {0}; the wire carries d as an integer pair,
// so it is finite there).
func MultipleOf(d float64) Refinement {
	if math.IsNaN(d) || math.IsInf(d, 0) {
		panic("multiple of d takes a finite d")
	}
	if d == 0 {
		panic("multiple of d takes d != 0")
	}
	return Refinement{Form: FormMultipleOf, A: d}
}

func OneOf(w []float64) Refinement {
	out := make([]float64, len(w))
	for i, x := range w {
		out[i] = element(x, "one of W")
	}
	return Refinement{Form: FormOneOf, W: out}
}

// ModWindowForms is modWindowForms in the TS source: the NORMALIZED
// modular window mod(p, lo, hi) -- values in [0, p) whose position in
// the cycle sits inside the window -- and when lo > hi the window
// WRAPS, [lo, p) union [0, hi] (the angle-axis gap that crosses 0).
// Spelled entirely in existing forms: the claim is about the normalized
// value the code computes with %, so no new grammar is needed and every
// decider already speaks it. A claim about UNNORMALIZED values (x mod p
// in ..., x unbounded) has no kernel form yet.
func ModWindowForms(p, lo, hi float64) []Refinement {
	if !(p > 0) || math.IsInf(p, 0) {
		panic("a modular period is a positive finite number")
	}
	if !(lo >= 0 && lo < p && hi >= 0 && hi < p) {
		panic("modular window edges live in [0, period)")
	}
	var window []Refinement
	if lo <= hi {
		window = []Refinement{AtLeast(lo), AtMost(hi)}
	} else {
		window = []Refinement{Union(
			MakeRefinedSet(AtLeast(lo), Below(p)),
			MakeRefinedSet(AtLeast(0), AtMost(hi)),
		)}
	}
	result := []Refinement{AtLeast(0), Below(p)}
	result = append(result, window...)
	return result
}

var EmptyTuple = Refinement{Form: FormEmptyTuple}

func Concatenation(a, b RefinedSet) Refinement {
	return Refinement{Form: FormConcatenation, A_: &a, B: &b}
}

func Star(a RefinedSet) Refinement {
	return Refinement{Form: FormStar, A_: &a}
}

// RepeatOf is "repeat of A between lo and hi" -- lo a natural, hi a
// natural >= lo or nil for unbounded.
func RepeatOf(a RefinedSet, lo int, hi *int) Refinement {
	if lo < 0 {
		panic("a repetition bound is a natural number")
	}
	if hi != nil && *hi < 0 {
		panic("a repetition ceiling is a natural number")
	}
	return Refinement{Form: FormRepeat, A_: &a, Lo: lo, Hi: hi}
}

// RepeatWordOf is "repeat words of A between lo and hi" -- the
// word-counted window.
func RepeatWordOf(a RefinedSet, lo int, hi *int) Refinement {
	if lo < 0 {
		panic("a repetition bound is a natural number")
	}
	if hi != nil && *hi < 0 {
		panic("a repetition ceiling is a natural number")
	}
	return Refinement{Form: FormRepeatWord, A_: &a, Lo: lo, Hi: hi}
}

func Union(a, b RefinedSet) Refinement {
	return Refinement{Form: FormUnion, A_: &a, B: &b}
}

func Difference(a, b RefinedSet) Refinement {
	return Refinement{Form: FormDifference, A_: &a, B: &b}
}

func MakeRefinedSet(forms ...Refinement) RefinedSet {
	return RefinedSet{Forms: forms}
}

// Numbers is the 1-tuple layer R-bar itself -- z.number(). (The
// kernel's scalar questions ask for at least one refinement, so R-bar
// is spelled as the ray from -infinity rather than as zero
// refinements.)
var Numbers = MakeRefinedSet(AtLeast(math.Inf(-1)))

// IsNumberGround is whether a set spells R-bar itself -- the whole
// number ground, the same bare ray z.number() (and a bare `number`
// keyword's own grounded set) both compile to. A caller that reaches
// this set through the bare-keyword path still needs to know it may
// hold NaN at runtime (R-bar's own ray forms never mention NaN --
// NaN is worn as the separate PossiblyNaN wrapper, never a set
// member), which is why the grounding of a bare `number` keyword
// must wrap this exact set in PossiblyNaN rather than handing it out
// bare (declared_value.go's AbstractValueOfDeclared).
func IsNumberGround(set RefinedSet) bool {
	return sameSetJSON(set, Numbers)
}

type rayCandidate struct {
	a      float64
	strict bool
}

// FoldRayForms is foldRayForms in the TS source: the tightest-ray fold,
// as a SEMANTIC operation: within the lower-ray class (atLeast/above)
// only the tightest constrains the intersection -- atLeast(a) and
// atLeast(b) === atLeast(max(a, b)), with the strict form winning ties
// -- and dually for the upper class. Everything else passes through in
// order, and a class is merged, never dropped, so a set stays
// scalar-anchored (the bare atLeast(-infinity) spelling of R-bar
// survives alone). Narrowing chains stack one ray per conjunct; posing
// the folded question saves the kernel the redundant forms (measured
// ~3500x on a 128-conjunct guard) while asking for the same set.
func FoldRayForms(forms []Refinement) []Refinement {
	var lo, hi *rayCandidate
	var rest []Refinement
	for _, f := range forms {
		switch f.Form {
		case FormAtLeast, FormAbove:
			candidate := rayCandidate{a: f.A, strict: f.Form == FormAbove}
			if lo == nil || candidate.a > lo.a || (candidate.a == lo.a && candidate.strict) {
				lo = &candidate
			}
		case FormAtMost, FormBelow:
			candidate := rayCandidate{a: f.A, strict: f.Form == FormBelow}
			if hi == nil || candidate.a < hi.a || (candidate.a == hi.a && candidate.strict) {
				hi = &candidate
			}
		default:
			rest = append(rest, f)
		}
	}
	var folded []Refinement
	if lo != nil {
		if lo.strict {
			folded = append(folded, Above(lo.a))
		} else {
			folded = append(folded, AtLeast(lo.a))
		}
	}
	if hi != nil {
		if hi.strict {
			folded = append(folded, Below(hi.a))
		} else {
			folded = append(folded, AtMost(hi.a))
		}
	}
	folded = append(folded, rest...)
	return folded
}

// NonNegativeIntegerBounds reads a set as a plain non-negative integer
// window [lo, hi] -- the exact conjunction {AtLeast(lo), AtMost(hi),
// Integer}, folded through FoldRayForms and no other form beside it,
// lo >= 0 and hi finite. ok=false on anything wider: an unbounded side
// (hi == nil in the ray sense, i.e. no AtMost survives the fold), a
// negative lower edge, a non-integer conjunct missing, or any other
// form riding alongside (a OneOf, a MultipleOf, a Union) that the two
// rays alone do not account for. This is the syntactic reading a
// declared z.number().int().min(lo).max(hi) window leaves in its set
// -- no kernel round trip, so it only ever answers the shapes built
// exactly this way.
func NonNegativeIntegerBounds(set RefinedSet) (lo float64, hi float64, ok bool) {
	folded := FoldRayForms(set.Forms)
	var loForm, hiForm *Refinement
	sawInteger := false
	for i, f := range folded {
		switch f.Form {
		case FormAtLeast:
			if loForm != nil {
				return 0, 0, false
			}
			loForm = &folded[i]
		case FormAtMost:
			if hiForm != nil {
				return 0, 0, false
			}
			hiForm = &folded[i]
		case FormInteger:
			sawInteger = true
		default:
			return 0, 0, false
		}
	}
	if loForm == nil || hiForm == nil || !sawInteger {
		return 0, 0, false
	}
	if math.IsInf(loForm.A, 0) || math.IsInf(hiForm.A, 0) {
		return 0, 0, false
	}
	if loForm.A < 0 || hiForm.A < loForm.A {
		return 0, 0, false
	}
	return loForm.A, hiForm.A, true
}

// WordOf is wordOf in the TS source: the tuple a set holds when it
// holds exactly one -- the canonical singleton shapes (stringTuple,
// exact tuples): the empty tuple, a one-element oneOf, and
// concatenations of those. Nil (with ok=false) anywhere else (a
// multi-form set can still be a singleton; this recognizer stays on the
// shapes the builders produce). A subset question with a recognized
// word on the left IS the membership question -- {w} subset-of B iff
// w in B -- and membership is answered exactly in both directions
// (memberB_iff), so the boundary poses that instead.
func WordOf(set RefinedSet) ([]float64, bool) {
	var word []float64
	pending := []RefinedSet{set} // leftmost on top
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if len(next.Forms) != 1 {
			return nil, false
		}
		form := next.Forms[0]
		switch form.Form {
		case FormEmptyTuple:
			// nothing to push
		case FormOneOf:
			if len(form.W) != 1 {
				return nil, false
			}
			word = append(word, form.W[0])
		case FormConcatenation:
			pending = append(pending, *form.B, *form.A_)
		default:
			return nil, false
		}
	}
	return word, true
}

// StatesSequence is whether a set DEMONSTRABLY states a sequence -- a
// string or an array shape: a star, a concatenation, a repetition, or
// the empty tuple sits among its forms. A positive test:
// relation-shaped or empty sets answer false and keep their own
// paths. (Hoisted from walk's sequence_measures.go so objectgraphs'
// value encoder asks the same question the walk does;
// walk.StatesSequence delegates here.)
func StatesSequence(set RefinedSet) bool {
	for _, f := range set.Forms {
		if f.Form == FormStar || f.Form == FormConcatenation ||
			f.Form == FormRepeat || f.Form == FormRepeatWord ||
			f.Form == FormEmptyTuple {
			return true
		}
	}
	return false
}

// SequenceShaped is whether EVERY top-level form of set is itself a
// string/sequence form -- EmptyTuple/Concatenation outright, a
// Star/Repeat/RepeatWord whose own element is demonstrably codepoints
// (repetitionElementIsCodepoints, below -- Star/Repeat also carry a
// NUMERIC element for a declared list[number]/z.array(z.number()), so
// wearing the form alone is not enough), or a Union/Difference whose
// BOTH operands recurse into this same reading. Unlike StatesSequence
// (a fast, non-recursive, POSITIVE test -- one sequence-shaped form
// anywhere is enough), SequenceShaped requires the WHOLE set to
// qualify, and is what catches a DERIVED string union whose top form
// is a Union of two sequence-shaped branches rather than a bare
// sequence form itself -- e.g. `["ok", "warn", "error"][code]`'s own
// join over a bounded index builds Union(Concatenation, Concatenation)
// at the top, never a bare Concatenation. Ported from
// fact_export.rs's sequence_shaped (assignability.rs:780).
//
// The whole-set alphabet shortcut (IsCharacter/IsStrings) below is not
// part of that port: Codepoints ITSELF (`{integer, union(atLeast(0) ∧
// atMost(0xD7FF), atLeast(0xE000) ∧ atMost(0x10FFFF))}`) carries
// FormInteger among its own top-level forms, which the per-form loop
// below always reads as scalar (the FormInteger case returns false
// unconditionally) — so without this shortcut SequenceShaped(Codepoints)
// itself answers false, and any Union built from the codepoint
// alphabet (prefix_read.lean's foldAlphabet, kernel.SeqPrefix's own
// answer for an open-left concatenation slice: Repeat over a folded
// Union of the operands' alphabets) reads as NOT sequence-shaped even
// though every leaf is a codepoint. The shortcut recognizes the
// alphabet wherever the recursion reaches it, at the top of a fresh
// set or nested arbitrarily deep inside a Union/Difference tree.
func SequenceShaped(set RefinedSet) bool {
	if IsCharacter(set) || IsStrings(set) {
		return true
	}
	if len(set.Forms) == 0 {
		return false
	}
	for _, form := range set.Forms {
		switch form.Form {
		case FormEmptyTuple, FormConcatenation:
			// neither carries a separate "element sort" of its own -- an
			// EmptyTuple names no element at all, and a Concatenation's
			// operands are themselves nested sets this checker only ever
			// builds over codepoints (the string-tuple encoding) --
			// sequence-shaped unconditionally
		case FormStar, FormRepeat, FormRepeatWord:
			if !repetitionElementIsCodepoints(form) {
				return false
			}
		case FormUnion, FormDifference:
			if form.A_ == nil || form.B == nil {
				return false
			}
			if !SequenceShaped(*form.A_) || !SequenceShaped(*form.B) {
				return false
			}
		case FormAtLeast, FormAbove, FormAtMost, FormBelow,
			FormInteger, FormMultipleOf, FormOneOf:
			return false
		default:
			return false
		}
	}
	return true
}

// IsCodepointAlphabetFold is whether set is prefix_read.lean's own
// foldAlphabet shape: a Union/Difference tree built ENTIRELY from
// codepoint-alphabet pieces (Codepoints/Strings, wherever the
// recursion reaches one) and ambiguous OneOf singletons (a fixed
// literal character's own codepoint value, one per position folded
// in) -- ANCHORED by at least one genuine alphabet leaf somewhere in
// the tree, never by singletons alone. This is a SEPARATE test from
// SequenceShaped, not a widening of it: SequenceShaped's contract is
// "both operands of a Union independently qualify" (correct for a
// DERIVED string union, e.g. Union(Concatenation, Concatenation)), and
// loosening FormOneOf to pass unconditionally there would also pass a
// genuinely numeric two-value union like {5}|{7}, which carries no
// codepoint anchor anywhere and must stay numeric.
// kernel.SeqPrefix's own answer for an open-left concatenation slice
// (Repeat over prefixReadOf's folded alphabet) is exactly this shape:
// one operand is the receiver's own scalar alphabet (Codepoints, from
// Strings' star), the other is a chain of Union(OneOf[literal
// codepoint], ...) folding in each fixed character the concatenation's
// literal side supplied. Called from containerNumericCandidate
// (format_for_hover.go) so the container hover reads this fold as a
// string, never a numeric tuple; the exporter's caseOfSet
// (walk/fact_export.go) shares the identical decision through the
// same StatesSequence/SequenceShaped/IsCodepointAlphabetFold trio, so
// hover and export can never disagree about this set.
func IsCodepointAlphabetFold(set RefinedSet) bool {
	anchored, sawAnchor := codepointAlphabetFold(set)
	return anchored && sawAnchor
}

// codepointAlphabetFold is IsCodepointAlphabetFold's recursive walk:
// ok=false the moment a form this fold never builds is found (a
// window, multipleOf, integer, concatenation, star/repeat over a
// non-codepoint element, or anything else); anchored=true once at
// least one genuine alphabet leaf (IsCharacter/IsStrings) is seen
// anywhere in the walk.
func codepointAlphabetFold(set RefinedSet) (ok bool, anchored bool) {
	if IsCharacter(set) || IsStrings(set) {
		return true, true
	}
	if len(set.Forms) != 1 {
		return false, false
	}
	form := set.Forms[0]
	switch form.Form {
	case FormOneOf:
		// an ambiguous literal leaf -- decides nothing alone, and
		// contributes no anchor of its own
		return true, false
	case FormUnion, FormDifference:
		if form.A_ == nil || form.B == nil {
			return false, false
		}
		aOk, aAnchor := codepointAlphabetFold(*form.A_)
		if !aOk {
			return false, false
		}
		bOk, bAnchor := codepointAlphabetFold(*form.B)
		if !bOk {
			return false, false
		}
		return true, aAnchor || bAnchor
	default:
		return false, false
	}
}

// repetitionElementIsCodepoints is whether a Star/Repeat/RepeatWord
// form's own element sits inside the codepoint alphabet -- the same
// gate SequenceShaped's Rust twin checks for the identical reason:
// this checker's grammar reuses Star/Repeat for a NUMERIC element too
// (a declared list[number]/z.array(z.number()) parameter seed), so a
// bare repetition form is sequence-shaped only when its element
// demonstrably IS codepoints, never merely because it wears one of
// these forms.
func repetitionElementIsCodepoints(form Refinement) bool {
	if form.A_ == nil {
		return false
	}
	return IsCharacter(*form.A_)
}

// OnOneTupleLayer stays on the 1-tuple layer: only the 1-tuple forms,
// through union and difference. (The bare root is NOT -- it holds
// every tuple.)
func OnOneTupleLayer(set RefinedSet) bool {
	if len(set.Forms) == 0 {
		return false
	}
	for _, form := range set.Forms {
		switch form.Form {
		case FormAtLeast, FormAbove, FormAtMost, FormBelow, FormInteger, FormMultipleOf, FormOneOf:
			// on the layer
		case FormUnion, FormDifference:
			if !OnOneTupleLayer(*form.A_) || !OnOneTupleLayer(*form.B) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
