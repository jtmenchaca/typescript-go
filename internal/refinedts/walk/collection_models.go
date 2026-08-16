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
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// collectionKey is the TS source's collectionKey: a key a collection
// can carry: a primitive exact value — SameValueZero on primitives is
// value equality the model decides. An object key compares by
// reference, identity the walk does not carry; a NaN key arrives as
// the NAN kind and is refused with it.
func collectionKey(k abstractdomain.AbstractValue) bool {
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
		UpdateTrackedEnv(ctx.Aliases, env, site.TrackedName, next)
		return next
	}
	symbol, hasIdentity := weakEntryIdentity(ctx, arguments[0])
	if !hasIdentity {
		evaluateExpression(ctx, env, arguments[0])
		if isSet {
			evaluateExpression(ctx, env, arguments[1])
		}
		refresh(nil, false)
		out := silence.Residue()
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
		out := silence.Residue()
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
	out := silence.Residue()
	return &out
}

// ReadCollectionConstruction is readCollectionConstruction in the TS
// source: `new Map([[k, v], …])` / `new Set([v, …])` on the
// default-lib constructor with a LITERAL entry array: the built
// collection, entries evaluated in order, a later duplicate key
// overwriting the earlier (the constructor's own Set/add semantics).
// Bare `new Map()` is the empty complete collection. A spread, an
// object key, or a non-literal argument leaves the construction
// unread.
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
	if len(args) != 1 || !ast.IsArrayLiteralExpression(args[0]) {
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
	for _, element := range args[0].AsArrayLiteralExpression().Elements.Nodes {
		if flavor == abstractdomain.FlavorMap {
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
		if ast.IsSpreadElement(element) {
			// a spread of a KNOWN complete collection pours its members
			// in, in insertion order — Set's own add semantics dedupe
			spread := evaluateExpression(ctx, env, element.AsSpreadElement().Expression)
			if spread.Kind != abstractdomain.KindCollection || !spread.Complete {
				return nil
			}
			for _, member := range spread.Entries {
				if !put(member.Key, abstractdomain.Undef) {
					return nil
				}
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
				out := silence.Residue()
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
			}
		}
		out := silence.Residue()
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
				UpdateTrackedEnv(ctx.Aliases, env, insertName, next)
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
				// or v, and the old value is not named — the entry stays
				// unstated and the answer with it
				refresh(c.Entries, false)
				out := silence.Residue()
				return &out
			}
			// an unreadable key still writes the COLLECTION, not the walk's
			// knowledge of its class — the entries drop, the record stays
			refresh(nil, false)
			out := silence.Residue()
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
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, next)
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
				refresh(next, c.Complete)
				if found {
					out := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				if c.Complete {
					out := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
					return &out
				}
				out := silence.Residue()
				return &out
			}
		}
		// an unreadable key still writes the COLLECTION, not the walk's
		// knowledge of its class: the entries drop, the record stays a
		// collection holding at least zero entries — later reads keep
		// their rows instead of noting an unmodeled call
		dropped := refresh(nil, false)
		if method == "delete" {
			out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})), nil, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, grade), abstractdomain.SetKindTagNone)
			return &out
		}
		if method == "add" || method == "set" {
			return &dropped
		}
		out := silence.Residue()
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
		receiverType := ctx.P.Checker.GetTypeAtLocation(receiverExpression)
		var receiverTypeName string
		if receiverType != nil && receiverType.Symbol() != nil {
			receiverTypeName = receiverType.Symbol().Name
		}
		if receiverTypeName == "Set" || receiverTypeName == "Map" || receiverTypeName == "WeakSet" || receiverTypeName == "WeakMap" {
			if (method == "delete" || method == "has") && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
				return &out
			}
			if method == "add" && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				return &receiver
			}
			// get answers the held value, or undefined for a missing key
			// (sec-map.prototype.get) — absence the walk can then narrow,
			// the spec grade riding on the wrapper itself
			if (receiverTypeName == "Map" || receiverTypeName == "WeakMap") && method == "get" && len(arguments) == 1 {
				evaluateExpression(ctx, env, arguments[0])
				out := abstractdomain.PossiblyUndefined(silence.Residue(), abstractdomain.TrustSpec, true, false)
				return &out
			}
			if method == "clear" && len(arguments) == 0 {
				out := abstractdomain.Undef
				return &out
			}
		}
	}
	return nil
}
