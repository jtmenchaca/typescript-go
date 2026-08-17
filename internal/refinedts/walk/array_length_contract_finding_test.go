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
// parameter fires the specific 7001 finding and no 7002 anywhere —
// the Array<number> declaration itself adds nothing and stays silent.
func TestArrayLengthContract_UnpinnedLengthFiresTheRangeErrorFinding(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		function f(tickCount: number, cormin: number): Array<number> {
			const values: Array<number> = [cormin, ...Array(tickCount - 1).fill(Infinity)];
			return values;
		}
	`, "f")
	findings := 0
	for _, d := range diagnostics {
		if d.Code == 7001 && strings.Contains(d.MessageText, "RangeError") {
			findings++
		}
		if d.Code == 7002 {
			t.Errorf("f fired 7002 (%s) — the length row speaks its own finding and the adds-nothing target stays silent", d.MessageText)
		}
	}
	if findings != 1 {
		t.Errorf("f fired %d RangeError findings, want exactly 1", findings)
	}
}

// TestArrayLengthContract_APinnedGoodLengthStaysSilent pins the
// discharged twin: an exact in-range length fires nothing.
func TestArrayLengthContract_APinnedGoodLengthStaysSilent(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		function g(cormin: number): Array<number> {
			const values: Array<number> = [cormin, ...Array(5).fill(Infinity)];
			return values;
		}
	`, "g")
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("g fired %d (%s) — an exact in-range length discharges the contract", d.Code, d.MessageText)
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
