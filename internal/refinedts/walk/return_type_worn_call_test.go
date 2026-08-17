// The declared-return ground at call results, both new consumers
// (evaluate_call_expression.go's wornReturnTypeIfUnknown and
// unmodeled_call_result.go's bodiless arm): a BODILESS callee whose
// return type states a literal union wears the union at library grade
// instead of the opaque unknown, and an INLINED callee whose recovery
// answers nothing wears the same ground under the body-in-reach
// standing. The fixtures mirror the recharts hook sites — ErrorBar's
// useErrorBarDirection over useChartLayout, and usePolarChartLayout's
// return-position useAppSelector.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

func wornCallDiagnostics(t *testing.T, source string, functionName string) []assignability.RefinementDiagnostic {
	t.Helper()
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, source)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	var diagnostics []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	declaration := entryEnvFunctionNamed(t, p, functionName)
	AnalyzeFunction(ctx, &FunctionContract{Declaration: declaration}, nil)
	return diagnostics
}

// TestWornReturnType_BodilessLiteralUnionServesAtTheDeclaration pins
// the ErrorBar.tsx:289 shape: a same-file helper narrows a bodiless
// hook's literal-union result and feeds an annotated local. The hook's
// stated union serves, the ternary's arms narrow inside it, and the
// annotated declaration determines — no 7002.
func TestWornReturnType_BodilessLiteralUnionServesAtTheDeclaration(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		declare function useChartLayoutish(): 'horizontal' | 'vertical' | 'centric' | 'radial';
		function useDirection(d: 'x' | 'y' | undefined): 'x' | 'y' {
			const layout = useChartLayoutish();
			if (d != null) {
				return d;
			}
			if (layout != null) {
				return layout === 'horizontal' ? 'y' : 'x';
			}
			return 'x';
		}
		function component(d: 'x' | 'y' | undefined): 'x' | 'y' {
			const realDirection: 'x' | 'y' = useDirection(d);
			return realDirection;
		}
	`, "component")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("component fired 7002 (%s) — the bodiless hook's stated union should serve through the inline", d.MessageText)
		}
	}
}

// TestWornReturnType_ReturnPositionBodilessCallDetermines pins the
// chartLayoutContext.tsx:157 shape: a bodiless call directly in return
// position, with `| undefined` in its stated type, wears the
// maybe-wrapped union and the declared return check determines.
func TestWornReturnType_ReturnPositionBodilessCallDetermines(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		declare function useSelectorish(): 'centric' | 'radial' | undefined;
		function usePolar(): 'centric' | 'radial' | undefined {
			return useSelectorish();
		}
	`, "usePolar")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("usePolar fired 7002 (%s) — the bodiless call's stated union should wear through return position", d.MessageText)
		}
	}
}

// TestWornReturnType_GenericInstantiationServesTheSelectorUnion pins
// the selectTooltipEventType.ts:34 shape: a generic bodiless hook
// handing back its selector's own return type — the call's
// INSTANTIATED type is the union, and it serves.
func TestWornReturnType_GenericInstantiationServesTheSelectorUnion(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		declare function useAppSelectorish<T>(selector: (s: { layout: 'a' | 'b' }) => T): T;
		function pickIt(): 'a' | 'b' {
			return useAppSelectorish(s => s.layout);
		}
	`, "pickIt")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("pickIt fired 7002 (%s) — the instantiated return type states the union", d.MessageText)
		}
	}
}

// TestWornReturnType_NarrowedUnionReturnDetermines pins the
// chartLayoutContext.tsx:143 shape: the hook result narrows by
// equality against two of its words and returns inside the guard.
func TestWornReturnType_NarrowedUnionReturnDetermines(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		declare function useChartLayoutish(): 'horizontal' | 'vertical' | 'centric' | 'radial';
		function useCartesian(): 'horizontal' | 'vertical' | undefined {
			const layout = useChartLayoutish();
			if (layout === 'horizontal' || layout === 'vertical') {
				return layout;
			}
			return undefined;
		}
	`, "useCartesian")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("useCartesian fired 7002 (%s) — the narrowed union should carry through the guarded return", d.MessageText)
		}
	}
}
