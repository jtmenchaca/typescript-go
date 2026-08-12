// from evaluation/schema_runtime_models.ts
//
// The schema-runtime models: .parse/.safeParse/.parseAsync on a
// stated schema — the proof-producing boundary — and .decode/.encode
// on a z.codec schema. Split from builtin_models.ts per the v2 tree.
//
// PARTIAL: the `.parse`/`.safeParse`/`.parseAsync` branch's stated-
// result readings are BLOCKED — annotations/library_adapters/zod/
// parse_evaluator.ts's evaluateParseOutcome has no Go port yet.
// WornOfAnnotation/WornOfObject (from annotations/worn_annotation.ts)
// landed in this package (worn_annotation.go) at integration and are
// no longer the blocker — evaluateParseOutcome is the one remaining
// gap, itself a genuine port task (it reads into the zod adapter's
// own compiled-schema shapes, library_adapters/zod/parse_evaluator.ts)
// left for a future porting unit. Per PORT.md's "blocked function"
// rule, this reader still runs its OWN sound half — the receiver
// forget (parse may alias its argument; an unmodeled parse still
// havocs a tracked receiver) — and then answers the same fallback its
// TS caller sees when the stated-result reading finds nothing: nil,
// which falls through to the ordinary unmodeled-call path. The
// `.decode`/`.encode` branch needs neither missing function and ports
// whole.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// readSchemaRuntimeCall is readSchemaRuntimeCall in the TS source.
func readSchemaRuntimeCall(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// `.decode(x)` / `.encode(x)` on a z.codec schema: the codec's OWN
	// callback runs on the argument — inline it, the way the transform
	// image inlines (vendored schemas.ts:2403: decode runs the
	// transform, encode the reverse)
	if (method == "decode" || method == "encode") && len(arguments) == 1 && ast.IsIdentifier(receiverExpression) {
		schemaSymbol := symbolAt(ctx.P.Checker, receiverExpression)
		var declaration *ast.Node
		if schemaSymbol != nil && len(schemaSymbol.Declarations) > 0 {
			declaration = schemaSymbol.Declarations[0]
		}
		var initializer *ast.Node
		if declaration != nil && ast.IsVariableDeclaration(declaration) {
			initializer = declaration.AsVariableDeclaration().Initializer
		}
		if initializer != nil && ast.IsCallExpression(initializer) {
			initCall := initializer.AsCallExpression()
			if ast.IsPropertyAccessExpression(initCall.Expression) &&
				initCall.Expression.AsPropertyAccessExpression().Name().Text() == "codec" &&
				initCall.Arguments != nil && len(initCall.Arguments.Nodes) == 3 &&
				ast.IsObjectLiteralExpression(initCall.Arguments.Nodes[2]) {
				var found *ast.Node
				for _, property := range initCall.Arguments.Nodes[2].AsObjectLiteralExpression().Properties.Nodes {
					if ast.IsPropertyAssignment(property) {
						assignment := property.AsPropertyAssignment()
						if ast.IsIdentifier(assignment.Name()) && assignment.Name().Text() == method {
							found = property
							break
						}
					}
				}
				if found != nil {
					initializerExpr := found.AsPropertyAssignment().Initializer
					if ast.IsArrowFunction(initializerExpr) || ast.IsFunctionExpression(initializerExpr) {
						argument := evaluateExpression(ctx, env, arguments[0])
						result := InlineCallback(ctx, env, initializerExpr, argument, LoopAnalyzers{
							AnalyzeStatement:   AnalyzeStatement,
							EvaluateExpression: evaluateExpression,
							IterationElement:   IterationElementOf,
						}, nil)
						if result.Kind != abstractdomain.KindUnknown {
							out := abstractdomain.AtTrustLevel(result, abstractdomain.TrustLibrary)
							return &out
						}
					}
				}
			}
		}
	}
	if (method == "parse" || method == "safeParse" || method == "parseAsync") && len(arguments) == 1 {
		argument := arguments[0]
		if ast.IsIdentifier(argument) {
			if _, tracked := env[argument.Text()]; tracked && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
				// parse may return its input — the result shares the
				// argument's reference (the caller may hold both)
				ctx.Aliases.Havoc(env, argument.Text())
			}
		}
		// evaluateParseOutcome (annotations/library_adapters/zod/
		// parse_evaluator.ts) and wornOfAnnotation/wornOfObject
		// (annotations/worn_annotation.ts) are BLOCKED — no Go port yet
		// (see file banner). The TS caller's own fallthrough when the
		// stated-result reading finds nothing is the ordinary unmodeled-
		// call path, which this nil answer reaches too — the receiver
		// forget above already ran, so this is the SAME sound half the
		// TS source's own unreached branches leave standing.
	}
	return nil
}
