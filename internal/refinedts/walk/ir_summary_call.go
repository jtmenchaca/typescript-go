// Call statements for the flow IR: a call to a callee that already has
// a COMPILED SUMMARY lowers to IrStatementCall rather than inlining the
// callee's body into fresh slots.
//
// The two routes differ in where the work happens. Inlining (see
// ir_inline_call.go) copies the callee's statements into the caller's
// slot vector at every site — the slots grow, the lowering repeats, and
// the callee's body is walked again inside every caller. A summary is
// compiled ONCE, kernel-side, and applied: the call statement carries
// the argument effects in and names where the out-states land, and the
// kernel splices the compiled program. The summary quantifies over all
// entries, so one compile serves every call.
//
// Which route a site takes is decided here: a callee whose declaration
// the registry answers for takes the summary route; everything else
// falls through to the existing inlining, exactly as before.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// (The lowered-body type is LoweredSummary, kernel_summaries.go — one
// type serves the memoized door and the worker.)

// summaryCalleeOf resolves a call expression's callee to the
// declaration the registry keys by, or nil. The lowering context's own
// ResolveCallee is the resolution — the same one the inlining route
// uses, so the two routes never disagree about which declaration a name
// stands for.
func summaryCalleeOf(context *LoweringContext, call *ast.Node) *ast.Node {
	if context.ResolveCallee == nil {
		return nil
	}
	if ast.IsNewExpression(call) {
		return constructorDeclarationOf(context, call.AsNewExpression().Expression)
	}
	if !ast.IsCallExpression(call) {
		return nil
	}
	return context.ResolveCallee(call.AsCallExpression().Expression)
}

// constructorDeclarationOf resolves `new X(...)`'s X — a plain
// identifier, through import aliases — to the class's CONSTRUCTOR
// declaration, the node the summary registry keys the constructor's
// blob by. A class with no constructor body, a declaration-file class,
// and every non-identifier callee answer nil.
func constructorDeclarationOf(context *LoweringContext, callee *ast.Node) *ast.Node {
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil
	}
	core := Unwrapped(callee)
	if core == nil || !ast.IsIdentifier(core) {
		return nil
	}
	symbol := symbolAt(context.Flow.P.Checker, core)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	classLike := symbol.ValueDeclaration
	// `const C = class { … }` binds the NAME to a variable, so the
	// symbol's value declaration is the VariableDeclaration — the class
	// expression sits in its initializer. A `const` binding holds that
	// one class for its whole life, so the expression reads exactly as
	// a declaration does; a `let`/`var` may hold a different
	// constructor by the time the `new` runs, and stays unresolved
	// here. Mirrors the same unwrap in evaluate_new_expression.go.
	if !ast.IsClassLike(classLike) && ast.IsVariableDeclaration(classLike) &&
		classLike.Parent != nil && (classLike.Parent.Flags&ast.NodeFlagsConst) != 0 {
		if initializer := classLike.AsVariableDeclaration().Initializer; initializer != nil {
			if unwrapped := Unwrapped(initializer); unwrapped != nil && ast.IsClassExpression(unwrapped) {
				classLike = unwrapped
			}
		}
	}
	// class-LIKE: a `const C = class { … }` expression constructs
	// exactly as a declaration does
	if !ast.IsClassLike(classLike) || ast.GetSourceFileOfNode(classLike).IsDeclarationFile {
		return nil
	}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if ast.IsConstructorDeclaration(member) && member.Body() != nil {
			return member
		}
	}
	return nil
}

// SummaryCallOrHavoc is the one call-lowering door every route uses,
// in three tiers:
//
//  1. a callee with a compiled summary → the CALL STATEMENT, which
//     splices the callee's own compiled program;
//  2. a callee whose own build is STILL IN FLIGHT — a recursive call,
//     which cannot splice itself → the cycle havoc, which is the one
//     tier that must never ask the registry for a shape;
//  3. every OTHER callee — one that does not resolve at all, or
//     resolves with no blob (a generator, a declined body, an
//     unmodeled library function, `x.y.then(cb)`) → the OPAQUE CALL
//     HAVOC (ir_opaque_havoc.go): the target takes `unknown` and so
//     does every flattened-local leaf the receiver or the arguments
//     mentioned.
//
// Tier 3 is what stops an unreadable call from declining the whole body.
// It computes its slot set through the SAME enumerator the statement
// floor uses, so "which slots could this have written" is answered once
// in the adapter and never twice differently.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads.
func SummaryCallOrHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	return SummaryCallOrHavocNamed(context, call, target, "")
}

// SummaryCallOrHavocNamed is SummaryCallOrHavoc with the FIRST-HAVOC NAME
// a caller may override: where `construct` is non-empty, every havoc
// tier below records THAT name instead of deriving "call <callee>" from
// the call's own syntax. This is for a call that sits ahead of every
// body statement — a defaulted parameter's own initializer — where the
// call is not itself the construct the outcome report should name; the
// construct RUNNING the call is. An empty `construct` is
// SummaryCallOrHavoc's own behavior, unchanged.
func SummaryCallOrHavocNamed(context *LoweringContext, call *ast.Node, target int, construct string) ([]kernelbridge.IrStatement, bool) {
	// a `this.<field>.<method>` call on a once-assigned Map/Set field has
	// no resolvable callee at all (summaryCalleeOf would decline it below
	// regardless), so this recognizer is tried first — it costs nothing on
	// every call this shape does not match, and turns a body that used to
	// go porous at "call this.m.get" into a completed one.
	if statement, ok := thisFieldMapCallStatement(context, call, target); ok {
		return []kernelbridge.IrStatement{statement}, true
	}
	// the module-const twin of the same recognizer: `<name>.has(x)` /
	// `.delete(x)` where name resolves to a top-level `const` initialized
	// to a bare `new Map(…)`/`new Set(…)` and never reassigned in the
	// file (ir_summary_module_set_calls.go). Same cost argument: a
	// module-const receiver has no resolvable FunctionContract either.
	if statement, ok := moduleSetCallStatement(context, call, target); ok {
		return []kernelbridge.IrStatement{statement}, true
	}
	// a NEW takes the blob tier alone: an unresolvable or declining
	// constructor falls back to the caller's own routes and floor, whose
	// havoc enumeration reads calls. The instance value itself has no
	// scalar spelling, so the target takes unknown AFTER the call — the
	// ctor's unwritten ret would read as "undefined", which is a claim
	// and a wrong one.
	if ast.IsNewExpression(call) {
		statement, ok := summaryCallStatement(context, call, -1)
		if !ok {
			return nil, false
		}
		// THE INSTANCE'S FIELDS. The constructor's own summary carries one
		// this-entry per field its body writes, and the exits of those
		// entries ARE the fresh instance's field values — from the caller's
		// side a constructor's `this.f = v` writes are indistinguishable
		// from a `return { f: v }`. Where the caller flattened its target
		// into per-field slots, each written row maps onto the target's own
		// slot for that field and the instance's knowledge survives the
		// call. constructorFieldRets makes exactly those writes.
		statement = constructorFieldRets(context, call, targetNameOf(context, target), statement)
		out := []kernelbridge.IrStatement{statement}
		out = append(out, arrayArgumentPostCallHavoc(context, call)...)
		if target >= 0 {
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
			})
		}
		return out, true
	}
	// an OBJECT-LITERAL METHOD called through its record: methodServes
	// gates the summary tier — a body whose receiver bundle did not
	// expand, or that moves a capture the write census cannot spell, must
	// not serve a summary its this-writes are invisible to — and
	// methodWrites is the capture-write havoc prepended to whichever tier
	// answers the site (LiteralMethodWriteStatements). (nil, true) for
	// every other callee, which keeps every tier exactly as it was.
	methodWrites, methodServes := LiteralMethodWriteStatements(context, call)
	withMethodWrites := func(statements []kernelbridge.IrStatement, ok bool) ([]kernelbridge.IrStatement, bool) {
		if !ok || len(methodWrites) == 0 {
			return statements, ok
		}
		return append(append([]kernelbridge.IrStatement{}, methodWrites...), statements...), true
	}
	if methodServes {
		if statement, ok := summaryCallStatement(context, call, target); ok {
			out := append([]kernelbridge.IrStatement{statement}, arrayArgumentPostCallHavoc(context, call)...)
			return withMethodWrites(out, true)
		}
	}
	if cycled, ok := summaryCycleHavocNamed(context, call, target, construct); ok {
		return withMethodWrites(withReceiverBundleHavoc(context, call, cycled))
	}
	// `cleanup()` — a call through a name this same body bound to a
	// closure. The SERVED route comes first: a closure whose every
	// capture resolves to a caller slot compiles to a summary with one
	// entry per capture, and the call statement fills each from the
	// caller's own slot and maps the written ones back. Where any capture
	// does not resolve, the write-set havoc below is the answer.
	if served, ok := ClosureCallStatementOf(context, call, target); ok {
		return served, true
	}
	if closed, ok := closureCallHavocNamed(context, call, target, construct); ok {
		return withMethodWrites(withReceiverBundleHavoc(context, call, closed))
	}
	// a call through a BARE IMPORTED IDENTIFIER whose declaration this
	// lowering never reads — a React/redux hook, a module function
	// pulled in from elsewhere. Tried here, BELOW every tier above: the
	// blob/summary and closure tiers own a callee that resolves to a
	// contract or a local closure this lowering CAN read, and must keep
	// first refusal on it. Only a callee those tiers already declined on
	// (an import with no reachable body) reaches this recognizer.
	if hooked, ok := importedHookCallStatement(context, call, target); ok {
		return withMethodWrites(hooked, true)
	}
	// the RECEIVER-CALLEE twin of the tier above: `document.getElementById(x)`,
	// `window.addEventListener(name, cb)`, `listenerApi.getState()` — a
	// property-access callee whose METHOD resolves outside this file,
	// proved non-interfering by the same write-and-call-free argument
	// test. Tried at the same position, for the same reason: every tier
	// above owns a callee this lowering can read a body for, and only a
	// callee those tiers already declined on reaches either recognizer.
	if receiverServed, ok := receiverCalleeCallStatement(context, call, target); ok {
		return withMethodWrites(receiverServed, true)
	}
	if construct == "" {
		havocked, ok := OpaqueCallHavoc(context, call, target)
		if !ok {
			return nil, false
		}
		return withMethodWrites(withReceiverBundleHavoc(context, call, havocked))
	}
	havocked, ok := OpaqueCallHavocNamed(context, call, target, construct)
	if !ok {
		return nil, false
	}
	return withMethodWrites(withReceiverBundleHavoc(context, call, havocked))
}

// withReceiverBundleHavoc adds the RECEIVER BUNDLE's slots to a havoc
// answer: an opaque callee may write any field of the object it was
// called on, and the statement enumerator misses a `this`-rooted
// receiver entirely (receiverBundleHavocSlots says why). Each slot not
// already written by the answer takes one `assign slot unknown`,
// appended in slot order ahead of nothing — the answer's own target
// write, where it has one, stays last.
func withReceiverBundleHavoc(
	context *LoweringContext,
	call *ast.Node,
	havocked []kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	slots := receiverBundleHavocSlots(context, call)
	if len(slots) == 0 {
		return havocked, true
	}
	already := map[int]struct{}{}
	for _, statement := range havocked {
		if statement.Kind == kernelbridge.IrStatementAssign {
			already[statement.Target] = struct{}{}
		}
	}
	missing := map[int]struct{}{}
	for _, slot := range slots {
		if _, held := already[slot]; held {
			continue
		}
		if slot < 0 {
			continue
		}
		missing[slot] = struct{}{}
	}
	if len(missing) == 0 {
		return havocked, true
	}
	return append(havocAssignments(missing), havocked...), true
}

// summaryCycleHavoc is the RECURSION floor: a callee that resolves but
// has no blob BECAUSE its own build is in flight (SummaryCycleInFlight)
// still lowers — writing TOP into whatever the call's value lands in is
// sound unconditionally, and it is what the kernel's own walk answers
// for a call through an empty summary. Without this a body containing a
// recursive call would decline whole, and the recursive declaration
// itself would never compile.
//
// The route never asks SummaryOutShapeFor: that would re-enter the
// in-flight lowering it is standing in for.
//
// The havoc SET is the opaque call's own — OpaqueCallHavoc — so the
// recursion floor and the unresolvable-callee floor are literally one
// rule: the target takes `unknown`, and so does every flattened-local
// leaf the receiver or an argument mentioned. (A scalar argument passes
// by value and needs nothing; the enumerator names only flattened
// locals, so the two facts agree by construction.) What is specific to
// this tier is only WHEN it applies, which is the in-flight test below.
func summaryCycleHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	return summaryCycleHavocNamed(context, call, target, "")
}

// summaryCycleHavocNamed is summaryCycleHavoc with the first-havoc name
// SummaryCallOrHavocNamed threads through — empty keeps the derived
// "call <callee>" name, non-empty overrides it, exactly as
// OpaqueCallHavocNamed's own construct parameter does; this tier's havoc
// SET is OpaqueCallHavoc's own, so the name is the only thing to thread.
func summaryCycleHavocNamed(context *LoweringContext, call *ast.Node, target int, construct string) ([]kernelbridge.IrStatement, bool) {
	if context.Flow == nil {
		return nil, false
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return nil, false
	}
	// the in-flight bit is asked FIRST: it is a mutex read of the
	// registry's building set, where SummaryBlobFor would start a build
	// for any callee that has none yet
	if !SummaryCycleInFlight(callee) {
		return nil, false
	}
	// a callee already holding a blob took the call statement above; if
	// it did not, its blob is absent for a reason other than the cycle
	// (a declined body), and the tier-3 opaque havoc serves it instead
	if _, has := SummaryBlobFor(context.Flow, callee); has {
		return nil, false
	}
	if construct == "" {
		return OpaqueCallHavoc(context, call, target)
	}
	return OpaqueCallHavocNamed(context, call, target, construct)
}

// ClosureCallHavocOf serves a call through a BODY-LOCAL CLOSURE —
// `cleanup()`, `onClose()`, a name this same body bound to an arrow or
// function expression. The site lowers as
//
//	<every tracked slot the closure's body assigns> := unknown
//	target := unknown
//
// and nothing else.
//
// WHY NOT THE SERVED SUMMARY. A summary's binding vector is the
// callee's PARAMETERS, its OWN locals, #done and #ret (lowerSummaryBody's
// layout) — a captured name is none of those, so a closure writing
// `settled` has NO ENTRY spelling that write and no row for a ret to map
// back through. Serving such a summary would splice a compiled program
// that moves nothing the caller can see, and the caller would go on
// believing `settled` across a call that assigns it. The write-set havoc
// is the answer that says what actually happened at the position it
// happened.
//
// The set is the DECLARATION's set (ClosureWriteSlots, ir_assignment.go),
// read from the closure body the callee name resolves to, so the two
// positions — the declaration's hand-over havoc and this call's havoc —
// are computed by one function over one write census. A closure whose
// declaration the lowering never admitted still serves here: the set is a
// syntactic reading of the body, independent of which route lowered the
// declaring statement.
//
// WHAT THIS DOES NOT ADD, and does not need to. The closure's own
// arguments and receiver are havocked by the tier below this one on every
// site this tier declines, and on the sites it serves they are covered by
// the same enumerator through OpaqueCallHavoc — which this route runs
// FIRST and then adds the write set to. A closure calling ANOTHER local
// closure (`onClose` calling `cleanup()`) needs no transitive walk here:
// closureAssignedNames descends through nested function literals, so a
// write inside a callee-closure that `onClose`'s body TEXTUALLY contains
// is in the set — and a write inside a SIBLING closure `onClose` merely
// calls is covered because that sibling's own declaration havocked it
// already, at a position ahead of every call.
//
// Declines where the callee is not a plain identifier, where the name
// resolves to no local closure of a body this lowering holds slots for,
// or where the opaque enumeration itself cannot bound the site.
func ClosureCallHavocOf(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	return closureCallHavocNamed(context, call, target, "")
}

// closureCallHavocNamed is ClosureCallHavocOf with the first-havoc name
// SummaryCallOrHavocNamed threads through, the same override
// OpaqueCallHavocNamed and summaryCycleHavocNamed carry — empty keeps
// the derived "call <callee>" name.
func closureCallHavocNamed(context *LoweringContext, call *ast.Node, target int, construct string) ([]kernelbridge.IrStatement, bool) {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	closure, ok := localClosureOf(context, call.AsCallExpression().Expression)
	if !ok {
		return nil, false
	}
	// the site's own hand-over havoc — the receiver and arguments through
	// the one enumerator, plus the target's unknown
	var havocked []kernelbridge.IrStatement
	var havocOk bool
	if construct == "" {
		havocked, havocOk = OpaqueCallHavoc(context, call, target)
	} else {
		havocked, havocOk = OpaqueCallHavocNamed(context, call, target, construct)
	}
	if !havocOk {
		return nil, false
	}
	written := ClosureWriteSlots(context, closure)
	if len(written) == 0 {
		return havocked, true
	}
	already := map[int]struct{}{}
	for _, statement := range havocked {
		if statement.Kind == kernelbridge.IrStatementAssign {
			already[statement.Target] = struct{}{}
		}
	}
	missing := map[int]struct{}{}
	for slot := range written {
		if _, held := already[slot]; held {
			continue
		}
		missing[slot] = struct{}{}
	}
	if len(missing) == 0 {
		return havocked, true
	}
	// the write set goes out AHEAD of the opaque answer, whose own target
	// write stays last — the same ordering withReceiverBundleHavoc keeps
	return append(havocAssignments(missing), havocked...), true
}
