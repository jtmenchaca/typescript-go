// split from ir_opaque_havoc.go — the enumeration

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the enumeration ─────────────────────────────────────────────── */

// havocSlotsOfStatement collects (a), (b) and (c) for one statement.
//
// A LOOP whose head or body no route read — `for (const k in o)`,
// `for await (const x of xs)`, a `while` whose head declines — havocs
// the UNION of its head's and its body's slot set ONCE. A loop is its
// statements repeated, and havoc is idempotent: writing unknown into a
// slot twice leaves the same state as writing it once, so the single
// pass covers every trip count including zero.
//
// A `try`/`catch`/`finally` is the union of all three blocks: any prefix
// of the try may have run before control left it, the catch may or may
// not run, and the finally always does — so every slot any of them could
// write is a slot the statement could write.
//
// A nested FUNCTION's body is walked, not skipped. A callback closes
// over this body's names, and calling it writes them: `xs.forEach(v => {
// total += v; })` writes `total`, which is one of THIS body's slots. So
// rules (a) and (b) apply inside the callback exactly as outside it, and
// the callback's own parameters and locals contribute nothing (their
// names have no slot here, so declaredNameSlots and flattenedSlotsUnder
// both answer empty for them). Only the enumeration's IMPOSSIBILITY scan
// stops at a function boundary — a return inside a callback returns from
// the callback, not from this body.
//
// A parameter of the callback that SHADOWS an outer name is the one case
// the syntactic reading over-approximates: `xs.forEach(total => …)`
// would havoc the outer `total` although the inner one is what moved.
// Over-approximating which slots moved is the safe direction — the floor
// may only ever havoc too much, never too little.
func havocSlotsOfStatement(context *LoweringContext, statement *ast.Node) (map[int]struct{}, bool) {
	slots := map[int]struct{}{}
	add := func(slot int) {
		if slot >= 0 {
			slots[slot] = struct{}{}
		}
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		// (a) an ASSIGNMENT target: `x = e`, `x += e`, `x++`, `--x`. The
		// target is read through the same IndexOf every lowering route
		// reads it through, so an unreadable right side does not stop the
		// left side's slot from being named.
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if slot, tracked := IndexOf(context, Unwrapped(bin.Left)); tracked {
					add(slot)
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if slot, tracked := IndexOf(context, Unwrapped(unary.Operand)); tracked {
					add(slot)
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if slot, tracked := IndexOf(context, Unwrapped(unary.Operand)); tracked {
					add(slot)
				}
			}
		}
		// (c) a DECLARED name, and every name a destructuring pattern
		// binds. A bound name with NO slot needs no havoc: nothing lowered
		// can read it later either — a read of a name with no slot declines
		// wherever it appears, so there is no knowledge about it for an
		// unread write to falsify.
		if ast.IsVariableDeclaration(node) {
			for _, slot := range declaredNameSlots(context, node.AsVariableDeclaration().Name()) {
				add(slot)
			}
		}
		// (b) a MENTION of a flattened local — every leaf it holds. The
		// mention is by IDENTIFIER, whatever position it sits in: the point
		// is that unseen code got a reference to the object.
		if ast.IsIdentifier(node) {
			for _, slot := range flattenedSlotsUnder(context, node.Text()) {
				add(slot)
			}
			// an ELEMENT ALIAS holds no slots under its own name, but the
			// element it stands for does — a mention hands the element's
			// reference to unseen code, which may write any of its member
			// slots (ElementAliasHavocSlots' own doc)
			for _, slot := range ElementAliasHavocSlots(context, node) {
				add(slot)
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return slots, true
}

// declaredNameSlots is the slots one declaration's NAME contributes: the
// single slot of a plain identifier, or every bound name's slot in a
// binding pattern (a destructuring target). A name with no slot
// contributes nothing, which is the (c) rule's own comment: a name the
// slot vector never laid out cannot be read later either.
func declaredNameSlots(context *LoweringContext, name *ast.Node) []int {
	if name == nil {
		return nil
	}
	if ast.IsIdentifier(name) {
		var out []int
		if slot, found := slotIndexOfName(context, name.Text()); found {
			out = append(out, slot)
		}
		// a declaration may name a FLATTENED local, whose slots are its
		// leaves rather than one slot of its own
		return append(out, flattenedSlotsUnder(context, name.Text())...)
	}
	if !ast.IsObjectBindingPattern(name) && !ast.IsArrayBindingPattern(name) {
		return nil
	}
	var out []int
	for _, element := range name.AsBindingPattern().Elements.Nodes {
		if !ast.IsBindingElement(element) {
			continue
		}
		out = append(out, declaredNameSlots(context, element.AsBindingElement().Name())...)
	}
	return out
}

// flattenedSlotsUnder is every slot a FLATTENED local holds under one
// spelled name, read through the recognizers' OWN slot spellings so this
// enumeration and the lowering can never disagree about what a local's
// slots are:
//
//	a record   → leafSlotsUnder      ("p.a", "p.a.b", …)
//	an array   → arraySlotsOf        ("a.len", "a.elem")
//	a Map/Set  → mapSlotsOf          ("m.size", "m.vals", and "m.keys")
//	a promise  → promiseInnerSlotOf  ("p.inner")
//
// A name that is an ordinary scalar contributes nothing here: its single
// slot is named by rule (a) or (c) where the statement writes it, and a
// scalar passed to unseen code is passed BY VALUE, so a callee cannot
// write back through it.
func flattenedSlotsUnder(context *LoweringContext, name string) []int {
	var out []int
	if leaves, ok := leafSlotsUnder(context, name); ok {
		for _, leaf := range leaves {
			out = append(out, leaf.Index)
		}
	}
	if lenSlot, elemSlot, ok := arraySlotsOf(context, name); ok {
		out = append(out, lenSlot, elemSlot)
	}
	if sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, name); ok {
		out = append(out, sizeSlot, valsSlot)
		if keysOk {
			out = append(out, keysSlot)
		}
	}
	if innerSlot, ok := promiseInnerSlotOf(context, name); ok {
		out = append(out, innerSlot)
	}
	return out
}
