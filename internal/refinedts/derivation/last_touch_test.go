// The last-touch ledger's own tests: a havocked binding's leaf carries
// the attribute with kind and construct/range, an untouched binding's
// leaf carries none, and a touch recorded with no named site spells
// just the kind. Driven directly against the recorder, the same way
// binding_ledger_test.go pins the binding ledger — the walk chokepoints
// are exercised through the fixture suite, not re-simulated here.

package derivation

import "testing"

// TestLastTouch_HavockedLeafCarriesTheAttribute pins the emission half:
// a judged position whose derivation stopped at a bare name that was
// last havocked by a named construct carries refinery.last-touch on its
// leaf, spelled "havocked by <construct>  @<range>".
func TestLastTouch_HavockedLeafCarriesTheAttribute(t *testing.T) {
	recorder := recording(t)
	RecordTouch("s", TouchHavocked, "s.add(x)", "f.ts:9:3-9:11")
	judgedOn("s", "f.ts:10:10-10:11")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	leaf := DeepestDeclined(traces[0].Root)
	if leaf == nil {
		t.Fatal("the trace carries no declined leaf")
	}
	want := "havocked by s.add(x)  @f.ts:9:3-9:11"
	if held := leaf.Attributes[AttrLastTouch]; held != want {
		t.Fatalf("refinery.last-touch = %q, want %q", held, want)
	}
}

// TestLastTouch_NoSiteSpellsJustTheKind pins the empty-site spelling: a
// touch recorded with no named construct/range (a chokepoint reached
// with no TouchSite active) spells the attribute as the bare kind word.
func TestLastTouch_NoSiteSpellsJustTheKind(t *testing.T) {
	recorder := recording(t)
	RecordTouch("s", TouchForgotten, "", "")
	judgedOn("s", "f.ts:10:10-10:11")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	leaf := DeepestDeclined(traces[0].Root)
	if leaf == nil {
		t.Fatal("the trace carries no declined leaf")
	}
	if held := leaf.Attributes[AttrLastTouch]; held != "forgotten" {
		t.Fatalf("refinery.last-touch = %q, want the bare kind %q", held, "forgotten")
	}
}

// TestLastTouch_UntouchedLeafCarriesNone pins the negative half: a
// bare-name leaf with NO recorded touch for that place carries no
// refinery.last-touch attribute at all — the ledger never guesses.
func TestLastTouch_UntouchedLeafCarriesNone(t *testing.T) {
	recorder := recording(t)
	judgedOn("neverTouched", "f.ts:10:10-10:11")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	leaf := DeepestDeclined(traces[0].Root)
	if leaf == nil {
		t.Fatal("the trace carries no declined leaf")
	}
	if _, held := leaf.Attributes[AttrLastTouch]; held {
		t.Fatal("the untouched leaf carries refinery.last-touch; it should carry none")
	}
}

// TestLastTouch_NotABareNameCarriesNone pins the same gate the binding
// ledger's chain uses: a leaf that declined on a compound construct
// (`x[0]`) is not a bare name, so no last-touch lookup is even
// attempted, regardless of what the ledger holds for `x`.
func TestLastTouch_NotABareNameCarriesNone(t *testing.T) {
	recorder := recording(t)
	RecordTouch("x", TouchHavocked, "x.clear()", "f.ts:3:1-3:11")
	judgedOn("x[0]", "f.ts:10:10-10:14")

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorded %d judged traces, want 1", len(traces))
	}
	leaf := DeepestDeclined(traces[0].Root)
	if leaf == nil {
		t.Fatal("the trace carries no declined leaf")
	}
	if _, held := leaf.Attributes[AttrLastTouch]; held {
		t.Fatal("x[0] is not a bare name; it should carry no refinery.last-touch")
	}
}

// TestLastTouch_TouchSiteNamesTheConstruct pins the caller-side seam a
// chokepoint that holds no node of its own uses: RecordTouchFromSite
// reads whatever TouchSite most recently named, and the site does not
// leak past its closer.
func TestLastTouch_TouchSiteNamesTheConstruct(t *testing.T) {
	recording(t)
	closeSite := TouchSite("s.add(x)", "f.ts:9:3-9:11")
	RecordTouchFromSite("s", TouchHavocked)
	closeSite()
	// a touch recorded AFTER the site closes gets no construct/range —
	// the site does not leak past its own closer
	RecordTouchFromSite("t", TouchForgotten)

	if held := LastTouchOf("s"); held != "havocked by s.add(x)  @f.ts:9:3-9:11" {
		t.Fatalf("LastTouchOf(s) = %q, want the named site spelled in", held)
	}
	if held := LastTouchOf("t"); held != "forgotten" {
		t.Fatalf("LastTouchOf(t) = %q, want the bare kind — the site had already closed", held)
	}
}

// TestLastTouch_NoLedgerWithTracingOff pins the off path: the ledger
// exists only while a collector is installed, so a touch recorded with
// no Recorder does not panic and remembers nothing.
func TestLastTouch_NoLedgerWithTracingOff(t *testing.T) {
	if Active() {
		t.Fatal("a Recorder is registered; this test needs the off path")
	}
	RecordTouch("s", TouchHavocked, "s.add(x)", "f.ts:9:3-9:11")
	if recorder := current(); recorder != nil {
		t.Fatal("RecordTouch created a Recorder; off is a nil test")
	}
	closeSite := TouchSite("x", "f.ts:1:1-1:2")
	closeSite()
}
