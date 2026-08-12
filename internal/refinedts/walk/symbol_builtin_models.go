// from evaluation/symbol_builtin_models.ts
//
// Symbol / Symbol.for: an opaque primitive. Symbol.for's registry
// hands the SAME symbol back per key (sec-symbol.for), so the key
// is identity the walk can carry; a fresh Symbol() has identity it
// cannot.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// readSymbolBuiltin is readSymbolBuiltin in the TS source: Symbol.for(key)
// and Symbol([description]) when they resolve to the default library.
// Nil when the call is not a Symbol construction.
func readSymbolBuiltin(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if ast.IsPropertyAccessExpression(call.Expression) {
		pa := call.Expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Symbol" &&
			pa.Name().Text() == "for" && resolvesToDefaultLib(ctx, pa.Expression) &&
			argCount == 1 && ast.IsStringLiteral(call.Arguments.Nodes[0]) {
			text := call.Arguments.Nodes[0].Text()
			out := abstractdomain.AbstractValue{
				Kind: abstractdomain.KindSymbol, SymbolKey: text, HasSymbolKey: true,
				Description: text, HasDescription: true,
			}
			return &out
		}
	}
	if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Symbol" &&
		resolvesToDefaultLib(ctx, call.Expression) && argCount <= 1 {
		if argCount == 0 {
			out := abstractdomain.AbstractValue{Kind: abstractdomain.KindSymbol}
			return &out
		}
		a := call.Arguments.Nodes[0]
		if ast.IsStringLiteral(a) {
			out := abstractdomain.AbstractValue{Kind: abstractdomain.KindSymbol, Description: a.Text(), HasDescription: true}
			return &out
		}
	}
	return nil
}
