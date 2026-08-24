// Bounded repetition over the grammar (TERMS.md §6): the NATIVE form
// repeat(E, lo, hi) -- sequences of scalars from E with length in
// [lo, hi], hi nil unbounded. The window's kernel derivative decrements
// the bounds, so its syntax is O(1) and its membership linear (the
// union-ladder encoding this replaced grew quadratically and blew the
// cost budget from n = 8).
//
// Two canonical special cases keep their classical shapes: the
// unconstrained window (0, null) is the star, and the single position
// (1, 1) over a CODEPOINT element is the element itself (a
// 1-character string IS the scalar layer -- this keeps
// z.string().length(1) a SCALAR set). Every other element at (1, 1)
// -- a number, an object, any non-codepoint sort -- keeps its
// Repeat/RepeatWord wrapper: a 1-element ARRAY is [T], never bare T,
// so z.array(T).min(1).max(1) still carries its element for
// AsRepetition's readers. AsRepetition reads back the shapes
// Repetition emits -- past and present -- so chained bounds re-derive
// the element and tighten, never stack.

package refinementsets

import "reflect"

// RepeatExactly is E^k: k concatenated copies, right-nested; E^0 is the
// empty tuple, E^1 is E itself. (z.tuple's shape -- positional, not
// counted.)
func RepeatExactly(element RefinedSet, k int) RefinedSet {
	if k == 0 {
		return MakeRefinedSet(EmptyTuple)
	}
	set := element
	for i := 1; i < k; i++ {
		set = MakeRefinedSet(Concatenation(element, set))
	}
	return set
}

// TightenRepetition tightens the repetition form of a sequence-shaped
// base -- the TERMS reading of .min/.max/.length on strings and
// arrays: length bounds are repetition, never value bounds. Returns
// nil (ok=false) when the base carries no repetition form (a scalar
// chain) -- unless a conjoinElement is given: a PATTERN-shaped base (a
// startsWith chain, an endsWith chain) then gains the window as a
// CONJOINED repetition of that element, the same spelling the schema
// compiler writes for .startsWith("u").min(2), so guard-narrowed
// knowledge and stated sets ask twin questions. The caller vouches the
// element fits the value's sort (codepoints for a string place).
//
// method is one of "min", "max", "length".
func TightenRepetition(
	base RefinedSet,
	method string,
	k int,
	conjoinElement *RefinedSet,
) (RefinedSet, bool) {
	for i := 0; i < len(base.Forms); i++ {
		rep, ok := AsRepetition(MakeRefinedSet(base.Forms[i]))
		if !ok {
			continue
		}
		var lo int
		switch method {
		case "min":
			lo = maxInt(rep.Lo, k)
		case "length":
			lo = k
		default:
			lo = rep.Lo
		}
		var hi *int
		switch method {
		case "max":
			if rep.Hi == nil {
				hi = intPtr(k)
			} else {
				hi = intPtr(minInt(*rep.Hi, k))
			}
		case "length":
			hi = intPtr(k)
		default:
			hi = rep.Hi
		}
		// the tightened window can CONTRADICT the base's own bound — a
		// `.max(3)` value under a `min` guard proving length >= 6 asks for
		// [6, 3], the empty window: no value satisfies both. That is the
		// vacuous branch, not a malformed set — this layer has no
		// spelling for "no such value" (metRepetition's own comment states
		// the same for the meet), so every caller here already reads
		// tightenedOk=false as "no tightened row", same as the sequence-
		// shaped-conjoin arm below. repetitionSafe keeps that reading
		// uniform instead of letting Repetition's invariant panic escape
		// this constructor.
		rebuilt, rebuiltOk := repetitionSafe(rep.Element, lo, hi)
		if !rebuiltOk {
			return RefinedSet{}, false
		}
		result := MakeRefinedSet()
		result.Forms = append(result.Forms, base.Forms[:i]...)
		result.Forms = append(result.Forms, rebuilt.Forms...)
		result.Forms = append(result.Forms, base.Forms[i+1:]...)
		return result, true
	}
	sequenceShaped := false
	for _, f := range base.Forms {
		if f.Form == FormStar || f.Form == FormConcatenation || f.Form == FormRepeat ||
			f.Form == FormRepeatWord || f.Form == FormEmptyTuple || f.Form == FormWord {
			sequenceShaped = true
			break
		}
	}
	if conjoinElement != nil && sequenceShaped {
		var lo int
		var hi *int
		if method == "max" {
			lo = 0
		} else {
			lo = k
		}
		if method != "min" {
			hi = intPtr(k)
		}
		result, ok := repetitionSafe(*conjoinElement, lo, hi)
		if !ok {
			return RefinedSet{}, false
		}
		combined := MakeRefinedSet()
		combined.Forms = append(combined.Forms, base.Forms...)
		combined.Forms = append(combined.Forms, result.Forms...)
		return combined, true
	}
	return RefinedSet{}, false
}

// repetitionSafe wraps Repetition's panics as a (value, ok) pair, for
// TightenRepetition's two build sites (a rebuilt repetition form, and a
// conjoined pattern window), both of which read a construction failure
// as "no chain spelling" / "no tightened row" rather than propagating
// the panic -- the TS source's try/catch around `repetition(...)`.
func repetitionSafe(element RefinedSet, lo int, hi *int) (result RefinedSet, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return Repetition(element, lo, hi), true
}

// Repetition is the set of sequences of element with length in
// [lo, hi]; hi nil means unbounded.
func Repetition(element RefinedSet, lo int, hi *int) RefinedSet {
	if lo < 0 {
		panic("a repetition bound is a natural number")
	}
	if hi != nil && *hi < lo {
		panic("a repetition upper bound is a natural number >= the lower")
	}
	if lo == 0 && hi == nil {
		return MakeRefinedSet(Star(element))
	}
	// the (1,1) collapse to the bare element is sound only when the
	// element IS the codepoint alphabet: a 1-character STRING is
	// itself a scalar (z.string().length(1) stays the Codepoints set,
	// unread by AsRepetition -- sequence_measures.go's own
	// WithinCodepointDoor reads it back). An ARRAY of any other
	// element (z.array(z.number()).min(1).max(1), a numeric list, an
	// object array, ...) is never confusable with its bare element --
	// [T] is a 1-element SEQUENCE, not T -- so it must keep the
	// Repeat/RepeatWord wrapper every element-consuming route
	// (destructuring, relational accumulation, AsRepetition callers)
	// reads back.
	if lo == 1 && hi != nil && *hi == 1 && IsCharacter(element) {
		return element
	}
	// the counted REPEAT form counts SCALAR elements -- the kernel's
	// design (its derivative peels one number per element). An element
	// beyond the 1-tuple layer (a quantified GROUP in a pattern) gets
	// the WORD-counted form, whose derivative peels through the first
	// word -- O(1) syntax whatever the window.
	if !OnOneTupleLayer(element) {
		return MakeRefinedSet(RepeatWordOf(element, lo, hi))
	}
	return MakeRefinedSet(RepeatOf(element, lo, hi))
}

// sameSetJSON is sameSet in the TS source: the TS source compares sets
// by JSON.stringify equality (a syntactic identity check, not a
// semantic one -- two differently-built but equal sets can compare
// unequal here, same as there). reflect.DeepEqual over the dereferenced
// tree is the direct substitute and additionally handles +-Infinity
// correctly, which JSON (both encoding/json and JS's JSON.stringify)
// cannot round-trip; NaN never reaches a Refinement.A (refused at
// construction in refinement_forms.go), so its DeepEqual semantics
// (NaN != NaN) never surface here. Named sameSetJSON, not sameSet, to
// flag at every call site that this is the port's substitution for the
// TS source's JSON-based identity, not an independent design choice.
func sameSetJSON(a, b RefinedSet) bool {
	return reflect.DeepEqual(normalizeSet(a), normalizeSet(b))
}

// normalizeSet is a plain (pointer-free) copy of a RefinedSet's tree,
// so reflect.DeepEqual compares structure rather than pointer
// identity of the nested *RefinedSet fields.
func normalizeSet(s RefinedSet) RefinedSet {
	forms := make([]Refinement, len(s.Forms))
	for i, f := range s.Forms {
		forms[i] = normalizeForm(f)
	}
	return RefinedSet{Forms: forms}
}

func normalizeForm(f Refinement) Refinement {
	out := f
	if f.A_ != nil {
		normalized := normalizeSet(*f.A_)
		out.A_ = &normalized
	}
	if f.B != nil {
		normalized := normalizeSet(*f.B)
		out.B = &normalized
	}
	if f.W != nil {
		out.W = append([]float64{}, f.W...)
	}
	return out
}

// peeledCopies is the result of counting leading copies of one element
// over a right-nested concatenation.
type peeledCopies struct {
	element RefinedSet
	copies  int
	end     RefinedSet
}

// peelCopies counts leading copies of one element over a right-nested
// concatenation; returns the copy count and what the nest ends in.
func peelCopies(set RefinedSet) (peeledCopies, bool) {
	if len(set.Forms) != 1 || set.Forms[0].Form != FormConcatenation {
		return peeledCopies{}, false
	}
	head := *set.Forms[0].A_
	copies := 1
	end := *set.Forms[0].B
	for len(end.Forms) == 1 && end.Forms[0].Form == FormConcatenation && sameSetJSON(*end.Forms[0].A_, head) {
		copies++
		end = *end.Forms[0].B
	}
	return peeledCopies{element: head, copies: copies, end: end}, true
}

// Repeated is the {element, lo, hi} triple AsRepetition reads back.
type Repeated struct {
	Element RefinedSet
	Lo      int
	Hi      *int
}

// AsRepetition reads a set back as a repetition {element, lo, hi} --
// succeeding on exactly the shapes Repetition builds, ok=false on
// anything else.
func AsRepetition(set RefinedSet) (Repeated, bool) {
	if len(set.Forms) != 1 {
		return Repeated{}, false
	}
	only := set.Forms[0]
	if only.Form == FormRepeat {
		return Repeated{Element: *only.A_, Lo: only.Lo, Hi: only.Hi}, true
	}
	// the WORD-counted window -- what Repetition builds when the
	// element is itself a sequence (an array of strings): same
	// {element, lo, hi} reading, each counted position a whole word
	if only.Form == FormRepeatWord {
		return Repeated{Element: *only.A_, Lo: only.Lo, Hi: only.Hi}, true
	}
	if only.Form == FormStar {
		return Repeated{Element: *only.A_, Lo: 0, Hi: nil}, true
	}
	if only.Form == FormEmptyTuple {
		return Repeated{}, false // element unknowable
	}
	if only.Form == FormConcatenation {
		peeled, ok := peelCopies(set)
		if !ok {
			return Repeated{}, false
		}
		if len(peeled.end.Forms) == 1 && peeled.end.Forms[0].Form == FormStar &&
			sameSetJSON(*peeled.end.Forms[0].A_, peeled.element) {
			return Repeated{Element: peeled.element, Lo: peeled.copies, Hi: nil}, true
		}
		if sameSetJSON(peeled.end, peeled.element) {
			hi := peeled.copies + 1
			return Repeated{Element: peeled.element, Lo: peeled.copies + 1, Hi: &hi}, true
		}
		return Repeated{}, false
	}
	if only.Form == FormUnion {
		// the left-folded union of exact powers E^lo union ... union E^hi
		var branches []RefinedSet
		current := set
		for len(current.Forms) == 1 && current.Forms[0].Form == FormUnion {
			branches = append([]RefinedSet{*current.Forms[0].B}, branches...)
			current = *current.Forms[0].A_
		}
		branches = append([]RefinedSet{current}, branches...)
		var element *RefinedSet
		var lengths []int
		for _, branch := range branches {
			if len(branch.Forms) == 1 && branch.Forms[0].Form == FormEmptyTuple {
				lengths = append(lengths, 0)
				continue
			}
			peeled, ok := peelCopies(branch)
			if !ok {
				// a lone E branch: one copy
				if element == nil || sameSetJSON(branch, *element) {
					if element == nil {
						b := branch
						element = &b
					}
					lengths = append(lengths, 1)
					continue
				}
				return Repeated{}, false
			}
			if !sameSetJSON(peeled.end, peeled.element) {
				return Repeated{}, false
			}
			if element == nil {
				e := peeled.element
				element = &e
			} else if !sameSetJSON(*element, peeled.element) {
				return Repeated{}, false
			}
			lengths = append(lengths, peeled.copies+1)
		}
		if element == nil {
			return Repeated{}, false
		}
		sortInts(lengths)
		lo := lengths[0]
		hi := lengths[len(lengths)-1]
		// contiguity: exactly the range Repetition builds
		if len(lengths) != hi-lo+1 {
			return Repeated{}, false
		}
		for i := range lengths {
			if lengths[i] != lo+i {
				return Repeated{}, false
			}
		}
		return Repeated{Element: *element, Lo: lo, Hi: &hi}, true
	}
	return Repeated{}, false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intPtr(v int) *int {
	return &v
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
