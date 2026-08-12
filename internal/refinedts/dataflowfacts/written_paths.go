// What code WRITES — the one home for the write questions
// (finding 14): which names a subtree assigns directly, which it
// can reach through calls, and whether a recorded fact about a
// place survives a stretch of code. The write that EXECUTES lives
// with the assignment readers (bindings/assignments.ts writeBinding
// / writeProperty / forgetThrough); this file answers what a piece
// of code COULD write.
package dataflowfacts

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── write collection ────────────────────────────────────────────── */

// targetIdentifiers collects every identifier an assignment target can
// reach — destructuring patterns included, conservatively.
func targetIdentifiers(target *ast.Node, into map[string]struct{}) {
	if ast.IsIdentifier(target) {
		into[target.Text()] = struct{}{}
		return
	}
	// a write through `this` (`this.#x = …`) writes the instance the
	// "this" binding tracks
	if target.Kind == ast.KindThisKeyword {
		into["this"] = struct{}{}
		return
	}
	target.ForEachChild(func(child *ast.Node) bool {
		targetIdentifiers(child, into)
		return false
	})
}

// WrittenNames collects the names the subtree writes DIRECTLY: assignment
// operators, ++/--, and for-in/for-of heads that assign an existing
// binding. An `arguments` mention poisons everything ("*") — a
// sloppy-mode body could alias a parameter through it. Descends into
// nested functions: their writes land whenever they run.
func WrittenNames(node *ast.Node, into map[string]struct{}) {
	if ast.IsIdentifier(node) && node.Text() == "arguments" {
		into["*"] = struct{}{}
	}
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
			bin.OperatorToken.Kind <= ast.KindLastAssignment {
			targetIdentifiers(bin.Left, into)
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			targetIdentifiers(unary.Operand, into)
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			targetIdentifiers(unary.Operand, into)
		}
	}
	if ast.IsForOfStatement(node) || ast.IsForInStatement(node) {
		forStmt := node.AsForInOrOfStatement()
		if !ast.IsVariableDeclarationList(forStmt.Initializer) {
			targetIdentifiers(forStmt.Initializer, into)
		}
	}
	node.ForEachChild(func(child *ast.Node) bool {
		WrittenNames(child, into)
		return false
	})
}

// reachedNames is direct writes plus the names calls could REACH: a
// method's receiver, and identifiers handed over as arguments — the
// write set a REFERENCE-shaped fact must respect.
func reachedNames(node *ast.Node, into map[string]struct{}) {
	WrittenNames(node, into)
	var visit func(n *ast.Node) bool
	visit = func(n *ast.Node) bool {
		if ast.IsCallExpression(n) || ast.IsNewExpression(n) {
			var callee *ast.Node
			var arguments []*ast.Node
			if ast.IsCallExpression(n) {
				call := n.AsCallExpression()
				callee = call.Expression
				if call.Arguments != nil {
					arguments = call.Arguments.Nodes
				}
			} else {
				newExpr := n.AsNewExpression()
				callee = newExpr.Expression
				if newExpr.Arguments != nil {
					arguments = newExpr.Arguments.Nodes
				}
			}
			if ast.IsPropertyAccessExpression(callee) {
				receiver := callee.AsPropertyAccessExpression().Expression
				for ast.IsPropertyAccessExpression(receiver) ||
					ast.IsElementAccessExpression(receiver) ||
					ast.IsParenthesizedExpression(receiver) {
					switch {
					case ast.IsPropertyAccessExpression(receiver):
						receiver = receiver.AsPropertyAccessExpression().Expression
					case ast.IsElementAccessExpression(receiver):
						receiver = receiver.AsElementAccessExpression().Expression
					case ast.IsParenthesizedExpression(receiver):
						receiver = receiver.AsParenthesizedExpression().Expression
					}
				}
				if ast.IsIdentifier(receiver) {
					into[receiver.Text()] = struct{}{}
				} else if receiver.Kind == ast.KindThisKeyword {
					into["this"] = struct{}{}
				}
			}
			for _, argument := range arguments {
				if ast.IsIdentifier(argument) {
					into[argument.Text()] = struct{}{}
				} else if argument.Kind == ast.KindThisKeyword {
					into["this"] = struct{}{}
				}
			}
		}
		n.ForEachChild(visit)
		return false
	}
	visit(node)
}

var (
	directWritesMu     sync.Mutex
	directWritesCache  = map[*ast.Node]map[string]struct{}{}
	reachedWritesMu    sync.Mutex
	reachedWritesCache = map[*ast.Node]map[string]struct{}{}
)

// DirectWrites is memoized WrittenNames.
//
// The TS source keys this memo with a WeakMap<ts.Node, …>; Go has no weak
// maps, so this substitutes a regular map guarded by a mutex. Functionally
// identical per program: entries live exactly as long as the program that
// produced their nodes is in use by this port.
func DirectWrites(node *ast.Node) map[string]struct{} {
	directWritesMu.Lock()
	held, ok := directWritesCache[node]
	directWritesMu.Unlock()
	if ok {
		return held
	}
	found := map[string]struct{}{}
	WrittenNames(node, found)
	directWritesMu.Lock()
	directWritesCache[node] = found
	directWritesMu.Unlock()
	return found
}

// ReachedWrites is memoized reachedNames.
func ReachedWrites(node *ast.Node) map[string]struct{} {
	reachedWritesMu.Lock()
	held, ok := reachedWritesCache[node]
	reachedWritesMu.Unlock()
	if ok {
		return held
	}
	found := map[string]struct{}{}
	reachedNames(node, found)
	reachedWritesMu.Lock()
	reachedWritesCache[node] = found
	reachedWritesMu.Unlock()
	return found
}

// StableIn reports whether a fact about `place` recorded outside `scopes`
// still binds every read inside them: nothing in any scope (nor any
// closure of the enclosing function) can have rewritten it.
func StableIn(place PlaceKey, scopes []*ast.Node, fn *ast.Node) bool {
	writesOf := ReachedWrites
	if place.Path == "" {
		writesOf = DirectWrites
	}
	for _, scope := range scopes {
		writes := writesOf(scope)
		if _, ok := writes["*"]; ok {
			return false
		}
		if _, ok := writes[place.BaseName]; ok {
			return false
		}
	}
	for _, nested := range NestedFunctions(fn) {
		writes := writesOf(nested)
		if _, ok := writes["*"]; ok {
			return false
		}
		if _, ok := writes[place.BaseName]; ok {
			return false
		}
	}
	return true
}
