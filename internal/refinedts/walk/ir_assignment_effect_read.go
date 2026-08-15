// split from ir_assignment.go — numeric expression-as-effect reading

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
