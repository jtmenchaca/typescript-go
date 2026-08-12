// ObjectAnnotation → the graph Specification the kernel judges.
// Recursion is a cycle, never an unrolling: a self- or mutually
// recursive schema is a finite graph with an arrow back to itself.
//
// Ported 1:1 from annotations/object_specification.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/objectgraphs"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// SpecificationResult is the {spec} | {unresolved} union
// SpecificationOf returns.
type SpecificationResult struct {
	Spec       objectgraphs.Specification
	Unresolved *ast.Node
}

// SpecificationOf is specificationOf in the TS source: assemble one
// object annotation's closure into the specification the kernel
// judges.
func SpecificationOf(root *ObjectAnnotation, objects ObjectRegistry) SpecificationResult {
	var nodes []refinementsets.RefinedSet
	var paths []objectgraphs.CardinalityPath
	var markings []objectgraphs.ObjectNode
	indexOf := map[*ObjectAnnotation]int{}
	// a reference whose target was never stated: the graph cannot be
	// assembled, and the caller reports it rather than judging a
	// half-built specification
	var unresolved *ast.Node

	// Recursion is a CYCLE, never an unrolling: an object already
	// being built returns its own index, so a self- or mutually
	// recursive schema is a finite graph with an arrow back to
	// itself.
	var nodeOf func(object *ObjectAnnotation) int
	nodeOf = func(object *ObjectAnnotation) int {
		if known, ok := indexOf[object]; ok {
			return known
		}
		index := len(nodes)
		nodes = append(nodes, refinementsets.MakeRefinedSet()) // the object node itself: the root R-bar*
		indexOf[object] = index
		type keyMark struct {
			name string
			path int
		}
		var keyMarks []keyMark
		for _, key := range object.Keys {
			head := -1
			hasHead := false
			switch key.Value.Kind {
			case KeyValueSet:
				head = len(nodes)
				nodes = append(nodes, derefSet(key.Value.Set))
				hasHead = true
			case KeyValueObject:
				head = nodeOf(key.Value.Object)
				hasHead = true
			case KeyValueReference:
				target := objects[key.Value.Target]
				if target == nil {
					if unresolved == nil {
						unresolved = key.At
					}
				} else {
					head = nodeOf(target)
					hasHead = true
				}
			case KeyValueCollection:
				// entries check assignability through the adapter's
				// entry walk, not the graph -- the spec omits the
				// key, claiming nothing of it
			}
			if !hasHead {
				continue
			}
			pathIndex := len(paths)
			paths = append(paths, objectgraphs.CardinalityPath{Tail: index, Count: derefSet(key.Count), Head: head})
			keyMarks = append(keyMarks, keyMark{name: key.Name, path: pathIndex})
		}
		objectKeys := make([]objectgraphs.ObjectKey, len(keyMarks))
		for i, mark := range keyMarks {
			objectKeys[i] = objectgraphs.ObjectKey{Name: mark.name, Path: mark.path}
		}
		markings = append(markings, objectgraphs.ObjectNode{Node: index, Keys: objectKeys})
		return index
	}

	nodeOf(root)
	if unresolved != nil {
		return SpecificationResult{Unresolved: unresolved}
	}
	return SpecificationResult{Spec: objectgraphs.Specification{Nodes: nodes, Paths: paths, Objects: markings}}
}
