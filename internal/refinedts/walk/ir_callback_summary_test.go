// Arrow callbacks, closure-converted: the free-name scan and its
// capture layout, the decline rules, and the collection statements a
// converted callback lowers to.
//
// The scan and the capture resolution read syntax and the caller's slot
// vector alone, so those probe without a kernel. The statement shapes
// compile the arrow's blob, which the kernel writes — those gate on the
// dylib the same way every other walk test here does, and are skipped
// rather than faked when it is absent.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// callbackArrowOf parses `xs.m(cb)` as an expression statement and
// answers the callback argument — the arrow the scan reads.
func callbackArrowOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := loweringParse(t, source)
	if len(statements) != 1 {
		t.Fatalf("len(statements) = %d, want 1", len(statements))
	}
	call, ok := collectionCallOf(Unwrapped(statements[0].AsExpressionStatement().Expression))
	if !ok {
		t.Fatalf("collectionCallOf(%q) declined", source)
	}
	arrow := arrowFunctionOf(call.Callback)
	if arrow == nil {
		t.Fatalf("the callback of %q is not an arrow", source)
	}
	return arrow
}

// callbackContext is the caller layout the conversion tests share: a
// flattened source array, a flattened target array, and whatever
// scalars the case captures.
func callbackContext(bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings:     bindings,
		Sorts:        sorts,
		Typeofs:      make([]TypeofTag, len(bindings)),
		SummaryTable: &SummaryTableBuilder{},
	}
}

func TestCallbackScan_AnArrowReadingOnlyItsParameterIsFreeOfCaptures(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + 1);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames(x => x + 1).Ok = false, want true")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none — x is the arrow's own parameter", scan.Reads)
	}
}

func TestCallbackScan_AReadCaptureBecomesOneEntryAfterTheParameters(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + factor);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames(x => x + factor).Ok = false, want true")
	}
	if len(scan.Reads) != 1 || scan.Reads[0] != "factor" {
		t.Fatalf("free reads = %v, want [factor]", scan.Reads)
	}
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "factor"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	captures, slots, ok := capturesOf(context, scan)
	if !ok {
		t.Fatalf("capturesOf(factor) ok = false, want true — factor has a slot")
	}
	if len(captures) != 1 || captures[0].Name != "factor" {
		t.Fatalf("captures = %v, want one named factor", captures)
	}
	if captures[0].Sort != BindingKindNumber {
		t.Errorf("capture sort = %q, want %q — a capture wears its caller slot's sort", captures[0].Sort, BindingKindNumber)
	}
	if len(slots) != 1 || slots[0] != 2 {
		t.Errorf("capture slots = %v, want [2] — the caller slot factor resolves to", slots)
	}
}

func TestCallbackScan_TheCaptureOrderIsSourceOrderOfFirstRead(t *testing.T) {
	// the layout the call site fills depends on this order being the
	// scan's own report, not a map's iteration
	arrow := callbackArrowOf(t, `xs.map(x => x * scale + offset - scale);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames ok = false, want true")
	}
	if len(scan.Reads) != 2 || scan.Reads[0] != "scale" || scan.Reads[1] != "offset" {
		t.Errorf("free reads = %v, want [scale offset] — first-read order, each name once", scan.Reads)
	}
}

func TestCallbackScan_AWrittenCaptureDeclinesTheArrow(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.forEach(x => { total += x; });`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow writing the captured `total` converted — a capture is READ-ONLY")
	}
}

func TestCallbackScan_AStepOnACapturedNameDeclinesTheArrow(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.forEach(x => { count++; });`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow stepping the captured `count` converted — a capture is READ-ONLY")
	}
}

func TestCallbackScan_AWriteToTheArrowsOwnLocalIsNotACapturedWrite(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => { let y = x; y = y + 1; return y; });`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("an arrow writing its OWN local declined — y is bound inside")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none", scan.Reads)
	}
}

func TestCallbackScan_ANestedFunctionInsideTheArrowDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => (y => y + x)(x));`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow holding a nested function converted — its own captures are unconverted")
	}
}

func TestCallbackScan_AThisRootedCallContributesNoFreeName(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => this.scale(x));`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("a `this`-rooted method call declined the arrow")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none — the receiver is consumed by the resolution", scan.Reads)
	}
}

func TestCallbackScan_AThisReadAsAValueDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + this.factor);`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("`this` read as a value converted — no slot holds a whole object")
	}
}

func TestCallbackCaptures_AFreeNameWithNoSlotDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + imported);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames ok = false, want true — the scan itself admits the read")
	}
	context := callbackContext(
		[]string{"xs.len", "xs.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, _, ok := capturesOf(context, scan); ok {
		t.Errorf("a capture with no caller slot converted — there is no var to bind its entry to")
	}
}

func TestCallbackStatement_AnOrdinaryAssignmentIsNotACollectionCall(t *testing.T) {
	context := callbackContext([]string{"x", "y"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `x = y + 1;`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("an ordinary assignment took the callback route")
	}
}

func TestCallbackStatement_AMapOverAnUnflattenedReceiverDeclines(t *testing.T) {
	// no "xs.len"/"xs.elem" pair, so the receiver is not an array here
	context := callbackContext(
		[]string{"ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindUnknown})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.map(x => x + 1);`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a map over an unflattened receiver lowered — its elem slot resolves to nothing")
	}
}

func TestCallbackStatement_AMapWhoseCallbackWritesACaptureDeclines(t *testing.T) {
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown, BindingKindNumber})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.map(x => { total += x; return x; });`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a map whose callback writes a capture lowered — the write would move the caller's state")
	}
}

func TestCallbackStatement_AMethodThatIsNotRecognizedDeclines(t *testing.T) {
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.reduce(x => x + 1);`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("`reduce` lowered — only map, filter and forEach are recognized")
	}
}

func TestCallbackStatement_MapEmitsTheLengthCopyThenTheCall(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.map(x => x + 1);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(map) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (the length copy, then the call)", len(stmts))
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign {
		t.Fatalf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementAssign)
	}
	// map preserves length: ys.len := var xs.len
	if stmts[0].Target != 2 {
		t.Errorf("the length write's target = %d, want 2 (ys.len)", stmts[0].Target)
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.Index != 0 {
		t.Errorf("the length write's effect = %+v, want var of slot 0 (xs.len)", stmts[0].Effect)
	}
	if stmts[1].Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[1].Kind = %q, want %q", stmts[1].Kind, kernelbridge.IrStatementCall)
	}
	// the callback's first entry is the source's element slot — the join
	// of every element, which is what makes one application cover them all
	if len(stmts[1].Args) == 0 {
		t.Fatalf("the call carries no entries")
	}
	if stmts[1].Args[0].Kind != kernelbridge.LoopEffectVar || stmts[1].Args[0].Index != 1 {
		t.Errorf("the call's first entry = %+v, want var of slot 1 (xs.elem)", stmts[1].Args[0])
	}
	// the callback's ret lands in the result's element slot
	if stmts[1].Rets[len(stmts[1].Rets)-1] != 3 {
		t.Errorf("the call's ret = %d, want 3 (ys.elem)", stmts[1].Rets[len(stmts[1].Rets)-1])
	}
	if len(context.SummaryTable.Blobs) != 1 {
		t.Errorf("len(table) = %d, want 1 — the arrow's blob is tabled once", len(context.SummaryTable.Blobs))
	}
}

func TestCallbackStatement_AMapWithACaptureBindsTheExtraEntryAfterTheParameter(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem", "factor"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown, BindingKindNumber})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.map(x => x * factor);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(map with a capture) ok = false, want true")
	}
	call := stmts[1]
	if call.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[1].Kind = %q, want %q", call.Kind, kernelbridge.IrStatementCall)
	}
	if len(call.Args) < 2 {
		t.Fatalf("the call carries %d entries, want at least 2 (the parameter, then the capture)", len(call.Args))
	}
	// entry 0 is the declared parameter, entry 1 the one capture — the
	// layout the summary was compiled under
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (xs.elem)", call.Args[0])
	}
	if call.Args[1].Kind != kernelbridge.LoopEffectVar || call.Args[1].Index != 4 {
		t.Errorf("entry 1 = %+v, want var of slot 4 (factor) — the capture rides after the parameter", call.Args[1])
	}
}

func TestCallbackStatement_FilterCopiesTheElementAndLosesTheLength(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.filter(x => x > 0);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(filter) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (the element copy, then the length)", len(stmts))
	}
	// every surviving element is one the source held
	if stmts[0].Target != 3 {
		t.Errorf("the element write's target = %d, want 3 (ys.elem)", stmts[0].Target)
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.Index != 1 {
		t.Errorf("the element write's effect = %+v, want var of slot 1 (xs.elem)", stmts[0].Effect)
	}
	// the length is an integer at least 0 and no more — the honest loss
	if stmts[1].Target != 2 {
		t.Errorf("the length write's target = %d, want 2 (ys.len)", stmts[1].Target)
	}
	if stmts[1].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("the length write's effect kind = %q, want %q", stmts[1].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	if kernel.Member(stmts[1].Effect.Set, []float64{-1}) {
		t.Errorf("member(ys.len, [-1]) = true, want false — a length is at least 0")
	}
	for _, value := range []float64{0, 5} {
		if !kernel.Member(stmts[1].Effect.Set, []float64{value}) {
			t.Errorf("member(ys.len, [%v]) = false, want true — nothing bounds a filter's length above", value)
		}
	}
}

func TestCallbackStatement_AFilterWhoseCallbackDoesNotConvertDeclines(t *testing.T) {
	// the predicate's truthiness is not modeled, but its LOWERABILITY
	// still gates — an effectful predicate may not pass as a pure one
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem", "seen"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown, BindingKindNumber})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.filter(x => { seen += 1; return x > 0; });`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a filter with an effectful predicate lowered — the write would move the caller's state")
	}
}

func TestCallbackStatement_ForEachIsTheSingleCallWithNoRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `xs.forEach(x => { const y = x + 1; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(forEach) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 (the call alone)", len(stmts))
	}
	if stmts[0].Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementCall)
	}
	// forEach's own value is undefined; nothing rides back
	for index, ret := range stmts[0].Rets {
		if ret != -1 {
			t.Errorf("rets[%d] = %d, want -1 — a forEach reads no out-state", index, ret)
		}
	}
}

func TestCallbackStatement_PromiseAllOfAMapOfAnAsyncArrowLowersAsTheMap(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = await Promise.all(xs.map(async x => x + 1));`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(await Promise.all(map)) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the same two the plain map emits", len(stmts))
	}
	// the await is the identity: #ret already holds the settled inner
	if stmts[0].Target != 2 || stmts[0].Effect.Index != 0 {
		t.Errorf("the length copy = %+v, want ys.len := var xs.len", stmts[0])
	}
	if stmts[1].Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[1].Kind = %q, want %q", stmts[1].Kind, kernelbridge.IrStatementCall)
	}
	if stmts[1].Rets[len(stmts[1].Rets)-1] != 3 {
		t.Errorf("the call's ret = %d, want 3 (ys.elem) — the settled inner rides there", stmts[1].Rets[len(stmts[1].Rets)-1])
	}
}

func TestCallbackStatement_TheUnawaitedPromiseAllSpellingLowersTheSameWay(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = Promise.all(xs.map(x => x + 1));`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(Promise.all(map)) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Errorf("len(stmts) = %d, want 2", len(stmts))
	}
}

// ── the index parameter ─────────────────────────────────────────────

func TestCallbackEntries_AnArrayCallbacksSecondParameterIsTheIndex(t *testing.T) {
	// no kernel needed: the entry layout is read off the caller's slot
	// vector and the constant set alone
	context := callbackContext(
		[]string{"xs.len", "xs.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	entries := arrayCallbackEntries(context, 3, 1)
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Effect.Kind != kernelbridge.LoopEffectVar || entries[0].Effect.Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (xs.elem)", entries[0].Effect)
	}
	// the index enters a CONSTANT SET, not absent — every concrete index
	// is a non-negative integer
	if entries[1].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("entry 1 kind = %q, want %q — the index is the integer ray, not absent",
			entries[1].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	if entries[1].Sort != BindingKindNumber {
		t.Errorf("entry 1 sort = %q, want %q — the index is number-sorted so arithmetic admits it",
			entries[1].Sort, BindingKindNumber)
	}
	// the third parameter is the array itself, which no slot holds
	if entries[2].Effect.Kind != kernelbridge.AbsentConst().Kind || entries[2].Sort != BindingKindUnknown {
		t.Errorf("entry 2 = %+v (sort %q), want the absent entry", entries[2].Effect, entries[2].Sort)
	}
}

func TestCallbackEntries_TheIndexSetIsTheNonNegativeIntegers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	set := indexCallbackEntry().Effect.Set
	for _, value := range []float64{0, 1, 7} {
		if !kernel.Member(set, []float64{value}) {
			t.Errorf("member(index, [%v]) = false, want true — a concrete index reaches every position", value)
		}
	}
	if kernel.Member(set, []float64{-1}) {
		t.Errorf("member(index, [-1]) = true, want false — an index is at least 0")
	}
	if kernel.Member(set, []float64{1.5}) {
		t.Errorf("member(index, [1.5]) = true, want false — an index is an integer")
	}
}

func TestCallbackStatement_AMapWhoseCallbackReadsTheIndexLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = xs.map((x, i) => x + i);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf((x, i) => x + i) ok = false, want true — " +
			"the index entry is number-sorted, so the arithmetic admits it")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (the length copy, then the call)", len(stmts))
	}
	call := stmts[1]
	if call.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[1].Kind = %q, want %q", call.Kind, kernelbridge.IrStatementCall)
	}
	if len(call.Args) < 2 {
		t.Fatalf("the call carries %d entries, want at least 2", len(call.Args))
	}
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (xs.elem)", call.Args[0])
	}
	if call.Args[1].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("entry 1 kind = %q, want %q — the index rides as the integer ray",
			call.Args[1].Kind, kernelbridge.LoopEffectConst)
	}
}

// ── Map and Set forEach ─────────────────────────────────────────────

// collectionContext is the caller layout a flattened Map or Set wears:
// the size, the values, and — for a Map — the keys.
func collectionContext(name string, isMap bool, valueSort BindingKind) *LoweringContext {
	bindings := []string{name + mapSizeSuffix, name + mapValsSuffix}
	sorts := []BindingKind{BindingKindNumber, valueSort}
	if isMap {
		bindings = append(bindings, name+mapKeysSuffix)
		sorts = append(sorts, BindingKindNumber)
	}
	return callbackContext(bindings, sorts)
}

func TestCallbackStatement_MapForEachCallsAtTheValuesSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := collectionContext("m", true, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `m.forEach(v => { const y = v + 1; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(m.forEach) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 (the call alone)", len(stmts))
	}
	call := stmts[0]
	if call.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[0].Kind = %q, want %q", call.Kind, kernelbridge.IrStatementCall)
	}
	if len(call.Args) == 0 {
		t.Fatalf("the call carries no entries")
	}
	// slot 1 is "m.vals" — the join of every value the collection holds
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (m.vals)", call.Args[0])
	}
	// forEach's own value is undefined; nothing rides back
	for index, ret := range call.Rets {
		if ret != -1 {
			t.Errorf("rets[%d] = %d, want -1 — a forEach reads no out-state", index, ret)
		}
	}
}

func TestCallbackStatement_AMapForEachsSecondParameterIsTheKey(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := collectionContext("m", true, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `m.forEach((v, k) => { const y = v + k; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(m.forEach((v, k) => …)) ok = false, want true")
	}
	call := stmts[0]
	if len(call.Args) < 2 {
		t.Fatalf("the call carries %d entries, want at least 2", len(call.Args))
	}
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (m.vals)", call.Args[0])
	}
	// slot 2 is "m.keys" — a Map calls back with (value, key, map)
	if call.Args[1].Kind != kernelbridge.LoopEffectVar || call.Args[1].Index != 2 {
		t.Errorf("entry 1 = %+v, want var of slot 2 (m.keys)", call.Args[1])
	}
}

func TestCallbackStatement_ASetForEachsSecondParameterIsTheValueAgain(t *testing.T) {
	// Set.forEach calls back with (value, value, set): the second
	// argument stands in for the key a Set does not have
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := collectionContext("s", false, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `s.forEach((v, w) => { const y = v + w; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(s.forEach((v, w) => …)) ok = false, want true")
	}
	call := stmts[0]
	if len(call.Args) < 2 {
		t.Fatalf("the call carries %d entries, want at least 2", len(call.Args))
	}
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want var of slot 1 (s.vals)", call.Args[0])
	}
	if call.Args[1].Kind != kernelbridge.LoopEffectVar || call.Args[1].Index != 1 {
		t.Errorf("entry 1 = %+v, want var of slot 1 (s.vals) again — a Set has no key",
			call.Args[1])
	}
}

func TestCallbackStatement_AForEachsThirdParameterIsAbsent(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := collectionContext("m", true, BindingKindNumber)
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `m.forEach((v, k, c) => { const y = v + k; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(three-parameter forEach) ok = false, want true")
	}
	call := stmts[0]
	if len(call.Args) < 3 {
		t.Fatalf("the call carries %d entries, want at least 3", len(call.Args))
	}
	// the collection itself is a whole object no slot holds
	if call.Args[2].Kind != kernelbridge.AbsentConst().Kind {
		t.Errorf("entry 2 kind = %q, want the absent entry's %q — no slot holds a whole Map",
			call.Args[2].Kind, kernelbridge.AbsentConst().Kind)
	}
}

func TestCallbackStatement_MapOnAFlattenedCollectionDeclines(t *testing.T) {
	// a Map has no `map` method; the receiver resolves to no array slots
	// either, so the statement declines whole
	context := collectionContext("m", true, BindingKindNumber)
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `ys = m.map(v => v + 1);`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("`m.map` lowered — a Map has no such method")
	}
}

func TestCallbackStatement_AForEachOnAnUnflattenedReceiverDeclines(t *testing.T) {
	context := callbackContext([]string{"n"}, []BindingKind{BindingKindNumber})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `m.forEach(v => { const y = v + 1; });`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a forEach over a receiver that is neither a flattened array nor a flattened collection lowered")
	}
}

// ── p.then(cb) ──────────────────────────────────────────────────────

// promiseHeldContext is a caller layout holding one promise-held local:
// its "p.inner" slot, registered the way the await lowering registers
// one, plus an Allocate that grows the vector for a `then` result.
func promiseHeldContext(t *testing.T, name string) *LoweringContext {
	t.Helper()
	context := callbackContext([]string{name + promiseInnerSuffix}, []BindingKind{BindingKindNumber})
	context.Allocate = func(spelled string, sort BindingKind, tag TypeofTag) (int, bool) {
		context.Bindings = append(context.Bindings, spelled)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, tag)
		return len(context.Bindings) - 1, true
	}
	holdPromiseInnerSlot(context, name, 0)
	return context
}

func TestCallbackStatement_ABareThenCallsAtTheInnerSlotWithNoRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := promiseHeldContext(t, "p")
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `p.then(v => { const y = v + 1; });`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(p.then(cb)) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 (the call alone)", len(stmts))
	}
	call := stmts[0]
	if call.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts[0].Kind = %q, want %q", call.Kind, kernelbridge.IrStatementCall)
	}
	if len(call.Args) == 0 {
		t.Fatalf("the call carries no entries")
	}
	// slot 0 is "p.inner" — what p settles to
	if call.Args[0].Kind != kernelbridge.LoopEffectVar || call.Args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want var of slot 0 (p.inner)", call.Args[0])
	}
	for index, ret := range call.Rets {
		if ret != -1 {
			t.Errorf("rets[%d] = %d, want -1 — a bare then reads no out-state", index, ret)
		}
	}
}

func TestCallbackStatement_AnAssignedThenMakesTheTargetAPromiseHeldLocal(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := promiseHeldContext(t, "p")
	context.Narrow = kernel.Narrow
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `const q = p.then(v => v + 1);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("SummaryCallbackStatementOf(q = p.then(cb)) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}
	// a fresh "q.inner" slot was allocated and registered
	targetSlot, held := promiseInnerSlotOf(context, "q")
	if !held {
		t.Fatalf("q was not registered as a promise-held local — a later `await q` would not read it back")
	}
	if context.Bindings[targetSlot] != "q"+promiseInnerSuffix {
		t.Errorf("slot %d is spelled %q, want %q", targetSlot, context.Bindings[targetSlot], "q"+promiseInnerSuffix)
	}
	// cb's ret rides there: then answers a promise, and by the
	// ret-as-inner convention cb's #ret is what it settles to
	call := stmts[0]
	if call.Rets[len(call.Rets)-1] != targetSlot {
		t.Errorf("the call's ret = %d, want %d (q.inner)", call.Rets[len(call.Rets)-1], targetSlot)
	}
}

func TestCallbackStatement_ThenOnAReceiverThatIsNotPromiseHeldDeclines(t *testing.T) {
	context := callbackContext([]string{"p"}, []BindingKind{BindingKindNumber})
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `p.then(v => { const y = v + 1; });`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a then over a receiver with no \"p.inner\" slot lowered — there is no settled value to call at")
	}
}

func TestCallbackStatement_CatchAndFinallyDecline(t *testing.T) {
	for _, method := range []string{"catch", "finally"} {
		context := promiseHeldContext(t, "p")
		context.Flow = &FlowContext{}
		statements := loweringParse(t, `p.`+method+`(v => { const y = v + 1; });`)
		if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
			t.Errorf("`p.%s(cb)` lowered — its callback runs on the rejection, which no slot holds", method)
		}
	}
}

func TestCallbackStatement_AChainedThenDeclines(t *testing.T) {
	// the second `then`'s receiver is a CALL, not a name, so no
	// promise-held slot resolves for it
	context := promiseHeldContext(t, "p")
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `const q = p.then(v => v + 1).then(w => w + 2);`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a chained `p.then(a).then(b)` lowered — the inner call is not a promise-held local")
	}
}

func TestCallbackStatement_AThenCallbackWithTwoParametersDeclines(t *testing.T) {
	// `then`'s callback takes only the settled value; a second declared
	// parameter is a shape this does not model
	context := promiseHeldContext(t, "p")
	context.Flow = &FlowContext{}
	statements := loweringParse(t, `p.then((v, extra) => { const y = v + 1; });`)
	if _, ok := SummaryCallbackStatementOf(context, statements[0]); ok {
		t.Errorf("a two-parameter then callback lowered")
	}
}
