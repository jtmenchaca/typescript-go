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
	if !ast.IsCallExpression(call) {
		return nil
	}
	return context.ResolveCallee(call.AsCallExpression().Expression)
}

// SummaryCallOrHavoc is the one call-lowering door every route uses,
// in three tiers:
//
//	1. a callee with a compiled summary → the CALL STATEMENT, which
//	   splices the callee's own compiled program;
//	2. a callee whose own build is STILL IN FLIGHT — a recursive call,
//	   which cannot splice itself → the cycle havoc, which is the one
//	   tier that must never ask the registry for a shape;
//	3. every OTHER callee — one that does not resolve at all, or
//	   resolves with no blob (a generator, a declined body, an
//	   unmodeled library function, `x.y.then(cb)`) → the OPAQUE CALL
//	   HAVOC (ir_opaque_havoc.go): the target takes `unknown` and so
//	   does every flattened-local leaf the receiver or the arguments
//	   mentioned.
//
// Tier 3 is what stops an unreadable call from declining the whole body.
// It computes its slot set through the SAME enumerator the statement
// floor uses, so "which slots could this have written" is answered once
// in the adapter and never twice differently.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads.
func SummaryCallOrHavoc(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if statement, ok := summaryCallStatement(context, call, target); ok {
		return []kernelbridge.IrStatement{statement}, true
	}
	if cycled, ok := summaryCycleHavoc(context, call, target); ok {
		return withReceiverBundleHavoc(context, call, cycled)
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
	callExpr := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpr.Arguments != nil {
		callArguments = callExpr.Arguments.Nodes
	}
	parameters := callee.Parameters()
	if len(callArguments) > len(parameters) {
		return kernelbridge.IrStatement{}, false
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
			for range census.Reads {
				args = append(args, kernelbridge.AbsentConst())
			}
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
	rets := make([]int, outIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[outIndex] = target
	}
	// the RECEIVER decides the this-entry fill: the callee's own
	// "this.<field>" entries take the caller's "<receiverPath>.<field>"
	// slots, and the ones it writes ride back out through rets
	if !bundleRetsAndArgs(context, call, calleeShape, args, rets) {
		return kernelbridge.IrStatement{}, false
	}
	// a class-typed PARAMETER's bundle rows fill the same way, one step
	// over: the ARGUMENT's spelled path prefixes the field name
	if !bundleParamRetsAndArgs(context, call, callee, calleeShape, args, rets) {
		return kernelbridge.IrStatement{}, false
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(callee, blob),
		Args:   args,
		Rets:   rets,
	}, true
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
// class-typed PARAMETER bundles. A "this."-rooted row is filled from the
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
		if ast.IsCallExpression(e) {
			return SummaryCallOrHavoc(context, e, -1)
		}
	}
	target, rhs, ok := callAssignmentShapeOf(context, statement)
	if !ok {
		return nil, false
	}
	head := Unwrapped(rhs)
	if !ast.IsCallExpression(head) {
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
