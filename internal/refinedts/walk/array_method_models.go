// from evaluation/array_method_models.ts
//
// The array-method models: Array.isArray, Array.from, the exact
// reads over an exact tuple, and the writing methods that transfer
// in place. Split from builtin_models.ts per the v2 tree.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readArrayIsArray is readArrayIsArray in the TS source: Array.isArray
// — the brand check (sec-array.isarray): exact over every knowledge
// state that pins the sort, unknown where the state does not (a set
// claim speaks tuples, which strings and arrays both are).
func readArrayIsArray(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if !(ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Array" && method == "isArray" &&
		resolvesToDefaultLib(ctx, receiverExpression) && argCount == 1) {
		return nil
	}
	argument := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(argument))
	answer := func(v bool) *abstractdomain.AbstractValue {
		n := float64(0)
		if v {
			n = 1
		}
		out := abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, grade)
		return &out
	}
	switch argument.Kind {
	case abstractdomain.KindValues:
		return answer(argument.KindTag == abstractdomain.PrimitiveArray)
	case abstractdomain.KindList:
		return answer(true)
	case abstractdomain.KindObject:
		// a typeof-"object" ground never pinned the brand — an array
		// satisfies that guard too, so the check stays open
		if argument.MaybeArray {
			out := silence.Residue()
			return &out
		}
		return answer(false)
	case abstractdomain.KindCollection, abstractdomain.KindHostFunction, abstractdomain.KindPromise,
		abstractdomain.KindDate, abstractdomain.KindRegex, abstractdomain.KindSymbol,
		abstractdomain.KindBigints, abstractdomain.KindUndef, abstractdomain.KindNaN:
		return answer(false)
	default:
		out := silence.Residue()
		return &out
	}
}

// readArrayFrom is readArrayFrom in the TS source: Array.from — the
// counted form builds its exact list, an exact length (or an exact
// sequence) with the callback inlined per index, the same machinery
// as map.
func readArrayFrom(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	pa := call.Expression.AsPropertyAccessExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if !(ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Array" &&
		pa.Name().Text() == "from" && resolvesToDefaultLib(ctx, pa.Expression) &&
		argCount >= 1 && argCount <= 2) {
		return nil
	}
	source := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	var items []abstractdomain.AbstractValue
	hasItems := false
	if source.Kind == abstractdomain.KindList {
		items = append(items, source.Items...)
		hasItems = true
	} else if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		for _, v := range source.Values {
			items = append(items, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
		}
		hasItems = true
	} else if source.Kind == abstractdomain.KindObject {
		length := lookupObjectKey(source.Keys, "length")
		if length != nil && length.Kind == abstractdomain.KindValues && len(length.Values) == 1 &&
			isNonNegativeInteger(length.Values[0]) && length.Values[0] <= 10_000 {
			n := int(length.Values[0])
			items = make([]abstractdomain.AbstractValue, n)
			for i := range items {
				items[i] = abstractdomain.Undef
			}
			hasItems = true
		}
	}
	if hasItems {
		if argCount == 1 {
			out := abstractdomain.KnownList(items, abstractdomain.TrustProved)
			return &out
		}
		callbackArgument := call.Arguments.Nodes[1]
		var callback *ast.Node
		if ast.IsArrowFunction(callbackArgument) || ast.IsFunctionExpression(callbackArgument) {
			callback = callbackArgument
		}
		if callback != nil {
			outputs := make([]abstractdomain.AbstractValue, len(items))
			flat := true
			for i, item := range items {
				index := abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
				outputs[i] = InlineCallback(ctx, env, callback, item, LoopAnalyzers{
					AnalyzeStatement:   AnalyzeStatement,
					EvaluateExpression: evaluateExpression,
					IterationElement:   IterationElementOf,
				}, &index)
				if !(outputs[i].Kind == abstractdomain.KindValues && len(outputs[i].Values) == 1 && outputs[i].KindTag == abstractdomain.PrimitiveNumber) {
					flat = false
				}
			}
			if flat {
				values := make([]float64, len(outputs))
				for i, out := range outputs {
					values[i] = out.Values[0]
				}
				result := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				return &result
			}
			result := abstractdomain.KnownList(outputs, abstractdomain.TrustProved)
			return &result
		}
	}
	out := silence.Residue()
	return &out
}

// lookupObjectKey finds a key by name in an ordered ObjectKey slice.
func lookupObjectKey(keys []abstractdomain.ObjectKey, name string) *abstractdomain.AbstractValue {
	for i := range keys {
		if keys[i].Name == name {
			return &keys[i].Value
		}
	}
	return nil
}

func isNonNegativeInteger(v float64) bool {
	return v >= 0 && v == float64(int64(v))
}

// readArrayOf is readArrayOf in the TS source: `Array.of(...items)` IS
// the array literal of its arguments (sec-array.of): all-scalar
// arguments collapse to the flat word, anything else is the exact
// list.
func readArrayOf(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	if !(method == "of" && ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Array" &&
		resolvesToDefaultLib(ctx, receiverExpression)) {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	argKnowns := make([]abstractdomain.AbstractValue, len(arguments))
	for i, argument := range arguments {
		argKnowns[i] = evaluateExpression(ctx, env, argument)
	}
	flat := true
	for _, k := range argKnowns {
		if !(k.Kind == abstractdomain.KindValues && len(k.Values) == 1 && k.KindTag == abstractdomain.PrimitiveNumber) {
			flat = false
			break
		}
	}
	if flat {
		values := make([]float64, len(argKnowns))
		for i, k := range argKnowns {
			values[i] = k.Values[0]
		}
		out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
		return &out
	}
	out := abstractdomain.KnownList(argKnowns, abstractdomain.TrustProved)
	return &out
}

// readArrayReadMethods is readArrayReadMethods in the TS source: the
// exact array reads under the read-only gate: membership by
// SameValueZero, positions by strict equality, windows and reversals
// as copies.
func readArrayReadMethods(site MethodCallSite, argKnowns []abstractdomain.AbstractValue, receiverStringy bool, oracleGrade abstractdomain.TrustLevel) *abstractdomain.AbstractValue {
	receiver, method := site.Receiver, site.Method
	exactInt := func(k abstractdomain.AbstractValue) (float64, bool) {
		if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveNumber &&
			len(k.Values) == 1 && k.Values[0] == float64(int64(k.Values[0])) {
			return k.Values[0], true
		}
		return 0, false
	}
	if !receiverStringy && receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray {
		if (method == "includes" || method == "indexOf" || method == "lastIndexOf") && len(argKnowns) == 1 {
			sought := argKnowns[0]
			if sought.Kind == abstractdomain.KindValues && len(sought.Values) == 1 && sought.KindTag == abstractdomain.PrimitiveNumber {
				v := sought.Values[0]
				if method == "includes" {
					found := false
					for _, x := range receiver.Values {
						if x == v {
							found = true
							break
						}
					}
					n := float64(0)
					if found {
						n = 1
					}
					out := abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, oracleGrade)
					return &out
				}
				index := -1.0
				if method == "indexOf" {
					for i, x := range receiver.Values {
						if x == v {
							index = float64(i)
							break
						}
					}
				} else {
					for i := len(receiver.Values) - 1; i >= 0; i-- {
						if receiver.Values[i] == v {
							index = float64(i)
							break
						}
					}
				}
				out := abstractdomain.KnownValues([]float64{index}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
				return &out
			}
		}
		if method == "toReversed" && len(argKnowns) == 0 {
			reversed := make([]float64, len(receiver.Values))
			for i, v := range receiver.Values {
				reversed[len(receiver.Values)-1-i] = v
			}
			out := abstractdomain.KnownValues(reversed, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
			return &out
		}
		if method == "slice" && len(argKnowns) <= 2 {
			window := make([]float64, len(argKnowns))
			every := true
			for i, k := range argKnowns {
				n, ok := exactInt(k)
				if !ok {
					every = false
					break
				}
				window[i] = n
			}
			if every {
				sliced := sliceFloat64(receiver.Values, window)
				out := abstractdomain.KnownValues(sliced, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				return &out
			}
		}
		if method == "with" && len(argKnowns) == 2 {
			i, iOk := exactInt(argKnowns[0])
			v := argKnowns[1]
			if iOk && v.Kind == abstractdomain.KindValues && len(v.Values) == 1 && v.KindTag == abstractdomain.PrimitiveNumber {
				index := int(i)
				if index < 0 {
					index = len(receiver.Values) + index
				}
				if index >= 0 && index < len(receiver.Values) {
					next := make([]float64, len(receiver.Values))
					copy(next, receiver.Values)
					next[index] = v.Values[0]
					out := abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
					return &out
				}
			}
		}
	}
	return nil
}

// sliceFloat64 mirrors Array.prototype.slice's argument reading over
// a []float64: 0, 1, or 2 exact integer bounds, clamped and negative-
// adjusted the spec's way (sec-array.prototype.slice).
func sliceFloat64(values []float64, window []float64) []float64 {
	n := len(values)
	clamp := func(i int) int {
		if i < 0 {
			i = n + i
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	start := 0
	end := n
	if len(window) >= 1 {
		start = clamp(int(window[0]))
	}
	if len(window) >= 2 {
		end = clamp(int(window[1]))
	}
	if start >= end {
		return []float64{}
	}
	out := make([]float64, end-start)
	copy(out, values[start:end])
	return out
}

// readArrayWriteMethods is readArrayWriteMethods in the TS source: the
// writing array methods over an exact tuple with exact operands — the
// tracked class updates in place, no havoc, the new tuple is known.
func readArrayWriteMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	if !(site.HasTrackedName && receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray &&
		(method == "push" || method == "pop" || method == "shift" || method == "unshift" || method == "splice" || method == "fill")) {
		return nil
	}
	trackedName := site.TrackedName
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	argKnowns := make([]abstractdomain.AbstractValue, len(arguments))
	for i, argument := range arguments {
		argKnowns[i] = evaluateExpression(ctx, env, argument)
	}
	exact := make([]float64, len(argKnowns))
	allExact := true
	for i, k := range argKnowns {
		if !(k.Kind == abstractdomain.KindValues && len(k.Values) == 1 && k.KindTag == abstractdomain.PrimitiveNumber) {
			allExact = false
			break
		}
		exact[i] = k.Values[0]
	}
	if allExact {
		held := make([]float64, len(receiver.Values))
		copy(held, receiver.Values)
		if method == "push" {
			next := append(append([]float64{}, held...), exact...)
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{float64(len(next))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "unshift" {
			next := append(append([]float64{}, exact...), held...)
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{float64(len(next))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "pop" && len(exact) == 0 {
			if len(held) == 0 {
				dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(held, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.Undef
				return &out
			}
			removed := held[len(held)-1]
			next := held[:len(held)-1]
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{removed}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "shift" && len(exact) == 0 {
			if len(held) == 0 {
				dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(held, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.Undef
				return &out
			}
			removed := held[0]
			next := held[1:]
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{removed}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "fill" && len(exact) == 1 {
			next := make([]float64, len(held))
			for i := range next {
				next[i] = exact[0]
			}
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
			return &out
		}
		if method == "splice" && len(exact) >= 1 {
			allInt := true
			for _, x := range exact {
				if x != float64(int64(x)) {
					allInt = false
					break
				}
			}
			if allInt {
				start := int(exact[0])
				if start < 0 {
					start = len(held) + start
					if start < 0 {
						start = 0
					}
				}
				if start > len(held) {
					start = len(held)
				}
				deleteCount := len(held) - start
				if len(exact) >= 2 {
					deleteCount = int(exact[1])
					if deleteCount < 0 {
						deleteCount = 0
					}
					if start+deleteCount > len(held) {
						deleteCount = len(held) - start
					}
				}
				removed := make([]float64, deleteCount)
				copy(removed, held[start:start+deleteCount])
				var inserted []float64
				if len(exact) > 2 {
					inserted = exact[2:]
				}
				next := make([]float64, 0, len(held)-deleteCount+len(inserted))
				next = append(next, held[:start]...)
				next = append(next, inserted...)
				next = append(next, held[start+deleteCount:]...)
				dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.KnownValues(removed, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				return &out
			}
		}
	}
	// inexact operands on a writing method: the class forgets
	ctx.Aliases.Havoc(env, trackedName)
	out := silence.Residue()
	return &out
}
