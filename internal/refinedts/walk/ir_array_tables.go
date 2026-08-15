// split from ir_array_slots.go — the flattened-sibling lookups and the
// two-phase table build over a body's locals

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// flattenedSources is what the array recognizer can ask about a spelled
// name it did not declare — the two sibling kinds a lone-spread
// initializer may stand on.
//
// `Collection` answers whether the name is a flattened Map or Set, which
// the BRIDGE (`const a = [...m.values()]`) needs. `Array` answers
// whether it is a flattened ARRAY, which the COPY (`const b = [...a]`)
// needs. Neither is readable off the declaration itself, which is why
// both are threaded in rather than derived here.
//
// A nil field admits no form that depends on it; the zero value admits
// neither, which is the behaviour before either existed.
type flattenedSources struct {
	Collection func(name string) (MapLocal, bool)
	Array      func(name string) (ArrayLocal, bool)
}

// collectionOf and arrayOf are the two lookups with the nil-field rule
// applied once, so no caller below repeats it.
func (sources flattenedSources) collectionOf(name string) (MapLocal, bool) {
	if sources.Collection == nil {
		return MapLocal{}, false
	}
	return sources.Collection(name)
}

func (sources flattenedSources) arrayOf(name string) (ArrayLocal, bool) {
	if sources.Array == nil {
		return ArrayLocal{}, false
	}
	return sources.Array(name)
}

// ArrayLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration.
//
// `collections` is the flattened Map/Set table (MapLocalsOf's answer),
// which the BRIDGE consults — `const a = [...m.values()]` needs it to
// know m is flattened. Passing nil admits no bridge, which is the
// behaviour before the bridge existed.
//
// TWO phases, the same shape MapLocalsOf runs: the literal and bridged
// arrays flatten first, then the COPIES (`const b = [...a]`), which need
// the table the first phase built to know their source is flattened. A
// copy of a copy resolves on a later pass, and the passes stop as soon
// as one adds nothing — a cycle among copies cannot arise (a source must
// be declared before the spread reads it), and the fixpoint terminates
// regardless because each pass either grows the table or ends it.
func ArrayLocalsOf(body *ast.Node, locals []*ast.Node, collections map[*ast.Node]MapLocal) map[*ast.Node]ArrayLocal {
	collectionsByName := map[string]MapLocal{}
	for _, collection := range collections {
		collectionsByName[collection.Name] = collection
	}
	arraysByName := map[string]ArrayLocal{}
	sources := flattenedSources{
		Collection: func(name string) (MapLocal, bool) {
			held, found := collectionsByName[name]
			return held, found
		},
		Array: func(name string) (ArrayLocal, bool) {
			held, found := arraysByName[name]
			return held, found
		},
	}
	out := map[*ast.Node]ArrayLocal{}
	// the first phase admits no copy: arraysByName is still empty, so
	// copiedArrayLocalOf finds no sibling and every declaration is read as
	// a literal or a bridge
	for _, declaration := range locals {
		if local, ok := ArrayLocalOf(body, declaration, sources); ok {
			out[declaration] = local
			arraysByName[local.Name] = local
		}
	}
	// the second phase: the forms that stand on an already-flattened
	// SIBLING array — the copy `const b = [...a]` and the callback result
	// `const b = a.map(cb)`. Both need the table the first phase built.
	for added := true; added; {
		added = false
		for _, declaration := range locals {
			if _, already := out[declaration]; already {
				continue
			}
			local, ok := copiedArrayLocalOf(body, declaration, sources)
			if !ok {
				local, ok = mappedArrayLocalOf(body, declaration, sources)
			}
			if !ok {
				continue
			}
			out[declaration] = local
			arraysByName[local.Name] = local
			added = true
		}
	}
	return out
}
