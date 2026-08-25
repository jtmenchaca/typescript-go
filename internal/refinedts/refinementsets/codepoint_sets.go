// Strings are tuples of Unicode scalar values (TERMS.md §6): the
// codepoint set is
//
//	C = R-bar ∩ integer ∩ ((>= 0 ∩ <= 0xD7FF) ∪ (>= 0xE000 ∩ <= 0x10FFFF))
//
// -- the surrogate range U+D800-U+DFFF is excluded; lone surrogates
// are not Unicode scalar values. A string value IS its codepoint
// tuple; there is no separate string ground.

package refinementsets

// Codepoints is the codepoint set C.
var Codepoints = MakeRefinedSet(
	Integer,
	Union(
		MakeRefinedSet(AtLeast(0), AtMost(0xD7FF)),
		MakeRefinedSet(AtLeast(0xE000), AtMost(0x10FFFF)),
	),
)

// Strings is the set of all strings: C*.
var Strings = MakeRefinedSet(Star(Codepoints))

// Digits is the ASCII decimal-digit alphabet '0'..'9' -- the element a
// number's decimal spelling repeats over. Exported so a caller outside
// this package (the number-to-string text conversion) can build a
// digit-counted repetition window without reaching into an unexported
// literal.
var Digits = MakeRefinedSet(OneOf([]float64{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}))

// RadixDigits is the radix-r digit alphabet Number::toString spells
// over (sec-numeric-types-number-tostring: digits then LOWERCASE
// letters): '0'..'9' capped at r for r <= 10, and '0'..'9' plus
// 'a'..('a'+r-11) beyond. A radix outside [2, 36] is the caller's to
// refuse; this constructor clamps to that range defensively.
func RadixDigits(radix int) RefinedSet {
	if radix < 2 {
		radix = 2
	}
	if radix > 36 {
		radix = 36
	}
	count := radix
	if count > 10 {
		count = 10
	}
	points := make([]float64, 0, radix)
	for i := 0; i < count; i++ {
		points = append(points, float64('0'+i))
	}
	for i := 10; i < radix; i++ {
		points = append(points, float64('a'+i-10))
	}
	return MakeRefinedSet(OneOf(points))
}

// CodepointsOf is a string's codepoint tuple. Iteration is by code
// point, so a paired surrogate reads as one scalar value.
//
// Substitution note: JavaScript strings are UTF-16 and can hold a LONE
// surrogate, which [...s] then reads as its own code unit -- the set,
// not the encoder, excludes it (see the comment on Codepoints). A Go
// string is required to be valid UTF-8, so a lone surrogate cannot
// occur here as a matter of the language's own string invariant; any
// byte sequence that would decode to one instead decodes to
// utf8.RuneError (U+FFFD) under range-over-string / DecodeRuneInString,
// which is itself a valid scalar value and simply is not what a caller
// meant. This is a representable-input difference from the TS source,
// not a behavior difference on any value both languages can hold.
func CodepointsOf(s string) []float64 {
	runes := []rune(s)
	out := make([]float64, len(runes))
	for i, r := range runes {
		out[i] = float64(r)
	}
	return out
}

// StringTuple is the singleton set holding exactly one string: a Word
// leaf carrying every codepoint on the one node for two or more
// characters, a one-element OneOf singleton for exactly one character
// (kept ambiguous with a numeric scalar on purpose -- the same shape a
// single-value numeric enum wears, so a lone codepoint still reads
// either way depending on the checked position's sort, exactly as
// unambiguousStringLiteral's own doc already states), and the empty
// tuple for the empty string. The Word leaf replaces what used to be a
// chain of one-codepoint Concatenation nodes -- the SAME literal, one
// node instead of one node per character (the shape
// kernelbridge/wire_nesting_guard.go's seam guard measures: an
// N-character literal spelled the old way counted N sequence-family
// nodes toward that guard's cap; spelled as one Word leaf it counts
// zero).
func StringTuple(s string) RefinedSet {
	points := CodepointsOf(s)
	if len(points) == 0 {
		return MakeRefinedSet(EmptyTuple)
	}
	if len(points) == 1 {
		return MakeRefinedSet(OneOf([]float64{points[0]}))
	}
	return MakeRefinedSet(Word(points))
}

// WordTuplesOf is the finite WORD LIST a set spells -- a union tree of
// exact string tuples -- or nil (ok=false) where any branch is not one
// word. The syntactic reading a literal-equality disjunction narrows
// by.
func WordTuplesOf(set RefinedSet) ([][]float64, bool) {
	if len(set.Forms) == 1 && set.Forms[0].Form == FormUnion {
		left, leftOk := WordTuplesOf(*set.Forms[0].A_)
		right, rightOk := WordTuplesOf(*set.Forms[0].B)
		if !leftOk || !rightOk {
			return nil, false
		}
		return append(append([][]float64{}, left...), right...), true
	}
	points, ok := stringLiteralPointsOfSet(set)
	if !ok {
		return nil, false
	}
	return [][]float64{points}, true
}

// WordTuplesOfConjunction is WordTuplesOf over a set that may carry
// SEVERAL forms. A multi-form set is a CONJUNCTION — every member
// satisfies every form — so any ONE form's finite word list is a
// superset of the members, and answering it widens the set claim in
// the sound direction. The meet machinery CONCATENATES form lists (a
// served return met with its declared type is the two-form shape), so
// a value that is exactly a word union can arrive wearing a second
// conjunct beside it. The TIGHTEST readable conjunct answers: fewer
// words is a smaller superset.
func WordTuplesOfConjunction(set RefinedSet) ([][]float64, bool) {
	if len(set.Forms) == 1 {
		return WordTuplesOf(set)
	}
	var best [][]float64
	held := false
	for _, form := range set.Forms {
		words, ok := WordTuplesOf(MakeRefinedSet(form))
		if !ok {
			continue
		}
		if !held || len(words) < len(best) {
			best = words
			held = true
		}
	}
	return best, held
}

// stringLiteralPointsOfSet reads one set as one exact word: a bare
// Word leaf (the current StringTuple encoding for two or more
// characters), a chain of one-codepoint singletons (the OLD StringTuple
// encoding -- still read here since it can arrive from an older call
// site or the kernel's own answers), a lone one-element OneOf (a
// one-character StringTuple), or the empty tuple.
func stringLiteralPointsOfSet(set RefinedSet) ([]float64, bool) {
	if len(set.Forms) == 1 && set.Forms[0].Form == FormEmptyTuple {
		return []float64{}, true
	}
	if len(set.Forms) == 1 && set.Forms[0].Form == FormWord {
		return append([]float64{}, set.Forms[0].W...), true
	}
	var points []float64
	cursor := &set
	for cursor != nil {
		if len(cursor.Forms) != 1 {
			return nil, false
		}
		form := cursor.Forms[0]
		if form.Form == FormOneOf && len(form.W) == 1 {
			points = append(points, form.W[0])
			return points, true
		}
		if form.Form == FormConcatenation {
			head := cursor.Forms[0]
			if len(head.A_.Forms) != 1 {
				return nil, false
			}
			headForm := head.A_.Forms[0]
			if headForm.Form != FormOneOf || len(headForm.W) != 1 {
				return nil, false
			}
			points = append(points, headForm.W[0])
			cursor = head.B
			continue
		}
		return nil, false
	}
	return nil, false
}

// sameAlphabet mirrors the TS source's JSON.stringify identity check
// (s === codepoints || JSON.stringify(s) === spelledAlphabet): whether
// a set is structurally the codepoint alphabet. Uses sameSetJSON (see
// repetition_window_forms.go) rather than encoding/json directly, so
// every structural-identity check in the package goes through the one
// +-Infinity-safe comparison.
func sameAlphabet(s RefinedSet) bool {
	return sameSetJSON(s, Codepoints)
}

// IsStringGround is whether a set spells C* itself -- the whole string
// ground: one star (or a trivial repetition window) over the codepoint
// alphabet.
func IsStringGround(set RefinedSet) bool {
	if len(set.Forms) != 1 {
		return false
	}
	f := set.Forms[0]
	if f.Form == FormStar {
		return sameAlphabet(*f.A_)
	}
	if f.Form == FormRepeat {
		return f.Lo == 0 && f.Hi == nil && sameAlphabet(*f.A_)
	}
	return false
}

// WithoutStringGround is forms with the redundant C* CONJUNCT dropped:
// beside any other form the ground adds nothing (every pattern built
// here is a language over C, and dropping a conjunct only weakens a
// claim), while the kernel's pattern prover reads ONE shape, never a
// stack. The ground stays when it is all there is.
func WithoutStringGround(forms []Refinement) []Refinement {
	var kept []Refinement
	for _, f := range forms {
		if f.Form == FormStar && sameAlphabet(*f.A_) {
			continue
		}
		if f.Form == FormRepeat && f.Lo == 0 && f.Hi == nil && sameAlphabet(*f.A_) {
			continue
		}
		kept = append(kept, f)
	}
	if len(kept) > 0 {
		return kept
	}
	return forms
}

// StartsWithSet is s~ . C* -- strings starting with s.
func StartsWithSet(s string) RefinedSet {
	if len(s) == 0 {
		return Strings
	}
	return MakeRefinedSet(Concatenation(StringTuple(s), Strings))
}

// EndsWithSet is C* . s~ -- strings ending with s.
func EndsWithSet(s string) RefinedSet {
	if len(s) == 0 {
		return Strings
	}
	return MakeRefinedSet(Concatenation(Strings, StringTuple(s)))
}

// IncludesSet is C* . s~ . C* -- strings containing s.
func IncludesSet(s string) RefinedSet {
	if len(s) == 0 {
		return Strings
	}
	return MakeRefinedSet(
		Concatenation(Strings, MakeRefinedSet(Concatenation(StringTuple(s), Strings))),
	)
}

// astralFloor: JS string reads are UTF-16 facts while the model counts
// scalar values: an astral scalar (>= 0x10000) is two code units, so
// unit counts and scalar counts diverge exactly on astral content.
const astralFloor = 0x10000

// Utf16LengthOf is the UTF-16 code-unit count of a scalar tuple --
// JavaScript's own .length of the string it spells.
func Utf16LengthOf(codepointValues []float64) int {
	units := 0
	for _, c := range codepointValues {
		if c >= astralFloor {
			units += 2
		} else {
			units += 1
		}
	}
	return units
}

// AstralFree is whether every scalar sits below the astral floor --
// exactly when unit indexing and scalar indexing coincide.
func AstralFree(codepointValues []float64) bool {
	for _, c := range codepointValues {
		if c >= astralFloor {
			return false
		}
	}
	return true
}
