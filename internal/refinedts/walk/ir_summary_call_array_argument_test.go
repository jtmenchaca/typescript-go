// Pins the SOUNDNESS half of construct (1)
// (ir_array_argument_use_diagnosis_test.go's own diagnosis, AGENT-BRIEF's
// WHOLE-ARRAY ARGUMENT ADMISSION): once a whole-array argument is
// admitted as a use usesAreAllArrayFormsFrom accepts, a caller's `tree`
// parameter flattens for the WHOLE body — including a position that
// reaches summaryCallStatement's OWN composed-call route (HoistCallEffect,
// ir_call_hoist.go — any call in EXPRESSION position, not the bare-
// statement position the loop diagnosis exercises, which the opaque
// floor's syntax-driven mention rule already covers safely).
//
// summaryCallStatement threads an array-typed argument's "tree.len"/
// "tree.elem" INTO the callee's entries (ir_summary_call_statement.go's
// arrayParamSlotsIn branch) but maps NO array-entry EXIT back through
// Rets — so a callee that writes the array (`otherUpdate` here pushes
// onto it) leaves the caller's OLD length claim standing after the call.
// This pin proves the unsound reading directly: a post-call
// `tree.length` read must not answer the PRE-call exact length once the
// callee could have grown the array.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// arrayArgumentCallStatementProgram builds a caller whose ONE statement
// hoists otherUpdate's call through HoistCallEffect (an expression
// position, `total = total + otherUpdate(...)`, forces the hoist since
// the call is not the statement's own RHS) — the shape that reaches
// summaryCallStatement directly, unlike a bare `otherUpdate(tree, 1);`
// statement (which the opaque floor already handles by syntax alone).
func arrayArgumentCallStatementProgram(t *testing.T, calleeBody string) (*FlowContext, *ast.Node) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function otherUpdate(tree: number[], cur: number): number {
			`+calleeBody+`
			return cur;
		}
		function f(tree: number[]): number {
			let total = 0;
			total = total + otherUpdate(tree, 1);
			return tree.length;
		}
	`)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	callee := entryEnvFunctionNamed(t, p, "otherUpdate")
	symbol := p.Checker.GetSymbolAtLocation(callee.AsFunctionDeclaration().Name())
	if symbol == nil {
		t.Fatalf("no symbol for otherUpdate")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: callee}
	return ctx, entryEnvFunctionNamed(t, p, "f")
}

// TestSummaryCallStatement_WholeArrayArgumentThroughAPushingCalleeMustNotKeepTheStaleLength
// is the soundness pin construct (1)'s two-half fix depends on. Admitting
// the whole-array argument use WITHOUT the exit-side fix would let this
// case flatten `tree` and then serve `tree.length` off the PRE-call
// state — wrong, since otherUpdate pushes. The fix must make the
// post-call length read either unknown/TOP (havoc) or the real grown
// length (thread-back) — anything BUT the stale pre-call exact value.
func TestSummaryCallStatement_WholeArrayArgumentThroughAPushingCalleeMustNotKeepTheStaleLength(t *testing.T) {
	ctx, fn := arrayArgumentCallStatementProgram(t, `tree.push(cur);`)
	lowered, ok := RelowerSummaryBody(ctx, fn)
	if !ok {
		t.Fatalf("f's body declined whole — this pin needs at least a lowering to inspect")
	}
	// Walk the lowered statements directly: find the hoisted call
	// statement (IrStatementCall) and the FINAL statement (the return's
	// own read, which should be `varState tree.len` or unknown/TOP after
	// the fix — never a bare pre-call CONST/varState untouched by any
	// join/havoc following the call).
	var callIndex = -1
	for i, statement := range lowered.Stmts {
		if statement.Kind == kernelbridge.IrStatementCall {
			callIndex = i
		}
	}
	if callIndex < 0 {
		t.Fatalf("f's lowering carries no IrStatementCall — the whole-array argument never reached the composed-call route (stmts=%+v)", lowered.Stmts)
	}
	// the SOUNDNESS requirement: some statement AFTER the call must write
	// (havoc or thread-back) tree's len slot — the call statement's own
	// Rets row for it, OR a following assign/join statement. Locate
	// tree's len slot by finding what the call's OWN Args threaded in
	// (the entry effect at the array parameter's position is a
	// varState/var read of tree's len slot — that slot index is the one
	// this pin must see touched again afterward).
	var treeLenSlot = -1
	call := lowered.Stmts[callIndex]
	for _, effect := range call.Args {
		if effect.Kind == kernelbridge.LoopEffectVarState || effect.Kind == kernelbridge.LoopEffectVar {
			// the FIRST var/varState-kind arg is tree's len slot (tree is
			// parameter 0, and its len entry is the first of its pair/members)
			treeLenSlot = effect.Index
			break
		}
	}
	if treeLenSlot < 0 {
		t.Fatalf("could not find tree's len slot among the call's own Args: %+v", call.Args)
	}
	touchedAfter := false
	if treeLenSlot < len(call.Rets) && call.Rets[treeLenSlot] == treeLenSlot {
		touchedAfter = true
	}
	for i := callIndex + 1; i < len(lowered.Stmts); i++ {
		statement := lowered.Stmts[i]
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == treeLenSlot {
			touchedAfter = true
		}
	}
	t.Logf("callIndex=%d treeLenSlot=%d rets=%v touchedAfter=%v stmts=%+v", callIndex, treeLenSlot, call.Rets, touchedAfter, lowered.Stmts)
	if !touchedAfter {
		t.Errorf("tree's len slot (%d) is never written or havocked after the call statement — a post-call tree.length read would still answer the PRE-call exact length even though otherUpdate pushes onto tree: UNSOUND", treeLenSlot)
	}
}
