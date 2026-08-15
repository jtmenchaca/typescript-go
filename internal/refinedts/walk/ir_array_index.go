// split from ir_array_slots.go — `a[i]` written and read, and the
// guard scope that says an index is below the length

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// compoundIndexOp is the arithmetic a compound assignment operator
// stands for — `+=` is add, `-=` is sub, and so on. Answers ok=false for
// every operator whose reading the effect grammar does not carry
// (`**=`, and the logical `&&=` / `||=` / `??=`, whose write is
// CONDITIONAL and so says something the unconditional effect grammar
// cannot).
func compoundIndexOp(kind ast.Kind) (kernelbridge.LoopEffectOp, bool) {
	switch kind {
	case ast.KindPlusEqualsToken:
		return kernelbridge.LoopOpAdd, true
	case ast.KindMinusEqualsToken:
		return kernelbridge.LoopOpSub, true
	case ast.KindAsteriskEqualsToken:
		return kernelbridge.LoopOpMul, true
	case ast.KindSlashEqualsToken:
		return kernelbridge.LoopOpDiv, true
	case ast.KindPercentEqualsToken:
		return kernelbridge.LoopOpRem, true
	// the bitwise and shift compounds: transferBitwise decides them
	case ast.KindAmpersandEqualsToken:
		return kernelbridge.LoopOpBitAnd, true
	case ast.KindBarEqualsToken:
		return kernelbridge.LoopOpBitOr, true
	case ast.KindCaretEqualsToken:
		return kernelbridge.LoopOpBitXor, true
	case ast.KindLessThanLessThanEqualsToken:
		return kernelbridge.LoopOpShl, true
	case ast.KindGreaterThanGreaterThanEqualsToken:
		return kernelbridge.LoopOpSar, true
	case ast.KindGreaterThanGreaterThanGreaterThanEqualsToken:
		return kernelbridge.LoopOpShr, true
	}
	return "", false
}

// ArrayIndexWriteOf is `a[i] = v` as a statement, and the COMPOUND forms
// `a[i] += v` / `-=` / `*=` / `/=` / `%=` with it: the elem slot JOINS
// the written value and the len slot is untouched. Weak on purpose —
// the two slots hold no per-position knowledge, so a write can only
// widen what a read may answer. A write PAST the end would also grow
// the length, which this does not claim; the length staying put is the
// honest reading, since the len slot is only ever read as a bound and a
// smaller bound admits fewer index reads, never more values.
//
// A compound write reads the position first, and the elem slot's var is
// what that read answers — the join of everything the array holds — so
// `a[i] += 1` writes join(elem, elem + 1). Same weak update, one
// arithmetic step in front of it.
func ArrayIndexWriteOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	compoundOp, isCompound := compoundIndexOp(bin.OperatorToken.Kind)
	if bin.OperatorToken.Kind != ast.KindEqualsToken && !isCompound {
		return nil, false
	}
	left := Unwrapped(bin.Left)
	if !ast.IsElementAccessExpression(left) {
		return nil, false
	}
	receiver := left.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	if _, isIndex := indexAccessOf(left, receiver.Text()); !isIndex {
		return nil, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	written, writtenOk := RhsEffect(context, context.Sorts[elemSlot], bin.Right)
	if !writtenOk {
		return nil, false
	}
	if isCompound {
		// the arithmetic forms speak the number sort only; a sequence slot
		// has no reading for `+=` here
		if context.Sorts[elemSlot] != BindingKindNumber {
			return nil, false
		}
		read := varEffect(elemSlot)
		written = kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectBinary, Op: compoundOp, A: &read, B: &written,
		}
	}
	return []AssignmentTarget{
		{Target: elemSlot, Effect: joinEffect(varEffect(elemSlot), written)},
	}, true
}

// ArrayIndexReadEffect is `a[i]` as an effect: the elem slot's var
// where a dominating `i < a.length` bounds the index, and the
// or-absent wrapping of that var where nothing does — an unguarded
// read may fall past the end, and the absent outcome is what falling
// past the end produces.
//
// "Dominating" is the guard scope the lowering carries: the if
// statement's lowering holds `i < a.length` while it lowers the THEN
// arm and drops it straight after, so a read outside the arm never
// sees the bound.
func ArrayIndexReadEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(node)
	if !ast.IsElementAccessExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver := head.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return kernelbridge.LoopEffect{}, false
	}
	index, isIndex := indexAccessOf(head, receiver.Text())
	if !isIndex {
		return kernelbridge.LoopEffect{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	elem := varEffect(elemSlot)
	if indexName, spelled := SpelledNameOf(Unwrapped(index)); spelled &&
		IndexIsBounded(context, indexName, receiver.Text()) {
		return elem, true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elem}, true
}

// boundIndexKey spells one "this index is below that array's length"
// fact, which is what the guard scope holds.
func boundIndexKey(indexName string, arrayName string) string {
	return indexName + "<" + arrayName
}

// IndexIsBounded is whether the lowering currently stands inside a
// guard that proved `index < array.length`.
func IndexIsBounded(context *LoweringContext, indexName string, arrayName string) bool {
	if context.BoundedIndices == nil {
		return false
	}
	_, held := context.BoundedIndices[boundIndexKey(indexName, arrayName)]
	return held
}

// BoundIndexOfTest reads a guard head as an index bound: `i < a.length`
// (and the mirrored `a.length > i`) against a flattened array. Answers
// the pair the THEN arm may read unguarded.
func BoundIndexOfTest(context *LoweringContext, head *ast.Node) (indexName string, arrayName string, ok bool) {
	e := Unwrapped(head)
	if !ast.IsBinaryExpression(e) {
		return "", "", false
	}
	bin := e.AsBinaryExpression()
	left, right := Unwrapped(bin.Left), Unwrapped(bin.Right)
	switch bin.OperatorToken.Kind {
	case ast.KindLessThanToken:
		// i < a.length
	case ast.KindGreaterThanToken:
		// a.length > i — the same fact, mirrored
		left, right = right, left
	default:
		return "", "", false
	}
	index, spelled := SpelledNameOf(left)
	if !spelled {
		return "", "", false
	}
	if !ast.IsPropertyAccessExpression(right) {
		return "", "", false
	}
	access := right.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Expression) ||
		!ast.IsIdentifier(access.Name()) || access.Name().Text() != "length" {
		return "", "", false
	}
	array := access.Expression.Text()
	if _, _, isArray := arraySlotsOf(context, array); !isArray {
		return "", "", false
	}
	return index, array, true
}

// HoldBoundIndex adds one proved index/array bound to the lowering
// context and answers the undo. The caller lowers the guarded arm and
// then calls the undo, so the bound never escapes the arm it was proved
// in.
//
// The context itself is mutated rather than copied: the slot vectors it
// carries GROW during lowering (an inlined callee allocates fresh
// slots), and a copy taken before that growth would index past its own
// Sorts. One context, one growing vector, and only the bound set moves.
func HoldBoundIndex(context *LoweringContext, indexName string, arrayName string) (undo func()) {
	previous := context.BoundedIndices
	held := map[string]struct{}{}
	for key := range previous {
		held[key] = struct{}{}
	}
	held[boundIndexKey(indexName, arrayName)] = struct{}{}
	context.BoundedIndices = held
	return func() { context.BoundedIndices = previous }
}
