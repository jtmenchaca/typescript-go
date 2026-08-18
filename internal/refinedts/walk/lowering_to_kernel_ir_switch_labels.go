// split from lowering_to_kernel_ir.go — switch label guards and literals

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
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
	literal, isBoolean, literalOk := switchLabelLiteral(context, label)
	if !literalOk {
		return nil, false
	}
	if isBoolean {
		// A BOOLEAN LABEL TESTS A BOOLEAN-TYPED SLOT ONLY. Booleans ride the
		// number sort (true/false are the words 1/0, this package's own
		// ToNumber encoding — literal_values.go, effect_expression.go), the
		// same sort a plain `case 1:` tests under. Gating on the sort alone
		// would let `case true:` match a number-sorted slot that happens to
		// hold the literal 1, which is not the claim the source makes — the
		// flow side keeps the same separation with its own "b:"/"n:" key
		// prefixes (SwitchKeyOfLabel). So a boolean label additionally
		// requires the discriminant's TYPEOF evidence to be "boolean",
		// exactly the extra fact the number-sorted number labels don't need
		// and booleans do.
		if !discriminantTypeofBoolean(context, discriminant, hoistedSlot, hoisted) {
			return nil, false
		}
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

// discriminantTypeofBoolean is whether the switch's discriminant slot
// carries TypeofTagBoolean — the extra gate a boolean case label needs
// beyond the number sort a plain numeric label already checks.
func discriminantTypeofBoolean(context *LoweringContext, discriminant *ast.Node, hoistedSlot int, hoisted bool) bool {
	on := hoistedSlot
	if !hoisted {
		var onOk bool
		on, onOk = IndexOf(context, discriminant)
		if !onOk {
			return false
		}
	}
	if context == nil || on < 0 || on >= len(context.Typeofs) {
		return false
	}
	return context.Typeofs[on] == TypeofTagBoolean
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
// (nil, false, false) for anything else — a computed label compares two
// values the equality tests do not speak.
//
// TRIED AND ABANDONED: a fourth route reading the checker's TYPE at a
// const-bound identifier's INITIALIZER, to ground a computed constant
// like `const FLAG_A = 1 << 0` past ConstChainLiteral's syntactic reach.
// Checked against the live checker (TypeAtLocation on the initializer
// node, and again with an `as const` assertion added): TypeScript never
// narrows a bitwise-shift result to a number-literal type either way —
// `1 << 0`'s type is plain `number` at the expression itself, so no
// checker-side reading exists to fall back to for this shape. Confirmed
// separately: the recharts corpus's own switch statements (state/
// selectors/axisSelectors.ts, polarScaleSelectors.ts, shape/Symbols.tsx,
// animation/easing.ts, component/Text.tsx, cartesian/CartesianAxis.tsx,
// polar/PolarRadiusAxis.tsx, util/ActiveShapeUtils.tsx, util/scale/
// RechartsScale.ts) spell every case label as an ordinary quoted string
// or number literal — a full-corpus search for `case <identifier>:` and
// `` case `…` `` (a template-literal label) found none — so the census's
// "switch on a case label that is not a literal" rows do not correspond
// to a reproducible non-literal-label fixture in this corpus at all,
// matching lowering_to_kernel_ir_switch_pins_test.go's own earlier
// finding that every specimen it tried already lowers via branch-both
// (porous, not declined) rather than hitting this decline.
//
// The middle result is whether the token stands for a BOOLEAN literal —
// `true`/`false` spelled as the number words 1/0 (this package's own
// ToNumber encoding) — which the caller uses to gate the discriminant's
// typeof beyond the plain number sort a numeric label already checks.
func switchLabelLiteral(context *LoweringContext, label *ast.Node) (*ast.Node, bool, bool) {
	head := Unwrapped(label)
	if _, isNumber := NumberOf(head); isNumber {
		return head, false, true
	}
	if ast.IsStringLiteral(head) {
		return head, false, true
	}
	// a boolean literal label — `case true:` / `case false:` — is the
	// exact word 1 or 0 under the number sort, the same encoding
	// EvaluateLiteral and SwitchLabelValuesWith already give it. A fresh
	// numeric-literal token is synthesized so both the hoisted equality
	// (NumberOf in hoistedLabelGuard) and the built `===` head (NumberOf
	// via TestOf) read it exactly as they read a written `case 1:`.
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	if head.Kind == ast.KindTrueKeyword {
		return factory.NewNumericLiteral("1", ast.TokenFlagsNone), true, true
	}
	if head.Kind == ast.KindFalseKeyword {
		return factory.NewNumericLiteral("0", ast.TokenFlagsNone), true, true
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false, false
	}
	c := context.Flow.P.Checker
	if resolved, ok := dataflowfacts.ConstChainLiteral(c, head); ok {
		if _, isNumber := NumberOf(resolved); isNumber {
			return resolved, false, true
		}
		if ast.IsStringLiteral(resolved) {
			return resolved, false, true
		}
	}
	// an enum member read — `case MyEnum.A:`. The checker's own literal
	// type for the member IS the value, at the same grade the enum
	// reader elsewhere takes it at.
	if !ast.IsPropertyAccessExpression(head) {
		return nil, false, false
	}
	receiverSymbol := symbolAt(c, head.AsPropertyAccessExpression().Expression)
	if receiverSymbol == nil {
		return nil, false, false
	}
	isEnum := false
	for _, declaration := range receiverSymbol.Declarations {
		if ast.IsEnumDeclaration(declaration) {
			isEnum = true
			break
		}
	}
	if !isEnum {
		return nil, false, false
	}
	memberType := typereading.TypeAtLocation(c, head)
	if memberType.IsNumberLiteral() {
		// a member the checker never pinned (a computed one) has no value
		// at all, and no token can be made for it
		value, ok := numberLiteralValue(memberType.AsLiteralType().Value())
		if !ok {
			return nil, false, false
		}
		// a NEGATIVE member spells as a minus over its magnitude, which is
		// the same shape NumberOf reads for a written `case -1:`
		if value < 0 {
			magnitude := factory.NewNumericLiteral(jsnum.Number(-value).String(), ast.TokenFlagsNone)
			return factory.NewPrefixUnaryExpression(ast.KindMinusToken, magnitude), false, true
		}
		return factory.NewNumericLiteral(jsnum.Number(value).String(), ast.TokenFlagsNone), false, true
	}
	if memberType.IsStringLiteral() {
		text, ok := memberType.AsLiteralType().Value().(string)
		if !ok {
			return nil, false, false
		}
		return factory.NewStringLiteral(text, ast.TokenFlagsNone), false, true
	}
	return nil, false, false
}
