// split from ir_loop_stmts.go — building the kernel's loopStmts
// statement, plain and with the head read for the exit refinement

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// loopStmtsStatement wraps a lowered body as the kernel's
// statement-bodied loop, with NO head read: no Written, no Cond, no
// After, no CondCmp — the kernel reads the write set off the statements
// and refines the exit by nothing.
func loopStmtsStatement(body []kernelbridge.IrStatement) kernelbridge.IrStatement {
	return kernelbridge.IrStatement{
		Kind:  kernelbridge.IrStatementLoopStmts,
		Stmts: body,
	}
}

// loopStmtsWithHead is the same statement with the loop's head attached
// for the EXIT refinement.
//
// The head is read through LoopHeadOf, the SAME machinery the
// effect-bodied route uses, so the two routes can never disagree about
// what a head says: `while (i < 10)` gives the truth set and the
// falsity set at slot i, and `while (i < n)` gives the two-slot shape.
//
// The kernel intersects every slot's FALSITY set into its exit and
// tightens the two-slot head's negation on top — sound because a loop
// leaves only when its head FAILS, which is as true of the zero-trip
// run as of any other.
//
// The TRUTH sets ride in Cond, and the certifying walk cuts by them at
// every trip entry before walking the body — a trip runs only when the
// head held. That cut is what the invariant certificate is decided
// against, so a head that reads is worth carrying at both ends, not
// only at the exit.
//
// A head that does not read (a call, a shape no comparison lowers)
// falls back to the plain statement — the exit refines by nothing, the
// certificate cuts by nothing, and the loop is exactly what it was
// before the head was consulted. The head having already passed
// writeAndCallFree is what makes consulting it safe: reading it costs
// nothing and moves nothing.
func loopStmtsWithHead(
	context *LoweringContext, condition *ast.Node,
	body []kernelbridge.IrStatement,
) kernelbridge.IrStatement {
	statement := loopStmtsStatement(body)
	if context == nil || condition == nil {
		return statement
	}
	head, headOk := LoopHeadOf(context, condition)
	if !headOk {
		return statement
	}
	if head.TwoSlot {
		statement.CondCmp = &kernelbridge.IrLoopCondCmp{
			On: head.On, Test: head.Test, OnB: head.OnB,
		}
		return statement
	}
	// the per-binding arrays the kernel reads by index, the head's own
	// slot alone carrying sets
	cond := make([]*refinementsets.RefinedSet, len(context.Bindings))
	after := make([]*refinementsets.RefinedSet, len(context.Bindings))
	if head.On >= 0 && head.On < len(cond) {
		cond[head.On] = head.Cond
		after[head.On] = head.After
	}
	statement.Cond = cond
	statement.After = after
	return statement
}
