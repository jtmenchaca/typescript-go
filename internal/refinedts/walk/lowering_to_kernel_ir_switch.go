// split from lowering_to_kernel_ir.go — switch lowering

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LowerSwitch is a `switch (x) { case k: … }` as a CHAIN of equality
// branches: each case's test with its own statements as the then-arm and
// the rest of the chain as the else-arm, down to the default arm, which
// becomes the innermost else. Exactly the desugaring the language's own
// semantics gives a switch whose every reached run ends in break or
// return.
//
// CLAUSE ORDER IS NOT TEST ORDER. A switch evaluates the discriminant
// once and then compares it against EVERY case label, in clause order,
// whatever position the default clause sits in; only when no label
// matched does the default's statements run. So the chain does not need
// the default to be the LAST clause — it needs the default to be the
// innermost ELSE, which is where "no label matched" lands however the
// clauses were written. `switch (k) { default: d(); break; case 1:
// a(); break; }` and the same two clauses swapped are the same runs,
// and both lower to `if k===1 then a() else d()`. The arms are
// therefore collected by what they TEST — one list of case arms and one
// default arm — rather than by where their clause sat.
//
// FALLTHROUGH IS CONCATENATION. A case that does not end its run
// continues into the next clause's statements, so the arm a matching
// run actually executes is its own statements followed by the next
// clause's, and the next's, until a clause that ends the run. That
// concatenation is what each arm lowers here: `case 1: case 2: f();
// break;` gives the empty clause 1 the statements of clause 2, and
// `case 1: g(); case 2: f(); break;` gives clause 1 `g(); f();`. A run
// that reaches the end of the clause list ends by LEAVING THE SWITCH —
// there is nothing after the last clause to fall into — so it lowers
// as its concatenated statements with no exit raise, exactly like an
// arm whose last statement is a bare break.
//
// Total-or-decline: any arm whose statements or label do not lower
// declines the whole switch, and the statement then takes its former
// route (which is nothing — a switch has no other IR lowering).
func LowerSwitch(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsSwitchStatement(statement) {
		return nil, false
	}
	switchStmt := statement.AsSwitchStatement()
	discriminant := switchStmt.Expression
	// EVERY REFUSAL BELOW NAMES ITSELF. A switch that declines here falls
	// to the havoc floor, which refuses any contained `return` and then
	// reports "return inside switch" — a row that names the return rather
	// than the thing the switch route actually could not read. The names
	// set here are what the floor's DeclinedHavocConstruct reports
	// instead, so each row points at the construct to go and build a
	// reading for.
	_, discriminantTracked := IndexOf(context, discriminant)
	// A DISCRIMINANT THAT RUNS CODE — `switch (await f())`,
	// `switch (this.kind())` — hoists its call to a temp ahead of the
	// chain. The evaluation-order argument is the branch heads' own: the
	// discriminant runs UNCONDITIONALLY and FIRST, before any clause and
	// before the switch decides anything, so the statement position ahead
	// of the chain reproduces source order. It also settles the repeated
	// read: the chain mentions the discriminant once per label, and a call
	// left in place would spell one run per label where the source runs it
	// once. After the hoist every label reads the same temp.
	//
	// THE TEMP CARRIES THE CALLEE'S SORT, and that is what makes the
	// LABELS testable. TestOf reads a strict equality under the SLOT'S
	// sort — number against a number literal, string against a string
	// literal — so a temp wearing the number or the string sort gives
	// every label equality a wire form, and the chain below tests them
	// exactly as `if (k === 1)` would. A temp whose callee's return type
	// resolved to neither sort still wears BindingKindUnknown; that one
	// has no label equality on the wire and keeps the untested chain,
	// where the call runs once, every arm's effects ride, and the arms
	// join, which a concrete run's one arm is admitted by.
	//
	// THE SORT CHANGES NOTHING ABOUT WHEN ANYTHING RUNS. The hoist's own
	// statement lands in context.Hoisted, which the caller flushes ahead
	// of the chain it appends, and every label reads that one temp — so
	// the discriminant still runs once, unconditionally, before the
	// chain. All the sort decides is whether the label equalities have
	// wire forms.
	//
	// The slot is kept here because the guard below tests it DIRECTLY. A
	// hoisted temp is spelled `#hoist<n>:`, which resolves to no name on
	// purpose, so an equality synthesized over the discriminant NODE
	// would be re-resolved by IndexOf and find nothing; the guard builds
	// its test from this index instead, and the # spelling stays
	// uncollidable with any source name.
	hoistedSlot, hoisted := 0, false
	// hoistedTestable is the temp that has a label equality on the wire:
	// hoisted AND wearing a sort the equality tests read. A temp wearing
	// BindingKindUnknown — a callee whose return type resolved to neither
	// sort — leaves this false and takes the untested chain below, the
	// same route it took before the temp carried a sort at all.
	hoistedTestable := false
	if !discriminantTracked {
		if !OpaqueTestableCondition(discriminant) {
			if hoistedSlot, hoisted = ConditionTestSlot(context, discriminant); !hoisted {
				// a discriminant whose evaluation moves state and that the hoist
				// could not take — no compiled callee, no statement stream, an
				// unsafe reordering — has no statement position here
				NoteSwitchRefusal(statement, "switch on an untracked discriminant")
				return nil, false
			}
			if hoistedSlot < len(context.Sorts) {
				sort := context.Sorts[hoistedSlot]
				hoistedTestable = sort == BindingKindNumber || sort == BindingKindString
			}
		} else {
			// AN OPAQUE-TESTABLE HEAD THAT NAMES NO SLOT — a bare identifier,
			// a literal, an ordinary expression that writes nothing and hides
			// no call — has no hoist to attempt because there is nothing to
			// hoist: OpaqueTestableCondition means evaluating it is safe to
			// skip entirely, not that its value is safe to CLAIM. The untested
			// branchBoth below is only sound for a call that genuinely RAN
			// (ConditionTestSlot's hoist, captured as a statement, whose
			// effects the join then carries); a discriminant that never ran
			// anything has no run to join over, so this switch declines
			// outright and the caller's havoc floor stands in — unknown
			// assigns to the slots the arms mention, claiming which arm ran.
			NoteSwitchRefusal(statement, "switch on an untracked discriminant")
			return nil, false
		}
	}
	clauses := switchStmt.CaseBlock.AsCaseBlock().Clauses.Nodes
	if len(clauses) == 0 {
		NoteSwitchRefusal(statement, "switch with no clauses")
		return nil, false
	}
	arms, armsOk := switchArmsOf(statement, clauses)
	if !armsOk {
		return nil, false
	}
	// the chain is built from the DEFAULT outwards: the default arm (or
	// the empty else where there is none — the run that matched no label
	// and fell past the switch) is the innermost else, and each case's
	// test wraps it
	var chain []kernelbridge.IrStatement
	if arms.HasDefault {
		lowered, ok := LowerStatements(context, stripTrailingBreak(arms.Default))
		if !ok {
			// the arm's own walk already named what it refused ON, and that
			// name is the one worth keeping — it points inside the clause
			NoteSwitchRefusal(statement, "switch whose default arm did not lower")
			return nil, false
		}
		chain = lowered
	}
	for index := len(arms.Cases) - 1; index >= 0; index-- {
		arm := arms.Cases[index]
		body, ok := LowerStatements(context, stripTrailingBreak(arm.Statements))
		if !ok {
			NoteSwitchRefusal(statement, "switch whose case arm did not lower")
			return nil, false
		}
		if !discriminantTracked && !hoistedTestable {
			// no slot to test: the arm and the rest of the chain are
			// SIBLINGS, both possible, and their exits join. A concrete run
			// takes exactly one of them and the join admits it either way —
			// the arm's own effects ride, and no claim is made about which
			// label matched.
			chain = []kernelbridge.IrStatement{{
				Kind: kernelbridge.IrStatementBranchBoth,
				Then: body,
				Else: chain,
			}}
			continue
		}
		// several labels reaching one arm are an `||` of equalities: each
		// label's test takes the same then-arm, and its else is the next
		// label's test — the nesting LowerGuard already builds for `a || b`
		guarded := chain
		for labelIndex := len(arm.Labels) - 1; labelIndex >= 0; labelIndex-- {
			test, testOk := switchLabelGuard(context, discriminant, hoistedSlot, hoistedTestable, arm.Labels[labelIndex], body, guarded)
			if !testOk {
				NoteSwitchRefusal(statement, "switch on a case label that is not a literal")
				return nil, false
			}
			guarded = test
		}
		chain = guarded
	}
	return chain, true
}

// switchCaseArm is one case arm: the labels that reach it and the
// statements a run reaching it executes — its clause's own statements
// followed by every clause it falls through into.
type switchCaseArm struct {
	Labels     []*ast.Node
	Statements []*ast.Node
}

// switchArms is the whole switch read as arms: the case arms in clause
// order, and the default's statements where a default clause exists.
type switchArms struct {
	Cases      []switchCaseArm
	Default    []*ast.Node
	HasDefault bool
}

// switchArmsOf turns the clause list into the arms the chain tests.
//
// The two readings the clause list needs:
//
//   - each clause's RUN is the concatenation of its own statements and
//     those of every clause after it, up to and including the first that
//     ends the run (break or return). An empty clause therefore shares
//     the following clause's run with no statements of its own, which is
//     the grouped-label form, and a non-empty falling-through clause
//     shares it with its own statements in front.
//   - the DEFAULT clause is not part of any case's run order. It is the
//     arm reached when no label matched, so it is collected on its own
//     and its own run is read the same way, from its own clause forward.
//
// A run that reaches the end of the clause list ENDS THERE: it has
// nothing left to fall into, so it leaves the switch, which is the same
// continuation a bare `break` gives one clause earlier (runFrom's own
// comment holds the argument). An empty trailing clause is the same
// thing with no statements — a no-op arm.
//
// The one refusal left is a second default clause, and it names
// ill-formed input rather than a construct: the grammar admits at most
// one DefaultClause, so it cannot fire on a program that compiles.
func switchArmsOf(statement *ast.Node, clauses []*ast.Node) (switchArms, bool) {
	// the run of clause `start`: its statements and the following
	// clauses' until one ends the run, or until the clause list runs out.
	runFrom := func(start int) []*ast.Node {
		var run []*ast.Node
		for index := start; index < len(clauses); index++ {
			body := clauses[index].AsCaseOrDefaultClause().Statements.Nodes
			run = append(run, body...)
			if armEndsItsRun(body) {
				return run
			}
		}
		// THE END OF THE CLAUSE LIST IS AN ENDING. A run that falls past
		// the last clause has nothing left to fall into, so it leaves the
		// switch — which is exactly what a bare `break` does, one clause
		// earlier. The two are the same continuation, and the chain
		// already spells it: stripTrailingBreak DROPS a trailing bare
		// break precisely because the arm's statements end where the arm
		// ends and control resumes after the switch, with no exit raise.
		// An end-of-list run is that same arm with the break never
		// written, so it lowers to the same statements and takes the same
		// continuation. `switch (x) { case 1: n = 1; case 2: n = 2; }`
		// gives case 1 the run `n = 1; n = 2;` and case 2 the run
		// `n = 2;`, both ending by leaving the switch.
		//
		// The concatenation is what makes this exact rather than
		// approximate: the run collected here is every statement a
		// matching run actually executes, in order, and there is no
		// statement after it inside the switch to account for.
		return run
	}
	out := switchArms{}
	var pendingLabels []*ast.Node
	for index, clause := range clauses {
		if ast.IsDefaultClause(clause) {
			if out.HasDefault {
				// ILL-FORMED INPUT, not a construct to go and read. CaseBlock's
				// two productions (ECMA-262, sec-switch-statement) are
				// `{ CaseClauses? }` and `{ CaseClauses? DefaultClause
				// CaseClauses? }` — the DefaultClause appears once and does not
				// repeat, so a second `default:` is a Syntax Error and tsc
				// reports it. This guard therefore cannot fire on a program
				// that compiles; it stands so a malformed tree reaching the
				// lowering answers nothing rather than building a chain with
				// two innermost elses.
				NoteSwitchRefusal(statement, "switch whose clause list is ill-formed: two default clauses, which the grammar does not admit")
				return switchArms{}, false
			}
			run := runFrom(index)
			// labels grouped ahead of the default reach the default's own
			// run, which is already the chain's innermost else — every one
			// of them lands there by matching no OTHER label, so the arm
			// needs no test of its own and the labels are dropped
			pendingLabels = nil
			out.Default = run
			out.HasDefault = true
			continue
		}
		label := clause.AsCaseOrDefaultClause().Expression
		body := clause.AsCaseOrDefaultClause().Statements.Nodes
		if len(body) == 0 {
			// an empty clause runs nothing of its own: its label joins the
			// next clause's run, which the following clause will collect
			pendingLabels = append(pendingLabels, label)
			continue
		}
		run := runFrom(index)
		labels := append(append([]*ast.Node{}, pendingLabels...), label)
		pendingLabels = nil
		out.Cases = append(out.Cases, switchCaseArm{Labels: labels, Statements: run})
	}
	// labels left over after the last clause: their run is what runFrom
	// answers from the FIRST of them, which is empty (every clause after
	// it was empty too, or they would have been collected above)
	if len(pendingLabels) > 0 {
		out.Cases = append(out.Cases, switchCaseArm{Labels: pendingLabels})
	}
	return out, true
}

// armEndsItsRun is whether a case clause's statements END the switch —
// a trailing `break` or `return`. Anything else falls through into the
// next clause, whose statements the arm then also runs.
func armEndsItsRun(statements []*ast.Node) bool {
	if len(statements) == 0 {
		return false
	}
	last := statements[len(statements)-1]
	if ast.IsBreakStatement(last) {
		// a LABELLED break leaves some outer statement, not this switch
		return last.AsBreakStatement().Label == nil
	}
	if ast.IsReturnStatement(last) {
		return true
	}
	// a trailing block ends the run where its own last statement does
	if ast.IsBlock(last) {
		return armEndsItsRun(last.AsBlock().Statements.Nodes)
	}
	return false
}

// stripTrailingBreak drops the `break` that ended a case clause: the
// chain's arm already ends where the arm ends, so the break has nothing
// left to leave. A trailing `return` STAYS — it writes the result slot
// and raises the done flag, which the arm must carry.
func stripTrailingBreak(statements []*ast.Node) []*ast.Node {
	if len(statements) == 0 {
		return statements
	}
	last := statements[len(statements)-1]
	if ast.IsBreakStatement(last) && last.AsBreakStatement().Label == nil {
		return statements[:len(statements)-1]
	}
	if ast.IsBlock(last) {
		inner := stripTrailingBreak(last.AsBlock().Statements.Nodes)
		out := append([]*ast.Node{}, statements[:len(statements)-1]...)
		return append(out, inner...)
	}
	return statements
}
