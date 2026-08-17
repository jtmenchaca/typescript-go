// The RETURN-position callback route's own gates, named one at a time:
// which of the several conditions SummaryCallbackReturnOf turns on is
// the one a given source shape fails.
//
// These probe the route with the caller layout handed in directly, so
// they say WHICH gate declined rather than only that the body did.
// Nothing here compiles a blob, so no kernel is needed for the decline
// direction.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// callbackReturnContext is loweringResultContext with a FLATTENED array
// laid out under `names` — the two ".len"/".elem" slots a receiver has
// to resolve for the route to read it at all.
func callbackReturnContext(arrays []string, scalars []string, scalarSorts []BindingKind, elementSort BindingKind) *LoweringContext {
	var bindings []string
	var sorts []BindingKind
	for _, name := range arrays {
		bindings = append(bindings, name+arrayLenSuffix, name+arrayElemSuffix)
		sorts = append(sorts, BindingKindNumber, elementSort)
	}
	bindings = append(bindings, scalars...)
	sorts = append(sorts, scalarSorts...)
	return loweringResultContext(bindings, sorts)
}

func TestCallbackReturn_AFlattenedArrayParameterReceiverPassesTheReceiverGate(t *testing.T) {
	// the premise a brief stated — that these bodies decline because the
	// receiver is not a flattened LOCAL — is not the gate. A flattened
	// ARRAY PARAMETER lays out exactly the same two slots (ArrayParameterOf,
	// ir_array_parameters.go), and arraySlotsOf reads them by NAME with no
	// question about where they came from. So the receiver resolves.
	context := callbackReturnContext(
		[]string{"ids"}, nil, nil, BindingKindNumber)
	if _, _, ok := arraySlotsOf(context, "ids"); !ok {
		t.Fatalf("arraySlotsOf(ids) declined — the two slots are laid out under that name")
	}
}

func TestCallbackReturn_AFlattenedARRAYCaptureResolvesAsItsLenElemBundle(t *testing.T) {
	// Sankey's getSumOfIds shape, reduced to its formerly-blocking part:
	//
	//   const getSumOfIds = (links: ReadonlyArray<L>, ids: number[]) =>
	//     ids.reduce((result, id) => result + getValue(links[id]), 0);
	//
	// `links` is a flattened array parameter — the caller holds
	// "links.len"/"links.elem" and no bare-name slot. capturesOf now
	// captures it as the two-leaf bundle, so the arrow's own element
	// reads resolve against the same pair the caller holds.
	arrow := callbackArrowOf(t, `ids.map(id => links[id]);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames declined — the scan itself admits an index read of a capture")
	}
	if len(scan.Reads) != 1 || scan.Reads[0] != "links" {
		t.Fatalf("free reads = %v, want [links]", scan.Reads)
	}
	context := callbackReturnContext(
		[]string{"ids", "links"}, nil, nil, BindingKindNumber)
	captures, slots, ok := capturesOf(context, scan)
	if !ok {
		t.Fatalf("a flattened-array capture declined — the len/elem bundle arm serves it now")
	}
	if len(captures) != 1 || captures[0].Name != "links" || len(captures[0].Members) != 2 ||
		captures[0].Members[0].Member != "len" || captures[0].Members[1].Member != "elem" {
		t.Fatalf("captures = %+v, want one `links` bundle of len and elem", captures)
	}
	if len(slots) != 2 {
		t.Errorf("slots = %v, want one per leaf entry — the lockstep arrowCallStatement binds by", slots)
	}
}

func TestCallbackReturn_ACallbackCallingAModuleLevelFunctionDeclinesAtTheCAPTUREGate(t *testing.T) {
	// the second blocking free name in the same Sankey bodies: `getValue`
	// and `centerY` are module-level consts holding arrows. They are
	// CALLEES, not values, and the enclosing body's slot vector holds
	// nothing under either name.
	arrow := callbackArrowOf(t, `ids.map(id => getValue(id));`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames declined")
	}
	if len(scan.Reads) != 1 || scan.Reads[0] != "getValue" {
		t.Fatalf("free reads = %v, want [getValue] — a callee name is reported as a free read", scan.Reads)
	}
	context := callbackReturnContext([]string{"ids"}, nil, nil, BindingKindNumber)
	if _, _, ok := capturesOf(context, scan); ok {
		t.Errorf("a module-level callee resolved as a capture — it has no caller slot")
	}
}

func TestCallbackReturn_TheSankeyReduceShapeLowersOnceItsCapturesHaveSlots(t *testing.T) {
	// the SAME `return ids.reduce(cb, 0)` shape, with every free name the
	// callback reads given an ordinary scalar slot. Every other gate the
	// route applies — the two-argument reduce reader, the flattened
	// receiver, the seed's sort against the ret slot's, the callback's
	// conversion, the call statement — passes. So capturesOf is the ONE
	// condition standing between these bodies and a lowering, and the
	// receiver named in the brief was never it.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext(
		[]string{"ids"}, []string{"weight"}, []BindingKind{BindingKindNumber}, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	statements := loweringParse(t, `return ids.reduce((result, id) => result + id + weight, 0);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	lowered, ok := SummaryCallbackReturnOf(context, head)
	if !ok {
		t.Fatalf("the reduce shape declined even with every capture slotted — a gate other than capturesOf is also closed")
	}
	if len(lowered) != 2 {
		t.Fatalf("len(lowered) = %d, want 2 — the seed write then the fold's call: %+v", len(lowered), lowered)
	}
	if lowered[0].Kind != kernelbridge.IrStatementAssign || lowered[0].Target != context.Result.Ret {
		t.Errorf("lowered[0] = %+v, want the seed written into the result slot", lowered[0])
	}
	if lowered[1].Kind != kernelbridge.IrStatementCall {
		t.Errorf("lowered[1].Kind = %v, want a call — the converted callback applied at the element join", lowered[1].Kind)
	}
	if len(lowered[1].Rets) == 0 || lowered[1].Rets[len(lowered[1].Rets)-1] != context.Result.Ret {
		t.Errorf("lowered[1].Rets = %v, want the callback's #ret mapped onto the result slot", lowered[1].Rets)
	}
}

func TestCallbackReturn_TheSeedlessOneArgumentReduceLowersWithTheElementAsTheAccumulatorsStart(t *testing.T) {
	// Text.tsx's findLongestLine: `words.reduce((a, b) => a.width > b.width ?
	// a : b)` — no seed, the array's own first element standing in for pass
	// zero. reduceCallOf used to refuse this shape outright (a doc comment
	// argued the empty-array-throws case was unmodeled); the fix reads it as
	// an ACCUMULATOR SEEDED FROM THE ELEMENT SLOT, on the same "on an empty
	// array the call throws and no later statement runs" stance the lowering
	// already takes toward every callee that may throw.
	//
	// Reduced to a SCALAR element here — a `number[]` receiver — since the
	// record-typed Text.tsx case additionally needs destructured callback
	// parameters and record elements, which are a sibling agent's territory.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"xs"}, nil, nil, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	statements := loweringParse(t, `return xs.reduce((a, b) => a > b ? a : b);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	lowered, ok := SummaryCallbackReturnOf(context, head)
	if !ok {
		t.Fatalf("the one-argument reduce declined — want it to lower with the element as the accumulator's start")
	}
	if len(lowered) != 2 {
		t.Fatalf("len(lowered) = %d, want 2 — the element-seeded write then the fold's call: %+v", len(lowered), lowered)
	}
	if lowered[0].Kind != kernelbridge.IrStatementAssign || lowered[0].Target != context.Result.Ret {
		t.Errorf("lowered[0] = %+v, want the source's element written into the result slot", lowered[0])
	}
	if lowered[1].Kind != kernelbridge.IrStatementCall {
		t.Errorf("lowered[1].Kind = %v, want a call — the converted callback applied at the element join", lowered[1].Kind)
	}
	if len(lowered[1].Rets) == 0 || lowered[1].Rets[len(lowered[1].Rets)-1] != context.Result.Ret {
		t.Errorf("lowered[1].Rets = %v, want the callback's #ret mapped onto the result slot", lowered[1].Rets)
	}
}

func TestCallbackReturn_AnArrayAnnotatedAccumulatorNowDeclinesOutrightOnceConcatFlattensIt(t *testing.T) {
	// Text.tsx's second reduce shape, reduced to scalars:
	// `xs.reduce((acc: number[], x) => acc.concat(x), [])`.
	//
	// RE-DIAGNOSED from this pin's earlier reading (which asserted a POROUS
	// lowering — ok=true, an unknown-valued result write). `.concat` is now
	// a recognized array use (concatCallOf, wired into
	// usesAreAllArrayFormsFrom, ir_array_use_scan.go — landed by a sibling
	// wave since this pin was first written), so arrayParamSlotsIn now
	// FLATTENS `acc`: the array-parameter branch's own refusal
	// (ir_summary_body_lowering_parameters.go, "an array parameter of an
	// arrow argument") is reached for real.
	//
	// This agent's own widening (parameterSlotSort.Array,
	// convertReduceArrowArray, arrowCallStatementArrayResult) gives that
	// refusal an escape — an arrow site that marks its entry 0 Array now
	// lays out the pair instead of refusing — but the escape only fires
	// where the OUTER return slot already carries a "#ret.len"/"#ret.elem"
	// pair (retPairSlotsOf), which is allocated by returnedLiteralShape's
	// isArrayProducingCollectionCall (ir_summary_returned_shape.go, NOT
	// this agent's territory) recognizing the call as array-producing.
	// That reader knows "map"/"filter" only — "reduce" is unlisted — so on
	// THIS harness (callbackReturnContext lays out no such pair at all,
	// matching what a live "reduce" return gets today) the gate stays
	// closed: reduceArrayAccumulatorSlotStatements declines cleanly
	// (TestCallbackReturn_TheArrayAccumulatorRouteDeclinesWithNoRetPair
	// pins that decline directly), and reduceSlotStatements falls through
	// to the ordinary scalar path — which now hits the array-parameter
	// refusal square on, since `acc` flattens and the scalar path's
	// parameterSorts entry carries no Array marking. The whole return
	// declines outright.
	//
	// The remaining fix is the one-line "reduce" addition to
	// isArrayProducingCollectionCall — outside this agent's exclusive
	// files (ir_summary_body.go / ir_callback_convert.go /
	// ir_callback_return.go / ir_summary_body_lowering_parameters.go /
	// this test file) — plus a matching array-producing recognition for
	// `.concat` as the CALLBACK's own returned shape (so the callback's
	// own compiled summary carries a RetShapeArray pair
	// arrowCallStatementArrayResult can map out), which is the same
	// sibling file's `isArrayProducingCollectionCall`, read from inside
	// the callback body rather than the outer one.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"xs"}, nil, nil, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	statements := loweringParse(t, `return xs.reduce((acc: number[], x) => acc.concat(x), []);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	_, ok := SummaryCallbackReturnOf(context, head)
	if ok {
		t.Fatalf("the array-annotated accumulator lowered — want an outright decline: `.concat` now flattens `acc` (concatCallOf), and with no #ret.len/#ret.elem pair laid out (isArrayProducingCollectionCall has no \"reduce\" case) the scalar fallback hits the array-parameter-of-an-arrow-argument refusal")
	}
}

func TestCallbackReturn_TheArrayAccumulatorRouteDeclinesWithNoRetPair(t *testing.T) {
	// reduceArrayAccumulatorSlotStatements's own middle gate, isolated:
	// the callback's accumulator reads as array-typed and the seed reads
	// as the empty-literal pair, but retPairSlotsOf finds no
	// "#ret.len"/"#ret.elem" rows in this harness's layout (nothing lays
	// them out for a plain scalar #ret context) — the same shape a live
	// `return xs.reduce(...)` gets today, since returnedLiteralShape's
	// isArrayProducingCollectionCall does not admit "reduce".
	context := callbackReturnContext([]string{"xs"}, nil, nil, BindingKindNumber)
	context.SummaryTable = &SummaryTableBuilder{}
	statements := loweringParse(t, `return xs.reduce((acc: number[], x) => acc.concat(x), []);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	reduceSource, seed, readOk := reduceCallOf(head)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	if _, _, ok := retPairSlotsOf(context); ok {
		t.Fatalf("retPairSlotsOf resolved a pair — this harness never lays one out; the premise of this pin no longer holds")
	}
	if _, ok := reduceArrayAccumulatorSlotStatements(context, reduceSource, seed, context.Result.Ret); ok {
		t.Errorf("reduceArrayAccumulatorSlotStatements lowered with no ret pair to write into")
	}
}

func TestCallbackReturn_TheArrayAccumulatorRouteLowersOnceARetPairAndAnArrayShapedCallbackReturnAreBothLaidOut(t *testing.T) {
	// proves the PLUMBING this agent built end to end, independent of the
	// two sibling gates TestCallbackReturn_AnArrayAnnotatedAccumulator…
	// names as the remaining work: with a "#ret.len"/"#ret.elem" pair
	// hand-laid (standing in for what returnedLiteralShape would allocate
	// once isArrayProducingCollectionCall admits "reduce") and a callback
	// whose OWN return is a bare array literal `[x]` — already
	// array-shaped by the EXISTING arrayRetMembersOf reader, no sibling
	// fix needed for the callback's own RetShapeArray — the whole chain
	// (parameterSlotSort.Array widening the layout,
	// convertReduceArrowArray compiling the pair entry,
	// arrowCallStatementArrayResult mapping the callback's own pair back
	// onto the caller's) lowers for real.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"xs"}, nil, nil, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	// lay out the pair a body whose OWN return were recognized
	// array-producing would carry — standing in for the sibling fix
	context.Bindings = append(context.Bindings, retLenSlotName(), retElemSlotName())
	context.Sorts = append(context.Sorts, BindingKindNumber, BindingKindUnknown)
	context.Typeofs = append(context.Typeofs, TypeofTagNumber, TypeofTagNone)
	statements := loweringParse(t, `return xs.reduce((acc: number[], x) => [x], []);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	reduceSource, seed, readOk := reduceCallOf(head)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	lenSlot, elemSlot, pairOk := retPairSlotsOf(context)
	if !pairOk {
		t.Fatalf("retPairSlotsOf declined — the hand-laid pair should resolve")
	}
	lowered, ok := reduceArrayAccumulatorSlotStatements(context, reduceSource, seed, context.Result.Ret)
	if !ok {
		t.Fatalf("reduceArrayAccumulatorSlotStatements declined — want the pair path to lower with both gates cleared")
	}
	if len(lowered) != 3 {
		t.Fatalf("len(lowered) = %d, want 3 — the len seed write, the elem seed write, then the call: %+v", len(lowered), lowered)
	}
	if lowered[0].Kind != kernelbridge.IrStatementAssign || lowered[0].Target != lenSlot {
		t.Errorf("lowered[0] = %+v, want the len slot seeded to 0", lowered[0])
	}
	if lowered[1].Kind != kernelbridge.IrStatementAssign || lowered[1].Target != elemSlot {
		t.Errorf("lowered[1] = %+v, want the elem slot seeded absent", lowered[1])
	}
	if lowered[2].Kind != kernelbridge.IrStatementCall {
		t.Fatalf("lowered[2].Kind = %v, want a call", lowered[2].Kind)
	}
	// Rets is indexed by the CALLEE's own out-state slot, not the
	// caller's — each of the caller's lenSlot/elemSlot must appear
	// exactly once among the mapped (non -1) entries, proving both of
	// the callback's own array-shaped exits landed somewhere.
	mapsTo := func(caller int) bool {
		for _, ret := range lowered[2].Rets {
			if ret == caller {
				return true
			}
		}
		return false
	}
	if !mapsTo(lenSlot) {
		t.Errorf("lowered[2].Rets = %v, want the callee's own len exit mapped onto the caller's len slot %d", lowered[2].Rets, lenSlot)
	}
	if !mapsTo(elemSlot) {
		t.Errorf("lowered[2].Rets = %v, want the callee's own elem exit mapped onto the caller's elem slot %d", lowered[2].Rets, elemSlot)
	}
}

func TestCallbackReturn_ArrayLiteralPairSeedOfReadsOnlyTheEmptyLiteral(t *testing.T) {
	// the one seed shape the array-accumulator route spells: `[]`, read as
	// length exactly 0 and an absent element (nothing has ever been
	// written). Any other seed — non-empty, a name, a call — declines,
	// since nothing else claims a length or an element set this route can
	// stand behind.
	empty := loweringParse(t, `xs.reduce(cb, []);`)
	emptyCall := Unwrapped(empty[0].AsExpressionStatement().Expression).AsCallExpression()
	lenEffect, elemEffect, ok := arrayLiteralPairSeedOf(emptyCall.Arguments.Nodes[1])
	if !ok {
		t.Fatalf("arrayLiteralPairSeedOf declined the empty literal `[]`")
	}
	if lenEffect.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("lenEffect.Kind = %v, want a const — the empty literal's length is exactly 0", lenEffect.Kind)
	}
	if elemEffect.Kind != kernelbridge.LoopEffectConstState || !elemEffect.Undef {
		t.Errorf("elemEffect = %+v, want AbsentConst's own shape (ConstState, Undef true) — no element has ever been written", elemEffect)
	}
	nonEmpty := loweringParse(t, `xs.reduce(cb, [1]);`)
	nonEmptyCall := Unwrapped(nonEmpty[0].AsExpressionStatement().Expression).AsCallExpression()
	if _, _, ok := arrayLiteralPairSeedOf(nonEmptyCall.Arguments.Nodes[1]); ok {
		t.Errorf("arrayLiteralPairSeedOf accepted a non-empty literal — the route spells only the empty one")
	}
	named := loweringParse(t, `xs.reduce(cb, seed);`)
	namedCall := Unwrapped(named[0].AsExpressionStatement().Expression).AsCallExpression()
	if _, _, ok := arrayLiteralPairSeedOf(namedCall.Arguments.Nodes[1]); ok {
		t.Errorf("arrayLiteralPairSeedOf accepted a named seed — only a literal is read")
	}
}

func TestCallbackReturn_TheConvertedCallbackIsCompiledWithASortedAccumulatorEntry(t *testing.T) {
	// what the sort is FOR: the entry the callback's first parameter is
	// compiled under. An unknown-sorted entry leaves `result + id`
	// reading its accumulator through no sort at all, which is what
	// arithmetic declines on.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"ids"}, nil, nil, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	statements := loweringParse(t, `return ids.reduce((result, id) => result + id, 0);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	reduceSource, seed, readOk := reduceCallOf(head)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	_, sourceElem, sourceOk := arraySlotsOf(context, reduceSource.Receiver)
	if !sourceOk {
		t.Fatalf("the receiver's slots did not resolve")
	}
	seedEffect, seedSort, seedOk := seedEffectOf(context, seed)
	if !seedOk {
		t.Fatalf("the seed did not read")
	}
	if seedSort != BindingKindNumber {
		t.Fatalf("seedSort = %q, want %q — `0` is a spelled number", seedSort, BindingKindNumber)
	}
	accumulator := joinEffect(seedEffect, varEffect(context.Result.Ret))
	converted, convertedOk := convertReduceArrow(
		context, reduceSource.Callback, sourceElem,
		accumulator,
		accumulatorSortOfResultSlot(context, context.Result.Ret, seedSort),
		TypeofTagNone,
	)
	if !convertedOk {
		t.Fatalf("the callback declined conversion")
	}
	if len(converted.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2 — the accumulator and the element", len(converted.Entries))
	}
	if converted.Entries[0].Sort != BindingKindNumber {
		t.Errorf("the accumulator entry's sort = %q, want %q — the seed's own sort, not the result slot's placeholder",
			converted.Entries[0].Sort, BindingKindNumber)
	}
}

func TestCallbackReturn_TheResultSlotsUnknownSortStillPromisesTheSeedsOwnSort(t *testing.T) {
	// "#ret" is laid out BindingKindUnknown unconditionally, so reading
	// the named route's gate literally against it made the accumulator
	// entry unknown-sorted for EVERY return-position reduce — and an
	// unknown-sorted entry is what makes `result + x` inside the callback
	// decline its own arithmetic (arrowParameterSorts' own reason for
	// existing). The seed IS written into that slot by this route's first
	// statement, so the seed's sort is true of the join's var half.
	context := callbackReturnContext([]string{"ids"}, nil, nil, BindingKindNumber)
	if context.Sorts[context.Result.Ret] != BindingKindUnknown {
		t.Fatalf("the result slot's sort = %q, want unknown — the premise of this rule", context.Sorts[context.Result.Ret])
	}
	if got := accumulatorSortOfResultSlot(context, context.Result.Ret, BindingKindNumber); got != BindingKindNumber {
		t.Errorf("accumulator sort over an unsorted result slot = %q, want %q — the seed's own sort", got, BindingKindNumber)
	}
	if got := accumulatorSortOfResultSlot(context, context.Result.Ret, BindingKindString); got != BindingKindString {
		t.Errorf("a string seed's accumulator sort = %q, want %q", got, BindingKindString)
	}
}

func TestCallbackReturn_AnUnsortedSeedPromisesNothingAndASortedDisagreementStaysUnknown(t *testing.T) {
	// the two decline directions the widening must NOT swallow: a seed
	// whose own reading committed to no sort promises none, and a target
	// that DOES wear a sort still rules a disagreeing seed out.
	context := callbackReturnContext(
		[]string{"ids"}, []string{"tally"}, []BindingKind{BindingKindNumber}, BindingKindNumber)
	if got := accumulatorSortOfResultSlot(context, context.Result.Ret, BindingKindUnknown); got != BindingKindUnknown {
		t.Errorf("an unsorted seed's accumulator sort = %q, want unknown — nothing was promised", got)
	}
	tally, found := slotIndexOfName(context, "tally")
	if !found {
		t.Fatalf("the number-sorted scalar slot is missing from the layout")
	}
	if got := accumulatorSortOfResultSlot(context, tally, BindingKindString); got != BindingKindUnknown {
		t.Errorf("a string seed over a number-sorted slot = %q, want unknown — the sorts disagree", got)
	}
	if got := accumulatorSortOfResultSlot(context, tally, BindingKindNumber); got != BindingKindNumber {
		t.Errorf("a number seed over a number-sorted slot = %q, want %q — the sorts agree", got, BindingKindNumber)
	}
}

func TestCallbackReturn_ASeedWhoseSortTheSortedTargetRejectsStillDeclinesTheWholeRoute(t *testing.T) {
	// the widening changed only what an UNSORTED target promises; the
	// route's own decline for a sorted target the seed contradicts is
	// untouched, and this pins it through the route rather than through
	// the helper.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"ids"}, nil, nil, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	// force the result slot to a NUMBER sort, which a word seed contradicts
	context.Sorts[context.Result.Ret] = BindingKindNumber
	statements := loweringParse(t, `return ids.reduce((acc, id) => acc, "");`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	if _, ok := SummaryCallbackReturnOf(context, head); ok {
		t.Errorf("a word seed lowered into a number-sorted result slot — the sort gate is what stops it")
	}
}

func TestCallbackReturn_AReturnedMapDeclinesBeforeTheReceiverIsEvenRead(t *testing.T) {
	// Treemap's `return children.map(child => …)` shape: the route's own
	// switch has cases for `find` and `flatMap` only. `map` and `filter`
	// answer an ARRAY, and the bare result slot has no ".len"/".elem"
	// pair to write one into — so the route declines on the METHOD,
	// whatever the receiver.
	context := callbackReturnContext([]string{"children"}, nil, nil, BindingKindUnknown)
	statements := loweringParse(t, `return children.map(c => c);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	if _, ok := SummaryCallbackReturnOf(context, head); ok {
		t.Errorf("a returned .map lowered — the result slot cannot hold an array")
	}
	// and the receiver itself was never the problem
	if _, _, ok := arraySlotsOf(context, "children"); !ok {
		t.Errorf("arraySlotsOf(children) declined — the receiver resolves; the method is the gate")
	}
}

func TestCallbackReturn_TextTsxCalculateDeclinesAtTheDestructuredSecondParameterNotTheArrayAccumulator(t *testing.T) {
	// tmp/recharts-src/src/component/Text.tsx:197, `calculate`'s own
	// reduce:
	//
	//   words.reduce((result: Array<WordsWithWidth>, { word, width }) => {
	//     const currentLine = result[result.length - 1];
	//     if (currentLine && width != null && (...)) {
	//       currentLine.words.push(word);
	//       currentLine.width += width + spaceWidth;
	//     } else {
	//       const newLine: WordsWithWidth = { words: [word], width };
	//       result.push(newLine);
	//     }
	//     return result;
	//   }, []);
	//
	// The accumulator (`result: Array<WordsWithWidth>`) is array-typed and
	// the seed is the empty literal `[]`, exactly the shape this agent's
	// pair route was built for — but the SECOND declared parameter is a
	// destructured `{ word, width }`, not a plain identifier, and
	// arrowParameterNames (ir_callback_recognition.go) declines on ANY
	// non-identifier parameter unconditionally: `!ast.IsIdentifier(pd.Name())`
	// has no destructuring arm at all. That gate sits in
	// convertArrowWithEntryLayout, upstream of every array/pair reading
	// this agent added — reduceArrayAccumulatorSlotStatements's own gates
	// (array-typed accumulator, ret pair, empty-literal seed) never run,
	// because convertReduceArrowArray's own convertArrowWithEntryLayout
	// call declines before compiling anything.
	//
	// Record elements ("Array<WordsWithWidth>"'s own member layout) and a
	// destructured callback parameter are BOTH outside this agent's
	// exclusive territory (ir_summary_body.go / ir_callback_convert.go /
	// ir_callback_return.go / ir_summary_body_lowering_parameters.go) —
	// they are the record-parameter family's own siblings
	// (ir_summary_record_parameter_uses.go and the record-destructuring
	// machinery). Even fixing them would not reach the outer shape here:
	// the callback body itself never returns anything array-shaped at
	// all — it returns the bare identifier `result` after two `.push`
	// mutations — so returnedLiteralShape's own reading of the CALLBACK's
	// body (needed for arrowCallStatementArrayResult to have anything to
	// map back) would still find no array-producing return to allocate a
	// pair for, independent of the destructuring gate. Three separate
	// gaps, all outside this agent's files.
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackReturnContext([]string{"words"}, nil, nil, BindingKindUnknown)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	context.SummaryTable = &SummaryTableBuilder{}
	context.Bindings = append(context.Bindings, retLenSlotName(), retElemSlotName())
	context.Sorts = append(context.Sorts, BindingKindNumber, BindingKindUnknown)
	context.Typeofs = append(context.Typeofs, TypeofTagNumber, TypeofTagNone)
	statements := loweringParse(t, `return words.reduce((result: Array<WordsWithWidth>, { word, width }) => {
		const currentLine = result[result.length - 1];
		if (currentLine) {
			currentLine.words.push(word);
		} else {
			result.push({ words: [word], width });
		}
		return result;
	}, []);`)
	head := Unwrapped(statements[0].AsReturnStatement().Expression)
	reduceSource, seed, readOk := reduceCallOf(head)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	// the array-typed accumulator and the empty-literal seed are BOTH
	// read correctly, in isolation — confirming the decline is the
	// destructured parameter, not a misreading of the accumulator or seed
	arrow := callbackFunctionOf(context, reduceSource.Callback)
	if arrow == nil || len(arrow.Parameters()) == 0 {
		t.Fatalf("the callback did not resolve to an arrow with a first parameter")
	}
	accumulatorParam := arrow.Parameters()[0].AsParameterDeclaration()
	if accumulatorParam.Type == nil {
		t.Fatalf("the accumulator's own type annotation did not resolve")
	}
	if _, isArray := arrayTypeNodeElementSort(accumulatorParam.Type); !isArray {
		t.Errorf("arrayTypeNodeElementSort declined `Array<WordsWithWidth>` — want it recognized as an array type")
	}
	if _, _, seedOk := arrayLiteralPairSeedOf(seed); !seedOk {
		t.Errorf("arrayLiteralPairSeedOf declined the seed `[]` — want the empty-literal pair read")
	}
	// the whole route still declines: the destructured second parameter
	// closes convertArrowWithEntryLayout's own gate before any of the
	// above ever gets used to compile anything
	if _, ok := SummaryCallbackReturnOf(context, head); ok {
		t.Errorf("Text.tsx's calculate reduce lowered — want a decline: the destructured second parameter `{ word, width }` closes the arrow-conversion gate")
	}
}
