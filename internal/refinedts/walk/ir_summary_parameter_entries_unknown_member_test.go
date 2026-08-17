// pins for binding-pattern parameters whose annotation names an
// unknown-sorted member (a richer-typed leaf like `number[]`) — TASK 1
// of the parameter-entries fix wave
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestKernelSummaryDirect_ABindingPatternMixesAScalarAndAnUnknownSortedMember
// pins `{ a, b }: { a: number, b: number[] }` — `a` is an ordinary
// scalar member, `b` is unknown-sorted (a richer-typed leaf). Before the
// fix, an unknown-sorted bound member refused the WHOLE pattern; after,
// it binds as an unknown-sorted entry (BindingKindUnknown, TypeofTagNone)
// beside `a`'s scalar entry — reads of `b` answer nothing, which is
// exactly what is known, the same argument the rest-parameter arm
// already makes.
func TestKernelSummaryDirect_ABindingPatternMixesAScalarAndAnUnknownSortedMember(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f({ a, b }: { a: number, b: number[] }) { return a; }")
	lowered, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("declined (%q / %q)", outcome, construct)
	}
	if lowered.ParamCount != 2 {
		t.Errorf("ParamCount = %d, want 2 — one entry per bound name (a, b)", lowered.ParamCount)
	}
	entries, entriesOk := SummaryParameterEntries(declaration.Parameters()[0])
	if !entriesOk || len(entries) != 2 {
		t.Fatalf("entries = %v (ok %v), want two bound names", entries, entriesOk)
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	a, aOk := byName["a"]
	if !aOk || a.Key != "a" || a.Sort != BindingKindNumber {
		t.Errorf("entry a = %+v (ok %v), want key a, sort number", a, aOk)
	}
	b, bOk := byName["b"]
	if !bOk || b.Key != "b" || b.Sort != BindingKindUnknown || b.TypeofTag != TypeofTagNone {
		t.Errorf("entry b = %+v (ok %v), want key b, sort unknown, typeof none", b, bOk)
	}
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	t.Logf("outcome = %q, construct = %q", outcome, construct)
}
