// from control_flow/ir_assignment.ts
//
// Assignments and RHS effects for the flow IR: a declaration or
// expression writes one tracked slot, and the right side lowers
// through the shared effect grammar.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// AssignmentTarget is the (target, effect) pair returned by the
// assignment readers below — the TS source's inline `{ target:
// number; effect: LoopEffect } | null` shape.
type AssignmentTarget struct {
	Target int
	Effect kernelbridge.LoopEffect
}

// EffectOf is effectOf in the TS source: an expression as a body
// effect, or (zero, false) where the reading ends.
//
// The read resolves through slotIndexOfName, which honours the CLOSED
// name map an inlined body carries — a free name inside an inlined
// callee must decline, never bind to the caller's slot of the same
// spelling. (The TS source reads context.bindings directly here; a
// name the callee did not declare could resolve to the caller's
// binding of that spelling, which is the capture the `names` map
// exists to forbid. Resolving through the one path IndexOf uses closes
// that.)
func EffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	numberSlot := func(i int) (kernelbridge.LoopEffect, bool) {
		// arithmetic admits only the number sort
		if context.Sorts[i] == BindingKindNumber {
			return varEffect(i), true
		}
		return kernelbridge.LoopEffect{}, false
	}
	return LowerEffectExpression(e, EffectReader{
		ReadPlace: func(spelled string) (kernelbridge.LoopEffect, bool) {
			i, found := slotIndexOfName(context, spelled)
			if !found {
				return kernelbridge.LoopEffect{}, false
			}
			return numberSlot(i)
		},
		// slot membership with no sort gate: the closure census asks
		// which names have state a later closure run could falsify, and
		// every held slot does, whatever sort it was laid out under
		HoldsPlace: func(spelled string) bool {
			_, found := slotIndexOfName(context, spelled)
			return found
		},
		// a DEEP record path (`p.a.b`), an array's `a.length`, and an
		// index read `a[i]` are all ordinary slot reads once the
		// flattenings gave them slots
		ReadNode: func(node *ast.Node) (kernelbridge.LoopEffect, bool) {
			if i, ok := PathSlotIndexOf(context, node); ok {
				return numberSlot(i)
			}
			if i, ok := ArrayLengthSlotOf(context, node); ok {
				return numberSlot(i)
			}
			// a deep access whose last step is a GETTER
			// (`this.holder.value`) never reaches Opaque —
			// LowerEffectExpression's deep-path arm answers ReadNode
			// alone — so the getter route is tried here too
			if held, ok := GetterReadEffect(context, node); ok {
				return held, true
			}
			// `Scope.TRANSIENT` — an ENUM MEMBER: immutable by the
			// language, its literal initializer is its value
			if held, ok := EnumMemberConstEffect(context, node); ok {
				return held, true
			}
			return kernelbridge.LoopEffect{}, false
		},
		Opaque: func(node *ast.Node) (kernelbridge.LoopEffect, bool) {
			// `a[i]`: the element slot, or-absent where nothing bounds i.
			// Arithmetic admits only a number-sorted element slot, the
			// same gate every other read here wears.
			if slot, ok := ArrayElementSlotOf(context, node); ok && context.Sorts[slot] == BindingKindNumber {
				return ArrayIndexReadEffect(context, node)
			}
			// `m.get(k)`: the collection's values slot, always or-absent
			// (no per-key knowledge can rule the miss out), under the
			// same number-sort gate
			if slot, ok := MapValueSlotOf(context, node); ok && context.Sorts[slot] == BindingKindNumber {
				return MapGetReadEffect(context, node)
			}
			// `this.value` where value is a GETTER: the read runs a body,
			// so it hoists as a zero-argument call exactly as an explicit
			// call does. Ahead of HoistCallEffect, which reads only a
			// CallExpression and has no reading for a property access.
			if held, ok := GetterReadEffect(context, node); ok {
				return held, true
			}
			// an IMPORTED (or same-file free) CONST whose initializer is a
			// literal: the declaration is one stable node in the program's
			// shared AST forest, so its initializer reads directly —
			// `contextId = STATIC_CONTEXT` takes the const's own value.
			if held, ok := FreeConstEffect(context, node); ok {
				return held, true
			}
			// `f(o.x = e)` — a SETTER assignment in expression position:
			// the setter's call hoists ahead of the statement and the
			// expression's value is the right side, the language's own
			// rule for what an assignment evaluates to
			if held, ok := SetterAssignmentEffect(context, node); ok {
				return held, true
			}
			// `s.indexOf(needle)` — a NUMBER off a word receiver. The
			// kernel answers the window the receiver's set supports:
			// {-1} u [0, 2*hi) for a receiver with scalar-count ceiling
			// hi, and {-1} u [0, +inf) with integrality where the
			// receiver states no ceiling. Ahead of the hoist, which
			// would spend a temp slot and answer unknown for a value the
			// kernel can bound.
			if held, ok := stringIndexOfEffect(context, node); ok {
				return held, true
			}
			// a CALL inside the expression — `count + this.bump()`, an
			// argument, a ternary arm: it HOISTS to a temp-slot call
			// statement emitted before this statement, and the expression
			// reads the temp. Gated on a statement stream existing and on
			// the reordering being observable by nothing
			// (ir_call_hoist.go); a refusal reads exactly as it did before
			// the route existed.
			return HoistCallEffect(context, node)
		},
	})
}

// stringIndexOfEffect reads `s.indexOf(needle)` over a WORD receiver
// and answers the kernel's numeric-from-sequence row.
//
// What the kernel claims. Every answer is -1, or an index INTO the
// receiver's UTF-16 code-unit sequence — at or above zero and strictly
// below the code-unit length (sec-string.prototype.indexof). For a
// receiver whose set states a scalar-count ceiling hi, TERMS-v2 §12
// bounds that length at 2*hi (each astral scalar counting twice), so
// the window is {-1} u [0, 2*hi); with no ceiling stated it is {-1} u
// [0, +inf), and the integrality rides either way.
//
// The NEEDLE is not sent. The window holds for every needle, so a
// needle operand would be a field no claim reads — but the needle still
// has to be a shape that RUNS as an ordinary search, which is what the
// one-argument gate is for: a second `position` argument shifts where
// the search starts, which changes no answer's bounds but is not a
// shape this reader has read, so it declines rather than guess.
//
// The receiver must read as a pure SEQUENCE. An ARRAY's `indexOf` is a
// different operation over a different receiver world — its answer is
// bounded by the element COUNT, not by a code-unit length — and it
// declines here, since an array local's name has no sequence reading.
func stringIndexOfEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	call := head.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return kernelbridge.LoopEffect{}, false
	}
	if !ast.IsIdentifier(access.Name()) || access.Name().Text() != "indexOf" {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return kernelbridge.LoopEffect{}, false
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := SequenceEffectOf(context, access.Expression)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectSeqNum,
		Op:   kernelbridge.LoopOpIndexOf,
		A:    &receiver,
	}, true
}

// FreeConstEffect reads a free identifier that resolves — through
// import aliases — to a CONST declaration, and answers the effect of
// the const's own initializer where that initializer carries no
// binding of its own: a numeric, string, or boolean literal, null or
// undefined, or a write-and-call-free object/array/template literal
// (whose value rides unknown). A `let`/`var` declaration answers
// nothing — module state can move — and so does any initializer that
// reads names or runs code: those belong to the exporting file's own
// bindings, which this context does not hold.
func FreeConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsIdentifier(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, node)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return kernelbridge.LoopEffect{}, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return kernelbridge.LoopEffect{}, false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || list.Flags&ast.NodeFlagsConst == 0 {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
	if ast.IsNumericLiteral(head) || head.Kind == ast.KindTrueKeyword ||
		head.Kind == ast.KindFalseKeyword || IsAbsentKeyword(head) {
		return LowerEffectExpression(head, EffectReader{
			ReadPlace: func(string) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
			Opaque: func(e *ast.Node) (kernelbridge.LoopEffect, bool) {
				if IsAbsentKeyword(e) {
					return kernelbridge.AbsentConst(), true
				}
				return kernelbridge.LoopEffect{}, false
			},
		})
	}
	// inertValue, not writeAndCallFree: a const in the EXPORTING file may
	// hold a literal carrying arrows (`export const HOOKS = { on: () => …
	// }`), and building those arrows runs nothing. Their later writes land
	// on the exporting file's own bindings, which this context holds no
	// slot for, so there is no belief here for the closure to falsify.
	if (ast.IsObjectLiteralExpression(head) || ast.IsArrayLiteralExpression(head) ||
		ast.IsStringLiteral(head) || ast.IsTemplateExpression(head)) && inertValue(head) {
		// the value has no scalar spelling in a number-sorted read (a
		// string const's tuple belongs to the sequence route) — unknown
		// is what this reader can hold of it, and it is exact about that
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
	}
	return kernelbridge.LoopEffect{}, false
}

// freeStringConstEffect reads a free identifier that resolves — through
// import aliases — to a CONST whose initializer is a STRING literal, and
// answers that exact tuple. The sequence world's half of
// FreeConstEffect: that one reads a const numerically and hands a string
// const's tuple to this route, which is where a sequence read belongs.
//
// A `let`/`var` answers nothing (module state can move), and so does any
// other initializer shape — a template with substitutions reads the
// exporting file's own bindings, which this context does not hold.
func freeStringConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsIdentifier(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, node)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return kernelbridge.LoopEffect{}, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return kernelbridge.LoopEffect{}, false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || list.Flags&ast.NodeFlagsConst == 0 {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
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
	return kernelbridge.LoopEffect{}, false
}

// EnumMemberConstEffect reads `Scope.TRANSIENT` — a property access
// whose NAME resolves to an ENUM MEMBER. An enum member is immutable
// by the language, so its explicit literal initializer IS its value: a
// numeric literal answers the exact constant. A string-membered or
// auto-numbered member answers nothing here — the numeric reader has
// no spelling for a word, and an auto value would be derived, never
// read.
func EnumMemberConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsPropertyAccessExpression(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, access.Name())
	if symbol == nil || symbol.ValueDeclaration == nil || !ast.IsEnumMember(symbol.ValueDeclaration) {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := symbol.ValueDeclaration.AsEnumMember().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
	if !ast.IsNumericLiteral(head) {
		return kernelbridge.LoopEffect{}, false
	}
	return LowerEffectExpression(head, EffectReader{
		ReadPlace: func(string) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
		Opaque:    func(*ast.Node) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
	})
}

// IsAbsentKeyword is whether an expression spells the ABSENT value:
// `null` or `undefined`. JavaScript's two absent spellings are one
// outcome kernel-side (KnownState's absent flag conflates them), so
// both lower to the same state constant.
//
// `undefined` is an ordinary identifier in the grammar, not a keyword
// token — a local named `undefined` would shadow it, so the tracked
// slots are consulted first by the caller (a tracked name COPIES) and
// only a free spelling reaches here.
func IsAbsentKeyword(e *ast.Node) bool {
	head := Unwrapped(e)
	if head.Kind == ast.KindNullKeyword {
		return true
	}
	return ast.IsIdentifier(head) && head.Text() == "undefined"
}

// RhsEffect is rhsEffect in the TS source: an assigned RIGHT side as
// an effect — a tracked name COPIES under any sort, `null`/`undefined`
// write the absent state constant under ANY sort, a string literal
// writes its exact tuple into a string-sorted slot, a string-sorted
// concatenation or template builds its sequence, and everything else
// reads numerically.
func RhsEffect(context *LoweringContext, targetSort BindingKind, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	if copy, ok := IndexOf(context, e); ok {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: copy}, true
	}
	// `x = null` / `return undefined`: the absent outcome, which no set
	// can hold — it rides in the state constant's flag instead. Under
	// any target sort: absence is neither a number nor a word.
	if IsAbsentKeyword(e) {
		return kernelbridge.AbsentConst(), true
	}
	if ast.IsStringLiteral(e) && targetSort == BindingKindString {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.StringTuple(e.AsStringLiteral().Text)}, true
	}
	// a string-sorted right side reads as a SEQUENCE first: `a + b`
	// between two string-sorted operands concatenates, and a template
	// literal is that concatenation spelled out. Numeric reading
	// follows for everything else.
	if targetSort == BindingKindString {
		if seq, ok := SequenceEffectOf(context, e); ok {
			return seq, true
		}
	}
	return EffectOf(context, e)
}

var compoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
	// the compound bitwise and shift forms. The number-sort gate below
	// (Sorts[target] != BindingKindNumber) is the only gate they need:
	// ToInt32 is total on doubles, so any number-sorted target and
	// operand is a legal input to transferBitwise.
	ast.KindAmpersandEqualsToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindBarEqualsToken:                               kernelbridge.LoopOpBitOr,
	ast.KindCaretEqualsToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanEqualsToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanEqualsToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: kernelbridge.LoopOpShr,
	// the compound exponentiation form. It wears the same number-sort
	// gate the rest of this table does: transferPow carries the spec's
	// own NaN rows as cells, so any number-sorted target and operand is
	// a legal input.
	ast.KindAsteriskAsteriskEqualsToken: kernelbridge.LoopOpPow,
}

// AssignmentOfExpression is assignmentOfExpression in the TS
// source: an assigning EXPRESSION's target index and effect —
// `x = e`, `x += e`-family compounds, and `i++`/`--i` steps.
func AssignmentOfExpression(context *LoweringContext, e *ast.Node) (AssignmentTarget, bool) {
	// i++ / --i and friends: the unit step, spelled as arithmetic —
	// the step READS its target numerically
	if ast.IsPostfixUnaryExpression(e) || ast.IsPrefixUnaryExpression(e) {
		var operator ast.Kind
		var operand *ast.Node
		if ast.IsPostfixUnaryExpression(e) {
			unary := e.AsPostfixUnaryExpression()
			operator, operand = unary.Operator, unary.Operand
		} else {
			unary := e.AsPrefixUnaryExpression()
			operator, operand = unary.Operator, unary.Operand
		}
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			target, ok := NumberIndexOf(context, operand)
			if !ok {
				return AssignmentTarget{}, false
			}
			op := kernelbridge.LoopOpAdd
			if operator == ast.KindMinusMinusToken {
				op = kernelbridge.LoopOpSub
			}
			one := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}
			targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
			return AssignmentTarget{
				Target: target,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &targetVar, B: &one},
			}, true
		}
	}
	if !ast.IsBinaryExpression(e) {
		return AssignmentTarget{}, false
	}
	bin := e.AsBinaryExpression()
	// the target slot, read through the same IndexOf every route reads a
	// place through. A MEMBER target resolves here exactly as a local
	// does where the member names a tracked slot — a record leaf
	// ("this.staticMethodKey"), an array slot — so `this.x ??= d` reaches
	// the same joins `x ??= d` reaches. A member that resolves to no slot
	// still declines: there is nothing to join into.
	target, ok := IndexOf(context, bin.Left)
	if !ok {
		return AssignmentTarget{}, false
	}
	if bin.OperatorToken.Kind == ast.KindEqualsToken {
		effect, ok := RhsEffect(context, context.Sorts[target], bin.Right)
		if !ok {
			return AssignmentTarget{}, false
		}
		return AssignmentTarget{Target: target, Effect: effect}, true
	}
	// `x ||= e`, `x &&= e`, `x ??= e`: the result is x itself or e —
	// the JOIN of the two admits every run, under any sort, exactly as
	// the short-circuit operators read in expression position
	switch bin.OperatorToken.Kind {
	case ast.KindBarBarEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindQuestionQuestionEqualsToken:
		right, rightOk := RhsEffect(context, context.Sorts[target], bin.Right)
		if !rightOk {
			return AssignmentTarget{}, false
		}
		targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
		return AssignmentTarget{
			Target: target,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &targetVar, B: &right},
		}, true
	}
	// `s += suffix` on a STRING-sorted slot is concatenation — exactly
	// `s = s + suffix`, which already lowers; the compound spelling gets
	// the same sequence reading instead of refusing on the number gate
	if bin.OperatorToken.Kind == ast.KindPlusEqualsToken && context.Sorts[target] == BindingKindString {
		right, rightOk := SequenceEffectOf(context, bin.Right)
		if !rightOk {
			return AssignmentTarget{}, false
		}
		targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
		return AssignmentTarget{
			Target: target,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &targetVar, B: &right},
		}, true
	}
	op, ok := compoundOps[bin.OperatorToken.Kind]
	if !ok {
		return AssignmentTarget{}, false
	}
	// a compound READS its target numerically
	if context.Sorts[target] != BindingKindNumber {
		return AssignmentTarget{}, false
	}
	b, ok := EffectOf(context, bin.Right)
	if !ok {
		return AssignmentTarget{}, false
	}
	targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
	return AssignmentTarget{
		Target: target,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &targetVar, B: &b},
	}, true
}

// ChainedAssignmentsOf lowers `x1 = x2 = e` — an assignment whose RIGHT
// SIDE is itself a plain assignment, however many links deep.
//
// JavaScript evaluates the chain right to left and every link takes the
// same value: `x2 = e` writes e into x2 and EVALUATES to what it wrote,
// which is then what `x1 =` writes. So the chain lowers as one
// assignment per link, innermost first, each outer link reading the slot
// the link inside it just wrote:
//
//	x1 = x2 = data.coordinate  ⇒  x2 := data.coordinate ; x1 := var x2
//
// Reading the INNER SLOT rather than re-reading the right side is what
// keeps the two links tied: the kernel then knows x1 and x2 hold the
// same value, which is the whole content of the chain. (Re-lowering the
// expression twice would give two independent readings of one
// evaluation, and for a right side with any width the two links would
// drift apart.)
//
// Only a plain `=` at every link is a chain. A compound (`x1 = x2 += e`)
// evaluates its target first and belongs to the compound rule, which
// reads one target; this route leaves those alone.
//
// The innermost right side is whatever RhsEffect spells for the
// innermost target's sort, so a chain ending in a string, an absent, or
// a call the hoist route reads all lower exactly as the same assignment
// written on its own would.
//
// Declines where any link's target has no slot or the innermost right
// side does not lower — the statement then falls to the routes below it,
// and ultimately the floor, exactly as the whole chain did before this
// route existed.
func ChainedAssignmentsOf(context *LoweringContext, e *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	// walk OUT to IN collecting each link's target, stopping at the first
	// right side that is not itself a plain assignment
	var targets []int
	current := e
	for {
		bin := current.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindEqualsToken {
			return nil, false
		}
		target, ok := IndexOf(context, bin.Left)
		if !ok {
			return nil, false
		}
		targets = append(targets, target)
		right := Unwrapped(bin.Right)
		if !ast.IsBinaryExpression(right) ||
			right.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsToken {
			// the innermost right side: the value the whole chain writes
			if len(targets) < 2 {
				// a single `x = e` is not a chain — the ordinary rule owns it
				return nil, false
			}
			innermost := targets[len(targets)-1]
			effect, effectOk := RhsEffect(context, context.Sorts[innermost], bin.Right)
			if !effectOk {
				return nil, false
			}
			// innermost first, then each outer link copying the slot inside it
			out := make([]AssignmentTarget, 0, len(targets))
			out = append(out, AssignmentTarget{Target: innermost, Effect: effect})
			for index := len(targets) - 2; index >= 0; index-- {
				out = append(out, AssignmentTarget{
					Target: targets[index],
					Effect: kernelbridge.LoopEffect{
						Kind:  kernelbridge.LoopEffectVar,
						Index: targets[index+1],
					},
				})
			}
			return out, true
		}
		current = right
	}
}

// ChainedAssignmentStatementOf is the statement-position reading of a
// chained assignment: `x1 = x2 = e;` as its own expression statement.
func ChainedAssignmentStatementOf(context *LoweringContext, s *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(s) {
		return nil, false
	}
	return ChainedAssignmentsOf(context, Unwrapped(s.AsExpressionStatement().Expression))
}

// DeclarationAssignment is declarationAssignment in the TS source: a
// single-name declaration's target and effect.
func DeclarationAssignment(context *LoweringContext, declarations []*ast.Node) (AssignmentTarget, bool) {
	if len(declarations) != 1 {
		return AssignmentTarget{}, false
	}
	return declaratorAssignment(context, declarations[0])
}

// declaratorAssignment lowers ONE declarator: an identifier name whose
// slot exists, holding its initializer's effect — or, with NO
// initializer, holding exactly the value the runtime gives it:
// undefined. `let x;` is not an unknown, it is an absent constant.
func declaratorAssignment(context *LoweringContext, declaration *ast.Node) (AssignmentTarget, bool) {
	d := declaration.AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) {
		return AssignmentTarget{}, false
	}
	target, ok := IndexOf(context, d.Name())
	if !ok {
		return AssignmentTarget{}, false
	}
	if d.Initializer == nil {
		return AssignmentTarget{Target: target, Effect: kernelbridge.AbsentConst()}, true
	}
	effect, ok := RhsEffect(context, context.Sorts[target], d.Initializer)
	if !ok {
		return AssignmentTarget{}, false
	}
	return AssignmentTarget{Target: target, Effect: effect}, true
}

// declaratorAssignments lowers ONE declarator into however many slots it
// writes, which is what a declarator whose name is NOT one scalar slot
// needs.
//
// Three cases, and the difference is only where the name's slots are:
//
//   - the name has its OWN slot: the single rule above, unchanged.
//   - the name is FLATTENED (a record, an array, a collection, a
//     promise) and the declarator has NO INITIALIZER: `let box;` leaves
//     every path under box undefined, so every leaf takes the absent
//     constant. That is the runtime's own answer for the whole subtree,
//     not an approximation of it.
//   - the name has NO SLOT ANYWHERE: nothing is written, and nothing
//     lowered can read the name either, so there is nothing to be wrong
//     about. This is a SUCCESS with an empty write list, which is what
//     lets a statement declaring several names lower when only some of
//     them are tracked.
//
// A FLATTENED name WITH an initializer is not this route's: the
// declaration-shaped recognizers (the object, array and collection
// families) already read those, and each writes the leaves from the
// initializer's own parts. Reaching here with one means those declined,
// and writing absent over the leaves would claim the initializer wrote
// nothing — so it declines, exactly as it did before.
func declaratorAssignments(context *LoweringContext, declaration *ast.Node) ([]AssignmentTarget, bool) {
	d := declaration.AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) {
		return nil, false
	}
	if _, has := IndexOf(context, d.Name()); has {
		assignment, ok := declaratorAssignment(context, declaration)
		if !ok {
			return nil, false
		}
		return []AssignmentTarget{assignment}, true
	}
	leaves := flattenedSlotsUnder(context, d.Name().Text())
	if len(leaves) == 0 {
		// a name nothing tracks: no write, and no reader either
		return nil, true
	}
	if d.Initializer != nil {
		return nil, false
	}
	out := make([]AssignmentTarget, 0, len(leaves))
	for _, leaf := range leaves {
		out = append(out, AssignmentTarget{Target: leaf, Effect: kernelbridge.AbsentConst()})
	}
	return out, true
}

// FunctionValuedDeclarationOf lowers `const f = () => { … }` — a
// closure held in a local. CREATING a closure runs nothing (inertValue
// draws the evaluation boundary at exactly a function literal), and a
// function value has no scalar spelling, so the name takes unknown.
//
// WHAT THE DECLARATION MAY ADMIT, and why. The declaration statement
// itself moves nothing: the closure's body runs at a CALL, never here.
// So the question the gate has to answer is not "does this body touch
// tracked state" but "can any route believe a slot the closure writes
// BEFORE the write happens". The consumers of an admitted declaration,
// enumerated:
//
//   - A CALL THROUGH THE NAME in this same body (`cleanup()`,
//     `onClose()`). ClosureCallHavocOf (ir_summary_call.go) serves that
//     statement by havocking exactly the closure's write set at the call
//     position, which is where the writes land. Nothing believes a
//     written slot across the call.
//   - THE NAME HANDED OUT — passed as an argument
//     (`disconnectSource.once('close', onClose)`), stored into a field,
//     returned. Every one of those is a mention of `f` in some later
//     statement, and `f` is a scalar-slotted local, so the statement
//     holding the mention takes its own route: a served call threads no
//     closure body, and every unserved shape reaches the opaque call
//     tier or the statement floor — both of which walk INTO the
//     mentioned closure's declaration only where the syntax carries it.
//     A bare `f` handed to unseen code is the hand-over case, and it is
//     covered by the write-set havoc this route emits AT THE
//     DECLARATION for exactly that set (below): the closure may be
//     called at any later time by code no statement here places, so
//     every name it writes is havocked once, up front, and no statement
//     after the declaration believes any of them.
//   - THE NAME NEVER USED AGAIN. Nothing reads it; the unknown write is
//     the whole claim and it claims nothing.
//
// So the admission is: emit the name's own `unknown`, and ALSO havoc
// every tracked slot the closure assigns (closureAssignedNames through
// ClosureWriteSlots). The havoc is what makes the hand-over case sound
// without asking where the value went — it is the same one-shot havoc
// the floor would have applied, computed from the closure's own write
// set rather than from every name the body mentions.
//
// WHAT STAYS REFUSED. A closure that writes through a FLATTENED local
// it captures — `p.a = 1` on a record, `xs.push(v)` on an array — is
// not spelled by the assigned-name reading: a member write's target has
// a slot only where the exact dotted path was laid out, and a mutator
// call is not a write form at all. closureMutatesFlattenedCapture below
// refuses those, and the statement keeps the floor.
func FunctionValuedDeclarationOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	d := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
		return nil, false
	}
	closure := Unwrapped(d.Initializer)
	if !ast.IsFunctionLike(closure) || closure.Body() == nil {
		return nil, false
	}
	if closureMutatesFlattenedCapture(context, closure.Body()) {
		return nil, false
	}
	// the closure's OWN write set, havocked here: the value may be called
	// at a time no statement of this body places, so no statement after
	// this one may believe a slot the body assigns
	out := havocAssignments(ClosureWriteSlots(context, closure.Body()))
	if slot, has := IndexOf(context, d.Name()); has {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
	}
	return out, true
}

// ClosureWriteSlots is the tracked slots a closure's body ASSIGNS —
// closureAssignedNames' spellings resolved against this lowering's own
// slot vector. The declaration route havocs them at the declaration
// (the value may leave), and the call route havocs them at each call
// (the value stayed and was called here).
//
// A spelling with no slot contributes nothing: nothing lowered can read
// a name the vector never laid out, so there is no belief for the
// closure's write to falsify.
func ClosureWriteSlots(context *LoweringContext, body *ast.Node) map[int]struct{} {
	slots := map[int]struct{}{}
	if context == nil || body == nil {
		return slots
	}
	written := map[string]struct{}{}
	closureAssignedNames(body, written)
	for name := range written {
		if slot, held := slotIndexOfName(context, name); held {
			slots[slot] = struct{}{}
		}
		// a written name that is itself a FLATTENED local (`p` in `p = q`)
		// moves every leaf under it, not one slot
		for _, leaf := range flattenedSlotsUnder(context, name) {
			slots[leaf] = struct{}{}
		}
	}
	return slots
}

// closureMutatesFlattenedCapture answers whether a closure's body could
// move a FLATTENED capture — a record leaf, an array's length or
// elements, a collection, a promise's inner — by a route the assigned-
// name census does not spell.
//
// Two shapes, and both are refusals because ClosureWriteSlots would
// UNDER-count them, which is the unsound direction:
//
//   - a MEMBER or ELEMENT write (`p.a = 1`, `xs[i] = v`) whose target
//     resolves to no single slot: closureAssignedNames records the
//     spelled step ("p.a"), and where that exact path has no slot the
//     write moves a leaf under `p` that nothing havocs;
//   - a MENTION of a flattened local ANYWHERE in the body: the closure
//     may hand that object on, or call a mutator on it (`xs.push(v)`,
//     `m.set(k, v)`), and neither is a write form the census reads.
//
// A mention of a SCALAR capture is not refused — a scalar passes by
// value, so unseen code cannot write back through it, and a scalar the
// closure assigns is already in the write set.
func closureMutatesFlattenedCapture(context *LoweringContext, body *ast.Node) bool {
	if context == nil || body == nil {
		return false
	}
	mutates := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if mutates || node == nil {
			return true
		}
		// a write whose target is a member or element step, with no slot of
		// its own: the leaf it moves is not one the write set names
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if memberWriteWithoutSlot(context, bin.Left) {
					mutates = true
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) || ast.IsPostfixUnaryExpression(node) {
			var operator ast.Kind
			var operand *ast.Node
			if ast.IsPrefixUnaryExpression(node) {
				operator, operand = node.AsPrefixUnaryExpression().Operator, node.AsPrefixUnaryExpression().Operand
			} else {
				operator, operand = node.AsPostfixUnaryExpression().Operator, node.AsPostfixUnaryExpression().Operand
			}
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				if memberWriteWithoutSlot(context, operand) {
					mutates = true
					return true
				}
			}
		}
		// a mention of a flattened local: the object itself is reachable to
		// the closure's later run, and a mutator call on it is no write form
		if ast.IsIdentifier(node) && len(flattenedSlotsUnder(context, node.Text())) > 0 {
			mutates = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return mutates
}

// memberWriteWithoutSlot is whether a write target is a member or
// element step that resolves to NO slot — the target whose moved leaf
// the assigned-name census cannot name. A bare identifier is not this
// route's (the census spells it), and a member step the vector DID lay
// out is spelled by its own dotted path.
func memberWriteWithoutSlot(context *LoweringContext, target *ast.Node) bool {
	head := Unwrapped(target)
	if head == nil || ast.IsIdentifier(head) {
		return false
	}
	if !ast.IsPropertyAccessExpression(head) && !ast.IsElementAccessExpression(head) {
		return false
	}
	if _, held := IndexOf(context, head); held {
		return false
	}
	return true
}

// MultiDeclarationAssignmentsOf lowers `let a = 1, b = 2` — a variable
// statement with SEVERAL declarators, each an ordinary declarator the
// single route already reads, and each writing however many slots its
// own name holds. All-or-nothing: one declarator no route spells
// declines the statement to the next route (and ultimately the floor),
// exactly as the whole statement declined before this existed.
//
// A declarator naming something with NO slot writes nothing and does not
// decline the statement — the clause `let viewBox, label, positionAttrs;`
// lowers the names that are tracked and passes over the ones that are
// not, which is sound because nothing lowered can read an untracked
// name.
//
// Only statements with two or more declarators are this route's — a
// single declarator keeps its existing routes, object literals and
// calls included, which run earlier in the dispatch.
func MultiDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) < 2 {
		return nil, false
	}
	out := make([]AssignmentTarget, 0, len(declarations))
	for _, declaration := range declarations {
		assignments, ok := declaratorAssignments(context, declaration)
		if !ok {
			return nil, false
		}
		out = append(out, assignments...)
	}
	return out, true
}

// AssignmentOf is assignmentOf in the TS source: an assignment's
// target index and effect, from `x = e`, `let x = e`, or `x += e`-
// family compounds.
func AssignmentOf(context *LoweringContext, s *ast.Node) (AssignmentTarget, bool) {
	if ast.IsVariableStatement(s) {
		return DeclarationAssignment(context, s.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes)
	}
	if !ast.IsExpressionStatement(s) {
		return AssignmentTarget{}, false
	}
	return AssignmentOfExpression(context, s.AsExpressionStatement().Expression)
}
