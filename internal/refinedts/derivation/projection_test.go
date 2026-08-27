// The projection rule's own tests: the normative tie-break, the
// template, and the two visible work items. These are the rules
// conformance compares ACROSS the three adapters
// (packages/tests/DERIVATION-TRACE.md), so they are pinned here rather
// than left to whichever fixture happens to exercise them.

package derivation

import "testing"

// spanAt builds a declined span with a gate, for the tie-break tests.
func spanAt(id string, name string, gate string, children ...*Span) *Span {
	return &Span{
		ID:     id,
		Name:   name,
		Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: name,
			AttrRange:     "f.ts:1:1-1:9",
			AttrGate:      gate,
		},
		Children: children,
	}
}

// TestDeepestDeclined_DepthOutranksASiblingsDecline pins the first half
// of the normative tie-break: a deeper subtree's decline wins over a
// shallower sibling's, because the deeper one is the sub-read the
// shallower was waiting on.
func TestDeepestDeclined_DepthOutranksASiblingsDecline(t *testing.T) {
	deep := spanAt("s3", "inner", "the deep gate")
	root := spanAt("s1", "root", "the root gate",
		spanAt("s2", "shallow", "the shallow gate"),
		spanAt("s4", "branch", "the branch gate", deep),
	)
	if held := DeepestDeclined(root); held != deep {
		t.Fatalf("DeepestDeclined = %v, want the deeper span s3", held)
	}
}

// TestDeepestDeclined_FirstWinsAtEqualDepth pins the second half: among
// declines at the SAME depth the FIRST in evaluation order wins, which
// is the standing "every undetermined names the first construct that
// blocked it" rule. A later sibling never displaces it.
func TestDeepestDeclined_FirstWinsAtEqualDepth(t *testing.T) {
	first := spanAt("s2", "first", "the first gate")
	root := spanAt("s1", "root", "the root gate",
		first,
		spanAt("s3", "second", "the second gate"),
	)
	if held := DeepestDeclined(root); held != first {
		t.Fatalf("DeepestDeclined = %v, want the first span s2 at equal depth", held)
	}
}

// TestDeepestDeclined_AnAnsweredSpanIsNeverTheLeaf keeps an answered
// sub-read out of the projection: only a decline can be the construct
// that blocked.
func TestDeepestDeclined_AnAnsweredSpanIsNeverTheLeaf(t *testing.T) {
	answered := &Span{
		ID: "s2", Name: "answered", Status: Answered,
		Attributes: map[string]string{AttrConstruct: "0", AttrRange: "f.ts:1:3-1:4"},
	}
	root := spanAt("s1", "root", "the root gate", answered)
	if held := DeepestDeclined(root); held != root {
		t.Fatalf("DeepestDeclined = %v, want the root — the answered child cannot be the leaf", held)
	}
}

// TestProject_RendersTheTemplate pins the one sentence template:
// "<construct>: <gate> — <operand construct> held <held>". The operand's
// SPELLING is recovered from the child span covering that exact range,
// since the attribute vocabulary carries the operand only as a range.
func TestProject_RendersTheTemplate(t *testing.T) {
	operand := &Span{
		ID: "s2", Name: "operand", Status: Declined,
		Attributes: map[string]string{AttrConstruct: "i", AttrRange: "f.ts:1:3-1:4"},
	}
	root := &Span{
		ID: "s1", Name: "reader", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "x[i]",
			AttrRange:     "f.ts:1:1-1:5",
			AttrGate:      "the index isn't one exact value",
			AttrOperand:   "f.ts:1:3-1:4",
			AttrHeld:      "no set",
		},
		Children: []*Span{operand},
	}
	want := "x[i]: the index isn't one exact value — i held no set"
	if held := Project(root); held != want {
		t.Fatalf("Project = %q, want %q", held, want)
	}
}

// TestProject_AGatelessDeclineFallsBack pins the spec's fallback: a
// declined span that has not adopted the decline helper carries no gate,
// and its projection reads "<construct>: <reader> declined".
func TestProject_AGatelessDeclineFallsBack(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "evaluateExpression", Status: Declined,
		Attributes: map[string]string{AttrConstruct: "widened", AttrRange: "f.ts:1:1-1:8"},
	}
	want := "widened: evaluateExpression declined"
	if held := Project(root); held != want {
		t.Fatalf("Project = %q, want %q", held, want)
	}
}

// TestWorkItemsOf_AGatelessLeafIsAWorkItem pins the first visible work
// item — an absent gate on a declined span is never an accepted state.
func TestWorkItemsOf_AGatelessLeafIsAWorkItem(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "reader", Status: Declined,
		Attributes: map[string]string{AttrConstruct: "widened", AttrRange: "f.ts:1:1-1:8"},
	}
	items := WorkItemsOf(Trace{Language: "ts", Position: "f.ts:1:1", Root: root})
	if len(items) != 1 {
		t.Fatalf("WorkItemsOf = %v, want exactly the gateless-leaf item", items)
	}
}

// TestWorkItemsOf_AWholePositionLeafRangeIsAWorkItem pins the second:
// a leaf covering the judged position's own range localizes nothing, so
// the trace passes the schema vacantly. It counts as a work item only
// when the position HAS a sub-expression that a reader could have named.
func TestWorkItemsOf_AWholePositionLeafRangeIsAWorkItem(t *testing.T) {
	inner := &Span{
		ID: "s2", Name: "evaluateExpression", Status: Answered,
		Attributes: map[string]string{AttrConstruct: "0", AttrRange: "f.ts:1:3-1:4"},
	}
	root := &Span{
		ID: "s1", Name: "reader", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "x[0]",
			AttrRange:     "f.ts:1:1-1:5",
			AttrGate:      "the index isn't one exact value",
		},
		Children: []*Span{inner},
	}
	items := WorkItemsOf(Trace{Language: "ts", Position: "f.ts:1:1", Root: root})
	if len(items) != 1 {
		t.Fatalf("WorkItemsOf = %v, want exactly the whole-position-range item", items)
	}
}

// TestWorkItemsOf_ABareNamePositionIsNotAWorkItem keeps the range rule
// from firing on a position that has no sub-expression at all. A plain
// name is its own leaf, and a leaf covering it is the only truthful
// answer, not a missing localization.
func TestWorkItemsOf_ABareNamePositionIsNotAWorkItem(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "reader", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "widened",
			AttrRange:     "f.ts:1:1-1:8",
			AttrGate:      "the walk holds nothing that pins this value",
		},
	}
	items := WorkItemsOf(Trace{Language: "ts", Position: "f.ts:1:1", Root: root})
	if len(items) != 0 {
		t.Fatalf("WorkItemsOf = %v, want none — a bare name has no sub-expression to name", items)
	}
}

// TestProject_OpaqueEvaluateExpressionRendersItsGate pins one of the
// gated-leaves adoption's own sites: evaluate_expression.go's dispatch
// seam used to decline a bare Opaque answer with no gate at all — the
// span carried the answer's ResidueReason only, which an Opaque value
// (a global whose declarations are all ambient, e.g. `Object`, `JSON`)
// never has. The seam now states the standing outside-determination gate
// itself, so the leaf is gated and the projection carries the full
// three-piece sentence rather than the "declined" fallback
// (TestProject_AGatelessDeclineFallsBack, above).
func TestProject_OpaqueEvaluateExpressionRendersItsGate(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "evaluateExpression", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "Object",
			AttrRange:     "f.ts:1:1-1:7",
			AttrGate:      "the value is determined outside this file, which proves no set",
			AttrHeld:      "no set",
		},
	}
	want := "Object: the value is determined outside this file, which proves no set — held no set"
	if held := Project(root); held != want {
		t.Fatalf("Project = %q, want %q", held, want)
	}
}

// TestProject_UnaryPlusOnAnObjectOperandRendersItsGate pins the second
// adoption site: ReadUnary's `+x` used to decline `+m`/`+new Money(5)`
// (an object operand ToPrimitive does not model) through UnknownOver
// with no reason at all. The reader now states its own premise — the
// same one its doc comment already gave for why an object operand
// "stays unread" — so the leaf is gated.
func TestProject_UnaryPlusOnAnObjectOperandRendersItsGate(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "evaluateExpression", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "+m",
			AttrRange:     "f.ts:1:1-1:3",
			AttrGate:      "unary + is ToNumber, which needs ToPrimitive on an object operand — a grammar this reader does not carry",
			AttrHeld:      "no set",
		},
	}
	want := "+m: unary + is ToNumber, which needs ToPrimitive on an object operand — a grammar this reader does not carry — held no set"
	if held := Project(root); held != want {
		t.Fatalf("Project = %q, want %q", held, want)
	}
}

// TestProject_IncompleteObjectKeyReadRendersItsGate pins the third
// adoption site: object_key_access.go's ReadObjectKeyAccess used to
// decline a key read off an incomplete object (JSON.parse's open key
// set — `parsed.x`, `decoded.a`) with a bare silence.Residue(). The
// reader now states the premise its own comments already described:
// the key set is not proven complete, so a name outside it is neither
// proven present nor proven absent.
func TestProject_IncompleteObjectKeyReadRendersItsGate(t *testing.T) {
	root := &Span{
		ID: "s1", Name: "evaluateExpression", Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: "decoded.a",
			AttrRange:     "f.ts:1:1-1:10",
			AttrGate:      "the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent",
			AttrHeld:      "no set",
		},
	}
	want := "decoded.a: the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent — held no set"
	if held := Project(root); held != want {
		t.Fatalf("Project = %q, want %q", held, want)
	}
}
