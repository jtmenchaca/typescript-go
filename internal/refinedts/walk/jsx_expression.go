// JSX evaluation: an element, a self-closing element, or a fragment
// is never a scalar — SYNTAX-COVERAGE.md §C's own rule, "a JSX
// element / fragment is never an Age (sort mismatch)". A React
// element is an ordinary object at runtime (sec has no bearing here;
// this is JSX.Element's own runtime shape, `{ type, props, ... }`),
// so the walk states exactly that much and no more: KnownObject with
// no read keys, proved, incomplete. That single fact is enough for
// CheckObjectKnown to refute a scalar-only target in plain words
// instead of falling through to the honest "not yet determined"
// alert every plain KindUnknown carries — the same floor a bare
// `new` construction states for a class the walk does not model
// (constructed_instance.go's ConstructedInstance).
//
// The children, the attribute initializers, and every spread
// attribute's source expression still evaluate — a `{ takeAge(x) }`
// expression container or an `age={(check(x), x)}` attribute reaches
// its sink exactly as it would outside JSX, because this file walks
// down into every position a value can appear.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// jsxElementValue is the one AbstractValue every JSX element,
// self-closing element, or fragment reads as: a proved object with
// unstated keys. Never an Age, a string, a boolean, or any other
// scalar sort — CheckObjectKnown/CheckListOrStructured turn that
// single fact into a refutation wherever the target set demonstrably
// rules an object out.
func jsxElementValue() abstractdomain.AbstractValue {
	return abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false)
}

// EvaluateJsxElement walks `<Tag attrs>children</Tag>`: the opening
// tag's attributes and every child evaluate for their own sinks and
// effects, and the element itself reads as jsxElementValue().
func EvaluateJsxElement(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	el := e.AsJsxElement()
	walkJsxAttributes(ctx, env, el.OpeningElement)
	walkJsxChildren(ctx, env, el.Children)
	return jsxElementValue()
}

// EvaluateJsxSelfClosingElement walks `<Tag attrs />`: the
// attributes evaluate, and the element itself reads as
// jsxElementValue().
func EvaluateJsxSelfClosingElement(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	self := e.AsJsxSelfClosingElement()
	walkJsxAttributesNode(ctx, env, self.Attributes)
	return jsxElementValue()
}

// EvaluateJsxFragment walks `<>children</>`: every child evaluates,
// and the fragment itself reads as jsxElementValue().
func EvaluateJsxFragment(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	frag := e.AsJsxFragment()
	walkJsxChildren(ctx, env, frag.Children)
	return jsxElementValue()
}

// EvaluateJsxExpressionContainer walks `{expr}` — an expression
// child or attribute initializer. An empty container (`{/* comment
// */}` or a bare `{}`) carries no expression at all and evaluates to
// nothing; the value otherwise is whatever the wrapped expression
// evaluates to, exactly as if the braces were not there.
func EvaluateJsxExpressionContainer(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	jsxExpr := e.AsJsxExpression()
	if jsxExpr.Expression == nil {
		return abstractdomain.Undef
	}
	return evaluateExpression(ctx, env, jsxExpr.Expression)
}

// walkJsxAttributeCallbackBody walks an attribute's handed-over
// function body directly — `onClick={() => sink(x)}` — the same
// "runs at a time this scope cannot place" reasoning
// CheckCallbackArgument (call_argument_contracts.go) applies to a
// contracted call's callback argument, without that function's
// return-contract requirement: a JSX attribute states no refinement
// on what the handler returns (an event handler's return value is
// never read), so there is no result to check — only the body's own
// sinks, which must still fire. evaluateExpression never reads INTO
// an arrow/function value on its own (SyntaxModels' "read at its
// calls" rule), and no ordinary call site exists for a handler JSX
// merely hands to a component — this is that handoff's own call
// site. A non-function initializer (a string, a spread member, a
// plain value attribute) is untouched; EvaluateJsxExpressionContainer
// already evaluated it as an ordinary expression.
func walkJsxAttributeCallbackBody(ctx *FlowContext, initializer *ast.Node) {
	if !ast.IsJsxExpression(initializer) {
		return
	}
	callback := initializer.AsJsxExpression().Expression
	if callback == nil {
		return
	}
	if !ast.IsArrowFunction(callback) && !ast.IsFunctionExpression(callback) {
		return
	}
	inner := *ctx
	inner.Declared = map[string]*annotations.DeclaredRefinement{}
	inner.ReturnSink = nil
	inner.DifferenceConstraints = nil
	inner.GateAssumptions = nil
	callbackEnv := NewEnv()
	body := callback.Body()
	if ast.IsBlock(body) {
		AnalyzeStatements(&inner, callbackEnv, body.AsBlock().Statements.Nodes, nil)
	} else {
		evaluateExpression(&inner, callbackEnv, body)
	}
}

// walkJsxAttributes walks the attribute list of a
// JsxOpeningElementNode (nil-safe: `<div>` with no attributes list
// still resolves through the checker to an empty JsxAttributes).
func walkJsxAttributes(ctx *FlowContext, env Env, opening *ast.Node) {
	if opening == nil {
		return
	}
	walkJsxAttributesNode(ctx, env, opening.AsJsxOpeningElement().Attributes)
}

// walkJsxAttributesNode walks one JsxAttributes node's properties:
// each plain attribute's initializer evaluates (a string literal
// carries no nested expression; a `{expr}` initializer evaluates
// like any expression container), and each spread attribute's
// source expression evaluates like any other spread source.
func walkJsxAttributesNode(ctx *FlowContext, env Env, attributes *ast.Node) {
	if attributes == nil {
		return
	}
	list := attributes.AsJsxAttributes().Properties
	if list == nil {
		return
	}
	for _, property := range list.Nodes {
		if ast.IsJsxAttribute(property) {
			attr := property.AsJsxAttribute()
			if attr.Initializer == nil {
				continue
			}
			evaluateExpression(ctx, env, attr.Initializer)
			walkJsxAttributeCallbackBody(ctx, attr.Initializer)
		} else if ast.IsJsxSpreadAttribute(property) {
			evaluateExpression(ctx, env, property.AsJsxSpreadAttribute().Expression)
		}
	}
}

// walkJsxChildren walks a JsxChildList: text children carry no
// expression and are skipped, an expression-container child
// evaluates its wrapped expression, and a nested element/self-closing
// element/fragment child recurses through evaluateExpression so its
// own attributes and children are walked in turn.
func walkJsxChildren(ctx *FlowContext, env Env, children *ast.NodeList) {
	if children == nil {
		return
	}
	for _, child := range children.Nodes {
		switch child.Kind {
		case ast.KindJsxText, ast.KindJsxTextAllWhiteSpaces:
			// no expression to evaluate
		case ast.KindJsxExpression:
			EvaluateJsxExpressionContainer(ctx, env, child)
		default:
			// a nested JsxElement, JsxSelfClosingElement, or JsxFragment —
			// evaluateExpression dispatches back into this file's own arms
			evaluateExpression(ctx, env, child)
		}
	}
}
