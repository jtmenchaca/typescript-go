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
		// (whose per-name join covers partial application) carry them
		written := map[string]struct{}{}
		AssignedNamesDirect(tryStmt.TryBlock, written)
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
