// Ports control_flow/flow_state_at_position.test.ts.
//
// NOT PORTED: every case calls service/hover_provider.ts's
// formatRefinementAt (via service/program_host.ts's
// programFromSource) — neither has a Go twin yet (service/ is a
// later wave, per go-port-tracker.md's own "service | pending
// (last)" row; confirmed no FormatRefinementAt symbol exists
// anywhere under internal/refinedts). AnswerFlowAt (walk/
// flow_state_at_position.go, already ported) is the dispatcher
// formatRefinementAt calls into once it exists, so these cases will
// port directly once the hover-provider seam lands — nothing here is
// blocked on walk itself.
package walk

import "testing"

func TestFlowStateAtPosition_AnExactComputedConstSpellsItsValue(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ACompoundAssignmentChainTracksPerStatement(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_StringsBooleansObjectsNaNWhereTscSaysLess(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ALiteralConstAlreadyReadsInTheTypeLineNoBraces(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ABigintLiteralIsExactAtAnyWidth(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ConditionalPositionsDeclineRatherThanMislead(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_InsideAFunctionBodyAParametersStatedSetFlows(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ABranchAppliesItsGuardALoopBodyItsInvariant(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_ABranchAppliesTheGuardsValueCopyLikeTheWalk(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_AStatedAnnotationWinsOverFlowKnowledge(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_BooleanArrayParameterStatesEach0Or1(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}

func TestFlowStateAtPosition_GlobalThisNumberNaNIsNaN(t *testing.T) {
	t.Skip("NOT PORTED: needs service/hover_provider.ts's formatRefinementAt (service/ tier, not ported yet)")
}
