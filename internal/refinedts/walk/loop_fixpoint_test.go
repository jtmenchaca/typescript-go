// Ports control_flow/loop_fixpoint.test.ts.
//
// NOT PORTED: every case calls service/check.ts's whole-file `check()`
// against a source importing the surface/z.ts stand-in — service/
// check.ts and the surface fixture have no Go twin yet (service/ is a
// later wave, per go-port-tracker.md's own "service | pending (last)"
// row). Each case exercises the loop fixpoint end to end against the
// live kernel (iterate, widen, kernel certifies the invariant, one
// checked pass) — walk/loop_fixpoint.go (SolveLoop and friends) is
// already ported; only the whole-pipeline `check()` entry these cases
// call through is missing.
package walk

import "testing"

func TestLoopFixpoint_ACountedWhileProvesItsBoundsHavocInvalidated(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_AnAccumulatorOverAForOfProvesItsFloor(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_ABreakFreesTheExitFromTheRefutedCondition(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_ADoWhileBodyRunsBeforeTheConditionIsTested(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_AnUnknownStepStillAlertsHonestly(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_AForStatementsCounterCertifiesThroughItsOwnClauses(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestLoopFixpoint_ALoopConditionsValueCopyRidesIntoTheBody(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}
