// collectionCallOf / reduceCallExpressionOf's own RECEIVER gate: the
// interior-path widening (collectionReceiverPathOf) admits a
// property-access chain rooted at an identifier or `this` beside the
// bare identifier the gate already read, and leaves every other
// decline (a spread argument, an optional step, a computed key) exactly
// as it was.
package walk

import (
	"testing"
)

// collectionCallSourceOf parses one expression statement and reads its
// collectionCallOf answer directly, without going through the callback
// scan (callbackArrowOf, ir_callback_summary_test.go) — these tests are
// about the RECEIVER gate alone, not the callback body.
func collectionCallSourceOf(t *testing.T, source string) (collectionCall, bool) {
	t.Helper()
	statements := loweringParse(t, source)
	if len(statements) != 1 {
		t.Fatalf("len(statements) = %d, want 1", len(statements))
	}
	return collectionCallOf(Unwrapped(statements[0].AsExpressionStatement().Expression))
}

func TestCollectionCallOf_ABareIdentifierReceiverStillResolves(t *testing.T) {
	call, ok := collectionCallSourceOf(t, `xs.map(x => x + 1);`)
	if !ok {
		t.Fatalf("collectionCallOf(xs.map(...)) declined — the bare-identifier receiver regressed")
	}
	if call.Receiver != "xs" {
		t.Errorf("Receiver = %q, want %q", call.Receiver, "xs")
	}
	if call.Method != "map" {
		t.Errorf("Method = %q, want %q", call.Method, "map")
	}
}

// the ledger's own named shape: `request.samples.map(cb)` — an
// INTERIOR-PATH receiver one property step below its root — now admits
// at the syntax gate and spells the dotted receiver arraySlotsOf already
// knows how to look up.
func TestCollectionCallOf_AnInteriorPathReceiverResolves(t *testing.T) {
	call, ok := collectionCallSourceOf(t, `request.samples.map(s => s + 1);`)
	if !ok {
		t.Fatalf("collectionCallOf(request.samples.map(...)) declined — the interior-path receiver did not widen")
	}
	if call.Receiver != "request.samples" {
		t.Errorf("Receiver = %q, want %q", call.Receiver, "request.samples")
	}
	if call.Method != "map" {
		t.Errorf("Method = %q, want %q", call.Method, "map")
	}
}

// a `this`-rooted interior path spells its root as "this", matching the
// convention every other dotted slot lookup in this package uses.
func TestCollectionCallOf_AThisRootedInteriorPathReceiverResolves(t *testing.T) {
	call, ok := collectionCallSourceOf(t, `this.samples.filter(s => s > 0);`)
	if !ok {
		t.Fatalf("collectionCallOf(this.samples.filter(...)) declined")
	}
	if call.Receiver != "this.samples" {
		t.Errorf("Receiver = %q, want %q", call.Receiver, "this.samples")
	}
	if call.Method != "filter" {
		t.Errorf("Method = %q, want %q", call.Method, "filter")
	}
}

// a two-step interior path (`a.b.c.map(...)`) widens the same way — the
// gate is not limited to exactly one property step.
func TestCollectionCallOf_ATwoStepInteriorPathReceiverResolves(t *testing.T) {
	call, ok := collectionCallSourceOf(t, `request.payload.samples.map(s => s);`)
	if !ok {
		t.Fatalf("collectionCallOf(request.payload.samples.map(...)) declined")
	}
	if call.Receiver != "request.payload.samples" {
		t.Errorf("Receiver = %q, want %q", call.Receiver, "request.payload.samples")
	}
}

// an OPTIONAL step in the receiver path still declines — the widening is
// plain-path only, mirroring propertyPathOf's own rule (as opposed to
// propertyPathAdmittingRootOptionalStep's wider one, which this reader
// does not use).
func TestCollectionCallOf_AnOptionalStepInTheReceiverPathStillDeclines(t *testing.T) {
	if _, ok := collectionCallSourceOf(t, `request?.samples.map(s => s);`); ok {
		t.Errorf("collectionCallOf(request?.samples.map(...)) resolved — an optional step should still decline")
	}
}

// a computed receiver step (`request[key].map(...)`) still declines —
// propertyPathOf only reads plain identifier/private-identifier steps.
func TestCollectionCallOf_AComputedReceiverStepStillDeclines(t *testing.T) {
	if _, ok := collectionCallSourceOf(t, `request[key].map(s => s);`); ok {
		t.Errorf("collectionCallOf(request[key].map(...)) resolved — a computed step should still decline")
	}
}

// an optional CALL itself (`xs?.map(...)`) still declines, unrelated to
// the receiver widening — this gate was and remains untouched.
func TestCollectionCallOf_AnOptionalCallStillDeclines(t *testing.T) {
	if _, ok := collectionCallSourceOf(t, `xs?.map(s => s);`); ok {
		t.Errorf("collectionCallOf(xs?.map(...)) resolved — an optional call should still decline")
	}
}

/* ── reduceCallExpressionOf shares the same receiver gate ──────────── */

func TestReduceCallOf_AnInteriorPathReceiverResolves(t *testing.T) {
	statements := loweringParse(t, `request.samples.reduce((total, s) => total + s, 0);`)
	source, seed, ok := reduceCallOf(Unwrapped(statements[0].AsExpressionStatement().Expression))
	if !ok {
		t.Fatalf("reduceCallOf(request.samples.reduce(...)) declined — the interior-path receiver did not widen")
	}
	if source.Receiver != "request.samples" {
		t.Errorf("Receiver = %q, want %q", source.Receiver, "request.samples")
	}
	if seed == nil {
		t.Errorf("seed = nil, want the literal 0 node")
	}
}

func TestOneArgumentReduceCallOf_AnInteriorPathReceiverResolves(t *testing.T) {
	statements := loweringParse(t, `request.samples.reduce((total, s) => total + s);`)
	source, ok := oneArgumentReduceCallOf(Unwrapped(statements[0].AsExpressionStatement().Expression))
	if !ok {
		t.Fatalf("oneArgumentReduceCallOf(request.samples.reduce(...)) declined")
	}
	if source.Receiver != "request.samples" {
		t.Errorf("Receiver = %q, want %q", source.Receiver, "request.samples")
	}
}
