// Pins for a UNION annotation carrying an undefined/null arm and for
// arms whose shared member sorts disagree — unionMembersOf's absent-arm
// and sort-degrade rules (the doc on unionMembersOf argues both: an
// absent arm is excluded and every survivor wears MayBeAbsent, sound by
// the continuing-run argument — a leaf read through an absent holder
// throws; a sort disagreement degrades the member to unknown-sorted
// rather than declining the expansion).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestUnionMembersOf_AnUndefinedArmExpandsWithEveryLeafMayBeAbsent(t *testing.T) {
	ClearResolvedRecordMembers()
	// tmp/recharts-src/src/chart/Sankey.tsx:46's own shape:
	// `entry: LinkDataItem | SankeyNode | undefined`. Both record arms
	// declare `value` (number vs any — the sorts disagree, so the leaf
	// degrades to unknown-sorted); the third arm is the bare `undefined`
	// keyword, excluded from the intersection and marking the survivor
	// MayBeAbsent.
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode | undefined) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	if !isRecord {
		t.Fatalf("the undefined-armed union declined — members: %+v", members)
	}
	if len(members) != 1 || members[0].Key != "value" {
		t.Fatalf("members = %+v, want exactly the shared `value` leaf", members)
	}
	if members[0].Sort != BindingKindUnknown {
		t.Errorf("value's sort = %v, want unknown (number vs any disagree)", members[0].Sort)
	}
	if !members[0].MayBeAbsent {
		t.Errorf("value is not MayBeAbsent — the undefined arm must mark every survivor")
	}
}

func TestUnionMembersOf_ANullArmExpandsWithEveryLeafMayBeAbsent(t *testing.T) {
	ClearResolvedRecordMembers()
	// the null twin: a leaf read through a null holder throws exactly as
	// through an undefined one (RequireObjectCoercible), so the member
	// layer treats the two alike — only the `&&`-fold distinguishes them
	// (a null holder's short-circuit value is null, which a leaf slot's
	// absent admission does not spell).
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode | null) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	if !isRecord {
		t.Fatalf("the null-armed union declined — members: %+v", members)
	}
	if len(members) != 1 || !members[0].MayBeAbsent {
		t.Fatalf("members = %+v, want the one shared leaf wearing MayBeAbsent", members)
	}
}

func TestUnionMembersOf_ASortDisagreementDegradesTheLeafInsteadOfDeclining(t *testing.T) {
	ClearResolvedRecordMembers()
	// no absent arm at all: the two record arms share `value` at
	// disagreeing sorts (number vs any/unknown). The member keeps its
	// NAME and loses its sort — unknown claims nothing about the value
	// and still promises the name every arm declares.
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
interface SankeyNode { dx: number; dy: number; value: any; }
function f(entry: LinkDataItem | SankeyNode) { return entry; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, isRecord := recordParamMembersIn(ctx, parameter)
	if !isRecord {
		t.Fatalf("the sort-disagreeing union declined — members: %+v", members)
	}
	if len(members) != 1 || members[0].Key != "value" || members[0].Sort != BindingKindUnknown {
		t.Fatalf("members = %+v, want the one `value` leaf, unknown-sorted", members)
	}
	if members[0].MayBeAbsent {
		t.Errorf("value wears MayBeAbsent with no absent arm and both arms requiring it")
	}
}

func TestUnionMembersOf_TwoRecordArmsSharingAnAgreeingMemberSurvive(t *testing.T) {
	ClearResolvedRecordMembers()
	// the agreeing-sorts control, unchanged behavior: one member, its own
	// sort intact
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

// TestUnionMembersOf_GetValueShapedBodyServesBothSides is the corpus
// target end to end: `(entry && entry.value) || 0` over an
// undefined-only union COMPLETES, admits the member's value on a
// present call, and admits 0 on an undefined call — never a fold that
// drops the absent arm's fallback.
func TestUnionMembersOf_GetValueShapedBodyServesBothSides(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface LinkDataItem { source: number; target: number; value: number; }
function getValue(entry: LinkDataItem | undefined): number { return (entry && entry.value) || 0; }
`)
	declaration := entryEnvFunctionNamed(t, p, "getValue")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	if !ok {
		t.Fatalf("getValue declined: outcome=%q construct=%q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}
