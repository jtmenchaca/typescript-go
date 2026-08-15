// split from ir_accessor_calls.go — the compound, the update, and the read-modify-write they share

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the compound: a read, the arithmetic, a write ───────────────── */

// setterCompoundOps is the compound assignment's operator, as the
// effect grammar's own arithmetic. It is compoundOps' table
// (ir_assignment.go) read for the accessor route: the same operators,
// because the same effect grammar carries them, and a compound through
// a setter must compute what a compound through a slot computes or the
// two spell different arithmetic for the same source.
var setterCompoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
	// the bitwise and shift compounds, matching compoundOps
	ast.KindAmpersandEqualsToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindBarEqualsToken:                               kernelbridge.LoopOpBitOr,
	ast.KindCaretEqualsToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanEqualsToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanEqualsToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: kernelbridge.LoopOpShr,
}

// setterCompoundWriteOf lowers `o.x += e` where x resolves to a get/set
// PAIR: the read-modify-write the runtime itself performs, spelled as
// the two calls it really is —
//
//	#get.o.x := call getter(…)        (hoisted, GetterReadEffect's own shape)
//	          call setter(#get.o.x + e)
//
// The getter's call statement rides out through context.Hoisted, which
// the statement dispatch flushes AHEAD of whatever this route returns
// (TakeHoisted) — so the read runs before the write, which is the order
// the language runs them in.
//
// THE PAIR IS REQUIRED, both halves. A compound through a get-only
// property writes nothing the language defines, and a compound through
// a set-only property reads `undefined` from a property with no getter
// — the arithmetic is then NaN, a value this route does not claim. So
// the route wants a getter AND a setter, and declines otherwise.
//
// The `||=`/`&&=`/`??=` family is NOT read here. Those short-circuit:
// the setter may not run at all, and no call statement stands for a
// call that may not have happened — the same reason the optional step
// declines at the resolution. The refusal is named so the report points
// at the syntax.
//
// Every other decline is the two halves' own: GetterReadEffect's
// (CanHoist, the allocator, the getter's blob) and
// SetterWriteStatements' (the setter's blob, the receiver path, the
// statement builder). Neither is second-guessed here — where either
// half declines, so does the compound, and the statement falls to the
// floor exactly as it did before this route existed.
func setterCompoundWriteOf(
	context *LoweringContext,
	bin *ast.BinaryExpression,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	switch bin.OperatorToken.Kind {
	case ast.KindBarBarEqualsToken, ast.KindAmpersandAmpersandEqualsToken,
		ast.KindQuestionQuestionEqualsToken:
		// a short-circuiting compound: the setter runs on SOME runs and not
		// others, and one call statement claims it ran on every one
		if _, setter, resolved := AccessorDeclarationsOf(context.Flow, access); resolved && setter != nil {
			NoteDeclinedConstruct(context, "a short-circuiting compound assignment through a setter")
		}
		return nil, false
	}
	op, isArithmetic := setterCompoundOps[bin.OperatorToken.Kind]
	if !isArithmetic {
		return nil, false
	}
	right, rightOk := EffectOf(context, bin.Right)
	if !rightOk {
		return nil, false
	}
	return setterReadModifyWrite(context, access, op, right)
}

/* ── the update: the compound with a constant operand ────────────── */

// setterUpdateWriteOf lowers `o.x++` and `--o.x` where x resolves to a
// get/set PAIR: the compound case with the constant 1 for its operand.
//
// PREFIX AND POSTFIX LOWER THE SAME. The two differ only in the VALUE
// the expression itself answers — the stepped value for a prefix, the
// value before the step for a postfix — and in a statement position
// nothing reads that value. The EFFECT is identical: the getter runs,
// one is added or subtracted, the setter runs. An update read for its
// value belongs to the expression route, which does not claim it
// (setterAssignmentEffect below reads only the assigning form, whose
// value is the right side by the language's own rule).
func setterUpdateWriteOf(context *LoweringContext, e *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	var operator ast.Kind
	var operand *ast.Node
	switch {
	case ast.IsPostfixUnaryExpression(e):
		unary := e.AsPostfixUnaryExpression()
		operator, operand = unary.Operator, unary.Operand
	case ast.IsPrefixUnaryExpression(e):
		unary := e.AsPrefixUnaryExpression()
		operator, operand = unary.Operator, unary.Operand
	default:
		return nil, false
	}
	if operator != ast.KindPlusPlusToken && operator != ast.KindMinusMinusToken {
		return nil, false
	}
	access := Unwrapped(operand)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	op := kernelbridge.LoopOpAdd
	if operator == ast.KindMinusMinusToken {
		op = kernelbridge.LoopOpSub
	}
	return setterReadModifyWrite(context, access, op, constNumber(1))
}

// setterReadModifyWrite is the one body the compound and the update
// share: the getter's read, the arithmetic against a lowered operand,
// and the setter's call statement.
//
// The GETTER IS ASKED FIRST, and the order matters twice. It matters
// for the run — the language reads before it writes — and it matters
// for the lowering, because GetterReadEffect appends its call statement
// to context.Hoisted, and a decline AFTER that append would leave a
// hoist behind for a statement that lowered no other way. The statement
// dispatch's own DropHoistedFrom truncates back to the statement's mark
// on every decline, so a half-read compound leaves nothing — the same
// contract every hoisting reader in the dispatch runs under.
func setterReadModifyWrite(
	context *LoweringContext,
	access *ast.Node,
	op kernelbridge.LoopEffectOp,
	operand kernelbridge.LoopEffect,
) ([]kernelbridge.IrStatement, bool) {
	getter, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	// both halves, or nothing: a compound through a get-only property
	// writes what the language does not define, and one through a
	// set-only property reads a property that has no getter
	if !resolved || getter == nil || setter == nil {
		return nil, false
	}
	held, readOk := GetterReadEffect(context, access)
	if !readOk {
		return nil, false
	}
	stepped := kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectBinary,
		Op:   op,
		A:    &held,
		B:    &operand,
	}
	return SetterWriteStatements(context, access, stepped)
}
