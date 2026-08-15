// split from ir_assignment.go — numeric expression-as-effect reading

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
			// `s.indexOf(needle)` / `s.lastIndexOf(needle)` — a NUMBER off
			// a word receiver, both riding the SAME kernel row (see
			// stringIndexOfEffect's doc). Ahead of the hoist, which would
			// spend a temp slot and answer unknown for a value the kernel
			// can bound.
			if held, ok := stringIndexOfEffect(context, node); ok {
				return held, true
			}
			// `s.includes(needle)` / `s.startsWith(needle)` /
			// `s.endsWith(needle)` — a BOOLEAN off a word receiver: the
			// two-value set, exactly as a comparison's. Ahead of the
			// hoist for the same reason as indexOf.
			if held, ok := stringBooleanSearchEffect(context, node); ok {
				return held, true
			}
			// `s.length` on a STATED string receiver — the code-unit
			// count. Ahead of the hoist for the same reason as indexOf.
			if held, ok := stringLengthEffect(context, node); ok {
				return held, true
			}
			// `a.indexOf(v)` / `a.lastIndexOf(v)` / `a.includes(v)` /
			// `a.at(i)` on a FLATTENED array — the numeric window, the
			// boolean pair, and the guarded element read. Ahead of the
			// hoist for the same reason as the string rows.
			if held, ok := ArrayNumericReadEffect(context, node); ok {
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

// stringIndexOfEffect reads `s.indexOf(needle)` OR `s.lastIndexOf(needle)`
// over a WORD receiver and answers the kernel's numeric-from-sequence
// row.
//
// What the kernel claims. Every answer is -1, or an index INTO the
// receiver's UTF-16 code-unit sequence — at or above zero and strictly
// below the code-unit length (sec-string.prototype.indexof and, for the
// same bound, sec-string.prototype.lastindexof: both search for a
// substring and answer either its index or -1, and neither's clause
// permits an index outside the string). For a receiver whose set states
// a scalar-count ceiling hi, TERMS-v2 §12 bounds that length at 2*hi
// (each astral scalar counting twice), so the window is {-1} u [0, 2*hi);
// with no ceiling stated it is {-1} u [0, +inf), and the integrality
// rides either way.
//
// BOTH methods ride the SAME wire op, LoopOpIndexOf ("indexOf"). The
// kernel's evalSeqNum (set_functions/walk.lean) does not read its op
// argument at all — every SeqNumOp answers the identical indexOfWindow
// — and the wire's own decoder (seqNumOpOf, boundary/exports.lean)
// accepts only the string "indexOf", so "indexOf" is the one legal
// spelling for this whole row; the op tag names the FAMILY (a bounded
// search returning an index-or-(-1)), not a search direction. The
// [-1, 2*hi) claim is sound for lastIndexOf on its own clause exactly as
// it is for indexOf on its: lastIndexOf's _start_ is clamped to
// [0, length - searchLength] (step 9) and its result is either
// StringLastIndexOf's found index — necessarily inside the receiver, by
// the same code-unit bound indexOf's result obeys — or the *-1* floor
// (step 10), so the direction the search runs in changes which match a
// tie picks, never the window either method's answer can land in.
//
// The NEEDLE is not sent. The window holds for every needle, so a
// needle operand would be a field no claim reads — but the needle still
// has to be a shape that RUNS as an ordinary search, which is what the
// one-argument gate is for: a second `position` argument shifts where
// the search starts (indexOf) or clamps its end (lastIndexOf), which
// changes no answer's bounds but is not a shape this reader has read,
// so it declines rather than guess.
//
// The receiver must read as a pure SEQUENCE. An ARRAY's `indexOf` /
// `lastIndexOf` is a different operation over a different receiver
// world — its answer is bounded by the element COUNT, not by a
// code-unit length — and it declines here, since an array local's name
// has no sequence reading.
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
	if !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	name := access.Name().Text()
	if name != "indexOf" && name != "lastIndexOf" {
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

// stringBooleanSearchEffect reads `s.includes(needle)`,
// `s.startsWith(needle)`, or `s.endsWith(needle)` over a WORD receiver
// and answers the exact two-value set — the same boolean-pair claim a
// comparison's return carries (booleanPairEffect), since each method's
// clause returns exactly *true* or *false* on every reachable step
// (sec-string.prototype.includes steps 9/10, sec-string.prototype.
// startswith and sec-string.prototype.endswith, each ending in a
// *true*/*false* return with no other exit).
//
// The NEEDLE and the optional POSITION are not sent, for the same
// reason indexOf's needle is not sent: the claim — the result is one of
// two values — holds for every needle and every position, so neither
// operand is a field any claim reads. What must still hold is that
// evaluating them moves nothing: each argument is admitted only where
// writeAndCallFree, matching the gate the comparison operators wear in
// LowerEffectExpression. The 1- and 2-argument forms are BOTH admitted
// — unlike indexOf's window, whose ceiling can change with a shifted
// search start, the boolean claim does not narrow or widen with the
// position argument at all, so gating out the 2-argument form would
// cost precision for no soundness reason.
//
// The receiver must read as a pure SEQUENCE, the same gate
// stringIndexOfEffect wears — an object with a same-named method over a
// different receiver world is not this call, and SequenceEffectOf's own
// sort reading is what rules that out.
func stringBooleanSearchEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
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
	if !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	switch access.Name().Text() {
	case "includes", "startsWith", "endsWith":
	default:
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 || len(call.Arguments.Nodes) > 2 {
		return kernelbridge.LoopEffect{}, false
	}
	for _, argument := range call.Arguments.Nodes {
		if ast.IsSpreadElement(argument) || !writeAndCallFree(argument) {
			return kernelbridge.LoopEffect{}, false
		}
	}
	if _, receiverOk := SequenceEffectOf(context, access.Expression); !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	return booleanPairEffect(), true
}

// stringMaxLength is the String type's own ceiling — an ordered
// sequence of UTF-16 elements up to a maximum length of 2^53 - 1
// (sec-ecmascript-language-types-string-type). Number.MAX_SAFE_INTEGER
// is the same value (sec-number.max_safe_integer), read here as a
// literal so this file states the spec's bound directly rather than by
// a cross-package name.
const stringMaxLength = 9007199254740991

// stringLengthEffect reads `s.length` over a STATED string receiver and
// answers a NUMBER: the receiver's own UTF-16 code-unit count.
//
// The EXACT case. A receiver this side holds as one exact word —
// SequenceEffectOf answering a `const` leaf built over a literal or a
// literal chain — has its own length exactly: Utf16LengthOf on its
// codepoint tuple, sent as a singleton set. This is the case a template
// substitution or a concatenation of two tracked strings does NOT reach
// (their exact text is not known here), so it serves only the literal
// case; a slot's own value, even a fully-string-sorted one, is not
// available to this Go-side reader at all — only the KERNEL sees a
// slot's entry set, and no wire op exists to ask it for a length (the
// numeric-from-sequence family, seqNumOpOf, decodes only "indexOf").
//
// The SORT-ONLY case. Everywhere else the receiver reads as a sequence
// (a name, a concatenation, a template, …) but this side cannot state
// its length, so the row claims what the String type itself claims and
// nothing more: a non-negative integer, at most 2^53 - 1
// (sec-ecmascript-language-types-string-type). That is a real
// determination — it excludes the absent value, NaN, and every
// negative or non-integer number — even though it names no tighter
// window.
func stringLengthEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(node)
	if !ast.IsPropertyAccessExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	access := head.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "length" {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := SequenceEffectOf(context, access.Expression)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	if receiver.Kind == kernelbridge.LoopEffectConst {
		if points, ok := refinementsets.WordTuplesOf(receiver.Set); ok && len(points) == 1 {
			return constNumber(float64(refinementsets.Utf16LengthOf(points[0]))), true
		}
	}
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(stringMaxLength)),
	}, true
}
