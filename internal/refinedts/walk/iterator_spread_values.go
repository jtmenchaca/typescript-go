// Reading values back OUT of Sets, Maps, iterator views, and a fresh
// generator — the exact items a drain hands over.
//
// The walk already tracks what goes INTO a collection (construction
// entries, add/set writes) and what one for-of element is
// (iteration_elements.go). These are the readers for the way back out,
// each answering an EXACT item list so a drained array keeps its
// length and its positions:
//
//   - collectionSpreadItems: the items iterating a built collection
//     yields, in entry order.
//   - drainedViewItems: the items a values()/keys()/entries() view
//     over an exactly-held receiver drains to.
//   - readSetAlgebraProducers: union / intersection / difference /
//     symmetricDifference over two built Sets, entry by entry from the
//     clauses.
//   - readMapGroupBy: the grouped map Map.groupBy builds, its callback
//     run once per exact item through the inline seam.
//   - readObjectGroupBy: the null-prototype object Object.groupBy
//     builds, readMapGroupBy's own grouping loop rebuilt as OBJECT
//     KEYS instead of Map entries (Object.groupBy coerces the
//     callback's result through ToPropertyKey, not SameValueZero).
//   - generatorFirstNextValue: the first next() of a directly-called
//     generator whose body opens with a plain yield — that yield's
//     value, with no absence arm.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// collectionSpreadItems is the item list iterating a BUILT collection
// hands over, one per entry, in entry order. Spreading a Set runs
// %Set.prototype.values%, which walks [[SetData]] in insertion order
// (sec-set.prototype-%symbol.iterator%, sec-set.prototype.values), so a
// Set contributes its members one apiece; spreading a Map runs entries
// (sec-map.prototype-%symbol.iterator%, sec-map.prototype.entries), so
// a Map contributes its [key, value] pairs. Only a COMPLETE record can
// speak for the whole drain — an incomplete one names some entries but
// not all of them, so no item list covers what the iteration yields —
// and the model's entries are already deduplicated the way the
// collection's own writes deduplicate, so the list's length is the
// collection's size.
func collectionSpreadItems(collection abstractdomain.AbstractValue) ([]abstractdomain.AbstractValue, bool) {
	if collection.Kind != abstractdomain.KindCollection || !collection.Complete {
		return nil, false
	}
	grade := abstractdomain.TrustLevelOf(collection)
	items := make([]abstractdomain.AbstractValue, 0, len(collection.Entries))
	for _, entry := range collection.Entries {
		if collection.CollectionFlavor == abstractdomain.FlavorSet {
			items = append(items, abstractdomain.AtTrustLevel(entry.Key, grade))
			continue
		}
		items = append(items, abstractdomain.KnownList([]abstractdomain.AbstractValue{entry.Key, entry.Value}, grade))
	}
	return items, true
}

// drainedViewItems is the exact item list draining `recv.values()` /
// `recv.keys()` / `recv.entries()` hands over, where the walk HOLDS the
// receiver exactly — an exact array tuple, a list, or a built
// collection. The receiver is read off the environment
// (heldArgumentValue), never evaluated, so no code runs twice; the
// caller has already evaluated the view call itself, and a view on the
// read-only list leaves the receiver's tracking intact.
//
// What each view yields, per its clause:
//
//   - an array's values(): the elements ascending
//     (sec-array.prototype.values, sec-createarrayiterator); keys():
//     the indices 0..n-1 (sec-array.prototype.keys); entries(): the
//     [index, element] pairs (sec-array.prototype.entries).
//   - a Set's values() and keys() are the same function
//     (sec-set.prototype.keys): the members in insertion order;
//     entries() yields [member, member] pairs
//     (sec-set.prototype.entries, ~key+value~).
//   - a Map's keys()/values()/entries() yield the entries' sides in
//     insertion order (sec-map.prototype.keys, sec-map.prototype.values,
//     sec-map.prototype.entries).
func drainedViewItems(ctx *FlowContext, env Env, e *ast.Node) ([]abstractdomain.AbstractValue, bool) {
	if !ast.IsCallExpression(e) {
		return nil, false
	}
	call := e.AsCallExpression()
	if call.Arguments != nil && len(call.Arguments.Nodes) != 0 {
		return nil, false
	}
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	view := access.Name().Text()
	if view != "values" && view != "keys" && view != "entries" {
		return nil, false
	}
	held := heldArgumentValue(ctx, env, access.Expression)
	if held == nil {
		return nil, false
	}
	return viewItemsOfHeld(*held, view)
}

// viewItemsOfHeld is drainedViewItems' value half: the item list one
// of the three views drains an exactly-held receiver to, with no
// syntax involved — what the clauses in drainedViewItems' comment fix
// per receiver shape. (false) where the held value's shape does not
// answer.
func viewItemsOfHeld(held abstractdomain.AbstractValue, view string) ([]abstractdomain.AbstractValue, bool) {
	grade := abstractdomain.TrustLevelOf(held)
	pick := func(index, element abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		switch view {
		case "keys":
			return index
		case "entries":
			return abstractdomain.KnownList([]abstractdomain.AbstractValue{index, element}, grade)
		}
		return element
	}
	switch held.Kind {
	case abstractdomain.KindValues:
		if held.KindTag != abstractdomain.PrimitiveArray {
			return nil, false
		}
		items := make([]abstractdomain.AbstractValue, 0, len(held.Values))
		for i, v := range held.Values {
			items = append(items, pick(
				abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, grade),
				abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade),
			))
		}
		return items, true
	case abstractdomain.KindList:
		items := make([]abstractdomain.AbstractValue, 0, len(held.Items))
		for i, element := range held.Items {
			items = append(items, pick(
				abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, grade),
				element,
			))
		}
		return items, true
	case abstractdomain.KindCollection:
		if !held.Complete {
			return nil, false
		}
		isMap := held.CollectionFlavor == abstractdomain.FlavorMap
		items := make([]abstractdomain.AbstractValue, 0, len(held.Entries))
		for _, entry := range held.Entries {
			key := abstractdomain.AtTrustLevel(entry.Key, grade)
			switch {
			case view == "entries" && isMap:
				items = append(items, abstractdomain.KnownList([]abstractdomain.AbstractValue{key, abstractdomain.AtTrustLevel(entry.Value, grade)}, grade))
			case view == "entries":
				items = append(items, abstractdomain.KnownList([]abstractdomain.AbstractValue{key, key}, grade))
			case view == "values" && isMap:
				items = append(items, abstractdomain.AtTrustLevel(entry.Value, grade))
			default:
				// a Map's keys(); a Set's values() and keys() are both
				// its members
				items = append(items, key)
			}
		}
		return items, true
	}
	return nil, false
}

// setAlgebraProducers is the four Set methods that BUILD a set rather
// than answer a boolean — the ones whose result the drains above then
// read entry by entry.
var setAlgebraProducers = map[string]bool{
	"union":               true,
	"intersection":        true,
	"difference":          true,
	"symmetricDifference": true,
}

// readSetAlgebraProducers is the producer models on a built Set with a
// built-Set argument, each replicated entry by entry from its clause —
// both operands are exact, so the result's entries AND their order are
// the specification's own:
//
//   - union: a copy of this' entries, then other's members not already
//     present, in other's order (sec-set.prototype.union).
//   - intersection: when |this| <= |other|, this' members that are in
//     other, in this' order; otherwise other's members that are in
//     this, in other's order (sec-set.prototype.intersection — the
//     clause's own size split).
//   - difference: this' entries with other's members removed, keeping
//     this' order — both of the clause's branches only blank slots of
//     a copy of this (sec-set.prototype.difference).
//   - symmetricDifference: a copy of this with the shared members
//     blanked, then other's members not in this appended in other's
//     order (sec-set.prototype.symmetricdifference).
//
// The argument is read off the environment (heldArgumentValue), never
// evaluated — when this reader declines, the unmodeled tail evaluates
// the argument itself, so nothing runs twice. Both records must be
// COMPLETE: a producer over a partial record would state entries the
// runtime set need not have, and miss ones it has. None of the four
// writes either operand, so no tracking forgets.
func readSetAlgebraProducers(site MethodCallSite, receiver abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	if !setAlgebraProducers[site.Method] {
		return nil
	}
	if receiver.Kind != abstractdomain.KindCollection || receiver.CollectionFlavor != abstractdomain.FlavorSet || !receiver.Complete {
		return nil
	}
	call := site.E.AsCallExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil
	}
	held := heldArgumentValue(site.Ctx, site.Env, call.Arguments.Nodes[0])
	if held == nil || held.Kind != abstractdomain.KindCollection ||
		held.CollectionFlavor != abstractdomain.FlavorSet || !held.Complete {
		return nil
	}
	other := *held
	entries := setAlgebraEntries(site.Method, receiver.Entries, other.Entries)
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(other))
	out := abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorSet,
		Entries:          entries,
		Complete:         true,
	}
	if grade != abstractdomain.TrustProved {
		out.Grade = grade
	}
	return &out
}

// setAlgebraEntries is the producers' value half: the result set's
// entries, from two exact entry lists, entry by entry from the four
// clauses named on readSetAlgebraProducers. Membership is the model's
// SameValueZero (findCollectionEntry), the same equality the
// collections' own writes deduplicate by.
func setAlgebraEntries(method string, this, other []abstractdomain.CollectionEntry) []abstractdomain.CollectionEntry {
	has := func(entries []abstractdomain.CollectionEntry, key abstractdomain.AbstractValue) bool {
		return findCollectionEntry(entries, key) != nil
	}
	memberOfOther := func(key abstractdomain.AbstractValue) abstractdomain.CollectionEntry {
		return abstractdomain.CollectionEntry{Key: key, Value: abstractdomain.Undef}
	}
	var entries []abstractdomain.CollectionEntry
	switch method {
	case "union":
		entries = append(entries, this...)
		for _, entry := range other {
			if !has(entries, entry.Key) {
				entries = append(entries, memberOfOther(entry.Key))
			}
		}
	case "intersection":
		if len(this) <= len(other) {
			for _, entry := range this {
				if has(other, entry.Key) {
					entries = append(entries, entry)
				}
			}
		} else {
			for _, entry := range other {
				if has(this, entry.Key) {
					entries = append(entries, memberOfOther(entry.Key))
				}
			}
		}
	case "difference":
		for _, entry := range this {
			if !has(other, entry.Key) {
				entries = append(entries, entry)
			}
		}
	case "symmetricDifference":
		for _, entry := range this {
			if !has(other, entry.Key) {
				entries = append(entries, entry)
			}
		}
		for _, entry := range other {
			if !has(this, entry.Key) {
				entries = append(entries, memberOfOther(entry.Key))
			}
		}
	}
	return entries
}

// readMapGroupBy is `Map.groupBy(items, callback)` with the walk
// holding the items exactly and an inline callback. groupBy calls the
// callback once per element in ascending order; each returned key
// names a group, the result map holds one entry per key in
// first-appearance order, and each entry's value is the array of that
// key's elements in items order (sec-map.groupby, via GroupBy's
// per-element key collection). The callback runs through the same
// inline seam Array.from's mapping form uses, so its body's judgments
// report once per element and whatever it writes to outer names
// forgets.
//
// When the items or a key do not read exactly, the answer declines to
// residue rather than nil — the items argument was already evaluated
// here, and a nil would have the unmodeled tail walk it a second
// time. The decline still forgets what the callback body writes: the
// runtime runs the callback per element whether or not this model can
// follow the keys.
func readMapGroupBy(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	if !(method == "groupBy" && ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Map" &&
		resolvesToDefaultLib(ctx, receiverExpression)) {
		return nil
	}
	call := e.AsCallExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return nil
	}
	callbackArgument := call.Arguments.Nodes[1]
	if !ast.IsArrowFunction(callbackArgument) && !ast.IsFunctionExpression(callbackArgument) {
		return nil
	}
	source := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	var items []abstractdomain.AbstractValue
	exact := false
	if source.Kind == abstractdomain.KindList {
		items = source.Items
		exact = true
	} else if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		grade := abstractdomain.TrustLevelOf(source)
		for _, v := range source.Values {
			items = append(items, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade))
		}
		exact = true
	}
	decline := func() *abstractdomain.AbstractValue {
		written := map[string]struct{}{}
		if body := callbackArgument.Body(); body != nil {
			AssignedNames(ctx.P.Checker, body, written)
		}
		for name := range written {
			if _, ok := env.Get(name); ok {
				HavocEnv(ctx.Aliases, env, name)
			}
		}
		NoteUnmodeledCall(ctx, e)
		out := silence.Residue()
		return &out
	}
	if !exact {
		return decline()
	}
	var entries []abstractdomain.CollectionEntry
	var groups [][]abstractdomain.AbstractValue
	for i, item := range items {
		index := abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		key := InlineCallback(ctx, env, callbackArgument, item, LoopAnalyzers{
			AnalyzeStatement:   AnalyzeStatement,
			EvaluateExpression: evaluateExpression,
			IterationElement:   IterationElementOf,
		}, &index)
		// only a primitive exact key names a group the model can place
		// (the collection-key discipline the get/has reads rely on)
		if !collectionKey(key) {
			return decline()
		}
		placed := false
		for gi := range entries {
			if abstractdomain.SameKnown(entries[gi].Key, key) {
				groups[gi] = append(groups[gi], item)
				placed = true
				break
			}
		}
		if !placed {
			entries = append(entries, abstractdomain.CollectionEntry{Key: key})
			groups = append(groups, []abstractdomain.AbstractValue{item})
		}
	}
	for gi := range entries {
		entries[gi].Value = abstractdomain.KnownList(groups[gi], abstractdomain.TrustProved)
	}
	out := abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorMap,
		Entries:          entries,
		Complete:         true,
	}
	return &out
}

// readObjectGroupBy is `Object.groupBy(items, callback)` with the walk
// holding the items exactly and an inline callback. GroupBy(items,
// callback, ~property~) (sec-groupby) calls the callback once per
// element in ascending order and coerces each returned key through
// ToPropertyKey — sec-object.groupby then builds a null-prototype
// object (OrdinaryObjectCreate(*null*)) with one own data property per
// distinct key, CreateDataPropertyOrThrow(obj, group.Key,
// CreateArrayFromList(group.Elements)). The grouping loop mirrors
// readMapGroupBy's exactly; only the RESULT SHAPE differs — an object
// with bareProto (no %Object.prototype% chain, matching
// OrdinaryObjectCreate(null)) instead of a KindCollection Map, and a
// STRING key (collectionKey's own primitive-exact gate, narrowed to
// PrimitiveString since a property key never carries the number/
// boolean primitives collectionKey otherwise admits — ToPropertyKey of
// a non-string, non-symbol callback result still produces a string,
// sec-topropertykey step 3 calling ToString, so a proved-string key is
// exactly what this model can place).
//
// When the items or a key do not read exactly, the answer declines to
// residue rather than nil — the items argument was already evaluated
// here, and a nil would have the unmodeled tail walk it a second
// time. The decline still forgets what the callback body writes: the
// runtime runs the callback per element whether or not this model can
// follow the keys.
func readObjectGroupBy(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	if !(method == "groupBy" && ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Object" &&
		resolvesToDefaultLib(ctx, receiverExpression)) {
		return nil
	}
	call := e.AsCallExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return nil
	}
	callbackArgument := call.Arguments.Nodes[1]
	if !ast.IsArrowFunction(callbackArgument) && !ast.IsFunctionExpression(callbackArgument) {
		return nil
	}
	source := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	var items []abstractdomain.AbstractValue
	exact := false
	if source.Kind == abstractdomain.KindList {
		items = source.Items
		exact = true
	} else if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		grade := abstractdomain.TrustLevelOf(source)
		for _, v := range source.Values {
			items = append(items, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade))
		}
		exact = true
	}
	decline := func() *abstractdomain.AbstractValue {
		written := map[string]struct{}{}
		if body := callbackArgument.Body(); body != nil {
			AssignedNames(ctx.P.Checker, body, written)
		}
		for name := range written {
			if _, ok := env.Get(name); ok {
				HavocEnv(ctx.Aliases, env, name)
			}
		}
		NoteUnmodeledCall(ctx, e)
		out := silence.Residue()
		return &out
	}
	if !exact {
		return decline()
	}
	var keyOrder []string
	groups := map[string][]abstractdomain.AbstractValue{}
	for i, item := range items {
		index := abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		key := InlineCallback(ctx, env, callbackArgument, item, LoopAnalyzers{
			AnalyzeStatement:   AnalyzeStatement,
			EvaluateExpression: evaluateExpression,
			IterationElement:   IterationElementOf,
		}, &index)
		// only a proved-exact STRING key names a group this model can
		// place as an object property — ToPropertyKey's own result sort
		// (sec-topropertykey), narrower than collectionKey's general
		// primitive-exact gate
		if key.Kind != abstractdomain.KindValues || key.KindTag != abstractdomain.PrimitiveString {
			return decline()
		}
		name := stringOf(key.Values)
		if symbolSlotKey(name) {
			return decline()
		}
		if _, seen := groups[name]; !seen {
			keyOrder = append(keyOrder, name)
		}
		groups[name] = append(groups[name], item)
	}
	var keys []abstractdomain.ObjectKey
	for _, name := range keyOrder {
		keys = setObjectKey(keys, name, abstractdomain.KnownList(groups[name], abstractdomain.TrustProved))
	}
	out := abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, true)
	return &out
}

// generatorFirstNextValue is the value the FIRST next() of a
// directly-called generator hands back, where the body's first
// statement is a plain `yield e`. `g().next()` resumes the fresh
// generator from the top of its body (sec-generator.prototype.next
// through sec-generatorresume: the suspended-start context evaluates
// the body from the beginning), and when the first statement is a
// plain yield the body suspends right there — the record is
// CreateIteratorResultObject(value, false) (sec-yield). This call
// cannot be the finishing one, so no absence rides on the value and
// done is exactly false.
//
// An ASYNC generator's suspended-start resume (sec-asyncgeneratorresume,
// RunSuspendedContext) runs the SAME body forward and lands on the SAME
// CreateIteratorResultObject(value, false) once the first statement's
// `yield e` suspends it — AsyncGeneratorResume's own algorithm carries
// no branch that changes what a plain `yield e` hands back, only
// AsyncGeneratorAwaitReturn wraps the settled record in a Promise the
// caller's own `await` unwraps before this reading is ever asked to
// speak. So the claim holds for either generator kind; only the
// SPELLING that gets it there (a promise around the record) differs,
// and that spelling is handled where the record is read, not here.
//
// The yield's expression is read in the body's own fresh scope, with
// nothing of the caller in it — the same discipline the yield join
// takes (generator_element.go generatorYieldElement), including the
// in-flight guard against a generator whose first yield reaches
// itself.
func generatorFirstNextValue(ctx *FlowContext, call *ast.Node) (abstractdomain.AbstractValue, bool) {
	declaration := GeneratorDeclarationOf(ctx, call)
	if declaration == nil {
		return abstractdomain.AbstractValue{}, false
	}
	body := declaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return abstractdomain.AbstractValue{}, false
	}
	statements := body.AsBlock().Statements.Nodes
	if len(statements) == 0 || !ast.IsExpressionStatement(statements[0]) {
		return abstractdomain.AbstractValue{}, false
	}
	expression := statements[0].AsExpressionStatement().Expression
	if !ast.IsYieldExpression(expression) {
		return abstractdomain.AbstractValue{}, false
	}
	yield := expression.AsYieldExpression()
	if yield.AsteriskToken != nil {
		// yield* hands on the inner iterable's first element, which is
		// not the yield expression's own value — not read here
		return abstractdomain.AbstractValue{}, false
	}
	// a bare `yield` hands the caller undefined
	if yield.Expression == nil {
		return abstractdomain.Undef, true
	}
	var walkingKey *ast.Symbol
	if name := declaration.Name(); name != nil {
		walkingKey = ctx.P.Checker.GetSymbolAtLocation(name)
	}
	walking := ctx.Inlining
	if walkingKey != nil {
		if _, inFlight := walking[walkingKey]; inFlight {
			return abstractdomain.AbstractValue{}, false
		}
		if walking == nil {
			walking = map[*ast.Symbol]struct{}{}
		}
		walking[walkingKey] = struct{}{}
		defer delete(walking, walkingKey)
	}
	// the body's own scope, with nothing of the caller in it
	inner := *ctx
	inner.Report = func(d assignability.RefinementDiagnostic) {}
	inner.ReturnSink = nil
	inner.ThrowSink = nil
	inner.Declared = nil
	inner.DifferenceConstraints = nil
	inner.GateAssumptions = nil
	inner.CallableParams = nil
	inner.Inlining = walking
	bodyEnv := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:          inner.P,
		Env:        bodyEnv,
		Parameters: declaration.Parameters(),
	})
	value := evaluateExpression(&inner, bodyEnv, yield.Expression)
	if value.Kind == abstractdomain.KindUnknown {
		return abstractdomain.AbstractValue{}, false
	}
	return value, true
}
