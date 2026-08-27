// The projection rule (DERIVATION-TRACE.md, "The projection rule").
//
// The printed undetermined sentence IS the projection of the trace's
// deepest declined span, by this template and no other:
//
//	<construct>: <gate> — <operand construct> held <held>
//
// A declined span that has not yet adopted the decline helper carries
// no refinery.gate; its projection falls back to
// `<construct>: <reader> declined`. That absence is a visible work
// item, not an accepted state.
//
// Nothing else in the tree hand-writes an undetermined sentence: the
// sentence and the trace are one carrier, so they cannot drift.

package derivation

import (
	"strconv"
	"strings"
)

// rangeContains answers whether inner's path:line:col-line:col sits
// inside outer's — same file, start no earlier, end no later.
func rangeContains(outer string, inner string) bool {
	outerPath, outerStart, outerEnd, outerOk := parseRange(outer)
	innerPath, innerStart, innerEnd, innerOk := parseRange(inner)
	if !outerOk || !innerOk || outerPath != innerPath {
		return false
	}
	return !before(innerStart, outerStart) && !before(outerEnd, innerEnd)
}

// point is a line and a column, both 1-based.
type point struct {
	line int
	col  int
}

func before(a point, b point) bool {
	if a.line != b.line {
		return a.line < b.line
	}
	return a.col < b.col
}

// parseRange splits path:line:col-line:col. The path may hold no dash
// on any platform this runs on, and the last four colon/dash-separated
// numbers are the two points, so the split reads from the right.
func parseRange(rng string) (path string, start point, end point, ok bool) {
	dash := strings.LastIndex(rng, "-")
	if dash < 0 {
		return "", point{}, point{}, false
	}
	head, tail := rng[:dash], rng[dash+1:]
	endLine, endCol, endOk := twoNumbers(tail)
	if !endOk {
		return "", point{}, point{}, false
	}
	colon := strings.LastIndex(head, ":")
	if colon < 0 {
		return "", point{}, point{}, false
	}
	startCol, err := strconv.Atoi(head[colon+1:])
	if err != nil {
		return "", point{}, point{}, false
	}
	head = head[:colon]
	colon = strings.LastIndex(head, ":")
	if colon < 0 {
		return "", point{}, point{}, false
	}
	startLine, err := strconv.Atoi(head[colon+1:])
	if err != nil {
		return "", point{}, point{}, false
	}
	return head[:colon], point{line: startLine, col: startCol},
		point{line: endLine, col: endCol}, true
}

// twoNumbers reads "line:col".
func twoNumbers(text string) (line int, col int, ok bool) {
	colon := strings.Index(text, ":")
	if colon < 0 {
		return 0, 0, false
	}
	line, err := strconv.Atoi(text[:colon])
	if err != nil {
		return 0, 0, false
	}
	col, err = strconv.Atoi(text[colon+1:])
	if err != nil {
		return 0, 0, false
	}
	return line, col, true
}

// DeepestDeclined is the declined span the undetermined sentence names.
//
// THE TIE-BREAK IS NORMATIVE (DERIVATION-TRACE.md): DEPTH FIRST — a
// deeper subtree's decline outranks a shallower sibling's, because the
// deeper one is the sub-read the shallower one was waiting on — then
// EVALUATION ORDER at equal depth, where the FIRST wins. That second
// half is the standing rule itself: every undetermined names the FIRST
// construct that blocked it, so among equals the earliest is the one the
// derivation actually stopped on and the later ones are its consequences.
//
// Conformance check 2 compares exactly this leaf across the three
// adapters, so the rule cannot be a local preference: an implementation
// that kept the last-at-equal-depth would name a different construct
// than the others on any position with two sibling declines, and the
// cross-language diff would read as a disagreement about the program
// rather than about the tie-break.
//
// Nil when nothing declined.
func DeepestDeclined(root *Span) *Span {
	if root == nil {
		return nil
	}
	var found *Span
	depthOf := -1
	var walk func(span *Span, depth int)
	walk = func(span *Span, depth int) {
		if span == nil {
			return
		}
		// strictly greater: a later sibling at the SAME depth never
		// displaces the first one to decline there
		if span.Status == Declined && depth > depthOf {
			found, depthOf = span, depth
		}
		for _, child := range span.Children {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
	return found
}

// ProjectDeepest is the sentence for a span that has just declined: the
// projection of the DEEPEST declined span at or below it, not of the
// span itself.
//
// THE SENTENCE AND THE TRACE ARE ONE CARRIER, and that only holds if
// both read the same span. The printed sentence is composed where the
// refusal happens — the judge, usually — while the trace's own
// projection is read later from its deepest declined span. Projecting
// the refusal site's own span would make the two disagree on any
// position whose sub-read also declined, which is most of them: the
// judge would print its gate while the trace named a deeper leaf. So
// the refusal site projects what the trace will project.
//
// A leaf that carries no gate still wins the depth rule, and its
// fallback sentence ("<construct>: <reader> declined") is then what BOTH
// carriers say. That is the spec's intent — the work item is visible in
// the printed sentence too, not hidden behind a gated ancestor.
func ProjectDeepest(span *Span) string {
	if span == nil {
		return ""
	}
	if deepest := DeepestDeclined(span); deepest != nil {
		return Project(deepest)
	}
	return Project(span)
}

// Project renders one declined span by the template. An empty answer
// means the span was not declined and has no projection.
func Project(span *Span) string {
	if span == nil || span.Status != Declined {
		return ""
	}
	construct := span.Attributes[AttrConstruct]
	gate := span.Attributes[AttrGate]
	if gate == "" {
		return construct + ": " + span.Name + " declined"
	}
	held := span.Attributes[AttrHeld]
	if held == "" {
		return construct + ": " + gate
	}
	// THE TAIL HANGS OFF `held`, NOT off the operand. The template's tail
	// states what the failing thing carried; the operand only NAMES which
	// sub-expression it was. A site that declined on the judged node
	// itself records no operand — operandRangeFor refuses a self-operand,
	// because a range equal to the span's own localizes nothing — and it
	// still knows what that node held. Requiring both dropped the tail on
	// exactly those sites, so the printed sentence lost the `held` clause
	// the trace was carrying and sentence == projection failed on every
	// self-operand decline (A7.guard.sort.ts:13 measured this).
	//
	// With an operand present the tail names it; without one the tail is
	// bare, which is the truthful shape when the gate is about the judged
	// node itself.
	operand := operandConstructOf(span)
	if operand == "" {
		return construct + ": " + gate + " — held " + held
	}
	return construct + ": " + gate + " — " + operand + " held " + held
}

// operandConstructOf is the failing operand's own source spelling.
//
// SPEC GAP, resolved inside the schema: the projection template names
// `<operand construct>`, but the attribute vocabulary carries the
// operand only as `refinery.operand`, a RANGE, and the schema's
// attributes object is additionalProperties:false — so no
// `refinery.operand_construct` key can be added without changing the
// schema. The operand's spelling is recovered instead: a child span
// covering that exact range already carries the spelling in its own
// refinery.construct, and that is the same text by construction. Where
// no child covers it (an operand the dispatcher never opened a span
// for), the range itself stands in, which still names the position.
func operandConstructOf(span *Span) string {
	operandRange := span.Attributes[AttrOperand]
	if operandRange == "" {
		return ""
	}
	var found string
	var walk func(s *Span)
	walk = func(s *Span) {
		if s == nil || found != "" {
			return
		}
		if s != span && s.Attributes[AttrRange] == operandRange {
			found = s.Attributes[AttrConstruct]
			return
		}
		for _, child := range s.Children {
			walk(child)
		}
	}
	walk(span)
	if found != "" {
		return found
	}
	return operandRange
}

// ProjectTrace is Project of the trace's deepest declined span.
func ProjectTrace(trace Trace) string {
	return Project(DeepestDeclined(trace.Root))
}

// WorkItemsOf are the trace's own visible work items — the places the
// spec names as not-yet-done, reported by the trace rather than left for
// a reader to notice.
//
// Two kinds, both from DERIVATION-TRACE.md:
//
//   - A DECLINED SPAN WITH NO GATE. Its projection falls back to
//     "<construct>: <reader> declined", which localizes but names no
//     failing premise — the spec calls the absence a visible work item,
//     not an accepted state, and it closes when that refusal site adopts
//     the decline helper.
//
//   - A LEAF RANGE THAT IS NOT A PROPER SUB-RANGE of the judged
//     position. When the position is not itself a leaf construct, a leaf
//     covering the whole position localizes nothing: the trace passes
//     the schema and answers question 1 vacantly, pointing at the same
//     text the reader already had. It closes when some reader below the
//     position opens a span on the sub-expression that actually blocked.
//
// Reported, never fatal: a work item is a queue entry, and a trace that
// carries one is still a trace.
func WorkItemsOf(trace Trace) []string {
	leaf := DeepestDeclined(trace.Root)
	if leaf == nil {
		return nil
	}
	var items []string
	if leaf.Attributes[AttrGate] == "" {
		items = append(items, "the deepest declined span ("+leaf.Name+
			") names no gate — its refusal site has not adopted the decline helper")
	}
	positionRange := ""
	if trace.Root != nil {
		positionRange = trace.Root.Attributes[AttrRange]
	}
	leafRange := leaf.Attributes[AttrRange]
	// a leaf that IS the root is the whole position by construction, and
	// so is any leaf whose range equals it
	if leafRange != "" && leafRange == positionRange && hasInnerConstruct(trace.Root) {
		items = append(items, "the leaf range is the judged position's own range ("+leafRange+
			") — no reader localized the blocking sub-expression")
	}
	return items
}

// hasInnerConstruct answers whether the judged position has any
// sub-expression at all — a span somewhere below it on a strictly
// smaller range. A position that is a bare leaf construct (a plain name,
// a literal) has none, and then a leaf covering the whole position is
// the correct and only answer, not a work item.
func hasInnerConstruct(root *Span) bool {
	if root == nil {
		return false
	}
	outer := root.Attributes[AttrRange]
	found := false
	var walk func(span *Span)
	walk = func(span *Span) {
		if span == nil || found {
			return
		}
		if span != root {
			if inner := span.Attributes[AttrRange]; inner != "" &&
				inner != outer && rangeContains(outer, inner) {
				found = true
				return
			}
		}
		for _, child := range span.Children {
			walk(child)
		}
	}
	walk(root)
	return found
}

// Render draws the trace as an indented tree, the default -explain
// output. The JSON form is the same data; this is the reading form.
func Render(trace Trace) string {
	var out strings.Builder
	out.WriteString(trace.Language + "  " + trace.Position + "\n")
	renderSpan(&out, trace.Root, 0)
	// The binding-ledger roots print as additional roots under the same
	// document, each behind its own header — never merged into the tree
	// above, because the projection reads the MAIN root and the chain
	// sharpens the work item, not the sentence.
	for _, bound := range trace.Chain {
		out.WriteString("\nbound from: " + bound.Attributes[AttrConstruct] +
			"  @" + bound.Attributes[AttrRange] + "\n")
		renderSpan(&out, bound, 0)
	}
	if sentence := ProjectTrace(trace); sentence != "" {
		out.WriteString("\nprojection: " + sentence + "\n")
	}
	for _, item := range WorkItemsOf(trace) {
		out.WriteString("work item: " + item + "\n")
	}
	return out.String()
}

func renderSpan(out *strings.Builder, span *Span, depth int) {
	if span == nil {
		return
	}
	indent := strings.Repeat("  ", depth)
	out.WriteString(indent + span.ID + " " + span.Name + " [" + string(span.Status) + "]")
	if construct := span.Attributes[AttrConstruct]; construct != "" {
		out.WriteString("  " + construct)
	}
	if rng := span.Attributes[AttrRange]; rng != "" {
		out.WriteString("  @" + rng)
	}
	out.WriteString("\n")
	for _, key := range []string{AttrAnswer, AttrGate, AttrOperand, AttrHeld, AttrQuestion, AttrLastTouch} {
		if value, held := span.Attributes[key]; held {
			out.WriteString(indent + "    " + key + " = " + value + "\n")
		}
	}
	for _, child := range span.Children {
		renderSpan(out, child, depth+1)
	}
}
