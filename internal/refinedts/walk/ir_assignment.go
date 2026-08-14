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
	if (ast.IsObjectLiteralExpression(head) || ast.IsArrayLiteralExpression(head) ||
		ast.IsStringLiteral(head) || ast.IsTemplateExpression(head)) && writeAndCallFree(head) {
		// the value has no scalar spelling in a number-sorted read (a
		// string const's tuple belongs to the sequence route) — unknown
		// is what this reader can hold of it, and it is exact about that
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
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

// FunctionValuedDeclarationOf lowers `const f = () => { … }` — a
// closure held in a local. CREATING a closure runs nothing, and a
// function value has no scalar spelling, so the name takes unknown —
// provided the closure can never move state this body tracks when it
// DOES run: it must write no name that has a slot here, and mention no
// flattened local (a mention hands the object to code that runs at a
// time nothing places). A closure that touches tracked state keeps the
// floor, whose one-shot havoc is only sound at the declaration — the
// capture-havoc machinery is the this-bundle's answer, not a plain
// local's.
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
	if closureTouchesTrackedState(context, closure.Body()) {
		return nil, false
	}
	var out []kernelbridge.IrStatement
	if slot, has := IndexOf(context, d.Name()); has {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
	}
	return out, true
}

// closureTouchesTrackedState answers whether a closure's body, run at
// any later time, could move state this lowering tracks: a write form
// whose target identifier has a slot, or any mention of a name whose
// flattened leaves have slots. A shadowing inner declaration makes the
// syntactic reading over-approximate toward refusal, which is the safe
// direction.
func closureTouchesTrackedState(context *LoweringContext, body *ast.Node) bool {
	touches := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if touches || node == nil {
			return true
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if target := Unwrapped(bin.Left); ast.IsIdentifier(target) {
					if _, has := IndexOf(context, target); has {
						touches = true
						return true
					}
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
				if target := Unwrapped(operand); ast.IsIdentifier(target) {
					if _, has := IndexOf(context, target); has {
						touches = true
						return true
					}
				}
			}
		}
		if ast.IsIdentifier(node) {
			if len(flattenedSlotsUnder(context, node.Text())) > 0 {
				touches = true
				return true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return touches
}

// MultiDeclarationAssignmentsOf lowers `let a = 1, b = 2` — a variable
// statement with SEVERAL declarators, each an ordinary declarator the
// single route already reads. All-or-nothing: one declarator no route
// spells declines the statement to the next route (and ultimately the
// floor), exactly as the whole statement declined before this existed.
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
		assignment, ok := declaratorAssignment(context, declaration)
		if !ok {
			return nil, false
		}
		out = append(out, assignment)
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
