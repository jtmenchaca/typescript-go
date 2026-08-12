// from control_flow/ir_guard.ts
//
// Guard lowering for the flow IR: single tests, typeof folds,
// Number.isNaN, and `!`/`&&`/`||` nesting against prepared arms.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// CmpOps is CMP_OPS in the TS source.
var CmpOps = map[ast.Kind]kernelbridge.NarrowCmpOp{
	ast.KindGreaterThanEqualsToken: kernelbridge.NarrowOpGe,
	ast.KindGreaterThanToken:       kernelbridge.NarrowOpGt,
	ast.KindLessThanEqualsToken:    kernelbridge.NarrowOpLe,
	ast.KindLessThanToken:          kernelbridge.NarrowOpLt,
}

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

// TestOfResult mirrors testOf's return shape.
type TestOfResult struct {
	On      int
	Test    kernelbridge.IrBranchTest
	W       float64
	HasW    bool
	Points  []float64
	Swapped bool
}

// TestOf is testOf in the TS source: the single-test head reader —
// strict equality under the literal's sort, definedness against
// `undefined`, ordered comparison under the number sort (mirrored
// for a literal left), bare truthiness under the binding's own sort.
// Swapped arms carry a negated head.
func TestOf(context *LoweringContext, head *ast.Node) (TestOfResult, bool) {
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		on, onOk := IndexOf(context, bin.Left)
		isUndefined := ast.IsIdentifier(bin.Right) && bin.Right.Text() == "undefined"
		cmp, hasCmp := CmpOps[kind]
		if onOk && isUndefined {
			// definedness reads under every sort
			if kind == ast.KindExclamationEqualsEqualsToken {
				return TestOfResult{On: on, Test: kernelbridge.IrTestDefined, Swapped: false}, true
			}
			if kind == ast.KindEqualsEqualsEqualsToken {
				return TestOfResult{On: on, Test: kernelbridge.IrTestDefined, Swapped: true}, true
			}
			return TestOfResult{}, false
		}
		if kind == ast.KindEqualsEqualsEqualsToken {
			// strict equality reads under the LITERAL's sort, and only
			// where the binding wears the same one
			if onOk && context.Sorts[on] == BindingKindNumber {
				if w, ok := NumberOf(bin.Right); ok {
					return TestOfResult{On: on, Test: kernelbridge.IrTestEq, W: w, HasW: true, Swapped: false}, true
				}
			}
			if onOk && context.Sorts[on] == BindingKindString && ast.IsStringLiteral(bin.Right) {
				text := bin.Right.AsStringLiteral().Text
				points := make([]float64, 0, len(text))
				for _, r := range text {
					points = append(points, float64(r))
				}
				return TestOfResult{On: on, Test: kernelbridge.IrTestEqSeq, Points: points, Swapped: false}, true
			}
			return TestOfResult{}, false
		}
		if hasCmp {
			// ordered comparison reads under the number sort only
			w, wOk := NumberOf(bin.Right)
			if onOk && context.Sorts[on] == BindingKindNumber && wOk {
				return TestOfResult{On: on, Test: irTestOfCmp(cmp), W: w, HasW: true, Swapped: false}, true
			}
			// a literal LEFT mirrors: k < x reads as x > k
			onRight, onRightOk := NumberIndexOf(context, bin.Right)
			kLeft, kLeftOk := NumberOf(bin.Left)
			if onRightOk && kLeftOk {
				return TestOfResult{On: onRight, Test: irTestOfCmp(mirrorCmp(cmp)), W: kLeft, HasW: true, Swapped: false}, true
			}
			return TestOfResult{}, false
		}
		return TestOfResult{}, false
	}
	// bare truthiness reads under the binding's own sort
	on, onOk := IndexOf(context, head)
	if onOk && context.Sorts[on] == BindingKindNumber {
		return TestOfResult{On: on, Test: kernelbridge.IrTestTruthyNum, Swapped: false}, true
	}
	if onOk && context.Sorts[on] == BindingKindString {
		return TestOfResult{On: on, Test: kernelbridge.IrTestTruthyStr, Swapped: false}, true
	}
	return TestOfResult{}, false
}

func irTestOfCmp(cmp kernelbridge.NarrowCmpOp) kernelbridge.IrBranchTest {
	switch cmp {
	case kernelbridge.NarrowOpGe:
		return kernelbridge.IrTestGe
	case kernelbridge.NarrowOpGt:
		return kernelbridge.IrTestGt
	case kernelbridge.NarrowOpLe:
		return kernelbridge.IrTestLe
	case kernelbridge.NarrowOpLt:
		return kernelbridge.IrTestLt
	}
	panic("irTestOfCmp: unreached op")
}

func mirrorCmp(cmp kernelbridge.NarrowCmpOp) kernelbridge.NarrowCmpOp {
	switch cmp {
	case kernelbridge.NarrowOpLt:
		return kernelbridge.NarrowOpGt
	case kernelbridge.NarrowOpLe:
		return kernelbridge.NarrowOpGe
	case kernelbridge.NarrowOpGt:
		return kernelbridge.NarrowOpLt
	case kernelbridge.NarrowOpGe:
		return kernelbridge.NarrowOpLe
	}
	panic("mirrorCmp: unreached op")
}

// IsNanShape is isNanShape in the TS source: whether a call is
// spelled `Number.isNaN(one argument)` — the syntactic recognition,
// the same trust the Math reads carry. The GLOBAL isNaN coerces
// through ToNumber first and is NOT this test.
func IsNanShape(head *ast.Node) bool {
	if !ast.IsCallExpression(head) {
		return false
	}
	call := head.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	access := call.Expression.AsPropertyAccessExpression()
	return ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Number" &&
		ast.IsIdentifier(access.Name()) && access.Name().Text() == "isNaN" &&
		call.Arguments != nil && len(call.Arguments.Nodes) == 1
}

// TestShaped is testShaped in the TS source: whether an expression
// is BOOLEAN-SHAPED — a test or a connective composition of tests,
// whose value the guard desugaring may spell as {1}/{0}. A call is
// admitted only where composition can resolve it (guard position
// decides truthiness alone, so a callee's exact value never leaks).
func TestShaped(context *LoweringContext, e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			return TestShaped(context, unary.Operand)
		}
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken {
			return TestShaped(context, bin.Left) && TestShaped(context, bin.Right)
		}
		if kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindExclamationEqualsEqualsToken {
			return true
		}
		_, hasCmp := CmpOps[kind]
		return hasCmp
	}
	if ast.IsTypeOfExpression(head) {
		return false
	}
	if ast.IsCallExpression(head) {
		return IsNanShape(head) || context.ResolveCallee != nil
	}
	return false
}

// LowerGuard is lowerGuard in the TS source: a condition lowered
// against prepared arms: `!` swaps, `&&`/`||` nest (the shared arm
// rides in both branches — plain data, and the runtime evaluation
// order is preserved: the right side runs only where the left
// decided nothing). A typeof head folds under the slot's evidence; a
// call head inlines its callee and branches on the result slot's
// truthiness. Nil where no leaf reads.
func LowerGuard(context *LoweringContext, condition *ast.Node, thn, els []kernelbridge.IrStatement) ([]kernelbridge.IrStatement, bool) {
	head := Unwrapped(condition)
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			return LowerGuard(context, unary.Operand, els, thn)
		}
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken {
			inner, ok := LowerGuard(context, bin.Right, thn, els)
			if !ok {
				return nil, false
			}
			return LowerGuard(context, bin.Left, inner, els)
		}
		if kind == ast.KindBarBarToken {
			inner, ok := LowerGuard(context, bin.Right, thn, els)
			if !ok {
				return nil, false
			}
			return LowerGuard(context, bin.Left, thn, inner)
		}
	}
	if viaTypeof, ok := TypeofRead(context, head); ok {
		if viaTypeof.IsConstant {
			if viaTypeof.Value {
				return thn, true
			}
			return els, true
		}
		then, elseArm := thn, els
		if !viaTypeof.Positive {
			then, elseArm = els, thn
		}
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranch,
			On:   viaTypeof.On,
			Test: kernelbridge.IrTestDefined,
			Then: then,
			Else: elseArm,
		}}, true
	}
	if IsNanShape(head) {
		on, ok := IndexOf(context, head.AsCallExpression().Arguments.Nodes[0])
		if !ok {
			return nil, false
		}
		return []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementBranch, On: on, Test: kernelbridge.IrTestIsNan, Then: thn, Else: els}}, true
	}
	if single, ok := TestOf(context, head); ok {
		then, elseArm := thn, els
		if single.Swapped {
			then, elseArm = els, thn
		}
		stmt := kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   single.On,
			Test: single.Test,
			Then: then,
			Else: elseArm,
		}
		if single.HasW {
			w := single.W
			stmt.W = &w
		}
		if single.Points != nil {
			stmt.Points = single.Points
		}
		return []kernelbridge.IrStatement{stmt}, true
	}
	if ast.IsCallExpression(head) {
		inlined, ok := InlineCall(context, head)
		if !ok {
			return nil, false
		}
		test := kernelbridge.IrTestTruthyNum
		if inlined.RetSort == BindingKindString {
			test = kernelbridge.IrTestTruthyStr
		}
		out := append([]kernelbridge.IrStatement{}, inlined.Stmts...)
		out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementBranch, On: inlined.RetIndex, Test: test, Then: thn, Else: els})
		return out, true
	}
	return nil, false
}

// SortOfArg is sortOfArg in the TS source: the static sort of an
// argument expression, for an inlined parameter slot.
func SortOfArg(context *LoweringContext, e *ast.Node) BindingKind {
	if i, ok := IndexOf(context, e); ok {
		return context.Sorts[i]
	}
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return BindingKindString
	}
	if _, ok := EffectOf(context, e); ok {
		return BindingKindNumber
	}
	return BindingKindUnknown
}

// TypeofOfArg is typeofOfArg in the TS source: the typeof evidence
// an argument expression carries into an inlined parameter slot.
func TypeofOfArg(context *LoweringContext, e *ast.Node) TypeofTag {
	if i, ok := IndexOf(context, e); ok {
		if context.Typeofs != nil && i < len(context.Typeofs) {
			return context.Typeofs[i]
		}
		return TypeofTagNone
	}
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return TypeofTagString
	}
	if head.Kind == ast.KindTrueKeyword || head.Kind == ast.KindFalseKeyword {
		return TypeofTagBoolean
	}
	if _, ok := EffectOf(context, e); ok {
		return TypeofTagNumber
	}
	return TypeofTagNone
}
