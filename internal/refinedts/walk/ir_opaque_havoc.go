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
//   - a break or continue that LEAVES the enumerated statement — a
//     labelled one whose label is declared outside it, or a bare one
//     with no enclosing switch or loop inside it. Control then goes
//     somewhere the havoc's own statement position does not reach. A
//     CONTAINED break or continue is admitted: it cannot leave the
//     statement, and the havoc of the whole statement already covers
//     every path through it (containedTransfer below);
//   - a `return` — a havoc writes slots and then falls through, but a
//     return raises the done flag and stops the block. Havocking a
//     statement that contains one would drop the raise, and a later
//     return would then overwrite the result slot the swallowed one
//     wrote — a WRONG answer about the returned value, not a weak one;
//   - a `throw` — see throwCarryingStatement below for the reasoning.
//
// A statement whose own route is the OPAQUE RETURN or the escaping
// THROW never reaches this floor at all: the lowering has an exact
// control-flow shape for each (lowering_to_kernel_ir.go), and this
// floor's refusal of both is what routes them there.

package walk

import (
	"sort"
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── naming a decline ────────────────────────────────────────────── */

// The lowering's decline reasons, one per lowering run, keyed by the
// context the run carries. A body that declines answers a bool and
// nothing else — the signature every route and every caller is built
// around — so the NAME of what it refused rides here beside it.
//
// Why this and not a context field: the field would say the same thing,
// and this says it without the whole package's contexts changing shape.
// Keyed by POINTER, which is the identity a lowering run has: nested
// LowerStatements calls for an if's arms share the caller's context, so
// an arm's refusal names the body's refusal, which is what the report
// wants. First-wins, exactly as FirstHavoc is, so the name points at
// the earliest place the body could not be read.
var (
	declinedConstructsLock sync.Mutex
	declinedConstructs     = map[*LoweringContext]string{}
	// how many LowerStatements runs are in flight on this context. The
	// OUTERMOST one owns the name: it clears any name left behind before
	// it starts, and drops the name on the way out when it SUCCEEDED —
	// an inner arm may have declined and been stood in for by the havoc
	// floor, and a body that lowered has no decline to report.
	loweringDepth = map[*LoweringContext]int{}
)

// EnterLoweringRun marks a LowerStatements run beginning on a context and
// answers whether it is the OUTERMOST one. The outermost run starts with
// a clean slate, so a name left by an earlier lowering of the same
// context cannot be read as this one's.
func EnterLoweringRun(context *LoweringContext) bool {
	if context == nil {
		return false
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	depth := loweringDepth[context]
	loweringDepth[context] = depth + 1
	if depth == 0 {
		delete(declinedConstructs, context)
		return true
	}
	return false
}

// LeaveLoweringRun marks a run finished. The outermost run that
// SUCCEEDED drops the name: an inner arm's decline that the havoc floor
// stood in for is not the body's decline, and the body lowered.
func LeaveLoweringRun(context *LoweringContext, succeeded bool) {
	if context == nil {
		return
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	depth := loweringDepth[context] - 1
	if depth <= 0 {
		delete(loweringDepth, context)
		if succeeded {
			delete(declinedConstructs, context)
		}
		return
	}
	loweringDepth[context] = depth
}

// NoteDeclinedConstruct records WHAT a lowering run refused, first-wins.
// Every decline that has a name for its construct calls this on the way
// out; the body-level owner reads it with DeclinedConstructOf and puts
// it in the outcome report, so a histogram row names syntax someone can
// act on rather than "a statement the lowering does not read".
func NoteDeclinedConstruct(context *LoweringContext, construct string) {
	if context == nil || construct == "" {
		return
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	if _, held := declinedConstructs[context]; held {
		return
	}
	declinedConstructs[context] = construct
}

// DeclinedConstructOf is the name a declining lowering left behind, and
// it CLEARS it: one lowering run, one read. Empty where the run declined
// with no name of its own, and the caller then says what it knows.
func DeclinedConstructOf(context *LoweringContext) string {
	if context == nil {
		return ""
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	held := declinedConstructs[context]
	delete(declinedConstructs, context)
	return held
}

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
//   - a break or continue that LEAVES this statement: the transfer goes
//     to a structure outside the subtree being enumerated, so the exit
//     the kernel walks to is not the exit the run reaches. The whole
//     rule is containment, and containedTransfer decides it — a break
//     whose switch or loop sits INSIDE the statement is admitted,
//     because it cannot leave and the havoc of the whole statement
//     already covers every path through it.
//   - a `return`: the havoc's own shape is "write these slots, then carry
//     on", and a return does not carry on — it writes the result slot,
//     raises the done flag, and ends the block. A havoc that swallowed
//     one would leave the flag down, every statement after the havocked
//     one would be walked as if it had run, and a later return would
//     overwrite the result slot the swallowed one wrote: a WRONG answer
//     about the returned value. Raising the flag instead is not
//     available either: the havoc cannot say WHICH of its paths
//     returned, so the flag would be raised on all of them. (A `return`
//     in STATEMENT position never arrives here — the lowering's own
//     opaque-return route holds the exact control shape for it.)
//   - a `throw`: throwCarryingStatement.
//
// A nested FUNCTION's own returns, throws and transfers are its
// business, not this statement's, so the scan stops at a function
// boundary — an unreadable statement holding a callback is enumerable
// exactly as one holding any other expression is.
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
			if !containedTransfer(node, statement) {
				enumerable = false
			}
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

// containedTransfer is whether a `break` or `continue` stays INSIDE the
// statement being enumerated. This is the whole rule, and it is the one
// the refusal is actually about:
//
// The havoc stands where the statement stood and writes every slot the
// statement could have written. A transfer that stays inside the
// statement is a path THROUGH it — the run leaves the statement at the
// statement's own exit, which is exactly where the havoc's writes sit,
// and the slot enumeration already unioned every block the transfer
// could have skipped or repeated. So the havoc covers it.
//
// A transfer that LEAVES the statement is the refusal: the run departs
// from somewhere in the middle for a target outside, and the kernel
// walks straight on past the havoc as though the statement had run to
// completion. The exit would then claim the havoc's writes happened on
// a path control had already left.
//
// Two shapes leave:
//
//   - a BARE break or continue with no enclosing switch or loop inside
//     the subtree — its target is an enclosing loop or switch of the
//     BODY, outside what is being havocked;
//   - a LABELLED break or continue whose labelled statement is not
//     inside the subtree — same thing, reached by name.
//
// Both walks are syntactic: from the transfer up through Parent to the
// subtree root. A parent chain that does not pass through the root at
// all (a caller handing this a detached node) answers false — the safe
// direction, since the refusal only ever costs coverage.
func containedTransfer(transfer *ast.Node, root *ast.Node) bool {
	if transfer == nil || root == nil {
		return false
	}
	label := transferLabelOf(transfer)
	isContinue := ast.IsContinueStatement(transfer)
	for node := transfer.Parent; node != nil; node = node.Parent {
		if label != "" {
			// the labelled statement this transfer names, found inside the
			// subtree: the target is contained
			if ast.IsLabeledStatement(node) &&
				node.AsLabeledStatement().Label.Text() == label {
				return true
			}
		} else if breakTargetOf(node, isContinue) {
			// the nearest switch or loop this bare transfer leaves, found
			// inside the subtree
			return true
		}
		if node == root {
			// the subtree ended before any target did
			return false
		}
		// a nested function boundary means this transfer was never this
		// statement's to begin with; the scan above stops at one, so
		// reaching it here is a caller passing an inner node directly
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
}

// transferLabelOf is the label a `break`/`continue` names, or "" for a
// bare one.
func transferLabelOf(transfer *ast.Node) string {
	if ast.IsBreakStatement(transfer) {
		if label := transfer.AsBreakStatement().Label; label != nil {
			return label.Text()
		}
		return ""
	}
	if ast.IsContinueStatement(transfer) {
		if label := transfer.AsContinueStatement().Label; label != nil {
			return label.Text()
		}
		return ""
	}
	return ""
}

// breakTargetOf is whether a node is what a BARE transfer leaves: a
// `continue` leaves the nearest loop only, a `break` leaves the nearest
// loop OR switch — the language's own rule.
func breakTargetOf(node *ast.Node, isContinue bool) bool {
	switch {
	case ast.IsForStatement(node), ast.IsForInStatement(node),
		ast.IsForOfStatement(node), ast.IsWhileStatement(node),
		ast.IsDoStatement(node):
		return true
	case ast.IsSwitchStatement(node):
		return !isContinue
	}
	return false
}

// DeclinedHavocConstruct names WHY the floor refused a statement, in the
// statement's own syntax rather than as a category. The coverage
// histogram is the work queue, so a row has to name something a reader
// can go and act on: "throw inside try" and "labelled break crossing
// out" are worth having, "a statement the lowering does not read" is
// not.
//
// The scan is havocEnumerable's, run again to find WHICH node refused —
// the predicate answers a bool because that is what the route needs, and
// this answers the name because that is what the report needs. Empty
// where nothing refuses (the caller then names its own reason).
func DeclinedHavocConstruct(statement *ast.Node) string {
	if statement == nil {
		return ""
	}
	reason := ""
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if reason != "" {
			return true
		}
		switch {
		case node.Kind == ast.KindWithStatement:
			reason = "with statement"
			return true
		case ast.IsBreakStatement(node), ast.IsContinueStatement(node):
			if !containedTransfer(node, statement) {
				reason = transferDeclineName(node)
			}
			return true
		case ast.IsReturnStatement(node):
			reason = "return inside " + havocConstructName(statement)
			return true
		case ast.IsThrowStatement(node):
			reason = "throw inside " + havocConstructName(statement)
			if throwInsideTry(node, statement) {
				reason = "throw inside try"
			}
			return true
		case isBareEvalCall(node):
			reason = "eval call"
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return reason
}

// transferDeclineName spells a break or continue that leaves the
// statement: labelled or bare, break or continue, each said as the
// construct it is.
func transferDeclineName(transfer *ast.Node) string {
	word := "break"
	if ast.IsContinueStatement(transfer) {
		word = "continue"
	}
	if transferLabelOf(transfer) != "" {
		return "labeled " + word + " crossing out"
	}
	return word + " crossing out"
}

// throwInsideTry is whether a throw sits lexically inside a `try` within
// the subtree — the case whose decline the reasoning in
// throwCarryingStatement holds, and the one the report names by that
// name so it reads as the construct it is.
func throwInsideTry(throw *ast.Node, root *ast.Node) bool {
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			return true
		}
		if node == root {
			return ast.IsTryStatement(root)
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
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
// So a throw is refused HERE: a `throw` anywhere in a statement this
// floor is standing in for declines it.
//
// WHAT CLOSED SINCE. The second objection is about the TRY, and it is
// structural — so read the structure. A `throw` in STATEMENT position
// whose parent chain up to the lowered body's root passes through no
// `try` cannot transfer to a catch of THIS body: it leaves the body
// outright. For that throw the first paragraph's reasoning is the whole
// story, and it closes — a throwing run returns NOTHING, so no claim
// about the returned outcome can be wrong about it. The lowering gives
// it the exact shape of a return of nothing:
//
//	#ret := AbsentConst()   (a run that threw returned no value)
//	#done := {1}            (and the block ends here)
//
// which is weaker than the truth (the run produced no outcome at all,
// not an absent one) and never stronger. That route lives in
// lowering_to_kernel_ir.go; a throw INSIDE a try keeps this decline,
// under the catch-invisibility reasoning above, and the report names it
// "throw inside try" rather than as a category.
//
// This predicate is unchanged: the floor still refuses every throw it
// meets, because a havoc STANDS IN FOR a statement and a statement
// standing in for a throw would have to spell the transfer, which is
// what it cannot do. Only the statement-position route above it
// changed.
//
// (The subtree walk is havocEnumerable's, which calls this at every
// node; this only rules on the node in front of it.)
func throwCarryingStatement(node *ast.Node) bool {
	return ast.IsThrowStatement(node)
}

// StatementRunsCode answers whether a statement's subtree can execute
// a callee — a call, a construction, an await, a yield, a tagged
// template. The capture-havoc bracketing reads it: any such execution
// may run a stored closure. (A getter behind a plain property read
// still runs code this test does not see — the standing gap every
// syntactic write/call test in this package accepts.)
func StatementRunsCode(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindCallExpression, ast.KindNewExpression, ast.KindAwaitExpression,
		ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return true
	}
	runs := false
	node.ForEachChild(func(child *ast.Node) bool {
		if runs {
			return true
		}
		if StatementRunsCode(child) {
			runs = true
			return true
		}
		return false
	})
	return runs
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
	case ast.IsLabeledStatement(statement):
		return "labeled statement"
	case ast.IsThrowStatement(statement):
		return "throw"
	case ast.IsReturnStatement(statement):
		return OpaqueReturnName(statement)
	case ast.IsExpressionStatement(statement):
		return havocExpressionName(Unwrapped(statement.AsExpressionStatement().Expression))
	}
	return "statement"
}

// OpaqueReturnName spells a return whose VALUE no reading lowered:
// "return (call this.x.y)", "return (object literal)" — the word
// `return`, then the returned expression's own syntax in parentheses,
// so the histogram row names the shape a reader can go and build a
// reading for. A bare `return` (which always lowers) spells itself.
//
// The value is what was lost; the control flow was not. The name says
// only "return", never "return declined", because the statement did
// lower — porously.
func OpaqueReturnName(statement *ast.Node) string {
	if statement == nil || !ast.IsReturnStatement(statement) {
		return "return"
	}
	expression := statement.AsReturnStatement().Expression
	if expression == nil {
		return "return"
	}
	return "return (" + returnedShapeName(Unwrapped(expression)) + ")"
}

// returnedShapeName is the returned expression's own syntax, spelled
// plainly. A CALL keeps the callee's dotted path, which is the one
// spelling that tells a reader which callee to teach the lowering
// about; everything else says what kind of expression it is.
func returnedShapeName(e *ast.Node) string {
	if e == nil {
		return "expression"
	}
	switch {
	case ast.IsCallExpression(e):
		return havocCallName(e)
	case ast.IsAwaitExpression(e):
		return "await " + returnedShapeName(Unwrapped(e.AsAwaitExpression().Expression))
	case ast.IsObjectLiteralExpression(e):
		return "object literal"
	case ast.IsArrayLiteralExpression(e):
		return "array literal"
	case ast.IsNewExpression(e):
		return "new"
	case ast.IsElementAccessExpression(e):
		return "computed member"
	case ast.IsPropertyAccessExpression(e):
		if spelled, ok := calleeSpelling(e); ok {
			return "member " + spelled
		}
		return "member"
	case ast.IsIdentifier(e):
		return "name " + e.Text()
	case ast.IsTaggedTemplateExpression(e):
		return "tagged template"
	case ast.IsTemplateExpression(e):
		return "template"
	case ast.IsConditionalExpression(e):
		return "conditional"
	case ast.IsBinaryExpression(e):
		return "binary"
	case ast.IsFunctionLike(e):
		return "function"
	}
	return "expression"
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
