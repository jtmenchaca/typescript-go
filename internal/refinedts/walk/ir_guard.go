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

// TestOfResult mirrors testOf's return shape. OnB/HasOnB carry the
// SECOND slot when the head compares two tracked number bindings
// (`i < n`); those results never carry W.
type TestOfResult struct {
	On      int
	Test    kernelbridge.IrBranchTest
	W       float64
	HasW    bool
	OnB     int
	HasOnB  bool
	Points  []float64
	Swapped bool
}

// TestOf is testOf in the TS source: the single-test head reader —
// strict equality under the literal's sort, definedness against
// `undefined`, ordered comparison under the number sort (against a
// constant, against a second tracked slot, or mirrored for a literal
// left), equality between two number-sorted slots (`===` and `==`
// alike, which agree there), bare truthiness under the binding's own
// sort. Swapped arms carry a negated head.
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
			if result, ok := eqSlotTestOf(context, bin, on, onOk); ok {
				return result, true
			}
			// `s === t` between two string slots: the join-only two-slot
			// sequence equality
			if result, ok := eqSeqSlotTestOf(context, bin, on, onOk); ok {
				return result, true
			}
			return TestOfResult{}, false
		}
		if kind == ast.KindEqualsEqualsToken {
			// LOOSE equality lowers ONLY as the two-slot number form.
			// Between two number-sorted slots `==` and `===` agree —
			// no coercion is reachable, both operands already being
			// numbers — so the same eqSlot test reads it. Every other
			// `==` head coerces and is not read here.
			if result, ok := eqSlotTestOf(context, bin, on, onOk); ok {
				return result, true
			}
			return TestOfResult{}, false
		}
		if hasCmp {
			// ordered comparison reads under the number sort only
			w, wOk := NumberOf(bin.Right)
			if onOk && context.Sorts[on] == BindingKindNumber && wOk {
				return TestOfResult{On: on, Test: irTestOfCmp(cmp), W: w, HasW: true, Swapped: false}, true
			}
			onRight, onRightOk := NumberIndexOf(context, bin.Right)
			// BOTH sides tracked numbers: `i < n` — the two-slot
			// comparison, which carries the other SLOT where the
			// one-slot form carries a constant
			if onOk && context.Sorts[on] == BindingKindNumber && onRightOk {
				return TestOfResult{
					On: on, Test: irTestOfCmp2(cmp),
					OnB: onRight, HasOnB: true, Swapped: false,
				}, true
			}
			// a literal LEFT mirrors: k < x reads as x > k
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

// eqSlotTestOf reads an equality head between two tracked NUMBER
// slots (`i === n`, `i == n`) as the two-slot equality test. Both
// sides must be number-sorted tracked slots: that is exactly where
// `==` and `===` agree, and it is the only shape the kernel's eqSlot
// narrowing speaks. Anything else — a literal side, a string sort, an
// untracked read — reads as nothing here.
func eqSlotTestOf(context *LoweringContext, bin *ast.BinaryExpression, on int, onOk bool) (TestOfResult, bool) {
	if !onOk || context.Sorts[on] != BindingKindNumber {
		return TestOfResult{}, false
	}
	onRight, onRightOk := NumberIndexOf(context, bin.Right)
	if !onRightOk {
		return TestOfResult{}, false
	}
	return TestOfResult{
		On: on, Test: kernelbridge.IrTestEqSlot,
		OnB: onRight, HasOnB: true, Swapped: false,
	}, true
}

// stringIndexOf is a tracked STRING-sorted read, the string twin of
// NumberIndexOf.
func stringIndexOf(context *LoweringContext, name *ast.Node) (int, bool) {
	i, ok := IndexOf(context, name)
	if !ok {
		return 0, false
	}
	if context.Sorts[i] == BindingKindString {
		return i, true
	}
	return 0, false
}

// eqSeqSlotTestOf reads a STRICT equality head between two tracked
// STRING slots (`s === t`) as the two-slot sequence-equality test.
//
// It narrows neither arm — the kernel walks both from the state as it
// stood and joins them. The two-slot tightening the number shapes get
// is built from enclosure bounds, and a word has none: readEnclosure
// answers nothing for a Concatenation, so there is no window to cross
// over. What this unlocks is bodies that decline TODAY because the
// guard has no lowering at all.
//
// Strict equality only. `==` between two strings agrees with `===`,
// but the loose form is not read anywhere else here either, and the
// one-shape rule keeps the recognition honest.
func eqSeqSlotTestOf(context *LoweringContext, bin *ast.BinaryExpression, on int, onOk bool) (TestOfResult, bool) {
	if !onOk || context.Sorts[on] != BindingKindString {
		return TestOfResult{}, false
	}
	onRight, onRightOk := stringIndexOf(context, bin.Right)
	if !onRightOk {
		return TestOfResult{}, false
	}
	return TestOfResult{
		On: on, Test: kernelbridge.IrTestEqSeqSlot,
		OnB: onRight, HasOnB: true, Swapped: false,
	}, true
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

// irTestOfCmp2 is irTestOfCmp's two-slot twin: the same four
// comparisons, named for the form whose right operand is a slot.
func irTestOfCmp2(cmp kernelbridge.NarrowCmpOp) kernelbridge.IrBranchTest {
	switch cmp {
	case kernelbridge.NarrowOpGe:
		return kernelbridge.IrTestGeSlot
	case kernelbridge.NarrowOpGt:
		return kernelbridge.IrTestGtSlot
	case kernelbridge.NarrowOpLe:
		return kernelbridge.IrTestLeSlot
	case kernelbridge.NarrowOpLt:
		return kernelbridge.IrTestLtSlot
	}
	panic("irTestOfCmp2: unreached op")
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
//
// THE VALUE CLAIM IS WHAT THIS PREDICATE IS ABOUT. Its one caller
// (the return route) writes {1} on the true path and {0} on the false
// one, so answering true asserts the expression's VALUE is the boolean
// its truth decides. That holds for a comparison, and it does NOT hold
// for a bare short-circuit: `return host && host.instance` evaluates to
// `host.instance`, not to `true`, and claiming {1} for it would be a
// wrong answer about the returned value.
//
// So a place's TRUTHINESS is admitted only where a `!` has already
// forced the result to a boolean — `!x`, and the `!!(a && b.patch)`
// nest writes — which is what `negated` carries down the walk. Under a
// negation the whole subtree's value is `true` or `false` whatever its
// leaves evaluate to, so a leaf that reads only as a truth is enough;
// with no negation in force each leaf must be a comparison, which is
// exactly what this predicate admitted before.
//
// `a ?? b` is the DEFINEDNESS branch, not a truthiness one: it takes
// the right side only when the left is null or undefined. It rides the
// same rule — under a negation the value is a boolean, so the left
// place needs only a slot to test definedness on.
func TestShaped(context *LoweringContext, e *ast.Node) bool {
	return testShaped(context, e, false /*negated*/)
}

// testShaped is the reading, carrying whether a `!` above it has
// already forced the value to a boolean.
func testShaped(context *LoweringContext, e *ast.Node, negated bool) bool {
	head := Unwrapped(e)
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			// `!e` is a boolean whatever e evaluates to, so everything under
			// it may read as a truth alone
			return testShaped(context, unary.Operand, true /*negated*/)
		}
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken {
			return testShaped(context, bin.Left, negated) && testShaped(context, bin.Right, negated)
		}
		if kind == ast.KindQuestionQuestionToken {
			// only under a negation: `a ?? b` bare evaluates to a or to b,
			// neither of which is the boolean the caller would write
			if !negated {
				return false
			}
			// the left side must be a tracked place for the definedness
			// branch to have something to test; the right side rides in the
			// else arm and is read as any other test-shaped expression
			if _, tracked := IndexOf(context, Unwrapped(bin.Left)); !tracked {
				return false
			}
			return testShaped(context, bin.Right, negated)
		}
		if kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindExclamationEqualsEqualsToken ||
			kind == ast.KindEqualsEqualsToken {
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
	return negated && truthyLeafShaped(context, head)
}

// truthyLeafShaped is whether a bare place reads as a truthiness leaf:
// TestOf answers a truthiness test for a tracked slot wearing the
// number or the string sort, and nothing else. The two questions are
// asked of the same slot through the same IndexOf, so TestShaped and
// TestOf cannot disagree about which leaves read.
func truthyLeafShaped(context *LoweringContext, head *ast.Node) bool {
	index, tracked := IndexOf(context, head)
	if !tracked {
		return false
	}
	sort := context.Sorts[index]
	return sort == BindingKindNumber || sort == BindingKindString
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
		if kind == ast.KindQuestionQuestionToken {
			// `a ?? b` in guard position: the value is `a` where `a` is
			// DEFINED and `b` where it is not, so the truth of the whole is
			// the truth of whichever side supplied the value. The definedness
			// branch on the left place picks the side, and each side's own
			// guard decides the arms underneath it — the left's truthiness in
			// the then arm, the right's whole reading in the else.
			//
			// This is definedness and not truthiness: `0 ?? b` is 0, and a
			// truthiness test on the left would wrongly hand it to `b`.
			on, tracked := IndexOf(context, Unwrapped(bin.Left))
			if !tracked {
				return nil, false
			}
			defined, definedOk := LowerGuard(context, bin.Left, thn, els)
			if !definedOk {
				return nil, false
			}
			absent, absentOk := LowerGuard(context, bin.Right, thn, els)
			if !absentOk {
				return nil, false
			}
			return []kernelbridge.IrStatement{{
				Kind: kernelbridge.IrStatementBranch,
				On:   on,
				Test: kernelbridge.IrTestDefined,
				Then: defined,
				Else: absent,
			}}, true
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
		if single.HasOnB {
			stmt.OnB = single.OnB
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
	// a template or a concatenation of string-sorted parts is a string
	// argument — the same reading RhsEffect gives a string-sorted slot
	if _, ok := SequenceEffectOf(context, e); ok {
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
	if _, ok := SequenceEffectOf(context, e); ok {
		return TypeofTagString
	}
	if _, ok := EffectOf(context, e); ok {
		return TypeofTagNumber
	}
	return TypeofTagNone
}
