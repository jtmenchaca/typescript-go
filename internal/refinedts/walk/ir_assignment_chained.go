// split from ir_assignment.go — chained assignments

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// ChainedAssignmentsOf lowers `x1 = x2 = e` — an assignment whose RIGHT
// SIDE is itself a plain assignment, however many links deep.
//
// JavaScript evaluates the chain right to left and every link takes the
// same value: `x2 = e` writes e into x2 and EVALUATES to what it wrote,
// which is then what `x1 =` writes. So the chain lowers as one
// assignment per link, innermost first, each outer link reading the slot
// the link inside it just wrote:
//
//	x1 = x2 = data.coordinate  ⇒  x2 := data.coordinate ; x1 := var x2
//
// Reading the INNER SLOT rather than re-reading the right side is what
// keeps the two links tied: the kernel then knows x1 and x2 hold the
// same value, which is the whole content of the chain. (Re-lowering the
// expression twice would give two independent readings of one
// evaluation, and for a right side with any width the two links would
// drift apart.)
//
// Only a plain `=` at every link is a chain. A compound (`x1 = x2 += e`)
// evaluates its target first and belongs to the compound rule, which
// reads one target; this route leaves those alone.
//
// The innermost right side is whatever RhsEffect spells for the
// innermost target's sort, so a chain ending in a string, an absent, or
// a call the hoist route reads all lower exactly as the same assignment
// written on its own would.
//
// Declines where any link's target has no slot or the innermost right
// side does not lower — the statement then falls to the routes below it,
// and ultimately the floor, exactly as the whole chain did before this
// route existed.
func ChainedAssignmentsOf(context *LoweringContext, e *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	// walk OUT to IN collecting each link's target, stopping at the first
	// right side that is not itself a plain assignment
	var targets []int
	current := e
	for {
		bin := current.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindEqualsToken {
			return nil, false
		}
		target, ok := IndexOf(context, bin.Left)
		if !ok {
			return nil, false
		}
		targets = append(targets, target)
		right := Unwrapped(bin.Right)
		if !ast.IsBinaryExpression(right) ||
			right.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsToken {
			// the innermost right side: the value the whole chain writes
			if len(targets) < 2 {
				// a single `x = e` is not a chain — the ordinary rule owns it
				return nil, false
			}
			innermost := targets[len(targets)-1]
			effect, effectOk := RhsEffect(context, context.Sorts[innermost], bin.Right)
			if !effectOk {
				return nil, false
			}
			// innermost first, then each outer link copying the slot inside it
			out := make([]AssignmentTarget, 0, len(targets))
			out = append(out, AssignmentTarget{Target: innermost, Effect: effect})
			for index := len(targets) - 2; index >= 0; index-- {
				out = append(out, AssignmentTarget{
					Target: targets[index],
					Effect: kernelbridge.LoopEffect{
						Kind:  kernelbridge.LoopEffectVar,
						Index: targets[index+1],
					},
				})
			}
			return out, true
		}
		current = right
	}
}

// ChainedAssignmentStatementOf is the statement-position reading of a
// chained assignment: `x1 = x2 = e;` as its own expression statement.
func ChainedAssignmentStatementOf(context *LoweringContext, s *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(s) {
		return nil, false
	}
	return ChainedAssignmentsOf(context, Unwrapped(s.AsExpressionStatement().Expression))
}
