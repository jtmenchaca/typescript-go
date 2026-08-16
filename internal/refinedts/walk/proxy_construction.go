// What `new Proxy(target, handler)` reads: the trapless [[Get]]
// forward, sec-proxy-object-internal-methods-and-internal-slots-get-p-
// receiver step 5 — "If handler.[[Get]] is undefined, then return
// ? target.[[Get]](P, Receiver)." A handler literal that never names a
// `get` trap forwards every property read straight to the target, so
// the constructed value reads exactly as the target's own value for
// every KEY LOOKUP a later `.prop`/`[k]` read performs — the same
// object, as far as this walk's read-only vocabulary can tell apart.
//
// This is deliberately narrower than "the Proxy IS the target": a
// `set`/`deleteProperty`/`has` trap changes what a WRITE or `in` test
// does, which this model does not claim anything about — only that a
// PROPERTY READ forwards, which is true regardless of what other traps
// the handler carries (spec step 5 is [[Get]]'s own algorithm, and
// nothing about [[Set]]/[[HasProperty]] changes it).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// handlerNamesGetTrap answers whether a handler object literal spells
// a `get` member at all — a PropertyAssignment, ShorthandPropertyAssignment,
// or MethodDeclaration named "get". A SpreadAssignment or a computed
// key could carry a `get` trap this static read cannot rule out, so
// either costs the handler its trapless reading (conservative: an
// unreadable handler shape declines the forward rather than risking an
// unsound claim).
func handlerNamesGetTrap(handler *ast.Node) (namesGet bool, readable bool) {
	if !ast.IsObjectLiteralExpression(handler) {
		return false, false
	}
	for _, property := range handler.AsObjectLiteralExpression().Properties.Nodes {
		switch {
		case ast.IsPropertyAssignment(property):
			name := property.AsPropertyAssignment().Name()
			if name == nil || !ast.IsIdentifier(name) {
				return false, false
			}
			if name.Text() == "get" {
				return true, true
			}
		case ast.IsShorthandPropertyAssignment(property):
			if property.AsShorthandPropertyAssignment().Name().Text() == "get" {
				return true, true
			}
		case ast.IsMethodDeclaration(property):
			name := property.Name()
			if name == nil || !ast.IsIdentifier(name) {
				return false, false
			}
			if name.Text() == "get" {
				return true, true
			}
		default:
			// a spread or an accessor/computed-named member could carry
			// a `get` trap this static read cannot see
			return false, false
		}
	}
	return false, true
}

// ReadProxyConstruction is `new Proxy(target, handler)` on the global
// constructor: the target's own value where the handler is a
// WRITE-/CALL-FREE object literal that provably names no `get` trap —
// nil for every other shape (a non-literal handler, a `get` trap
// present, a handler this read cannot fully enumerate).
func ReadProxyConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.Text() != "Proxy" ||
		!resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 2 {
		return nil
	}
	targetNode, handlerNode := newExpr.Arguments.Nodes[0], newExpr.Arguments.Nodes[1]
	handler := Unwrapped(handlerNode)
	if !ast.IsObjectLiteralExpression(handler) || !writeAndCallFree(handler) {
		return nil
	}
	namesGet, readable := handlerNamesGetTrap(handler)
	if !readable || namesGet {
		return nil
	}
	target := evaluateExpression(ctx, env, targetNode)
	// the handler still evaluates for its own effects (a computed
	// member name, though writeAndCallFree already rules out a write or
	// call inside it) — reading it as a value is not needed, since the
	// trapless forward claims nothing about the wrapper itself
	evaluateExpression(ctx, env, handlerNode)
	return &target
}
