// The indexOf-sentinel row (string_method_models.go): a slice whose
// index window still admits the search's -1 is a FINDING (7001), not
// an undetermined verdict — the slice's own value determines
// string-sorted whatever the window, so the body carries no 7002. The
// recharts twin is DataUtils.ts getPercentValue, where isPercent("")
// admits the empty string and indexOf('%') then answers -1 unguarded.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

// TestStringSentinelRow_UnguardedSliceFiresAFindingNotUndetermined pins
// both halves on the unguarded shape: the 7001 finding names the
// sentinel, and no 7002 appears — the value serves.
func TestStringSentinelRow_UnguardedSliceFiresAFindingNotUndetermined(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, `
		function cut(s: string): string {
			const i = s.indexOf('%');
			return s.slice(0, i);
		}
	`)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	var diagnostics []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	declaration := entryEnvFunctionNamed(t, p, "cut")
	AnalyzeFunction(ctx, &FunctionContract{Declaration: declaration}, nil)
	sentinelFindings := 0
	for _, d := range diagnostics {
		if d.Code == 7001 && strings.Contains(d.MessageText, "search's -1") {
			sentinelFindings++
		}
		if d.Code == 7002 {
			t.Errorf("cut fired 7002 (%s) — the slice's value determines string-sorted; the sentinel row is a finding, never an undetermined verdict", d.MessageText)
		}
	}
	if sentinelFindings != 1 {
		t.Errorf("cut fired %d sentinel findings, want exactly 1 — the unguarded -1 window names the hazard once", sentinelFindings)
	}
}

// TestStringSentinelRow_TheGuardDischargesTheFinding pins the guarded
// twin: `i !== -1` cuts the sentinel from the window, so nothing fires.
func TestStringSentinelRow_TheGuardDischargesTheFinding(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, `
		function cutGuarded(s: string): string {
			const i = s.indexOf('%');
			if (i !== -1) {
				return s.slice(0, i);
			}
			return s;
		}
	`)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	var diagnostics []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	declaration := entryEnvFunctionNamed(t, p, "cutGuarded")
	AnalyzeFunction(ctx, &FunctionContract{Declaration: declaration}, nil)
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("cutGuarded fired %d (%s) — the !== -1 guard discharges the sentinel and the body determines", d.Code, d.MessageText)
		}
	}
}
