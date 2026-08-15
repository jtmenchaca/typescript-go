// Call HOISTING for the flow IR: a call that sits inside an EXPRESSION
// rather than in a statement shape.
//
// Before this file, a call lowered only where the statement's own shape
// named it — `f(x);`, `x = f(x)`, `const x = f(x)`, `return f(x)`. A call
// ANYWHERE ELSE inside an expression — `return this.a(this.b(x)) + 1`, an
// argument `g(f(x))`, a ternary arm `c ? f(x) : 0` — reached the effect
// grammar's Opaque fallthrough, which has no reading for a call, so the
// whole statement fell to the havoc floor and every slot it could have
// written lost its knowledge.
//
// The fix is the one every compiler uses: the call subexpression becomes
// its own statement, emitted BEFORE the statement that contained it, into
// a fresh TEMP slot; the expression then reads that temp as an ordinary
// var. The call statement built here is byte-for-byte the one
// summaryCallStatement builds — same door, same arity fill, same receiver
// threading — so the hoisted site and a statement-position site of the
// same call lower identically.
//
// TWO GATES stand between a call subexpression and its hoist, and both
// are load-bearing:
//
//	(1) CanHoist — a statement stream must EXIST to hoist into. The
//	    effect readers are shared with the loop solver's body folding
//	    (FoldBody, ir_loop.go), whose effect language has no statement
//	    stream at all: a hoisted statement there would have nowhere to
//	    go and would silently vanish. So the flag is set only where
//	    LowerStatements is the caller.
//
//	(2) the ORDERING gate — hoistingIsOrderSafe below. Real JavaScript
//	    evaluates an expression left to right, and running the call
//	    BEFORE the whole statement is a REORDERING. It is sound only
//	    where the hoisted call cannot write anything the statement reads
//	    or writes at a position the call did not already precede.
package walk

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// HoistCallEffect is the FROZEN seam: a call subexpression lowers to a
// temp-slot call statement appended to context.Hoisted, and the answer is
// the var of that temp — what the surrounding expression reads in the
// call's place.
//
// The call must be a CallExpression (an AWAIT-wrapped one unwraps first
// and hoists identically — the ret-as-inner convention means the callee's
// ret slot already holds the settled value, so `await f(x)` and `f(x)`
// carry the same slot) whose callee resolves with a compiled blob. A
// callee without one is NOT hoisted: the opaque tier's havoc is a
// statement-shaped answer, and the statement that contained the call still
// has its own havoc floor, which covers exactly the same slots.
//
// (zero, false) wherever any gate refuses. The caller then reads on
// exactly as it did before this route existed — for the statement routes
// that means their own decline, and the havoc floor after them.
func HoistCallEffect(context *LoweringContext, call *ast.Node) (kernelbridge.LoopEffect, bool) {
	if context == nil || call == nil {
		return kernelbridge.LoopEffect{}, false
	}
	// (1) a statement stream must exist to hoist into
	if !context.CanHoist {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Allocate == nil {
		return kernelbridge.LoopEffect{}, false
	}
	// `await f(x)` in an expression: the settled value IS the callee's ret
	// slot, so the await peels off and the call hoists exactly as the bare
	// call does
	head := Unwrapped(call)
	if operand, isAwait := AwaitedOperandOf(head); isAwait {
		head = operand
	}
	if !ast.IsCallExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	// ONE CALL SITE, ONE CALL STATEMENT. A reader may reach the same call
	// node several times for one statement — SortOfArg probes an argument
	// through EffectOf and SequenceEffectOf only to learn its sort and
	// throws the effect away, and the statement routes try one reading
	// after another over the same expression. The real run evaluates that
	// site once, so the hoist answers the temp it already allocated rather
	// than allocating another and appending a second call statement.
	if temp, already := context.HoistedTemp[head]; already {
		return varEffect(temp), true
	}
	// the callee must resolve WITH a blob: this route builds a call
	// STATEMENT, and only a blob-backed callee has one to build
	if context.Flow == nil || context.SummaryTable == nil {
		return kernelbridge.LoopEffect{}, false
	}
	callee := summaryCalleeOf(context, head)
	if callee == nil {
		return kernelbridge.LoopEffect{}, false
	}
	if _, has := SummaryBlobFor(context.Flow, callee); !has {
		return kernelbridge.LoopEffect{}, false
	}
	// (2) the ordering gate, asked BEFORE the temp is allocated so a
	// refusal leaves the slot vector exactly as it stood
	if !hoistingIsOrderSafe(context, head, callee) {
		return kernelbridge.LoopEffect{}, false
	}
	// the temp wears the callee's RESOLVED RETURN SORT. What lands in it is
	// the callee's ret out-state verbatim (summaryCallStatement threads
	// `rets[outIndex] = target`), so the sort the temp may wear is the sort
	// of the value that call returns — which the host's own resolved type
	// at the call states, under exactly the masking LocalSortResolved
	// applies to `const x = f()`. The two readings are the same masking on
	// the same type, so a hoisted `f(x)` and a `const x = f(x)` of the same
	// callee sort identically.
	//
	// WHY NOT THE SUMMARY. A serving callee's LoweredSummary carries where
	// its ret slot sits and no sort for it — the shape has RetIndex and no
	// ret sort field at all — so the type is the only authority that
	// speaks here. Nothing is being preferred over a summary reading; there
	// is none to prefer.
	//
	// THE TYPE IS READ AT THE ORIGINAL NODE, before the await peel. The
	// temp holds the SETTLED value (the ret-as-inner convention), and the
	// checker resolves an await expression to its awaited type — so
	// `await g(y)` on an `async function g(): Promise<number>` sorts the
	// temp `number`, while the bare call node would have resolved
	// `Promise<number>` and sorted it unknown.
	//
	// THE TRUST GRADE. A resolved return type is the ANNOTATION'S claim,
	// carrying the same grade every declared type in this walk carries: a
	// parameter's sort (declaredParamSort), a field's (annotationSort), a
	// getter's temp (accessorReturnEvidence) are each the declaration's own
	// word, and this is that word for a return. A callee whose annotation
	// LIES about what it returns is the established annotation boundary
	// (TRUST.md) and not a new one this opens — the same lie already
	// mis-sorts `const x: number = f()` and every parameter bound from it.
	// A type the masking does not spell — a union across sorts, `any`,
	// `unknown`, an object, an unresolved Promise — stays unknown, which
	// admits only the definedness test: that loses coverage and never
	// soundness, and it is where every callee sat before this reading.
	sort, typeofTag := ResolvedExpressionSort(hoistCheckerOf(context), Unwrapped(call))
	temp, allocated := context.Allocate(hoistedTempName(head), sort, typeofTag)
	if !allocated {
		return kernelbridge.LoopEffect{}, false
	}
	statement, built := summaryCallStatement(context, head, temp)
	if !built {
		// the temp is already allocated and stays in the slot vector: it is
		// never written and never read, so it holds its entry state for the
		// whole walk and claims nothing. Rolling it back would mean shrinking
		// vectors another lowering may already hold a slice header of.
		return kernelbridge.LoopEffect{}, false
	}
	context.Hoisted = append(context.Hoisted, statement)
	if context.HoistedTemp == nil {
		context.HoistedTemp = map[*ast.Node]int{}
	}
	context.HoistedTemp[head] = temp
	return varEffect(temp), true
}

// hoistCheckerOf is the nil-tolerant reach for the host checker the
// temp's sort is resolved against. A lowering that runs without a
// program has none, and its temps then wear the unknown sort — exactly
// the behaviour every hoist had before the sort was read.
func hoistCheckerOf(context *LoweringContext) *checker.Checker {
	if context == nil || context.Flow == nil || context.Flow.P == nil {
		return nil
	}
	return context.Flow.P.Checker
}

// hoistedTempName spells a hoisted call's temp slot. The spelling is
// deliberately one no source name can collide with — a `#` prefix, the
// same convention the inlining route's "#in<n>:<name>" slots and the
// body's own "#done"/"#ret" use — so a hoisted temp is never resolvable
// by IndexOf from any expression the source wrote.
func hoistedTempName(call *ast.Node) string {
	spelled, ok := calleeSpelling(Unwrapped(call.AsCallExpression().Expression))
	if !ok {
		spelled = "call"
	}
	hoistSite++
	return fmt.Sprintf("#hoist%d:%s", hoistSite, spelled)
}

// hoistSite numbers hoisted temps so two hoists of the same callee in one
// body take different slots — the counter the inlining route's inlineSite
// is, for the same reason.
var hoistSite = 0

/* ── the ordering gate ───────────────────────────────────────────── */

// hoistingIsOrderSafe is the soundness rule this whole file turns on.
//
// THE PROBLEM. JavaScript evaluates an expression left to right, and every
// side effect lands at the position where its subexpression sits. Hoisting
// moves the call's effects to BEFORE the whole statement. For
//
//	total = count + this.bump();
//
// the real run reads `count` FIRST and then runs bump; the hoisted run
// runs bump first and then reads `count`. If bump writes `count`, the two
// runs disagree — the hoisted lowering would read the POST-call value
// where the real one read the pre-call value. That is not a weaker claim,
// it is a WRONG one, so it must not be lowered.
//
// THE RULE. The hoist is admitted only where the call's WRITE SET is
// disjoint from every OTHER slot the statement mentions. Both sides are
// enumerated, never guessed:
//
//   - the call's write set is exactly what its call statement writes,
//     which is what Rets names: the temp (fresh by construction, so it can
//     collide with nothing) plus the caller slots a WRITTEN bundle entry
//     rides back into. Those come from the callee's own LoweredSummary
//     BundleEntries — the census's Written rows, resolved to caller slots
//     by the same receiver-path spelling bundleRetsAndArgs resolves them
//     by, so the gate and the statement builder can never disagree about
//     which slots a call writes.
//
//   - the statement's slot mentions are havocSlotsOfStatement's — the ONE
//     enumerator the havoc floor uses. It over-approximates (a mention is
//     any identifier occurrence, in any position), which is the safe
//     direction here: the gate may refuse a hoist that would have been
//     fine, and refusing costs only precision, since the statement then
//     takes its ordinary route and the havoc floor after it.
//
// WHY THE CALL'S OWN SUBTREE IS EXCLUDED. The statement's mention set
// includes the call's own arguments and receiver, and those are slots the
// call statement itself reads — reading them at the hoist position is
// exactly what the real run does, since the call's own operands are
// evaluated at the call's own position. So the mentions inside the call
// being hoisted are subtracted before the intersection; only what the
// statement mentions AROUND the call is a reordering risk.
//
// WHAT THE RULE DOES NOT COVER, and does not need to. A callee that writes
// through anything but a bundle write-back — module state, a global, an
// object the caller never flattened — writes nothing this walk carries, so
// no slot's knowledge can be falsified by it. That is the same fact the
// opaque-call floor rests on, and the same fact that lets a scalar
// argument pass by value.
//
// UNRESOLVABLE ENUMERATION. Where havocSlotsOfStatement cannot bound the
// statement's mentions the gate REFUSES: a set it could not enumerate is
// not a set it can prove disjoint from anything.
func hoistingIsOrderSafe(context *LoweringContext, call *ast.Node, callee *ast.Node) bool {
	// with no statement in hand there is nothing to be ordered against, and
	// the gate cannot be checked — so it refuses
	if context.HoistStatement == nil {
		return false
	}
	writes := hoistedCallWriteSlots(context, call, callee)
	if len(writes) == 0 {
		// the call writes nothing the caller carries: no reordering of the
		// statement's reads can observe it
		return true
	}
	// the enumerability refusal rides on the full-statement scan; the
	// intersection itself runs against the mentions OUTSIDE the call's
	// own subtree. Exempting by slot identity is wrong: the receiver
	// path `this` expands to every field, so a call writing this.count
	// would exempt the very slot the statement reads around it. A
	// mention is safe only when every occurrence sits INSIDE the call —
	// then the whole call moves with its own operands and nothing
	// observes the order.
	if _, enumerable := statementSlotMentions(context, context.HoistStatement); !enumerable {
		return false
	}
	outside := slotMentionsOutside(context, context.HoistStatement, call)
	for slot := range writes {
		if _, mentioned := outside[slot]; mentioned {
			return false
		}
	}
	return true
}

// slotMentionsOutside is the widened mention scan with the hoisted
// call's OWN subtree excluded: identifiers and dotted paths, each
// contributing its slot and its bundle leaves, everywhere in the
// statement EXCEPT under the call being hoisted. What remains is
// exactly what the statement reads or writes around the call — the
// only occurrences a reorder could be observed through.
func slotMentionsOutside(context *LoweringContext, statement *ast.Node, exclude *ast.Node) map[int]struct{} {
	slots := map[int]struct{}{}
	noteName := func(name string) {
		if slot, held := slotIndexOfName(context, name); held {
			slots[slot] = struct{}{}
		}
		for _, slot := range flattenedSlotsUnder(context, name) {
			slots[slot] = struct{}{}
		}
	}
	var visit func(current *ast.Node) bool
	visit = func(current *ast.Node) bool {
		if current == exclude {
			return false
		}
		if ast.IsPropertyAccessExpression(current) {
			if path, spelled := dottedPathOf(current); spelled {
				noteName(path)
			}
		}
		if ast.IsIdentifier(current) {
			noteName(current.Text())
		}
		current.ForEachChild(visit)
		return false
	}
	visit(statement)
	return slots
}

// (hoistedCallOwnReads lived here: an exemption set for the ordering
// gate, keyed by SLOT identity. It exempted the receiver path's whole
// bundle, so a call writing this.count exempted the very slot the
// statement read around it. The gate now excludes the call's SUBTREE
// from the mention scan instead — slotMentionsOutside — which is the
// occurrence-level reading the exemption was reaching for.)

// statementSlotMentions is every slot a piece of syntax touches, for the
// ordering gate: the havoc enumerator's own set, WIDENED by the dotted
// paths it structurally cannot see.
//
// The widening is not an improvement on the enumerator — it is the same
// gap receiverBundleHavocSlots exists for, reached from another direction.
// havocSlotsOfStatement finds a mentioned local by IDENTIFIER, and a
// bundle slot spelled "this.count" has no identifier at its root: `this`
// is a keyword, so `this.count + this.bump()` walks that enumerator and
// contributes NOTHING for this.count. For the havoc floor that gap is
// covered at the call site; for the ordering gate it would be a hole in
// the one thing the gate must be right about, since the gate's whole job
// is to notice that the statement reads a slot the call writes.
//
// So every PROPERTY ACCESS PATH in the syntax is spelled out (dottedPathOf
// — the same reader the receiver threading uses) and its slot, where the
// caller has one, joins the set. Over-approximating is the safe direction:
// a mention the gate adds can only cause it to REFUSE a hoist, and a
// refused hoist costs precision, never soundness.
func statementSlotMentions(context *LoweringContext, node *ast.Node) (map[int]struct{}, bool) {
	slots, enumerable := havocSlotsOfStatement(context, node)
	if !enumerable {
		return nil, false
	}
	var visit func(current *ast.Node) bool
	visit = func(current *ast.Node) bool {
		if ast.IsPropertyAccessExpression(current) {
			if path, spelled := dottedPathOf(current); spelled {
				if slot, held := slotIndexOfName(context, path); held {
					slots[slot] = struct{}{}
				}
				// a path that names a flattened BUNDLE rather than a leaf
				// contributes every leaf under it
				for _, slot := range flattenedSlotsUnder(context, path) {
					slots[slot] = struct{}{}
				}
			}
		}
		if current.Kind == ast.KindThisKeyword {
			for _, slot := range flattenedSlotsUnder(context, "this") {
				slots[slot] = struct{}{}
			}
		}
		current.ForEachChild(visit)
		return false
	}
	visit(node)
	return slots, true
}

// hoistedCallWriteSlots is the caller slots a hoisted call STATEMENT
// writes, other than its own fresh temp: one per WRITTEN this-field bundle
// entry the receiver's path resolves to a caller slot.
//
// The reading is bundleRetsAndArgs' own, restated here because the gate
// must answer BEFORE the statement is built (a refusal must leave no temp
// and no statement behind). The two readings share receiverPathOf and
// slotIndexOfName, so a drift between them would have to be a drift in
// those, not between two spellings of the same rule.
//
// A callee whose lowering is not readable answers the EMPTY set together
// with a refusal upstream: summaryCallStatement would decline such a
// callee too, so the hoist never gets that far.
func hoistedCallWriteSlots(context *LoweringContext, call *ast.Node, callee *ast.Node) map[int]struct{} {
	writes := map[int]struct{}{}
	shape, known := LowerSummaryBody(context.Flow, callee)
	if !known {
		return writes
	}
	receiverPath, pathOk := receiverPathOf(call)
	if !pathOk {
		// no receiver path: the callee has no this-entries to write back
		// through, or the statement builder will decline the site outright
		return writes
	}
	for _, entry := range shape.BundleEntries {
		if !entry.Written || !strings.HasPrefix(entry.Path, "this.") {
			continue
		}
		field := strings.TrimPrefix(entry.Path, "this.")
		if slot, held := slotIndexOfName(context, receiverPath+"."+field); held {
			writes[slot] = struct{}{}
		}
	}
	return writes
}

/* ── the flush ───────────────────────────────────────────────────── */

// TakeHoisted is the statement stream's half of the seam: the hoists
// accumulated since the last take, emptied out of the context.
//
// A statement route calls this AFTER its own readers have run and BEFORE
// it appends its own statements, so the temps are written before the
// statement that reads them — which is the whole point of the hoist.
func TakeHoisted(context *LoweringContext) []kernelbridge.IrStatement {
	if context == nil {
		return nil
	}
	// the per-node memo belongs to ONE statement: the next statement's own
	// occurrences of the same call node are its own call, so the map goes
	// out with the accumulation
	context.HoistedTemp = nil
	if len(context.Hoisted) == 0 {
		return nil
	}
	taken := context.Hoisted
	context.Hoisted = nil
	return taken
}

// DropHoistedFrom discards every hoist accumulated past a mark — what a
// statement route calls when its own reading DECLINED, so a half-read
// statement leaves no call statements behind.
//
// WHY THIS IS THE CLEAN VERSION, and what leaking would actually cost.
// Leaking a declined statement's hoists would not be UNSOUND: a hoisted
// call statement is the callee's own compiled program applied to argument
// effects the caller really does evaluate, and its only writes are a fresh
// temp nothing reads and bundle write-backs the ordering gate already
// proved disjoint from the statement's other slots. Running it and then
// havocking the statement's slots on top computes a state no weaker than
// havocking alone.
//
// It would be WASTEFUL and it would be CONFUSING. Wasteful: a call
// statement the kernel splices costs a summary application, and the
// statement whose expression needed it just lost its reading, so nothing
// will ever read the temp. Confusing: the havoc floor's whole contract is
// "this statement lowered to assignments of unknown, and nothing else",
// and a stray call statement ahead of it makes the lowering of a declined
// statement depend on how far its reading got before declining — the same
// source in the same context could lower two ways. So the mark is taken
// before each statement's routes run and the accumulation is truncated
// back to it on every decline.
func DropHoistedFrom(context *LoweringContext, mark int) {
	if context == nil {
		return
	}
	if mark < 0 || mark > len(context.Hoisted) {
		return
	}
	context.Hoisted = context.Hoisted[:mark]
	// the memo goes with them: a node whose call statement was just
	// truncated away must hoist AGAIN for whichever route reads it next,
	// or that route would answer a temp no statement ever writes — which
	// reads as the temp's entry state, a WRONG answer rather than a weak
	// one. Clearing the whole map is right because the mark is always the
	// statement's own start (LowerStatements takes it once per statement),
	// so everything in the map was accumulated past it.
	if mark == 0 {
		context.HoistedTemp = nil
	}
}

// HoistedMark is the length of the accumulation, taken before a
// statement's routes run so a decline can truncate back to it.
func HoistedMark(context *LoweringContext) int {
	if context == nil {
		return 0
	}
	return len(context.Hoisted)
}
