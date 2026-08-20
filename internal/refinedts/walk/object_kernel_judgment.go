// The kernel's value-instantiation judgment
// (graph/value_instantiation.lean), asked ahead of the local per-key
// walk at a KNOWN object judged against a stated object annotation.
//
// Delegate-first, never weaker: the kernel path serves only where the
// annotation's WHOLE claim lowers to the graph specification — plain
// per-key sets and nested objects/references, no unread refines, no
// dependent bounds, no collection keys, no sequence measures, no
// bigint/symbol sorts — so a served verdict covers everything the
// local walk would have checked at the position. The instance side
// carries exact values AND candidate sets (a key known in 0..120
// crosses as its window; the kernel answers the subset question).
// Everywhere else, and on any decline (a gate, the encoder, the wire,
// the kernel's own subset-undecided fault, a kernel refusal via the
// recover convention), the local walk runs unchanged.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/objectgraphs"
)

// KernelObjectVerdict asks the value-instantiation judgment for a
// known object against a stated object annotation. True means the
// kernel path SERVED the position: fired faults reported as
// refutations, or a proved acceptance with nothing to report. False
// means the caller's local walk owns the position, unchanged.
func KernelObjectVerdict(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement, // kind == "object"
	node *ast.Node,
	what string,
) bool {
	if ctx.Kernel == nil || target.Object == nil {
		return false
	}
	if !annotationLowersWhole(target.Object, ctx.Objects,
		map[*annotations.ObjectAnnotation]bool{}) {
		return false
	}
	assembled := annotations.SpecificationOf(target.Object, ctx.Objects)
	if assembled.Unresolved != nil {
		return false
	}
	// SpecificationOf assembles the root object at node index 0
	values, reason := objectgraphs.ValueAssignmentOf(assembled.Spec, 0, known)
	if reason != "" {
		return false
	}
	verdict, declined := judgeValueRecovered(ctx.Kernel, assembled.Spec, values)
	if declined || !verdict.Structural || !verdict.HasValue {
		return false
	}
	var valueFaults []kernelbridge.KernelFault
	for _, fault := range verdict.Faults {
		// 3014 is the kernel's subset-undecided arm: the candidate set
		// and the stated set are not a shape pair the subset deciders
		// answer — a DECLINE, never a verdict about the program
		if fault.Code == 3014 {
			return false
		}
		if fault.Code >= 3008 && fault.Code <= 3013 {
			valueFaults = append(valueFaults, fault)
			continue
		}
		// a specification-level fault: the graph itself is refuted,
		// which pass 1b reports where the object is STATED — this
		// position stays with the local walk
		return false
	}
	if verdict.Value {
		return true // a proved instantiation: nothing to report
	}
	if len(valueFaults) == 0 {
		// a false verdict names its fault (valueFaults_nil); without
		// one, decline rather than accept or invent a sentence
		return false
	}
	for _, fault := range valueFaults {
		if fault.Key == "" {
			ctx.Report(assignability.At(node, 7001, what+": "+fault.MessageText))
			continue
		}
		ctx.Report(assignability.At(
			keyInitializerNode(node, fault.Key),
			7001,
			what+"'s key '"+fault.Key+"': "+fault.MessageText,
		))
	}
	return true
}

// keyInitializerNode places a top-level key's fault at its own
// initializer when the judged node is an object LITERAL — the same
// positioning CheckObjectTarget's per-key walk gives — and keeps the
// whole node for nested (dotted) keys and every other shape.
func keyInitializerNode(node *ast.Node, key string) *ast.Node {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return node
		}
	}
	if !ast.IsObjectLiteralExpression(node) {
		return node
	}
	for _, candidate := range node.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(candidate) {
			continue
		}
		assignment := candidate.AsPropertyAssignment()
		name := assignment.Name()
		if (ast.IsIdentifier(name) || ast.IsStringLiteral(name)) && name.Text() == key {
			return assignment.Initializer
		}
	}
	return node
}

// annotationLowersWhole answers whether EVERY claim the annotation
// closure states lowers into the graph specification the judgment
// reads — the serving gate. Anything the judgment does not model
// keeps the position with the local walk, which checks it today:
// an unread refine (the parse checks more than the keys say), a
// dependent bound between keys, a collection key (the pipeline omits
// it from the specification — a served acceptance would skip its
// entry checks), sequence measures, and bigint/symbol sorts. A
// nullable key (Absent) passes: null admission only ADDS beside the
// set, and the encoder declines absent-valued instances anyway.
func annotationLowersWhole(
	object *annotations.ObjectAnnotation,
	objects annotations.ObjectRegistry,
	visited map[*annotations.ObjectAnnotation]bool,
) bool {
	if visited[object] {
		return true // recursion is a cycle; the first visit gates it
	}
	visited[object] = true
	if object.Unread {
		return false
	}
	for _, key := range object.Keys {
		v := key.Value
		if v.Unread || len(v.Depends) > 0 || v.Measures != nil || v.KindTag != "" {
			return false
		}
		switch v.Kind {
		case annotations.KeyValueCollection:
			return false
		case annotations.KeyValueObject:
			if v.Object == nil || !annotationLowersWhole(v.Object, objects, visited) {
				return false
			}
		case annotations.KeyValueReference:
			targetObject := objects[v.Target]
			if targetObject == nil || !annotationLowersWhole(targetObject, objects, visited) {
				return false
			}
		}
	}
	return true
}

// judgeValueRecovered wraps objectgraphs.JudgeValue's panic-on-decline
// (RefinedTSKernel's question methods panic on a refused question,
// mirroring the TS source's `throw`) into a (verdict, declined) pair —
// the same recover convention service/check.go's
// checkAssignabilityRecovered keeps. The panic's own text is
// discarded (never a checker fact about the value being judged); the
// caller falls back to its local walk instead.
func judgeValueRecovered(
	kernel *kernelbridge.RefinedTSKernel,
	spec objectgraphs.Specification,
	values objectgraphs.ValueAssignment,
) (verdict kernelbridge.JudgeAnswer, declined bool) {
	defer func() {
		if recover() != nil {
			declined = true
		}
	}()
	verdict = objectgraphs.JudgeValue(kernel, spec, nil, values)
	return verdict, false
}
