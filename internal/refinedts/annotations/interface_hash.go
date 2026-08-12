// Canonical spellings of a file's exported statements — the
// INTERFACE HASH key used to invalidate dependents only when the
// boundary's meaning changed, not when a body did.
//
// Ported 1:1 from annotations/interface_hash.ts EXCEPT
// interfaceHashOf itself: its `contracts: Map<ts.Symbol,
// FunctionContract>` parameter needs evaluation/flow_context.ts's
// FunctionContract, which has no Go twin yet (evaluation/ is not
// ported -- see PORT.md's port order; blocked-on evaluation). Every
// other function here (formatSetKey, hashOf, formatStated,
// formatObject) reads only DeclaredRefinement/ObjectAnnotation/
// Annotation, all ported, so they land now; interfaceHashOf's own
// signature and the `contracts` fold are the blocked remainder,
// listed in the port report rather than silently stubbed.

package annotations

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// formatSetKey is formatSetKey in the TS source, WITH A KNOWN
// DIVERGENCE: the TS source keys on canonicalKeyOf(set) -- an
// order-FREE spelling (kernel_bridge/question_cache.ts) that treats
// e.g. `A union B` and `B union A` as the same key, falling back to a
// WeakMap identity token only for oversized sets with no tree format.
// canonicalKeyOf's Go twin (kernelbridge.CanonicalKeyOf) takes the
// DECODED wire tree (map[string]any), built by kernelbridge's
// unexported wireSet -- not exported, and this directory does not own
// kernelbridge (PORT.md's one-agent-one-directory rule), so adding an
// export here would be a cross-directory change outside this port
// unit's scope. This function instead keys on
// kernelbridge.EncodeSet(set), the canonical WIRE STRING (exported,
// already used identically by objectgraphs/graph_specification.go):
// deterministic and unique per distinct set, but FIELD-ORDER
// sensitive -- two structurally-equal sets built through different
// code paths that happen to nest forms in a different order would
// hash differently here, where the TS source's order-free key would
// not. Reported as a 1:1-impossible item; the fix is a kernelbridge
// export (kernelbridge.WireSet or an exported CanonicalKeyOfSet
// helper), which the kernelbridge port owner should add.
func formatSetKey(set refinementsets.RefinedSet) string {
	return kernelbridge.EncodeSet(set)
}

// hashOf is hashOf in the TS source: two FNV passes with distinct
// initialStates -- 64 collision bits; a collision here would silently
// validate stale facts, so width is cheap insurance.
func hashOf(text string) string {
	var h1 uint32 = 0x811c9dc5
	var h2 uint32 = 0x9747b28c
	for _, c := range []byte(text) {
		h1 = (h1 ^ uint32(c)) * 0x01000193
		h2 = (h2 ^ uint32(c)) * 0x01000197
	}
	return strconv.FormatUint(uint64(h1), 36) + "." + strconv.FormatUint(uint64(h2), 36)
}

// formatStated is formatStated in the TS source.
func formatStated(stated *DeclaredRefinement) string {
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
		return "m:" + formatStated(stated.Inner)
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

// InterfaceHashOfSets is the BLOCKED-remainder-free portion of
// interfaceHashOf: folds the imports and the annotations/objects
// registries (both fully typed and ported) into the hash. The
// contracts fold (`c:${symbol.name}=...`) is NOT included here --
// FunctionContract has no Go twin yet; a caller that also has
// contracts must fold their own `c:` lines in before hashing, or wait
// for evaluation/'s port.
func InterfaceHashOfSets(
	annotations map[*ast.Symbol]*Annotation,
	objects map[*ast.Symbol]*ObjectAnnotation,
	importHashes map[string]string,
) string {
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
	sortStrings(parts)
	return hashOf(strings.Join(parts, "\n"))
}

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1] > items[j]; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}
