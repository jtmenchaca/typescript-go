// from evaluation/iteration_elements.ts
//
// What one element of an iterable is, where the iterable expression
// itself says so. Split from builtin_models.ts per the v2 tree.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// resolvesToDefaultLib mirrors service/program_resolution.ts's
// resolvesToDefaultLib once p.host is the checker itself (PORT.md):
// whether the node's symbol declares in the checker's default
// library.
func resolvesToDefaultLib(ctx *FlowContext, node *ast.Node) bool {
	return ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(node))
}

// IterationElementOf: what one element of a for-of is, where the
// ITERABLE expression says so even though the walk holds no
// sequence for it: a web collection's iterator (web.ts), or an
// Object.entries call — whose pair KEYS are Strings
// (sec-object.entries) whatever the argument. Nil where neither
// speaks; the loop's own reading stands.
func IterationElementOf(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue {
	web := WebIterationElement(ctx.P, iterable)
	if web != nil {
		return web
	}
	if ast.IsCallExpression(iterable) {
		call := iterable.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) &&
				access.Expression.Text() == "Object" &&
				access.Name().Text() == "entries" &&
				resolvesToDefaultLib(ctx, access.Expression) &&
				len(call.Arguments.Nodes) == 1 {
				// only an identifier argument is read here — anything
				// richer was already evaluated once by the loop's
				// iterable walk, and reading it again would replay its
				// effects
				argument := call.Arguments.Nodes[0]
				var held *abstractdomain.AbstractValue
				if ast.IsIdentifier(argument) {
					if h, ok := env[argument.Text()]; ok {
						held = &h
					}
				}
				value := silence.Residue()
				if held != nil && held.Kind == abstractdomain.KindUnknown && held.Opaque {
					value = abstractdomain.Opaque
				}
				out := abstractdomain.KnownList([]abstractdomain.AbstractValue{
					abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone),
					value,
				}, abstractdomain.TrustSpec)
				return &out
			}
		}
	}
	return nil
}
