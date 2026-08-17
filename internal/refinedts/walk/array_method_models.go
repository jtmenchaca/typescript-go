// from evaluation/array_method_models.ts
//
// The array-method models: Array.isArray, Array.from, the exact
// reads over an exact tuple, and the writing methods that transfer
// in place. Split from builtin_models.ts per the v2 tree.

package walk

import (
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
	case abstractdomain.KindArrayHoles:
		// the same brand ReadArrayConstruction always builds under —
		// sec-array's algorithm returns an actual Array exotic object at
		// any length, holes included
		return answer(true)
	case abstractdomain.KindObjectStar:
		// the object-star is only ever built where the value IS an array
		// exotic object — Array.from, a spread literal, a declared `T[]`,
		// map/filter — so the brand is pinned even though the length is
		// not. isArray reads the brand alone (sec-isarray), so the
		// unstated count is beside the point.
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
// as map. An array-like `{length: n}` source with NO mapper and n past
// arrayConstructionHoleLimit builds KnownArrayHoles instead of
// declining — the same past-ceiling representation
// ReadArrayConstruction (array_construction.go) uses for `new Array(n)`.
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
	sourceExpression := call.Arguments.Nodes[0]
	source := evaluateExpression(ctx, env, sourceExpression)
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
			isNonNegativeInteger(length.Values[0]) {
			n := int(length.Values[0])
			if n <= arrayConstructionHoleLimit {
				items = make([]abstractdomain.AbstractValue, n)
				for i := range items {
					items[i] = abstractdomain.Undef
				}
				hasItems = true
			} else if argCount == 1 {
				// past the materialization ceiling, with NO mapper: sec-
				// array.from's array-like branch (steps _arrayLike_.._k_,
				// tmp/ecma262/spec.html sec-array.from) reads
				// Get(arrayLike, ToString(k)) at every index 0..length-1
				// and writes it via CreateDataPropertyOrThrow — so every
				// index becomes an OWN property. An array-like whose
				// length key is all the walk knows (no own numeric keys
				// read) has no property at any index, and Get on an
				// ordinary object with no matching own or inherited
				// property answers undefined (OrdinaryGet, sec-
				// ordinary-object-internal-methods-and-internal-slots-get-p-receiver).
				// The RESULT is therefore a DENSE array of n undefined
				// VALUES — every index IS an own property, unlike
				// `new Array(n)`'s sparse holes, where no index is an own
				// property at all. KnownArrayHoles' Length/ElementSet claims
				// cover both shapes identically for every read the walk
				// answers off a KindArrayHoles receiver except
				// Object.keys/values/entries (.length, an element read,
				// Array.isArray, instanceof, truthiness all read the same
				// either way — a sparse hole read and a dense-undefined
				// read both answer undefined). The Dense=true argument below
				// is what lets Object.keys(arr) answer the n index strings
				// instead of [] (object_static_models.go). A mapper argument
				// (argCount == 2) still declines past the ceiling below: each
				// output is
				// Call(mapper, thisArg, kValue, 𝔽(k)) (step 10.d.i), which
				// this model can only answer by inlining the callback per
				// index — the same reason readArrayFrom's own map/filter
				// siblings need materialized items.
				out := abstractdomain.KnownArrayHoles(n, abstractdomain.TrustProved, true)
				return &out
			}
		}
	} else if collectionItems, ok := collectionSpreadItems(source); ok {
		// a BUILT collection drains exactly: a Set's members, a Map's
		// [key, value] pairs, in entry order (sec-array.from step 6 over
		// the collection's own iterator)
		items = append(items, collectionItems...)
		hasItems = true
	}
	if !hasItems {
		// a values()/keys()/entries() view over a receiver the walk
		// holds exactly drains those same exact items — read off the
		// environment, which the view call's evaluation above left
		// intact
		if viewItems, ok := drainedViewItems(ctx, env, sourceExpression); ok {
			items = append(items, viewItems...)
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
	// a bare builtin ITERATOR argument — `Array.from(map.values())`. The
	// walk holds no items for it (a view over a collection it never
	// tracked), but the iterator's own type argument states what one
	// element is, and Array.from drains the whole iterator
	// (sec-array.from step 6: every element the iterator yields, in
	// order), so the array holds exactly those elements at a count the
	// collection decides. Only the UNARY form: `Array.from(it, f)` maps
	// each element through f, and what f answers is a separate walk this
	// row does not run, so the two-argument form keeps the answer below.
	if argCount == 1 {
		if sequence, ok := builtinIteratorSequenceOf(ctx, sourceExpression); ok {
			return &sequence
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
		// `join` — sec-array.prototype.join: an undefined separator reads
		// as "," (step 3), else ToString of the exact separator; each
		// element converts through ToString (step 7.c.ii) — a Number
		// spells its decimal form (sec-numeric-types-number-tostring,
		// through the kernel's proved speller) — and the pieces
		// concatenate with the separator between them. The undef marker
		// declines the separator: it conflates undefined (the ","
		// default) with null, whose ToString is the word "null".
		if method == "join" && len(argKnowns) <= 1 {
			separator, separatorOk := ",", true
			if len(argKnowns) == 1 {
				separator, separatorOk = exactStringOf(argKnowns[0])
			}
			if separatorOk {
				text, grade := jsNumericJoin(site.Ctx.Kernel.Decimal, receiver.Values, separator)
				out := abstractdomain.KnownValues(refinementsets.CodepointsOf(text), abstractdomain.PrimitiveString, abstractdomain.MinTrustLevel(oracleGrade, grade))
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
	// `join` over an exact LIST: each item's own text reading joins
	// through the one text model (sec-array.prototype.join step 7.c.ii
	// reads ToString of each element); an undefined item contributes
	// the empty string — step 7.c skips ToString for undefined AND
	// null, so the undef marker's conflation costs nothing here. Exact
	// only when every piece is.
	if !receiverStringy && receiver.Kind == abstractdomain.KindList && method == "join" && len(argKnowns) <= 1 {
		separator, separatorOk := ",", true
		if len(argKnowns) == 1 {
			separator, separatorOk = exactStringOf(argKnowns[0])
		}
		if separatorOk {
			grade := oracleGrade
			pieces := make([]string, len(receiver.Items))
			every := true
			for i, item := range receiver.Items {
				if item.Kind == abstractdomain.KindUndef {
					pieces[i] = ""
					continue
				}
				reading, ok := TextOfKnown(site.Ctx.Kernel.Decimal, item)
				if !ok || !reading.HasExact {
					every = false
					break
				}
				grade = abstractdomain.MinTrustLevel(grade, reading.Grade)
				pieces[i] = stringOf(reading.Exact)
			}
			if every {
				out := abstractdomain.KnownValues(refinementsets.CodepointsOf(strings.Join(pieces, separator)), abstractdomain.PrimitiveString, grade)
				return &out
			}
		}
	}
	// `join` over ARRAY-HOLES: sec-array.prototype.join
	// (tmp/ecma262/spec.html, sec-array.prototype.join) reads
	// Get(obj, ToString(k)) at every index and, "If element is neither
	// undefined nor null," appends its ToString — an undefined element
	// (every element here; the present-element set is ∅) contributes
	// NOTHING, not even the empty string via a ToString call, so the
	// result is exactly (length−1) copies of the separator with no
	// piece between them: `new Array(n).join(",")` is exactly n−1
	// commas, the empty string at n ≤ 1. The exact answer is fully
	// DETERMINED (there is no per-element uncertainty — the piece is
	// always ""), so this is not a materialization tradeoff the way an
	// arbitrary array's join is; the only cost is the string LENGTH,
	// same shape arrayConstructionHoleLimit already bounds.
	if !receiverStringy && receiver.Kind == abstractdomain.KindArrayHoles && method == "join" && len(argKnowns) <= 1 {
		length, lengthOk := abstractdomain.LengthOfArrayHoles(receiver)
		separator, separatorOk := ",", true
		if len(argKnowns) == 1 {
			separator, separatorOk = exactStringOf(argKnowns[0])
		}
		if lengthOk && separatorOk {
			grade := abstractdomain.MinTrustLevel(oracleGrade, abstractdomain.TrustLevelOf(receiver))
			copies := length - 1
			if copies < 0 {
				copies = 0
			}
			if copies <= arrayConstructionHoleLimit {
				out := abstractdomain.KnownValues(refinementsets.CodepointsOf(strings.Repeat(separator, copies)), abstractdomain.PrimitiveString, grade)
				return &out
			}
			// past the materialization ceiling: the same EXACT claim,
			// carried as the fixed-length window over the one-character
			// alphabet {separator} rather than a materialized codepoint
			// array (refinementsets.Repetition, the idiom
			// sequence_copy_models.go/destructuring.go already use for a
			// length-bounded string set). Only when the separator itself
			// is exactly ONE codepoint — a multi-codepoint separator
			// repeated `copies` times has a UTF-16 length this window
			// cannot pin exactly (astral separators cost 2 units per
			// occurrence). The one-codepoint case's .length DOES read
			// back exactly now: evaluate_property_access.go's stringy
			// branch answers the window's own lo==hi scalar count
			// whenever the element alphabet is proven astral-free
			// (astralFreeSet, number_range.go) — a single BMP separator
			// (a comma) qualifies; an astral one (an emoji) does not and
			// keeps the floor-only answer.
			separatorCodepoints := refinementsets.CodepointsOf(separator)
			if len(separatorCodepoints) == 1 {
				element := refinementsets.MakeRefinedSet(refinementsets.OneOf(separatorCodepoints))
				copiesHi := copies
				out := abstractdomain.KnownSet(
					refinementsets.Repetition(element, copies, &copiesHi),
					nil, grade, abstractdomain.SetKindTagNone,
				)
				return &out
			}
		}
	}
	return nil
}

// jsNumericJoin joins an exact numeric tuple's elements through the
// ONE number-text model (TextOfKnown's decimal spelling, text_of_value.go),
// separated by an exact separator — sec-array.prototype.join's loop
// with every element a Number, each spelled by
// sec-numeric-types-number-tostring. Returns the joined text and the
// trust floor of the spellings used.
func jsNumericJoin(decimal func(v float64) (string, bool), values []float64, separator string) (string, abstractdomain.TrustLevel) {
	grade := abstractdomain.TrustProved
	pieces := make([]string, len(values))
	for i, v := range values {
		reading, _ := TextOfKnown(decimal, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
		grade = abstractdomain.MinTrustLevel(grade, reading.Grade)
		pieces[i] = stringOf(reading.Exact)
	}
	return strings.Join(pieces, separator), grade
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

// readFreshArrayFill answers `.fill(value)` (no start/end) called
// directly on a FRESH array value — one this walk just built and holds
// no tracked name for, like `Array(2).fill(41)` — rather than on a
// tracked identifier. sec-array.prototype.fill (spec.html) sets every
// slot [0, length) through Set with no other observable effect
// (step 9a), so an exact receiver's OWN VALUE composes to the same
// exact array with every slot replaced by the fill value; there is no
// env binding to update since the receiver expression names nothing
// tracked; the caller (an enclosing spread, element read, or
// `.length`) reads the returned value directly off the call
// expression's own evaluation. Distinct from readArrayWriteMethods
// below, which owns the TRACKED-name case (push/pop/shift/unshift/
// splice/fill through UpdateTrackedEnv, including the tracked
// `.fill()` mutation) — this reader fires only where NO tracked name
// exists to update, so the two never compete over the same call.
func readFreshArrayFill(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	if method != "fill" || site.HasTrackedName {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// only the one-argument form (no start/end) — a partial window
	// still sets every slot it covers to the SAME value, but the
	// window bounds would need their own exact-integer reading before
	// this reader could compose a sound per-slot result
	if len(arguments) != 1 {
		return nil
	}
	value := evaluateExpression(ctx, env, arguments[0])
	switch receiver.Kind {
	case abstractdomain.KindList:
		next := make([]abstractdomain.AbstractValue, len(receiver.Items))
		for i := range next {
			next[i] = value
		}
		grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(value))
		out := abstractdomain.KnownList(next, grade)
		return &out
	case abstractdomain.KindValues:
		if receiver.KindTag != abstractdomain.PrimitiveArray {
			return nil
		}
		if !(value.Kind == abstractdomain.KindValues && value.KindTag == abstractdomain.PrimitiveNumber && len(value.Values) == 1) {
			return nil
		}
		next := make([]float64, len(receiver.Values))
		for i := range next {
			next[i] = value.Values[0]
		}
		grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(value))
		out := abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, grade)
		return &out
	default:
		return nil
	}
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
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{float64(len(next))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "unshift" {
			next := append(append([]float64{}, exact...), held...)
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{float64(len(next))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "pop" && len(exact) == 0 {
			if len(held) == 0 {
				UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(held, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.Undef
				return &out
			}
			removed := held[len(held)-1]
			next := held[:len(held)-1]
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{removed}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "shift" && len(exact) == 0 {
			if len(held) == 0 {
				UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(held, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.Undef
				return &out
			}
			removed := held[0]
			next := held[1:]
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			out := abstractdomain.KnownValues([]float64{removed}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			return &out
		}
		if method == "fill" && len(exact) == 1 {
			next := make([]float64, len(held))
			for i := range next {
				next[i] = exact[0]
			}
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
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
				UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
				out := abstractdomain.KnownValues(removed, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				return &out
			}
		}
	}
	// inexact operands on a writing method: the class forgets
	HavocEnv(ctx.Aliases, env, trackedName)
	out := silence.Residue()
	return &out
}

// readArraySortReverseMethods is the in-place reorderings — `sort()`
// with no comparator, and `reverse()` — over an exact numeric tuple.
// Each mutates the tracked array to its new order and answers that
// same array back, the way push/pop/splice above update the class in
// place rather than leaving the old order to be reread stale.
//
// `sort(comparator)` — a comparator argument runs arbitrary code this
// reader does not walk, so only the zero-argument form is modeled;
// the comparator form falls through to the havoc below, same as an
// inexact operand on the other writers.
func readArraySortReverseMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	if !(site.HasTrackedName && receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray &&
		(method == "sort" || method == "reverse")) {
		return nil
	}
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	trackedName := site.TrackedName
	if method == "reverse" && argCount == 0 {
		reversed := make([]float64, len(receiver.Values))
		for i, v := range receiver.Values {
			reversed[len(receiver.Values)-1-i] = v
		}
		next := abstractdomain.KnownValues(reversed, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, next)
		return &next
	}
	// `sort()` with no comparator: CompareArrayElements' default arm
	// converts each element through ToString and orders the pair by
	// that STRING comparison (sec-array.prototype.sort via
	// sec-comparearrayelements, steps step-sortcompare-tostring-x/y
	// onward) — never the numeric order, so `[9, 10]` sorts to
	// `[10, 9]` (the strings "10" < "9"). The permutation the sort
	// order clause requires is a STABLE one (equal elements keep
	// their relative places), which sort.SliceStable gives outright.
	if method == "sort" && argCount == 0 {
		type keyedValue struct {
			text  string
			value float64
		}
		keyed := make([]keyedValue, len(receiver.Values))
		every := true
		for i, v := range receiver.Values {
			text, ok := ctx.Kernel.Decimal(v)
			if !ok {
				every = false
				break
			}
			keyed[i] = keyedValue{text: text, value: v}
		}
		if every {
			sort.SliceStable(keyed, func(a, b int) bool {
				return keyed[a].text < keyed[b].text
			})
			sorted := make([]float64, len(keyed))
			for i, k := range keyed {
				sorted[i] = k.value
			}
			out := abstractdomain.KnownValues(sorted, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, out)
			return &out
		}
	}
	// a comparator argument, or an element whose decimal spelling the
	// kernel declines: the class forgets rather than reread a stale
	// order
	HavocEnv(ctx.Aliases, env, trackedName)
	out := silence.Residue()
	return &out
}
