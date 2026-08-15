// from control_flow/tracked_bindings.ts
//
// Tracked bindings for kernel IR lowering: the sort a slot was read
// under, what typeof answers for its defined values, the spelled name
// a path is tracked by, and the locals a body declares.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
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
// identifier, or a single property step on an identifier or `this` —
// "this.count" is the spelling a method's bundle layout gives its
// field slots.
func SpelledNameOf(e *ast.Node) (string, bool) {
	if ast.IsIdentifier(e) {
		return e.Text(), true
	}
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		if !ast.IsIdentifier(access.Name()) {
			return "", false
		}
		if ast.IsIdentifier(access.Expression) {
			return access.Expression.Text() + "." + access.Name().Text(), true
		}
		if access.Expression.Kind == ast.KindThisKeyword {
			return "this." + access.Name().Text(), true
		}
	}
	return "", false
}

// Unwrapped is the expression behind parens and casts, for sort
// peeking. `<T>e`, `e as T`, and `e satisfies T` all erase to `e` at
// runtime exactly like a plain cast does — TypeScript emits no
// runtime check for any of the three — so all three peel here
// alongside `e!`.
func Unwrapped(e *ast.Node) *ast.Node {
	for ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) ||
		ast.IsTypeAssertion(e) || ast.IsSatisfiesExpression(e) {
		switch {
		case ast.IsParenthesizedExpression(e):
			e = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			e = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			e = e.AsNonNullExpression().Expression
		case ast.IsTypeAssertion(e):
			e = e.AsTypeAssertion().Expression
		case ast.IsSatisfiesExpression(e):
			e = e.AsSatisfiesExpression().Expression
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
// a sequence-shaped initializer — a string literal, a template, a
// `+` chain of those (SpelledSequenceShape) — is a string; anything
// else the lowering reads numerically, and an unreadable initializer
// leaves the sort unknown — tests on it then decline, which only
// loses coverage, never soundness.
func LocalSort(declaration *ast.Node) BindingKind {
	decl := declaration.AsVariableDeclaration()
	initializer := decl.Initializer
	if initializer == nil {
		return BindingKindUnknown
	}
	if SpelledSequenceShape(initializer) {
		return BindingKindString
	}
	// a shape that is DEFINITELY not a number — an object, an array, a
	// function value, a construction, a regex, a bigint — wears unknown,
	// as this comment always promised: tests on it decline, which loses
	// coverage and never soundness. Everything else reads numerically
	// (booleans ride the number sort by the package's own rule).
	head := Unwrapped(initializer)
	if head != nil {
		switch {
		case ast.IsObjectLiteralExpression(head), ast.IsArrayLiteralExpression(head),
			ast.IsFunctionLike(head), ast.IsNewExpression(head),
			ast.IsRegularExpressionLiteral(head), ast.IsBigIntLiteral(head):
			return BindingKindUnknown
		}
	}
	return BindingKindNumber
}

// LocalSortResolved is LocalSort with the host's own type consulted
// for the ONE head shape whose syntax carries no sort evidence at all:
// a CALL. `const s = getName()` reads number under the syntactic
// fallback, and a word written into a number-sorted slot is admitted
// into arithmetic downstream — so the call's resolved return type is
// what fixes the sort.
//
// Only the call head consults the checker. The other shapes reaching
// the number fallback keep their syntactic reading:
//
//   - an identifier or a property access is a REREAD of a place the
//     lowering already laid out, and that slot's own sort is what its
//     readers use; the initializer's slot follows the same reading it
//     always has.
//   - an arithmetic or comparison expression, a numeric literal, an
//     update, a `typeof`/`!` unary — the syntax itself is the numeric
//     evidence (booleans ride the number sort by this package's own
//     rule, stated on LocalSort).
//
// A nil checker means syntax only: the answer is LocalSort's, so a
// caller lowering without a checker keeps exactly today's behaviour.
// A resolved type that is neither number-like nor string-like, that
// mixes sorts, or that resolves to nothing wears unknown — tests on it
// then decline, which loses coverage and never soundness.
func LocalSortResolved(c *checker.Checker, declaration *ast.Node) BindingKind {
	syntactic := LocalSort(declaration)
	if c == nil || syntactic != BindingKindNumber {
		return syntactic
	}
	decl := declaration.AsVariableDeclaration()
	initializer := decl.Initializer
	if initializer == nil {
		return syntactic
	}
	head := Unwrapped(initializer)
	if head == nil || !ast.IsCallExpression(head) {
		return syntactic
	}
	t := c.GetTypeAtLocation(head)
	if t == nil {
		return BindingKindUnknown
	}
	// the same masking the statement dispatch reads a sort under: a type
	// wearing ONLY number/boolean flags is the number sort, one wearing
	// only string flags the string sort, and a union across the two — or
	// anything else, `any` included — is unknown
	flags := t.Flags()
	numOrBool := checker.TypeFlagsNumber | checker.TypeFlagsNumberLiteral |
		checker.TypeFlagsBoolean | checker.TypeFlagsBooleanLiteral
	if (flags&numOrBool) != 0 && (flags & ^numOrBool) == 0 {
		return BindingKindNumber
	}
	strOrLit := checker.TypeFlagsString | checker.TypeFlagsStringLiteral
	if (flags&strOrLit) != 0 && (flags & ^strOrLit) == 0 {
		return BindingKindString
	}
	return BindingKindUnknown
}

// ResolvedExpressionSort is LocalSortResolved's masking applied to an
// EXPRESSION rather than to a declaration's initializer: the sort and
// the typeof evidence the host's own resolved type states for a value
// the lowering has no declaration to read.
//
// The one caller today is the hoisted call temp (ir_call_hoist.go),
// which holds a callee's return with no `const x = f()` anywhere to
// read the sort off. LocalSortResolved cannot serve it — that function
// takes a VariableDeclaration and asks LocalSort first, and a hoisted
// temp has neither — so the masking it states is restated here over the
// type alone, and the two must agree or one call site would sort its
// result differently from `const x = f()` on the same callee.
//
// THE MASKING IS LocalSortResolved'S, unchanged: a type wearing ONLY
// number/boolean flags is the number sort (booleans ride the number
// sort by this package's own rule, stated on LocalSort), one wearing
// only string flags the string sort, and everything else — a union
// across the two, `any`, `unknown`, an object, a Promise — is unknown,
// which admits only the definedness test. A nil checker or an
// unresolvable type is unknown for the same reason.
//
// THE TYPEOF HALF is what the resolved type can say and the sort
// cannot: number and boolean share the number sort, and `typeof`
// answers differently for them. A type wearing only boolean flags
// answers "boolean", only number flags "number", only string flags
// "string" — the same three annotationSort spells from a type NODE,
// read here from the resolved type instead.
func ResolvedExpressionSort(c *checker.Checker, e *ast.Node) (BindingKind, TypeofTag) {
	if c == nil || e == nil {
		return BindingKindUnknown, TypeofTagNone
	}
	t := c.GetTypeAtLocation(e)
	if t == nil {
		return BindingKindUnknown, TypeofTagNone
	}
	// the same masking the statement dispatch reads a sort under, and the
	// same one LocalSortResolved applies to a call initializer
	flags := t.Flags()
	num := checker.TypeFlagsNumber | checker.TypeFlagsNumberLiteral
	boolean := checker.TypeFlagsBoolean | checker.TypeFlagsBooleanLiteral
	numOrBool := num | boolean
	if (flags&numOrBool) != 0 && (flags & ^numOrBool) == 0 {
		// number and boolean share the SORT and differ in typeof: a type
		// wearing boolean flags alone answers "boolean", one wearing number
		// flags alone "number", and a mixture of the two claims neither
		switch {
		case (flags & ^boolean) == 0:
			return BindingKindNumber, TypeofTagBoolean
		case (flags & ^num) == 0:
			return BindingKindNumber, TypeofTagNumber
		}
		return BindingKindNumber, TypeofTagNone
	}
	strOrLit := checker.TypeFlagsString | checker.TypeFlagsStringLiteral
	if (flags&strOrLit) != 0 && (flags & ^strOrLit) == 0 {
		return BindingKindString, TypeofTagString
	}
	return BindingKindUnknown, TypeofTagNone
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
	if SpelledSequenceShape(e) {
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
