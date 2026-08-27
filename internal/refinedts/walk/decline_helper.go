// THE DECLINE HELPER (DERIVATION-TRACE.md, "Threading: dispatchers,
// not readers").
//
//	Decline(gate, operandExpr, held)
//
// records the attributes on the open span AND renders the sentence by
// the projection template — one carrier, so the printed undetermined
// sentence and the trace cannot drift. A refusal site that wants
// precision calls this instead of hand-writing prose.
//
// The sentence it returns is the RTS7002-class undetermined sentence
// and nothing else. Error sentences (7001 and the rest) are
// marker-matched corpus-wide and are never rendered here.
//
// Adoption is incremental and self-prioritizing: every row fix that
// touches a decline site converts it. The sites adopted now are the
// highest-frequency generic ones — the judge's own "could not be
// derived" and unreadable-value paths.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
)

// Decline records the named premise that failed on the currently open
// span, names the operand it failed on, states what that operand held,
// and returns the projected sentence.
//
// gate is the premise in the checker's own words — "the index is not a
// single exact value", "the guard proves no set for this place". It is
// never a category name.
//
// operandExpr is the sub-expression the gate failed on; nil where the
// gate is about the judged node itself.
//
// held is what that operand carried at the failure, in the kernel's own
// spelling — the same formatter every set spelling in this tree uses.
// Like DeclineSentence, the sentence it returns does not depend on
// whether a trace is running — see that function's note.
func Decline(gate string, operandExpr *ast.Node, held abstractdomain.AbstractValue) string {
	heldSpelling := ""
	if held.Kind != abstractdomain.KindUnknown || held.ResidueReason == "" {
		heldSpelling = spellValue(held)
	}
	return DeclineSentence(gate, operandExpr, heldSpelling)
}

// PlacesReadIn is every plain name the judged expression reads — the
// places whose guards belong in this position's derivation. A judged
// position is a small expression (a returned binding, a call argument),
// so the walk is short and only ever runs under an open trace.
func PlacesReadIn(node *ast.Node) []string {
	if node == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var places []string
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil {
			return
		}
		if ast.IsIdentifier(n) {
			name := n.Text()
			if _, already := seen[name]; !already {
				seen[name] = struct{}{}
				places = append(places, name)
			}
			return
		}
		n.ForEachChild(func(child *ast.Node) bool {
			walk(child)
			return false
		})
	}
	walk(node)
	return places
}

// declineGateOf is the named premise for the judge's generic
// undetermined site. A reader that already composed its own
// first-blocker sentence (silence.ResidueOf) states the gate itself;
// where none did, the gate is the bare alert's own premise — nothing
// the walk held pins this value — and that unnamed-reader case is the
// visible work item the spec calls for, not an accepted state.
func declineGateOf(known abstractdomain.AbstractValue) string {
	if known.ResidueReason != "" {
		return known.ResidueReason
	}
	return "the walk holds nothing that pins this value"
}

// spellUnknownHeld is what an unknown operand held. An unknown with a
// reader's sentence attached held nothing spellable as a set, so the
// held slot states that rather than printing "unspellable" — the value
// really is the absence of a set, and the gate above already names why.
func spellUnknownHeld(known abstractdomain.AbstractValue) string {
	if known.Kind == abstractdomain.KindUnknown {
		return "no set"
	}
	return spellValue(known)
}

// DeclineSentence is Decline for a site that has no AbstractValue to
// state — the gate failed on the shape of the position rather than on
// a value it read. held is the plain spelling the site already writes.
//
// THE SENTENCE DOES NOT DEPEND ON WHETHER A TRACE IS RUNNING. The
// projection is a function of the gate, the construct, and what was
// held — all three are in hand here whether or not a Recorder is
// installed. An earlier shape returned "" with no trace and let the
// caller print the bare gate string instead, so the ordinary sweep
// printed `the index isn't a single exact value…` while the traced run
// printed `x[0]: the index isn't a single exact value… — held no set`.
// The one-carrier rule is that those are the same sentence, so the
// no-trace path composes the same projection over a span it builds
// locally rather than falling back to prose the projection never saw.
func DeclineSentence(gate string, operandExpr *ast.Node, held string) string {
	if span := derivation.OpenSpan(); span != nil {
		derivation.DeclineOpen(gate, operandRangeFor(span, operandExpr, gate, held), held)
		return derivation.ProjectDeepest(span)
	}
	return declineSentenceUntraced(gate, operandExpr, held)
}

// declineSentenceUntraced is the projection with no Recorder installed:
// the same template over a span assembled from what the refusal site
// already holds.
//
// The node it names is the OPERAND when the site handed one that is a
// proper sub-expression, and the judged node otherwise — the same choice
// operandRangeFor makes, minus the span bookkeeping there is no trace to
// keep. Nothing here reads a source text unless a sentence is actually
// being composed, so the ordinary path pays one Construct call per
// reported undetermined and never one per walked node.
func declineSentenceUntraced(gate string, operandExpr *ast.Node, held string) string {
	if operandExpr == nil {
		return ""
	}
	span := &derivation.Span{
		Name:   "checkAssignability",
		Status: derivation.Declined,
		Attributes: map[string]string{
			derivation.AttrConstruct: derivation.Construct(operandExpr),
			derivation.AttrRange:     derivation.Range(operandExpr),
		},
	}
	if gate != "" {
		span.Attributes[derivation.AttrGate] = gate
	}
	if held != "" {
		span.Attributes[derivation.AttrHeld] = held
	}
	return derivation.Project(span)
}

// operandRangeFor is the failing operand's range, and nothing where the
// operand IS the judged node itself.
//
// A SELF-OPERAND NAMES NOTHING. The spec's refinery.operand exists to
// localize the failure to a sub-expression INSIDE the span's own
// construct — `x[0]`'s index, a call's argument. A site that hands the
// judged node back as its own operand states the range the span already
// carries, which adds no localization and, worse, drives the helper to
// open a duplicate leaf that then wins DeepestDeclined with fewer
// attributes than its parent — the projection would drop the
// `— <operand> held <held>` tail while the emitted sentence kept it, and
// sentence == projection is exactly what the one-carrier rule promises.
// So a self-operand is refused here, and the span's projection falls to
// `<construct>: <gate>`, which is the truthful shape when the gate is
// about the judged node itself.
//
// A genuinely inner operand still gets a leaf span when no dispatcher
// opened one for it, because projection.go's operandConstructOf recovers
// the operand's SPELLING from a child covering that exact range.
func operandRangeFor(span *derivation.Span, operandExpr *ast.Node, gate string, held string) string {
	if operandExpr == nil {
		return ""
	}
	operandRange := derivation.Range(operandExpr)
	if operandRange == "" || operandRange == span.Attributes[derivation.AttrRange] {
		return ""
	}
	if operandLeaf := derivation.BeginNode("operand", operandExpr); operandLeaf != nil {
		operandLeaf.Decline(gate, "", held)
		operandLeaf.End()
	}
	return operandRange
}
