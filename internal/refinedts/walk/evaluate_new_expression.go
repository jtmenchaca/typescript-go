// from evaluation/evaluate_new_expression.ts
//
// What a `new C(args)` expression holds: builtin collection and Date
// construction, the Error family's message, web platform constructors,
// a class in reach read from its own text, or opaque/residue when the
// constructor has no body the walk can follow.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// EvaluateNewExpression is evaluateNewExpression in the TS source.
func EvaluateNewExpression(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	// the built-ins' own argument contracts — `new Array(len)` and
	// kin (builtin_contracts.ts)
	CheckBuiltinContracts(ctx, env, e)
	if collection := ReadCollectionConstruction(ctx, env, e); collection != nil {
		return collection
	}
	if date := ReadDateConstruction(ctx, env, e); date != nil {
		return date
	}
	// the Error family: an OWN message property from the argument
	// (sec-error-message — installed when the argument is not
	// undefined; the prototype's "" answers otherwise), everything
	// else open — the object is incomplete
	if ast.IsIdentifier(newExpr.Expression) && ErrorConstructors[newExpr.Expression.Text()] &&
		resolvesToDefaultLib(ctx, newExpr.Expression) {
		var argument *ast.Node
		hasArgument := false
		if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
			argument, hasArgument = newExpr.Arguments.Nodes[0], true
		}
		var message abstractdomain.AbstractValue
		if !hasArgument {
			message = abstractdomain.KnownValues(nil, abstractdomain.PrimitiveString, abstractdomain.TrustSpec)
		} else {
			message = evaluateExpression(ctx, env, argument)
		}
		var held abstractdomain.AbstractValue
		if message.Kind == abstractdomain.KindValues && message.KindTag == abstractdomain.PrimitiveString {
			held = message
		} else {
			held = abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		}
		out := abstractdomain.KnownObject([]abstractdomain.ObjectKey{{Name: "message", Value: held}}, nil, false, abstractdomain.TrustSpec, false)
		return &out
	}
	// the web platform constructors (web.ts): Response with its
	// birth facts, the rest as objects with unstated keys
	web := WebNew(ctx.P, e, func(init *ast.Node, hasInit bool) *abstractdomain.AbstractValue {
		if !hasInit {
			return nil
		}
		known := evaluateExpression(ctx, env, init)
		if known.Kind != abstractdomain.KindObject {
			return nil
		}
		if idx, ok := objectKeyIndex(known, "status"); ok {
			out := known.Keys[idx].Value
			return &out
		}
		// a COMPLETE init without a status key takes the default
		if known.Complete {
			out := abstractdomain.KnownValues([]float64{200}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			return &out
		}
		return nil
	})
	if web != nil {
		return web
	}
	// `new` over a constructor with no body in reach builds a value
	// from outside the file's determination — opaque, like the calls
	if BodilessCallee(ctx, newExpr.Expression) && !CalleeInDefaultLib(ctx, newExpr.Expression) {
		if newExpr.Arguments != nil {
			for _, argument := range newExpr.Arguments.Nodes {
				evaluateExpression(ctx, env, argument)
			}
		}
		out := abstractdomain.Opaque
		return &out
	}
	// `new` through a member of an OPAQUE value — the dynamic
	// constructor pattern, `new (date.constructor)(value)`: the
	// constructor entered from outside, so does what it builds
	calleeCore := newExpr.Expression
	for {
		if ast.IsParenthesizedExpression(calleeCore) {
			calleeCore = calleeCore.AsParenthesizedExpression().Expression
			continue
		}
		if ast.IsAsExpression(calleeCore) {
			calleeCore = calleeCore.AsAsExpression().Expression
			continue
		}
		if ast.IsNonNullExpression(calleeCore) {
			calleeCore = calleeCore.AsNonNullExpression().Expression
			continue
		}
		break
	}
	if ast.IsPropertyAccessExpression(calleeCore) || ast.IsElementAccessExpression(calleeCore) {
		var receiverExpr *ast.Node
		if ast.IsPropertyAccessExpression(calleeCore) {
			receiverExpr = calleeCore.AsPropertyAccessExpression().Expression
		} else {
			receiverExpr = calleeCore.AsElementAccessExpression().Expression
		}
		if ast.IsIdentifier(receiverExpr) {
			held, ok := env.Get(receiverExpr.Text())
			if ok && held.Kind == abstractdomain.KindUnknown && held.Opaque {
				if newExpr.Arguments != nil {
					for _, argument := range newExpr.Arguments.Nodes {
						evaluateExpression(ctx, env, argument)
					}
				}
				out := abstractdomain.Opaque
				return &out
			}
		}
	}
	// a class declared IN REACH reads its constructor: the instance
	// wears the property initializers overlaid with every value the
	// constructor's own text writes to `this` — joined, so every
	// taken path is covered. class-LIKE, so `const C = class { … }`
	// reads its members exactly as a declaration does — the same
	// widening ir_summary_call.go's constructorDeclarationOf already
	// carries on the summary side.
	if ast.IsIdentifier(calleeCore) {
		symbol := ctx.P.Checker.GetSymbolAtLocation(calleeCore)
		if symbol != nil && symbol.ValueDeclaration != nil {
			declaration := symbol.ValueDeclaration
			if ast.IsClassLike(declaration) && !ast.GetSourceFileOfNode(declaration).IsDeclarationFile {
				var argKnowns []abstractdomain.AbstractValue
				if newExpr.Arguments != nil {
					argKnowns = make([]abstractdomain.AbstractValue, len(newExpr.Arguments.Nodes))
					for i, argument := range newExpr.Arguments.Nodes {
						argKnowns[i] = evaluateExpression(ctx, env, argument)
					}
				}
				out := ConstructedInstance(ctx, declaration, argKnowns)
				return &out
			}
		}
	}
	return nil
}
