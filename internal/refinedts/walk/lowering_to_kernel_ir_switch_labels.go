// split from lowering_to_kernel_ir.go — switch label guards and literals

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// switchLabelGuard is one `case k:` as its equality branch, built
// through the shared guard composer so the discriminant's sort decides
// the reading exactly as an `if (x === k)` head would.
//
// A HOISTED DISCRIMINANT TESTS ITS SLOT DIRECTLY. The composer resolves
// the head's left operand by IndexOf, which reads a spelled NAME, and a
// hoisted temp is spelled `#hoist<n>:` — a spelling nothing resolves,
// which is what keeps it uncollidable with any source name. So where the
// discriminant hoisted, the equality is built from the temp's INDEX
// against the label's literal rather than synthesized as a node over the
// discriminant, taking the same two arms TestOf takes: IrTestEq with W
// under the number sort, IrTestEqSeq with the label's code points under
// the string sort. The temp keeping its # spelling out of the name table
// is the point — the slot travels as an index, never as a name.
func switchLabelGuard(
	context *LoweringContext,
	discriminant *ast.Node,
	hoistedSlot int,
	hoisted bool,
	label *ast.Node,
	thn []kernelbridge.IrStatement,
	els []kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	// the label as the literal TOKEN it names: itself where it is already
	// one, the const it is bound to, or the enum member's own value
	literal, literalOk := switchLabelLiteral(context, label)
	if !literalOk {
		return nil, false
	}
	if hoisted {
		return hoistedLabelGuard(context, hoistedSlot, literal, thn, els)
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	equality := factory.NewBinaryExpression(
		nil,
		discriminant,
		nil,
		factory.NewToken(ast.KindEqualsEqualsEqualsToken),
		literal,
	)
	return LowerGuard(context, equality, thn, els)
}

// hoistedLabelGuard is one `case k:` against a HOISTED discriminant's
// temp: the equality read straight off the slot index, since the temp's
// `#hoist<n>:` spelling resolves to no name and a synthesized node would
// find nothing.
//
// The two arms are TestOf's own equality arms under the slot's sort
// (ir_guard.go:150-162) and the statement is assembled the way lowerGuard
// assembles one — the number sort takes IrTestEq carrying the literal's
// value in W, the string sort takes IrTestEqSeq carrying the literal's
// code points. A slot wearing neither sort has no equality on the wire:
// it answers nothing here, and the caller's chain keeps the untested
// siblings.
func hoistedLabelGuard(
	context *LoweringContext,
	on int,
	literal *ast.Node,
	thn []kernelbridge.IrStatement,
	els []kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || on >= len(context.Sorts) {
		return nil, false
	}
	if context.Sorts[on] == BindingKindNumber {
		w, isNumber := NumberOf(literal)
		if !isNumber {
			return nil, false
		}
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranch,
			On:   on,
			Test: kernelbridge.IrTestEq,
			W:    &w,
			Then: thn,
			Else: els,
		}}, true
	}
	if context.Sorts[on] == BindingKindString && ast.IsStringLiteral(literal) {
		text := literal.AsStringLiteral().Text
		points := make([]float64, 0, len(text))
		for _, r := range text {
			points = append(points, float64(r))
		}
		return []kernelbridge.IrStatement{{
			Kind:   kernelbridge.IrStatementBranch,
			On:     on,
			Test:   kernelbridge.IrTestEqSeq,
			Points: points,
			Then:   thn,
			Else:   els,
		}}, true
	}
	return nil, false
}

// switchLabelLiteral is the literal token a case label names, which is
// what the equality test reads: TestOf takes the right operand
// syntactically, so a label that only NAMES a literal has to hand the
// token over rather than the name.
//
// Three routes, in the order they cost:
//
//   - the label is already a literal word or number;
//   - the label follows const-to-const links to one
//     (dataflowfacts.ConstChainLiteral — the same resolver the walk side's
//     SwitchLabelValuesWith reads its labels through, so the two agree
//     about which labels resolve);
//   - the label is an enum member, whose value the checker holds as the
//     member's literal type. tsgo spells a number literal's value as
//     jsnum.Number, a NAMED float64, so numberLiteralValue reads it; the
//     token handed back is freshly made from that value, since an enum
//     member has no literal token of its own to point at.
//
// (nil, false) for anything else — a computed label compares two values
// the equality tests do not speak.
func switchLabelLiteral(context *LoweringContext, label *ast.Node) (*ast.Node, bool) {
	head := Unwrapped(label)
	if _, isNumber := NumberOf(head); isNumber {
		return head, true
	}
	if ast.IsStringLiteral(head) {
		return head, true
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false
	}
	c := context.Flow.P.Checker
	if resolved, ok := dataflowfacts.ConstChainLiteral(c, head); ok {
		if _, isNumber := NumberOf(resolved); isNumber {
			return resolved, true
		}
		if ast.IsStringLiteral(resolved) {
			return resolved, true
		}
	}
	// an enum member read — `case MyEnum.A:`. The checker's own literal
	// type for the member IS the value, at the same grade the enum
	// reader elsewhere takes it at.
	if !ast.IsPropertyAccessExpression(head) {
		return nil, false
	}
	receiverSymbol := symbolAt(c, head.AsPropertyAccessExpression().Expression)
	if receiverSymbol == nil {
		return nil, false
	}
	isEnum := false
	for _, declaration := range receiverSymbol.Declarations {
		if ast.IsEnumDeclaration(declaration) {
			isEnum = true
			break
		}
	}
	if !isEnum {
		return nil, false
	}
	memberType := c.GetTypeAtLocation(head)
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	if memberType.IsNumberLiteral() {
		// a member the checker never pinned (a computed one) has no value
		// at all, and no token can be made for it
		value, ok := numberLiteralValue(memberType.AsLiteralType().Value())
		if !ok {
			return nil, false
		}
		// a NEGATIVE member spells as a minus over its magnitude, which is
		// the same shape NumberOf reads for a written `case -1:`
		if value < 0 {
			magnitude := factory.NewNumericLiteral(jsnum.Number(-value).String(), ast.TokenFlagsNone)
			return factory.NewPrefixUnaryExpression(ast.KindMinusToken, magnitude), true
		}
		return factory.NewNumericLiteral(jsnum.Number(value).String(), ast.TokenFlagsNone), true
	}
	if memberType.IsStringLiteral() {
		text, ok := memberType.AsLiteralType().Value().(string)
		if !ok {
			return nil, false
		}
		return factory.NewStringLiteral(text, ast.TokenFlagsNone), true
	}
	return nil, false
}
