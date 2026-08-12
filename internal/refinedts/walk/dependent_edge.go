// from assignability/dependent_edge.ts
//
// A call-site or object-key dependent edge: value REL sibling. An
// exact sibling instantiates the bound as a constant set; a windowed
// sibling judges by its edges — accept past the far edge, refute
// short of the near one. Anything less alerts.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var dependsSymbols = map[string]string{
	"ge": ">=",
	"gt": ">",
	"le": "<=",
	"lt": "<",
}

// DependentRelation is the { op, param } shape the TS source spells
// inline at every call site of judgeDependentRelation /
// checkDependentEdge.
type DependentRelation struct {
	Op    string // "ge" | "gt" | "le" | "lt"
	Param string
}

// JudgeDependentRelation is judgeDependentRelation in the TS source:
// one DEPENDENT relation checked — value REL sibling. An exact
// sibling instantiates the bound as a constant set and the kernel
// judges it; a windowed sibling judges by its edges — accept past
// the far edge (the relation holds against EVERY possible sibling),
// refute short of the near one (every pair violates); anything less
// alerts.
func JudgeDependentRelation(
	ctx *FlowContext,
	value abstractdomain.AbstractValue,
	sibling *abstractdomain.AbstractValue,
	relation DependentRelation,
	node *ast.Node,
	what string,
) {
	symbol := dependsSymbols[relation.Op]
	var single float64
	hasSingle := false
	if sibling != nil && sibling.Kind == abstractdomain.KindValues &&
		sibling.KindTag == abstractdomain.PrimitiveNumber && len(sibling.Values) == 1 &&
		sibling.Values[0] == sibling.Values[0] { // NaN check: v == v is false for NaN
		single = sibling.Values[0]
		hasSingle = true
	}
	if hasSingle {
		var bound refinementsets.Refinement
		switch relation.Op {
		case "ge":
			bound = refinementsets.AtLeast(single)
		case "gt":
			bound = refinementsets.Above(single)
		case "le":
			bound = refinementsets.AtMost(single)
		default: // "lt"
			bound = refinementsets.Below(single)
		}
		// the standard assignability frame, with the bound's ORIGIN
		// appended — the target set was instantiated from the sibling,
		// and the reader should see where its number came from
		origin := " (" + symbol + " '" + relation.Param + "', which is " +
			formatJSNumberLocal(single) + " here)"
		originalReport := ctx.Report
		withOrigin := *ctx
		withOrigin.Report = func(d assignability.RefinementDiagnostic) {
			if d.Code == 7001 {
				d.MessageText = d.MessageText + origin
			}
			originalReport(d)
		}
		set := refinementsets.MakeRefinedSet(bound)
		CheckAssignability(
			&withOrigin,
			value,
			annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set},
			node,
			what,
			nil,
		)
		return
	}
	if CheckDependentEdge(ctx, value, sibling, relation, node, what) {
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// CheckDependentEdge is checkDependentEdge in the TS source: the
// set-vs-window arm of a dependent bound, decided ONCE for every
// judgment site (call arguments and object keys): the value accepts
// when every admitted member clears the sibling window's far edge
// (so the relation holds against EVERY possible sibling), refutes —
// reported here, with the bound's origin — when every member falls
// short of the near edge (every pair violates). True when decided
// either way; false leaves the caller its own alert: no sibling, no
// window, no set, or an edge the kernel could not answer.
func CheckDependentEdge(
	ctx *FlowContext,
	value abstractdomain.AbstractValue,
	sibling *abstractdomain.AbstractValue,
	relation DependentRelation,
	node *ast.Node,
	what string,
) bool {
	if sibling == nil {
		return false
	}
	window := RangeOfKnown(*sibling)
	if window == nil {
		return false
	}
	if !abstractdomain.IsNumericKind(value) {
		return false
	}
	valueSet, ok := abstractdomain.SetOfKnown(value)
	if !ok {
		return false
	}
	lower := relation.Op == "ge" || relation.Op == "gt"
	// accept: past the window's far edge, so the relation holds
	// against EVERY possible sibling value
	var acceptForm refinementsets.Refinement
	switch relation.Op {
	case "ge":
		acceptForm = refinementsets.AtLeast(window.Hi)
	case "gt":
		acceptForm = refinementsets.Above(window.Hi)
	case "le":
		acceptForm = refinementsets.AtMost(window.Lo)
	default: // "lt"
		acceptForm = refinementsets.Below(window.Lo)
	}
	// refute: short of the near edge, so EVERY pair violates
	var refuteForm refinementsets.Refinement
	switch relation.Op {
	case "ge":
		refuteForm = refinementsets.Below(window.Lo)
	case "gt":
		refuteForm = refinementsets.AtMost(window.Lo)
	case "le":
		refuteForm = refinementsets.Above(window.Hi)
	default: // "lt"
		refuteForm = refinementsets.AtLeast(window.Hi)
	}
	symbol := dependsSymbols[relation.Op]
	return callDependentEdgeQuestions(ctx, valueSet, acceptForm, refuteForm, window, lower, symbol, relation, node, what)
}

// callDependentEdgeQuestions is the TS source's try/catch around the
// two ScalarSubset asks: kernelbridge's question methods panic on a
// refused question (PORT.md), so this recovers the same way the TS
// catch does — a refused question leaves the caller's alert, never a
// crash.
func callDependentEdgeQuestions(
	ctx *FlowContext,
	valueSet refinementsets.RefinedSet,
	acceptForm, refuteForm refinementsets.Refinement,
	window *NumberRange,
	lower bool,
	symbol string,
	relation DependentRelation,
	node *ast.Node,
	what string,
) (decided bool) {
	defer func() {
		if recover() != nil {
			decided = false
		}
	}()
	if ctx.Kernel.ScalarSubset(valueSet, refinementsets.MakeRefinedSet(acceptForm)) {
		return true
	}
	if ctx.Kernel.ScalarSubset(valueSet, refinementsets.MakeRefinedSet(refuteForm)) {
		var edge refinementsets.Refinement
		var edgeWords string
		if lower {
			edge = refinementsets.AtLeast(window.Lo)
			edgeWords = "at least " + formatJSNumberLocal(window.Lo)
		} else {
			edge = refinementsets.AtMost(window.Hi)
			edgeWords = "at most " + formatJSNumberLocal(window.Hi)
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+refinementsets.FormatForDiagnostics(valueSet)+"' is not "+
				"assignable to type '"+refinementsets.FormatForDiagnostics(refinementsets.MakeRefinedSet(edge))+"' "+
				"("+symbol+" '"+relation.Param+"', which is "+edgeWords+" here)",
		))
		return true
	}
	return false
}
