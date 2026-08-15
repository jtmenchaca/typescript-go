// split from ir_guard.go — the typeof head reader and its word table

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

var typeofWords = map[string]struct{}{
	"number": {}, "string": {}, "boolean": {}, "object": {},
	"function": {}, "symbol": {}, "bigint": {}, "undefined": {},
}

// TypeofReadResult is the union TS return type of typeofRead:
// {kind:"test",...} | {kind:"constant",...} | null.
type TypeofReadResult struct {
	IsTest     bool
	On         int
	Positive   bool
	IsConstant bool
	Value      bool
}

// TypeofRead is typeofRead in the TS source: a `typeof x === "…"` /
// `!==` head on a tracked slot. Under the slot's typeof evidence the
// test collapses: quoting "undefined" IS the definedness test under
// any evidence; quoting the slot's own tag is definedness too (every
// defined value answers the tag); any other valid quote can never
// hold (values answer the tag, absence answers "undefined") — a
// constant. No evidence, no claim.
func TypeofRead(context *LoweringContext, head *ast.Node) (TypeofReadResult, bool) {
	if !ast.IsBinaryExpression(head) {
		return TypeofReadResult{}, false
	}
	bin := head.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	eq := op == ast.KindEqualsEqualsEqualsToken
	ne := op == ast.KindExclamationEqualsEqualsToken
	if !eq && !ne {
		return TypeofReadResult{}, false
	}
	left := Unwrapped(bin.Left)
	right := Unwrapped(bin.Right)
	var typeofSide *ast.Node
	switch {
	case ast.IsTypeOfExpression(left):
		typeofSide = left
	case ast.IsTypeOfExpression(right):
		typeofSide = right
	}
	var litSide *ast.Node
	switch {
	case ast.IsStringLiteral(left):
		litSide = left
	case ast.IsStringLiteral(right):
		litSide = right
	}
	if typeofSide == nil || litSide == nil {
		return TypeofReadResult{}, false
	}
	on, ok := IndexOf(context, Unwrapped(typeofSide.AsTypeOfExpression().Expression))
	if !ok {
		return TypeofReadResult{}, false
	}
	quoted := litSide.AsStringLiteral().Text
	if quoted == "undefined" {
		return TypeofReadResult{IsTest: true, On: on, Positive: ne}, true
	}
	var tag TypeofTag
	if context.Typeofs != nil && on < len(context.Typeofs) {
		tag = context.Typeofs[on]
	}
	if tag == TypeofTagNone {
		return TypeofReadResult{}, false
	}
	if quoted == string(tag) {
		return TypeofReadResult{IsTest: true, On: on, Positive: eq}, true
	}
	if _, known := typeofWords[quoted]; known {
		return TypeofReadResult{IsConstant: true, Value: ne}, true
	}
	return TypeofReadResult{}, false
}
