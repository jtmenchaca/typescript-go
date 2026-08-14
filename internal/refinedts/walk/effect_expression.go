// from control_flow/effect_expression.ts
//
// The EXPRESSION half of the effect grammar, shared by both
// lowerings — the loop solver (loop_effect.ts) and the flow IR
// (lowering_to_kernel_ir.ts): literals through parens/casts, tracked
// reads, negation and unary plus, the five arithmetic operators, the
// Math reads (five unary, min/max), and the ternary as a join (its
// condition must be write-free — both arms are admitted, sound).
// `ReadPlace` answers a spelled tracked (or known) name; `Opaque`
// says what an unmodeled shape becomes — the solver path answers
// unknown for write-free shapes, the IR path declines. (0-value,
// false) means the reading declines.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var binOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskToken: kernelbridge.LoopOpMul,
	ast.KindSlashToken:    kernelbridge.LoopOpDiv,
	ast.KindPercentToken:  kernelbridge.LoopOpRem,
}

var mathOps = map[string]kernelbridge.LoopEffectOp{
	"floor": kernelbridge.LoopOpFloor,
	"ceil":  kernelbridge.LoopOpCeil,
	"round": kernelbridge.LoopOpRound,
	"trunc": kernelbridge.LoopOpTrunc,
	"abs":   kernelbridge.LoopOpAbs,
}

// booleanBinaryTokens: every binary operator whose VALUE is exactly
// true or false. The effect claims the two-value set {0,1} — exact as
// a set, reading neither operand — so it is admissible only when
// evaluating the operands cannot move state (writeAndCallFree).
var booleanBinaryTokens = map[ast.Kind]struct{}{
	ast.KindLessThanToken:                {},
	ast.KindGreaterThanToken:             {},
	ast.KindLessThanEqualsToken:          {},
	ast.KindGreaterThanEqualsToken:       {},
	ast.KindEqualsEqualsToken:            {},
	ast.KindEqualsEqualsEqualsToken:      {},
	ast.KindExclamationEqualsToken:       {},
	ast.KindExclamationEqualsEqualsToken: {},
	ast.KindInstanceOfKeyword:            {},
	ast.KindInKeyword:                    {},
}

// logicalTokens: the short-circuit operators. `a && b` evaluates to a
// on a falsy a and to b otherwise, so the JOIN of both operands' sets
// admits every value the expression can take — a superset on the arm
// the short-circuit picked, never an exclusion. Same for || and ??.
var logicalTokens = map[ast.Kind]struct{}{
	ast.KindAmpersandAmpersandToken: {},
	ast.KindBarBarToken:             {},
	ast.KindQuestionQuestionToken:   {},
}

// booleanPairEffect is the constant two-value set a boolean-valued
// operator produces — true rides 1 and false rides 0, the same
// encoding the true/false keyword constants below use.
func booleanPairEffect() kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
	}
}

// pureBuiltinReaders: `<root>.<name>` callees whose spec behavior is a
// READ — they inspect their arguments and global state and move
// nothing. The bool says whether the value is a PREDICATE (exactly
// true or false — the two-value set); false means the value has no
// scalar spelling and rides unknown. Reflect's write half
// (defineMetadata, set, deleteProperty…) is deliberately absent.
var pureBuiltinReaders = map[string]map[string]bool{
	"Reflect": {
		"getMetadata": false, "getOwnMetadata": false,
		"hasMetadata": true, "hasOwnMetadata": true,
		"getMetadataKeys": false, "getOwnMetadataKeys": false,
	},
	"Object": {
		"keys": false, "values": false, "entries": false,
		"getPrototypeOf": false, "getOwnPropertyNames": false,
		"getOwnPropertyDescriptor": false, "getOwnPropertySymbols": false,
	},
	"Array":  {"isArray": true},
	"Number": {"isInteger": true, "isFinite": true, "isNaN": true, "isSafeInteger": true},
}

// pureBuiltinEffect reads a call to a curated pure builtin: the callee
// must be exactly `<root>.<name>` on the global root identifier, and
// every argument must move nothing (write- and call-free) — an
// argument that runs code would need a statement, which an effect is
// not. Predicates answer the exact two-value set; the rest answer
// unknown, which is what a metadata object or key list is worth in a
// scalar slot.
func pureBuiltinEffect(call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	names, knownRoot := pureBuiltinReaders[access.Expression.Text()]
	if !knownRoot {
		return kernelbridge.LoopEffect{}, false
	}
	isPredicate, knownName := names[access.Name().Text()]
	if !knownName {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			if !writeAndCallFree(argument) {
				return kernelbridge.LoopEffect{}, false
			}
		}
	}
	if isPredicate {
		return booleanPairEffect(), true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
}

// writeAndCallFree answers whether evaluating the subtree can move any
// state the lowering tracks: no write form (an assignment, ++/--,
// delete) and no code the lowering does not run (a call, a `new`, an
// await, a yield, a tagged template). A getter behind a plain property
// read still runs code this test does not see — the same standing gap
// the ternary's write-free condition and the opaque branch's test
// accept.
func writeAndCallFree(node *ast.Node) bool {
	if node == nil {
		return true
	}
	if ast.IsBinaryExpression(node) {
		operator := node.AsBinaryExpression().OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			return false
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		operator := node.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	switch node.Kind {
	case ast.KindDeleteExpression, ast.KindCallExpression, ast.KindNewExpression,
		ast.KindAwaitExpression, ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return false
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !writeAndCallFree(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// ContainsWrite is containsWrite in the TS source: does the subtree
// perform any write? A shape mapped to an opaque effect must be
// write-free, or the lowering's state would miss the write. Shared
// by both lowerings.
func ContainsWrite(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsWrite(child)
		}
		return false
	})
	return found
}

// EffectReader mirrors the destructured `reader` parameter of
// lowerEffectExpression in the TS source.
//
// ReadNode is the Go addition: a DEEP path read (`p.a.b`) that
// SpelledNameOf cannot spell, which nested-record flattening makes an
// ordinary slot read. Tried after ReadPlace declines and before Opaque;
// nil leaves the reading exactly as ReadPlace left it.
type EffectReader struct {
	ReadPlace func(spelled string) (kernelbridge.LoopEffect, bool)
	ReadNode  func(e *ast.Node) (kernelbridge.LoopEffect, bool)
	Opaque    func(e *ast.Node) (kernelbridge.LoopEffect, bool)
}

// stringSlotEffect is a tracked STRING-sorted read as an effect, or
// (zero, false). Sequence building admits only the string sort: a
// number-sorted operand of `+` is arithmetic, not concatenation, and
// an unknown-sorted one is a reread across sorts, which is never a
// claim.
func stringSlotEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	i, ok := IndexOf(context, e)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Sorts[i] != BindingKindString {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}, true
}

// concatOf pairs two operand effects into one concatenation effect.
func concatOf(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &a, B: &b}
}

// SequenceEffectOf reads an expression as a SEQUENCE effect — the
// string world's half of the effect grammar, beside the numeric
// LowerEffectExpression:
//
//   - a string literal is its exact tuple;
//   - a tracked string-sorted name is a read;
//   - `a + b` with BOTH sides sequence-readable is their
//     concatenation (a `+` with a number-sorted operand is arithmetic
//     and is not read here — it takes the numeric route as before);
//   - a template literal `a${x}b` is that concatenation spelled out,
//     its literal chunks as exact tuples and its substitutions as
//     sequence reads.
//
// A non-string or untracked part declines the whole reading, exactly
// as it did before this route existed. Parens and casts unwrap first.
//
// A CALL is read only where the syntax already committed the expression
// to being a sequence — inside a `+` chain or a template substitution.
// Handed a bare call as the WHOLE expression this declines, because
// SortOfArg (ir_guard.go) reads a success here as "this argument is
// string-sorted", and a numeric callee's result is not.
func SequenceEffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	return sequenceEffectOf(context, e, false /*inSequence*/)
}

// sequenceEffectOf is the reading, carrying whether the caller has already
// committed the expression to the sequence world.
func sequenceEffectOf(context *LoweringContext, e *ast.Node, inSequence bool) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsStringLiteral().Text),
		}, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsNoSubstitutionTemplateLiteral().Text),
		}, true
	}
	if slot, ok := stringSlotEffect(context, head); ok {
		return slot, true
	}
	// `a[i]` on a string-sorted flattened array is a sequence read — the
	// element slot, or-absent where nothing bounds i
	if slot, ok := ArrayElementSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return ArrayIndexReadEffect(context, head)
	}
	// `m.get(k)` on a string-sorted collection is the same or-absent
	// read of the values slot
	if slot, ok := MapValueSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return MapGetReadEffect(context, head)
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return kernelbridge.LoopEffect{}, false
		}
		// a `+` chain commits BOTH sides to the sequence world, so a call in
		// either operand is a call inside a sequence
		a, aOk := sequenceEffectOf(context, bin.Left, true /*inSequence*/)
		if !aOk {
			return kernelbridge.LoopEffect{}, false
		}
		b, bOk := sequenceEffectOf(context, bin.Right, true /*inSequence*/)
		if !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return concatOf(a, b), true
	}
	if ast.IsTemplateExpression(head) {
		return templateSequenceOf(context, head)
	}
	// a CALL inside the sequence — `"n=" + this.name()`, a template
	// substitution `${this.name()}` — hoists to a temp-slot call statement
	// ahead of this statement, and the concatenation reads the temp
	// (ir_call_hoist.go). Refused wherever no statement stream exists or
	// the reordering could be observed, and then the reading declines
	// exactly as it did before.
	//
	// The hoist is asked only for a call whose spelling is INSIDE a
	// sequence the syntax already committed to — a bare call handed to this
	// reader on its own is not a sequence, and answering one here would
	// tell SortOfArg (ir_guard.go) that a numeric callee's result is
	// string-sorted, since SortOfArg reads "SequenceEffectOf succeeded" as
	// exactly that claim. So the whole-expression case declines and the
	// numeric reader (EffectOf's Opaque) hoists it instead; the memo means
	// the two never build two statements for one site.
	if inSequence && (ast.IsCallExpression(head) || isAwaitedCallShape(head)) {
		return HoistCallEffect(context, head)
	}
	// a string-sorted GETTER read inside a sequence: the same hoisted
	// call, admitted only where the temp's sort came out string — the
	// same commitment gate the explicit call above wears, for the same
	// SortOfArg reason
	if inSequence && ast.IsPropertyAccessExpression(head) {
		if held, ok := GetterReadEffect(context, head); ok &&
			held.Kind == kernelbridge.LoopEffectVar &&
			held.Index < len(context.Sorts) &&
			context.Sorts[held.Index] == BindingKindString {
			return held, true
		}
	}
	return kernelbridge.LoopEffect{}, false
}

// isAwaitedCallShape is `await f(…)` — the one wrapper the hoist route
// peels, spelled here so the sequence reader can ask before handing the
// node over.
func isAwaitedCallShape(e *ast.Node) bool {
	operand, isAwait := AwaitedOperandOf(e)
	return isAwait && ast.IsCallExpression(operand)
}

// SpelledSequenceShape is whether an expression is a sequence by its
// own SYNTAX alone — a string literal, any template literal, or a `+`
// chain of those. It reads no names and consults no slot vector, so it
// answers the same in any context: the sort question a caller must
// settle before the callee's slots exist.
func SpelledSequenceShape(e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) || ast.IsNoSubstitutionTemplateLiteral(head) ||
		ast.IsTemplateExpression(head) {
		return true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return false
		}
		return SpelledSequenceShape(bin.Left) && SpelledSequenceShape(bin.Right)
	}
	return false
}

// templateSequenceOf is `a${x}b${y}c` as a right-nested chain of the
// concatenation effect: the head's literal text, then per span the
// substituted expression's sequence reading followed by that span's
// literal text. An empty literal chunk contributes the empty tuple,
// which concatenates to nothing — kept rather than special-cased, so
// the chain's shape is one rule.
func templateSequenceOf(context *LoweringContext, head *ast.Node) (kernelbridge.LoopEffect, bool) {
	template := head.AsTemplateExpression()
	if template.TemplateSpans == nil {
		return kernelbridge.LoopEffect{}, false
	}
	parts := []kernelbridge.LoopEffect{{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(template.Head.AsTemplateHead().Text),
	}}
	for _, spanNode := range template.TemplateSpans.Nodes {
		span := spanNode.AsTemplateSpan()
		// a substitution sits INSIDE a sequence the template already
		// committed to, so a call there hoists
		substituted, ok := sequenceEffectOf(context, span.Expression, true /*inSequence*/)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, substituted)
		var text string
		switch {
		case ast.IsTemplateMiddle(span.Literal):
			text = span.Literal.AsTemplateMiddle().Text
		case ast.IsTemplateTail(span.Literal):
			text = span.Literal.AsTemplateTail().Text
		default:
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(text),
		})
	}
	// fold right so the chain nests the way the kernel's Concatenation
	// form does
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		out = concatOf(parts[i], out)
	}
	return out, true
}

// LowerEffectExpression is lowerEffectExpression in the TS source.
func LowerEffectExpression(e *ast.Node, reader EffectReader) (kernelbridge.LoopEffect, bool) {
	if ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) {
		var inner *ast.Node
		switch {
		case ast.IsParenthesizedExpression(e):
			inner = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			inner = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			inner = e.AsNonNullExpression().Expression
		}
		return LowerEffectExpression(inner, reader)
	}
	if ast.IsNumericLiteral(e) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(jsnum.FromString(e.AsNumericLiteral().Text))})),
		}, true
	}
	if e.Kind == ast.KindTrueKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}, true
	}
	if e.Kind == ast.KindFalseKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))}, true
	}
	if spelled, ok := SpelledNameOf(e); ok {
		if held, ok := reader.ReadPlace(spelled); ok {
			return held, true
		}
		if reader.ReadNode != nil {
			if held, ok := reader.ReadNode(e); ok {
				return held, true
			}
		}
		return reader.Opaque(e)
	}
	// a DEEP path (`p.a.b`) has no one-step spelling; a flattened nested
	// record gives it a slot all the same
	if ast.IsPropertyAccessExpression(e) && reader.ReadNode != nil {
		if held, ok := reader.ReadNode(e); ok {
			return held, true
		}
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			if ast.IsNumericLiteral(unary.Operand) {
				return kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectConst,
					Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))})),
				}, true
			}
			a, ok := LowerEffectExpression(unary.Operand, reader)
			if !ok {
				return kernelbridge.LoopEffect{}, false
			}
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: kernelbridge.LoopOpNeg, A: &a}, true
		}
		if unary.Operator == ast.KindPlusToken {
			return LowerEffectExpression(unary.Operand, reader)
		}
		// `!x` always produces exactly true or false — the two-value set,
		// under the same moves-nothing gate the comparisons wear
		if unary.Operator == ast.KindExclamationToken && writeAndCallFree(unary.Operand) {
			return booleanPairEffect(), true
		}
		return reader.Opaque(e)
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		// a COMPARISON (and instanceof/in) always produces exactly true or
		// false: the two-value set is the exact answer, and no operand is
		// read — admitted only where evaluating the operands moves nothing.
		// A gate failure falls to Opaque, exactly what the operator did
		// before this arm existed.
		if _, isBoolean := booleanBinaryTokens[bin.OperatorToken.Kind]; isBoolean && writeAndCallFree(e) {
			return booleanPairEffect(), true
		}
		// a SHORT-CIRCUIT operator's value is one of its operands, so the
		// join of both admits every run. A side that does not lower falls
		// to Opaque — again the operator's old path.
		if _, isLogical := logicalTokens[bin.OperatorToken.Kind]; isLogical {
			a, aOk := LowerEffectExpression(bin.Left, reader)
			b, bOk := LowerEffectExpression(bin.Right, reader)
			if aOk && bOk {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
			}
			return reader.Opaque(e)
		}
		op, ok := binOps[bin.OperatorToken.Kind]
		if !ok {
			return reader.Opaque(e)
		}
		a, aOk := LowerEffectExpression(bin.Left, reader)
		b, bOk := LowerEffectExpression(bin.Right, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
	}
	if ast.IsConditionalExpression(e) {
		cond := e.AsConditionalExpression()
		if ContainsWrite(cond.Condition) {
			return kernelbridge.LoopEffect{}, false
		}
		a, aOk := LowerEffectExpression(cond.WhenTrue, reader)
		b, bOk := LowerEffectExpression(cond.WhenFalse, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
	}
	// an OBJECT LITERAL, ARRAY LITERAL, or TEMPLATE whose evaluation
	// moves nothing: the value has no scalar spelling — unknown IS what
	// a slot can hold of it — and building it changed no state, so the
	// unknown claim costs the read and nothing else. A literal whose
	// parts run code keeps the old path (its effects need a statement).
	if (ast.IsObjectLiteralExpression(e) || ast.IsArrayLiteralExpression(e) ||
		ast.IsTemplateExpression(e)) && writeAndCallFree(e) {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
	}
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		// a PURE BUILTIN read — `Reflect.getMetadata(...)`, `Object.keys(x)`,
		// `Array.isArray(x)`: the callee reads without moving anything, so
		// with write-and-call-free arguments the whole expression moves
		// nothing and its value spells as its contract promises — the
		// two-value set for the predicates, unknown for the rest. The list
		// is curated read-only spec behavior, never guessed.
		if effect, pure := pureBuiltinEffect(call); pure {
			return effect, true
		}
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Math" {
				name := access.Name().Text()
				if un, ok := mathOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
					a, ok := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !ok {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: un, A: &a}, true
				}
				if (name == "min" || name == "max") && call.Arguments != nil && len(call.Arguments.Nodes) == 2 {
					a, aOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					b, bOk := LowerEffectExpression(call.Arguments.Nodes[1], reader)
					if !aOk || !bOk {
						return kernelbridge.LoopEffect{}, false
					}
					op := kernelbridge.LoopOpMin
					if name == "max" {
						op = kernelbridge.LoopOpMax
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
				}
			}
		}
		return reader.Opaque(e)
	}
	return reader.Opaque(e)
}
