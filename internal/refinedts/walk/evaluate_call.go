// from interprocedural/evaluate_call.ts
//
// Reading a call by walking the callee. A stated contract is trusted
// at its call sites, but a function whose body is in reach can do
// better than its statement: the body runs here, silently, on a copy
// of the caller's knowledge, and the value it returns is the value
// the call has.
//
// What makes that sound is bookkeeping split across the sibling
// modules: contract lookup, callee effect facts, the body walk, and
// memoized replay. This file keeps the public entries — resolve a
// contract, check a callback argument, name projection sources, and
// open an inline.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ContractOf is the contract behind a call, resolved by symbol —
// through an import alias, so a function imported from another file
// wears its stated contract here too; a METHOD callee resolves
// through its property name (`api.fee(…)`, `new A().fee(…)`). A
// function declaration whose NAME the file reassigns may hold a
// different function at runtime, so no contract stands for the call.
func ContractOf(ctx *FlowContext, callee *ast.Node) *FunctionContract {
	found := ContractBySymbol(ctx, callee)
	if found == nil {
		return nil
	}
	declaration := found.Declaration
	if ast.IsFunctionDeclaration(declaration) {
		fd := declaration.AsFunctionDeclaration()
		if fd.Name() != nil {
			if _, reassigned := narrowing.ReassignedNames(ast.GetSourceFileOfNode(declaration))[fd.Name().Text()]; reassigned {
				return nil
			}
		}
	}
	// a METHOD overridden anywhere in view dispatches virtually: the
	// resolved base body must not stand for every instance, so no
	// contract stands for the call
	if ast.IsMethodDeclaration(declaration) {
		md := declaration.AsMethodDeclaration()
		if ast.IsIdentifier(md.Name()) {
			if _, overridden := OverriddenMethodNames(&ctx.Contracts)[md.Name().Text()]; overridden {
				return nil
			}
		}
	}
	return found
}

// CheckCallbackArgument checks a FUNCTION-typed parameter whose
// return type states an annotation: the callback handed there owes
// every return to that statement. The contract collector never sees
// a contextually typed literal, so the obligation is checked at the
// hand-over. The body runs at times this scope cannot place, so it
// walks on a fresh environment — captured names carry nothing.
func CheckCallbackArgument(ctx *FlowContext, argument *ast.Node, parameterType *ast.Node) {
	if parameterType == nil || !ast.IsFunctionTypeNode(parameterType) {
		return
	}
	if !ast.IsArrowFunction(argument) && !ast.IsFunctionExpression(argument) {
		return
	}
	read := annotations.AnnotationOfType(ctx.P, parameterType.Type(), ctx.Registry, ctx.Objects)
	if read.Stated == nil {
		return
	}
	if read.Stated.Kind == annotations.DeclaredVariable && !read.Stated.BoundGrounded {
		return
	}
	// the body runs at times this scope cannot place, so nothing
	// walk-ordered survives into it: no difference rows, no gate
	// assumptions
	inner := *ctx
	inner.Declared = map[string]*annotations.DeclaredRefinement{}
	inner.ReturnSink = nil
	inner.DifferenceConstraints = nil
	inner.GateAssumptions = nil
	callbackEnv := Env{}
	body := argument.Body()
	if ast.IsBlock(body) {
		AnalyzeStatements(&inner, callbackEnv, body.AsBlock().Statements.Nodes, read.Stated)
	} else {
		CheckAssignability(&inner, evaluateExpression(&inner, callbackEnv, body), *read.Stated, body, "a returned value", nil)
	}
}

// ProjectionSources is the tracked roots a binding's initializer
// PROJECTS from: the receiver chain of a property or element read,
// and each candidate of a conditional. Binding such a value shares
// the underlying reference with the holder, so the two names link —
// a write through the child reaches the holder.
func ProjectionSources(e *ast.Node, into []string) []string {
	if ast.IsParenthesizedExpression(e) {
		return ProjectionSources(e.AsParenthesizedExpression().Expression, into)
	}
	if ast.IsAsExpression(e) {
		return ProjectionSources(e.AsAsExpression().Expression, into)
	}
	if ast.IsPropertyAccessExpression(e) || ast.IsElementAccessExpression(e) {
		var root *ast.Node
		if ast.IsPropertyAccessExpression(e) {
			root = e.AsPropertyAccessExpression().Expression
		} else {
			root = e.AsElementAccessExpression().Expression
		}
		for {
			if ast.IsPropertyAccessExpression(root) {
				root = root.AsPropertyAccessExpression().Expression
			} else if ast.IsElementAccessExpression(root) {
				root = root.AsElementAccessExpression().Expression
			} else if ast.IsParenthesizedExpression(root) {
				root = root.AsParenthesizedExpression().Expression
			} else if ast.IsAsExpression(root) {
				root = root.AsAsExpression().Expression
			} else {
				break
			}
		}
		if ast.IsIdentifier(root) {
			into = append(into, root.Text())
		}
		return into
	}
	if ast.IsConditionalExpression(e) {
		ce := e.AsConditionalExpression()
		for _, branch := range []*ast.Node{ce.WhenTrue, ce.WhenFalse} {
			if ast.IsIdentifier(branch) {
				into = append(into, branch.Text())
			} else {
				into = ProjectionSources(branch, into)
			}
		}
		return into
	}
	return into
}

// InlineContractCall is the per-contract EFFECT SUMMARY, computed
// once and held on the declaration node. In the checker's own model,
// a call's only cross-boundary effects come from write constructs
// (assignments, ++/--/delete) and from CALLS — so a body containing
// neither, and calling nothing but other effect-free contracts and
// the read-only default-library names, provably changes NOTHING the
// caller tracks, and its inline is idle work. `selfContained`
// additionally means the body reads nothing beyond its own
// parameters and locals, so its recovered result is a function of
// the arguments alone — memoizable. Anything unresolvable is
// conservatively impure; a cycle of write-free bodies is pure (the
// walk answers recursion with unknown anyway).
func InlineContractCall(ctx *FlowContext, env Env, call *ast.Node, contract *FunctionContract, argKnowns []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if !tracing.Recording(tracing.GrainStep) {
		return InlineContractBody(ctx, env, call, contract, argKnowns)
	}
	return tracing.Span("inlineContractCall", func() abstractdomain.AbstractValue {
		return InlineContractBody(ctx, env, call, contract, argKnowns)
	}, tracing.GrainStep)
}
