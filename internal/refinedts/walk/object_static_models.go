// from evaluation/object_static_models.ts
//
// The Object statics and Reflect: entries/fromEntries/create/
// getPrototypeOf on evaluated arguments, the read-only statics that
// provably keep their arguments' facts, and the placeable writes
// (defineProperty, Reflect.set, assign). Split from
// builtin_models.ts per the v2 tree.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

func stringOrNoSubstitutionLiteralText(node *ast.Node) (string, bool) {
	if ast.IsStringLiteral(node) || ast.IsNoSubstitutionTemplateLiteral(node) {
		return node.Text(), true
	}
	return "", false
}

// readObjectStaticValues is readObjectStaticValues in the TS source:
// Object.entries/fromEntries/create/getPrototypeOf — the value
// answers on evaluated arguments.
func readObjectStaticValues(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	isObjectReceiver := ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Object" && resolvesToDefaultLib(ctx, receiverExpression)
	// Object.entries: the own enumerable string-keyed pairs, in order
	// (sec-object.entries → EnumerableOwnProperties ~key+value~, whose
	// entry keys are Strings). Exact on a COMPLETE tracked object; an
	// OPAQUE argument's pairs enter from outside with it.
	if isObjectReceiver && method == "entries" && len(arguments) == 1 {
		argument := evaluateExpression(ctx, env, arguments[0])
		if argument.Kind == abstractdomain.KindObject && argument.Complete {
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(argument))
			items := make([]abstractdomain.AbstractValue, len(argument.Keys))
			for i, key := range argument.Keys {
				pair := abstractdomain.KnownList([]abstractdomain.AbstractValue{
					abstractdomain.KnownValues(refinementsets.CodepointsOf(key.Name), abstractdomain.PrimitiveString, grade),
					key.Value,
				}, grade)
				items[i] = pair
			}
			out := abstractdomain.KnownList(items, grade)
			return &out
		}
		if argument.Kind == abstractdomain.KindUnknown && argument.Opaque {
			opaque := abstractdomain.Opaque
			return &opaque
		}
		out := silence.Residue()
		return &out
	}
	// Object.fromEntries over an EXACT list of string-keyed pairs: the
	// built object, key by key (sec-object.fromentries)
	if isObjectReceiver && method == "fromEntries" && len(arguments) == 1 {
		argument := evaluateExpression(ctx, env, arguments[0])
		if argument.Kind == abstractdomain.KindList {
			var keys []abstractdomain.ObjectKey
			readable := true
			for _, item := range argument.Items {
				if item.Kind == abstractdomain.KindList && len(item.Items) == 2 {
					key := item.Items[0]
					if key.Kind == abstractdomain.KindValues && key.KindTag == abstractdomain.PrimitiveString {
						keys = setObjectKey(keys, stringOf(key.Values), item.Items[1])
						continue
					}
				}
				readable = false
				break
			}
			if readable {
				out := abstractdomain.KnownObject(keys, nil, true, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(argument)), false)
				return &out
			}
		}
		// any other iterable still BUILDS an ordinary object —
		// OrdinaryObjectCreate plus data-property writes
		// (sec-object.fromentries); its keys are unstated here
		out := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
		return &out
	}
	// Object.create: OrdinaryObjectCreate with the given prototype
	// (sec-object.create). A `null` prototype births an object with no
	// keys and no chain to read through — a read of any key answers
	// undefined, which is exactly what complete states. Any other
	// prototype leaves the chain readable, so the object's keys stay
	// unstated.
	if isObjectReceiver && method == "create" && len(arguments) == 1 {
		var out abstractdomain.AbstractValue
		if arguments[0].Kind == ast.KindNullKeyword {
			out = abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustSpec, true)
		} else {
			out = abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
		}
		return &out
	}
	// Object.getPrototypeOf answers an Object or null
	// (sec-object.getprototypeof — the argument coerces by ToObject, so
	// primitives answer their wrapper prototypes; undefined and null
	// THROW, and a run that throws never reaches the read). An object
	// with unstated keys, or the absent value.
	if isObjectReceiver && method == "getPrototypeOf" && len(arguments) == 1 {
		inner := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
		out := abstractdomain.PossiblyUndefined(inner, "", false, false)
		return &out
	}
	return nil
}

// readObjectStaticMethods is readObjectStaticMethods in the TS
// source: the read-only Object statics, Object.assign into a fresh
// literal, and the placeable writes on a tracked target.
func readObjectStaticMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	isObjectReceiver := ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Object" && resolvesToDefaultLib(ctx, receiverExpression)
	// the READ-ONLY Object statics provably keep their arguments'
	// facts: no havoc, and the representable results transfer. (Placed
	// BEFORE the array-method gate: "values"/"keys" name array
	// iterators there, and Object is not an array.)
	if isObjectReceiver && (method == "keys" || method == "values" || method == "entries" ||
		method == "hasOwn" || method == "getOwnPropertyNames" || method == "isExtensible" ||
		method == "isFrozen" || method == "isSealed" || method == "is") {
		argKnowns := make([]abstractdomain.AbstractValue, len(arguments))
		for i, argument := range arguments {
			argKnowns[i] = evaluateExpression(ctx, env, argument)
		}
		var first abstractdomain.AbstractValue
		hasFirst := len(argKnowns) > 0
		if hasFirst {
			first = argKnowns[0]
		}
		// Object.values over all-scalar keys is the exact tuple; a
		// structured value set is the exact LIST. Object.keys is the
		// list of key strings, entries the list of [key, value] pairs —
		// all requiring the COMPLETE key set.
		if method == "values" && hasFirst && first.Kind == abstractdomain.KindObject && first.Complete {
			values := make([]float64, 0, len(first.Keys))
			exact := true
			for _, key := range first.Keys {
				if key.Value.Kind == abstractdomain.KindValues && len(key.Value.Values) == 1 && key.Value.KindTag == abstractdomain.PrimitiveNumber {
					values = append(values, key.Value.Values[0])
				} else {
					exact = false
				}
			}
			if exact {
				out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				return &out
			}
			items := make([]abstractdomain.AbstractValue, len(first.Keys))
			for i, key := range first.Keys {
				items[i] = key.Value
			}
			out := abstractdomain.KnownList(items, abstractdomain.TrustProved)
			return &out
		}
		if method == "keys" && hasFirst && first.Kind == abstractdomain.KindObject && first.Complete {
			items := make([]abstractdomain.AbstractValue, len(first.Keys))
			for i, key := range first.Keys {
				items[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(key.Name), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
			}
			out := abstractdomain.KnownList(items, abstractdomain.TrustProved)
			return &out
		}
		if method == "entries" && hasFirst && first.Kind == abstractdomain.KindObject && first.Complete {
			items := make([]abstractdomain.AbstractValue, len(first.Keys))
			for i, key := range first.Keys {
				items[i] = abstractdomain.KnownList([]abstractdomain.AbstractValue{
					abstractdomain.KnownValues(refinementsets.CodepointsOf(key.Name), abstractdomain.PrimitiveString, abstractdomain.TrustProved),
					key.Value,
				}, abstractdomain.TrustProved)
			}
			out := abstractdomain.KnownList(items, abstractdomain.TrustProved)
			return &out
		}
		// Object.hasOwn mirrors `in` over a complete key set
		if method == "hasOwn" && hasFirst && first.Kind == abstractdomain.KindObject && first.Complete && len(arguments) >= 2 {
			keyArgument := arguments[1]
			if text, ok := stringOrNoSubstitutionLiteralText(keyArgument); ok {
				found := lookupObjectKey(first.Keys, text) != nil
				n := float64(0)
				if found {
					n = 1
				}
				out := abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)
				return &out
			}
		}
		out := silence.Residue()
		return &out
	}
	// Object.assign INTO a fresh literal reads its sources without
	// touching them — the result is the merge, and a read is never a
	// reason to forget
	if isObjectReceiver && method == "assign" && len(arguments) >= 1 && ast.IsObjectLiteralExpression(arguments[0]) {
		merged := evaluateExpression(ctx, env, arguments[0])
		for _, source := range arguments[1:] {
			s := evaluateExpression(ctx, env, source)
			if merged.Kind != abstractdomain.KindObject || s.Kind != abstractdomain.KindObject {
				merged = silence.Residue()
				continue
			}
			keys := append([]abstractdomain.ObjectKey{}, merged.Keys...)
			for _, key := range s.Keys {
				keys = setObjectKey(keys, key.Name, key.Value)
			}
			merged = abstractdomain.KnownObject(keys, nil, merged.Complete && s.Complete, abstractdomain.TrustProved, false)
		}
		return &merged
	}
	// Object.defineProperty / Reflect.set / Object.assign on a tracked
	// target are PLACEABLE writes: the key takes the value
	isReflectReceiver := ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Reflect" && resolvesToDefaultLib(ctx, receiverExpression)
	if (isObjectReceiver || isReflectReceiver) && len(arguments) >= 1 && ast.IsIdentifier(arguments[0]) {
		targetName := arguments[0].Text()
		if _, tracked := env[targetName]; tracked {
			held, ok := env[targetName]
			if !ok {
				held = silence.Residue()
			}
			var key string
			hasKey := false
			if len(arguments) >= 2 {
				key, hasKey = stringOrNoSubstitutionLiteralText(arguments[1])
			}
			if isObjectReceiver && method == "defineProperty" && hasKey && held.Kind == abstractdomain.KindObject {
				var descriptor *ast.Node
				if len(arguments) >= 3 {
					descriptor = arguments[2]
				}
				written := silence.Residue()
				if descriptor != nil && ast.IsObjectLiteralExpression(descriptor) {
					for _, property := range descriptor.AsObjectLiteralExpression().Properties.Nodes {
						if ast.IsPropertyAssignment(property) {
							assignment := property.AsPropertyAssignment()
							if ast.IsIdentifier(assignment.Name()) && assignment.Name().Text() == "value" {
								written = evaluateExpression(ctx, env, assignment.Initializer)
								break
							}
						}
					}
				}
				keys := setObjectKey(append([]abstractdomain.ObjectKey{}, held.Keys...), key, written)
				dataflowfacts.UpdateTracked(ctx.Aliases, env, targetName, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				out := silence.Residue()
				return &out
			}
			if isReflectReceiver && method == "set" && hasKey && held.Kind == abstractdomain.KindObject && len(arguments) >= 3 {
				written := evaluateExpression(ctx, env, arguments[2])
				keys := setObjectKey(append([]abstractdomain.ObjectKey{}, held.Keys...), key, written)
				dataflowfacts.UpdateTracked(ctx.Aliases, env, targetName, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				out := silence.Residue()
				return &out
			}
			if isObjectReceiver && method == "assign" {
				var keys []abstractdomain.ObjectKey
				readable := held.Kind == abstractdomain.KindObject
				if readable {
					keys = append([]abstractdomain.ObjectKey{}, held.Keys...)
				}
				for _, source := range arguments[1:] {
					s := evaluateExpression(ctx, env, source)
					if readable && s.Kind == abstractdomain.KindObject {
						for _, key := range s.Keys {
							keys = setObjectKey(keys, key.Name, key.Value)
						}
					} else {
						readable = false
					}
				}
				if readable {
					next := abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false)
					dataflowfacts.UpdateTracked(ctx.Aliases, env, targetName, next)
					return &next
				}
				ctx.Aliases.Havoc(env, targetName)
				out := silence.Residue()
				return &out
			}
		}
	}
	return nil
}
