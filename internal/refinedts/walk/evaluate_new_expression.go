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
	// what `new Array(…)` BUILDS — the one-length hole array, the
	// items array (array_construction.go)
	if array := ReadArrayConstruction(ctx, env, e); array != nil {
		return array
	}
	// what `new Uint8Array(…)` / `new Int8Array(…)` /
	// `new Uint8ClampedArray(…)` BUILD — a zero-filled view, or each
	// array-like element seeded through its own ToXxx conversion
	// (typed_array_models.go)
	if typedArray := ReadTypedArrayConstruction(ctx, env, e); typedArray != nil {
		return typedArray
	}
	if collection := ReadCollectionConstruction(ctx, env, e); collection != nil {
		return collection
	}
	// `new Proxy(target, {})` with no `get` trap: [[Get]] forwards to
	// the target exactly (proxy_construction.go, sec-proxy-object step
	// 5) — checked before the bodiless-callee opaque fallback below,
	// which would otherwise answer every Proxy construction opaque
	// regardless of whether its handler traps reads at all
	if proxied := ReadProxyConstruction(ctx, env, e); proxied != nil {
		return proxied
	}
	if date := ReadDateConstruction(ctx, env, e); date != nil {
		return date
	}
	// `new Promise(executor)` with the resolve calls in view: the
	// promise wraps the join of what the executor resolves
	if promise := ReadPromiseConstruction(ctx, env, e); promise != nil {
		return promise
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
	// from outside the file's determination — the same opaque-or-worn
	// reading unmodeled_call_result.go's opaqueWorn gives an unmodeled
	// CALL: the constructed instance's own RESOLVED type is a claim tsc
	// already checked the (bodiless) constructor's signature against,
	// so a `new UnknownPerson(40).age` read carries that shape (LIBRARY
	// grade) rather than falling to the bare, structure-blind opaque
	// that would let `.age` read as unknown instead of the number
	// ground its declared signature states.
	if BodilessCallee(ctx, newExpr.Expression) && !CalleeInDefaultLib(ctx, newExpr.Expression) {
		if newExpr.Arguments != nil {
			for _, argument := range newExpr.Arguments.Nodes {
				evaluateExpression(ctx, env, argument)
			}
		}
		if ground := ReturnTypeGround(ctx, e); ground != nil {
			out := abstractdomain.AtTrustLevel(*ground, abstractdomain.TrustLibrary)
			return &out
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
	// carries on the summary side. symbolAt follows ONE alias hop
	// (cast_and_await.go) — an imported class name's own symbol is the
	// IMPORT SPECIFIER's alias symbol, whose ValueDeclaration is the
	// specifier node, not the class; without the hop every imported
	// `new Person(...)` fell through this branch entirely and the
	// instance never read the exporting file's constructor.
	if ast.IsIdentifier(calleeCore) {
		symbol := symbolAt(ctx.P.Checker, calleeCore)
		if symbol != nil && symbol.ValueDeclaration != nil {
			declaration := symbol.ValueDeclaration
			// `const C = class { … }` binds the NAME to a variable, so the
			// symbol's value declaration is the VariableDeclaration — the
			// class expression sits in its initializer. A `const` binding
			// holds that one class for its whole life, so the expression
			// reads exactly as a declaration does; a `let`/`var` may hold a
			// different constructor by the time the `new` runs, and stays
			// unresolved here.
			if !ast.IsClassLike(declaration) && ast.IsVariableDeclaration(declaration) &&
				declaration.Parent != nil && (declaration.Parent.Flags&ast.NodeFlagsConst) != 0 {
				if initializer := declaration.AsVariableDeclaration().Initializer; initializer != nil {
					if unwrapped := Unwrapped(initializer); unwrapped != nil && ast.IsClassExpression(unwrapped) {
						declaration = unwrapped
					}
				}
			}
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
