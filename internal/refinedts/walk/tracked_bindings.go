// from control_flow/tracked_bindings.ts
//
// Tracked bindings for kernel IR lowering: the sort a slot was read
// under, what typeof answers for its defined values, the spelled name
// a path is tracked by, and the locals a body declares.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
)

// BindingKind is the sort a binding was READ UNDER — the host-type
// layer that tells "A", 65, and [65] apart. Every value test and
// every arithmetic read is admitted only under its sort; "unknown"
// admits nothing but the definedness test (the admitted-language
// rule: a reread across sorts is never a claim).
type BindingKind string

const (
	BindingKindNumber  BindingKind = "number"
	BindingKindString  BindingKind = "string"
	BindingKindUnknown BindingKind = "unknown"
)

// TypeofTag is what `typeof` answers for a slot's every DEFINED
// value, where the knowledge pins one — "" claims nothing and
// typeof tests on the slot decline. Kept apart from BindingKind
// because booleans ride the number SORT while their typeof is
// "boolean".
type TypeofTag string

const (
	TypeofTagNumber  TypeofTag = "number"
	TypeofTagString  TypeofTag = "string"
	TypeofTagBoolean TypeofTag = "boolean"
	TypeofTagNone    TypeofTag = ""
)

// SpelledNameOf is the spelled name a binding is tracked under: an
// identifier, or a single property step on an identifier.
func SpelledNameOf(e *ast.Node) (string, bool) {
	if ast.IsIdentifier(e) {
		return e.Text(), true
	}
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		if ast.IsIdentifier(access.Expression) && ast.IsIdentifier(access.Name()) {
			return access.Expression.Text() + "." + access.Name().Text(), true
		}
	}
	return "", false
}

// Unwrapped is the expression behind parens and casts, for sort
// peeking.
func Unwrapped(e *ast.Node) *ast.Node {
	for ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) {
		switch {
		case ast.IsParenthesizedExpression(e):
			e = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			e = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			e = e.AsNonNullExpression().Expression
		}
	}
	return e
}

// NumberOf is a literal number, through a leading minus.
func NumberOf(e *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(e) {
		return float64(jsnum.FromString(e.AsNumericLiteral().Text)), true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			return -float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text)), true
		}
	}
	return 0, false
}

// CollectLocalsResult is the locals collectLocals found, or nothing
// when the body declined.
type CollectLocalsResult struct {
	Locals []*ast.Node // VariableDeclaration
}

// CollectLocals is single-identifier local declarations across a
// body, in source order — recursing into branch arms and blocks,
// never into nested functions (a nested function anywhere declines:
// its captures read and write outside the lowered world).
//
// A local holding an object literal is collected here like any other
// single-identifier local; what it becomes in the slot vector is
// decided afterwards by the recognizer in ir_object_slots.go, which
// either flattens it into one slot per key ("p.lo", "p.hi") or leaves
// it a single whole-name slot whose key reads then find no slot and
// decline the lowering.
func CollectLocals(body *ast.Node) (CollectLocalsResult, bool) {
	var locals []*ast.Node
	declined := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if declined {
			return false
		}
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			declined = true
			return false
		}
		if ast.IsVariableDeclaration(node) {
			if !ast.IsIdentifier(node.Name()) {
				declined = true
				return false
			}
			locals = append(locals, node)
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if declined {
		return CollectLocalsResult{}, false
	}
	return CollectLocalsResult{Locals: locals}, true
}

// LocalSort is a local's sort, read from its initializer's syntax:
// a string literal is a string; anything else the lowering reads
// numerically, and an unreadable initializer leaves the sort
// unknown — tests on it then decline, which only loses coverage,
// never soundness.
func LocalSort(declaration *ast.Node) BindingKind {
	decl := declaration.AsVariableDeclaration()
	initializer := decl.Initializer
	if initializer == nil {
		return BindingKindUnknown
	}
	if ast.IsStringLiteral(initializer) {
		return BindingKindString
	}
	return BindingKindNumber
}

// LocalTypeof is a local's typeof evidence, from its initializer's
// syntax alone.
func LocalTypeof(declaration *ast.Node) TypeofTag {
	decl := declaration.AsVariableDeclaration()
	initializer := decl.Initializer
	if initializer == nil {
		return TypeofTagNone
	}
	e := Unwrapped(initializer)
	if ast.IsStringLiteral(e) {
		return TypeofTagString
	}
	if _, ok := NumberOf(e); ast.IsNumericLiteral(e) || ok {
		return TypeofTagNumber
	}
	if e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword {
		return TypeofTagBoolean
	}
	return TypeofTagNone
}
