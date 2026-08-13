// The widened summary route (KernelSummaryDirect — this port's S5
// completion): a body whose only writes hit locals summarizes
// kernel-side without the effect-free pre-scan, and the declines that
// keep the route honest. Skipped (never a faked pass) when the native
// kernel dylib is absent, the same gate kernel_delegation_test uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// summaryDeclarationOf parses a throwaway source whose FIRST statement
// is a function declaration and answers that declaration node.
func summaryDeclarationOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/s.ts", Path: "/s.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 || !ast.IsFunctionDeclaration(file.Statements.Nodes[0]) {
		t.Fatalf("no function declaration parsed from %q", source)
	}
	return file.Statements.Nodes[0]
}

func exactNumber(t *testing.T, x float64) abstractdomain.AbstractValue {
	t.Helper()
	return abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

func TestKernelSummaryDirect_ALocalWritingLoopBodySummarizesWithoutTheEffectFreeGate(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// scanBody calls this body impure (it assigns s and i), so the
	// effect-free route never reaches it — the direct route must.
	// The while head compares against a LITERAL, so the head's truth
	// and falsity sets narrow the loop (LoopHeadOf); a head comparing
	// two slots lowers too, but claims no narrowing on either side.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = 0; let i = 0; while (i < 3) { s = s + n; i = i + 1; } return s; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 2)}, contract)
	if !ok {
		t.Fatalf("KernelSummaryDirect ok = false, want a summarized answer")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("summarized answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) = 2+2+2 = 6; the kernel's loop answer must ADMIT 6 (a
	// widened invariant is sound; an answer excluding 6 is not)
	if !kernel.Member(state.Set, []float64{6}) {
		t.Errorf("summary of f(2) excludes the true value 6: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_APropertyWritingBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `o: { k: number }` EXPANDS to the entry slot "o.k", so a slot for
	// the write's target now exists — and the write must still decline:
	// the caller's own object would move, and a summary carries no effect
	// back out through its entries
	declaration := summaryDeclarationOf(t,
		"function f(o: { k: number }, n: number) { o.k = n; return n; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 2)}, contract); ok {
		t.Errorf("a property-writing body summarized — its effect on the caller's object would be dropped")
	}
}

/* ── record parameters ───────────────────────────────────────────── */

// recordObject builds the object argument an expanded parameter reads:
// one key per name, each holding the exact number given.
func recordObject(t *testing.T, fields map[string]float64, order []string) abstractdomain.AbstractValue {
	t.Helper()
	keys := make([]abstractdomain.ObjectKey, 0, len(order))
	for _, name := range order {
		keys = append(keys, abstractdomain.ObjectKey{Name: name, Value: exactNumber(t, fields[name])})
	}
	return abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false)
}

func TestSummaryParameterEntries_ATypeLiteralParameterExpandsOneEntryPerMember(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: string, on: boolean }) { return p.lo; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok {
		t.Fatalf("a scalar-membered type literal declined the expansion")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 — one per member", len(entries))
	}
	wantName := []string{"p.lo", "p.hi", "p.on"}
	wantSort := []BindingKind{BindingKindNumber, BindingKindString, BindingKindNumber}
	wantTypeof := []TypeofTag{TypeofTagNumber, TypeofTagString, TypeofTagBoolean}
	for i, entry := range entries {
		if entry.Name != wantName[i] {
			t.Errorf("entry %d name = %q, want %q", i, entry.Name, wantName[i])
		}
		if entry.Sort != wantSort[i] {
			t.Errorf("entry %d sort = %q, want %q", i, entry.Sort, wantSort[i])
		}
		if entry.TypeofTag != wantTypeof[i] {
			t.Errorf("entry %d typeof = %q, want %q", i, entry.TypeofTag, wantTypeof[i])
		}
	}
}

func TestSummaryParameterEntries_AScalarParameterKeepsItsSingleWholeNameEntry(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v (ok %v), want exactly one", entries, ok)
	}
	if entries[0].Name != "n" || entries[0].Sort != BindingKindNumber {
		t.Errorf("scalar entry = %+v, want {n number number}", entries[0])
	}
}

func TestSummaryParameterEntries_TheShapesThatStayWholeNameDeclines(t *testing.T) {
	// each of these keeps the parameter a single unknown-sorted entry —
	// the expansion answers no members for any of them
	sources := map[string]string{
		"a class name":       "function f(p: Point) { return 1; }",
		"an interface name":  "function f(p: Shape) { return 1; }",
		"a union":            "function f(p: { lo: number } | { hi: number }) { return 1; }",
		"an optional member": "function f(p: { lo?: number }) { return 1; }",
		"a method":           "function f(p: { lo(): number }) { return 1; }",
		"an index signature": "function f(p: { [k: string]: number }) { return 1; }",
		"a nested literal":   "function f(p: { lo: { deep: number } }) { return 1; }",
		"a richer member":    "function f(p: { lo: number[] }) { return 1; }",
		"an empty literal":   "function f(p: {}) { return 1; }",
	}
	for name, source := range sources {
		declaration := summaryDeclarationOf(t, source)
		if _, expanded := recordParamMembersOf(declaration.Parameters()[0]); expanded {
			t.Errorf("%s expanded — only a type literal of scalar members may", name)
		}
		entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
		if !ok || len(entries) != 1 || entries[0].Name != "p" {
			t.Errorf("%s: entries = %v (ok %v), want the single whole-name entry", name, entries, ok)
		}
	}
}

func TestLowerSummaryBody_ABodyReadingARecordParameterMemberLowersOverTheLeafSlots(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		t.Fatalf("a body reading p.lo / p.hi declined — the leaf slots should carry the reads")
	}
	// ParamCount counts the EXPANDED entries, not the one declared
	// parameter
	if summary.ParamCount != 2 {
		t.Errorf("ParamCount = %d, want 2 — one entry per member", summary.ParamCount)
	}
	// the done flag and the result slot are still the last two of the base
	// vector, so the expansion shifted them without disturbing the
	// convention
	if summary.DoneIndex != summary.RetIndex-1 {
		t.Errorf("DoneIndex %d / RetIndex %d — the flag must sit immediately before the result", summary.DoneIndex, summary.RetIndex)
	}
	if summary.RetIndex >= summary.SlotCount {
		t.Errorf("RetIndex %d outside SlotCount %d", summary.RetIndex, summary.SlotCount)
	}
}

func TestKernelSummaryDirect_ARecordArgumentsFieldsBuildTheLeafEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2, "hi": 5}, []string{"lo", "hi"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a record argument declined — its fields spell as scalar entry states")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f({lo:2, hi:5}) = 7; the answer must ADMIT it
	if !kernel.Member(state.Set, []float64{7}) {
		t.Errorf("the summary of f({lo:2,hi:5}) excludes the true value 7: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_ANonObjectArgumentForAnExpandedParameterDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	// the entries were laid out expecting the members; a scalar spells
	// none of them
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 3)}, contract); ok {
		t.Errorf("a scalar argument filled an expanded parameter's leaf entries")
	}
}

func TestKernelSummaryDirect_AnObjectArgumentMissingADeclaredMemberDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("an object without hi filled hi's entry — that leaf has no state to send")
	}
}

func TestKernelSummaryDirect_AWholeRecordParameterUseDeclinesTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `return p` names the WHOLE record, which the expansion does not
	// build — there is no one value for it to denote
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { const q = p; return q.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a whole-p use summarized — the expansion holds no such value")
	}
}

func TestKernelSummaryDirect_AnUndeclaredMemberReadDeclinesTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { return p.mid; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a read of an undeclared member summarized — no slot holds it")
	}
}

func TestKernelSummaryDirect_AClassTypedParameterIsUnchanged(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// a class name is not a type literal: the parameter keeps its single
	// unknown-sorted slot, and the `p.lo` read finds no slot — exactly
	// today's decline
	declaration := summaryDeclarationOf(t,
		"function f(p: Point) { return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a class-typed parameter expanded — its instance has methods and aliases the flattening cannot hold")
	}
}

func TestKernelSummaryDirect_AnAsyncBodySummarizesAndAnswersAPromiseOfItsRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the ret-as-inner convention: the lowered body's #ret holds the
	// SETTLED value (n + 1), and the boundary wraps it — so the body
	// summarizes and the caller's view is a Promise of the return set
	declaration := summaryDeclarationOf(t,
		"async function f(n: number) { return n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract)
	if !ok {
		t.Fatalf("an async body declined — the ret-as-inner convention makes it lowerable")
	}
	if answer.Kind != abstractdomain.KindPromise {
		t.Fatalf("async summary answered kind %v, want a Promise wrapper", answer.Kind)
	}
	if answer.Inner == nil {
		t.Fatalf("the Promise carries no inner value")
	}
	state, stateOk := StateOfKnown(*answer.Inner)
	if !stateOk || state.Top {
		t.Fatalf("the promise's inner did not spell as a scalar state: %+v", *answer.Inner)
	}
	// f(1) settles at 2; the inner set must ADMIT it
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the promise's inner excludes the true settled value 2: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_AGeneratorBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the call's value is an ITERATOR, and no slot spells one — there is
	// no inner value for a boundary wrapper to adopt
	declaration := summaryDeclarationOf(t,
		"function* f(n: number) { yield n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract); ok {
		t.Errorf("a generator body summarized — the call's value is an iterator, not the yield set")
	}
}
