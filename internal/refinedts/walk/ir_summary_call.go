// Call statements for the flow IR: a call to a callee that already has
// a COMPILED SUMMARY lowers to IrStatementCall rather than inlining the
// callee's body into fresh slots.
//
// The two routes differ in where the work happens. Inlining (see
// ir_inline_call.go) copies the callee's statements into the caller's
// slot vector at every site — the slots grow, the lowering repeats, and
// the callee's body is walked again inside every caller. A summary is
// compiled ONCE, kernel-side, and applied: the call statement carries
// the argument effects in and names where the out-states land, and the
// kernel splices the compiled program. The summary quantifies over all
// entries, so one compile serves every call.
//
// Which route a site takes is decided here: a callee whose declaration
// the registry answers for takes the summary route; everything else
// falls through to the existing inlining, exactly as before.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// (The lowered-body type is LoweredSummary, kernel_summaries.go — one
// type serves the memoized door and the worker.)

// summaryCalleeOf resolves a call expression's callee to the
// declaration the registry keys by, or nil. The lowering context's own
// ResolveCallee is the resolution — the same one the inlining route
// uses, so the two routes never disagree about which declaration a name
// stands for.
func summaryCalleeOf(context *LoweringContext, call *ast.Node) *ast.Node {
	if context.ResolveCallee == nil {
		return nil
	}
	if !ast.IsCallExpression(call) {
		return nil
	}
	return context.ResolveCallee(call.AsCallExpression().Expression)
}

// summaryCallStatement builds the call statement for a resolved callee
// with a compiled summary: each argument lowers as an effect over the
// caller's bindings (any that does not declines the whole call), and
// Rets names the caller slot the callee's RETURN out-state writes — -1
// everywhere else, which drops those out-states.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call statement whose value nothing reads.
func summaryCallStatement(context *LoweringContext, call *ast.Node, target int) (kernelbridge.IrStatement, bool) {
	if context.Flow == nil || context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return kernelbridge.IrStatement{}, false
	}
	blob, has := SummaryBlobFor(context.Flow, callee)
	if !has {
		return kernelbridge.IrStatement{}, false
	}
	outIndex, shapeOk := SummaryOutShapeFor(context.Flow, callee)
	if !shapeOk {
		return kernelbridge.IrStatement{}, false
	}
	callExpr := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpr.Arguments != nil {
		callArguments = callExpr.Arguments.Nodes
	}
	parameters := callee.Parameters()
	if len(callArguments) > len(parameters) {
		return kernelbridge.IrStatement{}, false
	}
	// an argument that WRITES would move the caller's state on the way
	// in, which the effect grammar does not carry
	for _, argument := range callArguments {
		if ast.IsSpreadElement(argument) || ContainsWrite(argument) {
			return kernelbridge.IrStatement{}, false
		}
	}
	// one entry effect per callee SLOT — the callee's arity is its whole
	// binding vector (see buildSummaryBlob), so the parameters come from
	// the call, every local and the result slot enter absent, and the
	// done flag enters {0}: exactly the entry states the apply side
	// sends, so the spliced compile and the direct apply agree
	calleeShape, shapeKnown := LowerSummaryBody(context.Flow, callee)
	if !shapeKnown {
		return kernelbridge.IrStatement{}, false
	}
	args := make([]kernelbridge.LoopEffect, 0, calleeShape.SlotCount)
	for index, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return kernelbridge.IrStatement{}, false
		}
		if index >= len(callArguments) {
			args = append(args, kernelbridge.AbsentConst())
			continue
		}
		argument := callArguments[index]
		effect, ok := RhsEffect(context, SortOfArg(context, argument), argument)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		args = append(args, effect)
	}
	for len(args) < calleeShape.SlotCount {
		if len(args) == calleeShape.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	// Rets: -1 says nothing reads that out-state. Only the return
	// out-state maps, and only where the site has a slot for it.
	rets := make([]int, outIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[outIndex] = target
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(callee, blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// SummaryCallStatementOf is the lowering-side entry: a call expression
// STATEMENT (`f(…)`), or a call assigned into a tracked slot (`x =
// f(…)`, `const x = f(…)`), where the callee has a compiled summary.
// Declines where the callee has none, where an argument does not lower,
// or where an assignment's target has no slot — and the statement then
// takes the inlining route, exactly as before.
func SummaryCallStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	// `f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if ast.IsCallExpression(e) {
			call, ok := summaryCallStatement(context, e, -1)
			if !ok {
				return nil, false
			}
			return []kernelbridge.IrStatement{call}, true
		}
	}
	target, rhs, ok := callAssignmentShapeOf(context, statement)
	if !ok {
		return nil, false
	}
	head := Unwrapped(rhs)
	if !ast.IsCallExpression(head) {
		return nil, false
	}
	call, callOk := summaryCallStatement(context, head, target)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// callAssignmentShapeOf is the `let x = e` / `x = e` shape both call
// routes read: the tracked target slot and the right side. Declines
// where the target has no slot.
func callAssignmentShapeOf(context *LoweringContext, statement *ast.Node) (target int, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return 0, nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return 0, nil, false
		}
		index, found := IndexOf(context, d.Name())
		if !found {
			return 0, nil, false
		}
		return index, d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return 0, nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return 0, nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return 0, nil, false
	}
	index, found := IndexOf(context, bin.Left)
	if !found {
		return 0, nil, false
	}
	return index, bin.Right, true
}
