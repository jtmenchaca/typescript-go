// A whole function body lowered for the SUMMARY compiler.
//
// The whole-body route (kernel_summaries.go) lowers per call, keyed by
// the sorts the arguments supplied — a different sort vector is a
// different lowering. A SUMMARY quantifies over all entries instead, so
// it is compiled once per declaration and the sorts cannot come from
// any call: they are read from the declaration's own parameter type
// annotations, which every entry shares.
//
// What this adds beyond the per-call lowering is the callee TABLE: a
// body whose call sites lower to IrStatementCall carries one blob per
// callee, and the table rides out beside the statements so the compile
// can splice each callee's already-built summary.
//
// The lowering is also where a body's OUTCOME is recorded — complete,
// porous, or declined naming the construct it refused
// (summary_outcome.go holds the store). This is the one place the fate
// of a body is known, so it is the one place that reports it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// lowerSummaryBody lowers a declaration's whole body for the summary
// compiler: the parameter slots first (the compiler's arity), then the
// locals' slots, then the done flag and the result slot, with the
// callee table the body's call statements built. LowerSummaryBody
// (kernel_summaries.go) is the memoized door in front of it.
//
// Total-or-decline, exactly as every other lowering here: a body that
// leaves the grammar answers false and the caller keeps its existing
// route.
func lowerSummaryBody(ctx *FlowContext, declaration *ast.Node) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, declaration, nil, nil)
}

// lowerArrowSummary lowers an ARROW (or function expression) ARGUMENT
// closure-converted: its declared parameters first, then one entry per
// READ-ONLY capture in the scan's order, then the locals, the done flag
// and the result slot. Everything past the extra entries is the
// ordinary body lowering — a capture is just another entry as far as
// the compiled summary is concerned, which is exactly why closure
// conversion needs nothing new kernel-side.
//
// The declared parameters wear the SITE's sorts rather than the
// declaration's annotations. A top-level declaration's summary must read
// sorts from its own annotations, since it quantifies over callers a
// lowering cannot see; an arrow argument has exactly ONE call site, and
// that site fills entry 0 with a `var` of the receiver's element slot
// whose sort the caller's layout already carries. Reading the sort off
// the annotation instead would make every unannotated `x => x + 1`
// unknown-sorted and decline its own arithmetic — the parameter would be
// the one entry whose sort the site knows and the summary refuses. The
// captures already ride their caller slots' sorts for the same reason;
// this puts the parameters on the same footing.
//
// The async gate is NOT consulted here beyond what summaryLowerable
// says: a lowered async body's #ret holds the SETTLED inner value (the
// ret-as-inner convention), so an async arrow converts exactly like a
// sync one and the awaiting site adds nothing.
func lowerArrowSummary(
	ctx *FlowContext,
	arrow *ast.Node,
	parameters []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, arrow, parameters, captures)
}

// parameterSlotSort is the sort and typeof evidence ONE declared
// parameter entry wears when the call site knows them. A nil entry list
// (the declaration route) leaves every parameter reading its own
// annotation.
//
// Array marks a declared parameter the site is filling as a flattened
// ARRAY — the len/elem PAIR ir_array_slots.go's local carries, not one
// scalar value. Sort/TypeofTag then describe the ELEMENT half only (the
// length is always number); Array is what tells
// summaryParameterEntries to accept the pair layout at this position
// instead of refusing it as one entry short.
type parameterSlotSort struct {
	Sort      BindingKind
	TypeofTag TypeofTag
	Array     bool
}

// lowerSummaryBodyWithCaptures is the one lowering both doors share:
// nil parameters and nil captures is the plain declaration route, a
// supplied pair is the closure-converted arrow route. The arrow route
// comes through lowerArrowSummary, which records under the ARROW node —
// the same declaration identity the outcome store keys by.
//
// THE OUTCOME IS RECORDED HERE, and only here: this is the one place a
// body's fate is known. Three answers, one per body:
//
//   - DECLINED, naming the first CONSTRUCT the lowering refused ("a
//     generator body", "a binding-pattern parameter") — the reason the
//     worker returned;
//   - POROUS, naming the first construct that HAVOCKED, where the
//     lowering succeeded but context.FirstHavoc is non-empty;
//   - COMPLETE otherwise — the lowering read every statement.
//
// The outcome store ranks a later record against the one it holds
// (RecordSummaryOutcome), so the fixpoint's re-lowerings never talk a
// settled body back down.
func lowerSummaryBodyWithCaptures(
	ctx *FlowContext,
	declaration *ast.Node,
	parameterSorts []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	summary, havoc, declined, ok := lowerSummaryBodyReporting(ctx, declaration, parameterSorts, captures)
	name := summaryBodyName(declaration)
	if !ok {
		// A declaration with NO BODY is not a body: an overload signature
		// stands in front of the implementation that follows it, and an
		// abstract member stands in front of the subclasses that supply
		// it. Neither has statements for the lowering to read, so neither
		// is a body the outcome store should hold a row for — recording
		// one puts scaffolding in the denominator and then declines it.
		// The implementation and the subclass bodies record their own.
		if isBodylessSignature(declaration) {
			return LoweredSummary{}, false
		}
		RecordSummaryOutcome(checkerOf(ctx), declaration, name, SummaryDeclined, declined)
		return LoweredSummary{}, false
	}
	if havoc != "" {
		RecordSummaryOutcome(checkerOf(ctx), declaration, name, SummaryPorous, havoc)
		return summary, true
	}
	RecordSummaryOutcome(checkerOf(ctx), declaration, name, SummaryComplete, "")
	return summary, true
}

// isBodylessSignature answers whether a declaration is a SIGNATURE
// rather than a body: a function, method, or constructor declaration
// with no body at all.
//
// TypeScript spells two of these. An OVERLOAD signature sits directly in
// front of the implementation that carries the statements — `create(a):
// T;` twice, then `create(a, b?, c?): T { … }` — and the implementation
// is the body every call really runs. An ABSTRACT member (or a member of
// an ambient class or an interface) has no implementation in this file
// at all; the bodies live in the subclasses, which are declarations of
// their own.
//
// Either way there are no statements here to read, so the enumeration
// counts the implementation once instead of counting each signature and
// then declining it for the thing it never had.
func isBodylessSignature(declaration *ast.Node) bool {
	if declaration == nil || declaration.Body() != nil {
		return false
	}
	switch declaration.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration,
		ast.KindConstructor, ast.KindMethodSignature,
		ast.KindGetAccessor, ast.KindSetAccessor:
		return true
	}
	return false
}

// summaryBodyName spells a lowered body the way the report spells
// contracts: its declared name, or "" for an anonymous arrow or function
// expression — the outcome store keys on the NODE, so an unnamed body
// still has one record; the name is only what the tally prints.
func summaryBodyName(declaration *ast.Node) string {
	if declaration == nil {
		return ""
	}
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return ""
	}
	return name.Text()
}
