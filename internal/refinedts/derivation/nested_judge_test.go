// A JUDGE CAN CLOSE AS SOMEONE ELSE'S CHILD (trace.go's Traces comment):
// checking a call's arguments runs from inside evaluateExpression's own
// span for the call expression, so the argument's checkAssignability
// root opens while that outer span is still on the stack. It is not the
// bottom of the stack when it closes, so End() files it as a child
// rather than into r.finished — and a root-only scan of r.finished would
// never surface it as its own judged position, even though its status
// and children are exactly what a genuine root judge carries.
//
// Found against A10.sink.invariant.ts:17 (packages/tests/e2e/membership/
// A10.sink.invariant/A10.sink.invariant.ts): CheckContractArguments
// (walk/call_argument_contracts.go) calls CheckAssignability on a
// generic call's argument while EvaluateCallExpression's own
// evaluateExpression span for the whole call is still open, so the
// argument's checkAssignability root becomes a child of it instead of a
// root of its own.

package derivation

import "testing"

// TestTraces_SurfacesAJudgeNestedUnderAnotherSpan pins the fix: a
// checkAssignability span that closes as a CHILD of an enclosing span —
// never as the bottom of the stack — is still one of Traces()' judged
// positions, with its own already-recorded children intact.
func TestTraces_SurfacesAJudgeNestedUnderAnotherSpan(t *testing.T) {
	recorder := recording(t)

	outer := Begin("evaluateExpression", "withLength(\"ab\")", "f.ts:1:1-1:20")
	judge := Begin(JudgedName, "\"ab\"", "f.ts:1:12-1:16")
	judge.Answer("{length: unknown}")
	judge.End()
	outer.Answer("number")
	outer.End()

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1 — the nested judge should surface on its own", len(traces))
	}
	if held := traces[0].Root.Name; held != JudgedName {
		t.Fatalf("the surfaced trace's root name is %q, want %q", held, JudgedName)
	}
	if held := traces[0].Root.Status; held != Answered {
		t.Fatalf("the surfaced trace's root status is %q, want %q", held, Answered)
	}
	if held := traces[0].Root.Attributes[AttrRange]; held != "f.ts:1:12-1:16" {
		t.Fatalf("the surfaced trace's range is %q, want the nested judge's own range", held)
	}
}

// TestTraces_StillSurfacesARootLevelJudge pins that the fix is additive:
// a checkAssignability span that DOES close at the bottom of the stack —
// the ordinary case every other derivation test exercises — still
// surfaces exactly as before.
func TestTraces_StillSurfacesARootLevelJudge(t *testing.T) {
	recorder := recording(t)
	root := Begin(JudgedName, "widened", "f.ts:2:10-2:17")
	root.Decline("the walk holds nothing that pins this value", "", "no set")
	root.End()

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	if held := traces[0].Root.Status; held != Declined {
		t.Fatalf("the root-level judge's status is %q, want %q", held, Declined)
	}
}
