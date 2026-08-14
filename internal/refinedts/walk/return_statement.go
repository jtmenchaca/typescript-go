// from control_flow/return_statement.ts
//
// Walk a return: evaluate the value, sink it, and check the stated
// result — including a silent ternary whose arms sink separately.
//

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// enclosingFunctionOf is enclosingFunctionOf in the TS source: the
// function whose body holds this return, for its spelled return type
// — the plain-absence claim reads off it.
func enclosingFunctionOf(s *ast.Node) *ast.Node {
	node := s.Parent
	for node != nil {
		if ast.IsFunctionLike(node) {
			return node
		}
		node = node.Parent
	}
	return nil
}

// AnalyzeReturnStatement is analyzeReturnStatement in the TS source.
func AnalyzeReturnStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	rs := statement.AsReturnStatement()
	// inside a SILENT inline recovery, a returned ternary sinks its
	// arms separately: the join happens at the summary, where a
	// recursive pass-through arm (`c ? base : f(x - 1)`) survives
	// as a whole entry for the induction instead of laundering
	// through the join. A STATED result type takes the same split —
	// each arm is what that path returns, so each checks against the
	// stated type on its own — and then also checks the whole ternary,
	// which is what the plain path below does.
	if rs.Expression != nil && ast.IsConditionalExpression(rs.Expression) && ctx.ReturnSink != nil {
		ternary := rs.Expression.AsConditionalExpression()
		evaluateExpression(ctx, env, ternary.Condition)
		// both arms sink whole (the summary joins them); each runs
		// under everything its side of the condition proves
		assumed := assumeCondition(ctx, env, ternary.Condition, AssumeConditionScope{
			WhenTrueScope:  ternary.WhenTrue,
			WhenFalseScope: ternary.WhenFalse,
			At:             rs.Expression,
		}, false, false)
		whenTrue := evaluateExpression(assumed.WhenTrue.Ctx, assumed.WhenTrue.Env, ternary.WhenTrue)
		whenFalse := evaluateExpression(assumed.WhenFalse.Ctx, assumed.WhenFalse.Env, ternary.WhenFalse)
		*ctx.ReturnSink = append(*ctx.ReturnSink, whenTrue)
		*ctx.ReturnSink = append(*ctx.ReturnSink, whenFalse)
		if result != nil {
			// the check the stated type is owed: each arm against it at
			// its own site, so the report names the arm that breaks it
			CheckAssignability(ctx, whenTrue, *result, ternary.WhenTrue, "a returned value", nil)
			CheckAssignability(ctx, whenFalse, *result, ternary.WhenFalse, "a returned value", nil)
			// a parameter-dependent bound on the result is per-run — the
			// sibling's state at THIS return is the evidence
			CheckDependentReturn(ctx, env, rs.Expression, abstractdomain.JoinKnown(whenTrue, whenFalse), *result)
		}
		return true
	}
	known := silence.Residue()
	if rs.Expression != nil {
		known = evaluateExpression(ctx, env, rs.Expression)
	}
	if ctx.ReturnSink != nil {
		*ctx.ReturnSink = append(*ctx.ReturnSink, known)
	}
	if result != nil && rs.Expression != nil {
		CheckAssignability(ctx, known, *result, rs.Expression, "a returned value", nil)
		// a parameter-dependent bound on the result is per-run — the
		// sibling's state at THIS return is the evidence
		CheckDependentReturn(ctx, env, rs.Expression, known, *result)
	} else if rs.Expression != nil {
		// a PLAIN spelled return type still states its absence
		// exclusion — a value the walk proved may be undefined breaks
		// that claim even where no refinement is stated
		fn := enclosingFunctionOf(statement)
		if fn != nil {
			var returnType *ast.Node
			if data := fn.FunctionLikeData(); data != nil {
				returnType = data.Type
			}
			if returnType != nil {
				AlertPlainAbsence(ctx, known, returnType, rs.Expression, "a returned value")
			}
		}
	}
	return true
}
