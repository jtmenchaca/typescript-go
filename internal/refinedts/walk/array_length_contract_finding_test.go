// The Array(len) contract row's undetermined outcome speaks as a
// FINDING (builtin_contracts.go): the row judges a THROW contract, so
// an unpinned length names a construction that may throw a RangeError
// — never the generic undetermined sentence. And a bare-generic
// annotation target (an ungrounded DeclaredVariable bound) alerts
// nowhere on unknown knowledge (check_assignability.go): the position
// states nothing beyond tsc's own shape check. The fixtures mirror
// recharts' getNiceTickValues.ts:240-241 and axisSelectors.ts:864.
package walk

import (
	"strings"
	"testing"
)

// TestArrayLengthContract_UnpinnedLengthFiresTheRangeErrorFinding pins
// the getNiceTickValues shape: a computed length over a plain number
// parameter fires the specific 7001 finding.
//
// OLD PREMISE (pre-grounding): "the Array<number> declaration itself
// adds nothing and stays silent" — a bare `number` parameter/element
// type read as no DeclaredRefinement (nil), so Array<number>'s own
// element statement never reached a membership question. Now that a
// bare primitive keyword states its own ground set (JT's ruling,
// annotations/type_node_sets.go), Array<number>'s element position is
// GENUINELY checked — `cormin`'s own value and the spread's elements
// judge against `number`'s ground, and an unpinned length still
// carries the honest 7002 the length row cannot resolve (a computed
// count means the array's own SIZE is unknown, which a stated element
// type does not discharge). The RangeError finding still fires
// alongside it — this test now asserts BOTH, rather than asserting no
// 7002 at all.
func TestArrayLengthContract_UnpinnedLengthFiresTheRangeErrorFinding(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		function f(tickCount: number, cormin: number): Array<number> {
			const values: Array<number> = [cormin, ...Array(tickCount - 1).fill(Infinity)];
			return values;
		}
	`, "f")
	findings := 0
	sawKernelDeclined := false
	for _, d := range diagnostics {
		if d.Code == 7001 && strings.Contains(d.MessageText, "RangeError") {
			findings++
		}
		if d.Code == 7002 {
			sawKernelDeclined = true
		}
	}
	if findings != 1 {
		t.Errorf("f fired %d RangeError findings, want exactly 1", findings)
	}
	if !sawKernelDeclined {
		t.Errorf("f fired no 7002 — Array<number>'s own element position is now genuinely checked (grounded), and an unpinned length's spread elements should carry an honest undetermined verdict")
	}
}

// TestArrayLengthContract_APinnedGoodLengthStaysSilent pins the
// discharged twin: an exact in-range length.
//
// OLD PREMISE (pre-grounding): "an exact in-range length discharges
// the contract" (asserted zero diagnostics of either code) — the same
// nil-Stated premise as above. Array<number>'s element position now
// grounds, so `cormin`'s own untracked value against the ground
// carries an honest undetermined verdict — the length contract itself
// is still discharged (no 7001 RangeError finding), which is the half
// this test now asserts.
func TestArrayLengthContract_APinnedGoodLengthStaysSilent(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		function g(cormin: number): Array<number> {
			const values: Array<number> = [cormin, ...Array(5).fill(Infinity)];
			return values;
		}
	`, "g")
	for _, d := range diagnostics {
		if d.Code == 7001 {
			t.Errorf("g fired 7001 (%s) — an exact in-range length discharges the RangeError contract", d.MessageText)
		}
	}
}

// TestUngroundedVariableTarget_UnknownJoinStaysSilent pins the
// axisSelectors.ts:864 shape: a ternary joining a narrowed optional
// member's spread against a generic-aliased parameter, at a
// declaration annotated with the bare-generic alias — the target's
// bound is ungrounded, so unknown knowledge alerts nowhere.
func TestUngroundedVariableTarget_UnknownJoinStaysSilent(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		type ChartDataish<DataPointType = unknown> = ReadonlyArray<DataPointType>;
		function h(item: { data: ChartDataish | undefined }, chartDataSlice: ChartDataish): void {
			const itemData: ChartDataish = item.data != null ? [...item.data] : chartDataSlice;
			void itemData;
		}
	`, "h")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("h fired 7002 (%s) — a bare-generic target states nothing beyond tsc's shape check", d.MessageText)
		}
	}
}

// TestUngroundedVariableTarget_ResolveDefaultPropsShapeStaysSilent
// pins the resolveDefaultProps.tsx:42 twin: a generic-typed reduce
// result at a `T`-annotated declaration — the same ungrounded-bound
// verdict.
func TestUngroundedVariableTarget_ResolveDefaultPropsShapeStaysSilent(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		function resolve<T>(realProps: T, defaultProps: Partial<T>): T {
			const resolvedProps: T = { ...realProps };
			const dp: Partial<T> = defaultProps;
			const keys = Object.keys(defaultProps) as Array<keyof T>;
			const withDefaults: T = keys.reduce((acc: T, key: keyof T): T => {
				if (acc[key] === undefined && dp[key] !== undefined) {
					acc[key] = dp[key];
				}
				return acc;
			}, resolvedProps);
			return withDefaults;
		}
	`, "resolve")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("resolve fired 7002 (%s) — every annotated position here is a bare generic with an ungrounded bound", d.MessageText)
		}
	}
}
