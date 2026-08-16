// The yield-position contract of a generator body.
//
// A generator's declared return type names Generator<Y, R, N> — three
// positions, not one. Y states what every `yield e` hands the caller,
// R states what the return statement hands the finished generator's
// last next(), and N states what the caller's next(v) hands each
// resumption back. So a `yield 200` against Generator<Age, …> owes
// exactly the judgment `return 200` owes against a stated result of
// Age — the value flows to a declared position, and the position's set
// judges it.
//
// Two pieces live here:
//
//   - generatorStatedPositions reads Y and R off the written return
//     type at contract-compile time (contract_file_facts.go's
//     readSignature calls it for declarations written with the star).
//     Each argument compiles through the same door every other stated
//     position uses (annotations.AnnotationOfType), so a refined alias,
//     a literal union, and a plain type all read here exactly as they
//     read at a result position.
//   - CheckYieldedValue judges one yield during the body's own walk:
//     the operand runs in this environment, and the enclosing
//     generator's stated yield position (ctx.YieldStated, set per body
//     by analyzeFunctionBody) judges what it read. A delegating
//     `yield* xs` hands the caller every element OF xs, so the element
//     is what judges — through the same readers the drain routes use.
//
// What a yield RESUMES WITH (the N position) is still the caller's to
// send and is not read here — the one syntax table
// (syntax_models.go) speaks that decline after the judgment runs.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// generatorStatedPositions reads the yield and return positions a
// generator's written return type states: the Y and R of
// `Generator<Y, R, N>` and its kin. The recognized names are the same
// ones generatorReturnTypeNames vouches for the call-result reading,
// plus the two structural ones (`Iterable`, `AsyncIterable`): the
// caller gates on the declaration's own star, and a starred body is a
// generator whatever iterable supertype its type spells, so argument
// zero is what it yields either way.
//
// Nil positions mean the type states nothing there — an unrecognized
// name, a missing argument list, a plain type argument. An argument
// whose reading is UNSUPPORTED reports its own sentence at the
// argument, the same way an unsupported result type reports.
func generatorStatedPositions(
	p *program.CheckerProgram,
	returnType *ast.Node,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	reporting bool,
	report func(d assignability.RefinementDiagnostic),
) (yieldStated *annotations.DeclaredRefinement, result *annotations.DeclaredRefinement) {
	if !ast.IsTypeReferenceNode(returnType) || !ast.IsIdentifier(returnType.AsTypeReferenceNode().TypeName) {
		return nil, nil
	}
	reference := returnType.AsTypeReferenceNode()
	name := reference.TypeName
	nameText := name.AsIdentifier().Text
	if !generatorReturnTypeNames[nameText] && nameText != "Iterable" && nameText != "AsyncIterable" {
		return nil, nil
	}
	if !p.Checker.SymbolInDefaultLib(p.Checker.GetSymbolAtLocation(name)) {
		return nil, nil
	}
	if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) == 0 {
		return nil, nil
	}
	arguments := reference.TypeArguments.Nodes
	yieldRead := annotations.AnnotationOfType(p, arguments[0], registry, objects)
	if yieldRead.Stated != nil {
		yieldStated = yieldRead.Stated
	} else if yieldRead.Unsupported != "" && reporting {
		report(assignability.At(arguments[0], 7004, yieldRead.Unsupported))
	}
	if len(arguments) >= 2 {
		resultRead := annotations.AnnotationOfType(p, arguments[1], registry, objects)
		if resultRead.Stated != nil {
			result = resultRead.Stated
		} else if resultRead.Unsupported != "" && reporting {
			report(assignability.At(arguments[1], 7004, resultRead.Unsupported))
		}
	}
	return yieldStated, result
}

// CheckYieldedValue walks one yield expression's operand and judges it
// against the enclosing generator's stated yield position. The operand
// runs HERE, in the body's own environment — its reads and writes are
// this walk's — and what it read is what flows to the caller, so that
// is what the stated position judges.
//
// A delegating `yield* xs` hands the caller every element OF xs, not
// xs itself: the element judges — a generator callee's own yields
// where they read (GeneratorElementOf walks that body in a fresh
// environment), the walked value's element otherwise. An element
// nothing determines judges as unknown, which alerts at the stated
// position rather than passing silently.
//
// A bare `yield` hands the caller undefined, a value like any other —
// it judges at the yield itself.
//
// The yield expression's own VALUE (what the caller's next(v) sends
// back — the N position) is not read here; the caller falls through to
// the one syntax table for that decline.
func CheckYieldedValue(ctx *FlowContext, env Env, e *ast.Node) {
	y := e.AsYieldExpression()
	if y.AsteriskToken != nil {
		if y.Expression == nil {
			return
		}
		walked := evaluateExpression(ctx, env, y.Expression)
		if ctx.YieldStated == nil {
			return
		}
		element, ok := GeneratorElementOf(ctx, y.Expression)
		if !ok {
			element = ElementOf(walked)
		}
		CheckAssignability(ctx, element, *ctx.YieldStated, y.Expression, "a yielded value", nil)
		return
	}
	known := abstractdomain.Undef
	at := e
	if y.Expression != nil {
		known = evaluateExpression(ctx, env, y.Expression)
		at = y.Expression
	}
	if ctx.YieldStated == nil {
		return
	}
	CheckAssignability(ctx, known, *ctx.YieldStated, at, "a yielded value", nil)
}
