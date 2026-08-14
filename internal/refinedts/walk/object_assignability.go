// from assignability/object_assignability.ts
//
// Object / objectArray arms of one checked position: per-key subset
// (TypeScript's structural subtyping, TERMS.md term 10), array-of-
// records element-by-element, and an object known against a set that
// demonstrably states a sequence or a scalar layer.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// CheckObjectArrayTarget is checkObjectArrayTarget in the TS source:
// an array of records — an exact list judges element by element
// against the object annotation and its count against the window;
// anything less exact stays the honest alert.
func CheckObjectArrayTarget(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement, // kind == "objectArray"
	node *ast.Node,
	what string,
) {
	// a flat NUMBER tuple (the empty array included): the count
	// judges against the window, and any element it holds is a
	// number — a member of no stated object
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveArray {
		count := len(known.Values)
		hiExceeded := !target.HiUnbounded && float64(count) > target.Hi
		if float64(count) < target.Lo || hiExceeded {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of "+strconv.Itoa(count)+" element"+pluralS(count)+" is not "+
					"assignable to the stated counts",
			))
			return
		}
		if count == 0 {
			return
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" holds a number where the statement requires an object",
		))
		return
	}
	if known.Kind == abstractdomain.KindList {
		count := len(known.Items)
		hiExceeded := !target.HiUnbounded && float64(count) > target.Hi
		if float64(count) < target.Lo || hiExceeded {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of "+strconv.Itoa(count)+" element"+pluralS(count)+" is not "+
					"assignable to the stated counts",
			))
			return
		}
		var captured []assignability.RefinementDiagnostic
		probe := *ctx
		probe.Report = func(d assignability.RefinementDiagnostic) {
			captured = append(captured, d)
		}
		// an array LITERAL gives each item its own syntactic position —
		// the element's object literal, whose key initializers carry
		// their true contextual sorts (the array node itself wears the
		// array sort and would gate every scalar key to an alert).
		//
		// A spread contributes an unknown number of items, so only the
		// positions a spread cannot have moved map: the elements BEFORE
		// the first spread line up with the head of the item list, and
		// the elements AFTER the last spread line up with its tail.
		// Everything between the two spreads points at the whole array,
		// as it did before.
		elements := make([]*ast.Node, len(known.Items))
		if ast.IsArrayLiteralExpression(node) {
			written := node.AsArrayLiteralExpression().Elements.Nodes
			firstSpread, lastSpread := -1, -1
			for i, e := range written {
				if ast.IsSpreadElement(e) {
					if firstSpread < 0 {
						firstSpread = i
					}
					lastSpread = i
				}
			}
			if firstSpread < 0 {
				if len(written) == len(known.Items) {
					copy(elements, written)
				}
			} else {
				// the head: written[0:firstSpread] are items 0..firstSpread-1
				head := firstSpread
				if head > len(elements) {
					head = len(elements)
				}
				copy(elements[:head], written[:head])
				// the tail: the elements after the last spread are the last
				// so many items, in order
				tail := len(written) - lastSpread - 1
				if tail > len(elements)-head {
					tail = len(elements) - head
				}
				if tail > 0 {
					copy(elements[len(elements)-tail:], written[len(written)-tail:])
				}
			}
		}
		for i, item := range known.Items {
			itemNode := node
			if elements[i] != nil {
				itemNode = elements[i]
			}
			CheckAssignabilityAgainst(
				&probe,
				item,
				annotations.DeclaredRefinement{Kind: annotations.DeclaredObject, Object: target.Object},
				itemNode,
				"an element",
				nil,
			)
		}
		var refuted *assignability.RefinementDiagnostic
		for i := range captured {
			if captured[i].Code == 7001 {
				refuted = &captured[i]
				break
			}
		}
		if refuted != nil {
			ctx.Report(*refuted)
			return
		}
		if len(captured) == 0 {
			return
		}
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// CheckObjectTarget is checkObjectTarget in the TS source: per-key
// subset against a stated object: missing required keys refute,
// present keys recurse, dependent bounds between keys judge with
// both sides in view, and an unread refine keeps the honest alert.
func CheckObjectTarget(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement, // kind == "object"
	node *ast.Node,
	what string,
) {
	if known.Kind != abstractdomain.KindObject {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	// the same annotation, by identity: nothing left to check
	if known.Stated != nil && known.Stated == objectAnnotationRefOf(target.Object) {
		return
	}
	knownKeys := make(map[string]abstractdomain.AbstractValue, len(known.Keys))
	for _, k := range known.Keys {
		knownKeys[k.Name] = k.Value
	}
	for _, key := range target.Object.Keys {
		held, hasKey := knownKeys[key.Name]
		if !hasKey {
			// a key the value does not carry: absence is a cardinality
			// fact, and only a count admitting zero allows it
			if !key.MayBeAbsent {
				ctx.Report(assignability.At(
					node,
					7001,
					what+" is missing the key '"+key.Name+"'",
				))
			}
			continue
		}
		keyTarget := DeclaredOfKey(key)
		if keyTarget == nil {
			continue // a reference: the graph judges it
		}
		// an object LITERAL judges each key at its own initializer —
		// whose contextual type is the key's declared type, so the
		// admitted-language check reaches keys too
		keyNode := node
		if ast.IsObjectLiteralExpression(node) {
			for _, candidate := range node.AsObjectLiteralExpression().Properties.Nodes {
				if ast.IsPropertyAssignment(candidate) {
					assignment := candidate.AsPropertyAssignment()
					name := assignment.Name()
					if (ast.IsIdentifier(name) || ast.IsStringLiteral(name)) && name.Text() == key.Name {
						keyNode = assignment.Initializer
						break
					}
				}
			}
		}
		CheckAssignability(ctx, held, *keyTarget, keyNode, "the key '"+key.Name+"'", nil)
	}
	// the DEPENDENT bounds between keys: with both keys' knowledge
	// in view, an exact sibling instantiates the bound and a
	// windowed one judges by its edges
	for _, key := range target.Object.Keys {
		if key.Value.Kind != annotations.KeyValueSet || key.Value.Depends == nil {
			continue
		}
		held, hasKey := knownKeys[key.Name]
		if !hasKey {
			continue
		}
		for _, dep := range key.Value.Depends {
			var sibling *abstractdomain.AbstractValue
			if v, ok := knownKeys[dep.Param]; ok {
				sibling = &v
			}
			JudgeDependentRelation(
				ctx,
				held,
				sibling,
				DependentRelation{Op: dep.Op, Param: dep.Param},
				node,
				"the key '"+key.Name+"'",
			)
		}
	}
	// an UNREAD refine rides the statement: the parse checks more
	// than the keys say, so a position the keys would prove stays
	// undetermined, honestly
	if target.Object.Unread {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
	}
}

// CheckObjectKnown is checkObjectKnown in the TS source: an object
// known where the target is not an object: a tuple-layer set that
// demonstrably states a sequence or a scalar layer refutes in plain
// words; anything else stays the honest alert.
func CheckObjectKnown(
	ctx *FlowContext,
	known abstractdomain.AbstractValue, // kind == "object"
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	// an object where a tuple-layer set is DEMONSTRABLY stated — a
	// sequence shape or a scalar-layer form — is a member of it on
	// no run, so the position REFUTES in plain words (the branch
	// alerted before 2026-08-08, hiding the wire-vs-decoded cast
	// class behind "cannot be determined"). An UNREAD or formless
	// placeholder set (a record's widest sound claim) states more
	// than its forms say, so it keeps the honest alert.
	if target.Kind == annotations.DeclaredSet && !target.Unread &&
		(StatesSequence(*target.Set) ||
			(len(target.Set.Forms) > 0 && refinementsets.OnOneTupleLayer(*target.Set))) {
		says := "a plain value"
		if StatesSequence(*target.Set) {
			says = "a string or an array"
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" is an object, and the position states "+
				says+" — an object is not allowed here",
		))
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}
