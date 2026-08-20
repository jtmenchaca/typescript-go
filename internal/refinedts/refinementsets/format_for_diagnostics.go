// The diagnostic-message algebra: the set spelled for a diagnostic or
// the runtime's own error. ASCII comparisons joined with &&, the
// algebra's own symbols (∪ ∖ · ×). >= 0 && <= 100. Split from
// display.ts per the v2 tree; the folds and string shapes it applies
// live in format_string_shapes.go.

package refinementsets

import "strconv"

// unionWords is every arm of a union tree read as ONE string literal
// -- the enum spelling, "margin" | "border" -- or ok=false where any
// arm is not a literal chain (the algebra's ∪ then applies). This
// caller has NO sort at hand, so only PRINTABLE chains spell as text:
// a real word's characters are printable, while a scalar singleton
// ({3}, a numeric enum's member) shares the chain shape as a control
// code -- the algebra keeps those.
func unionWords(r RefinedSet) ([]string, bool) {
	if len(r.Forms) == 1 && r.Forms[0].Form == FormUnion {
		left, leftOk := unionWords(*r.Forms[0].A_)
		right, rightOk := unionWords(*r.Forms[0].B)
		if !leftOk || !rightOk {
			return nil, false
		}
		return append(append([]string{}, left...), right...), true
	}
	// the whole-strings arm of a maybe-absent template reads as its own
	// name beside the words
	if shape, ok := StringShapeOf(r); ok && shape == "string" {
		return []string{"string"}, true
	}
	points, ok := StringLiteralPoints(r)
	if !ok {
		return nil, false
	}
	// a SINGLE codepoint is exactly the shape a scalar singleton wears
	// too ({40}, a numeric enum's member, and the one-character chain
	// {"("} are the same RefinedSet) -- this caller has no sort at
	// hand to tell them apart, so a lone point declines here and falls
	// to scalarUnionValuesOf's numeric reading instead. Only a chain of
	// TWO OR MORE codepoints is unambiguous: no scalar union member is
	// itself a multi-point tuple, so length alone settles it there.
	if len(points) < 2 {
		return nil, false
	}
	for _, p := range points {
		if !(p >= 0x20 && p != 0x7f) {
			return nil, false
		}
	}
	s, sOk := FromPoints(points)
	if !sOk {
		return nil, false
	}
	return []string{s}, true
}

// scalarUnionValues is every exact number a pure-scalar union tree
// admits, in order -- or ok=false where any arm is more than a oneOf.
func scalarUnionValues(r RefinedSet) ([]float64, bool) {
	if len(r.Forms) != 1 {
		return nil, false
	}
	return scalarUnionValuesOf(r.Forms[0])
}

func scalarUnionValuesOf(r Refinement) ([]float64, bool) {
	if r.Form == FormOneOf {
		return r.W, true
	}
	if r.Form == FormUnion {
		left, leftOk := scalarUnionValues(*r.A_)
		right, rightOk := scalarUnionValues(*r.B)
		if !leftOk || !rightOk {
			return nil, false
		}
		return append(append([]float64{}, left...), right...), true
	}
	return nil, false
}

// pieceLabel is one concatenation piece, spelled plainly. Only an ENUM
// of literal chains (two or more words) spells as words -- a lone
// chain keeps the sortless codepoint spelling, because a tuple of
// numbers and a string share that shape and only the checked
// position's sort can tell them apart (the sorted-door rule
// FormatStringLiteral documents).
func pieceLabel(s RefinedSet) string {
	words, ok := unionWords(s)
	if ok && len(words) >= 2 {
		return "(" + joinStrings(words, " | ") + ")"
	}
	return FormatForDiagnostics(s)
}

// FormatForm is formatForm in the TS source.
func FormatForm(r Refinement) string {
	switch r.Form {
	case FormAtLeast:
		return ">= " + FormatNumber(r.A)
	case FormAbove:
		return "> " + FormatNumber(r.A)
	case FormAtMost:
		return "<= " + FormatNumber(r.A)
	case FormBelow:
		return "< " + FormatNumber(r.A)
	case FormInteger:
		return "integer"
	case FormMultipleOf:
		return "multiple of " + FormatNumber(r.A)
	case FormOneOf:
		if len(r.W) == 1 {
			return FormatNumber(r.W[0])
		}
		parts := make([]string, len(r.W))
		for i, w := range r.W {
			parts[i] = FormatNumber(w)
		}
		return "one of {" + joinStrings(parts, ", ") + "}"
	case FormEmptyTuple:
		return "the empty tuple"
	case FormConcatenation:
		return pieceLabel(*r.A_) + " · " + pieceLabel(*r.B)
	case FormStar:
		if IsCharacter(*r.A_) {
			return "string"
		}
		inner := FormatForDiagnostics(*r.A_)
		if containsSpace(inner) {
			return "array of (" + inner + ")"
		}
		return "array of " + inner
	case FormRepeat, FormRepeatWord:
		// the character windows read as strings via StringShapeOf; this
		// is the generic fallback
		inner := FormatForDiagnostics(*r.A_)
		var window string
		if r.Hi == nil {
			window = strconv.Itoa(r.Lo) + " or more"
		} else if r.Lo == *r.Hi {
			window = "exactly " + strconv.Itoa(r.Lo)
		} else {
			window = strconv.Itoa(r.Lo) + " to " + strconv.Itoa(*r.Hi)
		}
		return "(" + inner + ") × " + window
	case FormUnion:
		if absorbed, ok := AbsorbUnion(*r.A_, *r.B); ok {
			return FormatForDiagnostics(absorbed)
		}
		if deduped, ok := WithoutRepeatedArms(*r.A_, *r.B); ok {
			return FormatForDiagnostics(deduped)
		}
		// a union of literal chains is an enum of words -- spell the
		// words, never their codepoints
		leftWords, leftOk := unionWords(*r.A_)
		rightWords, rightOk := unionWords(*r.B)
		if leftOk && rightOk {
			return joinStrings(append(append([]string{}, leftWords...), rightWords...), " | ")
		}
		// a union of exact SCALARS is the enum of its numbers -- a
		// numeric enum's set reads 0 | 1 | 2, never nested ∪
		if values, ok := scalarUnionValuesOf(r); ok {
			parts := make([]string, len(values))
			for i, v := range values {
				parts[i] = FormatNumber(v)
			}
			return joinStrings(parts, " | ")
		}
		return "(" + FormatForDiagnostics(*r.A_) + " ∪ " + FormatForDiagnostics(*r.B) + ")"
	case FormDifference:
		return "(" + FormatForDiagnostics(*r.A_) + " ∖ " + FormatForDiagnostics(*r.B) + ")"
	}
	UnreachedForm(r)
	return ""
}

func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' {
			return true
		}
	}
	return false
}

// FormatForDiagnostics is the set, spelled for a reader -- the
// SPELLING folds (vacuous rays vanish, repeated bounds keep the
// tightest, absorbed union sides vanish) and reads the string shapes
// as strings; the set itself is untouched.
// z.number().min(0).max(100) reads >= 0 && <= 100;
// z.string().length(2) reads "string of exactly 2 characters".
func FormatForDiagnostics(r RefinedSet) string {
	if len(r.Forms) == 0 {
		return "any value"
	}
	if asString, ok := StringShapeOf(r); ok {
		return asString
	}
	folded := FoldedForms(r)
	parts := make([]string, len(folded))
	for i, f := range folded {
		parts[i] = FormatForm(f)
	}
	// everything folded away: the set was just the numbers
	if len(parts) == 0 {
		return "number"
	}
	return joinStrings(parts, " && ")
}
