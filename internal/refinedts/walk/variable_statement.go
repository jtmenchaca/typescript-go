// from control_flow/variable_statement.ts
//
// Walk a variable statement: annotation-typed invariants, ambient
// declare, alias linking (this, projection, returned-parameter
// aliases). Destructuring patterns go through destructure_binding.
//
// CROSS-DIRECTORY: ContractOf, ProjectionSources are
// interprocedural/evaluate_call.ts's exported functions — not yet
// ported (interprocedural is a concurrent wave-2 agent's package,
// joining this same walk package per PORT.md).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// AnalyzeVariableStatement is analyzeVariableStatement in the TS
// source.
func AnalyzeVariableStatement(ctx *FlowContext, env Env, statement *ast.Node) bool {
	varStmt := statement.AsVariableStatement()
	for _, declaration := range varStmt.DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		if BindDestructuringDeclaration(ctx, env, declaration) {
			continue
		}
		decl := declaration.AsVariableDeclaration()
		if !ast.IsIdentifier(decl.Name()) {
			continue
		}
		// an annotation-typed binding states its invariant
		statedRefinement := false
		if decl.Type != nil {
			stated := annotations.AnnotationOfType(ctx.P, decl.Type, ctx.Registry, ctx.Objects)
			if stated.Unsupported != "" {
				ctx.Report(assignability.At(decl.Type, 7004, stated.Unsupported))
			} else if stated.Stated != nil {
				ctx.Declared[decl.Name().Text()] = stated.Stated
				statedRefinement = true
			}
		}
		ambient := false
		if modifiers := statement.Modifiers(); modifiers != nil {
			for _, m := range modifiers.Nodes {
				if m.Kind == ast.KindDeclareKeyword {
					ambient = true
					break
				}
			}
		}
		var held abstractdomain.AbstractValue
		if decl.Initializer != nil {
			held = evaluateExpression(ctx, env, decl.Initializer)
		} else if ambient {
			// ambient `declare const x: T` states a value that exists
			// elsewhere, so it stays unknown
			held = silence.Residue()
		} else {
			// `let x;` holds the absent value until a later write
			held = abstractdomain.Undef
		}
		value := silence.SeededBinding(ctx.P.Checker, held, decl.Name())
		if decl.Initializer == nil {
			// no WRITE to checkAssignability — for an ambient declare, the alert
			// belongs where the value is read
			env.Set(decl.Name().Text(), value)
		} else {
			WriteBinding(ctx, env, decl.Name().Text(), value, decl.Initializer, "an initialized value")
			// a PLAIN spelled annotation (no refinement read) still
			// states its absence exclusion
			if decl.Type != nil && !statedRefinement {
				AlertPlainAbsence(ctx, value, decl.Type, decl.Initializer, "an initialized value")
			}
		}
		// binding a reference to another name: the two now share it —
		// directly (an assertion wrapper changes no reference), or
		// embedded inside a literal
		direct := peeledReference(decl.Initializer)
		if direct != nil && ast.IsIdentifier(direct) && dataflowfacts.ReferenceTyped(ctx.P.Checker, direct) {
			ctx.Aliases.Link(decl.Name().Text(), direct.Text())
		}
		// `const self = this` shares the instance: linked, so a forget
		// through either name clears both
		if direct != nil && direct.Kind == ast.KindThisKeyword {
			if _, hasThis := env.Get("this"); hasThis {
				ctx.Aliases.Link(decl.Name().Text(), "this")
			}
		}
		if decl.Initializer != nil {
			LinkEmbedded(ctx, decl.Name().Text(), decl.Initializer)
		}
		// reading a reference OUT of a tracked holder — or selecting
		// one of several — makes the binding share it: linked, so a
		// write through the child reaches the holder(s)
		if decl.Initializer != nil && dataflowfacts.ReferenceTyped(ctx.P.Checker, decl.Name()) {
			for _, holder := range ProjectionSources(decl.Initializer, nil) {
				if _, ok := env.Get(holder); ok {
					ctx.Aliases.Link(decl.Name().Text(), holder)
				}
			}
		}
		// a call that RETURNS one of its parameters hands back an
		// ALIAS: the binding links to the identifier argument at that
		// position
		if direct != nil && ast.IsCallExpression(direct) && dataflowfacts.ReferenceTyped(ctx.P.Checker, decl.Name()) {
			linkReturnedParameterAlias(ctx, env, decl.Name().Text(), direct)
		}
	}
	return false
}

// peeledReference looks through the wrappers that change no reference
// — parentheses, an `as` cast, a non-null assertion — to the
// expression whose runtime value is the very same value. `x`, `(x)`,
// `x as T` and `x!` all hand back the one object, so each of them
// names the same reference for aliasing.
func peeledReference(e *ast.Node) *ast.Node {
	for e != nil {
		switch {
		case ast.IsParenthesizedExpression(e):
			e = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			e = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			e = e.AsNonNullExpression().Expression
		default:
			return e
		}
	}
	return e
}

// linkReturnedParameterAlias is the TS source's local function: a
// call that returns one of its parameters is an alias — the binding
// links to the identifier argument at that position.
func linkReturnedParameterAlias(ctx *FlowContext, env Env, bindingName string, direct *ast.Node) {
	call := direct.AsCallExpression()
	callee := ContractOf(ctx, call.Expression)
	if callee == nil || callee.Declaration == nil || callee.Declaration.Body() == nil {
		return
	}
	body := callee.Declaration.Body()
	returned := map[string]struct{}{}
	if ast.IsBlock(body) {
		var scanReturns func(node *ast.Node)
		scanReturns = func(node *ast.Node) {
			if ast.IsReturnStatement(node) {
				rs := node.AsReturnStatement()
				if rs.Expression != nil && ast.IsIdentifier(rs.Expression) {
					returned[rs.Expression.Text()] = struct{}{}
				}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				scanReturns(child)
				return false
			})
		}
		scanReturns(body)
	} else if bodyValue := peeledReference(body); bodyValue != nil && ast.IsIdentifier(bodyValue) {
		// an expression body returns its value directly, through the
		// wrappers that change no reference — `(x) => (x)` and
		// `(x) => x as T` hand back the parameter exactly as `(x) => x`
		// does
		returned[bodyValue.Text()] = struct{}{}
	}
	var callArguments []*ast.Node
	if call.Arguments != nil {
		callArguments = call.Arguments.Nodes
	}
	for i, parameter := range callee.Declaration.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) {
			continue
		}
		if _, ok := returned[pd.Name().Text()]; !ok {
			continue
		}
		if i >= len(callArguments) {
			continue
		}
		argument := peeledReference(callArguments[i])
		if argument != nil && ast.IsIdentifier(argument) {
			if _, ok := env.Get(argument.Text()); ok {
				ctx.Aliases.Link(bindingName, argument.Text())
			}
		}
	}
}
