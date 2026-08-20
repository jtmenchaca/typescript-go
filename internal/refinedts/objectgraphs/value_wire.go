// The value assignment a judge question may carry beside its
// specification: the instance the kernel's value-instantiation
// judgment checks (graph/value_instantiation.lean, decoded by
// boundary/decode_graph.lean's decodeValues). A leaf is one tuple in
// EncodeTuple's two spellings; a SET leaf is a candidate set in
// EncodeSet's spelling (the kernel judges the subset question — the
// whole candidate set admitted by the stated key); a node is an
// object instance's present keys. Field order is hand-built to match
// the Lean decoder byte for byte, per PORT.md's wire convention.
package objectgraphs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ValueTree is one value: a concrete leaf tuple, a candidate set, or
// an object instance's present keys.
type ValueTree struct {
	// IsLeaf: the value is the one tuple below.
	IsLeaf bool
	Tuple  []float64
	// IsSet: the value is the candidate set below — what the checker
	// knows of a non-constant value (a key known in 0..120 crosses as
	// its window).
	IsSet bool
	Set   refinementsets.RefinedSet
	// Otherwise: an object instance with the keys below.
	Keys []ValueTreeKey
}

// ValueTreeKey is one present key of an object instance.
type ValueTreeKey struct {
	Name  string
	Value ValueTree
}

// ValueAssignment is the "values" field of a judge question: the node
// the instance is judged at, and the instance itself.
type ValueAssignment struct {
	Root  int
	Value ValueTree
}

func wireValueTree(t ValueTree) string {
	if t.IsLeaf {
		return `{"tuple":` + kernelbridge.EncodeTuple(t.Tuple) + `}`
	}
	if t.IsSet {
		return `{"set":` + kernelbridge.EncodeSet(t.Set) + `}`
	}
	keys := make([]string, len(t.Keys))
	for i, k := range t.Keys {
		name, err := json.Marshal(k.Name)
		if err != nil {
			panic(fmt.Sprintf("wireValueTree: %v", err))
		}
		keys[i] = fmt.Sprintf(`{"name":%s,"value":%s}`, name, wireValueTree(k.Value))
	}
	return fmt.Sprintf(`{"keys":[%s]}`, strings.Join(keys, ","))
}

// WireValueAssignment is the "values" JSON — the wire twin of
// boundary/decode_graph.lean's decodeValues.
func WireValueAssignment(a ValueAssignment) string {
	return fmt.Sprintf(`{"root":%d,"value":%s}`, a.Root, wireValueTree(a.Value))
}

// EncodeSpecificationWithValues rides the value assignment beside the
// specification: EncodeSpecification's own bytes with one more
// top-level field spliced before the closing brace, so a question
// that states no values stays byte-identical to what it always was.
func EncodeSpecificationWithValues(s Specification, groups []CountGroup, values ValueAssignment) string {
	base := EncodeSpecification(s, groups)
	return base[:len(base)-1] + `,"values":` + WireValueAssignment(values) + "}"
}

// JudgeValue asks the judge with the instance riding beside the
// specification — the same refined_judge symbol; the answer carries
// the value verdict and the 3008-3012 faults.
func JudgeValue(
	kernel *kernelbridge.RefinedTSKernel,
	s Specification,
	groups []CountGroup,
	values ValueAssignment,
) kernelbridge.JudgeAnswer {
	return kernel.CheckAssignability(EncodeSpecificationWithValues(s, groups, values))
}
