// split from ir_callback_summary_test.go — the ARRAY statement shapes:
// map, filter, forEach, the Promise.all(map) spelling, and the index
// entry an array callback's second parameter takes
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
