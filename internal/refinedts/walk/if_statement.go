// from control_flow/if_statement.ts
//
// Walk an if statement: evaluate the guard, report a provably false
// test, assumeCondition on both arms, then exact-join what survives.
// Dead or exiting arms contribute nothing.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// PerformsTest is performsTest in the TS source: whether a condition
// PERFORMS a test — applies a call, a comparison, or a NEGATION to its
// values — as opposed to a bare read of a constant flag or a literal,
// the spelling of deliberate dead code (`if (DEBUG)`), which stays
// exempt the way tsc exempts `if (false)` bodies from its
// unreachable-code error.
//
// A NEGATION is a test whatever it negates. `!` applies ToBoolean and
// inverts it — an operation on the value, not a bare read of it — so
// `if (!b)` on a b the walk proved true is a dead branch the author
// needs to hear about, exactly as `if (b === false)` would be. The
// earlier reading recursed into the operand, so a negated bare
// identifier answered false and the dead-guard report was suppressed on
// the one spelling this row is about. The exemption the doc above
// describes is for a BARE read (`if (DEBUG)`); `!DEBUG` was never that
// spelling.
func PerformsTest(e *ast.Node) bool {
	bare := e
	if ast.IsParenthesizedExpression(e) {
		bare = e.AsParenthesizedExpression().Expression
	}
	if bare != e {
		return PerformsTest(bare)
	}
	if ast.IsCallExpression(bare) {
		return true
	}
	if ast.IsBinaryExpression(bare) {
		op := bare.AsBinaryExpression().OperatorToken.Kind
		return op == ast.KindEqualsEqualsEqualsToken ||
			op == ast.KindExclamationEqualsEqualsToken ||
			op == ast.KindEqualsEqualsToken ||
			op == ast.KindExclamationEqualsToken ||
			op == ast.KindLessThanToken ||
			op == ast.KindGreaterThanToken ||
			op == ast.KindLessThanEqualsToken ||
			op == ast.KindGreaterThanEqualsToken ||
			op == ast.KindInstanceOfKeyword ||
			op == ast.KindInKeyword
	}
	if ast.IsPrefixUnaryExpression(bare) {
		unary := bare.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			return true
		}
	}
	return false
}

const deadGuardText = "this condition is provably false on every run — the test can " +
	"never pass, so the branch under it never runs"

// AnalyzeIfStatement is analyzeIfStatement in the TS source.
//
// CROSS-DIRECTORY: assumeCondition is this directory's own
// assume_condition.ts, not yet ported (a large file, ported
// separately in this same wave). checkAssignability is
// assignability/check_assignability.ts — a concurrent agent's
// FlowContext-reading file joining this package per PORT.md.
func AnalyzeIfStatement(
	ctx *FlowContext,
	env Env,
	statement *ast.Node,
	result *annotations.DeclaredRefinement,
) bool {
	ifStmt := statement.AsIfStatement()
	// the condition EVALUATES — an inlined call answers its boolean, a
	// known value its ToBoolean — and a computed verdict kills the
	// refuted branch outright; everything else the condition proves is
	// the assume operator's (assume_condition.ts), the same routine
	// every branching site consumes
	conditionKnown := evaluateExpression(ctx, env, ifStmt.Expression)
	computedValue, computedKnown := abstractdomain.TruthinessDecided(conditionKnown)
	// a PROVABLY FALSE test speaks: the walk already kills the branch
	// below, and a determination the walk acts on is a determination
	// the code's author needs to hear (the vacuous-guard bug class —
	// `isFunction(key)` on a for-in string key). Not under an assumed
	// correlation gate, where a condition folds false by assumption in
	// one of two walked passes rather than on every run.
	// Nor on an ABSENCE test the tested name's own declaration demands:
	// `value === undefined` on a name the host types `V | undefined`
	// (Map.get's signature) is the only spelling that compiles, and the
	// walk folding it false rests on knowledge past the declaration —
	// the same reason CallSiteSeeded stays quiet just above.
	if computedKnown && !computedValue && PerformsTest(ifStmt.Expression) &&
		len(ctx.GateAssumptions) == 0 && !ctx.CallSiteSeeded &&
		!AbsenceGuardTheDeclarationDemands(ctx, env, ifStmt.Expression) {
		ctx.Report(assignability.At(ifStmt.Expression, 7001, deadGuardText))
	}
	elseScope := ifStmt.ElseStatement
	if elseScope == nil {
		elseScope = statement.Parent
	}
	if elseScope == nil {
		elseScope = statement
	}
	assumed := assumeCondition(ctx, env, ifStmt.Expression, AssumeConditionScope{
		WhenTrueScope:  ifStmt.ThenStatement,
		WhenFalseScope: elseScope,
		At:             statement,
	}, computedValue, computedKnown)
	whenTrue, whenFalse := assumed.WhenTrue, assumed.WhenFalse
	thenExits := whenTrue.Dead || AnalyzeStatement(whenTrue.Ctx, whenTrue.Env, ifStmt.ThenStatement, result)
	elseExits := whenFalse.Dead
	if !elseExits && ifStmt.ElseStatement != nil {
		elseExits = AnalyzeStatement(whenFalse.Ctx, whenFalse.Env, ifStmt.ElseStatement, result)
	}
	if whenTrue.Dead && !whenFalse.Dead {
		ReplaceEnv(env, whenFalse.Env)
		if ifStmt.ElseStatement == nil {
			return false
		}
		return elseExits
	}
	if whenFalse.Dead && !whenTrue.Dead {
		if thenExits {
			return true
		}
		ReplaceEnv(env, whenTrue.Env)
		return false
	}
	if thenExits && elseExits {
		return true
	}
	if thenExits {
		ReplaceEnv(env, whenFalse.Env)
		// the continuation runs with the condition REFUTED —
		// `if (i >= s.length) return` is the defensive spelling of
		// `if (i < s.length) { … }`, and both leave the same row
		continuationScope := statement.Parent
		if continuationScope == nil {
			continuationScope = statement
		}
		assumed.RefuteIntoContinuation(statement, continuationScope, env)
	} else if elseExits {
		ReplaceEnv(env, whenTrue.Env)
		// the continuation runs with the condition HELD — the then
		// branch's own rows carry forward
		if len(assumed.HeldConstraints) > 0 {
			dataflowfacts.NoteExitConstraints(statement, assumed.HeldConstraints)
		}
	} else {
		ReplaceEnv(env, JoinEnvs(whenTrue.Env, whenFalse.Env))
	}
	return false
}
