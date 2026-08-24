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
	// the try body walks through listWalk directly — the SAME
	// edge-serving, per-statement path every other block gets (a plain
	// Block, a for body, and this same function's own catch/finally
	// blocks below all reach it through AnalyzeStatements) — rather than
	// a hand-rolled per-statement loop calling AnalyzeStatement in
	// isolation. A recognized cross-language call inside try{} now
	// reaches ForeignEdgeAt exactly as it does in those other shapes.
	//
	// listWalk directly, not AnalyzeStatements: AnalyzeStatements can
	// split the SAME list into two speculative passes when it tests one
	// immutable gate across two or more `if`s (the correlation pass),
	// walking each pass over its OWN throwaway env before joining only
	// at the end — there is no single per-statement state in that shape
	// for a snapshot to name. listWalk is the unconditional per-
	// statement walk underneath both that split and the plain path, so
	// calling it here keeps the snapshot semantics well-defined for
	// every try body, split-triggering or not, at the cost of not
	// running the correlation pass over a try body specifically (the
	// old hand loop never ran it either, so nothing this try walk did
	// before now regresses).
	//
	// The snapshot semantics the hand loop existed for ride along on
	// StatementObserver: listWalk calls it after every statement it
	// walks, in the same order the old loop appended, so the exception
	// join below still reads one state per try-body prefix.
	tryCtx := *ctx
	tryCtx.StatementObserver = func(stmtEnv Env, exits bool) {
		snapshots = append(snapshots, stmtEnv.Clone())
	}
	tryExits := listWalk(&tryCtx, tryEnv, tryStmt.TryBlock.AsBlock().Statements.Nodes, result)
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
		// entryValues holds each havocked name's value from BEFORE the
		// havoc below runs — the seed the join must start from. Reading
		// the seed off `held` AFTER havoc would start every havocked
		// name's join at Unknown, and JoinKnown treats Unknown as
		// absorbing (an unknown SIDE of an ordinary branch join must
		// stay unknown, since that branch's true value is unproven) —
		// so seeding from the post-havoc value makes the join a no-op
		// for exactly the names it exists to serve. The pre-havoc entry
		// value is the correct seed: an exception can fire before the
		// try body's first statement runs at all, so the entry value is
		// itself one of the states catch must join over.
		entryValues := map[string]abstractdomain.AbstractValue{}
		for name := range written {
			if v, ok := catchEnv.Get(name); ok {
				entryValues[name] = v
				HavocEnv(ctx.Aliases, catchEnv, name)
			}
		}
		// The havoc above marks which names need the join: a name it
		// forgot to Unknown is exactly a name this join must recover,
		// by joining its pre-havoc entry value with every try-prefix
		// snapshot. A name never in `written` was never havocked, so
		// `held` here already holds its correct value, unchanged by the
		// try body — joining it against snapshots that all repeat that
		// same value back is a no-op. Running the join unconditionally
		// over every held name therefore changes nothing for the
		// untouched names and recovers the join for the havocked ones.
		catchEnv.Range(func(name string, held abstractdomain.AbstractValue) bool {
			joined, wasHavocked := entryValues[name]
			if !wasHavocked {
				joined = held
			}
			for _, snapshot := range snapshots {
				joined = abstractdomain.JoinKnown(joined, envOrResidue(snapshot, name))
			}
			catchEnv.Set(name, joined)
			return true
		})
		binding := catchClause.VariableDeclaration
		if binding != nil {
			bindingName := binding.AsVariableDeclaration().Name()
			caught := silence.ResidueOf("a caught value can be anything the try block threw — its own type states no sort")
			if ast.IsIdentifier(bindingName) {
				catchEnv.Set(bindingName.Text(), silence.SeededBinding(ctx.P.Checker, caught, bindingName))
			} else if ast.IsBindingPattern(bindingName) {
				// `catch ({ message })` — a pattern binder over the SAME
				// caught value every plain `catch (error)` binds; each
				// leaf reads through the shared destructuring reader
				// (destructure_binding.go's destructureInto), the same
				// route a variable declaration's own pattern takes
				destructureInto(ctx, catchEnv, bindingName, caught)
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
