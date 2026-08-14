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
	// `await Promise.resolve(x)`: the composition wears x exactly,
	// for EVERY non-thenable x — not only a primitive one.
	//
	// The guarantee is the resolving function's own steps
	// (sec-createresolvingfunctions, the [[Resolve]] closure), reached
	// from Promise.resolve through PromiseResolve
	// (sec-promise.resolve → sec-promise-resolve, step 3 "Perform
	// Call(promiseCapability.[[Resolve]], undefined, « resolution »)"):
	//
	//	4. If resolution is not an Object, then
	//	   a. Perform FulfillPromise(promise, resolution).
	//	...
	//	7. If IsCallable(thenAction) is false, then
	//	   a. Perform FulfillPromise(promise, resolution).
	//
	// So a NON-OBJECT fulfills unchanged (step 4) and an OBJECT whose
	// `then` is not callable ALSO fulfills unchanged (step 7). Only a
	// callable `then` enqueues NewPromiseResolveThenableJob and adopts
	// another value — the one genuine exception. And await reads the
	// fulfillment value, so away from that exception the composition
	// hands x back.
	//
	// The gate is therefore on the argument's own VALUE, not on its
	// type's sort: the walk evaluates x once and unwraps when x's KIND
	// cannot be a thenable (CannotBeThenable below). A promise value is
	// exactly the excluded case — its `then` is callable by
	// construction — and an object value is admitted only when the walk
	// knows its keys completely and none of them is `then`.
	//
	// The argument evaluates exactly ONCE either way: the branch
	// returns on both outcomes rather than falling through to the
	// generic path, which would evaluate the whole call — and with it
	// the argument — a second time. On the non-unwrap outcome the
	// awaited value is the walk's own residue: Promise.resolve of a
	// thenable adopts a value this walk cannot name, and the generic
	// path would state nothing better. This mirrors the parseAsync
	// branch below, which likewise evaluates its arguments and returns.
	if ast.IsCallExpression(inner) {
		call := inner.AsCallExpression()
		if call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
			ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			if pa.Name().Text() == "resolve" && ast.IsIdentifier(pa.Expression) &&
				pa.Expression.Text() == "Promise" &&
				ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(pa.Expression)) {
				argument := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
				if CannotBeThenable(argument) {
					return argument
				}
				// a thenable (or a value the walk cannot rule out as one)
				// adopts something unnamed — and a promise ARGUMENT is
				// `Promise.resolve` returning it unchanged
				// (sec-promise-resolve step 1), so the await reads its own
				// inner value
				if argument.Kind == abstractdomain.KindPromise {
					return abstractdomain.AtTrustLevel(*argument.Inner, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(argument), abstractdomain.TrustLevelOf(*argument.Inner)))
				}
				return silence.Residue()
			}
		}
	}
	// `await schema.parseAsync(x)` — and `await schema.parse(x)` on a
	// z.promise schema. Either way the awaited value wears the schema's
	// stated set, at library grade, since the library's runtime is what
	// enforces it. The two spellings reach that same set by different
	// routes:
	//
	//   - a z.promise schema (annotation.Promise) validates the
	//     RESOLVED value against the inner statement (vendored
	//     core/schemas.ts:4541), and the annotation's set already IS
	//     the inner one — so awaiting either parse or parseAsync reads
	//     through to it.
	//   - parseAsync on ANY schema returns a real Promise of the
	//     validated output: vendored core/parse.ts:39-49, `_parseAsync`
	//     awaits the run and `return result.value as
	//     core.output<typeof schema>`, throwing when issues remain; and
	//     the declared surface is `Promise<core.output<this>>` for
	//     every schema (vendored classic/schemas.ts:117, mini/
	//     schemas.ts:32), never only for z.promise. So the awaited
	//     value wears the schema's set whatever the schema is — the
	//     ordinary object/string/number case included, which the
	//     annotation.Promise gate alone never reached.
	//
	// The verification is the doctrine's two readings: the installed
	// library's surface and the vendored source above agree that the
	// Promise wrapper here belongs to parseAsync's own signature, not
	// to the schema's shape.
	//
	// `parse` (sync) on a NON-promise schema is deliberately NOT read
	// here: it returns the validated value directly, so an `await` over
	// it is awaiting a non-thenable and the generic path below already
	// hands the value through unchanged.
	if ast.IsCallExpression(inner) {
		call := inner.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			method := pa.Name().Text()
			if (method == "parseAsync" || method == "parse") && ast.IsIdentifier(pa.Expression) {
				schemaSymbol := symbolAt(ctx.P.Checker, pa.Expression)
				var annotation *annotations.Annotation
				if schemaSymbol != nil {
					annotation = ctx.Registry[schemaSymbol]
				}
				if annotation != nil && (annotation.Promise || method == "parseAsync") {
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

// CannotBeThenable answers whether a value's KIND rules out a callable
// `then` — the one property that makes `Promise.resolve(x)` fulfil with
// something other than x (sec-createresolvingfunctions steps 4 and 7:
// a non-Object fulfils unchanged, and so does an Object whose `then` is
// not callable; only a callable `then` enqueues the thenable job).
//
// Every PRIMITIVE kind answers yes — a non-Object can carry no `then`
// at all. The exotic built-ins the domain names — a Date, a RegExp, a
// Map or Set, an exact list — are Objects, but none of them carries a
// `then` on its prototype chain, so each fulfils unchanged too.
//
// An OBJECT value answers yes only when the walk knows its keys
// COMPLETELY (Complete) and no key is named `then`; a partial key set
// could hide one. A KindPromise answers no by construction. Unknown,
// opaque, variable, and union kinds answer no: the walk cannot rule a
// thenable out, and only ruling it out licenses the unwrap.
func CannotBeThenable(value abstractdomain.AbstractValue) bool {
	switch value.Kind {
	case abstractdomain.KindValues, abstractdomain.KindSet,
		abstractdomain.KindBigints, abstractdomain.KindSymbol,
		abstractdomain.KindUndef, abstractdomain.KindNaN:
		// primitives: not Objects, so step 4 fulfils them unchanged
		return true
	case abstractdomain.KindDate, abstractdomain.KindRegex,
		abstractdomain.KindCollection, abstractdomain.KindList:
		// Objects whose prototypes carry no `then`, so step 7 fulfils
		// them unchanged
		return true
	case abstractdomain.KindObject:
		if !value.Complete {
			return false
		}
		for _, key := range value.Keys {
			if key.Name == "then" {
				return false
			}
		}
		// a bare-prototype object carries only its own keys; an ordinary
		// one inherits from Object.prototype, which has no `then` either
		return true
	default:
		return false
	}
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
