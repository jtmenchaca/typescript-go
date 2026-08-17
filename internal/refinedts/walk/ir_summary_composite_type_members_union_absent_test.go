// Pins for a UNION annotation carrying an undefined/null keyword arm —
// unionMembersOf/unionArmMembersOf's CURRENT reading of such an arm,
// before any widening.
package walk

import (
	"testing"
)

func TestUnionMembersOf_AnUndefinedArmDeclinesTheWholeExpansion(t *testing.T) {
	ClearResolvedRecordMembers()
	// tmp/recharts-src/src/chart/Sankey.tsx:46's own shape:
	// `entry: LinkDataItem | SankeyNode | undefined`. Both record arms
	// declare `value`; the third arm is the bare `undefined` keyword.
	//
	// recordParamMembersIn's isRecord bool is the layer that actually
	// reflects the union expansion — SummaryParameterEntriesIn's own
	// `ok` stays true regardless (a declined record still falls through
	// to the ordinary single unknown-sorted whole-name slot), so
	// asserting on SummaryParameterEntriesIn's `ok` alone would pin the
	// fallback, not the union reader under test.
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode | undefined) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	t.Logf("isRecord=%v members=%+v", isRecord, members)
	if isRecord {
		t.Fatalf("undefined arm now expands (was declining) — members: %+v; update this pin and the brief, this is progress on Task 2, not a regression", members)
	}
}

func TestUnionMembersOf_ANullArmDeclinesTheWholeExpansion(t *testing.T) {
	ClearResolvedRecordMembers()
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode | null) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	t.Logf("isRecord=%v members=%+v", isRecord, members)
	if isRecord {
		t.Fatalf("null arm now expands (was declining) — members: %+v; update this pin and the brief, this is progress on Task 2, not a regression", members)
	}
}

func TestUnionMembersOf_TwoRecordArmsSharingAMemberSurviveWithNoAbsentArm(t *testing.T) {
	ClearResolvedRecordMembers()
	// The two-record part of the same Sankey shape, isolated: no
	// undefined/null arm at all. Both LinkDataItem and SankeyNode declare
	// `value` (LinkDataItem's is number-sorted, SankeyNode's is `any` and
	// so contributes unknown-sorted) — the two sorts DISAGREE
	// (BindingKindNumber vs BindingKindUnknown), which unionMembersOf's
	// own merge rule refuses (a member the arms sort differently is a
	// member no one slot can stand for) — so this is expected to decline
	// on the SORT MISMATCH, not on the union machinery itself. Pinning
	// this establishes that the two-record survival case is limited by
	// SankeyNode's own `any`-typed member, a fact independent of the
	// undefined-arm question this task is about.
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	t.Logf("isRecord=%v members=%+v", isRecord, members)
	if isRecord {
		t.Fatalf("two record arms with a sort-disagreeing shared member now expand (was declining) — members: %+v; update this pin, this is a distinct fix from Task 2's undefined-arm question", members)
	}
}

func TestUnionMembersOf_TwoRecordArmsSharingAnAgreeingMemberSurvive(t *testing.T) {
	ClearResolvedRecordMembers()
	// The same two-record shape with SankeyNode's `value` narrowed to
	// number (matching LinkDataItem's), so the sorts AGREE and the union
	// of the two record arms alone (no undefined arm) is expected to
	// expand today, independent of any undefined-arm fix.
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: number; }
function f(entry: LinkDataItem | SankeyNode) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	if !isRecord {
		t.Fatalf("two record arms sharing an agreeing member declined — members: %+v", members)
	}
	if len(members) != 1 {
		t.Fatalf("len(members) = %d, want 1 (value): %+v", len(members), members)
	}
	if members[0].Key != "value" || members[0].Sort != BindingKindNumber {
		t.Errorf("member = %+v, want Key=value Sort=number", members[0])
	}
}
