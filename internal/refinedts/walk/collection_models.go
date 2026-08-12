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
	switch newExpr.Expression.Text() {
	case "Map":
		flavor = abstractdomain.FlavorMap
	case "Set":
		flavor = abstractdomain.FlavorSet
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
		if held, ok := env[site.TrackedName]; ok {
			collectionReceiver = held
		}
	}
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
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
		if held, ok := env[site.TrackedName]; ok {
			collectionReceiver = held
		}
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	trackedName := site.TrackedName
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
			dataflowfacts.UpdateTracked(ctx.Aliases, env, trackedName, next)
			return next
		}
		if method == "clear" && len(arguments) == 0 {
			refresh(nil, true)
			out := abstractdomain.Undef
			return &out
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
