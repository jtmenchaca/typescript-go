// from control_flow/loop_statement.ts
//
// Walk a loop statement: a literally-false while runs zero times;
// a for-head let/const is loop-scoped and restores after solveLoop
// certifies the invariant.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// AnalyzeLoopStatement is analyzeLoopStatement in the TS source.
func AnalyzeLoopStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	// a while condition that is a FALSY literal runs zero iterations —
	// the body writes nothing. The condition follows its const-to-const
	// links first, so `const OFF = false; while (OFF)` gets the same
	// shortcut as `while (false)`. What the resolver reads is a literal
	// token: the two boolean keywords, a numeric literal, and a string
	// literal — 0, -0, and "" are falsy under ToBoolean, so they run
	// zero times too. A condition that is not a literal (a call, a
	// comparison, a let-bound name) declines and takes the full loop
	// machinery, even where the walk could decide it false.
	if ast.IsWhileStatement(statement) {
		if literal, ok := dataflowfacts.ConstChainLiteral(ctx.P.Checker, statement.AsWhileStatement().Expression); ok && dataflowfacts.FalsyLiteral(literal) {
			return false
		}
	}
	// a for-head let/const is LOOP-scoped: what it shadows comes
	// back after the loop (any later use of the head name is out of
	// scope — tsc's own error — except through an outer shadow,
	// which must read the OUTER binding again)
	loopScoped := map[string]abstractdomain.AbstractValue{}
	loopScopedHas := map[string]bool{}
	if ast.IsForStatement(statement) {
		forStmt := statement.AsForStatement()
		if forStmt.Initializer != nil && ast.IsVariableDeclarationList(forStmt.Initializer) &&
			(forStmt.Initializer.Flags&(ast.NodeFlagsLet|ast.NodeFlagsConst)) != 0 {
			for _, declaration := range forStmt.Initializer.AsVariableDeclarationList().Declarations.Nodes {
				decl := declaration.AsVariableDeclaration()
				if ast.IsIdentifier(decl.Name()) {
					name := decl.Name().Text()
					if held, ok := env.Get(name); ok {
						loopScoped[name] = held
						loopScopedHas[name] = true
					} else {
						loopScopedHas[name] = false
					}
				}
			}
		}
	}
	// iterate → widen → the kernel certifies the invariant; a binding
	// whose candidate fails certification goes unknown, honestly. A
	// break in the body belongs to this loop, so an enclosing
	// switch's sink is out of reach from here.
	loopCtx := *ctx
	loopCtx.BreakSink = nil
	loopCtx.ContinueSink = nil
	SolveLoop(&loopCtx, env, statement, result, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	}, nil)
	for name, held := range loopScoped {
		// only a SHADOWING head name restores — see the block rule
		if loopScopedHas[name] {
			env.Set(name, held)
		}
	}
	return false
}
