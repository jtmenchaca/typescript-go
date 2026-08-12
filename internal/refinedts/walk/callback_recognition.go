// from interprocedural/callback_recognition.ts
//
// Resolving an expression to a Callback whose body is in reach —
// through an import alias, so an imported name works the same as a
// local one. Resolution (with the const and reassignment gates) is
// pinnedFunctionOf's, shared with the predicate and tree channels.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// FunctionInReach is the function a NAME resolves to when its body
// is in reach.
func FunctionInReach(ctx *FlowContext, expression *ast.Node) Callback {
	if !ast.IsIdentifier(expression) {
		return nil
	}
	return narrowing.PinnedFunctionOf(ctx.P.Checker, expression)
}
