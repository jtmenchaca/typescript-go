package zod

import "testing"

func TestPatternOrUnread_AVerifiedV4PatternCompilesExact(t *testing.T) {
	compiled := PatternOrUnread(V4FormatPatterns["cuid2"], "cuid2")
	if compiled.Unsupported != nil {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported.Unsupported)
	}
	if compiled.Annotation.Unread {
		t.Errorf("Unread = true, want false for a format grammar the compiler speaks")
	}
	if compiled.Annotation.Word == nil || compiled.Annotation.Word.Text != "cuid2" {
		t.Errorf("Word = %+v, want {Text: cuid2}", compiled.Annotation.Word)
	}
}

func TestPatternOrUnread_ALookaroundPatternRidesUnread(t *testing.T) {
	// email leads with two negative lookaheads the format grammar CAN
	// read as differences (per chain_root_constructor.go's comment);
	// hostname's length lookahead is the harder case documented in
	// zod_format_patterns.go -- exercise it here.
	pattern := "^(?=.{1,253}\\.?$)[a-zA-Z0-9].*$"
	compiled := PatternOrUnread(pattern, "hostname-like")
	if compiled.Unsupported != nil {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported.Unsupported)
	}
	if !compiled.Annotation.Unread {
		t.Errorf("Unread = false, want true for a length-lookahead pattern the grammar cannot compile")
	}
}
