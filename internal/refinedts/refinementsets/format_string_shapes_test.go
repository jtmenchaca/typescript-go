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
	if got := FormatForDiagnostics(StringTuple("US")); got != "85 · 83" {
		t.Errorf("FormatForDiagnostics(StringTuple(US)) = %q, want %q", got, "85 · 83")
	}
}
