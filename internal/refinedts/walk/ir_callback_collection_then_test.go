// split from ir_callback_summary_test.go — the NON-ARRAY receivers:
// forEach over a flattened Map or Set, and `p.then(cb)` over a
// promise-held local
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
