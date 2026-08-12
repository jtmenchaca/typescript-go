// Ports control_flow/dead_guard.test.ts.
//
// NOT PORTED: every case calls service/check.ts's whole-file `check()`
// and asserts on `result.refinements` (either a length/code pair for a
// provably-false guard firing 7001, or an empty list for a guard that
// must stay live) — service/check.ts has no Go twin yet (service/ is
// a later wave, per go-port-tracker.md's own "service | pending
// (last)" row). Each case below names its TS twin and the real-world
// source line it guards against (nest, tailwindcss, prisma,
// recharts, postgres-driver, framework-authoring) so the eventual
// service/ porter can find them.
package walk

import "testing"

func TestDeadGuard_AForInKeyAgainstAnInlineFunctionTestFires(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_APredicateHelpersVacuousCallFiresThroughTheFold(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ARecursiveCallsReferenceArgumentForgetsItsGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ACalleesClosureWriteReachesTheLoopModelItsGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_APushBeforeContinueReachesTheFixpointItsGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AnArrowInsideAnObjectLiteralArgumentStillHandsOver(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_StringifysSpecImageKeepsItsGuardAlive(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AnOverriddenMethodDispatchesVirtuallyItsGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ACapturedReferenceArgumentForgetsItsGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_APredicatesInternalNarrowingNeverEscapesTheCall(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_NestedPredicatesStayEffectFreeNarrowingStillNeverEscapes(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ALiteralDisjunctionShedsBothBranchUnions(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AHeldDisjunctionPinsAPlainStringToItsWords(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ALibGetsLiteralUnionReturnKeepsItsWords(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ATypeofObjectGroundLeavesArrayIsArrayLive(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AnOptionalParameterAdmitsAnUndefinedArgument(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AConstantFlagIsExemptDeliberateDeadCode(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AnUndeterminedGuardStaysSilent(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_AConstructorInterfaceWearsTheFunctionWordItsUnionGuardLives(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}

func TestDeadGuard_ABooleanErasedThroughAnyNeverFoldsToAScalarWord(t *testing.T) {
	t.Skip("NOT PORTED: needs service/check.ts (service/ tier, not ported yet)")
}
