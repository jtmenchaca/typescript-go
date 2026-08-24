// The row this file pins: n-dual-stack.ts:69 (marker at 68,
// recursiveSelfCall). `countdown`'s body is `if (n <= 0) return 0;
// return countdown(n - 1);` — a self-recursive call while its own
// summary is still in flight. `countdown(depth)` is called with an
// UNBOUNDED `depth: number`, which admits NaN and Infinity: for
// either, `n <= 0` never decides true and the recursion never
// terminates. The fixture expects `return countdown(depth);` to FIRE
// (the value is not honestly determined), and nothing did.
//
// The bug this pins: JoinSinkSummarized (function_summaries.go) is
// the join every recursive walk's return sink runs through — a
// self-call reached while the SAME symbol is already being inlined
// answers a recursion MARKER (RecursionMarker) rather than re-walking
// (the cycle-cut). JoinSinkSummarized used to EXCLUDE every marker
// entry from the join outright ("a sink joined with pass-through
// markers dropped") — so `countdown`'s sink `[{0} (the base case),
// marker (the recursive branch)]` joined to the base case ALONE:
// `countdown(depth)` answered the exact literal 0 for every depth,
// including NaN/Infinity, where the recursive branch never actually
// resolves to 0 or to anything else. 0 sits inside Age's [0,120]
// window, so the call read as silently in-set — the marker's "this
// branch is still being derived" fact never reached the join, which
// is the sense in which the in-flight cycle FORGOT the bound instead
// of admitting it could not determine it.
//
// The fix: a marker still counts as a drop (MarkerDropCount's
// memo-safety bookkeeping is unchanged), but now also joins into the
// result as an unknown contribution — abstractdomain.JoinKnown
// degrades any concrete base joined with a plain unknown to unknown,
// so `countdown`'s sink now joins to KindUnknown, which fires the
// honest 7002 alert once checked against Age.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

// TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThanBeingExcluded
// is the unit-level pin, no program/kernel needed: a sink holding one
// concrete base and one recursion marker must join to KindUnknown, not
// to the concrete base alone.
func TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThanBeingExcluded(t *testing.T) {
	symbol := &ast.Symbol{Name: "countdownTestSymbol"}
	base := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	// no ctx/declaration: this unit level pin needs no program in reach,
	// so RecursionMarker falls back to the bare unknown — its own doc on
	// that fallback (function_summaries.go)
	marker := RecursionMarker(nil, symbol, nil)
	dropsBefore := MarkerDropCount()

	joined := JoinSinkSummarized([]abstractdomain.AbstractValue{base, marker})

	if joined.Kind != abstractdomain.KindUnknown {
		spelled, _ := abstractdomain.FormatAbstractValue(joined)
		t.Errorf("JoinSinkSummarized([0, marker]) = %q, want KindUnknown — the marker's in-flight branch must poison the join, not be excluded from it", spelled)
	}
	if MarkerDropCount() != dropsBefore+1 {
		t.Errorf("MarkerDropCount did not advance by 1 across one marker in the sink — the memo-safety bookkeeping regressed")
	}
}

// TestJoinSinkSummarized_AllMarkersStillAnswerResidue is the boundary
// this fix must not disturb: a sink of ONLY markers already answered
// residue before this fix (len(bases) == 0 short-circuits before the
// new unknown-join line ever runs) and must keep doing so.
func TestJoinSinkSummarized_AllMarkersStillAnswerResidue(t *testing.T) {
	symbol := &ast.Symbol{Name: "onlyMarkersTestSymbol"}
	marker := RecursionMarker(nil, symbol, nil)

	joined := JoinSinkSummarized([]abstractdomain.AbstractValue{marker, marker})

	if joined.Kind != abstractdomain.KindUnknown {
		spelled, _ := abstractdomain.FormatAbstractValue(joined)
		t.Errorf("JoinSinkSummarized([marker, marker]) = %q, want KindUnknown (residue)", spelled)
	}
}

const recursiveSelfCallSource = "function recursiveSelfCall(depth: number): number {\n" +
	"  function countdown(n: number): number {\n" +
	"    if (n <= 0) return 0;\n" +
	"    return countdown(n - 1);\n" +
	"  }\n" +
	"  return countdown(depth);\n" +
	"}\n"

// TestRecursiveSelfCall_TheInFlightCycleFiresRatherThanAnsweringZero is
// the end-to-end pin, through AnalyzeFunction over the whole body —
// the same door the fixture's own check runs through. countdown(depth)
// must fire the undetermined alert against Age's [0,120] window rather
// than silently pinning the base case's 0.
func TestRecursiveSelfCall_TheInFlightCycleFiresRatherThanAnsweringZero(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, recursiveSelfCallSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, "recursiveSelfCall")
	var diagnostics []assignability.RefinementDiagnostic
	bodyCtx := *ctx
	bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	contract := &FunctionContract{
		Declaration: fn,
		Result:      withReachStatedWindow(0, 120),
		Grounded:    true,
	}
	AnalyzeFunction(&bodyCtx, contract, nil)

	fired := false
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			fired = true
		}
	}
	if !fired {
		t.Errorf("recursiveSelfCall did not fire for countdown(depth) — the in-flight recursive branch was silently excluded from the join instead of poisoning it to unknown")
	}
}
