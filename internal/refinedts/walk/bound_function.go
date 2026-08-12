// from interprocedural/bound_function.ts
//
// A partial application made by `f.bind(thisArg, ...prebound)`. A
// bound function called with arguments calls its target with the
// prebound arguments PREPENDED — the target's argument list is the
// list-concatenation of [[BoundArguments]] and the call's own
// (tmp/ecma262/spec.html sec-function.prototype.bind,
// sec-bound-function-exotic-objects-call-thisargument-argumentslist).
// So a call's first argument binds the target's parameter at
// `offset`, not at 0.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// BoundFunction is a partial application made by `f.bind(thisArg,
// ...prebound)`.
type BoundFunction struct {
	Target Callback
	// Offset is how many leading parameters the prebound arguments
	// consume.
	Offset            int
	PreboundArguments []*ast.Node
}

// This file's TS twin defines its own local `mentionsThis` ("Does
// any node in the subtree formatAt `this`?"), functionally identical
// to evaluation/this_binding.ts's own `MentionsThis` — the Go port
// already has one copy of that walk in this_binding.go, so this file
// calls MentionsThis rather than duplicating it.

// BoundFunctionOf reads `f.bind(thisArg, ...prebound)` where `bind`
// is the default library's Function.prototype.bind (a user method
// sharing the name is not it) and `f`'s body is in reach. The
// thisArg is NOT modeled: an arrow target never uses it (the spec's
// own note on bind), and any other target whose body mentions `this`
// is refused — the walk claims nothing there.
func BoundFunctionOf(ctx *FlowContext, expression *ast.Node) *BoundFunction {
	e := expression
	for ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) {
		if ast.IsParenthesizedExpression(e) {
			e = e.AsParenthesizedExpression().Expression
		} else {
			e = e.AsAsExpression().Expression
		}
	}
	if !ast.IsCallExpression(e) {
		return nil
	}
	call := e.AsCallExpression()
	callee := call.Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return nil
	}
	pae := callee.AsPropertyAccessExpression()
	if pae.Name().Text() != "bind" || !resolvesToDefaultLib(ctx, pae.Name()) {
		return nil
	}
	// a spread among the bind's arguments breaks the prebound COUNT —
	// the offset would be a guess
	for _, argument := range call.Arguments.Nodes {
		if ast.IsSpreadElement(argument) {
			return nil
		}
	}
	target := FunctionInReach(ctx, pae.Expression)
	if target == nil {
		return nil
	}
	if !ast.IsArrowFunction(target) && MentionsThis(target) {
		return nil
	}
	offset := len(call.Arguments.Nodes) - 1
	if offset < 0 {
		offset = 0
	}
	// a prebound REST parameter swallows every later argument — the
	// positional offset past it means nothing
	for index, parameter := range target.Parameters() {
		if index < offset && parameter.AsParameterDeclaration().DotDotDotToken != nil {
			return nil
		}
	}
	prebound := call.Arguments.Nodes
	if len(prebound) > 1 {
		prebound = prebound[1:]
	} else {
		prebound = nil
	}
	return &BoundFunction{
		Target:            target,
		Offset:            offset,
		PreboundArguments: prebound,
	}
}

// StoredBoundFunctionOf is the bind call behind an expression
// standing where a function value is expected: the expression IS
// `f.bind(...)`, or a NAME (through an import alias) whose const
// initializer is one.
func StoredBoundFunctionOf(ctx *FlowContext, expression *ast.Node) *BoundFunction {
	direct := BoundFunctionOf(ctx, expression)
	if direct != nil {
		return direct
	}
	if !ast.IsIdentifier(expression) {
		return nil
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(expression)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		aliased := func() (result *ast.Symbol) {
			defer func() {
				if recover() != nil {
					result = nil
				}
			}()
			return ctx.P.Checker.GetAliasedSymbol(symbol)
		}()
		if aliased == nil {
			return nil
		}
		symbol = aliased
	}
	if symbol == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil || !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	varDecl := declaration.AsVariableDeclaration()
	if varDecl.Initializer == nil ||
		!ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return nil
	}
	return BoundFunctionOf(ctx, varDecl.Initializer)
}
