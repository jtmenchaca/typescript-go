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

// SummaryCallOrHavoc is the one call-lowering door every route uses: a
// callee with a compiled summary takes the call statement, and a callee
// whose own summary build is STILL IN FLIGHT — a recursive call, which
// cannot splice itself — takes the HAVOC route instead of declining.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads. The answer is the statements the
// site contributes: one call statement, one havoc assignment, or none
// at all for a bare cycle call.
func SummaryCallOrHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if statement, ok := summaryCallStatement(context, call, target); ok {
		return []kernelbridge.IrStatement{statement}, true
	}
	return summaryCycleHavoc(context, call, target)
}

// summaryCycleHavoc is the RECURSION floor: a callee that resolves but
// has no blob BECAUSE its own build is in flight (SummaryCycleInFlight)
// still lowers — writing TOP into whatever the call's value lands in is
// sound unconditionally, and it is what the kernel's own walk answers
// for a call through an empty summary. Without this a body containing a
// recursive call would decline whole, and the recursive declaration
// itself would never compile.
//
// The route never asks SummaryOutShapeFor: that would re-enter the
// in-flight lowering it is standing in for.
//
// No leaf-slot havoc is needed beyond the target. Only SCALAR arguments
// lower into calls — the argument loop in summaryCallStatement below
// declines a spread or a writing argument, and a record or array
// argument has no scalar effect for RhsEffect to build, so it declines
// the call before any of this — and a scalar passes by value, so a
// callee cannot write back through it.
func summaryCycleHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if context.Flow == nil {
		return nil, false
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return nil, false
	}
	// the in-flight bit is asked FIRST: it is a mutex read of the
	// registry's building set, where SummaryBlobFor would start a build
	// for any callee that has none yet
	if !SummaryCycleInFlight(callee) {
		return nil, false
	}
	// a callee already holding a blob took the call statement above; if
	// it did not, its blob is absent for a reason other than the cycle
	// (a declined body), and the havoc floor does not apply
	if _, has := SummaryBlobFor(context.Flow, callee); has {
		return nil, false
	}
	// a bare cycle call contributes no statement: nothing reads its
	// value, and the havoc has nowhere to land
	if target < 0 {
		return nil, true
	}
	return []kernelbridge.IrStatement{{
		Kind:   kernelbridge.IrStatementAssign,
		Target: target,
		Effect: unknownEffect,
	}}, true
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
	// the callee's parameters no longer map 1:1 onto entries: a type-
	// literal parameter EXPANDS to one entry per member. The entry list is
	// built by walking the declared parameters through the very expansion
	// the layout used (SummaryParameterEntries), so a drift between the
	// two is impossible — one function answers both.
	args := make([]kernelbridge.LoopEffect, 0, calleeShape.SlotCount)
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntries(parameter)
		if !entriesOk {
			return kernelbridge.IrStatement{}, false
		}
		members, expanded := recordParamMembersOf(parameter)
		if expanded {
			// a missing argument leaves every leaf absent — the same "entered
			// absent" the scalar case gives an omitted argument
			if index >= len(callArguments) {
				for range entries {
					args = append(args, kernelbridge.AbsentConst())
				}
				continue
			}
			leafEffects, leavesOk := recordArgumentEffects(context, members, callArguments[index])
			if !leavesOk {
				return kernelbridge.IrStatement{}, false
			}
			args = append(args, leafEffects...)
			continue
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

// recordArgumentEffects maps ONE argument onto an expanded parameter's
// leaf entries, IN THE PARAMETER'S MEMBER ORDER — the order
// SummaryParameterEntries laid the entries out, so effect j fills member
// j's slot whatever order the argument spelled its keys.
//
// Two argument shapes lower, and nothing else:
//
//	(a) an OBJECT LITERAL whose keys are exactly the members — each
//	    member's value lowers as an ordinary effect through the shared
//	    RHS grammar, under the member's own sort;
//	(b) a FLATTENED RECORD LOCAL of exactly those leaves — each member
//	    reads the caller slot spelled "q.<member>" as a var.
//
// Anything else declines the whole call: a call's result, a parameter
// the caller itself holds unexpanded, a literal with an extra or missing
// key, a spread. There is no partial fill — a leaf left at its absent
// entry state would read inside the callee as undefined, which is not
// what the caller passed.
func recordArgumentEffects(
	context *LoweringContext,
	members []recordParamMember,
	argument *ast.Node,
) ([]kernelbridge.LoopEffect, bool) {
	head := Unwrapped(argument)
	// (a) `f({ lo: 1, hi: n })`
	if ast.IsObjectLiteralExpression(head) {
		valueOfKey := map[string]*ast.Node{}
		for _, property := range head.AsObjectLiteralExpression().Properties.Nodes {
			if !ast.IsPropertyAssignment(property) {
				return nil, false
			}
			assignment := property.AsPropertyAssignment()
			if !ast.IsIdentifier(assignment.Name()) || assignment.Initializer == nil {
				return nil, false
			}
			key := assignment.Name().Text()
			if _, already := valueOfKey[key]; already {
				return nil, false
			}
			valueOfKey[key] = assignment.Initializer
		}
		// EXACTLY the members: an extra key is a shape the parameter did
		// not declare, a missing one leaves a leaf unwritten
		if len(valueOfKey) != len(members) {
			return nil, false
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			value, has := valueOfKey[member.Key]
			if !has {
				return nil, false
			}
			effect, ok := RhsEffect(context, member.Sort, value)
			if !ok {
				return nil, false
			}
			out = append(out, effect)
		}
		return out, true
	}
	// (b) `f(q)` where q is a flattened record local of exactly these
	// leaves. leafSlotsUnder is the same reader the record-to-record
	// assignment uses, so "the caller flattened q" and "q's leaves have
	// slots" are one question.
	if ast.IsIdentifier(head) {
		leaves, leavesOk := leafSlotsUnder(context, head.Text())
		if !leavesOk || len(leaves) != len(members) {
			return nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			slot, has := slotOfPath[member.Key]
			if !has {
				return nil, false
			}
			out = append(out, varEffect(slot))
		}
		return out, true
	}
	return nil, false
}

// SummaryCallStatementOf is the lowering-side entry: a call expression
// STATEMENT (`f(…)`), or a call assigned into a tracked slot (`x =
// f(…)`, `const x = f(…)`), where the callee has a compiled summary —
// or, where the callee's own build is in flight, the havoc floor.
// Declines where the callee resolves to nothing, where an argument does
// not lower, or where an assignment's target has no slot — and the
// statement then takes the inlining route, exactly as before.
func SummaryCallStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	// `f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if ast.IsCallExpression(e) {
			return SummaryCallOrHavoc(context, e, -1)
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
	return SummaryCallOrHavoc(context, head, target)
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
