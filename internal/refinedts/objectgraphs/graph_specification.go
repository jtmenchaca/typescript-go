// TERMS.md terms 7–11: a specification is a graph of data nodes and
// cardinality nodes with their directed edges — nodes are refined
// sets, each path data node → cardinality node → data node, object
// nodes state their keys as such paths. This is the wire shape the
// kernel's checkAssignability decodes; nodes and paths reference each other by
// position.
package objectgraphs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CardinalityPath is the TS CardinalityPath interface.
type CardinalityPath struct {
	Tail  int
	Count refinementsets.RefinedSet
	Head  int
}

// ObjectKey is the TS ObjectKey interface.
type ObjectKey struct {
	Name string
	Path int
}

// ObjectNode is the TS ObjectNode interface.
type ObjectNode struct {
	Node int
	Keys []ObjectKey
}

// Specification is the TS Specification interface.
type Specification struct {
	Nodes   []refinementsets.RefinedSet
	Paths   []CardinalityPath
	Objects []ObjectNode
}

// CountGroup is CountGroup in the TS source.
//
// Several paths out of ONE tail, and the admissible set for the SUM
// of their counts at each element of that tail (graph/coexistence.lean).
//
// Every path states a condition on all elements of its tail, counted
// on its own pair subset, so nothing in a bare specification can say
// "one branch or the other, exactly one" — the branches count
// different subsets. A group says it:
//
//	two branches at {0,1}, total {1}     one edge, two destinations
//	n branches at {0,1}, total {0,1}     a choice of types
//	n branches at {0,1}, total ≥ 1       at least one of these keys
//
// Groups travel beside the specification, never inside it.
type CountGroup struct {
	Tail       int
	Paths      []CardinalityPath
	Admissible refinementsets.RefinedSet
}

// CountGroupParts is the destructured named-parameter struct for
// CountGroupOf (the TS parts object).
type CountGroupParts struct {
	Tail       int
	Paths      []CardinalityPath
	Admissible refinementsets.RefinedSet
}

// CountGroupOf is countGroup in the TS source.
func CountGroupOf(parts CountGroupParts) CountGroup {
	return CountGroup{
		Tail:       parts.Tail,
		Paths:      parts.Paths,
		Admissible: parts.Admissible,
	}
}

// SpecificationParts is the destructured named-parameter struct for
// SpecificationOf (the TS parts object; Paths/Objects are optional
// there, defaulting to none).
type SpecificationParts struct {
	Nodes   []refinementsets.RefinedSet
	Paths   []CardinalityPath
	Objects []ObjectNode
}

// SpecificationOf is specification in the TS source. Paths/Objects
// default to an empty (non-nil) slice, matching the TS `?? []`.
func SpecificationOf(parts SpecificationParts) Specification {
	paths := parts.Paths
	if paths == nil {
		paths = []CardinalityPath{}
	}
	objects := parts.Objects
	if objects == nil {
		objects = []ObjectNode{}
	}
	return Specification{
		Nodes:   parts.Nodes,
		Paths:   paths,
		Objects: objects,
	}
}

/* ── the specification wire, and the two asks that carry it ───────── */

// In the TS tree encodeSpecification lives in kernel_bridge's
// wire_format.ts, importing these types. Go forbids that direction —
// this package imports kernelbridge for its kernel calls — so the
// encoder and the typed ask wrappers live HERE, beside the types, and
// kernelbridge exposes the wire-string asks
// (RefinedTSKernel.Structural / CheckAssignability). Field order is
// hand-built to match the TS JSON.stringify bytes exactly, per
// PORT.md's wire convention.

func wirePathJSON(p CardinalityPath) string {
	return fmt.Sprintf(`{"tail":%d,"count":%s,"head":%d}`,
		p.Tail, kernelbridge.EncodeSet(p.Count), p.Head)
}

// EncodeSpecification is encodeSpecification in the TS source
// (wire_format.ts). Groups ride BESIDE the specification: the
// kernel's Specification and every theorem over it stay as they
// were, and a question that states none decodes to exactly the graph
// it used to.
func EncodeSpecification(s Specification, groups []CountGroup) string {
	var out strings.Builder
	out.WriteString(`{"nodes":[`)
	for i, node := range s.Nodes {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(kernelbridge.EncodeSet(node))
	}
	out.WriteString(`],"paths":[`)
	for i, path := range s.Paths {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(wirePathJSON(path))
	}
	out.WriteString(`],"objects":[`)
	for i, object := range s.Objects {
		if i > 0 {
			out.WriteByte(',')
		}
		keys := make([]string, len(object.Keys))
		for j, key := range object.Keys {
			name, err := json.Marshal(key.Name)
			if err != nil {
				panic(fmt.Sprintf("EncodeSpecification: %v", err))
			}
			keys[j] = fmt.Sprintf(`{"name":%s,"path":%d}`, name, key.Path)
		}
		out.WriteString(fmt.Sprintf(`{"node":%d,"keys":[%s]}`,
			object.Node, strings.Join(keys, ",")))
	}
	out.WriteByte(']')
	if len(groups) > 0 {
		out.WriteString(`,"groups":[`)
		for i, group := range groups {
			if i > 0 {
				out.WriteByte(',')
			}
			paths := make([]string, len(group.Paths))
			for j, path := range group.Paths {
				paths[j] = wirePathJSON(path)
			}
			out.WriteString(fmt.Sprintf(
				`{"tail":%d,"paths":[%s],"admissible":%s}`,
				group.Tail, strings.Join(paths, ","),
				kernelbridge.EncodeSet(group.Admissible)))
		}
		out.WriteByte(']')
	}
	out.WriteByte('}')
	return out.String()
}

// Structural is the typed form of the TS kernel.structural(spec).
func Structural(kernel *kernelbridge.RefinedTSKernel, s Specification) bool {
	return kernel.Structural(EncodeSpecification(s, nil))
}

// CheckAssignability is the typed form of the TS
// kernel.checkAssignability(spec); groups ride beside when stated.
func CheckAssignability(
	kernel *kernelbridge.RefinedTSKernel,
	s Specification,
	groups []CountGroup,
) kernelbridge.JudgeAnswer {
	return kernel.CheckAssignability(EncodeSpecification(s, groups))
}
