// split from ir_callback_summary.go — the CONVERSION: the per-site blob
// cache, the closure conversion that compiles an arrow under a site's
// entry layout, and the one call statement a converted callback takes
package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arrowBlobEntry is one arrow's compiled answer. Ok false is a
// remembered decline, the same discipline summary_registry.go keeps for
// declarations.
type arrowBlobEntry struct {
	Blob       kernelbridge.SummaryBlob
	Lowered    LoweredSummary
	Parameters []parameterSlotSort
	Captures   []capturedSlot
	Ok         bool
}

// arrowBlobs keys per-SITE blobs by the arrow's own node. An arrow is
// not a declaration the contract registry knows — it exists at exactly
// one call site and its capture layout belongs to the body that spells
// it — so it is deliberately NOT stored in the declaration-keyed
// registry. Node identity is the key: the same arrow node re-lowered
// (a body compiled twice) answers the blob it already built.
var (
	arrowBlobsMu sync.Mutex
	arrowBlobs   = map[*ast.Node]arrowBlobEntry{}
)

// convertedArrow is a converted callback: the blob its summary compiled
// to, the slot layout the entries follow, and the caller slots the
// capture entries read.
type convertedArrow struct {
	Node    *ast.Node
	Blob    kernelbridge.SummaryBlob
	Lowered LoweredSummary
	// Entries: what each DECLARED parameter entry is filled with, in
	// declaration order — the same layout the blob was compiled under.
	Entries      []callbackEntry
	CaptureSlots []int
}

// convertArrow closure-converts an arrow argument and compiles it: scan
// the free names, resolve each to a caller slot, lower the body with the
// declared parameters wearing the site's element sort and the captures
// laid out after them, and ask the kernel to compile it — exactly as
// buildSummaryBlob does for a declaration, through
// kernelbridge.AskSummarize with the lowering's own slot count as the
// arity and the table its call statements built.
//
// The blob is remembered under the arrow NODE, not in the declaration-
// keyed registry: an arrow belongs to its one site.
//
// One entry per declared parameter — arrowParameterSorts reads the
// parameter sorts straight off entries, which is only true where the
// count agrees. convertArrowWithEntryLayout is the general core this
// wraps, for a site (the reduce accumulator) whose entry vector runs
// wider than the declared parameter count and supplies its own sorts.
func convertArrow(context *LoweringContext, argument *ast.Node, entries []callbackEntry) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	if _, ok := arrowParameterNames(arrow); !ok {
		return convertedArrow{}, false
	}
	if len(entries) != len(arrow.Parameters()) {
		return convertedArrow{}, false
	}
	return convertArrowWithEntryLayout(context, argument, entries, arrowParameterSorts(entries))
}

// convertArrowWithEntryLayout is convertArrow's shared core: the site
// hands in BOTH the flattened entry vector (one callbackEntry per
// EFFECT position, which convertArrow's own arg loop appends verbatim)
// and the DECLARED-parameter-indexed sort vector
// (lowerArrowSummary/summaryParameterEntries reads it by declaration
// position, never by entry position) — the two run the same length only
// where every parameter fills one entry, which convertArrow enforces
// before calling in. A site whose accumulator parameter is array-shaped
// (convertReduceArrow) supplies a wider entries vector (len/elem pair)
// beside the NARROWER, declared-length parameterSorts (one row per
// parameter, Array marking the pair), and calls this directly.
func convertArrowWithEntryLayout(
	context *LoweringContext,
	argument *ast.Node,
	entries []callbackEntry,
	parameterSorts []parameterSlotSort,
) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	if context.Flow == nil {
		return convertedArrow{}, false
	}
	if _, ok := arrowParameterNames(arrow); !ok {
		return convertedArrow{}, false
	}
	if len(parameterSorts) != len(arrow.Parameters()) {
		return convertedArrow{}, false
	}
	captures, captureSlots, ok := capturesOf(context, scanFreeNames(arrow))
	if !ok {
		return convertedArrow{}, false
	}
	arrowBlobsMu.Lock()
	held, has := arrowBlobs[arrow]
	arrowBlobsMu.Unlock()
	if has {
		if !held.Ok {
			return convertedArrow{}, false
		}
		// the remembered blob was compiled against ITS entry layout — the
		// parameter sorts the site supplied AND the captures it resolved. A
		// re-lowering that resolved a different one is a different arrow's
		// worth of entries, so it declines rather than reusing the blob.
		if !sameParameterSorts(held.Parameters, parameterSorts) || !sameCaptures(held.Captures, captures) {
			return convertedArrow{}, false
		}
		return convertedArrow{
			Node: arrow, Blob: held.Blob, Lowered: held.Lowered,
			Entries: entries, CaptureSlots: captureSlots,
		}, true
	}
	lowered, loweredOk := lowerArrowSummary(context.Flow, arrow, parameterSorts, captures)
	if !loweredOk {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	arrowBlobsMu.Lock()
	arrowBlobs[arrow] = arrowBlobEntry{
		Blob:       blob,
		Lowered:    lowered,
		Parameters: parameterSorts,
		Captures:   captures,
		Ok:         true,
	}
	arrowBlobsMu.Unlock()
	return convertedArrow{
		Node: arrow, Blob: blob, Lowered: lowered,
		Entries: entries, CaptureSlots: captureSlots,
	}, true
}

// sameParameterSorts is whether two declared-parameter layouts agree
// entry for entry — the other half of the check a remembered blob's
// reuse turns on. A blob compiled with a number-sorted first entry may
// not be reused at a site whose element slot is a string.
func sameParameterSorts(held []parameterSlotSort, wanted []parameterSlotSort) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Sort != wanted[index].Sort || held[index].TypeofTag != wanted[index].TypeofTag {
			return false
		}
	}
	return true
}

// sameCaptures is whether two capture layouts agree name for name in
// order — the check a remembered blob's reuse turns on.
//
// WRITTEN is compared beside the name and the sort, and it has to be: a
// blob compiled when a capture was read-only carries NO bundle row for
// it, so a site that reused that blob while treating the capture as
// written would map a write-back through a row the compile never made —
// the caller would keep believing a slot the closure moved. The flag is
// part of the entry layout, so it is part of the identity.
//
// AN OBJECT capture's LEAF VOCABULARY is compared the same way and for
// the same reason, one step down: the member list IS the entry layout
// for that capture, so a blob compiled against three leaves may not be
// reused where the caller's flattening now offers two — entry k would
// take a value belonging to entry j. Each leaf's own Sort and Written
// join the comparison beside its name, because each is what its entry
// and its row were compiled under. A scalar capture and an object
// capture under one name are different layouts and differ here on the
// member count.
func sameCaptures(held []capturedSlot, wanted []capturedSlot) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Name != wanted[index].Name ||
			held[index].Sort != wanted[index].Sort ||
			held[index].Written != wanted[index].Written {
			return false
		}
		if len(held[index].Members) != len(wanted[index].Members) {
			return false
		}
		for leaf := range held[index].Members {
			if held[index].Members[leaf].Member != wanted[index].Members[leaf].Member ||
				held[index].Members[leaf].Sort != wanted[index].Members[leaf].Sort ||
				held[index].Members[leaf].TypeofTag != wanted[index].Members[leaf].TypeofTag ||
				held[index].Members[leaf].Written != wanted[index].Members[leaf].Written {
				return false
			}
		}
	}
	return true
}

// arrowCallStatement builds the ONE call statement a converted arrow
// takes: the declared parameter entries take the effects the site's
// entry layout supplies, the capture entries take a `var` of each
// captured caller slot, every remaining entry enters absent with the
// done flag at {0} — exactly the entry states applySummary sends, so a
// spliced compile and a direct apply agree — and Rets maps the arrow's
// #ret out-state onto `target`, or nothing where target is -1.
func arrowCallStatement(
	context *LoweringContext,
	converted convertedArrow,
	target int,
) (kernelbridge.IrStatement, bool) {
	if context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	lowered := converted.Lowered
	// entries run ONE PER SLOT the declared parameters fill, not one per
	// declared parameter — an array-shaped accumulator (convertReduceArrowArray)
	// widens its own declared position into a len/elem pair, so entries
	// can outrun converted.Node.Parameters() by exactly as many pairs as
	// the compile itself laid out. lowered.ParamCount is the layout's own
	// count of those slots (ir_summary_body_lowering.go's ParamCount
	// comment), so it — not the declared count — is what entries must
	// agree with.
	filled := len(converted.Entries)
	if filled+len(converted.CaptureSlots) != lowered.ParamCount {
		return kernelbridge.IrStatement{}, false
	}
	args := make([]kernelbridge.LoopEffect, 0, lowered.SlotCount)
	// the declared parameters, each the effect the site's layout laid out
	// for it — the element or value the traversal hands each pass, the
	// index or key beside it, and absent where the site holds nothing.
	// The blob was compiled under these same entries' sorts.
	for _, entry := range converted.Entries {
		args = append(args, entry.Effect)
	}
	// the capture entries, in the scan's order
	for _, slot := range converted.CaptureSlots {
		args = append(args, varStateEffect(slot))
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
	rets := make([]int, lowered.RetIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[lowered.RetIndex] = target
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(converted.Node, converted.Blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// arrowCallStatementArrayResult is arrowCallStatement for a converted
// arrow whose OWN return is array-shaped (converted.Lowered.RetShape ==
// RetShapeArray, laid out by the arrow body's own returnedLiteralShape
// reading) — the accumulator update `acc.concat(x)` inside a REDUCE
// callback over an array-typed accumulator, whose result is itself a
// length and a joined element rather than one scalar.
//
// Where arrowCallStatement maps the callback's scalar #ret onto one
// target slot, this maps the callback's OWN "#ret.len"/"#ret.elem" out-
// states onto the caller's targetLen/targetElem — Rets is keyed by
// callee OUT-STATE slot index (kernelbridge.IrStatement's own doc: "per
// callee out-state, the caller binding it writes"), so each of the
// callback's three exits (scalar #ret, #ret.len, #ret.elem) maps to its
// own caller slot independently; the scalar #ret is left unmapped (-1)
// since nothing here reads it.
func arrowCallStatementArrayResult(
	context *LoweringContext,
	converted convertedArrow,
	targetLen int,
	targetElem int,
) (kernelbridge.IrStatement, bool) {
	if converted.Lowered.RetShape != RetShapeArray {
		return kernelbridge.IrStatement{}, false
	}
	lenIndex, lenOk := RetMemberSlotOfLowered(converted.Lowered, "len")
	elemIndex, elemOk := RetMemberSlotOfLowered(converted.Lowered, "elem")
	if !lenOk || !elemOk {
		return kernelbridge.IrStatement{}, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return kernelbridge.IrStatement{}, false
	}
	top := lenIndex
	if elemIndex > top {
		top = elemIndex
	}
	for len(call.Rets) <= top {
		call.Rets = append(call.Rets, -1)
	}
	call.Rets[lenIndex] = targetLen
	call.Rets[elemIndex] = targetElem
	return call, true
}

// RetMemberSlotOfLowered is RetMemberSlotOf read off a COMPILED
// LoweredSummary's own RetMembers rather than a live LoweringContext's —
// the callback's row list, read by the OUTER call statement that splices
// its blob, not by a lowering still building one.
func RetMemberSlotOfLowered(lowered LoweredSummary, member string) (int, bool) {
	for _, entry := range lowered.RetMembers {
		if entry.Name == member {
			return entry.Index, true
		}
	}
	return 0, false
}

// convertArrayArrow converts a callback under the ARRAY entry layout —
// the element, the index, then absent. The arrow's own parameter count
// decides how many entries the layout spells, so a one-parameter
// callback takes exactly the element and a three-parameter one takes the
// array position as absent.
func convertArrayArrow(context *LoweringContext, argument *ast.Node, elementSlot int) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	entries := arrayCallbackEntries(context, len(arrow.Parameters()), elementSlot)
	return convertArrow(context, argument, entries)
}

// convertReduceArrow converts a callback under REDUCE's entry layout —
// the accumulator, then the element, then the index, then absent. Every
// slot the array layout spells shifts one to the right, which is the
// same shift ArrayCallbackPins applies for `reduce` on the other
// callback-binding path; the two routes agree on the layout because
// they agree on the reason for it, which is `reduce`'s own signature
// (accumulator, element, index, array).
func convertReduceArrow(
	context *LoweringContext,
	argument *ast.Node,
	elementSlot int,
	accumulator kernelbridge.LoopEffect,
	accumulatorSort BindingKind,
	accumulatorTypeof TypeofTag,
) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	declared := len(arrow.Parameters())
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = callbackEntry{
				Effect: accumulator,
				Sort:   accumulatorSort,
				Typeof: accumulatorTypeof,
			}
		case 1:
			entries[index] = slotCallbackEntry(context, elementSlot)
		case 2:
			entries[index] = indexCallbackEntry()
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return convertArrow(context, argument, entries)
}

// convertReduceArrowArray converts a callback under REDUCE's entry
// layout where the ACCUMULATOR (declared parameter 0) is itself
// ARRAY-shaped — the len/elem PAIR ir_array_slots.go's local carries,
// not one scalar. The element and index entries shift exactly as
// convertReduceArrow's do; only entry 0 widens, into two.
//
// accumulatorLen/accumulatorElem are the two effects entry 0 splits
// into — the join the caller already built for each half (mirroring
// convertReduceArrow's single accumulator join, one join per slot).
// elementSort/elementTypeof describe the ELEM half only; the len half
// is always number (ir_array_slots.go's own convention — every
// flattened array's length slot is number-sorted).
func convertReduceArrowArray(
	context *LoweringContext,
	argument *ast.Node,
	elementSlot int,
	accumulatorLen kernelbridge.LoopEffect,
	accumulatorElem kernelbridge.LoopEffect,
	elementSort BindingKind,
	elementTypeof TypeofTag,
) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	declared := len(arrow.Parameters())
	if declared == 0 {
		// reduce always declares at least the accumulator — an arrow with
		// none has no position for the pair to fill
		return convertedArrow{}, false
	}
	parameterSorts := make([]parameterSlotSort, declared)
	parameterSorts[0] = parameterSlotSort{Sort: elementSort, TypeofTag: elementTypeof, Array: true}
	var entries []callbackEntry
	entries = append(entries,
		callbackEntry{Effect: accumulatorLen, Sort: BindingKindNumber, Typeof: TypeofTagNumber},
		callbackEntry{Effect: accumulatorElem, Sort: elementSort, Typeof: elementTypeof},
	)
	for index := 1; index < declared; index++ {
		switch index {
		case 1:
			entries = append(entries, slotCallbackEntry(context, elementSlot))
			parameterSorts[index] = parameterSlotSort{
				Sort: context.Sorts[elementSlot], TypeofTag: context.Typeofs[elementSlot],
			}
		case 2:
			entries = append(entries, indexCallbackEntry())
			parameterSorts[index] = parameterSlotSort{Sort: BindingKindNumber, TypeofTag: TypeofTagNumber}
		default:
			entries = append(entries, absentCallbackEntry())
			parameterSorts[index] = parameterSlotSort{Sort: BindingKindUnknown, TypeofTag: TypeofTagNone}
		}
	}
	return convertArrowWithEntryLayout(context, argument, entries, parameterSorts)
}
