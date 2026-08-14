// from control_flow/try_statement.ts
//
// Walk try/catch/finally: snapshots of the try, catch havoc of
// try-text writes, catch binding via seededBinding, then finally join.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// raisesNothing answers whether running this subtree can raise — the
// conservative half of the question, so only shapes the walk can read
// end to end answer true. A call, a `new`, an await, a yield, a
// tagged template, a `throw`, and a `delete` all run code this test
// does not see. So does every property and element access: the
// receiver may be absent, and a getter behind a plain read runs a
// body — the same standing gap writeAndCallFree's comment names.
// What remains are identifiers, literals, and the operators over
// them, plus the assignments whose two sides are themselves readable.
func raisesNothing(node *ast.Node) bool {
	if node == nil {
		return true
	}
	switch node.Kind {
	case ast.KindCallExpression, ast.KindNewExpression, ast.KindAwaitExpression,
		ast.KindYieldExpression, ast.KindTaggedTemplateExpression, ast.KindDeleteExpression,
		ast.KindThrowStatement, ast.KindPropertyAccessExpression, ast.KindElementAccessExpression,
		ast.KindSpreadElement, ast.KindSpreadAssignment:
		return false
	}
	// a nested function is not RUN here, but the statements this walks
	// are its own body's, which this reading does not cover
	if ast.IsFunctionLike(node) {
		return false
	}
	// an assignment to anything but a plain name writes through a
	// receiver, which is the access case above
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment &&
			!ast.IsIdentifier(bin.Left) {
			return false
		}
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !raisesNothing(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// statementsFromFirstThrowing drops the leading run of statements that
// provably raise nothing and returns the rest — the statements at or
// after the first one an exception can come out of. A catch entry
// forgets what those write; the dropped run's writes had already
// completed when any exception was raised.
func statementsFromFirstThrowing(statements []*ast.Node) []*ast.Node {
	for index, s := range statements {
		if !raisesNothing(s) {
			return statements[index:]
		}
	}
	return nil
}

// AnalyzeTryStatement is analyzeTryStatement in the TS source.
func AnalyzeTryStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	tryStmt := statement.AsTryStatement()
	// the try walks statement by statement, snapshotting: an
	// exception observed by catch carries one of these states —
	// callee-mediated effects land whole at their call statement
	// (exceptional exits already joined into the post-call state by
	// the inline), while a name the try TEXT itself writes can be
	// caught mid-sequence, so it is forgotten at catch entry
	tryEnv := env.Clone()
	snapshots := []Env{tryEnv.Clone()}
	tryExits := false
	for _, s := range tryStmt.TryBlock.AsBlock().Statements.Nodes {
		tryExits = AnalyzeStatement(ctx, tryEnv, s, result)
		snapshots = append(snapshots, tryEnv.Clone())
		if tryExits {
			break
		}
	}
	var catchEnv Env
	catchExits := false
	if tryStmt.CatchClause != nil {
		catchEnv = env.Clone()
		catchClause := tryStmt.CatchClause.AsCatchClause()
		// only the try TEXT's own writes havoc here — callee-mediated
		// effects land whole at their statement, and the snapshots
		// (whose per-name join covers partial application) carry them.
		// The havoc starts at the first statement that could throw: a
		// leading run of statements that move state but raise nothing
		// completes before any exception can be observed, so catch sees
		// their writes intact.
		written := map[string]struct{}{}
		for _, s := range statementsFromFirstThrowing(tryStmt.TryBlock.AsBlock().Statements.Nodes) {
			AssignedNamesDirect(s, written)
		}
		for name := range written {
			if _, ok := catchEnv.Get(name); ok {
				HavocEnv(ctx.Aliases, catchEnv, name)
			}
		}
		catchEnv.Range(func(name string, held abstractdomain.AbstractValue) bool {
			if held.Kind == abstractdomain.KindUnknown {
				return true
			}
			joined := held
			for _, snapshot := range snapshots {
				joined = abstractdomain.JoinKnown(joined, envOrResidue(snapshot, name))
			}
			catchEnv.Set(name, joined)
			return true
		})
		binding := catchClause.VariableDeclaration
		if binding != nil {
			bindingName := binding.AsVariableDeclaration().Name()
			if ast.IsIdentifier(bindingName) {
				catchEnv.Set(bindingName.Text(), silence.SeededBinding(ctx.P.Checker, silence.Residue(), bindingName))
			}
		}
		catchExits = AnalyzeStatements(ctx, catchEnv, catchClause.Block.AsBlock().Statements.Nodes, result)
	}
	// the continuing state: the try's normal completion joined with
	// the catch's — either path may be the one that ran
	switch {
	case catchEnv == nil:
		ReplaceEnv(env, tryEnv)
	case catchExits:
		ReplaceEnv(env, tryEnv)
	case tryExits:
		ReplaceEnv(env, catchEnv)
	default:
		ReplaceEnv(env, JoinEnvs(tryEnv, catchEnv))
	}
	if tryStmt.FinallyBlock != nil {
		AnalyzeStatements(ctx, env, tryStmt.FinallyBlock.AsBlock().Statements.Nodes, result)
	}
	return tryExits && (tryStmt.CatchClause == nil || catchExits)
}
