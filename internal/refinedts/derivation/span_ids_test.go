// Span ids are a property of the EMITTED DOCUMENT, not of the walk
// (DERIVATION-TRACE.md): assigned at emission in tree order — the root,
// then depth-first through its subtree, then each chained root after the
// main root's subtree. Two identical trees produce identical ids, and
// comparisons never normalize them away.
//
// These tests drive the recorder through the real close path, because
// what the ruling is about is precisely the difference between the order
// the walk OPENED spans in and the order the emitted tree holds them.

package derivation

import "testing"

// TestSpanIDs_AreAssignedInTreeOrderNotWalkOrder pins the ruling. The
// walk here opens a discarded root FIRST — a sub-read no judged position
// reclaims, exactly what the binding ledger's line-gate exemption
// produces — so every id the judged position's spans took at Begin is
// offset. The emitted trace must still number from s1 in tree order.
func TestSpanIDs_AreAssignedInTreeOrderNotWalkOrder(t *testing.T) {
	recorder := recording(t)
	// three spans the walk opens and throws away: not a judged position,
	// and never reclaimed
	discarded := Begin("evaluateExpression", "elsewhere()", "f.ts:1:1-1:12")
	inner := Begin("builtinModels", "elsewhere()", "f.ts:1:1-1:12")
	inner.Decline("no builtin model recognizes this callee", "", "no model")
	inner.End()
	discarded.Decline("", "", "no set")
	discarded.End()

	// the judged position, whose Begin-time ids therefore start at s4
	root := Begin(JudgedName, "widened", "f.ts:2:10-2:17")
	read := Begin("evaluateExpression", "widened", "f.ts:2:10-2:17")
	read.Decline("", "", "no set")
	read.End()
	root.Decline("the walk holds nothing that pins this value", "", "no set")
	root.End()

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	if held := traces[0].Root.ID; held != "s1" {
		t.Fatalf("the root id is %q, want s1 — ids number the emitted tree, not the walk", held)
	}
	if held := traces[0].Root.Children[0].ID; held != "s2" {
		t.Fatalf("the root's first child id is %q, want s2", held)
	}
}

// TestSpanIDs_ChainedRootsFollowTheMainSubtree pins the order the ruling
// names: the main root's whole subtree first, then each chained root.
func TestSpanIDs_ChainedRootsFollowTheMainSubtree(t *testing.T) {
	recorder := recording(t)
	// the binding's producer, with a sub-read of its own — the shape the
	// probe has, where builtinModels sits under evaluateExpression
	producer := Begin("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29")
	model := Begin("builtinModels", "opaqueSource()", "f.ts:1:15-1:29")
	model.Decline("no builtin model recognizes this callee", "", "no model")
	model.End()
	producer.Decline("", "", "no set")
	producer.End()
	RecordBinding("raw", "f.ts:1:15-1:29")
	judgedOn("raw", "f.ts:2:10-2:13")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	trace := traces[0]
	// main root s1, its one child s2, then the chained root s3 and ITS
	// child s4 — the chain numbers after the main subtree, never
	// interleaved with it
	if held := trace.Root.ID; held != "s1" {
		t.Fatalf("the main root id is %q, want s1", held)
	}
	if held := trace.Root.Children[0].ID; held != "s2" {
		t.Fatalf("the main root's child id is %q, want s2", held)
	}
	if len(trace.Chain) != 1 {
		t.Fatalf("the chain holds %d roots, want 1", len(trace.Chain))
	}
	if held := trace.Chain[0].ID; held != "s3" {
		t.Fatalf("the chained root id is %q, want s3 — chained roots follow the main subtree", held)
	}
	if held := trace.Chain[0].Children[0].ID; held != "s4" {
		t.Fatalf("the chained root's child id is %q, want s4", held)
	}
}

// TestSpanIDs_AreStableAcrossRepeatedEmission pins idempotence. Traces()
// has more than one caller per run — -explain renders the tree and
// -explain-json encodes it — so a second ask must hand back the same
// ids, not renumber on top of the first pass.
func TestSpanIDs_AreStableAcrossRepeatedEmission(t *testing.T) {
	recorder := recording(t)
	declineLeaf("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29", "no builtin model recognizes this callee")
	RecordBinding("raw", "f.ts:1:15-1:29")
	judgedOn("raw", "f.ts:2:10-2:13")

	first := Render(recorder.Traces()[0])
	second := Render(recorder.Traces()[0])
	if first != second {
		t.Fatalf("a second emission differs from the first:\n%s\nversus\n%s", first, second)
	}
}
