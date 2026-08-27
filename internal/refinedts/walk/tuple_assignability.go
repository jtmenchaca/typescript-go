// A value judged against a TUPLE-stated position: the arity, then each
// slot against its own declared set.
//
// A tuple target states two facts a starred array target cannot. The
// LENGTH is exactly the slot count, so a value whose own length is
// known and different is refuted outright — the claim A7.sink.assign's
// `const p: [number, number] = xs` and A7.sink.ret's
// `return xs` (an unbounded `number[]` into a length-3 return) both
// turn on. And slot i has its OWN set, which slot j need not share, so
// a per-slot judgment can refute what a joined element set would admit.
//
// The judgment reuses the ordinary judge for each slot rather than
// posing a new kind of kernel question: a slot is one checked position
// like any other, and CheckAssignabilityAgainst already knows how to
// answer it.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CheckTupleTarget judges a value against a tuple-stated position.
func CheckTupleTarget(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement, // kind == "tuple"
	node *ast.Node,
	what string,
) {
	slots := len(target.Slots)
	// an EXACT list: the arity is read straight off the items, and each
	// item is judged at its own slot's statement
	if known.Kind == abstractdomain.KindList {
		if len(known.Items) != slots {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of "+strconv.Itoa(len(known.Items))+" element"+pluralS(len(known.Items))+
					" is not assignable to a tuple of "+strconv.Itoa(slots)+" element"+pluralS(slots),
			))
			return
		}
		checkSlotsOfItems(ctx, known.Items, target, node, what)
		return
	}
	// the FLAT spelling of the same exact sequence: an all-numeric array
	// literal answers KnownValues tagged PrimitiveArray, one float per
	// position, and never builds a KindList (array_literal.go's `flat`
	// arm). It states the arity and every slot just as exactly, so it
	// judges through the same road with each position lifted back to a
	// one-value claim — without this arm `const p: [10, 20] = [10, 20]`
	// reached the "states no length" decline below and reported 7002 on
	// a value whose length and slots are both fully known.
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveArray {
		if len(known.Values) != slots {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of "+strconv.Itoa(len(known.Values))+" element"+pluralS(len(known.Values))+
					" is not assignable to a tuple of "+strconv.Itoa(slots)+" element"+pluralS(slots),
			))
			return
		}
		grade := abstractdomain.TrustLevelOf(known)
		items := make([]abstractdomain.AbstractValue, len(known.Values))
		for i, v := range known.Values {
			items[i] = abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade)
		}
		checkSlotsOfItems(ctx, items, target, node, what)
		return
	}
	// a SET-shaped sequence — the star a `number[]` position wears
	// (typereading/recipes.go's StarOfElement) — states a LENGTH WINDOW
	// rather than a count. Where the window provably cannot hold the
	// slot count, the tuple's arity is refuted: an unbounded star's
	// window is [0, ∞), which DOES admit the count, so the refutation
	// below fires only on a genuinely disjoint window and the ordinary
	// unbounded case falls through to the per-slot reading.
	if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone {
		if window, hasWindow := refinementsets.AsRepetition(known.Set); hasWindow {
			hiExceeded := window.Hi != nil && slots > *window.Hi
			if slots < window.Lo || hiExceeded {
				ctx.Report(assignability.At(
					node,
					7001,
					what+" is not assignable to a tuple of "+strconv.Itoa(slots)+" element"+pluralS(slots)+
						" — its length is not fixed at "+strconv.Itoa(slots),
				))
				return
			}
			// the window ADMITS the count but does not PIN it: every
			// length in [lo, hi] is still a value this position can hold,
			// so the length obligation is not discharged. Only a window
			// that admits exactly the slot count proves the arity.
			pinned := window.Hi != nil && *window.Hi == slots && window.Lo == slots
			if !pinned {
				ctx.Report(assignability.At(
					node,
					7001,
					what+" has a length the statement does not fix at "+strconv.Itoa(slots)+
						" — a tuple of "+strconv.Itoa(slots)+" element"+pluralS(slots)+" admits that length only",
				))
				return
			}
			// arity proved: every slot judges against the ONE element set
			// the window states, since the window says nothing per
			// position
			element := abstractdomain.KnownSet(window.Element, nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
			items := make([]abstractdomain.AbstractValue, slots)
			for i := range items {
				items[i] = element
			}
			checkSlotsOfItems(ctx, items, target, node, what)
			return
		}
	}
	// every other reading — an object-star, a collection, an unknown —
	// states no length this rule can compare against the slot count, so
	// the position stays undetermined rather than admitted on a fact
	// nobody proved
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// ReadTupleFieldGrowth judges a LENGTH-CHANGING method call on a class
// field whose declared type is a tuple: `this.xs.push(x)` where the
// field reads `xs: [number, number, number]`.
//
// The declared tuple states the field's length as an invariant, and
// push/unshift/pop/shift/splice each move it — the value after the call
// is a different length than the declaration admits, so the write is
// refused AT THE GROWTH SITE rather than at some later read.
//
// It needs its own reader because the tracked-mutation road cannot see
// this call at all: MethodCallSite only fills TrackedName for a bare
// IDENTIFIER receiver (builtin_models.go), and `this.xs` is a property
// access, so readArrayWriteMethods never runs and the field's
// declaration is never consulted. Answers nil for every call this rule
// does not cover, leaving the dispatcher's other readers to answer.
//
// Judging only — the value the call answers stays with whatever reader
// takes it next, exactly as WriteElement judges without claiming the
// write's result.
func ReadTupleFieldGrowth(site MethodCallSite) *abstractdomain.AbstractValue {
	switch site.Method {
	case "push", "unshift", "pop", "shift", "splice":
	default:
		return nil
	}
	receiver := site.ReceiverExpression
	if !ast.IsPropertyAccessExpression(receiver) {
		return nil
	}
	pa := receiver.AsPropertyAccessExpression()
	if pa.Expression.Kind != ast.KindThisKeyword {
		return nil
	}
	name := pa.Name()
	if name == nil || !(ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)) {
		return nil
	}
	class := dataflowfacts.EnclosingThisClass(receiver)
	if class == nil {
		return nil
	}
	field := propertyDeclarationNamed(class.ClassLikeData(), name.Text())
	if field == nil || field.AsPropertyDeclaration().Type == nil {
		return nil
	}
	read := annotations.AnnotationOfType(site.Ctx.P, field.AsPropertyDeclaration().Type, site.Ctx.Registry, site.Ctx.Objects)
	if read.Unsupported != "" || read.Stated == nil || read.Stated.Kind != annotations.DeclaredTuple {
		return nil
	}
	slots := len(read.Stated.Slots)
	// `splice` with no arguments removes nothing (sec-array.prototype.
	// splice: an absent start makes actualDeleteCount 0 and inserts
	// nothing), so it alone leaves the length where it was.
	if site.Method == "splice" {
		call := site.E.AsCallExpression()
		if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
			return nil
		}
	}
	site.Ctx.Report(assignability.At(
		site.E,
		7001,
		"the field '"+name.Text()+"' is declared a tuple of "+strconv.Itoa(slots)+" element"+pluralS(slots)+
			"; '"+site.Method+"' changes its length",
	))
	return nil
}

// checkSlotsOfItems judges each item against its own slot's statement.
// The per-item POSITION is the array literal's own element where the
// judged node is one (the same mapping CheckObjectArrayTarget makes, so
// a refutation points at the element that caused it rather than the
// whole literal); otherwise every slot reports at the judged node.
//
// A refutation from any slot is THE answer and reports alone; an
// undetermined slot with no refutation anywhere keeps the alert. That
// ordering is CheckObjectArrayTarget's, and it is what keeps a definite
// verdict from being buried under an alert raised by a different slot.
func checkSlotsOfItems(
	ctx *FlowContext,
	items []abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	var captured []assignability.RefinementDiagnostic
	probe := *ctx
	probe.Report = func(d assignability.RefinementDiagnostic) {
		captured = append(captured, d)
	}
	elements := make([]*ast.Node, len(items))
	if ast.IsArrayLiteralExpression(node) {
		written := node.AsArrayLiteralExpression().Elements.Nodes
		hasSpread := false
		for _, e := range written {
			if ast.IsSpreadElement(e) {
				hasSpread = true
				break
			}
		}
		// a SPREAD moves every position after it by an unknown amount, so
		// no written element lines up with a known slot — the whole
		// literal is the position for all of them. Without a spread the
		// written elements and the items are the same list, in order.
		if !hasSpread && len(written) == len(items) {
			copy(elements, written)
		}
	}
	for i, item := range items {
		itemNode := node
		if elements[i] != nil {
			itemNode = elements[i]
		}
		CheckAssignabilityAgainst(
			&probe,
			item,
			*target.Slots[i],
			itemNode,
			"slot "+strconv.Itoa(i),
			nil,
		)
	}
	for i := range captured {
		if captured[i].Code == 7001 {
			ctx.Report(captured[i])
			return
		}
	}
	if len(captured) > 0 {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
	}
}
