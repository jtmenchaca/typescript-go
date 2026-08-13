// The sound floor for a statement no route reads: HAVOC, not decline.
//
// Before this file, a statement no lowering route recognized declined
// the WHOLE body, and the body kept nothing. That is the wrong trade in
// the slot world: a statement the lowering cannot read still cannot do
// anything except move slots, and there is a finite, enumerable set of
// slots it can move. Writing `unknown` into each of those and keeping
// every other slot's knowledge is sound, and it keeps the body's route.
//
// WHAT A STATEMENT CAN AFFECT, in the slot world:
//
//	(a) the tracked SCALAR slots it assigns — an assignment target, a
//	    compound, a `++`/`--` step, a destructuring target with a slot;
//	(b) every LEAF slot of any FLATTENED local the statement MENTIONS
//	    anywhere. A mention hands the object to code the lowering cannot
//	    see, and unseen code may move any leaf: a record's leaves, an
//	    array's length and element, a collection's size, values and keys,
//	    a promise-held local's inner;
//	(c) the names it DECLARES — `const x = <unreadable>` finds x's slot
//	    and havocs it.
//
// Module state, global state, and `this` state are not in the slot
// world at all, so they need nothing: no slot holds them, and no lowered
// read can answer from them.
//
// A havoc is `assign slot unknown` and nothing else. It is never a
// guessed set — the point of the floor is that it claims NOTHING about
// what the statement did, only about which slots it could have touched.
// A slot set the enumeration cannot bound is a DECLINE, not a wider
// havoc, because "which slots" is the one thing this route must be
// right about.
//
// The impossibilities, each a genuine one:
//
//   - `with (o) { … }` — every free name in the block may resolve into
//     o's properties, so which names the block writes is not a syntactic
//     question at all;
//   - a bare `eval(…)` call — the same, for arbitrary code;
//   - a LABELLED `break` or `continue` — control leaves structure the IR
//     has no statement for, so the havoc would sit where the transfer
//     never reaches;
//   - a `return` — a havoc writes slots and then falls through, but a
//     return raises the done flag and stops the block. Havocking a
//     statement that contains one would drop the raise, and every
//     statement after it would then be walked as if it ran;
//   - a `throw` — see throwCarryingStatement below for the reasoning.

package walk

import (
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// OpaqueHavocStatements is the LAST resort of the statement dispatch: a
// statement every route declined lowers as one `assign slot unknown` per
// slot it could possibly have written, deduplicated and in slot order.
//
// (nil, false) only where the slot set cannot be enumerated at all —
// never as "this statement looked hard". A false answer here is what
// still declines the body.
//
// The FIRST havocked construct's spelling is recorded into the lowering
// context (FirstHavoc), which the body-level owner reads at the end to
// report the body's outcome. Recording is first-wins and this route
// never reads it back.
func OpaqueHavocStatements(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || statement == nil {
		return nil, false
	}
	if !havocEnumerable(statement) {
		return nil, false
	}
	slots, ok := havocSlotsOfStatement(context, statement)
	if !ok {
		return nil, false
	}
	NoteFirstHavoc(context, havocConstructName(statement))
	return havocAssignments(slots), true
}

// havocAssignments turns a collected slot set into the statements that
// write them: one `assign slot unknown` each, deduplicated, in SLOT
// ORDER — so the same statement in the same context always lowers to the
// same IR, whatever order the enumeration walked the syntax in.
func havocAssignments(slots map[int]struct{}) []kernelbridge.IrStatement {
	ordered := make([]int, 0, len(slots))
	for slot := range slots {
		ordered = append(ordered, slot)
	}
	sort.Ints(ordered)
	out := make([]kernelbridge.IrStatement, 0, len(ordered))
	for _, slot := range ordered {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: unknownEffect,
		})
	}
	return out
}

/* ── the impossibilities ─────────────────────────────────────────── */

// havocEnumerable is whether the statement's written-slot set is a
// syntactic question at all. Each false below is a genuine enumeration
// impossibility, not a difficulty:
//
//   - `with (o) …`: a free name inside the block may be o's property or
//     the enclosing binding, and only the runtime object decides. The set
//     of slots the block writes is therefore not readable from syntax.
//   - `eval(…)` called as a BARE name: the code it runs is a value, and
//     that code may write any binding in scope. (A `o.eval(…)` member
//     call is an ordinary method call — only the direct call has the
//     scope-piercing semantics, so only it is refused here.)
//   - a LABELLED `break`/`continue`: it leaves an enclosing labelled
//     statement, and the IR has no statement for that transfer. Havocking
//     here would put the writes on a path the kernel walks straight
//     through, so the exit would claim the havoc happened where control
//     had already left. A BARE break or continue inside a loop is the
//     same problem for the same reason and is refused with them.
//   - a `return`: the havoc's own shape is "write these slots, then carry
//     on", and a return does not carry on — it writes the result slot,
//     raises the done flag, and ends the block. A havoc that swallowed
//     one would leave the flag down, and every statement after the
//     havocked one would then be walked as if it had run. Raising the
//     flag instead is not available either: the havoc cannot say WHICH of
//     its paths returned, so the flag would be raised on all of them.
//   - a `throw`: throwCarryingStatement.
//
// A nested FUNCTION's own returns and throws are its business, not this
// statement's, so the scan stops at a function boundary — an unreadable
// statement holding a callback is enumerable exactly as one holding any
// other expression is.
func havocEnumerable(statement *ast.Node) bool {
	enumerable := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !enumerable {
			return true
		}
		switch {
		case node.Kind == ast.KindWithStatement:
			enumerable = false
			return true
		case ast.IsBreakStatement(node), ast.IsContinueStatement(node):
			enumerable = false
			return true
		case ast.IsReturnStatement(node):
			enumerable = false
			return true
		case throwCarryingStatement(node):
			enumerable = false
			return true
		case isBareEvalCall(node):
			enumerable = false
			return true
		}
		// a nested function's transfers leave IT, not this statement
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return enumerable
}

// isBareEvalCall is `eval(…)` called through the bare name — the direct
// call whose code runs in the CALLER's scope and may write any binding
// there. `o.eval(x)` is an ordinary method call on some object and is
// not this.
func isBareEvalCall(node *ast.Node) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	callee := Unwrapped(node.AsCallExpression().Expression)
	return ast.IsIdentifier(callee) && callee.Text() == "eval"
}

// throwCarryingStatement is the THROW half of the impossibility test
// above, kept as its own named predicate because the DECISION it encodes
// needs its reasoning written down. The reasoning, and where it fails to
// close:
//
// The tempting lowering is "raise the done flag with the result slot
// left alone" — a throw ends the body's run, which is what the flag
// spells. Two things stop it.
//
// First, the flag is what the apply route reads to decide whether a
// path FELL OFF: `allReturned` is "the done flag cannot still be zero",
// and a body that is not allReturned has its answer wrapped
// PossiblyUndefined. Raising the flag on a throwing path therefore
// SUPPRESSES that wrap. For a body like `if (c) throw e; return 1;` the
// route would then claim "this call returns a value in {1}, never
// undefined" — which happens to hold for every run that completes, since
// a throwing run returns nothing at all. So the claim about the RETURN
// is defensible.
//
// Second, and this is what does not close: a throw inside a `try` does
// not end the body — it transfers to the `catch`, whose statements then
// run. Raising the done flag makes every later statement in the block
// conditional on the flag being down, so the catch's own writes become
// invisible to the walk. The caller would then read the state as it
// stood BEFORE the catch ran, which is not a weaker claim but a WRONG
// one. Separating "a throw that leaves the body" from "a throw the
// enclosing try catches" is exactly the structural reading the flag
// cannot spell, and there is no second flag here to spell it with.
//
// So a throw is refused: a `throw` anywhere in a statement declines it,
// and a body carrying one keeps today's decline. That is a coverage
// cost, named here rather than paid for with a claim that is wrong in
// the try case.
//
// (The subtree walk is havocEnumerable's, which calls this at every
// node; this only rules on the node in front of it.)
func throwCarryingStatement(node *ast.Node) bool {
	return ast.IsThrowStatement(node)
}

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

/* ── the call site's havoc ───────────────────────────────────────── */

// OpaqueCallHavoc is the CALL half of the same rule, and it reuses
// exactly the enumerator above: a call whose callee does not resolve, or
// resolves without a blob — a generator, a class method the registry
// declined, `x.y.then(cb)`, an unmodeled library function — lowers as
//
//	target := unknown
//	<every flattened-local leaf mentioned in the receiver or arguments>
//	         := unknown
//
// and nothing else. The callee may compute anything and may mutate any
// object it was handed; both of those are what the two lines above
// claim, and the call's own value is `unknown`, which claims nothing.
//
// `target` is the caller slot the call's value lands in, or -1 where
// nothing reads it (a bare call statement) — a bare call still havocs
// the objects it was handed.
//
// The RECURSION case (summaryCycleHavoc, ir_summary_call.go) is this
// same rule reached down a different path and it stays where it is: it
// answers before this one, and it is the one place that must NOT ask the
// registry for a shape it is standing in for.
func OpaqueCallHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	callExpr := call.AsCallExpression()
	// the same impossibilities the statement route refuses: a bare
	// `eval(…)` writes bindings no syntax names, and an argument carrying
	// a throw or a labelled break is a statement-shaped thing inside an
	// expression (an IIFE body) the enumeration cannot bound
	if !havocEnumerable(call) {
		return nil, false
	}
	// the RECEIVER and every ARGUMENT, through the ONE enumerator: each is
	// handed to the callee, and a flattened local handed out may come back
	// with any leaf moved. Running the statement enumerator over them
	// rather than a mention-only scan of its own is what makes an ARGUMENT
	// that is itself a callback — `xs.forEach(v => { total += v; })`,
	// whose enclosing statement took this route — havoc `total` too: the
	// callee may call the callback, and the callback writes this body's
	// slot.
	slots := map[int]struct{}{}
	for _, part := range append([]*ast.Node{callExpr.Expression}, callArgumentsOf(callExpr)...) {
		if part == nil {
			continue
		}
		partSlots, ok := havocSlotsOfStatement(context, part)
		if !ok {
			return nil, false
		}
		for slot := range partSlots {
			slots[slot] = struct{}{}
		}
	}
	out := havocAssignments(slots)
	// the call's VALUE, where the site has a slot for it. Appended after
	// the leaf havocs so the target's own write is last — it is the
	// statement's answer, and a target that is ALSO a mentioned leaf
	// (`p.a = f(p)`) then keeps one write rather than two.
	if target >= 0 {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: target,
			Effect: unknownEffect,
		})
	}
	NoteFirstHavoc(context, havocCallName(call))
	return out, true
}

// callArgumentsOf is a call's argument list, or nil — the one spelling
// the havoc route needs and the AST states as an optional node list.
func callArgumentsOf(call *ast.CallExpression) []*ast.Node {
	if call.Arguments == nil {
		return nil
	}
	return call.Arguments.Nodes
}

/* ── naming the construct ────────────────────────────────────────── */

// havocCallName spells a havocked CALL for the outcome report: the
// callee's own spelling where it has one ("call fetch",
// "call this.injector.load"), and the bare word otherwise.
func havocCallName(call *ast.Node) string {
	callee := Unwrapped(call.AsCallExpression().Expression)
	if spelled, ok := calleeSpelling(callee); ok {
		return "call " + spelled
	}
	return "call"
}

// calleeSpelling reads a callee expression as a dotted path — `f`,
// `o.m`, `this.injector.load` — for the report. Anything else (a
// computed member, a call's result, a parenthesized function) has no
// one spelling.
func calleeSpelling(node *ast.Node) (string, bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		if !ast.IsIdentifier(access.Name()) {
			return "", false
		}
		steps = append(steps, access.Name().Text())
		current = Unwrapped(access.Expression)
	}
	switch {
	case ast.IsIdentifier(current):
		steps = append(steps, current.Text())
	case current.Kind == ast.KindThisKeyword:
		steps = append(steps, "this")
	default:
		return "", false
	}
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	return strings.Join(steps, "."), true
}

// havocConstructName spells a havocked STATEMENT for the outcome report.
// The words are the source construct's own, not a category: the report
// says "for-in" where a for-in havocked, so a reader can go to the
// syntax the coverage was lost at.
func havocConstructName(statement *ast.Node) string {
	switch {
	case ast.IsForInStatement(statement):
		return "for-in"
	case ast.IsForOfStatement(statement):
		if statement.AsForInOrOfStatement().AwaitModifier != nil {
			return "for await"
		}
		return "for-of"
	case ast.IsForStatement(statement):
		return "for"
	case ast.IsWhileStatement(statement):
		return "while"
	case ast.IsDoStatement(statement):
		return "do-while"
	case ast.IsTryStatement(statement):
		return "try"
	case ast.IsSwitchStatement(statement):
		return "switch"
	case ast.IsIfStatement(statement):
		return "if"
	case ast.IsVariableStatement(statement):
		return "declaration"
	case ast.IsExpressionStatement(statement):
		return havocExpressionName(Unwrapped(statement.AsExpressionStatement().Expression))
	}
	return "statement"
}

// havocExpressionName spells the EXPRESSION an unreadable expression
// statement stands on — the shape the coverage was lost at.
func havocExpressionName(e *ast.Node) string {
	switch {
	case ast.IsCallExpression(e):
		return havocCallName(e)
	case ast.IsElementAccessExpression(e):
		return "computed member"
	case ast.IsAwaitExpression(e):
		return "await"
	case ast.IsNewExpression(e):
		return "new"
	case ast.IsDeleteExpression(e):
		return "delete"
	case ast.IsBinaryExpression(e):
		bin := e.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
			bin.OperatorToken.Kind <= ast.KindLastAssignment {
			if ast.IsElementAccessExpression(Unwrapped(bin.Left)) {
				return "computed member"
			}
			return "assignment"
		}
		return "expression"
	}
	return "expression"
}
