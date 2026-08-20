package refinementsets

import "testing"

func TestAnAbsorbedUnionSideVanishesFromTheSpelling(t *testing.T) {
	// {0} ∪ (<= 0): the singleton says nothing the ray does not
	joined := MakeRefinedSet(Union(MakeRefinedSet(OneOf([]float64{0})), MakeRefinedSet(AtMost(0))))
	if got := FormatForDiagnostics(joined); got != "<= 0" {
		t.Errorf("FormatForDiagnostics(joined) = %q, want %q", got, "<= 0")
	}
	// and mirrored
	mirrored := MakeRefinedSet(Union(MakeRefinedSet(AtMost(0)), MakeRefinedSet(OneOf([]float64{0}))))
	if got := FormatForDiagnostics(mirrored); got != "<= 0" {
		t.Errorf("FormatForDiagnostics(mirrored) = %q, want %q", got, "<= 0")
	}
	// a side adding real members stays
	real := MakeRefinedSet(Union(MakeRefinedSet(OneOf([]float64{5})), MakeRefinedSet(AtMost(0))))
	if got := FormatForDiagnostics(real); got != "(5 ∪ <= 0)" {
		t.Errorf("FormatForDiagnostics(real) = %q, want %q", got, "(5 ∪ <= 0)")
	}
}

func TestAStarOfANumericSetReadsAsAnArray(t *testing.T) {
	got := FormatForDiagnostics(MakeRefinedSet(Star(MakeRefinedSet(AtLeast(1), AtMost(5), Integer))))
	want := "array of (>= 1 && <= 5 && integer)"
	if got != want {
		t.Errorf("FormatForDiagnostics(star) = %q, want %q", got, want)
	}
	got = FormatForDiagnostics(MakeRefinedSet(Star(MakeRefinedSet(Integer))))
	want = "array of integer"
	if got != want {
		t.Errorf("FormatForDiagnostics(star int) = %q, want %q", got, want)
	}
}

func TestTheStringShapesReadAsStrings(t *testing.T) {
	if got := FormatForDiagnostics(Codepoints); got != "character" {
		t.Errorf("FormatForDiagnostics(Codepoints) = %q, want character", got)
	}
	if got := FormatForDiagnostics(MakeRefinedSet(Star(Codepoints))); got != "string" {
		t.Errorf("FormatForDiagnostics(star codepoints) = %q, want string", got)
	}
	if got := FormatForDiagnostics(Repetition(Codepoints, 2, intPtr(2))); got != "string of exactly 2 characters" {
		t.Errorf("FormatForDiagnostics(rep 2,2) = %q", got)
	}
	// a 1-element sequence IS the scalar layer, so length(1) is the
	// character set itself
	if got := FormatForDiagnostics(Repetition(Codepoints, 1, intPtr(1))); got != "character" {
		t.Errorf("FormatForDiagnostics(rep 1,1) = %q, want character", got)
	}
	if got := FormatForDiagnostics(Repetition(Codepoints, 2, nil)); got != "string of at least 2 characters" {
		t.Errorf("FormatForDiagnostics(rep 2,nil) = %q", got)
	}
	if got := FormatForDiagnostics(Repetition(Codepoints, 0, intPtr(3))); got != "string of at most 3 characters" {
		t.Errorf("FormatForDiagnostics(rep 0,3) = %q", got)
	}
	if got := FormatForDiagnostics(Repetition(Codepoints, 2, intPtr(4))); got != "string of 2 to 4 characters" {
		t.Errorf("FormatForDiagnostics(rep 2,4) = %q", got)
	}
}

func TestThePatternChainsReadAsTheirPatterns(t *testing.T) {
	strings := MakeRefinedSet(Star(Codepoints))
	got := FormatForDiagnostics(MakeRefinedSet(Concatenation(StringTuple("ab"), strings)))
	if want := `string starting with "ab"`; got != want {
		t.Errorf("startsWith = %q, want %q", got, want)
	}
	got = FormatForDiagnostics(MakeRefinedSet(Concatenation(strings, StringTuple(".ts"))))
	if want := `string ending with ".ts"`; got != want {
		t.Errorf("endsWith = %q, want %q", got, want)
	}
	got = FormatForDiagnostics(MakeRefinedSet(Concatenation(
		strings,
		MakeRefinedSet(Concatenation(StringTuple("@"), strings)),
	)))
	if want := `string containing "@"`; got != want {
		t.Errorf("includes = %q, want %q", got, want)
	}
	// the surface STACKS a pattern onto C* -- the vacuous "string" form
	// vanishes beside the real constraint
	got = FormatForDiagnostics(MakeRefinedSet(
		Star(Codepoints),
		Concatenation(strings, StringTuple(".ts")),
	))
	if want := `string ending with ".ts"`; got != want {
		t.Errorf("stacked = %q, want %q", got, want)
	}
}

// TestANumericUnionOfPrintableCodepointsSpellsAsNumbersNotCharacters
// pins the garble a returned `"b" < "a" ? 200 : 40` diagnostic showed:
// Union(OneOf([200]), OneOf([40])) is a plain SCALAR union — 200 and 40
// happen to sit in the printable Unicode range (È and (), which used to
// let unionWords misread each one-member OneOf as a one-character
// string literal (StringLiteralPoints reads any singleton chain, and
// the old printability gate never checked length). unionWords now
// requires >= 2 codepoints before reading a chain as a word — the same
// "admitted-language rule" threshold abstractdomain.stringWordSet
// already enforces on the join side (a length-one word is the one a
// scalar position could reread) — so this union falls through to
// scalarUnionValuesOf and spells its actual numbers.
func TestANumericUnionOfPrintableCodepointsSpellsAsNumbersNotCharacters(t *testing.T) {
	joined := MakeRefinedSet(Union(MakeRefinedSet(OneOf([]float64{200})), MakeRefinedSet(OneOf([]float64{40}))))
	got := FormatForDiagnostics(joined)
	if got != "200 | 40" {
		t.Errorf(`FormatForDiagnostics(200|40) = %q, want "200 | 40" (not a garbled character reading)`, got)
	}
}

// TestATwoOrMoreCharacterStringEnumStillSpellsAsWords confirms the
// >= 2 codepoint floor above does not regress the legitimate case
// unionWords exists for: an enum of real multi-character string
// literals still spells as quoted words, never as a numeric or
// codepoint reading.
func TestATwoOrMoreCharacterStringEnumStillSpellsAsWords(t *testing.T) {
	joined := MakeRefinedSet(Union(StringTuple("margin"), StringTuple("border")))
	got := FormatForDiagnostics(joined)
	if want := `"margin" | "border"`; got != want {
		t.Errorf("FormatForDiagnostics(margin|border) = %q, want %q", got, want)
	}
}
