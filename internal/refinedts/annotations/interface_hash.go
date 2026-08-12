// Canonical spellings of a file's exported statements — the
// INTERFACE HASH key used to invalidate dependents only when the
// boundary's meaning changed, not when a body did.
//
// Ported 1:1 from annotations/interface_hash.ts EXCEPT
// interfaceHashOf itself: its `contracts: Map<ts.Symbol,
// FunctionContract>` parameter needs FunctionContract, which now
// lives in package walk (walk/flow_context.go) rather than here --
// annotations already imports walk transitively nowhere, but walk
// imports annotations, so interfaceHashOf's own contracts-folding
// body joins walk instead (walk/interface_hash.go), per PORT.md's
// two-way-cycle rule. FormatStated, HashOf, and SortStrings are
// exported here so that file can reuse them rather than duplicate
// this formatting logic (walk-integration-punchlist.md item 5).
// Every other function here (formatSetKey, hashOf, formatStated,
// formatObject) reads only DeclaredRefinement/ObjectAnnotation/
// Annotation, all ported, so they land now; PartsOfSets is the
// shared unsorted-parts builder InterfaceHashOfSets and walk's full
// InterfaceHashOf both fold onto.

package annotations

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// formatSetKey is formatSetKey in the TS source: a set's format key
// for the interface hash. Keys on kernelbridge.CanonicalKeyOfSet, the
// order-FREE spelling (kernel_bridge/question_cache.ts's
// canonicalKeyOf) that treats e.g. `A union B` and `B union A` as the
// same key. The TS source falls back to a WeakMap identity token only
// for oversized sets past the spelling budget (temporal grammars,
// shared module singletons) — process-local object identity is sound
// there because those sets are shared BY REFERENCE. Go's callers hold
// *refinementsets.RefinedSet fields but pass this function a
// dereferenced VALUE (derefSet), so two calls for the "same" logical
// set are two distinct copies with no shared identity to key on —
// the fallback instead keys on kernelbridge.EncodeSet(set), the
// canonical WIRE STRING: deterministic and equal for equal values,
// which is what identity-keying was standing in for here.
func formatSetKey(set refinementsets.RefinedSet) string {
	if canonical := kernelbridge.CanonicalKeyOfSet(set); canonical != nil {
		return *canonical
	}
	return kernelbridge.EncodeSet(set)
}

// HashOf is hashOf in the TS source: two FNV passes with distinct
// initialStates -- 64 collision bits; a collision here would silently
// validate stale facts, so width is cheap insurance. Exported so
// walk's full InterfaceHashOf (the contracts-folding remainder) can
// hash the same way.
func HashOf(text string) string {
	var h1 uint32 = 0x811c9dc5
	var h2 uint32 = 0x9747b28c
	for _, c := range []byte(text) {
		h1 = (h1 ^ uint32(c)) * 0x01000193
		h2 = (h2 ^ uint32(c)) * 0x01000197
	}
	return strconv.FormatUint(uint64(h1), 36) + "." + strconv.FormatUint(uint64(h2), 36)
}

// FormatStated is formatStated in the TS source. Exported so walk's
// full InterfaceHashOf can format a FunctionContract's params/result
// (the `c:` line) the same way this file formats annotations/objects.
func FormatStated(stated *DeclaredRefinement) string {
	switch stated.Kind {
	case DeclaredSet:
		temporalJSON, _ := json.Marshal(stated.Temporal)
		return "s:" + formatSetKey(derefSet(stated.Set)) + ":" + string(temporalJSON)
	case DeclaredObject:
		return "o:" + formatObject(stated.Object)
	case DeclaredVariable:
		return "v:" + stated.Symbol.Name + ":" + formatSetKey(derefSet(stated.Bound)) + ":" +
			strconv.FormatBool(stated.BoundGrounded) + ":" + strconv.Itoa(stated.StarDepth)
	case DeclaredPossiblyUndefined:
		return "m:" + FormatStated(stated.Inner)
	case DeclaredObjectArray:
		hi := "null"
		if !stated.HiUnbounded {
			hi = strconv.FormatFloat(stated.Hi, 'g', -1, 64)
		}
		return "oa:" + formatObject(stated.Object) + ":" + strconv.FormatFloat(stated.Lo, 'g', -1, 64) + ":" + hi
	default:
		return ""
	}
}

// formatObject is formatObject in the TS source.
func formatObject(object *ObjectAnnotation) string {
	parts := make([]string, len(object.Keys))
	for i, key := range object.Keys {
		var valueSpelling string
		switch key.Value.Kind {
		case KeyValueSet:
			valueSpelling = formatSetKey(derefSet(key.Value.Set))
		case KeyValueObject:
			valueSpelling = formatObject(key.Value.Object)
		case KeyValueCollection:
			keySpelling := ""
			if key.Value.Key != nil {
				keySpelling = formatSetKey(*key.Value.Key)
			}
			valueSpelling = key.Value.Flavor + ":" + keySpelling + "->" + formatSetKey(derefSet(key.Value.Value)) + "#" + formatSetKey(derefSet(key.Value.Size))
		case KeyValueReference:
			valueSpelling = "ref:" + key.Value.Target.Name
		}
		parts[i] = key.Name + "#" + formatSetKey(derefSet(key.Count)) + "#" + strconv.FormatBool(key.MayBeAbsent) + "#" + valueSpelling
	}
	return strings.Join(parts, "|")
}

// PartsOfSets builds the UNSORTED, UNHASHED "i:"/"a:"/"g:" lines
// interfaceHashOf folds before its "c:" contract lines and the final
// sort+hash — the shared prefix InterfaceHashOfSets and walk's full
// InterfaceHashOf (the contracts-folding remainder) both build on.
func PartsOfSets(
	annotations map[*ast.Symbol]*Annotation,
	objects map[*ast.Symbol]*ObjectAnnotation,
	importHashes map[string]string,
) []string {
	var parts []string
	// the imports' hashes FOLD IN, so a change propagates through
	// RE-EXPORT chains: a barrel that declares nothing still changes
	// hash when what it re-exports changes -- without this, a schema
	// edit behind `export * from` would leave every dependent's cache
	// validating against an unchanged barrel hash, judging stale sets
	for name, hash := range importHashes {
		parts = append(parts, "i:"+name+"="+hash)
	}
	for symbol, annotation := range annotations {
		temporalJSON, _ := json.Marshal(annotation.Temporal)
		parts = append(parts, "a:"+symbol.Name+"="+formatSetKey(derefSet(annotation.Set))+":"+string(temporalJSON))
	}
	for symbol, object := range objects {
		parts = append(parts, "g:"+symbol.Name+"="+formatObject(object))
	}
	return parts
}

// InterfaceHashOfSets is the BLOCKED-remainder-free portion of
// interfaceHashOf: folds the imports and the annotations/objects
// registries (both fully typed and ported) into the hash. The
// contracts fold (`c:${symbol.name}=...`) is NOT included here --
// a caller that also has contracts uses walk.InterfaceHashOf instead,
// which folds PartsOfSets plus its own "c:" lines before sorting and
// hashing.
func InterfaceHashOfSets(
	annotations map[*ast.Symbol]*Annotation,
	objects map[*ast.Symbol]*ObjectAnnotation,
	importHashes map[string]string,
) string {
	parts := PartsOfSets(annotations, objects, importHashes)
	SortStrings(parts)
	return HashOf(strings.Join(parts, "\n"))
}

// SortStrings is the TS source's plain `.sort()` over strings
// (default lexicographic ordering). Exported so walk's full
// InterfaceHashOf sorts its combined parts list the same way.
func SortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1] > items[j]; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}
