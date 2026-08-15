// split from ir_summary_call.go — the body-local closure's blob, its call statement, and the name resolution both read

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

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
// CLOSURE NODE the name was declared to hold — the function-like node
// the layout, the compile, and the write census all need. The census
// (closureAssignedNames) collects a closure's own top-level writes only
// when handed the function-like itself; a bare body block reads as "no
// closures inside", the empty set that let a stored closure's writes
// pass unhavocked.
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

