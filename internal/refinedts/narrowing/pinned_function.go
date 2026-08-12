// Resolve a function-valued expression to the ONE body a call
// provably runs: an arrow or function expression directly, a const
// binding's initializer, or a function declaration whose name the
// file never reassigns. Every channel that reads a body without a
// call (predicate lifting, callback inlining, the narrow tree)
// resolves here, so the const and reassignment guards cannot
// diverge. Split from condition_analysis.ts per the v2 tree.

package narrowing

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// PinnedFunctionOf is pinnedFunctionOf in the TS source: resolve a
// function-valued expression to the ONE body a call provably runs: an
// arrow or function expression directly, a const binding's initializer,
// or a function declaration (with a body) whose name the file never
// reassigns — through an import alias either way. nil anywhere the body
// is not pinned. Every channel that reads a body without a call
// (predicate lifting, callback inlining, the narrow tree) resolves
// HERE, so the const and reassignment guards cannot diverge between
// them.
func PinnedFunctionOf(c *checker.Checker, e *ast.Node) *ast.Node {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	if ast.IsArrowFunction(cursor) || ast.IsFunctionExpression(cursor) {
		return cursor
	}
	if !ast.IsIdentifier(cursor) {
		return nil
	}
	symbol := c.GetSymbolAtLocation(cursor)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		// an imported function reads the same — the alias followed to
		// its declaration
		aliased := func() (result *ast.Symbol) {
			defer func() {
				if recover() != nil {
					result = nil
				}
			}()
			return c.GetAliasedSymbol(symbol)
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
	if declaration == nil {
		return nil
	}
	if ast.IsFunctionDeclaration(declaration) {
		if declaration.Body() == nil {
			return nil // ambient
		}
		name := declaration.Name()
		if name == nil {
			return nil
		}
		if _, reassigned := ReassignedNames(ast.GetSourceFileOfNode(declaration))[name.Text()]; reassigned {
			return nil
		}
		return declaration
	}
	if ast.IsVariableDeclaration(declaration) {
		varDecl := declaration.AsVariableDeclaration()
		if varDecl.Initializer != nil && ast.IsVariableDeclarationList(declaration.Parent) &&
			(declaration.Parent.Flags&ast.NodeFlagsConst) != 0 {
			init := varDecl.Initializer
			for ast.IsParenthesizedExpression(init) {
				init = init.AsParenthesizedExpression().Expression
			}
			if ast.IsArrowFunction(init) || ast.IsFunctionExpression(init) {
				return init
			}
			// a const initialized by a CALL to a pinned factory whose every
			// return is the SAME function pins that function — `const add =
			// makeAdder()` reads like a const-bound arrow. The guard set
			// stops a cycle of call-initialized consts from re-entering.
			if ast.IsCallExpression(init) {
				resolvingFactoryMu.Lock()
				_, cycling := resolvingFactoryBindings[declaration]
				if cycling {
					resolvingFactoryMu.Unlock()
					return nil
				}
				resolvingFactoryBindings[declaration] = struct{}{}
				resolvingFactoryMu.Unlock()
				defer func() {
					resolvingFactoryMu.Lock()
					delete(resolvingFactoryBindings, declaration)
					resolvingFactoryMu.Unlock()
				}()
				return FactoryPinnedFunction(c, init)
			}
			return nil
		}
		return nil
	}
	return nil
}

// resolvingFactoryMu guards resolvingFactoryBindings: the call-initialized
// const bindings currently being resolved — a cycle
// (`const a = b(); const b = a();`) answers nil instead of re-entering.
//
// The TS source keys this with a `Set<ts.Declaration>`; this substitutes
// a regular map guarded by a mutex for the same reason the memoized
// caches elsewhere in this port do (see reassigned_names.go).
var (
	resolvingFactoryMu       sync.Mutex
	resolvingFactoryBindings = map[*ast.Node]struct{}{}
)

// ownReturnExpressions is ownReturnExpressions in the TS source: the
// return expressions the factory's OWN body makes — a nested function's
// returns are not the factory's. True only when every exit carries an
// expression; a bare `return` means the call may answer undefined,
// which pins nothing.
func ownReturnExpressions(body *ast.Node, into *[]*ast.Node) bool {
	everyReturnCarries := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsArrowFunction(node) || ast.IsFunctionExpression(node) ||
			ast.IsFunctionDeclaration(node) || ast.IsClassLike(node) {
			return false
		}
		if ast.IsReturnStatement(node) {
			expr := node.AsReturnStatement().Expression
			if expr == nil {
				everyReturnCarries = false
			} else {
				*into = append(*into, expr)
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	body.ForEachChild(visit)
	return everyReturnCarries
}

// capturesUnthreadedState is capturesUnthreadedState in the TS source:
// does the function read any binding declared inside `factory` but
// outside itself (the factory's parameters and locals — state this
// resolver does not thread), or any outer name the file ever
// reassigns? Either capture unpins: the body would be walked on the
// CALLER's environment, which does not hold that state.
func capturesUnthreadedState(c *checker.Checker, fn *ast.Node, factory *ast.Node) bool {
	file := ast.GetSourceFileOfNode(fn)
	reassigned := ReassignedNames(file)
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if ast.IsIdentifier(node) {
			parent := node.Parent
			// a property NAME is not a use of a binding
			if ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Name() == node {
				return false
			}
			if ast.IsPropertyAssignment(parent) && parent.AsPropertyAssignment().Name() == node {
				return false
			}
			if ast.IsBindingElement(parent) && parent.AsBindingElement().PropertyName == node {
				return false
			}
			symbol := c.GetSymbolAtLocation(node)
			if symbol == nil {
				return false
			}
			declaration := symbol.ValueDeclaration
			if declaration == nil {
				return false // type-only or ambient
			}
			if ast.GetSourceFileOfNode(declaration) != file {
				return false // another file's
			}
			inside := func(owner *ast.Node) bool {
				return declaration.Pos() >= owner.Pos() && declaration.End() <= owner.End()
			}
			if inside(fn) {
				return false // the function's own parameter or local
			}
			if inside(factory) {
				found = true // factory state the caller's environment lacks
				return true
			}
			if _, ok := reassigned[node.Text()]; ok {
				found = true
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(fn)
	return found
}

// FactoryPinnedFunction is factoryPinnedFunction in the TS source: the
// ONE function a factory call provably answers: the callee is pinned,
// every return the factory's own body makes resolves — directly or
// through a const-bound name — to the SAME function node, and that
// function captures nothing this resolver cannot thread (see
// capturesUnthreadedState). nil anywhere the pin is not certain; the
// caller then treats the call as unknown, exactly as before.
func FactoryPinnedFunction(c *checker.Checker, call *ast.Node) *ast.Node {
	callExpr := call.AsCallExpression()
	factory := PinnedFunctionOf(c, callExpr.Expression)
	if factory == nil {
		return nil
	}
	factoryBody := factory.Body()
	if factoryBody == nil {
		return nil
	}
	var returns []*ast.Node
	if ast.IsBlock(factoryBody) {
		if !ownReturnExpressions(factoryBody, &returns) {
			return nil
		}
	} else {
		returns = append(returns, factoryBody)
	}
	if len(returns) == 0 {
		return nil
	}
	var pinned *ast.Node
	for _, expression := range returns {
		fn := PinnedFunctionOf(c, expression)
		if fn == nil {
			return nil
		}
		if pinned == nil {
			pinned = fn
		} else if pinned != fn {
			return nil // two different functions
		}
	}
	if pinned == nil || pinned.Body() == nil {
		return nil
	}
	if capturesUnthreadedState(c, pinned, factory) {
		return nil
	}
	return pinned
}

// BodyExpressionOf is bodyExpressionOf in the TS source: the single
// expression a predicate body computes: a concise arrow body, or a
// block that is exactly one `return`.
func BodyExpressionOf(fn *ast.Node) *ast.Node {
	body := fn.Body()
	if body == nil {
		return nil
	}
	if !ast.IsBlock(body) {
		return body
	}
	statements := body.AsBlock().Statements
	if statements == nil || len(statements.Nodes) != 1 {
		return nil
	}
	only := statements.Nodes[0]
	if !ast.IsReturnStatement(only) || only.AsReturnStatement().Expression == nil {
		return nil
	}
	return only.AsReturnStatement().Expression
}
