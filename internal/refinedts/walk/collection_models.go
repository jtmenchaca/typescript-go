// from evaluation/collection_models.ts
//
// Map/Set models: construction from literal entries, get/has reads
// on built collections, class-aware writes, and the spec-fixed
// answers on receivers the analysis no longer pins exactly. Split
// from builtin_models.ts per the v2 tree.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// collectionKey is the TS source's collectionKey: a key a collection
// can carry: a primitive exact value — SameValueZero on primitives is
// value equality the model decides. An object key compares by
// reference, identity the walk does not carry.
//
// NaN IS SUCH A KEY. A Map or Set compares keys by SameValueZero
// (sec-map.prototype.set, sec-set.prototype.add), and its Number arm
// opens "If x is NaN and y is NaN, return *true*"
// (sec-numeric-types-number-sameValueZero step 1). So NaN is ONE key
// that a second, syntactically distinct NaN lookup hits — exactly the
// question this predicate answers yes to for every other exact
// primitive. The domain already decides the comparison: SameKnown
// answers true for two KindNaN values, which is the equality
// findCollectionEntry looks entries up by. Refusing the NAN kind here
// left `m.set(NaN, v)` storing no entry, so `m.has(NaN)` could not
// answer the *true* the spec fixes.
func collectionKey(k abstractdomain.AbstractValue) bool {
	if k.Kind == abstractdomain.KindNaN {
		return true
	}
	return k.Kind == abstractdomain.KindValues && k.KindTag != abstractdomain.PrimitiveArray &&
		(k.KindTag == abstractdomain.PrimitiveString || len(k.Values) == 1)
}

// weakEntryIdentity is a WeakMap/WeakSet key's REFERENCE identity — the
// declaring symbol of a plain, never-reassigned local identifier
// (`const key = {}`). collectionKey's value-equality reading is the
// wrong question for a WeakMap key (sec-weakmap.prototype.set requires
// an Object key, and WeakMapData compares SameValue on the KEY
// REFERENCE, never its shape — two `{}` literals are different keys
// even though they read identically as objects); this is deliberately
// NOT routed through SameKnown/CollectionEntry's own KindObject
// comparison, which compares by key SET and would wrongly treat every
// empty object as the same key.
//
// A plain identifier that the file never reassigns anywhere denotes
// the SAME reference at every read between its declaration and any
// later mention (ReassignedNames is file-wide, so this is sound though
// coarser than a scope-local check) — the same soundness bar
// assignments.go's WriteProperty applies to a "local const" object
// write.
func weakEntryIdentity(ctx *FlowContext, key *ast.Node) (*ast.Symbol, bool) {
	if !ast.IsIdentifier(key) {
		return nil, false
	}
	// gated to OBJECT-sorted keys only: an ordinary Map's string/number
	// key read through a plain identifier must keep collectionKey's own
	// value-equality reading untouched — this model answers the
	// reference-identity question a WeakMap key poses, never a primitive
	// one
	if !dataflowfacts.ReferenceTyped(ctx.P.Checker, key) {
		return nil, false
	}
	if _, reassigned := narrowing.ReassignedNames(ast.GetSourceFileOfNode(key))[key.Text()]; reassigned {
		return nil, false
	}
	symbol := symbolAt(ctx.P.Checker, key)
	if symbol == nil {
		return nil, false
	}
	return symbol, true
}

// weakEntrySymbol wraps an identity symbol as the CollectionEntry.Key
// this file's own set-then-get reads compare — Kind stays KindObject
// (an ordinary, if unusual, object-sorted value) so nothing outside
// sameWeakEntry ever reads .Symbol off it; SameKnown's own KindObject
// case (key-set comparison) is untouched.
func weakEntrySymbol(symbol *ast.Symbol) abstractdomain.AbstractValue {
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindObject, Complete: true, Symbol: symbol}
}

// sameWeakEntry compares two weakEntrySymbol markers by the identity
// they carry — the declaring symbol, never the object's shape.
func sameWeakEntry(a, b abstractdomain.AbstractValue) bool {
	return a.Kind == abstractdomain.KindObject && b.Kind == abstractdomain.KindObject &&
		a.Symbol != nil && a.Symbol == b.Symbol
}

// findWeakEntry finds a weakEntrySymbol-keyed entry by identity, or
// nil.
func findWeakEntry(entries []abstractdomain.CollectionEntry, key abstractdomain.AbstractValue) *abstractdomain.CollectionEntry {
	for i := range entries {
		if sameWeakEntry(entries[i].Key, key) {
			return &entries[i]
		}
	}
	return nil
}

// readWeakEntrySet is `.set(key, value)` / `.add(key)` on a tracked
// WeakMap/WeakSet whose key is an identity weakEntryIdentity can name
// — the entry list keyed by symbol identity rather than
// collectionKey's value equality (weakEntryIdentity's own doc). A key
// this reader cannot identify still writes the collection: the
// entries drop and a later get/has on ANY key answers honestly rather
// than keeping a stale record.
func readWeakEntrySet(site MethodCallSite, collectionReceiver abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	ctx, env, e, method := site.Ctx, site.Env, site.E, site.Method
	if !site.HasTrackedName || collectionReceiver.Kind != abstractdomain.KindCollection {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	isSet := collectionReceiver.CollectionFlavor == abstractdomain.FlavorMap && method == "set" && len(arguments) == 2
	isAdd := collectionReceiver.CollectionFlavor == abstractdomain.FlavorSet && method == "add" && len(arguments) == 1
	if !isSet && !isAdd {
		return nil
	}
	// an ordinary Map/Set's PRIMITIVE key stays with collectionKey's own
	// value-equality path below — this reader answers only the
	// reference-identity question an OBJECT key poses, checked before
	// any evaluation or write so a plain `.set("key", 40)` call is left
	// completely untouched
	if !dataflowfacts.ReferenceTyped(ctx.P.Checker, arguments[0]) {
		return nil
	}
	c := collectionReceiver
	grade := abstractdomain.TrustLevelOf(c)
	refresh := func(entries []abstractdomain.CollectionEntry, complete bool) abstractdomain.AbstractValue {
		next := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: c.CollectionFlavor, Entries: entries, Complete: complete}
		if grade != abstractdomain.TrustProved {
			next.Grade = grade
		}
		// THE LAST-TOUCH SITE SEAM: the call itself — `.set(key, value)` /
		// `.add(key)` — is the mutating construct behind this write.
		if derivation.Active() {
			closeSite := derivation.TouchSite(derivation.Construct(e), derivation.Range(e))
			UpdateTrackedEnv(ctx.Aliases, env, site.TrackedName, next)
			closeSite()
		} else {
			UpdateTrackedEnv(ctx.Aliases, env, site.TrackedName, next)
		}
		return next
	}
	symbol, hasIdentity := weakEntryIdentity(ctx, arguments[0])
	if !hasIdentity {
		evaluateExpression(ctx, env, arguments[0])
		if isSet {
			evaluateExpression(ctx, env, arguments[1])
		}
		refresh(nil, false)
		out := silence.ResidueOf("the WeakMap/WeakSet key isn't a plain, never-reassigned identifier, " +
			"so its reference identity can't be tracked")
		return &out
	}
	key := weakEntrySymbol(symbol)
	var value abstractdomain.AbstractValue
	if isSet {
		value = evaluateExpression(ctx, env, arguments[1])
	} else {
		value = abstractdomain.Undef
	}
	var next []abstractdomain.CollectionEntry
	if held := findWeakEntry(c.Entries, key); held != nil {
		next = make([]abstractdomain.CollectionEntry, len(c.Entries))
		for i, entry := range c.Entries {
			if sameWeakEntry(entry.Key, key) {
				next[i] = abstractdomain.CollectionEntry{Key: key, Value: value}
			} else {
				next[i] = entry
			}
		}
	} else {
		next = append(append([]abstractdomain.CollectionEntry{}, c.Entries...), abstractdomain.CollectionEntry{Key: key, Value: value})
	}
	out := refresh(next, c.Complete)
	return &out
}

// readWeakEntryGet is `.get(key)` / `.has(key)` on a tracked WeakMap/
// WeakSet whose key is an identity weakEntryIdentity can name — a
// found entry answers even on an incomplete record (a call the walk
// could not follow may have set other keys, but THIS key was set
// right here); a miss stays honestly unknown, since an untraced call
// could have set this exact key too.
func readWeakEntryGet(site MethodCallSite, collectionReceiver abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	ctx, e, method := site.Ctx, site.E, site.Method
	if collectionReceiver.Kind != abstractdomain.KindCollection {
		return nil
	}
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if argCount != 1 || (method != "get" && method != "has") {
		return nil
	}
	symbol, hasIdentity := weakEntryIdentity(ctx, call.Arguments.Nodes[0])
	if !hasIdentity {
		return nil
	}
	key := weakEntrySymbol(symbol)
	held := findWeakEntry(collectionReceiver.Entries, key)
	grade := abstractdomain.TrustLevelOf(collectionReceiver)
	if method == "has" {
		if held != nil {
			out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
			return &out
		}
		if collectionReceiver.Complete {
			out := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
			return &out
		}
		out := silence.ResidueOf("this key was never seen set, but the record isn't complete — " +
			"an untracked call could have set this exact key too")
		return &out
	}
	if collectionReceiver.CollectionFlavor != abstractdomain.FlavorMap {
		return nil
	}
	if held != nil {
		out := abstractdomain.AtTrustLevel(held.Value, grade)
		return &out
	}
	// a key this walk never saw .set — provably absent exactly when the
	// record is COMPLETE (no untracked call may have set it since; a
	// call the walk cannot follow already drops Complete through
	// readWeakEntrySet's own unreadable-key fallback, or through
	// HavocEnv/ForgetThrough elsewhere), the same reading an ordinary
	// Map gives a missing key on a complete record
	if collectionReceiver.Complete {
		out := abstractdomain.AtTrustLevel(abstractdomain.Undef, grade)
		return &out
	}
	out := silence.ResidueOf("this key was never seen set, but the record isn't complete — " +
		"an untracked call could have set this exact key too")
	return &out
}

// ReadCollectionConstruction is readCollectionConstruction in the TS
// source: `new Map([[k, v], …])` / `new Set([v, …])` on the
// default-lib constructor: the built collection, entries taken in
// order, a later duplicate key overwriting the earlier (the
// constructor's own set/add semantics, per AddEntriesFromIterable's
// own per-item loop). Bare `new Map()` is the empty complete
// collection.
//
// The entry list is read from whichever side states it. A LITERAL
// entry array states its pairs in syntax. A SPREAD element inside one
// states them through the spread value — a built collection's own
// iteration order, or an exactly-held list's items. And a NON-LITERAL
// argument that evaluates to an exactly-held list states them
// outright, which is the `new Map(Object.entries(o))` shape: the
// constructor iterates what it is handed, so a list the walk holds
// exactly is an entry set it can carry through.
//
// An object key, or any item the walk cannot read as the shape the
// constructor requires, leaves the construction unread.
func ReadCollectionConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) {
		return nil
	}
	var flavor abstractdomain.Flavor
	hasFlavor := true
	weak := false
	switch newExpr.Expression.Text() {
	case "Map":
		flavor = abstractdomain.FlavorMap
	case "Set":
		flavor = abstractdomain.FlavorSet
	case "WeakMap":
		flavor, weak = abstractdomain.FlavorMap, true
	case "WeakSet":
		flavor, weak = abstractdomain.FlavorSet, true
	default:
		hasFlavor = false
	}
	if !hasFlavor || !resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	var args []*ast.Node
	if newExpr.Arguments != nil {
		args = newExpr.Arguments.Nodes
	}
	if len(args) == 0 {
		out := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: flavor, Entries: nil, Complete: true}
		return &out
	}
	// a WeakMap/WeakSet's constructor argument is an iterable of
	// object-keyed entries this walk cannot compare by identity at
	// construction time (the literal-entries route below is
	// collectionKey's value-equality reading, wrong for a reference
	// key) — only the bare, empty constructor is read; set-then-get
	// identity is tracked from `.set()` onward instead
	// (readWeakEntrySet/readWeakEntryGet).
	if weak {
		return nil
	}
	if len(args) != 1 {
		return nil
	}
	var entries []abstractdomain.CollectionEntry
	put := func(key, value abstractdomain.AbstractValue) bool {
		if !collectionKey(key) {
			return false
		}
		for i, held := range entries {
			if abstractdomain.SameKnown(held.Key, key) {
				entries[i] = abstractdomain.CollectionEntry{Key: key, Value: value}
				return true
			}
		}
		entries = append(entries, abstractdomain.CollectionEntry{Key: key, Value: value})
		return true
	}
	// putIterated pours ONE iterated item into the fold, the way the
	// constructor's own loop does. AddEntriesFromIterable
	// (sec-map-iterable) requires each item of a Map's iterable to be an
	// Object and reads its "0" and "1" — exactly a two-item list here —
	// then calls set(k, v); a Set's constructor calls add(item) on the
	// item itself (sec-set-iterable). An item the walk cannot read as
	// that shape puts nothing and stops the read.
	putIterated := func(item abstractdomain.AbstractValue) bool {
		if flavor != abstractdomain.FlavorMap {
			return put(item, abstractdomain.Undef)
		}
		if item.Kind != abstractdomain.KindList || len(item.Items) != 2 {
			return false
		}
		return put(item.Items[0], item.Items[1])
	}
	// THE WHOLE ARGUMENT AS A VALUE. `new Map(entries)` where entries is
	// a binding — not an array literal written at the call — is the same
	// construction: AddEntriesFromIterable walks whatever the argument
	// iterates, and an exactly-held list states every item it yields, in
	// order. So a complete list argument is read here, which is how a
	// Map built from Object.entries(o) or from a drained view carries the
	// entry set those produced rather than losing it at the constructor.
	//
	// WHAT IS READ, AND WHY NOT EVERYTHING. The argument's value is taken
	// from what the walk already HOLDS for a name, and evaluated only for
	// a call this tree already knows writes nothing it is handed
	// (ReadOnlyStaticCalls — Object.entries and its kin). Anything richer
	// is left alone: this reader runs inside a chain whose later readers
	// evaluate the same construction, so evaluating an effectful argument
	// here would run its code a second time.
	if !ast.IsArrayLiteralExpression(args[0]) {
		held := constructorIterableValue(ctx, env, args[0])
		if held == nil || held.Kind != abstractdomain.KindList {
			return nil
		}
		for _, item := range held.Items {
			if !putIterated(item) {
				return nil
			}
		}
		out := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: flavor, Entries: entries, Complete: true}
		return &out
	}
	for _, element := range args[0].AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) {
			// a spread pours the spread value's own items in, in order.
			// A built collection states them through collectionSpreadItems
			// — a Set contributes its members one apiece, a Map its
			// [key, value] pairs (sec-map.prototype-%symbol.iterator%) —
			// and an exactly-held list states them outright. Each item
			// then lands through the constructor's own per-item step, so a
			// later duplicate key overwrites the earlier exactly as a
			// written-out pair would. The spread value is read under
			// constructorIterableValue's own no-replay rule, so a spread of
			// an effectful expression declines rather than running it here.
			spread := constructorIterableValue(ctx, env, element.AsSpreadElement().Expression)
			if spread == nil {
				return nil
			}
			items, spreadable := collectionSpreadItems(*spread)
			if !spreadable {
				if spread.Kind != abstractdomain.KindList {
					return nil
				}
				items = spread.Items
			}
			for _, item := range items {
				if !putIterated(item) {
					return nil
				}
			}
			continue
		}
		if flavor == abstractdomain.FlavorMap {
			// a pair written out at the call site reads its two halves
			// straight from the syntax, so a key or value the walk can
			// evaluate needs no list value standing behind the pair
			if !ast.IsArrayLiteralExpression(element) || len(element.AsArrayLiteralExpression().Elements.Nodes) != 2 {
				return nil
			}
			pair := element.AsArrayLiteralExpression().Elements.Nodes
			key := evaluateExpression(ctx, env, pair[0])
			value := evaluateExpression(ctx, env, pair[1])
			if !put(key, value) {
				return nil
			}
			continue
		}
		if !put(evaluateExpression(ctx, env, element), abstractdomain.Undef) {
			return nil
		}
	}
	out := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: flavor, Entries: entries, Complete: true}
	return &out
}

// constructorIterableValue is the value of a Map/Set constructor's
// whole-argument iterable, where reading it cannot replay an effect.
//
// A NAME (or a property chain off one) is read from what the walk
// already holds — no code runs at all. A CALL is evaluated only when
// its callee is one of the statics this tree already recognizes as
// reading its arguments and writing nothing (ReadOnlyStaticCalls:
// Object.entries and its kin), which is the `new Map(Object.entries(o))`
// shape. Nil for anything else, so an effectful argument is never run
// here — the same discipline heldArgumentValue's own doc states, with
// the read-only statics allowed through because they provably have
// nothing to replay.
func constructorIterableValue(ctx *FlowContext, env Env, argument *ast.Node) *abstractdomain.AbstractValue {
	if held := heldArgumentValue(ctx, env, argument); held != nil {
		return held
	}
	if !ast.IsCallExpression(argument) {
		return nil
	}
	call := argument.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	access := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || !resolvesToDefaultLib(ctx, access.Expression) {
		return nil
	}
	methods, known := dataflowfacts.ReadOnlyStaticCalls[access.Expression.Text()]
	if !known {
		return nil
	}
	if _, readOnly := methods[access.Name().Text()]; !readOnly {
		return nil
	}
	value := evaluateExpression(ctx, env, argument)
	return &value
}

// findCollectionEntry finds an entry by SameValueZero-equal key
// (sameKnown in the TS source), or (nil) when none is found.
func findCollectionEntry(entries []abstractdomain.CollectionEntry, key abstractdomain.AbstractValue) *abstractdomain.CollectionEntry {
	for i := range entries {
		if abstractdomain.SameKnown(entries[i].Key, key) {
			return &entries[i]
		}
	}
	return nil
}

// readCollectionGetHas is readCollectionGetHas in the TS source:
// `.get`/`.has` with a primitive exact key on a built Map or Set.
func readCollectionGetHas(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	collectionReceiver := receiver
	if site.HasTrackedName {
		if held, ok := env.Get(site.TrackedName); ok {
			collectionReceiver = held
		}
	}
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	// a WeakMap/WeakSet's OBJECT key reads by reference identity, never
	// by collectionKey's value equality (weakEntryIdentity's own doc) —
	// checked first so the generic value-key path below never runs on
	// an object argument it would only residue away
	if weakAnswer := readWeakEntryGet(site, collectionReceiver); weakAnswer != nil {
		return weakAnswer
	}
	// a collection read: `.get`/`.has` with a primitive exact key on a
	// built Map or Set — a found entry answers even on an incomplete
	// record (it was put there); a miss answers only when every entry
	// is named
	if collectionReceiver.Kind == abstractdomain.KindCollection && argCount == 1 && (method == "get" || method == "has") {
		key := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
		if collectionKey(key) {
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(collectionReceiver), abstractdomain.TrustLevelOf(key))
			held := findCollectionEntry(collectionReceiver.Entries, key)
			if method == "has" {
				if held != nil {
					out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				if collectionReceiver.Complete {
					out := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				// a standing guard already established this exact key
				// present — the entry is in [[MapData]], so this read is
				// the *true* sec-map.prototype.has returns for one, even
				// though THIS walk never saw the entry set on the record
				if KeyPresenceEstablishedForHas(env, e) {
					out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, grade))
					return &out
				}
				// neither entry nor fact pins WHICH boolean — but `has`
				// answers a Boolean on every run (sec-map.prototype.has
				// returns *true* or *false*, nothing else), so the sort's
				// whole ground {0, 1} is the determined answer, not a
				// residue: an incomplete record blocks exactness only
				out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, grade))
				return &out
			}
			if collectionReceiver.CollectionFlavor == abstractdomain.FlavorMap {
				if held != nil {
					out := abstractdomain.AtTrustLevel(held.Value, grade)
					return &out
				}
				if collectionReceiver.Complete {
					out := abstractdomain.AtTrustLevel(abstractdomain.Undef, grade)
					return &out
				}
				// a `.get` miss on a Map the walk cannot prove complete: an
				// untracked call could have set this exact key too, the same
				// incomplete-record reason the `.has` arm above and
				// readWeakEntryGet's own `.get` arm already name
				out := silence.ResidueOf("this key was never seen set, but the record isn't complete — " +
					"a miss only answers when every entry is named")
				return &out
			}
		}
		// a SYMBOLIC key the value-equality reading cannot compare can
		// still be established present — by a held `has(k)` guard or a
		// completed `set(k, v)` — under the fact's own spelling
		// discipline, which compares the key's spelling rather than its
		// value (key_presence_facts.go). Consulted before the residue
		// below; `get` needs no arm here because its residue answers
		// KindUnknown and evaluate_call_expression's unknown path asks
		// the same fact before wearing the resolved signature.
		if method == "has" && KeyPresenceEstablishedForHas(env, e) {
			out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean,
				abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(collectionReceiver)))
			return &out
		}
		// a symbolic key with no standing fact still gets `has`'s SORT:
		// a Boolean comes back on every run, so {0, 1} is determined
		// even though which one is not — the same reading the exact-key
		// incomplete-miss arm above takes. `get` keeps the residue: its
		// value is the held V or undefined, and the annotation readers
		// on the caller's side reconstruct that pair from the stated
		// signature.
		if method == "has" {
			out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean,
				abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(collectionReceiver)))
			return &out
		}
		out := silence.ResidueOf("the key isn't one primitive exact value, so it isn't a key " +
			"collectionKey's value-equality reading can compare")
		return &out
	}
	return nil
}

// readCollectionMethods is readCollectionMethods in the TS source:
// the collection writes on a tracked name — set/add/delete/clear —
// and the spec-fixed methods on a Set or Map the analysis no longer
// pins exactly.
func readCollectionMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, receiver, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Receiver, site.Method
	collectionReceiver := receiver
	if site.HasTrackedName {
		if held, ok := env.Get(site.TrackedName); ok {
			collectionReceiver = held
		}
	}
	// the set-algebra producers build a NEW set from two built ones,
	// and Map.groupBy builds a grouped map from exact items — answered
	// before the write rows because neither writes its operands
	// (iterator_spread_values.go)
	if answered := readSetAlgebraProducers(site, collectionReceiver); answered != nil {
		return answered
	}
	if answered := readMapGroupBy(site); answered != nil {
		return answered
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	trackedName := site.TrackedName
	// `m.getOrInsert(k, v)` — Map.prototype.getOrInsert
	// (lib.esnext.collection.d.ts): the held value where k is present,
	// else v inserted under k and answered. The receiver may stand
	// behind parens and casts — `(m as unknown as { getOrInsert… })` —
	// which erase at runtime (Unwrapped, tracked_bindings.go), so the
	// call still reads and writes the tracked collection at the
	// collection's own grade. getOrInsertComputed stays unmodeled: its
	// second argument is a callback whose run needs the callback-summary
	// machinery, not a plain value.
	if method == "getOrInsert" && len(arguments) == 2 {
		insertName, hasInsertName := trackedName, site.HasTrackedName
		held := collectionReceiver
		if !hasInsertName {
			if root := Unwrapped(site.ReceiverExpression); root != nil && ast.IsIdentifier(root) {
				if tracked, ok := env.Get(root.Text()); ok && tracked.Kind == abstractdomain.KindCollection {
					insertName, hasInsertName = root.Text(), true
					held = tracked
				}
			}
		}
		if hasInsertName && held.Kind == abstractdomain.KindCollection &&
			held.CollectionFlavor == abstractdomain.FlavorMap {
			c := held
			grade := abstractdomain.TrustLevelOf(c)
			key := evaluateExpression(ctx, env, arguments[0])
			value := evaluateExpression(ctx, env, arguments[1])
			refresh := func(entries []abstractdomain.CollectionEntry, complete bool) {
				next := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: c.CollectionFlavor, Entries: entries, Complete: complete}
				if grade != abstractdomain.TrustProved {
					next.Grade = grade
				}
				// THE LAST-TOUCH SITE SEAM: the call `m.getOrInsert(k, v)`
				// itself is the mutating construct behind this write.
				if derivation.Active() {
					closeSite := derivation.TouchSite(derivation.Construct(e), derivation.Range(e))
					UpdateTrackedEnv(ctx.Aliases, env, insertName, next)
					closeSite()
				} else {
					UpdateTrackedEnv(ctx.Aliases, env, insertName, next)
				}
			}
			if collectionKey(key) {
				grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(key))
				if present := findCollectionEntry(c.Entries, key); present != nil {
					// the present value wins; the map is unchanged
					out := abstractdomain.AtTrustLevel(present.Value, grade)
					return &out
				}
				if c.Complete {
					// the key is provably absent: v is inserted and answered
					refresh(append(append([]abstractdomain.CollectionEntry{}, c.Entries...), abstractdomain.CollectionEntry{Key: key, Value: value}), true)
					out := abstractdomain.AtTrustLevel(value, grade)
					return &out
				}
				// maybe-present: after the call the key holds its OLD value
				// or v. The receiver's own stated `Map<K, V>` covers both
				// — the old value entered wearing V at its own write, and
				// v is judged against V by the same reader — so the answer
				// is V's stated set joined with v where V is spelled, and
				// the key is present afterward either way
				// (sec: the model's own header; RecordKeyPresenceAfterWrite
				// runs after refresh's sweep). Only a receiver spelling no
				// V keeps the residue.
				refresh(c.Entries, false)
				RecordKeyPresenceAfterWrite(env, receiverExpression, arguments[0])
				if statedValue := MapStatedValueOfGetOrInsert(ctx, receiverExpression, value, arguments[1]); statedValue != nil {
					out := abstractdomain.AtTrustLevel(abstractdomain.JoinKnown(*statedValue, value), grade)
					return &out
				}
				out := silence.ResidueOf("getOrInsert's key may already be present, but the old value " +
					"isn't named, so the entry stays unstated")
				return &out
			}
			// an unreadable key still writes the COLLECTION, not the walk's
			// knowledge of its class — the entries drop, the record stays.
			// The answered VALUE follows the same stated-V rule as the
			// maybe-present arm above, and the key is present afterward
			refresh(nil, false)
			RecordKeyPresenceAfterWrite(env, receiverExpression, arguments[0])
			if statedValue := MapStatedValueOfGetOrInsert(ctx, receiverExpression, value, arguments[1]); statedValue != nil {
				out := abstractdomain.AtTrustLevel(abstractdomain.JoinKnown(*statedValue, value), grade)
				return &out
			}
			out := silence.ResidueOf("getOrInsert's key isn't one primitive exact value, so the " +
				"collection writes but the inserted-or-held value isn't named")
			return &out
		}
	}
	// a collection WRITE on a tracked name updates the record in
	// place, class-aware — the same discipline array pushes use; an
	// unreadable key forgets instead
	if collectionReceiver.Kind == abstractdomain.KindCollection && site.HasTrackedName &&
		(method == "set" || method == "add" || method == "delete" || method == "clear") {
		c := collectionReceiver
		grade := abstractdomain.TrustLevelOf(c)
		refresh := func(entries []abstractdomain.CollectionEntry, complete bool) abstractdomain.AbstractValue {
			next := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: c.CollectionFlavor, Entries: entries, Complete: complete}
			if grade != abstractdomain.TrustProved {
				next.Grade = grade
			}
			// THE LAST-TOUCH SITE SEAM: the call — `.set`/`.add`/`.delete`/
			// `.clear` — is the mutating construct behind this write.
			if derivation.Active() {
				closeSite := derivation.TouchSite(derivation.Construct(e), derivation.Range(e))
				UpdateTrackedEnv(ctx.Aliases, env, trackedName, next)
				closeSite()
			} else {
				UpdateTrackedEnv(ctx.Aliases, env, trackedName, next)
			}
			return next
		}
		if method == "clear" && len(arguments) == 0 {
			refresh(nil, true)
			out := abstractdomain.Undef
			return &out
		}
		// a WeakMap/WeakSet's OBJECT key writes by reference identity,
		// never by collectionKey's value equality (weakEntryIdentity's
		// own doc) — checked first so an object argument never falls
		// into the value-key path below, which would only drop the
		// collection's entries for a key it cannot compare
		if weakAnswer := readWeakEntrySet(site, collectionReceiver); weakAnswer != nil {
			return weakAnswer
		}
		if c.CollectionFlavor == abstractdomain.FlavorMap && method == "set" && len(arguments) == 2 {
			key := evaluateExpression(ctx, env, arguments[0])
			value := evaluateExpression(ctx, env, arguments[1])
			// the receiver's stated `Map<K, V>` makes V an invariant on
			// every stored value — judged here, at the write, the same
			// way a declared binding's own set is (CheckMapValueWrite's
			// own doc on why the read side depends on it)
			CheckMapValueWrite(ctx, e, value, arguments[1])
			if collectionKey(key) {
				var next []abstractdomain.CollectionEntry
				if held := findCollectionEntry(c.Entries, key); held != nil {
					next = make([]abstractdomain.CollectionEntry, len(c.Entries))
					for i, entry := range c.Entries {
						if abstractdomain.SameKnown(entry.Key, key) {
							next[i] = abstractdomain.CollectionEntry{Key: key, Value: value}
						} else {
							next[i] = entry
						}
					}
				} else {
					next = append(append([]abstractdomain.CollectionEntry{}, c.Entries...), abstractdomain.CollectionEntry{Key: key, Value: value})
				}
				out := refresh(next, c.Complete)
				return &out
			}
		}
		if c.CollectionFlavor == abstractdomain.FlavorSet && method == "add" && len(arguments) == 1 {
			key := evaluateExpression(ctx, env, arguments[0])
			if collectionKey(key) {
				present := findCollectionEntry(c.Entries, key) != nil
				var next []abstractdomain.CollectionEntry
				if present {
					next = c.Entries
				} else {
					next = append(append([]abstractdomain.CollectionEntry{}, c.Entries...), abstractdomain.CollectionEntry{Key: key, Value: abstractdomain.Undef})
				}
				out := refresh(next, c.Complete)
				return &out
			}
		}
		if method == "delete" && len(arguments) == 1 {
			key := evaluateExpression(ctx, env, arguments[0])
			if collectionKey(key) {
				found := false
				next := make([]abstractdomain.CollectionEntry, 0, len(c.Entries))
				for _, entry := range c.Entries {
					if abstractdomain.SameKnown(entry.Key, key) {
						found = true
						continue
					}
					next = append(next, entry)
				}
				// refresh's UpdateTrackedEnv sweeps every dotted entry
				// under the root, surviving presence facts included — a
				// literal-keyed fact different from a literal deleted key
				// names an entry this delete cannot touch
				// (KeyPresenceSurvivesRemoval), so those are collected
				// first and written back after the sweep
				survivors := keyPresenceSurvivorsHeld(env, receiverExpression, arguments[0])
				refresh(next, c.Complete)
				for _, place := range survivors {
					env.Set(place, keyPresenceEstablished)
				}
				if found {
					out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				if c.Complete {
					out := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				// which boolean is unpinned on an incomplete record, but
				// `delete` answers a Boolean on every run
				// (sec-map.prototype.delete returns *true* or *false*), so
				// the sort's whole ground {0, 1} is the determined answer
				out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, grade))
				return &out
			}
		}
		// an unreadable key still writes the COLLECTION, not the walk's
		// knowledge of its class: the entries drop, the record stays a
		// collection holding at least zero entries — later reads keep
		// their rows instead of noting an unmodeled call
		dropped := refresh(nil, false)
		if method == "delete" {
			// delete answers a BOOLEAN (sec-map.prototype.delete), and a
			// boolean is KindValues{0,1} wearing PrimitiveBoolean — a
			// KindSet oneOf{0,1} is indistinguishable from the numbers 0
			// and 1, so every later reader asking "is this a boolean?"
			// says no and Number(...) of it models the general conversion,
			// NaN included.
			out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, grade))
			return &out
		}
		if method == "add" || method == "set" {
			// the record's entries dropped, but the KEY handed to set/add
			// is present afterward whatever the other entries are — the
			// same fact a held `has(k)` guard writes, written after
			// refresh's own sweep (RecordKeyPresenceAfterWrite's doc)
			if len(arguments) >= 1 {
				RecordKeyPresenceAfterWrite(env, receiverExpression, arguments[0])
			}
			return &dropped
		}
		out := silence.ResidueOf("clear/set/add/delete matched with an argument count the model " +
			"doesn't recognize, so no write shape applies")
		return &out
	}
	// a Set or Map method on a receiver the walk no longer pins exactly
	// (a loop rejoined it): the results the spec fixes still answer —
	// delete and has return booleans (sec-set.prototype.delete,
	// sec-set.prototype.has, sec-map.prototype.delete,
	// sec-map.prototype.has), add returns the receiver itself
	// (sec-set.prototype.add), clear undefined — none of it is an
	// unmodeled gap
	{
		receiverType := typereading.TypeAtLocation(ctx.P.Checker, receiverExpression)
		var receiverTypeName string
		if receiverType != nil && receiverType.Symbol() != nil {
			receiverTypeName = receiverType.Symbol().Name
		}
		if receiverTypeName == "Set" || receiverTypeName == "Map" || receiverTypeName == "WeakSet" || receiverTypeName == "WeakMap" {
			if (method == "delete" || method == "has") && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				// a delete REMOVES an entry, so the standing presence
				// facts it can invalidate sweep — all but a literal-keyed
				// fact provably different from a literal deleted key
				// (DropKeyPresenceOnRemoval's doc); the tracked arm gets
				// its sweep from UpdateTrackedEnv and restores the same
				// survivors after it
				if method == "delete" {
					DropKeyPresenceOnRemoval(env, receiverExpression, arguments[0])
				}
				// a standing guard for this exact key answers the read
				// exactly, even on a receiver whose entries the walk
				// never watched — the fact is about [[MapData]], not
				// about the walk's record of it (key_presence.go)
				if method == "has" && KeyPresenceEstablishedForHas(env, e) {
					out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
					return &out
				}
				// both answer a BOOLEAN, which the domain spells as
				// KindValues{0,1} tagged PrimitiveBoolean; an untagged
				// KindSet oneOf{0,1} reads back as the numbers 0 and 1 and
				// loses the sort.
				out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
				return &out
			}
			if method == "add" && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				// after the add this key IS present
				// (sec-set.prototype.add appends when no entry matches,
				// returns with it present either way) — the fact a later
				// `has(k)` reads (RecordKeyPresenceAfterWrite's doc)
				RecordKeyPresenceAfterWrite(env, receiverExpression, arguments[0])
				return &receiver
			}
			// `m.set(k, v)` on a receiver the walk holds no entries for.
			// The RECORD is not pinned, so nothing is claimed about what
			// the map holds afterward — but the receiver's stated
			// `Map<K, V>` still makes V an invariant on the value going
			// in, and that obligation does not depend on knowing the
			// entries. Judging it here is what makes a declared map
			// parameter a real sink; without this arm the call fell
			// through unclaimed and the written value was never judged
			// at all. `set` answers the receiver itself
			// (sec-map.prototype.set's own `return M`).
			if receiverTypeName == "Map" && method == "set" && len(arguments) == 2 {
				evaluateExpression(ctx, env, arguments[0])
				written := evaluateExpression(ctx, env, arguments[1])
				CheckMapValueWrite(ctx, e, written, arguments[1])
				// after the set this key IS present (sec-map.prototype.set
				// appends a new record or writes the matching one) — the
				// fact a later `get(k)`/`has(k)` reads to drop the absence
				// its host signature states (RecordKeyPresenceAfterWrite's
				// doc). Other keys' standing facts survive: a set removes
				// no entry.
				RecordKeyPresenceAfterWrite(env, receiverExpression, arguments[0])
				return &receiver
			}
			// get answers the held value, or undefined for a missing key
			// (sec-map.prototype.get) — absence the walk can then narrow,
			// the spec grade riding on the wrapper itself
			if (receiverTypeName == "Map" || receiverTypeName == "WeakMap") && method == "get" && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				out := abstractdomain.PossiblyUndefined(silence.ResidueOf("the receiver's entries aren't pinned "+
					"(a loop rejoined it), so the held value at this key isn't named"), abstractdomain.TrustSpec, true, false)
				return &out
			}
			if method == "clear" && len(arguments) == 0 {
				// clear removes EVERY entry, so every standing presence
				// fact rooted at this receiver sweeps with them — the nil
				// removed key keeps nothing
				DropKeyPresenceOnRemoval(env, receiverExpression, nil)
				out := abstractdomain.Undef
				return &out
			}
		}
	}
	return nil
}
