// The binding ledger's own tests (DERIVATION-TRACE.md, the binding
// ledger): capture at the write, reclaim at a bare-name leaf, recursive
// reclaim, cycle refusal by place, no chain when the leaf is not a bare
// name, and no ledger at all with tracing off.
//
// Each test drives the recorder directly rather than through a walk, so
// the ledger's rules are pinned independently of which construct happens
// to bind a name in a fixture.

package derivation

import "testing"

// recording installs a Recorder for this test and returns it with its
// closer. requestedLine 0 records every position.
func recording(t *testing.T) *Recorder {
	t.Helper()
	recorder, closer := BeginRecording("ts", "f.ts:1", 0)
	t.Cleanup(closer)
	return recorder
}

// declineLeaf opens a span, declines it, and closes it — one closed
// root whose leaf is that span.
func declineLeaf(name string, construct string, rng string, gate string) {
	handle := Begin(name, construct, rng)
	handle.Decline(gate, "", "no set")
	handle.End()
}

// judgedOn opens a JudgedName root over one declined bare-name read and
// closes it, which is what triggers the chain reclaim.
func judgedOn(construct string, rng string) {
	root := Begin(JudgedName, construct, rng)
	inner := Begin("evaluateExpression", construct, rng)
	inner.Decline("", "", "no set")
	inner.End()
	root.Decline("the walk holds nothing that pins this value", "", "no set")
	root.End()
}

// TestBindingLedger_CapturesTheProducerAtTheWrite pins the capture half:
// an environment write remembers the span subtree that produced the
// written value, keyed by the place written.
func TestBindingLedger_CapturesTheProducerAtTheWrite(t *testing.T) {
	recorder := recording(t)
	declineLeaf("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29", "no builtin model recognizes this callee")
	RecordBinding("raw", "f.ts:1:15-1:29")
	bound := recorder.bindings["raw"]
	if bound == nil {
		t.Fatal("the ledger remembered nothing for raw")
	}
	if held := bound.Attributes[AttrConstruct]; held != "opaqueSource()" {
		t.Fatalf("the remembered producer is %q, want opaqueSource()", held)
	}
}

// TestBindingLedger_ReclaimsAtABareNameLeaf pins the reclaim half: a
// judged position whose derivation stopped at a bare name carries the
// binding's derivation as a chained root.
func TestBindingLedger_ReclaimsAtABareNameLeaf(t *testing.T) {
	recorder := recording(t)
	declineLeaf("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29", "no builtin model recognizes this callee")
	RecordBinding("raw", "f.ts:1:15-1:29")
	judgedOn("raw", "f.ts:2:10-2:13")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	chain := traces[0].Chain
	if len(chain) != 1 {
		t.Fatalf("the chain holds %d roots, want 1", len(chain))
	}
	if held := chain[0].Attributes[AttrConstruct]; held != "opaqueSource()" {
		t.Fatalf("the chained root is %q, want opaqueSource()", held)
	}
	// a claimed root sheds the root-only attributes — it is no longer a
	// document of its own
	if _, held := chain[0].Attributes[AttrPosition]; held {
		t.Fatal("the chained root kept refinery.position; a claimed root sheds it")
	}
	// and the projection still reads the MAIN root, not the chain
	if sentence := ProjectTrace(traces[0]); sentence != "raw: evaluateExpression declined" {
		t.Fatalf("the projection is %q — the chain sharpened the sentence, which it must never do", sentence)
	}
}

// TestBindingLedger_ReclaimsRecursively pins the recursive half: a
// chained root whose own leaf is again a bare name reclaims that name's
// binding too, nearest binding first.
func TestBindingLedger_ReclaimsRecursively(t *testing.T) {
	recorder := recording(t)
	declineLeaf("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29", "no builtin model recognizes this callee")
	RecordBinding("raw", "f.ts:1:15-1:29")
	// `const widened = raw as number` — its own leaf is the bare name raw
	outer := Begin("evaluateExpression", "raw as number", "f.ts:2:19-2:32")
	inner := Begin("evaluateExpression", "raw", "f.ts:2:19-2:22")
	inner.Decline("", "", "no set")
	inner.End()
	outer.Decline("", "", "no set")
	outer.End()
	RecordBinding("widened", "f.ts:2:19-2:32")

	judgedOn("widened", "f.ts:3:10-3:17")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	chain := traces[0].Chain
	if len(chain) != 2 {
		t.Fatalf("the chain holds %d roots, want 2", len(chain))
	}
	if held := chain[0].Attributes[AttrConstruct]; held != "raw as number" {
		t.Fatalf("the nearest chained root is %q, want raw as number", held)
	}
	if held := chain[1].Attributes[AttrConstruct]; held != "opaqueSource()" {
		t.Fatalf("the second chained root is %q, want opaqueSource()", held)
	}
}

// TestBindingLedger_RefusesACycleByPlace pins the termination rule:
// `let a = b; let b = a` writes each place from the other, so a chain
// walked by place alone would not terminate. A place already chained is
// not chained again.
func TestBindingLedger_RefusesACycleByPlace(t *testing.T) {
	recorder := recording(t)
	// a's producer is the bare name b
	declineLeaf("evaluateExpression", "b", "f.ts:1:11-1:12", "")
	RecordBinding("a", "f.ts:1:11-1:12")
	// b's producer is the bare name a
	declineLeaf("evaluateExpression", "a", "f.ts:2:11-2:12", "")
	RecordBinding("b", "f.ts:2:11-2:12")

	judgedOn("a", "f.ts:3:10-3:11")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	// a → (producer spelling b) → b → (producer spelling a) → a is
	// already chained, and the walk stops: two roots, not an unbounded run
	if held := len(traces[0].Chain); held != 2 {
		t.Fatalf("the chain holds %d roots, want 2 — the cycle closed at the repeated place", held)
	}
}

// TestBindingLedger_NoChainWhenTheLeafIsNotABareName pins the gate on
// the reclaim: a leaf that declined on a compound construct already
// named the construct that blocked it, and there is no binding behind
// that to chain.
func TestBindingLedger_NoChainWhenTheLeafIsNotABareName(t *testing.T) {
	recorder := recording(t)
	declineLeaf("evaluateExpression", "opaqueSource()", "f.ts:1:15-1:29", "no builtin model recognizes this callee")
	RecordBinding("x", "f.ts:1:15-1:29")

	judgedOn("x[0]", "f.ts:2:10-2:14")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	if held := len(traces[0].Chain); held != 0 {
		t.Fatalf("the chain holds %d roots, want 0 — x[0] is not a bare name", held)
	}
}

// TestBindingLedger_NoLedgerWithTracingOff pins the off path: the ledger
// exists only while a collector is installed, so a write with no
// Recorder records nothing and costs nothing.
func TestBindingLedger_NoLedgerWithTracingOff(t *testing.T) {
	// no recording() — nothing is registered on this goroutine
	if Active() {
		t.Fatal("a Recorder is registered; this test needs the off path")
	}
	// the write must not panic and must not build a ledger
	RecordBinding("raw", "f.ts:1:15-1:29")
	if recorder := current(); recorder != nil {
		t.Fatal("RecordBinding created a Recorder; off is a nil test")
	}
}

// TestBareNameLeafOf_RefusesALeafWithChildren pins the second half of
// "stopped at a bare name": a span that opened children of its own did
// derive something below it, so it did not stop at the name.
func TestBareNameLeafOf_RefusesALeafWithChildren(t *testing.T) {
	child := spanAt("s2", "inner", "the inner gate")
	root := &Span{
		ID:     "s1",
		Name:   "evaluateExpression",
		Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "widened",
			AttrRange:     "f.ts:1:1-1:8",
		},
		Children: []*Span{child},
	}
	// the deepest declined span here is the CHILD, whose construct is
	// "inner" — a bare name by spelling, and it has no children, so the
	// leaf test passes on it and not on the parent
	if place := BareNameLeafOf(root); place != "inner" {
		t.Fatalf("BareNameLeafOf = %q, want the childless deepest leaf inner", place)
	}
}
