// No TS twin: the tagged template was a decline row in the syntax
// table on both trees, and this reading is new here.
//
// A tagged template `` tag`a${x}b` `` calls `tag`. This reads it as
// the call it is. The tag call's argument list is the template object
// first and the substitution values after it, in source order — the
// list-concatenation of « siteObj » and the substitutions
// (tmp/ecma262/spec.html
// sec-runtime-semantics-argumentlistevaluation, the two
// |TemplateLiteral| alternatives; the note at sec-tagged-templates
// states the same shape). The effective-argument list built below
// (EffectiveArguments, spread_expansion.go) carries those positions to
// every reader in the inline path, so a tag whose body is in reach is
// walked with its parameters bound the way a plain call's are.
//
// A tag the inline path declines still reads: the substitutions run
// for their effects, whatever the tag can write forgets, and the
// result wears the tag's own return contract — the same last-reader
// ground an unmodeled call gets.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// EvaluateTaggedTemplate reads a tagged template as a call of its tag.
func EvaluateTaggedTemplate(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	tagged := e.AsTaggedTemplateExpression()
	substitutions := TemplateSubstitutions(tagged.Template)
	// a tag whose declaration is in reach runs HERE: its body is walked
	// with the template object bound to the first parameter and the
	// substitutions to the rest, its effects on the caller's names
	// apply, and the value it returns is the value this expression has
	contract := ContractOf(ctx, tagged.Tag)
	// the TAG resolves before the arguments do (sec-tagged-templates:
	// tagRef and tagFunc are read, and only then does EvaluateCall run
	// the argument list), so a contracted METHOD tag's receiver — a
	// call result, a constructor — walks for its effects first
	if contract != nil && ast.IsPropertyAccessExpression(tagged.Tag) {
		evaluateExpression(ctx, env, tagged.Tag.AsPropertyAccessExpression().Expression)
	}
	// then the substitutions run, left to right — a `${i++}` or a
	// `${f()}` inside the template writes exactly here. Their values are
	// the tag's arguments from position 1 on; position 0 is the template
	// object, whose node slot is nil because the programmer wrote no
	// expression for it — the object is built by GetTemplateObject from
	// the literal itself (tmp/ecma262/spec.html sec-gettemplateobject).
	//
	// It is built as the ONE effective-argument list every reader below
	// takes, so a tag's parameters place against the same positions a
	// plain call's do. A substitution is never a spread — a template
	// literal has no spread form — so the list is exact by construction.
	effective := EffectiveArguments{Exact: true}
	effective.Nodes = append(effective.Nodes, nil)
	effective.Knowns = append(effective.Knowns, TemplateObjectValue(tagged.Template))
	for _, substitution := range substitutions {
		effective.Nodes = append(effective.Nodes, substitution)
		effective.Knowns = append(effective.Knowns, evaluateExpression(ctx, env, substitution))
	}
	if contract != nil {
		CheckContractArguments(ctx, contract, effective)
	}
	if contract != nil && contract.Declaration.Body() != nil {
		// the stated result is the contract; the recovered value rode
		// the caller's knowledge through the body. Both hold of the same
		// value, so the call wears their MEET — exactly the plain call's
		// rule (evaluate_call_expression.go).
		var statedResult *abstractdomain.AbstractValue
		if contract.Result != nil && contract.Result.Kind != annotations.DeclaredVariable {
			v := AbstractValueOfDeclared(*contract.Result)
			statedResult = &v
		}
		// the effect-free and constant-write shortcuts read positions
		// through the shared reader now, so a tagged call takes them on
		// the plain call's terms (evaluate_call_expression.go)
		summary := Summarize(ctx, *contract)
		if summary.EffectFree && summary.SelfContained {
			recovered := RecoverPure(ctx, e, *contract, effective, true)
			if statedResult == nil {
				return recovered
			}
			return abstractdomain.MeetKnown(recovered, *statedResult)
		}
		if !summary.EffectFree && summary.HasConstantWrites {
			ApplyConstantWrites(ctx, env, effective, summary.ConstantWrites)
			if statedResult != nil {
				return *statedResult
			}
			return silence.Residue()
		}
		recovered := InlineContractCall(ctx, env, e, contract, effective)
		if statedResult == nil {
			return recovered
		}
		return abstractdomain.MeetKnown(recovered, *statedResult)
	}
	// no body in reach: the tag runs, and no path below reads it —
	// whatever it can reach through a substitution forgets, the way a
	// body-less callee's reference arguments forget at a plain call
	for _, substitution := range substitutions {
		if ast.IsIdentifier(substitution) {
			if _, ok := env.Get(substitution.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, substitution) {
				HavocEnv(ctx.Aliases, env, substitution.Text())
			}
		}
	}
	// the tag's own RESOLVED return type is the reader the tagged
	// template shares with every unmodeled call: a stated annotation
	// first, then a default-library callee's clean sort ground
	if worn := AnnotationOfReturnType(ctx, e); worn != nil {
		return *worn
	}
	if CalleeInDefaultLib(ctx, tagged.Tag) {
		if ground := ReturnTypeGround(ctx, e); ground != nil {
			return *ground
		}
	}
	// a tag with NO BODY anywhere in reach — a .d.ts signature, an
	// import from a module the program cannot see — builds its value
	// outside this file's determination, so the read stays opaque
	if BodilessCallee(ctx, tagged.Tag) && !CalleeInDefaultLib(ctx, tagged.Tag) {
		return abstractdomain.Opaque
	}
	// a tag whose body sits in this program but wears no contract the
	// registry holds: the walk has no declaration to enter
	if assignability.CollectingReasons() {
		assignability.NoteReason(assignability.ReasonNote{
			Site:        "expression",
			Node:        e,
			Said:        "the tag " + CalleeWords(tagged.Tag) + " has no contract the walk holds, so its body is not read here",
			Unsupported: true,
		})
	}
	return silence.Residue()
}

// TemplateObjectValue is the object a tag receives as its FIRST
// argument: the template object GetTemplateObject builds from the
// literal (tmp/ecma262/spec.html sec-gettemplateobject).
//
// Its indexed elements are the COOKED strings, in order, and each is
// syntactic — the parser already cooked them, so every element is an
// exact string here. The value is a LIST, which reads `strings[i]`
// exactly and `strings.length` exactly.
//
// What the list does NOT spell is the `raw` property. sec-gettemplateobject
// defines the template object as an Array carrying a non-enumerable
// `"raw"` key holding a second frozen array of the RAW strings (the
// same text with escape sequences unprocessed). A list value in this
// domain holds items and nothing else — there is no shape here that
// carries indexed items AND a named key — so `raw` goes unspelled, and
// a `strings.raw` read falls through every reader to no determination
// rather than to a wrong one. An object with numeric-name keys would
// spell `raw`, but would lose the exact `[i]` and `.length` reads that
// tag bodies actually take, so the list is the reading that determines
// more without claiming anything false.
func TemplateObjectValue(template *ast.Node) abstractdomain.AbstractValue {
	cookedStrings := TemplateCookedStrings(template)
	items := make([]abstractdomain.AbstractValue, 0, len(cookedStrings))
	for _, cooked := range cookedStrings {
		items = append(items, abstractdomain.KnownValues(
			refinementsets.CodepointsOf(cooked), abstractdomain.PrimitiveString, abstractdomain.TrustProved,
		))
	}
	// the cooked strings are read straight off the parsed literal, so
	// the list is exact at the spec's own grade
	return abstractdomain.KnownList(items, abstractdomain.TrustSpec)
}

// TemplateCookedStrings is the template literal's cooked string parts,
// in source order: the head, then each span's trailing literal. A
// literal with n substitutions has n+1 of them
// (sec-gettemplateobject reads exactly this list as TemplateStrings
// with argument *false*). A no-substitution template is its single
// whole text.
func TemplateCookedStrings(template *ast.Node) []string {
	if template == nil {
		return nil
	}
	if ast.IsNoSubstitutionTemplateLiteral(template) {
		return []string{template.Text()}
	}
	if !ast.IsTemplateExpression(template) {
		return nil
	}
	expression := template.AsTemplateExpression()
	out := []string{expression.Head.Text()}
	if expression.TemplateSpans == nil {
		return out
	}
	for _, span := range expression.TemplateSpans.Nodes {
		out = append(out, span.AsTemplateSpan().Literal.Text())
	}
	return out
}

// TemplateSubstitutions is the `${…}` expressions of a template
// literal, in source order. A no-substitution template has none.
func TemplateSubstitutions(template *ast.Node) []*ast.Node {
	if template == nil || !ast.IsTemplateExpression(template) {
		return nil
	}
	spans := template.AsTemplateExpression().TemplateSpans
	if spans == nil {
		return nil
	}
	out := make([]*ast.Node, 0, len(spans.Nodes))
	for _, span := range spans.Nodes {
		out = append(out, span.AsTemplateSpan().Expression)
	}
	return out
}
