// split from ir_accessor_calls.go — the setter's doors: the statement write, the written value, the expression position

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// SetterWriteOf is the statement door for an accessor-backed write:
// `this.value = e` / `holder.value = e` where the property's symbol
// declares a SET accessor. The right side lowers under the setter
// parameter's own declared sort, and the write is the setter's call
// statement (SetterWriteStatements) — the value at entry 0, the
// written this-fields riding back through rets.
//
// The door reads THREE assigning shapes, each the same call statement
// with a different value at entry 0:
//
//   - `o.x = e` — the plain write, the value being e itself;
//   - `o.x += e` and its arithmetic siblings — the read-modify-write
//     the runtime performs: the GETTER's call, the arithmetic, the
//     SETTER's call (setterCompoundWriteOf);
//   - `o.x++` / `--o.x` — the same, with the constant 1 for e
//     (setterUpdateWriteOf).
func SetterWriteOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	// `o.x++` / `--o.x`: no binary node at all, so the update route reads
	// it ahead of the binary shapes below
	if written, ok := setterUpdateWriteOf(context, e); ok {
		return written, true
	}
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		// `o.x += e` and its siblings: the compound reads through the
		// getter before it writes through the setter
		return setterCompoundWriteOf(context, bin)
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	if context.Flow == nil {
		return nil, false
	}
	value, valueOk := setterValueEffect(context, access, bin.Right)
	if !valueOk {
		return nil, false
	}
	return SetterWriteStatements(context, access, value)
}

// setterValueEffect lowers a written RIGHT SIDE under the SETTER
// parameter's own declared sort — the entry it fills is that
// parameter's, so the sort the value is read at is the parameter's and
// never the site's.
//
// Declines where the access resolves to no setter (a get-only property
// or a plain field, neither of which this file's routes claim) and
// where the right side itself fails to lower.
func setterValueEffect(
	context *LoweringContext,
	access *ast.Node,
	right *ast.Node,
) (kernelbridge.LoopEffect, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.LoopEffect{}, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	sort := BindingKindUnknown
	if parameters := setter.Parameters(); len(parameters) == 1 {
		sort = declaredParamSort(parameters[0])
	}
	return RhsEffect(context, sort, right)
}

/* ── the setter in expression position ───────────────────────────── */

// SetterAssignmentEffect lowers `o.x = e` used as a VALUE — `f(o.x =
// e)`, `y = (o.x = e)`, an arm of a ternary — rather than as a bare
// statement.
//
// The language's own rule makes this the getter read's shape exactly.
// An assignment expression's value is its RIGHT SIDE, not whatever the
// setter did with it: `f(o.x = 3)` passes 3 to f however the setter
// stored it, and reads the property back not at all. So the whole
// lowering is
//
//	         call setter(e)      (hoisted)
//	the expression's value := e
//
// — the setter's call statement pushed into context.Hoisted, which the
// statement route flushes ahead of its own statements, and the answered
// effect being the value the caller already lowered.
//
// A SETTER IN EXPRESSION POSITION WITH NO STATEMENT POSITION is the one
// shape this cannot carry, and it is named rather than dropped. Where
// CanHoist is false — a loop head, a branch test, any reading with no
// statement stream (ir_lowering_context.go's own list) — there is
// nowhere to put the call, and a call that runs on a path the IR does
// not spell is a wrong answer, not a weak one. That decline calls
// NoteDeclinedConstruct so the outcome report names the syntax someone
// can act on.
//
// The COMPOUND and UPDATE forms in expression position are not read
// here. Their value is the stepped value (or, for a postfix, the value
// before the step), which is the GETTER's temp and not the right side —
// a different reading, and one that also has to answer which of the two
// the position wanted. This route claims the plain assignment alone,
// whose value the language pins without any reading of the accessor at
// all.
func SetterAssignmentEffect(
	context *LoweringContext,
	e *ast.Node,
) (kernelbridge.LoopEffect, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(e)
	if !ast.IsBinaryExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	bin := head.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return kernelbridge.LoopEffect{}, false
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return kernelbridge.LoopEffect{}, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	if !context.CanHoist {
		NoteDeclinedConstruct(context, "a setter in expression position")
		return kernelbridge.LoopEffect{}, false
	}
	value, valueOk := setterValueEffect(context, access, bin.Right)
	if !valueOk {
		return kernelbridge.LoopEffect{}, false
	}
	written, writeOk := SetterWriteStatements(context, access, value)
	if !writeOk {
		NoteDeclinedConstruct(context, "a setter in expression position")
		return kernelbridge.LoopEffect{}, false
	}
	context.Hoisted = append(context.Hoisted, written...)
	// the assignment's value is the RIGHT SIDE by the language's rule —
	// what the setter stored is the setter's business, and nothing here
	// reads the property back
	return value, true
}
