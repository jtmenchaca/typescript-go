package refinementsets

import "testing"

func TestLiteralChainsFormatAtAsStringsOnlyThroughTheSortedDoor(t *testing.T) {
	if got, ok := FormatStringLiteral(StringTuple("USA")); !ok || got != `"USA"` {
		t.Errorf("FormatStringLiteral(USA) = %q, %v, want \"USA\", true", got, ok)
	}
	if got, ok := FormatStringLiteral(StringTuple("")); !ok || got != `""` {
		t.Errorf("FormatStringLiteral(\"\") = %q, %v, want \"\", true", got, ok)
	}
	if _, ok := FormatStringLiteral(MakeRefinedSet(AtLeast(0))); ok {
		t.Errorf("FormatStringLiteral(atLeast(0)) should fail")
	}
	// a two-or-more-character literal is now a Word LEAF (StringTuple's
	// own collapse), not a chain of Concatenation nodes: StringShapeOf
	// does not read a bare Word (ConcatParts returns it as one
	// unsplittable part, under the >= 2 parts floor the pattern-chain
	// reading needs), so FormatForDiagnostics falls to FormatForm's own
	// FormWord case, which spells the literal as its quoted text -- the
	// true string, replacing the old per-codepoint "85 · 83" arithmetic
	// a Concatenation chain used to read as.
	if got := FormatForDiagnostics(StringTuple("US")); got != `"US"` {
		t.Errorf("FormatForDiagnostics(StringTuple(US)) = %q, want %q", got, `"US"`)
	}
}
