// Pins for two whole-record-parameter positions the use scan refused:
//
//  1. a ROOT-ADJACENT OPTIONAL STEP on a declared leaf (`p?.lo` —
//     axisSelectors.ts's `axisSettings?.dataKey`, combineActiveTooltipIndex's
//     `tooltipInteraction?.index`). The read resolves the same leaf slot
//     `p.lo` does (PathSlotIndexOf already admits the root-adjacent
//     `?.`), and the value is covered by the slot's own entry state: a
//     holder whose annotation carries an absent arm marks every leaf
//     MayBeAbsent (unionMembersOf's absent-arm rule), so the absent
//     admission already rides the entry state the read answers with.
//
//  2. an UNDECLARED-PATH READ in a position that hands out no silent
//     in-body write channel — `return state.layout.inner` (chartLayout's
//     selectChartLayout), `return state.axes.byId[id]`
//     (selectXAxisSettingsNoDefaults), `p.ticks.map(String)`
//     (getDomainDefinition). A test/return position reads the interior
//     and grants the caller nothing it could not already reach through
//     the record it passed; a call-argument or callee position hands the
//     interior to running code and takes the hand-over havoc
//     (recordParameterHandedOver). A STORE (`const q = p.inner`) reads
//     whole too where interiorStoreAliasAdmissible
//     (ir_summary_record_parameter_interior_reads.go) proves every use
//     of `q` afterward is itself a read-only consumer — axisSelectors.ts's
//     `const axis = state.cartesianAxis.zAxis[axisId]` is this shape. A
//     store whose alias IS written through anywhere (`q.deep = 5`) still
//     refuses: the aliased value resolves to no slot for that write to
//     land in.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestKernelSummaryDirect_ARootOptionalStepOnAnAbsentArmedRecordReads
// pins `p?.index` on a `Record | undefined` parameter — the
// combineActiveTooltipIndex shape. Before the fix the optional step
// matched no path arm (propertyPathOf refuses `?.`) and the identifier
// fell to the whole-name refusal.
func TestKernelSummaryDirect_ARootOptionalStepOnAnAbsentArmedRecordReads(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface TooltipInteractionState { active: boolean; index: string; }
function combine(tooltipInteraction: TooltipInteractionState | undefined): string | null {
  const desiredIndex = tooltipInteraction?.index;
  if (desiredIndex == null) {
    return null;
  }
  return desiredIndex;
}
`)
	declaration := entryEnvFunctionNamed(t, p, "combine")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("combine's body declined: outcome=%q construct=%q — the root-optional step on a declared leaf must read", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the optional-step read still falls to the whole-name refusal", construct)
	}
}

// TestKernelSummaryDirect_ARootOptionalStepOnANonAbsentRecordReads pins
// `a?.dataKey != null` on a plainly-typed record parameter — the
// combineAppliedValues shape (`axisSettings?.dataKey`): the holder's
// annotation has no absent arm, so the optional step reads exactly what
// the plain step reads.
func TestKernelSummaryDirect_ARootOptionalStepOnANonAbsentRecordReads(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(a: { dataKey: string }) { if (a?.dataKey != null) { return a.dataKey; } return 'x'; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the optional-step read still falls to the whole-name refusal", construct)
	}
}

// TestKernelSummaryDirect_AnInteriorMemberReturnLowers pins
// `return state.layout.inner` where `layout`'s own joined path names no
// declared leaf (its type carries type arguments, the RechartsRootState
// cause) — selectChartLayout's shape. The read stands in a RETURN: the
// caller already holds the record it passed, so the returned interior
// reference grants it nothing new, and the body lowers instead of
// declining whole.
func TestKernelSummaryDirect_AnInteriorMemberReturnLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface Box<T> { inner: T; }
interface Root { layout: Box<string>; other: number; }
function f(state: Root) { return state.layout.inner; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a returned interior read must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the interior return still falls to the whole-path refusal", construct)
	}
}

// TestKernelSummaryDirect_AnInteriorElementReadReturnedLowers pins
// `return state.axes.byId[id]` — the selectXAxisSettingsNoDefaults
// shape: the undeclared path stands as an ELEMENT ACCESS receiver, and
// the element read's own value stands in a return. The element read
// hands out no more than the interior read itself did, so the position
// that consumes the element decides.
func TestKernelSummaryDirect_AnInteriorElementReadReturnedLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface Box<T> { inner: T; }
interface Root { axes: Box<string>; other: number; }
function f(state: Root, id: string) { return state.axes.byId[id]; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a returned interior element read must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the interior element read still falls to the whole-path refusal", construct)
	}
}

// TestKernelSummaryDirect_AnInteriorPathCalleeHandsOver pins
// `p.ticks.map(String)` — getDomainDefinition's shape: the path past
// the member stands as a CALLEE, which runs code holding the interior
// reference, so the body takes the hand-over havoc and lowers.
func TestKernelSummaryDirect_AnInteriorPathCalleeHandsOver(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { ticks: number[]; type: string }) { p.ticks.map(String); return p.type; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — an interior-path callee must take the hand-over havoc", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the interior callee still falls to the whole-path refusal", construct)
	}
}

// TestKernelSummaryDirect_AConciseArrowInteriorBodyLowers pins the
// CONCISE arrow shape — `(state) => state.options.defaultTooltipEventType`
// (selectTooltipEventType.ts, selectTooltipAxisId.ts): the interior
// path IS the arrow's expression body, which is the return position
// spelled without the keyword.
func TestKernelSummaryDirect_AConciseArrowInteriorBodyLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface Box<T> { inner: T; }
interface Root { options: Box<string>; other: number; }
const f = (state: Root) => state.options.defaultKind;
`)
	var declaration *ast.Node
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, varDeclaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			decl := varDeclaration.AsVariableDeclaration()
			if decl.Initializer != nil && ast.IsArrowFunction(decl.Initializer) {
				declaration = decl.Initializer
			}
		}
	}
	if declaration == nil {
		t.Fatalf("no arrow initializer found")
	}
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a concise arrow's interior body is the return position", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the concise-arrow interior body still falls to the whole-path refusal", construct)
	}
}

// TestKernelSummaryDirect_AReadOnlyInteriorReadStoredInALocalLowers pins
// `const q = p.inner; return q.deep;` — every use of `q` after the store
// is itself a read-only consumer (`q.deep` stands in RETURN position,
// never written), so interiorStoreAliasAdmissible admits the store: `q`
// answers exactly what `p.inner.deep` read directly would have, and no
// write channel opens that the record's own leaf slots do not already
// cover. This was the prior boundary negative — updated to the sound
// answer once the read-only-alias admission landed (the write-through
// boundary is pinned separately below).
func TestKernelSummaryDirect_AReadOnlyInteriorReadStoredInALocalLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { inner: { deep: number } }) { const q = p.inner; return q.deep; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a read-only interior-read alias must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the read-only stored alias still falls to the whole-path refusal", construct)
	}
}

// TestKernelSummaryDirect_AWrittenInteriorAliasStillDeclines is the
// BOUNDARY negative interiorStoreAliasAdmissible keeps: `const q =
// p.inner; q.deep = 5;` writes through the alias — the aliased interior
// value resolves to no slot for that write to land in, so the store
// stays refused exactly as `const q = p` does.
func TestKernelSummaryDirect_AWrittenInteriorAliasStillDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { inner: { deep: number } }) { const q = p.inner; q.deep = 5; return p.inner.deep; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if ok && construct != "a whole-record parameter use" {
		t.Fatalf("f's body lowered (outcome=%q construct=%q) — a written-through interior alias was not meant to be served", outcome, construct)
	}
}

// TestKernelSummaryDirect_AnIndexedInteriorReadStoredAndNullGuardedLowers
// pins axisSelectors.ts's own shape (selectZAxisSettings): `const axis =
// state.cartesianAxis.zAxis[axisId]; if (axis == null) { return
// fallback; } return axis;`. `cartesianAxis`'s own annotation
// (`ReturnType<typeof cartesianAxisReducer>`) carries type arguments, so
// it contributes as an UNKNOWN-SORTED declared leaf rather than a nested
// family (nestedMemberLeavesOf's own TypeReferenceNode arm) — the read
// past it (`.zAxis[axisId]`) is an interior read with no slot at all,
// stored under `axis`. Every later use of `axis` is a null-equality
// test and two returns — both read-only — so the store admits.
func TestKernelSummaryDirect_AnIndexedInteriorReadStoredAndNullGuardedLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface ZAxisSettings { id: number; }
interface AxisMapState { zAxis: Record<string, ZAxisSettings>; }
declare function cartesianAxisReducer(): AxisMapState;
interface RechartsRootState { cartesianAxis: ReturnType<typeof cartesianAxisReducer>; other: number; }
const implicitZAxis: ZAxisSettings = { id: 0 };
function selectZAxisSettings(state: RechartsRootState, axisId: string): ZAxisSettings {
  const axis = state.cartesianAxis.zAxis[axisId];
  if (axis == null) {
    return implicitZAxis;
  }
  return axis;
}
`)
	declaration := entryEnvFunctionNamed(t, p, "selectZAxisSettings")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("selectZAxisSettings's body declined: outcome=%q construct=%q — a null-guarded read-only interior alias must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the indexed interior alias still falls to the whole-path refusal", construct)
	}
}
