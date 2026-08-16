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
// Three pieces live here:
//
//   - generatorStatedPositions reads Y, R, and N off the written
//     return type at contract-compile time (contract_file_facts.go's
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
//   - the yield EXPRESSION'S OWN VALUE — what the caller's next(v)
//     sends back, the N position — reads as a stated position too,
//     the same way a parameter's stated type seeds its entry value
//     (BindEntryEnv's AbstractValueOfDeclared idiom): a `yield e` used
//     as a value (`const v = yield e`) evaluates to
//     AbstractValueOfDeclared(*ctx.YieldResumeStated) wherever N is
//     grounded and stated, seeded per body in analyzeFunctionBody
//     exactly as YieldStated is. The one syntax table (syntax_models.go)
//     still speaks the decline wherever N states nothing — an
//     ungrounded generator, an unstated third argument, or a
//     non-generator context ctx.YieldResumeStated never reaches.

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
//
// A generator's return type may be SPELLED THROUGH AN ALIAS —
// `type G = Generator<Age, Age, unknown>; function* f(): G` — so the
// written node is followed through the alias chain (aliasedTypeReference)
// BEFORE the name test below runs: the test needs to see `Generator`
// itself, which a bare `G` never spells directly.
func generatorStatedPositions(
	p *program.CheckerProgram,
	returnType *ast.Node,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	reporting bool,
	report func(d assignability.RefinementDiagnostic),
) (yieldStated *annotations.DeclaredRefinement, result *annotations.DeclaredRefinement, resumeStated *annotations.DeclaredRefinement) {
	returnType = aliasedTypeReference(p, returnType)
	if !ast.IsTypeReferenceNode(returnType) || !ast.IsIdentifier(returnType.AsTypeReferenceNode().TypeName) {
		return nil, nil, nil
	}
	reference := returnType.AsTypeReferenceNode()
	name := reference.TypeName
	nameText := name.AsIdentifier().Text
	if !generatorReturnTypeNames[nameText] && nameText != "Iterable" && nameText != "AsyncIterable" {
		return nil, nil, nil
	}
	if !p.Checker.SymbolInDefaultLib(p.Checker.GetSymbolAtLocation(name)) {
		return nil, nil, nil
	}
	if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) == 0 {
		return nil, nil, nil
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
	if len(arguments) >= 3 {
		resumeRead := annotations.AnnotationOfType(p, arguments[2], registry, objects)
		if resumeRead.Stated != nil {
			resumeStated = resumeRead.Stated
		} else if resumeRead.Unsupported != "" && reporting {
			report(assignability.At(arguments[2], 7004, resumeRead.Unsupported))
		}
	}
	return yieldStated, result, resumeStated
}

// aliasedTypeReference follows a written return-type node through a
// plain (non-generic) type-alias chain to the type reference it
// finally names — `type G = Generator<Age, Age, unknown>` unfolds `G`
// to the `Generator<...>` node the name test above reads.
//
// Only the SAME alias-following idiom annotations/type_node_aliases.go
// already vouches for a value position (symbolAt, then the first
// TypeAliasDeclaration among the symbol's declarations) — no separate
// rule. A GENERIC alias (`type G<T> = Generator<T, T, unknown>`)
// is left alone: the type arguments would need their own binding pass
// to substitute, which the generator position does not attempt here,
// so the node returns exactly as written and the caller's name test
// declines it the same way it declines any other unrecognized shape.
// A node that is not a bare type reference, or whose symbol names no
// alias, returns unchanged — the ordinary `Generator<...>` spelling
// takes zero trips through this loop.
func aliasedTypeReference(p *program.CheckerProgram, typeNode *ast.Node) *ast.Node {
	cursor := typeNode
	for range maxAliasChainDepth {
		if !ast.IsTypeReferenceNode(cursor) || !ast.IsIdentifier(cursor.AsTypeReferenceNode().TypeName) {
			return cursor
		}
		reference := cursor.AsTypeReferenceNode()
		if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
			// the alias itself takes arguments at THIS reference — either
			// it is generic (left alone, see the doc comment) or it is
			// already `Generator<...>` itself, which the caller reads
			// directly; either way the chase stops here
			return cursor
		}
		symbol := symbolAt(p.Checker, reference.TypeName)
		if symbol == nil {
			return cursor
		}
		var aliasDecl *ast.Node
		for _, d := range symbol.Declarations {
			if ast.IsTypeAliasDeclaration(d) {
				aliasDecl = d
				break
			}
		}
		if aliasDecl == nil {
			return cursor
		}
		typeAlias := aliasDecl.AsTypeAliasDeclaration()
		if typeAlias.TypeParameters != nil {
			// a generic alias's body may mention its own type parameters —
			// substituting them is outside what a return-type position
			// reads here (see the doc comment)
			return cursor
		}
		cursor = typeAlias.Type
	}
	return cursor
}

// maxAliasChainDepth bounds aliasedTypeReference's walk: `type A = B;
// type B = C; …` is finite in any real program, and the ordinary
// direct-spelling case (the overwhelming majority) exits the loop
// after zero iterations. The bound exists only so a pathological or
// mutually-recursive alias chain cannot spin the checker.
const maxAliasChainDepth = 8

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
// back — the N position) is not judged here — there is nothing to
// check it AGAINST here, since this function reads the OPERAND, not
// the yield expression itself. Reading N is evaluateExpression's own
// arm (evaluate_expression.go's KindYieldExpression case): stated,
// AbstractValueOfDeclared(*ctx.YieldResumeStated); nil, the one syntax
// table's decline.
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
