// split from ir_opaque_havoc.go — the call site's havoc

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the call site's havoc ───────────────────────────────────────── */

// OpaqueCallHavoc is the CALL half of the same rule, and it reuses
// exactly the enumerator above: a call whose callee does not resolve, or
// resolves without a blob — a generator, a class method the registry
// declined, `x.y.then(cb)`, an unmodeled library function — lowers as
//
//	target := unknown
//	<every flattened-local leaf mentioned in the receiver or arguments>
//	         := unknown
//
// and nothing else. The callee may compute anything and may mutate any
// object it was handed; both of those are what the two lines above
// claim, and the call's own value is `unknown`, which claims nothing.
//
// `target` is the caller slot the call's value lands in, or -1 where
// nothing reads it (a bare call statement) — a bare call still havocs
// the objects it was handed.
//
// The RECURSION case (summaryCycleHavoc, ir_summary_call.go) is this
// same rule reached down a different path and it stays where it is: it
// answers before this one, and it is the one place that must NOT ask the
// registry for a shape it is standing in for.
func OpaqueCallHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	// the same nil/shape guard OpaqueCallHavocNamed repeats below: it must
	// run BEFORE havocCallName touches the call, which assumes a real call
	// expression and does not check
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	return OpaqueCallHavocNamed(context, call, target, havocCallName(call))
}

// OpaqueCallHavocNamed is OpaqueCallHavoc with the FIRST-HAVOC NAME
// supplied by the caller instead of derived from the callee's own
// spelling. A call site that sits ahead of every body statement — a
// defaulted parameter's own initializer — is not the callee's call at
// all from the outcome report's point of view; it is that OTHER
// construct running code, so the name it records should say that
// construct, not "call <callee>". Every ordinary body-statement call
// keeps its callee-derived name through OpaqueCallHavoc above, which is
// this function with that name supplied.
func OpaqueCallHavocNamed(context *LoweringContext, call *ast.Node, target int, construct string) ([]kernelbridge.IrStatement, bool) {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	callExpr := call.AsCallExpression()
	// the same impossibilities the statement route refuses: a bare
	// `eval(…)` writes bindings no syntax names, and an argument carrying
	// a throw or a labelled break is a statement-shaped thing inside an
	// expression (an IIFE body) the enumeration cannot bound
	if !havocEnumerable(call) {
		return nil, false
	}
	// the RECEIVER and every ARGUMENT, through the ONE enumerator: each is
	// handed to the callee, and a flattened local handed out may come back
	// with any leaf moved. Running the statement enumerator over them
	// rather than a mention-only scan of its own is what makes an ARGUMENT
	// that is itself a callback — `xs.forEach(v => { total += v; })`,
	// whose enclosing statement took this route — havoc `total` too: the
	// callee may call the callback, and the callback writes this body's
	// slot.
	slots := map[int]struct{}{}
	for _, part := range append([]*ast.Node{callExpr.Expression}, callArgumentsOf(callExpr)...) {
		if part == nil {
			continue
		}
		partSlots, ok := havocSlotsOfStatement(context, part)
		if !ok {
			return nil, false
		}
		for slot := range partSlots {
			slots[slot] = struct{}{}
		}
	}
	out := havocAssignments(slots)
	// the call's VALUE, where the site has a slot for it. Appended after
	// the leaf havocs so the target's own write is last — it is the
	// statement's answer, and a target that is ALSO a mentioned leaf
	// (`p.a = f(p)`) then keeps one write rather than two.
	if target >= 0 {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: target,
			Effect: unknownEffect,
		})
	}
	NoteFirstHavoc(context, construct)
	return out, true
}

// callArgumentsOf is a call's argument list, or nil — the one spelling
// the havoc route needs and the AST states as an optional node list.
func callArgumentsOf(call *ast.CallExpression) []*ast.Node {
	if call.Arguments == nil {
		return nil
	}
	return call.Arguments.Nodes
}
