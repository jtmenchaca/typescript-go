// split from ir_assignment.go — function-valued declarations and closure write sets

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
	// this one may believe a slot the body assigns. The whole closure
	// node goes to the census — a bare body block would read as "no
	// closures inside" and miss the closure's own top-level writes.
	out := havocAssignments(ClosureWriteSlots(context, closure))
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
//
// THE ARGUMENT IS THE CLOSURE NODE, never its body: the census walks a
// non-function subtree looking for closures INSIDE it, so a bare body
// block answers the empty set and the closure's own top-level writes
// go unseen — the under-count that let a stored closure's writes pass
// unhavocked.
func ClosureWriteSlots(context *LoweringContext, closure *ast.Node) map[int]struct{} {
	slots := map[int]struct{}{}
	if context == nil || closure == nil {
		return slots
	}
	written := map[string]struct{}{}
	closureAssignedNames(closure, written)
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
