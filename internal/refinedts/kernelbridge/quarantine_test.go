package kernelbridge

import "testing"

// TestQuarantinedQuestionDeclinesByKey pins the quarantine gate itself:
// a key named by SetQuarantinedQuestions reports quarantined; any other
// key does not.
func TestQuarantinedQuestionDeclinesByKey(t *testing.T) {
	SetQuarantinedQuestions([]string{"member\x00killer-wire"})
	t.Cleanup(func() { SetQuarantinedQuestions(nil) })

	if !isQuarantined("member\x00killer-wire") {
		t.Fatalf("isQuarantined(the named key) = false, want true")
	}
	if isQuarantined("member\x00some-other-wire") {
		t.Fatalf("isQuarantined(a different key) = true, want false")
	}
	if isQuarantined("scalarSubset\x00killer-wire") {
		t.Fatalf("isQuarantined(same wire, different op) = true, want false — the full op+key string is the identity, not the wire alone")
	}
}

// TestSetQuarantinedQuestionsEmptyDisables pins the default: no
// quarantine configured (nil or empty) declines nothing.
func TestSetQuarantinedQuestionsEmptyDisables(t *testing.T) {
	SetQuarantinedQuestions([]string{"member\x00abc"})
	SetQuarantinedQuestions(nil)
	if isQuarantined("member\x00abc") {
		t.Fatalf("isQuarantined after clearing with nil = true, want false")
	}

	SetQuarantinedQuestions([]string{"member\x00abc"})
	SetQuarantinedQuestions([]string{})
	if isQuarantined("member\x00abc") {
		t.Fatalf("isQuarantined after clearing with an empty slice = true, want false")
	}
}

// TestSetQuarantinedQuestionsReplacesNotAccumulates pins that a second
// call REPLACES the quarantine list rather than adding to it — the
// shape a fresh process start (cmd/tsgo's -quarantine flag, parsed
// once) needs: one call states the whole set for this run.
func TestSetQuarantinedQuestionsReplacesNotAccumulates(t *testing.T) {
	SetQuarantinedQuestions([]string{"member\x00first"})
	SetQuarantinedQuestions([]string{"scalarSubset\x00second"})
	t.Cleanup(func() { SetQuarantinedQuestions(nil) })

	if isQuarantined("member\x00first") {
		t.Fatalf("isQuarantined(the first call's key) = true, want false — replaced by the second call")
	}
	if !isQuarantined("scalarSubset\x00second") {
		t.Fatalf("isQuarantined(the second call's key) = false, want true")
	}
}

// TestSetQuarantinedQuestionsMultipleKeys pins that more than one
// quarantined question can be named at once — a coordinator restarting
// a child that had already survived one quarantine and then died again
// on a second question needs both declined, not just the latest.
func TestSetQuarantinedQuestionsMultipleKeys(t *testing.T) {
	SetQuarantinedQuestions([]string{"member\x00a", "scalarSubset\x00b"})
	t.Cleanup(func() { SetQuarantinedQuestions(nil) })

	if !isQuarantined("member\x00a") {
		t.Fatalf("isQuarantined(member\\x00a) = false, want true")
	}
	if !isQuarantined("scalarSubset\x00b") {
		t.Fatalf("isQuarantined(scalarSubset\\x00b) = false, want true")
	}
	if isQuarantined("member\x00c") {
		t.Fatalf("isQuarantined(member\\x00c) = true, want false — never named")
	}
}
