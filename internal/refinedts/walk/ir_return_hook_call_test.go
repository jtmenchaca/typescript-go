// Pins for the return-position IMPORTED HOOK CALL arm: `return
// useAppSelector(selector)`, `return useContext(SomeContext)`. Census
// rows "return (call useAppSelector)" ×11, "return (call useContext)"
// ×7 — the fixture mirrors tmp/recharts-src/src/context/
// chartLayoutContext.tsx's usePolarChartLayout-shaped bodies and
// state/hooks.ts's useAppDispatch, whose imports resolve outside the
// entry file the same way twoFileTestProgram builds them
// (imported_hook_ground_test.go's own two-file pattern).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

const returnHookCallHooksSource = `
	declare function useSelector<T>(selector: (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => T): T;
	export const useAppSelector: <T>(selector: (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => T) => T = useSelector;

	declare function useContextImpl<T>(context: T): T;
	export const useContext: <T>(context: T) => T = useContextImpl;
`

// TestReturnHookCall_UseAppSelectorCompletes pins the usePolarChartLayout
// shape (chartLayoutContext.tsx: `return useAppSelector(selectPolarChartLayout);`)
// — a return whose whole value IS a bare call through a cross-file
// imported hook. Before this arm the return route's every reading
// declined for this shape (no FunctionContract resolves useAppSelector,
// so InlineCall and the summary-call-statement arm both refuse) and fell
// to the opaque return, which still lowers COMPLETE-shaped control but
// notes the havoc — porous, not declined outright, but not what the
// corpus's own useAppSelector shape should settle at once the imported-
// hook recognizer covers a return-position call the same way it already
// covers a statement-position one.
func TestReturnHookCall_UseAppSelectorCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelector } from './hooks';
		const selectPolarChartLayout = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		export function usePolarChartLayout() {
			return useAppSelector(selectPolarChartLayout);
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "usePolarChartLayout")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for usePolarChartLayout")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — a bare return of a vouched-for imported hook call should serve, unknown value, no tracked slot havocked", outcome, construct, ok)
	}
}

// TestReturnHookCall_UseContextCompletes pins the useAppDispatch shape's
// FIRST statement in isolation — `return useContext(SomeContext);` — a
// bare cross-file hook call in return position with a single identifier
// argument (no function-literal selector at all).
func TestReturnHookCall_UseContextCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useContext } from './hooks';
		declare const RechartsReduxContext: { store: { dispatch: unknown } } | null;
		export function useRawContext() {
			return useContext(RechartsReduxContext);
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useRawContext")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for useRawContext")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — return useContext(x) with a writeAndCallFree argument should serve", outcome, construct, ok)
	}
}
