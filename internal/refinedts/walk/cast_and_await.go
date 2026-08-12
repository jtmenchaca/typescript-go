// from evaluation/cast_and_await.ts
//
// Casts change no runtime value; await reads fulfillment.
// `as` / `!` / type assertion / satisfies / await (Promise.resolve
// primitive unwrap, schema.parseAsync, promise unwrap).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// allowCasts is ALLOW_CASTS in the TS source (service/analysis_limits.ts).
// service/ is not ported yet (go-port-tracker.md: "pending (last)"), so
// this reads its current false default inline, per the same convention
// abstractdomain.TrustLevelAdmitted uses for STRICTNESS.
const allowCasts = false

// symbolAt is symbolAt in the TS source (service/program_resolution.ts):
// the symbol behind a node, followed THROUGH import aliases — an
// imported name resolves to its declaration in the exporting file, so
// registries keyed by declaration symbols answer for imported names
// too. Inlined at each call site per PORT.md (no ready-made wrapper
// yet; service/program_resolution.ts is not ported).
func symbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		symbol = c.GetAliasedSymbol(symbol)
	}
	return symbol
}

// EvaluateAwait is evaluateAwait in the TS source.
func EvaluateAwait(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	await := e.AsAwaitExpression()
	inner := await.Expression
	// `await Promise.resolve(x)`: Promise.resolve fulfills with a
	// non-thenable x unchanged (sec-promise.resolve — "a new promise
	// resolved with the passed argument"), and await reads the
	// fulfillment value. A primitive can never be a thenable, so when
	// x's SORT is a primitive the composition wears x exactly
	if ast.IsCallExpression(inner) {
		call := inner.AsCallExpression()
		if call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
			ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			if pa.Name().Text() == "resolve" && ast.IsIdentifier(pa.Expression) &&
				pa.Expression.Text() == "Promise" &&
				ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(pa.Expression)) {
				sort := primitives.SortOfPresent(ctx.P.Checker, ctx.P.Checker.GetTypeAtLocation(call.Arguments.Nodes[0]))
				if sort == primitives.SortNumber || sort == primitives.SortString || sort == primitives.SortBool {
					return evaluateExpression(ctx, env, call.Arguments.Nodes[0])
				}
			}
		}
	}
	// `await schema.parseAsync(x)` on a z.promise schema: the library
	// validates the RESOLVED value against the inner statement
	// (vendored core/schemas.ts:4541), so the awaited value wears the
	// inner set — at library grade, since the library's runtime is
	// what enforces it
	if ast.IsCallExpression(inner) {
		call := inner.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			if (pa.Name().Text() == "parseAsync" || pa.Name().Text() == "parse") &&
				ast.IsIdentifier(pa.Expression) {
				schemaSymbol := symbolAt(ctx.P.Checker, pa.Expression)
				var annotation *annotations.Annotation
				if schemaSymbol != nil {
					annotation = ctx.Registry[schemaSymbol]
				}
				if annotation != nil && annotation.Promise {
					if call.Arguments != nil {
						for _, argument := range call.Arguments.Nodes {
							evaluateExpression(ctx, env, argument)
						}
					}
					return WornOfAnnotation(*annotation)
				}
			}
		}
	}
	// the awaited call's effects — reference-argument writes, closure
	// writes — happen before the value lands, so the inner expression
	// evaluates in full; a promise-valued result unwraps to what it
	// resolves to
	landed := evaluateExpression(ctx, env, inner)
	if landed.Kind == abstractdomain.KindPromise {
		return abstractdomain.AtTrustLevel(*landed.Inner, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(landed), abstractdomain.TrustLevelOf(*landed.Inner)))
	}
	return landed
}

// EvaluateSatisfies is evaluateSatisfies in the TS source.
func EvaluateSatisfies(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	satisfies := e.AsSatisfiesExpression()
	// `v satisfies T` is a stated obligation on v — checked where it
	// is written, exactly like an annotated binding
	value := evaluateExpression(ctx, env, satisfies.Expression)
	read := annotations.AnnotationOfType(ctx.P, satisfies.Type, ctx.Registry, ctx.Objects)
	if read.Stated != nil && read.Unsupported == "" {
		CheckAssignability(ctx, value, *read.Stated, satisfies.Expression, "a satisfies value", nil)
	}
	return value
}

// EvaluateCast is evaluateCast in the TS source.
func EvaluateCast(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	// an assertion changes no runtime value — `as`, `x!`, and the
	// legacy `<T>x` alike — so the value's knowledge rides through
	// every cast. A SORT-CHANGING crossing demotes the knowledge to
	// the "asserted" grade (the weakest boundary): downstream reads
	// under the wrong sort still degrade at the read site (the
	// admitted-language rule), while checked positions get to judge
	// the real value. The sorts compare END TO END: `x as unknown as
	// T` reads through when the origin and the final sort agree — the
	// opaque hop rereads nothing.
	//
	// The workspace can BELIEVE casts instead (ALLOW_CASTS, or the
	// per-site `@refinedts-allow-cast` comment): a believed
	// sort-changing cast adopts the asserted shape and stays quiet —
	// the value becomes unknown, and no diagnostic is caused by the
	// cast.
	innermost := castExpressionOf(e)
	for {
		if ast.IsAsExpression(innermost) {
			innermost = innermost.AsAsExpression().Expression
			continue
		}
		if ast.IsNonNullExpression(innermost) {
			innermost = innermost.AsNonNullExpression().Expression
			continue
		}
		if ast.IsTypeAssertion(innermost) {
			innermost = innermost.AsTypeAssertion().Expression
			continue
		}
		if ast.IsParenthesizedExpression(innermost) {
			innermost = innermost.AsParenthesizedExpression().Expression
			continue
		}
		break
	}
	// the PRESENT-part kindTag: absence members are not words, so `x!`
	// on `number | undefined` preserves the sort, and a maybe wrapper
	// survives the `!` — the assertion is the developer's claim, not
	// a proof
	from := primitives.SortOfPresent(ctx.P.Checker, ctx.P.Checker.GetTypeAtLocation(innermost))
	to := primitives.SortOfPresent(ctx.P.Checker, ctx.P.Checker.GetTypeAtLocation(e))
	value := evaluateExpression(ctx, env, innermost)
	if from != to || from == primitives.SortOpaque {
		// OPAQUE provenance survives any crossing — there are no words
		// to reread
		if value.Kind == abstractdomain.KindUnknown {
			if value.Opaque {
				return abstractdomain.Opaque
			}
			return silence.Residue()
		}
		if allowCasts || CastAllowedByComment(ctx, e) {
			return silence.Residue()
		}
		return abstractdomain.AtTrustLevel(value, abstractdomain.TrustAsserted)
	}
	return value
}

// castExpressionOf reads e's own inner expression for the three cast
// syntaxes evaluateForm dispatches on: AsExpression | NonNullExpression
// | TypeAssertion.
func castExpressionOf(e *ast.Node) *ast.Node {
	switch {
	case ast.IsAsExpression(e):
		return e.AsAsExpression().Expression
	case ast.IsNonNullExpression(e):
		return e.AsNonNullExpression().Expression
	case ast.IsTypeAssertion(e):
		return e.AsTypeAssertion().Expression
	default:
		return e
	}
}
