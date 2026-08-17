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
	// SummaryBlobFor's own "Ok" answers COMPILE success only — a body
	// that lowered whole but havocked some construct along the way
	// (summary_outcome.go's SummaryPorous) still compiles a blob, and
	// "Porous blobs still count coverage; they no longer answer calls"
	// (kernel_summaries.go's applySummary, the TOP-LEVEL serving rule)
	// is exactly the guard a NESTED call site needs too: hoisting a
	// call into a porous callee's blob would splice that callee's own
	// weakened (possibly TOP) ret into THIS body's statements while
	// this body's own lowering still reports itself complete — the
	// composed answer is porous, but nothing said so. Requiring the
	// callee's own SummaryOutcomeOf to have settled COMPLETE before
	// hoisting is the same rule applySummary already enforces one call
	// up, moved to where a callee's blob gets EMBEDDED rather than
	// SERVED.
	if outcome, _, recorded := SummaryOutcomeOf(callee); !recorded || outcome != SummaryComplete {
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
