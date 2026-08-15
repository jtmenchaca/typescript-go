// split from ir_guard.go — the single-test head reader and its comparison tables
// (named _head to keep the file out of Go's _test.go set)

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
