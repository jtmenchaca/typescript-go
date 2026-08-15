// split from lowering_to_kernel_ir.go — throw coverage

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// lowerThrowStatement is the walk's `throw` route, exactly as it stood
// inside lowerStatementList. Like the return route it ENDS the statement
// list on every path, so its answer is the list's answer.
//
// `mark` is the statement's own hoist mark, which is all dropHoists needs:
// a declining throw truncates the accumulation back to it so the statement
// leaves no call statement behind.
//
// THE ESCAPING THROW. `throw e` whose parent chain up to this
// body's root passes through no `try` cannot reach a catch of this
// body — it leaves the body outright. A run that threw returns
// NOTHING, so no claim about the returned outcome can be wrong
// about it, and the shape that says so is a return of nothing:
//
//	#ret  := thrown
//	#done := {1}
//
// THROWN, not absent. The run produced no returned value at all,
// and the kernel has a constructor for exactly that — its fourth
// Outcome, distinct from the absent VALUE a bare `return;` writes.
// Sending absent here is what made `if (x) throw new E(); return v`
// serve `v ∪ undefined`: the throw arm's absent merged into the ret
// set, and nothing downstream could tell it from a path that really
// returned undefined. Writing thrown keeps the two apart, and the
// ret row's returned half (KnownStateWire.Returned) then reads the
// real return alone.
//
// The done flag reads exactly as it does for a `return` — the block
// ends here, later statements are dead, and the apply route's
// allReturned reading sees a path that left.
//
// A throw INSIDE a try no longer declines by default. The old
// reasoning — raising the flag would make the catch's writes
// invisible — is about a catch that CONTINUES this statement list.
// Under the try route's branchBoth the catch is a SIBLING arm,
// walked from the state as it stood, so the flag raised in the try
// arm cannot reach it. ThrowCoveredByItsTry is the test for
// exactly that shape; every other enclosing try keeps the decline.
func lowerThrowStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	mark int,
) ([]kernelbridge.IrStatement, bool) {
	dropHoists := func() { DropHoistedFrom(context, mark) }
	if context.Result == nil {
		NoteDeclinedConstruct(context, "throw with no result slot")
		dropHoists()
		return nil, false
	}
	// A throw INSIDE a try is sound too where the enclosing try's
	// own lowering covers it — ThrowCoveredByItsTry holds the whole
	// argument. A throw under any OTHER try keeps the decline, and
	// the report names it "throw inside try" so the row points at
	// the construct.
	if ThrowReachesATry(s) && !ThrowCoveredByItsTry(s) {
		NoteDeclinedConstruct(context, "throw inside try")
		dropHoists()
		return nil, false
	}
	// the thrown EXPRESSION's own effects: `throw new E(x)` runs a
	// constructor that may move whatever the arguments mention. The
	// mention havoc covers exactly that — the floor's own rule —
	// and with it the whole statement is READ: control exact (ret
	// absent, done raised — a throw produces no value, and code
	// after the call only runs when no throw happened), value
	// nothing, effects covered. Only an unenumerable expression
	// keeps the porous mark.
	if slots, enumerable := havocSlotsOfStatement(context, s); enumerable {
		out = append(out, havocAssignments(slots)...)
	} else {
		NoteFirstHavoc(context, "throw")
	}
	out = append(out, kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: context.Result.Ret,
		Effect: kernelbridge.ThrownConst(),
	})
	out = append(out, kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: context.Result.Done,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
	})
	return out, true
}

// ThrowReachesATry is whether a `throw` transfers to a `catch` of THIS
// body rather than leaving it — the one structural question the escaping
// throw's lowering turns on.
//
// Syntactic, by the parent chain: from the throw upwards, a `try`
// reached before the body's root means the throw goes to that try's
// catch (or through its finally and on out, which is the same thing for
// this question: statements of this body run after the throw, and the
// walk's done flag cannot spell "ran the catch, then ended"). The root
// is the enclosing function — or the source file, for the top-level
// statement lists the lowering tests hand in.
//
// A throw inside a nested FUNCTION is not this body's throw at all, and
// never reaches here: the statement routes never descend into one.
//
// The answer is TRUE for a chain the walk cannot follow (a detached
// node with no parent set), because the refusal only ever costs
// coverage while a wrong false would raise the done flag over a catch.
func ThrowReachesATry(throw *ast.Node) bool {
	if throw == nil {
		return true
	}
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) || ast.IsSourceFile(node) {
			return false
		}
	}
	// no parent chain at all: nothing was read, so nothing is claimed
	return true
}

// ThrowCoveredByItsTry is whether a `throw` inside a try is one the
// enclosing try's OWN lowering already covers — the question that turns
// "throw inside try" from a decline into an ordinary read.
//
// THE ARGUMENT. LowerTryStatement builds one statement:
//
//	branchBoth
//	  then: the try block's statements, lowered whole
//	  else: havoc(every slot the try block could write)
//	        catch parameter := unknown
//	        the catch block's statements
//
// and branchBoth's contract (kernelbridge/loop_questions.go) is that
// BOTH arms walk from the state as it stood and their exits join. The
// arms are siblings, not a sequence. So whatever the then arm writes —
// including the done flag — is not what the else arm reads. That is the
// exact hole in the old decline's reasoning: it argued that raising the
// flag at a throw would gate the catch's own statements behind the
// flag's falsity, which is true only where the catch CONTINUES the
// statement list the throw sat in. Under this route it does not.
//
// What the two arms cover, run by run:
//
//   - a run that completed the try normally IS the then arm, and the
//     throw statement never executed on it. The then arm lowering the
//     throw as `ret := absent; done := {1}` and ending the block costs
//     that run nothing, because the run did not reach the throw.
//   - a run that threw at statement k ran statements 1..k-1 and then the
//     catch. Its state at catch entry differs from the entry state only
//     in slots written by that prefix. The else arm's prefix havoc is
//     havocSlotsOfStatement over the WHOLE try block, which is a
//     superset of any prefix's write set, so the catch walks from a
//     state weaker than the real one — for every k, the explicit throw's
//     own k included. That is what already made the route sound for an
//     interrupted run, and an explicit `throw` is only one more value of
//     k, not a new kind of interruption.
//
// So the then arm is right about the runs it stands for and the else arm
// covers the rest, which is the whole obligation.
//
// THE THROW'S OWN EXPRESSION. `throw new E(x)` runs a constructor before
// control transfers. The then arm's throw route havocs the statement's
// mention set (havocSlotsOfStatement over the throw) before writing ret
// and done, so that path carries the effects. The else arm carries them
// too, by the same superset reasoning: the throw statement is inside the
// try block, so its mentions are in the block's own enumeration. Nothing
// the expression could move escapes both arms.
//
// THE SHAPE THIS IS TRUE OF. Only the try LowerTryStatement actually
// serves: a catch clause present and no finally. A `finally` runs on
// every completion and the route declines it outright, so a throw under
// one has no branchBoth above it at all; a try with no catch does not
// catch the throw, which then leaves the body through the enclosing
// frames — a different question the escaping-throw route is not in a
// position to answer here. Both keep the decline.
//
// The nearest enclosing try is the one that catches, so only that one is
// consulted; a throw nested in an inner try under an outer one is the
// inner try's business. The walk stops at a function boundary for the
// same reason ThrowReachesATry does.
func ThrowCoveredByItsTry(throw *ast.Node) bool {
	if throw == nil {
		return false
	}
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			tryStmt := node.AsTryStatement()
			// a throw sitting in the CATCH block is not caught by this try
			// at all — it leaves through the enclosing frames, and the arm
			// structure above says nothing about it
			if tryStmt.CatchClause != nil && tryStmt.CatchClause.Contains(throw) {
				return false
			}
			return tryStmt.CatchClause != nil && tryStmt.FinallyBlock == nil
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
}
