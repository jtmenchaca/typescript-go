// Pin for the `in`-operator gap: a whole-record parameter standing as
// the RIGHT operand of `'key' in p` (axisSelectors.ts's
// `getDomainDefinition`: `if (axisSettings == null || !('domain' in
// axisSettings)) { … }`). HasProperty (sec-relational-operators-runtime-
// semantics-evaluation, specifications/javascript/spec.html) reads the right operand's
// value and answers a fresh boolean — the same read-only shape the
// truthiness/equality/typeof arms already cover — but wholeRecordUseAt
// had no case for it, so the whole use fell to the default refusal.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestKeyboardMiddlewareShapedGenericParameter_PremiseCheck is a
// DIAGNOSIS pin, not a fix: keyboardEventsMiddleware.ts's effect
// functions take `listenerApi: ListenerEffectAPI<RechartsRootState,
// AppDispatch>` — a TYPE REFERENCE WITH TYPE ARGUMENTS applied.
// namedTypeMembersOf refuses a reference carrying type arguments before
// reaching a member reader (constraintMembersOf's own doc: "type
// arguments and qualified names are refused... before this is
// reached") — so `listenerApi` never becomes a record-expanded
// parameter at all, and `listenerApi.getState()` / `.dispatch(...)` are
// ordinary member reads on an UN-expanded name, never routed through
// recordParameterUseOf. This pin checks what construct the body
// actually declines on, to tell whether keyboardEventsMiddleware.ts's
// ×3 count is this family at all.
func TestKeyboardMiddlewareShapedGenericParameter_PremiseCheck(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface Listener<S, D> {
  getState(): S;
  dispatch(action: unknown): D;
}
interface RootState {
  rootProps: { accessibilityLayer: boolean };
}
function effect(listenerApi: Listener<RootState, unknown>) {
  const state: RootState = listenerApi.getState();
  const accessibilityLayerIsActive = state.rootProps.accessibilityLayer !== false;
  if (!accessibilityLayerIsActive) {
    return;
  }
}
`)
	declaration := entryEnvFunctionNamed(t, p, "effect")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("keyboard-middleware-shaped body: outcome=%q construct=%q", outcome, construct)
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — a generic-typed parameter with type arguments reached the record-parameter-use scan, which namedTypeMembersOf's refusal should have prevented", construct)
	}
}

// TestGetDomainDefinitionShapedBody_ExactAxisSelectorsSourceServes pins
// the REAL axisSelectors.ts source, verbatim in shape:
// `getDomainDefinition`'s `if (axisSettings == null || !('domain' in
// axisSettings)) { return defaultNumericDomain; }` — the `in` fix's
// target specimen — through the checker-backed harness (a named
// interface type, not the inline-literal fixture the earlier pin
// used), matching how the real file resolves `AllAxisSettings`.
func TestGetDomainDefinitionShapedBody_ExactAxisSelectorsSourceServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface AxisSettings {
  domain: [number, number];
  ticks: number[];
  type: string;
  tickCount: number;
}
const defaultNumericDomain: [number, string] = [0, 'auto'];
function getDomainDefinition(axisSettings: AxisSettings) {
  if (axisSettings == null || !('domain' in axisSettings)) {
    return defaultNumericDomain;
  }
  return axisSettings.domain;
}
`)
	declaration := entryEnvFunctionNamed(t, p, "getDomainDefinition")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("getDomainDefinition-shaped body: outcome=%q construct=%q", outcome, construct)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the in-operator fix should carry the real axisSelectors.ts shape through", outcome, construct)
	}
}

// TestKernelSummaryDirect_AnInOperatorRightOperandReadsMemberSafe pins
// `function f(p: { lo: number }) { if ('lo' in p) { return p.lo; } return
// 0; }` — before the fix this declined as "a whole-record parameter
// use" (the `in` right operand matched no arm in wholeRecordUseAt and
// fell to recordParameterUnreadable); after, it is member-safe
// (recordParameterReadWhole) and the body lowers.
func TestKernelSummaryDirect_AnInOperatorRightOperandReadsMemberSafe(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { if ('lo' in p) { return p.lo; } return 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("before-fix baseline: ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

// TestKernelSummaryDirect_AnInOperatorLeftOperandStillDeclines is the
// BOUNDARY negative: `p` standing as the LEFT operand of `in`
// (`p in q`, treating the record as the PROPERTY KEY) is not a shape
// axisSelectors.ts uses and this fix does not cover it — ToPropertyKey
// coerces the left operand to a string/symbol, which is still a read,
// but the fix below only recognizes the RIGHT-operand position
// (matching the equality/instanceof arms' own left-or-right symmetry
// would be a separate, deliberate widening). This pin proves the scan
// still declines a shape the new arm does not claim to serve.
func TestKernelSummaryDirect_AnInOperatorLeftOperandStillDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }, q: object) { if (p in q) { return 1; } return 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if ok {
		t.Fatalf("f's body lowered — the left-operand shape was not meant to be served yet")
	}
}

// TestCenterYShapedRecordParameter_MidPositionCallArgumentHandsOverWhole
// is a PREMISE CHECK, not a new fix: Treemap.tsx's `position` hands
// `parentRect: RectanglePosition` to `horizontalPosition(row, parentSize,
// parentRect, isFlush)` — the record argument sits in the MIDDLE of a
// four-argument call, not alone. wholeRecordUseAt's call/new argument
// arm walks every argument slot looking for the node (ir_summary_record_
// parameter_uses.go's argumentsOf loop), so position never depended on
// being the sole or first argument. `ok=true` here is the whole claim —
// the callee `horizontal` is left UNDECLARED on purpose, so the outcome
// lands porous on "return (call horizontal)" (an unresolved-callee
// question this pin does not police), never on "a whole-record
// parameter use": the hand-over arm itself already serves the body.
func TestCenterYShapedRecordParameter_MidPositionCallArgumentHandsOverWhole(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		`function position(row: number, parentSize: number, parentRect: { x: number; y: number }, isFlush: boolean) {
			return horizontal(row, parentSize, parentRect, isFlush);
		}`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("position's body declined: outcome=%q construct=%q — the mid-position hand-over regressed", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("outcome = %q (construct %q), the hand-over arm itself regressed", outcome, construct)
	}
}

// TestCenterYShapedRecordParameter_SpreadReturnOfARecordParameterServes
// is a PREMISE CHECK for horizontalPosition/verticalPosition's own
// shape: `return { ...parentRect, y: parentRect.y + rowHeight, height:
// parentRect.height - rowHeight };` spreads the whole parameter into a
// fresh return object — already the SpreadAssignment read-whole arm
// (wholeRecordUseAt). Confirms the spread-in-a-returned-literal shape,
// not merely a bare `{ ...p }`, still classifies read-whole.
func TestCenterYShapedRecordParameter_SpreadReturnOfARecordParameterServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		`function horizontal(rowHeight: number, parentRect: { x: number; y: number; height: number }) {
			return { ...parentRect, y: parentRect.y + rowHeight, height: parentRect.height - rowHeight };
		}`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("horizontal's body declined: outcome=%q construct=%q — the spread-return shape regressed", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}
