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
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
		if target >= 0 {
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
			})
		}
		return out, true
	}
	if statement, ok := summaryCallStatement(context, call, target); ok {
		return []kernelbridge.IrStatement{statement}, true
	}
	if cycled, ok := summaryCycleHavoc(context, call, target); ok {
		return withReceiverBundleHavoc(context, call, cycled)
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
	if closed, ok := ClosureCallHavocOf(context, call, target); ok {
		return withReceiverBundleHavoc(context, call, closed)
	}
	havocked, ok := OpaqueCallHavoc(context, call, target)
	if !ok {
		return nil, false
	}
	return withReceiverBundleHavoc(context, call, havocked)
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
	return OpaqueCallHavoc(context, call, target)
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
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	body, ok := localClosureBodyOf(context, call.AsCallExpression().Expression)
	if !ok {
		return nil, false
	}
	// the site's own hand-over havoc — the receiver and arguments through
	// the one enumerator, plus the target's unknown
	havocked, havocOk := OpaqueCallHavoc(context, call, target)
	if !havocOk {
		return nil, false
	}
	written := ClosureWriteSlots(context, body)
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

/* ── the served closure call ─────────────────────────────────────── */

// ClosureCallStatementOf SERVES a call through a body-local closure —
// `cleanup()`, `endStream()`, `onClose()` — instead of havocking the
// closure's write set.
//
// WHAT MAKES IT POSSIBLE. A summary's entries used to be the callee's
// parameters alone, so a closure writing the captured `settled` had no
// entry spelling that write and nothing for a ret to map back through.
// The capture rows are that spelling: the closure's summary allocates one
// entry per captured name beside its parameters
// (lowerSummaryBodyWithCaptures' capture loop), and a WRITTEN capture's
// row rides out in BundleEntries exactly as a record-parameter leaf does.
//
// WHERE THE ENTRIES COME FROM. The caller and the closure share the
// scope: a captured `settled` IS the caller's `settled` slot, identified
// by spelled name against the caller's own slot table — the same
// resolution ClosureWriteSlots already performs to compute the havoc set.
// So entry j takes `var <that slot>` on the way in, and a written row's
// exit maps back onto that same slot on the way out. Nothing is threaded
// through a path or a holder; a capture's identity is its spelling.
//
// THE SERVING GATE, and every arm of it is a decline back to the
// write-set havoc rather than a decline of the body:
//
//   - the census must READ the closure whole (closureCapturedCensus) —
//     a nested function, a `this` in any position, an element step
//     through a capture, or a captured object handed to code refuses it;
//   - EVERY capture must resolve to a caller slot. One that does not —
//     an import, a module-level const, an outer function's local the
//     caller never laid out — has no `var` to bind its entry to and no
//     slot for its write-back to land on. A partial fill is not an
//     option: an unresolved capture's entry would enter absent, which
//     CLAIMS the name is undefined inside the closure. An OBJECT capture
//     resolves the same way one level down: its leaf vocabulary is the
//     caller's own flattened leaves under that name — at whatever depth
//     the caller laid them out, since a nested literal flattens to
//     "p.a.b" and leafSlotsUnder hands that back under the path "a.b" —
//     and a capture whose caller value was never flattened, or one
//     reading a path the caller never laid out, has no vocabulary at all;
//   - the closure's body must LOWER (lowerArrowSummary) and the kernel
//     must compile it. Either refusal leaves the site exactly where it
//     was.
//
// WHAT IS NOT WEAKER THAN THE HAVOC. The havoc route wrote `unknown`
// into every slot the closure assigns. This route writes each written
// capture's own EXIT into that same slot, and an exit is what the
// closure actually left there — never weaker, since the kernel's own
// walk answers top wherever the body could not say more.
func ClosureCallStatementOf(
	context *LoweringContext,
	call *ast.Node,
	target int,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	if context.Flow == nil || context.SummaryTable == nil {
		return nil, false
	}
	closure, ok := localClosureOf(context, call.AsCallExpression().Expression)
	if !ok {
		return nil, false
	}
	// a closure whose own build is running — `step()` calling itself —
	// cannot splice itself, and the write-set havoc below is its floor
	if _, building := context.Inlining[closure]; building {
		return nil, false
	}
	// an ARGUMENT at a served closure call moves nothing the entries
	// carry: the closure's parameters are laid out from its own
	// annotations, and this route fills them from the arguments below
	callExpression := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpression.Arguments != nil {
		callArguments = callExpression.Arguments.Nodes
	}
	for _, argument := range callArguments {
		if ast.IsSpreadElement(argument) || ContainsWrite(argument) {
			return nil, false
		}
	}
	if len(callArguments) > len(closure.Parameters()) {
		return nil, false
	}
	captures, captureSlots, capturesOk := closureCapturesOf(context, closure)
	if !capturesOk {
		return nil, false
	}
	converted, convertedOk := convertLocalClosure(context, closure, captures)
	if !convertedOk {
		return nil, false
	}
	statement, statementOk := closureCallStatement(
		context, converted, closure, captureSlots, callArguments, target)
	if !statementOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{statement}, true
}

// closureCapturesOf runs the census over a closure and resolves every
// captured name to the caller's own slot — the entry layout and the
// caller slots the fill and the write-backs read, in ONE order.
//
// The order is the census's own (source order of first use), and it is
// the order the layout lays the entries out in, so capture j's entry and
// capture j's caller slot are the same j at both seams. A map's iteration
// would not be.
//
// (false) where the census refused, or where ANY captured name has no
// caller slot — the whole-or-nothing gate ClosureCallStatementOf's doc
// states.
func closureCapturesOf(
	context *LoweringContext,
	closure *ast.Node,
) ([]capturedSlot, []int, bool) {
	reads, objects, writes, ok := closureCapturedCensus(closure)
	if !ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	// the OBJECT captures first, each expanded into its leaves. Their
	// SHAPE comes from the CALLER: the leaf vocabulary is whatever the
	// caller's own slot family holds under that name (a flattened record
	// local's leaves), and a member the census read that the caller never
	// laid out has no slot to enter from — the whole capture refuses,
	// exactly as a scalar capture with no caller slot does.
	for _, object := range objects {
		leaves, leavesOk := leafSlotsUnder(context, object.Name)
		if !leavesOk {
			// the caller's value under this name is NOT flattened — an
			// ordinary scalar slot, an unrecognized local, a parameter the
			// layout never expanded, an import. There is no leaf vocabulary
			// to lay entries out from, so the site keeps the write-set havoc.
			return nil, nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		// a method call the callee resolution says MAY move the receiver
		// havocs every leaf inside the summary, so every leaf must ride out
		// Written for the caller to take the moved values back
		moves := false
		methodWrites := map[string]struct{}{}
		for _, method := range object.MethodCalls {
			if capturedMethodMoves(context, closure, object.Name, method) {
				methodWrites[method] = struct{}{}
				moves = true
			}
		}
		capture := capturedSlot{
			Name:         object.Name,
			MethodCalls:  object.MethodCalls,
			MethodWrites: methodWrites,
		}
		for _, member := range object.Members {
			slot, held := slotOfPath[member]
			if !held {
				// a member the closure reads that the caller's flattening never
				// laid out: no slot to fill the entry from, and filling it
				// absent would CLAIM the member is undefined inside the closure
				return nil, nil, false
			}
			if slot >= len(context.Sorts) || slot >= len(context.Typeofs) {
				return nil, nil, false
			}
			_, writtenHere := object.Written[member]
			capture.Members = append(capture.Members, capturedLeaf{
				Member:    member,
				Sort:      context.Sorts[slot],
				TypeofTag: context.Typeofs[slot],
				Written:   writtenHere || moves,
			})
			slots = append(slots, slot)
		}
		if len(capture.Members) == 0 {
			// a capture used only as a method receiver reads no leaf and
			// carries no entry — the layout would allocate nothing for it and
			// the havoc it needs would have no slot to land on
			return nil, nil, false
		}
		captures = append(captures, capture)
	}
	for _, name := range reads {
		index, found := slotIndexOfName(context, name)
		if !found {
			// no caller slot: no `var` to bind the entry to, and no place
			// for a write-back to land
			return nil, nil, false
		}
		if index >= len(context.Sorts) || index >= len(context.Typeofs) {
			return nil, nil, false
		}
		// a capture the caller FLATTENED (a record, an array, a
		// collection) has leaves the entry does not hold, and a write
		// through one of them moves a slot no row names
		if leaves := flattenedSlotsUnder(context, name); len(leaves) > 0 {
			return nil, nil, false
		}
		_, written := writes[name]
		captures = append(captures, capturedSlot{
			Name:      name,
			Sort:      context.Sorts[index],
			TypeofTag: context.Typeofs[index],
			Written:   written,
		})
		slots = append(slots, index)
	}
	// a write the census reported for a name the read list does not
	// carry would be a row with no entry — the census appends every
	// written name to its reads, so this states the invariant rather
	// than fixing anything
	for name := range writes {
		held := false
		for _, capture := range captures {
			if capture.Name == name {
				held = true
				break
			}
		}
		if !held {
			return nil, nil, false
		}
	}
	return captures, slots, true
}

// capturedMethodMoves answers whether `<capture>.<method>(…)` inside a
// closure may move a member of the captured object.
//
// The reading is callee_effects' own, unchanged in substance: a callee
// this package can SUMMARIZE answers from its summary — a body that
// lowered, wrote no this-field and returned no receiver moved nothing on
// the object it ran on (SummaryReceiverEffects) — and everything else
// answers TRUE. An unresolved callee, a declined lowering, a builtin
// (`removeListener`, `end`) whose declaration this package holds no body
// for: each is a doubt, and every doubt moves the object.
//
// True costs the leaves their believability from that statement on; it
// never costs the closure its serving, which is the difference between
// this and the census refusing.
func capturedMethodMoves(
	context *LoweringContext,
	closure *ast.Node,
	name string,
	method string,
) bool {
	if context == nil || context.Flow == nil {
		return true
	}
	call, found := capturedMethodCallIn(closure, name, method)
	if !found {
		return true
	}
	contract := ContractOf(context.Flow, call.AsCallExpression().Expression)
	if contract == nil || contract.Declaration == nil {
		return true
	}
	// a callee whose lowering is already running cannot be summarized from
	// underneath itself — the memo fills only when it finishes
	if _, building := context.Inlining[contract.Declaration]; building {
		return true
	}
	if _, lowered := LowerSummaryBody(context.Flow, contract.Declaration); !lowered {
		return true
	}
	receiverTouched, _ := SummaryReceiverEffects(context.Flow, contract.Declaration)
	return receiverTouched
}

// capturedMethodCallIn finds ONE call node spelling
// `<name>.<method>(…)` inside a closure's body — the site the contract
// lookup needs, since a contract resolves from an expression and the
// census reports only spellings.
//
// The FIRST such call is enough: every call under one spelling resolves
// through the same property access on the same name, so they answer one
// contract.
func capturedMethodCallIn(closure *ast.Node, name string, method string) (*ast.Node, bool) {
	body := closure.Body()
	if body == nil {
		return nil, false
	}
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := Unwrapped(node.AsCallExpression().Expression)
			if callee != nil && ast.IsPropertyAccessExpression(callee) {
				access := callee.AsPropertyAccessExpression()
				receiver := Unwrapped(access.Expression)
				if receiver != nil && ast.IsIdentifier(receiver) &&
					receiver.Text() == name && ast.IsIdentifier(access.Name()) &&
					access.Name().Text() == method {
					found = node
					return true
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found, found != nil
}

// localClosureBlobEntry is one body-local closure's compiled answer,
// keyed by the closure NODE the way arrowBlobs keys a converted callback:
// a closure belongs to the body that spells it, and its capture layout is
// that body's slot vector — so it is deliberately not in the
// declaration-keyed registry.
type localClosureBlobEntry struct {
	Blob     kernelbridge.SummaryBlob
	Lowered  LoweredSummary
	Captures []capturedSlot
	Ok       bool
}

// localClosureBuilding is the set of closure nodes whose compile is
// RUNNING right now — the cycle guard, and it has to be its own set
// rather than the lowering context's Inlining map.
//
// A context seeds Inlining with the ONE declaration it is lowering, and
// convertLocalClosure builds a FRESH context for the closure, so a
// self-call (`const step = () => step()`) is caught by the inner
// context's own seed. MUTUAL recursion is not: `a` calling `b` calling
// `a` gives each context a seed naming only itself, and the compile
// would descend forever. This set spans the whole descent, so the second
// entry into either closure declines and the site takes the write-set
// havoc — the same floor the registry's own in-flight tier gives a
// recursive declaration.
var (
	localClosureBlobsMu  sync.Mutex
	localClosureBlobs    = map[*ast.Node]localClosureBlobEntry{}
	localClosureBuilding = map[*ast.Node]struct{}{}
)

// ClearLocalClosureBlobs drops every remembered closure blob. Keyed on
// closure nodes from one program, so a caller that builds a new program
// clears it, as it clears the layout's own memos.
func ClearLocalClosureBlobs() {
	localClosureBlobsMu.Lock()
	localClosureBlobs = map[*ast.Node]localClosureBlobEntry{}
	localClosureBuilding = map[*ast.Node]struct{}{}
	localClosureBlobsMu.Unlock()
}

// convertLocalClosure lowers a body-local closure with its capture rows
// and asks the kernel to compile it — the same two steps convertArrow
// takes for a callback argument, and for the same reason: the entries a
// blob is compiled under include the captures, so the blob belongs to the
// capture layout that built it.
//
// A remembered blob is reused only where the capture layout AGREES name
// for name; a re-lowering that resolved a different one is a different
// entry vector and declines rather than filling the old blob's rows from
// new slots.
func convertLocalClosure(
	context *LoweringContext,
	closure *ast.Node,
	captures []capturedSlot,
) (localClosureBlobEntry, bool) {
	localClosureBlobsMu.Lock()
	held, has := localClosureBlobs[closure]
	_, building := localClosureBuilding[closure]
	if !has && !building {
		localClosureBuilding[closure] = struct{}{}
	}
	localClosureBlobsMu.Unlock()
	if has {
		if !held.Ok || !sameCaptures(held.Captures, captures) {
			return localClosureBlobEntry{}, false
		}
		return held, true
	}
	if building {
		// this closure's own compile is already running further up the
		// descent — a cycle, which cannot splice itself
		return localClosureBlobEntry{}, false
	}
	defer func() {
		localClosureBlobsMu.Lock()
		delete(localClosureBuilding, closure)
		localClosureBlobsMu.Unlock()
	}()
	lowered, loweredOk := lowerArrowSummary(context.Flow, closure, nil, captures)
	if !loweredOk {
		localClosureBlobsMu.Lock()
		localClosureBlobs[closure] = localClosureBlobEntry{Captures: captures}
		localClosureBlobsMu.Unlock()
		return localClosureBlobEntry{}, false
	}
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		localClosureBlobsMu.Lock()
		localClosureBlobs[closure] = localClosureBlobEntry{Captures: captures}
		localClosureBlobsMu.Unlock()
		return localClosureBlobEntry{}, false
	}
	entry := localClosureBlobEntry{Blob: blob, Lowered: lowered, Captures: captures, Ok: true}
	localClosureBlobsMu.Lock()
	localClosureBlobs[closure] = entry
	localClosureBlobsMu.Unlock()
	return entry, true
}

// closureCallStatement builds the ONE call statement a served closure
// call takes.
//
// The entry vector, in the layout's own order: the closure's DECLARED
// parameters take the call's arguments (absent where the call passed
// none — the runtime's own answer for a missing argument), then the
// CAPTURE entries each take a `var` of the caller slot that name resolved
// to, then every remaining slot enters absent with the done flag at {0} —
// exactly the entry states applySummary sends, so a spliced compile and a
// direct apply agree.
//
// Rets: the closure's #ret out-state lands on `target` where the site has
// one, and EVERY WRITTEN CAPTURE ROW maps its own exit back onto the
// caller slot it was filled from. That second half is the write-back, and
// it is read from the layout's own BundleEntries rather than recomputed —
// the row's Index is the entry position the layout allocated, and the
// caller slot is the one closureCapturesOf resolved for the same capture,
// so the two seams walk one answer.
func closureCallStatement(
	context *LoweringContext,
	converted localClosureBlobEntry,
	closure *ast.Node,
	captureSlots []int,
	callArguments []*ast.Node,
	target int,
) (kernelbridge.IrStatement, bool) {
	lowered := converted.Lowered
	declared := len(closure.Parameters())
	if declared+len(captureSlots) != lowered.ParamCount {
		// the layout expanded a parameter into several entries (a record,
		// an array, a class-typed bundle), which this route's argument fill
		// does not spell — the havoc floor keeps the site
		return kernelbridge.IrStatement{}, false
	}
	args := make([]kernelbridge.LoopEffect, 0, lowered.SlotCount)
	for index := range declared {
		if index >= len(callArguments) {
			args = append(args, kernelbridge.AbsentConst())
			continue
		}
		argument := callArguments[index]
		effect, ok := RhsEffect(context, SortOfArg(context, argument), argument)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		args = append(args, effect)
	}
	for _, slot := range captureSlots {
		args = append(args, varEffect(slot))
	}
	for len(args) < lowered.SlotCount {
		if len(args) == lowered.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	retsLength := lowered.RetIndex + 1
	if lowered.SlotCount > retsLength {
		retsLength = lowered.SlotCount
	}
	rets := make([]int, retsLength)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[lowered.RetIndex] = target
	}
	// the write-backs: each written capture row's exit onto the caller
	// slot its entry was filled from.
	//
	// The key is the row's own spelling below the "#capture." prefix — a
	// scalar row's caller name ("settled"), an OBJECT row's
	// name-and-member ("stream.writableEnded") — and the map is walked in
	// the SAME order the layout appended entries in, which is the order
	// captureSlots holds. One position per entry on both sides, leaves
	// included: that is what recordParamRets does with its leaf paths, and
	// it is why neither seam re-derives an index.
	slotOfCapture := map[string]int{}
	position := 0
	for _, capture := range converted.Captures {
		if len(capture.Members) > 0 {
			for _, leaf := range capture.Members {
				if position < len(captureSlots) {
					slotOfCapture[capture.Name+"."+leaf.Member] = captureSlots[position]
				}
				position++
			}
			continue
		}
		if position < len(captureSlots) {
			slotOfCapture[capture.Name] = captureSlots[position]
		}
		position++
	}
	if position != len(captureSlots) {
		// the layout's entry count and this site's slot vector disagree —
		// the site declines rather than mapping a row onto a slot the
		// allocator never paired it with
		return kernelbridge.IrStatement{}, false
	}
	for _, entry := range lowered.BundleEntries {
		name, isCapture := capturedNameOfSlot(entry.Path)
		if !isCapture || !entry.Written {
			continue
		}
		slot, held := slotOfCapture[name]
		if !held {
			// a row naming a capture this site did not resolve: the layout
			// and this fill disagree, and the site declines rather than
			// writing an exit into a slot nothing bound
			return kernelbridge.IrStatement{}, false
		}
		if entry.Index < 0 || entry.Index >= len(rets) {
			return kernelbridge.IrStatement{}, false
		}
		rets[entry.Index] = slot
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(closure, converted.Blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// localClosureOf resolves a call's callee — a plain identifier — to the
// CLOSURE NODE the name was declared to hold, where localClosureBodyOf
// answers the body. The two read the same declaration; this one answers
// the function-like node the layout and the compile need, since a summary
// is lowered from the declaration, not from the block.
func localClosureOf(context *LoweringContext, callee *ast.Node) (*ast.Node, bool) {
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false
	}
	head := Unwrapped(callee)
	if head == nil || !ast.IsIdentifier(head) {
		return nil, false
	}
	symbol := symbolAt(context.Flow.P.Checker, head)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil, false
	}
	closure := Unwrapped(initializer)
	if closure == nil || !ast.IsFunctionLike(closure) || closure.Body() == nil {
		return nil, false
	}
	return closure, true
}

// localClosureBodyOf resolves a call's callee — a plain identifier — to
// the FUNCTION BODY of a closure the name was declared to hold:
// `const f = () => { … }` / `const f = function () { … }`, or a `let`
// bound the same way.
//
// The resolution is the checker's symbol, so a name shadowed by an inner
// scope resolves to the declaration the call actually reaches rather than
// to the spelling. A symbol whose declaration is not a variable
// declaration, or whose initializer is not a function literal with a
// body, answers nothing — the site then takes the opaque tier it always
// took.
//
// The declaration is NOT required to sit in this lowering's own body: an
// arrow declared in an enclosing scope and called here writes the slots
// this vector spells under the same names, and havocking them is right
// wherever the closure was built. A name whose writes touch nothing this
// vector holds yields an empty set, which costs the site nothing.
func localClosureBodyOf(context *LoweringContext, callee *ast.Node) (*ast.Node, bool) {
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false
	}
	head := Unwrapped(callee)
	if head == nil || !ast.IsIdentifier(head) {
		return nil, false
	}
	symbol := symbolAt(context.Flow.P.Checker, head)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil, false
	}
	closure := Unwrapped(initializer)
	if closure == nil || !ast.IsFunctionLike(closure) || closure.Body() == nil {
		return nil, false
	}
	return closure.Body(), true
}

// summaryCallStatement builds the call statement for a resolved callee
// with a compiled summary: each argument lowers as an effect over the
// caller's bindings (any that does not declines the whole call), and
// Rets names the caller slot the callee's RETURN out-state writes — -1
// everywhere else, which drops those out-states.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call statement whose value nothing reads.
func summaryCallStatement(context *LoweringContext, call *ast.Node, target int) (kernelbridge.IrStatement, bool) {
	if context.Flow == nil || context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return kernelbridge.IrStatement{}, false
	}
	blob, has := SummaryBlobFor(context.Flow, callee)
	if !has {
		return kernelbridge.IrStatement{}, false
	}
	outIndex, shapeOk := SummaryOutShapeFor(context.Flow, callee)
	if !shapeOk {
		return kernelbridge.IrStatement{}, false
	}
	// a NEW expression serves through the same statement, with three
	// differences the code below branches on: its this-entries stay
	// ABSENT (a fresh instance's fields before the initializers run —
	// never any caller receiver's slots), its unwritten ret maps nowhere
	// (the instance is a value, not undefined — the caller assigns
	// unknown separately), and an expanded-parameter argument declines
	// (the threading below reads a CallExpression).
	isNew := ast.IsNewExpression(call)
	var callArguments []*ast.Node
	if isNew {
		if newArguments := call.AsNewExpression().Arguments; newArguments != nil {
			callArguments = newArguments.Nodes
		}
	} else {
		if a := call.AsCallExpression().Arguments; a != nil {
			callArguments = a.Nodes
		}
	}
	parameters := callee.Parameters()
	// arity: extra arguments are admitted only into a trailing REST
	// parameter, and each extra one must MOVE NOTHING — its value lands
	// in the rest array, whose entry is unknown regardless, so only its
	// evaluation effects matter and an inert one has none
	restParameter := len(parameters) > 0 &&
		parameters[len(parameters)-1].AsParameterDeclaration().DotDotDotToken != nil
	if len(callArguments) > len(parameters) {
		if !restParameter {
			return kernelbridge.IrStatement{}, false
		}
		for _, extra := range callArguments[len(parameters):] {
			if !writeAndCallFree(extra) {
				return kernelbridge.IrStatement{}, false
			}
		}
	}
	// an argument that WRITES would move the caller's state on the way
	// in, which the effect grammar does not carry
	for _, argument := range callArguments {
		if ast.IsSpreadElement(argument) || ContainsWrite(argument) {
			return kernelbridge.IrStatement{}, false
		}
	}
	// one entry effect per callee SLOT — the callee's arity is its whole
	// binding vector (see buildSummaryBlob), so the parameters come from
	// the call, every local and the result slot enter absent, and the
	// done flag enters {0}: exactly the entry states the apply side
	// sends, so the spliced compile and the direct apply agree
	calleeShape, shapeKnown := LowerSummaryBody(context.Flow, callee)
	if !shapeKnown {
		return kernelbridge.IrStatement{}, false
	}
	// a callee that RETURNS ITS RECEIVER hands the composed caller an
	// alias it may write through later — writes this route's rets could
	// never carry back. The call declines to the opaque tier, whose
	// receiver-bundle havoc is the honest answer.
	if calleeShape.ReturnsReceiver {
		return kernelbridge.IrStatement{}, false
	}
	// the callee's parameters no longer map 1:1 onto entries: a type-
	// literal parameter EXPANDS to one entry per member. The entry list is
	// built by walking the declared parameters through the very expansion
	// the layout used (SummaryParameterEntries), so a drift between the
	// two is impossible — one function answers both.
	args := make([]kernelbridge.LoopEffect, 0, calleeShape.SlotCount)
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntries(parameter)
		if !entriesOk {
			return kernelbridge.IrStatement{}, false
		}
		// a CLASS-TYPED parameter expanded to one entry per read field;
		// placeholders hold the positions and bundleParamRetsAndArgs
		// below overwrites them from the argument's own spelled path
		if _, census, _, isBundle := BundleParamCensus(context.Flow, callee.Body(), parameter); isBundle && census.Believable() && len(census.Reads) > 0 {
			if isNew {
				// the placeholder rows below are overwritten by the
				// threading this route skips for a new — absent rows would
				// CLAIM the argument's fields are undefined
				return kernelbridge.IrStatement{}, false
			}
			for range census.Reads {
				args = append(args, kernelbridge.AbsentConst())
			}
			continue
		}
		// a BINDING-PATTERN parameter: one argument effect per bound
		// entry, each reading the argument object's member by the entry's
		// Key — the record-argument reader keyed the same way
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			if index >= len(callArguments) {
				for range entries {
					args = append(args, kernelbridge.AbsentConst())
				}
				continue
			}
			pseudo := make([]recordParamMember, len(entries))
			for at, entry := range entries {
				pseudo[at] = recordParamMember{Key: entry.Key, Sort: entry.Sort, TypeofTag: entry.TypeofTag}
			}
			leafEffects, leavesOk := recordArgumentEffects(context, pseudo, callArguments[index])
			if !leavesOk {
				return kernelbridge.IrStatement{}, false
			}
			args = append(args, leafEffects...)
			continue
		}
		// an ARRAY-TYPED parameter's two entries take the caller's own
		// flattened array slots, "<argument>.len" and "<argument>.elem",
		// where the argument is a bare name the caller flattened the same
		// way. Anything else — a literal, a call's result, a name the
		// caller kept whole — fills both UNKNOWN rather than absent: the
		// callee is passed a real array, and absent would claim it is
		// undefined. Two effects go out either way, which is what keeps
		// this vector the same length the layout laid out.
		if _, flattened := arrayParamSlotsIn(context.Flow, parameter); flattened {
			lenEffect, elemEffect := unknownEffect, unknownEffect
			if index < len(callArguments) {
				if head := Unwrapped(callArguments[index]); ast.IsIdentifier(head) {
					if lenSlot, elemSlot, isArray := arraySlotsOf(context, head.Text()); isArray {
						lenEffect, elemEffect = varEffect(lenSlot), varEffect(elemSlot)
					}
				}
			}
			args = append(args, lenEffect, elemEffect)
			continue
		}
		members, expanded := recordParamMembersOf(parameter)
		if expanded {
			// a missing argument leaves every leaf absent — the same "entered
			// absent" the scalar case gives an omitted argument
			if index >= len(callArguments) {
				for range entries {
					args = append(args, kernelbridge.AbsentConst())
				}
				continue
			}
			leafEffects, leavesOk := recordArgumentEffects(context, members, callArguments[index])
			if !leavesOk {
				return kernelbridge.IrStatement{}, false
			}
			args = append(args, leafEffects...)
			continue
		}
		if index >= len(callArguments) {
			// a DEFINITELY-MISSING argument on a DEFAULTED parameter rides
			// the default itself — a CONST effect means the same thing in
			// every binding space, so the callee's lowered default is this
			// caller's argument effect verbatim. The body's definedness
			// branch then joins two identical values and stays exact.
			if effect, defaulted := calleeShape.DefaultEffects[len(args)]; defaulted {
				if effect.Kind == kernelbridge.LoopEffectConst || effect.Kind == kernelbridge.LoopEffectConstState {
					args = append(args, effect)
					continue
				}
			}
			args = append(args, kernelbridge.AbsentConst())
			continue
		}
		argument := callArguments[index]
		effect, ok := RhsEffect(context, SortOfArg(context, argument), argument)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		args = append(args, effect)
	}
	for len(args) < calleeShape.SlotCount {
		if len(args) == calleeShape.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	// Rets: -1 says nothing reads that out-state. The return out-state
	// maps where the site has a slot for it, and a WRITTEN this-field
	// entry maps back into the caller's own slot for that field
	// (bundleRetsAndArgs below).
	//
	// The vector runs to the callee's WHOLE slot count, not to its ret
	// index: a callee whose returns carry MEMBER slots has those slots
	// past #ret (returnedLiteralShape's allocation sits after it), and a
	// vector stopping at #ret would leave rows the kernel answers with no
	// position to be named at. Every row past #ret stays -1 unless the
	// member threading below claims it — the statement route's caller has
	// one scalar slot for the call's value, and a fresh object's members
	// map onto no caller slot it already holds.
	retsLength := outIndex + 1
	if calleeShape.SlotCount > retsLength {
		retsLength = calleeShape.SlotCount
	}
	rets := make([]int, retsLength)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 && !isNew {
		rets[outIndex] = target
	}
	// A MEMBER-CARRYING RETURN at a statement-route call site: the callee
	// built a fresh object whose members ride their own exits, and the
	// caller's target is ONE scalar slot. There is nothing to write those
	// members into — no caller slot spells "the k-th key of the value this
	// call is about to produce" — so the rows stay -1 and the target keeps
	// the scalar #ret's unknown, exactly as before this shape existed.
	//
	// The value is not lost: the DIRECT APPLY route (applySummary) rebuilds
	// the object from these same exits, and that is the route every
	// expression-position call takes. What this seam owes is only that the
	// two agree about WHICH slot is which member, which they do by reading
	// one list — the callee's own RetMembers.
	if !threadRetMemberRets(context, calleeShape, target, rets) {
		return kernelbridge.IrStatement{}, false
	}
	// the RECEIVER decides the this-entry fill: the callee's own
	// "this.<field>" entries take the caller's "<receiverPath>.<field>"
	// slots, and the ones it writes ride back out through rets. A NEW
	// skips both threadings whole: its instance is FRESH — the absent
	// fill already in place is exactly a field before its initializer —
	// and its writes land on an object no caller slot spells yet.
	if !isNew {
		if !bundleRetsAndArgs(context, call, calleeShape, args, rets) {
			return kernelbridge.IrStatement{}, false
		}
		if !bundleParamRetsAndArgs(context, call, callee, calleeShape, args, rets) {
			return kernelbridge.IrStatement{}, false
		}
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(callee, blob),
		Args:   args,
		Rets:   rets,
	}, true
}

/* ── the constructed instance's fields ───────────────────────────── */

// targetNameOf spells the caller slot a call's value lands in, so the
// field threading below can look for that name's own leaves. A target of
// -1, or one past the binding vector, spells nothing.
func targetNameOf(context *LoweringContext, target int) string {
	if context == nil || target < 0 || target >= len(context.Bindings) {
		return ""
	}
	return context.Bindings[target]
}

// constructorFieldRets writes the CONSTRUCTOR's this-field exits into the
// caller's own slots for the fresh instance's fields.
//
// A `new X()` runs a constructor whose summary already carries one
// this-entry per field the body touches, with Written marking the ones it
// assigns (the same BundleEntries an ordinary method rides with — nothing
// in the layout special-cases a constructor out of that machinery). Those
// entries entered ABSENT, which is exactly a field before its initializer
// runs, and their EXITS hold what the constructor left in them.
//
// The caller can read those exits wherever it holds a slot for the same
// field of the same instance: `const c = new C()` with the caller's own
// "c.count" flattened is the shape, and the row's out then lands on that
// slot. Where the caller flattened nothing the rows stay -1 and the
// instance is the unknown it has always been — dropping them is sound for
// the reason the receiver threading gives: nothing lowered can read a
// spelling the caller has no slot for.
//
// This is the ONE difference from an ordinary call's write-back, and it
// is a difference in direction only: an ordinary call maps a written
// field back onto a slot the caller filled on the way IN, while a fresh
// instance's fields were never filled by anyone — the entries stayed
// absent — so these rows carry values OUT of a constructor into slots the
// caller had no value for. The exits are the constructor's own writes
// either way, and summarize_eq covers the whole exit row.
func constructorFieldRets(
	context *LoweringContext,
	call *ast.Node,
	targetName string,
	statement kernelbridge.IrStatement,
) kernelbridge.IrStatement {
	if context == nil || targetName == "" || statement.Rets == nil {
		return statement
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return statement
	}
	calleeShape, known := LowerSummaryBody(context.Flow, callee)
	if !known {
		return statement
	}
	for _, entry := range calleeShape.BundleEntries {
		if !entry.Written {
			// a field the constructor only READS holds whatever it entered
			// with, which for a fresh instance is absent — writing that back
			// would claim the field IS undefined, and the class's own
			// initializers may have set it outside this body's sight
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		slot, held := slotIndexOfName(context, targetName+"."+field)
		if !held {
			continue
		}
		if entry.Index < 0 || entry.Index >= len(statement.Rets) {
			continue
		}
		statement.Rets[entry.Index] = slot
	}
	return statement
}

/* ── the returned value's members ────────────────────────────────── */

// threadRetMemberRets decides where a member-carrying return's exits
// land in the CALLER.
//
// The callee's RetMembers name one out-slot per member of the value it
// returns. A caller writing that value into one scalar slot has no place
// for them — an object is not a scalar, and the target holds the same
// unknown it always held — so the rows stay -1 and the exits are dropped.
// Dropping is sound and not weaker than declining: nothing the caller
// lowered can read a member of a value it has no name for, so no
// knowledge survives the call for the dropped rows to falsify.
//
// The rows become READABLE at the call sites that DO hold names for the
// members: `const { a, b } = f()`, where the caller flattened `a` and `b`
// as its own locals. That threading is the destructure route's to make —
// it knows which local each key binds to — and it reads this same
// RetMembers list, so the layout's answer about which slot is which
// member is the one answer all three seams walk.
//
// (false) never today: every shape this route meets is either threaded or
// left at -1, and there is no member layout that makes the call itself
// unlowerable. The flag rides so the caller's three threadings read the
// same way.
func threadRetMemberRets(
	context *LoweringContext,
	calleeShape LoweredSummary,
	target int,
	rets []int,
) bool {
	if calleeShape.RetShape == RetShapeNone || len(calleeShape.RetMembers) == 0 {
		return true
	}
	for _, member := range calleeShape.RetMembers {
		if member.Index < 0 || member.Index >= len(rets) {
			// the layout and this vector disagree about how many slots the
			// callee has — the call declines rather than writing an exit into
			// a position nothing laid out
			return false
		}
		// a caller slot spelled "<target's name>.<member>" is what a
		// destructured or flattened target would hold. The statement route's
		// target is an index, not a name, so nothing is spelled here and the
		// row is dropped; the direct-apply route serves these sites.
		_ = target
		rets[member.Index] = -1
	}
	return true
}

/* ── receiver threading ──────────────────────────────────────────── */

// bundleRetsAndArgs threads the CALL'S RECEIVER through the callee's
// this-field bundle entries, writing into `args` and `rets` in place.
//
// The callee's summary carries one BundleEntry per expanded bundle
// entry. The "this."-prefixed ones are the callee's own receiver fields,
// and WHICH caller values they hold is decided by the receiver the call
// was spelled on:
//
//	this.m(…)                → the caller's own "this.<field>" slots
//	wrapper.m(…)             → the caller's "wrapper.<field>" slots
//	this.injector.load(…)    → the caller's "this.injector.<field>" slots
//
// so the rule is one rule: the receiver's spelled dotted path prefixes
// the field name, and the caller's slot of that spelling fills the
// entry. A chain is not a special case — it is the same prefix one step
// longer.
//
// A field the CALLER has no slot for fills with the UNKNOWN effect,
// which evaluates top: the caller knows nothing about that field, and
// nothing is what the entry must say. It is emphatically NOT the absent
// constant the unfilled padding uses — absent claims the field IS
// undefined, which is a claim about a value the caller never had.
//
// A WRITTEN entry (the callee's body assigns that field) maps its own
// out — the entry's Index, since the compiled out vector is the whole
// binding row — back to the caller's slot for the same spelling. Where
// the caller has no slot the ret stays -1 and the write lands nowhere:
// nothing lowered can read that spelling, so no knowledge survives the
// call that the write would falsify. (The opaque-call alternative would
// have havocked those same leaves, so dropping the write-back is not
// weaker than declining the site.)
//
// Non-"this." entries are the wave-3 record-parameter leaves, already
// filled from the argument vector above; this leaves them alone.
//
// (false) only where the callee HAS this-entries and the receiver has no
// spelled path to fill them from — a computed step, an optional step, a
// call's result. Filling those with unknown would be sound, but the
// receiver is then an object the site cannot name at all, and the opaque
// tier havocs what it was handed rather than pretending the entries were
// threaded.
func bundleRetsAndArgs(
	context *LoweringContext,
	call *ast.Node,
	calleeShape LoweredSummary,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	thisEntries := make([]BundleEntry, 0, len(calleeShape.BundleEntries))
	for _, entry := range calleeShape.BundleEntries {
		if strings.HasPrefix(entry.Path, "this.") {
			thisEntries = append(thisEntries, entry)
		}
	}
	if len(thisEntries) == 0 {
		return true
	}
	receiverPath, pathOk := receiverPathOf(call)
	if !pathOk {
		return false
	}
	for _, entry := range thisEntries {
		if entry.Index < 0 || entry.Index >= len(args) {
			return false
		}
		field := strings.TrimPrefix(entry.Path, "this.")
		slot, held := slotIndexOfName(context, receiverPath+"."+field)
		if !held {
			// the caller has no slot for this field: unknown, never absent
			args[entry.Index] = unknownEffect
			continue
		}
		args[entry.Index] = varEffect(slot)
		if entry.Written && entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return true
}

// bundleParamRetsAndArgs is bundleRetsAndArgs' half for the CALLEE'S
// PARAMETER bundles — the class-typed ones and the record-expanded ones,
// which are one rule here. A "this."-rooted row is filled from the
// call's receiver; a "<holder>."-rooted row is filled from the ARGUMENT
// passed at that parameter's position, and the rule is the same one step
// over: the argument's own spelled dotted path prefixes the field name,
// and the caller's slot of that spelling fills the entry.
//
//	f(wrapper)          → the caller's "wrapper.<field>" slots
//	f(this.wrapper)     → the caller's "this.wrapper.<field>" slots
//
// An argument that is not a spelled path (a call's result, a literal, a
// computed or optional step) fills its rows UNKNOWN, never absent: the
// caller holds no name for that object, and nothing is what the entry
// must say. Unlike the receiver case this does not decline the site —
// the argument is still passed by value and the rest of the call is
// exactly as sound.
//
// A RECORD-EXPANDED parameter takes the write-back half alone, and this
// is the difference from the class-typed rows. Its args were already
// filled by recordArgumentEffects, which reads BOTH shapes the record
// route admits — an object literal (case (a)) and a flattened record
// local (case (b)) — and a literal's leaves are effects no dotted path
// spells. Overwriting them from the path would lose the literal's own
// values, so only rets moves here. A written leaf maps back exactly where
// case (b) gave the caller a slot to map into, which is the caller's
// "q.<member>"; a case-(a) literal has no such slot, the row's ret stays
// -1, and the write lands nowhere because the object the callee wrote is
// one the caller kept no name for.
func bundleParamRetsAndArgs(
	context *LoweringContext,
	call *ast.Node,
	callee *ast.Node,
	calleeShape LoweredSummary,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	if context.Flow == nil {
		return true
	}
	callExpr := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpr.Arguments != nil {
		callArguments = callExpr.Arguments.Nodes
	}
	for index, parameter := range callee.Parameters() {
		holder, _, _, isBundle := BundleParamCensus(context.Flow, callee.Body(), parameter)
		if !isBundle {
			// a RECORD-EXPANDED parameter is a bundle of another kind: its
			// leaves are spelled under the parameter's own name, so the holder
			// is that name and the rows read back by the same field split
			if recordHolder, expanded := recordParamHolderOf(context.Flow, parameter); expanded {
				if !recordParamRets(context, calleeShape, recordHolder, index, callArguments, args, rets) {
					return false
				}
			}
			continue
		}
		argumentPath := ""
		hasPath := false
		if index < len(callArguments) {
			argumentPath, hasPath = dottedPathOf(Unwrapped(callArguments[index]))
		}
		for _, entry := range calleeShape.BundleEntries {
			field, isRow := BundleParamFieldNameOf(entry.Path, holder)
			if !isRow {
				continue
			}
			if entry.Index < 0 || entry.Index >= len(args) {
				return false
			}
			if !hasPath {
				args[entry.Index] = unknownEffect
				continue
			}
			slot, held := slotIndexOfName(context, argumentPath+"."+field)
			if !held {
				args[entry.Index] = unknownEffect
				continue
			}
			args[entry.Index] = varEffect(slot)
			if entry.Written && entry.Index < len(rets) {
				rets[entry.Index] = slot
			}
		}
	}
	return true
}

// recordParamHolderOf is the name a record-expanded parameter's leaves
// are spelled under — the parameter's own identifier. A binding-pattern
// parameter expands under the placeholder holder recordParamMembersIn
// uses and owns no name the caller could have flattened, so it answers
// false and takes no threading.
func recordParamHolderOf(ctx *FlowContext, parameter *ast.Node) (string, bool) {
	if _, expanded := recordParamMembersIn(ctx, parameter); !expanded {
		return "", false
	}
	name := parameter.AsParameterDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return "", false
	}
	return name.Text(), true
}

// recordParamRets threads the WRITE-BACKS for one record-expanded
// parameter: each leaf row the callee's body moved maps its out back onto
// the caller slot holding that same leaf.
//
// The caller's slot is found the way recordArgumentEffects case (b) found
// the value it sent — the argument is a bare identifier naming a
// flattened record local, and the leaf sits at "<argument>.<member>".
// Every other argument shape (an object literal, a call's result, a
// dotted path the caller never flattened) leaves the row's ret at -1: no
// slot of the caller spells that leaf, so no belief of the caller's
// survives the call for the write to falsify.
//
// This never touches args. The record route filled them from the argument
// itself, and its literal case carries values no path could restate.
func recordParamRets(
	context *LoweringContext,
	calleeShape LoweredSummary,
	holder string,
	index int,
	callArguments []*ast.Node,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	if index >= len(callArguments) {
		return true
	}
	head := Unwrapped(callArguments[index])
	if !ast.IsIdentifier(head) {
		return true
	}
	leaves, leavesOk := leafSlotsUnder(context, head.Text())
	if !leavesOk {
		return true
	}
	slotOfPath := map[string]int{}
	for _, leaf := range leaves {
		slotOfPath[leaf.Path] = leaf.Index
	}
	for _, entry := range calleeShape.BundleEntries {
		member, isRow := BundleParamFieldNameOf(entry.Path, holder)
		if !isRow || !entry.Written {
			continue
		}
		if entry.Index < 0 || entry.Index >= len(args) {
			return false
		}
		slot, held := slotOfPath[member]
		if !held {
			continue
		}
		if entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return true
}

// receiverPathOf spells the object a call was made ON, as the dotted
// path the caller's slots are named under:
//
//	this.m(…)              → "this"
//	wrapper.m(…)           → "wrapper"
//	this.injector.load(…)  → "this.injector"
//
// The path is the callee expression MINUS its last step, which is the
// method name and never a slot. A bare `f(…)` has no receiver at all.
//
// The declines, each because no slot spelling exists for what was
// written:
//
//   - a COMPUTED step (`this.parts[i].load()`, `o[k].m()`) — nothing
//     spells which object the index picked;
//   - an OPTIONAL step (`this.injector?.load()`) — the receiver may be
//     absent, and no slot carries "the fields of a maybe-absent object";
//   - a receiver that is not rooted in `this` or an identifier (a call's
//     result, a literal, a parenthesized function) — there is no name
//     for the caller's slots to have been laid out under.
func receiverPathOf(call *ast.Node) (string, bool) {
	callee := Unwrapped(call.AsCallExpression().Expression)
	if !ast.IsPropertyAccessExpression(callee) {
		// a bare `f(…)`: no receiver, so no path
		return "", false
	}
	access := callee.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	return dottedPathOf(Unwrapped(access.Expression))
}

// dottedPathOf reads an expression as the dotted slot spelling it names
// — "this", "wrapper", "this.injector", "a.b.c" — or (false) where a
// step is computed or optional, or the root is neither `this` nor an
// identifier.
func dottedPathOf(node *ast.Node) (string, bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
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

// receiverBundleHavocSlots is the OPAQUE tier's half of the same
// receiver reading: the caller slots a call's receiver names, for a call
// that took the havoc route instead of the call statement.
//
// It exists because the statement enumerator's mention rule (rule (b),
// havocSlotsOfStatement) finds a flattened local by IDENTIFIER, and a
// `this`-rooted receiver has no identifier at its root — `this` is a
// keyword. So `this.injector.load(x)`, whose receiver is the bundle
// "this.injector", walked the enumerator and contributed NOTHING: the
// callee may write any of that bundle's fields, and every one of those
// slots kept its stale knowledge across the call. An identifier-rooted
// receiver (`wrapper.get(k)`) is already covered — the enumerator sees
// `wrapper` and asks flattenedSlotsUnder for it — so this adds the
// `this`-rooted case and, for a chain, the exact bundle path the
// receiver names rather than only its root.
//
// The slots are every one spelled under the receiver's path: its leaves
// (leafSlotsUnder and the collection recognizers, through
// flattenedSlotsUnder) plus the path's OWN slot where it has one — a
// field the callee may replace outright.
func receiverBundleHavocSlots(context *LoweringContext, call *ast.Node) []int {
	receiverPath, ok := receiverPathOf(call)
	if !ok {
		return nil
	}
	out := flattenedSlotsUnder(context, receiverPath)
	if slot, held := slotIndexOfName(context, receiverPath); held {
		out = append(out, slot)
	}
	return out
}

// recordArgumentEffects maps ONE argument onto an expanded parameter's
// leaf entries, IN THE PARAMETER'S MEMBER ORDER — the order
// SummaryParameterEntries laid the entries out, so effect j fills member
// j's slot whatever order the argument spelled its keys.
//
// Two argument shapes lower, and nothing else:
//
//	(a) an OBJECT LITERAL whose keys are exactly the members — each
//	    member's value lowers as an ordinary effect through the shared
//	    RHS grammar, under the member's own sort;
//	(b) a FLATTENED RECORD LOCAL of exactly those leaves — each member
//	    reads the caller slot spelled "q.<member>" as a var.
//
// Anything else declines the whole call: a call's result, a parameter
// the caller itself holds unexpanded, a literal with an extra or missing
// key, a spread. There is no partial fill — a leaf left at its absent
// entry state would read inside the callee as undefined, which is not
// what the caller passed.
func recordArgumentEffects(
	context *LoweringContext,
	members []recordParamMember,
	argument *ast.Node,
) ([]kernelbridge.LoopEffect, bool) {
	head := Unwrapped(argument)
	// (a) `f({ lo: 1, hi: n })`
	if ast.IsObjectLiteralExpression(head) {
		valueOfKey := map[string]*ast.Node{}
		for _, property := range head.AsObjectLiteralExpression().Properties.Nodes {
			if !ast.IsPropertyAssignment(property) {
				return nil, false
			}
			assignment := property.AsPropertyAssignment()
			if !ast.IsIdentifier(assignment.Name()) || assignment.Initializer == nil {
				return nil, false
			}
			key := assignment.Name().Text()
			if _, already := valueOfKey[key]; already {
				return nil, false
			}
			valueOfKey[key] = assignment.Initializer
		}
		// EXACTLY the members: an extra key is a shape the parameter did
		// not declare, a missing one leaves a leaf unwritten
		if len(valueOfKey) != len(members) {
			return nil, false
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			value, has := valueOfKey[member.Key]
			if !has {
				return nil, false
			}
			effect, ok := RhsEffect(context, member.Sort, value)
			if !ok {
				return nil, false
			}
			out = append(out, effect)
		}
		return out, true
	}
	// (b) `f(q)` where q is a flattened record local of exactly these
	// leaves. leafSlotsUnder is the same reader the record-to-record
	// assignment uses, so "the caller flattened q" and "q's leaves have
	// slots" are one question.
	if ast.IsIdentifier(head) {
		leaves, leavesOk := leafSlotsUnder(context, head.Text())
		if !leavesOk || len(leaves) != len(members) {
			return nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			slot, has := slotOfPath[member.Key]
			if !has {
				return nil, false
			}
			out = append(out, varEffect(slot))
		}
		return out, true
	}
	return nil, false
}

// SummaryCallStatementOf is the lowering-side entry: a call expression
// STATEMENT (`f(…)`), or a call assigned into a tracked slot (`x =
// f(…)`, `const x = f(…)`), where the callee has a compiled summary —
// or, where the callee's own build is in flight, the havoc floor.
// Declines where the callee resolves to nothing, where an argument does
// not lower, or where an assignment's target has no slot — and the
// statement then takes the inlining route, exactly as before.
func SummaryCallStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	// `f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if ast.IsCallExpression(e) || ast.IsNewExpression(e) {
			return SummaryCallOrHavoc(context, e, -1)
		}
	}
	target, rhs, ok := callAssignmentShapeOf(context, statement)
	if !ok {
		return nil, false
	}
	head := Unwrapped(rhs)
	if !ast.IsCallExpression(head) && !ast.IsNewExpression(head) {
		return nil, false
	}
	return SummaryCallOrHavoc(context, head, target)
}

// callAssignmentShapeOf is the `let x = e` / `x = e` shape both call
// routes read: the tracked target slot and the right side. Declines
// where the target has no slot.
func callAssignmentShapeOf(context *LoweringContext, statement *ast.Node) (target int, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return 0, nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return 0, nil, false
		}
		index, found := IndexOf(context, d.Name())
		if !found {
			return 0, nil, false
		}
		return index, d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return 0, nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return 0, nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return 0, nil, false
	}
	index, found := IndexOf(context, bin.Left)
	if !found {
		return 0, nil, false
	}
	return index, bin.Right, true
}
