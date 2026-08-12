// from control_flow/answers/parameter_answer.ts
//
// Exit 2: a parameter declaration. CallSiteBindings (callback pin
// or non-exported join), then the plain parameter's type. Words →
// claim; word-less → determined; nothing → not-tracked.
//
// CROSS-DIRECTORY: CallSiteBindings is
// control_flow/call_site_bindings.ts's exported function — that
// file is not yet ported (642 lines; deferred past this port unit,
// reported). Called here by its expected exported Go name and
// signature `func(ctx CallSiteCtx, fn *ast.Node) (Env, bool)`
// (mirroring `Map<string, AbstractValue> | null`).
//
// CallSiteCtx itself IS defined here (a leaf data shape — "Everything
// callSiteBindings needs to evaluate a site" — needed by this file
// now, same precedent as GateAssumption in flow_context.go): the
// call_site_bindings.go porter should import/reuse this struct
// rather than redefine it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CallSiteCtx is the TS CallSiteCtx type: everything CallSiteBindings
// needs to evaluate a site. Judgment and hover build this once and
// pass it down.
type CallSiteCtx struct {
	P         *program.CheckerProgram
	Registry  annotations.AnnotationRegistry
	Objects   annotations.ObjectRegistry
	Contracts map[*ast.Symbol]*FunctionContract
	Kernel    *kernelbridge.RefinedTSKernel
}

// CallSiteBindings (called below) is callSiteBindings in the TS
// source (control_flow/call_site_bindings.ts) — NOT ported in this
// unit, so this file will not compile until call_site_bindings.go
// defines `func CallSiteBindings(ctx CallSiteCtx, fn *ast.Node) (Env,
// bool)`. Per PORT.md's cross-directory convention this is recorded,
// not stubbed.

func answerCallbackParameter(ctx CallSiteCtx, token *ast.Node, parameter *ast.Node) (Answer, bool) {
	fn := parameter.Parent
	if !ast.IsArrowFunction(fn) && !ast.IsFunctionExpression(fn) {
		return Answer{}, false
	}
	// a `new Promise(executor)` parameter is the promise's own
	// settlement function — a host function, read at its calls, the
	// same verdict a function-valued binding gets
	if ast.IsNewExpression(fn.Parent) {
		newExpr := fn.Parent.AsNewExpression()
		if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 && newExpr.Arguments.Nodes[0] == fn &&
			ast.IsIdentifier(newExpr.Expression) && newExpr.Expression.Text() == "Promise" &&
			len(fn.Parameters()) > 0 {
			return No(Unknown{Why: "noted", Said: Sentence.SettlementFunction, Unsupported: false}), true
		}
	}
	bindings, ok := CallSiteBindings(ctx, fn)
	if !ok {
		return Answer{}, false
	}
	held, ok := bindings[token.Text()]
	if !ok {
		return Answer{}, false
	}
	plain := held
	if held.Kind == abstractdomain.KindSet {
		plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{ctx.Kernel}, held.Set)
	}
	words, hasWords := abstractdomain.FormatAbstractValue(plain)
	if !hasWords && held.Kind == abstractdomain.KindPossiblyUndefined {
		return No(Unknown{Why: "noted", Said: Sentence.PresentAndAbsent, Unsupported: false}), true
	}
	if !hasWords {
		return Answer{}, false
	}
	return Claim(words, abstractdomain.TrustLevelOf(plain), false), true
}

// AnswerParameter is answerParameter in the TS source.
func AnswerParameter(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*FunctionContract,
	kernel *kernelbridge.RefinedTSKernel,
	token *ast.Node,
	declaration *ast.Node,
) (Answer, bool) {
	if declaration == nil || !ast.IsParameterDeclaration(declaration) || ast.GetSourceFileOfNode(declaration) != p.Entry {
		return Answer{}, false
	}
	ctx := CallSiteCtx{P: p, Registry: registry, Objects: objects, Contracts: contracts, Kernel: kernel}
	if fromCall, ok := answerCallbackParameter(ctx, token, declaration); ok {
		return fromCall, true
	}
	var worn abstractdomain.AbstractValue
	hasWorn := false
	if ast.IsFunctionDeclaration(declaration.Parent) {
		if bindings, ok := CallSiteBindings(ctx, declaration.Parent); ok {
			if v, ok := bindings[token.Text()]; ok {
				worn, hasWorn = v, true
			}
		}
	}
	if !hasWorn {
		worn, hasWorn = InitialStateOfPlainParameter(p, declaration), true
	}
	if worn.Kind != abstractdomain.KindUnknown {
		plain := worn
		if worn.Kind == abstractdomain.KindSet {
			plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{kernel}, worn.Set)
		}
		if words, hasWords := abstractdomain.FormatAbstractValue(plain); hasWords {
			return Claim(words, abstractdomain.TrustLevelOf(plain), false), true
		}
		if worn.Kind == abstractdomain.KindPossiblyUndefined {
			return No(Unknown{Why: "noted", Said: Sentence.PresentAndAbsent, Unsupported: false}), true
		}
		return No(Unknown{Why: "noted", Said: Sentence.WalkStatesNothing, Unsupported: false}), true
	}
	return No(Unknown{Why: "not-tracked"}), true
}
