package refinementsets

import "testing"

func TestAnchoredClassesAndQuantifiersCompile(t *testing.T) {
	if got := FormatGrammar("^[0-9]+$", ""); !got.Ok {
		t.Errorf("^[0-9]+$ should compile, got unsupported: %s", got.Unsupported)
	}
	if got := FormatGrammar("^(a|b)c?$", ""); !got.Ok {
		t.Errorf("^(a|b)c?$ should compile, got unsupported: %s", got.Unsupported)
	}
	if got := FormatGrammar("^a{2,4}$", ""); !got.Ok {
		t.Errorf("^a{2,4}$ should compile, got unsupported: %s", got.Unsupported)
	}
	if got := FormatGrammar(`^\d\w\s$`, ""); !got.Ok {
		t.Errorf(`^\d\w\s$ should compile, got unsupported: %s`, got.Unsupported)
	}
	if got := FormatGrammar("^[^a-z]$", ""); !got.Ok {
		t.Errorf("^[^a-z]$ should compile, got unsupported: %s", got.Unsupported)
	}
}

func TestAnUnanchoredSideIsPaddedARegexMatchesASubstring(t *testing.T) {
	padded := FormatGrammar("abc", "")
	if !padded.Ok {
		t.Fatalf("expected a set, got unsupported: %s", padded.Unsupported)
	}
	// C* . (abc . C*): both sides padded
	if got := padded.Set.Forms[0].Form; got != FormConcatenation {
		t.Errorf("padded.Set.Forms[0].Form = %v, want concatenation", got)
	}
}

func TestWhatLeavesRegularLanguagesIsRefused(t *testing.T) {
	if FormatGrammar(`(a)\1`, "").Ok { // backreference
		t.Errorf("backreference should be unsupported")
	}
	if FormatGrammar("(?=a)b", "").Ok { // lookahead
		t.Errorf("lookahead should be unsupported")
	}
	if FormatGrammar("a^b", "").Ok { // mid-anchor
		t.Errorf("mid-anchor should be unsupported")
	}
	if FormatGrammar("a{,}", "").Ok { // malformed count
		t.Errorf("malformed count should be unsupported")
	}
}
