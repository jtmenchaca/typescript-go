// pins for binding-pattern parameters whose annotation names a member
// that binds no depth-1 row of its own (a richer-typed leaf like
// `number[]`, which nestedMemberLeavesOf's array arm now expands to
// nested "len"/"elem" leaves) — TASK 1 of the parameter-entries fix wave
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestKernelSummaryDirect_ABindingPatternMixesAScalarAndATopEntryMember
// pins `{ a, b }: { a: number, b: number[] }` — `a` is an ordinary
// scalar member; `b`'s own annotation (`number[]`) now expands through
// nestedMemberLeavesOf's array arm to nested "len"/"elem" leaves (this
// wave), so `b` has no depth-1 row and binds the nestedRoots TOP entry
// (ir_summary_parameter_entries.go) instead of an unknown-sorted leaf
// entry keyed "b" — reads of `b` answer nothing, which is exactly what
// is known, beside `a`'s scalar entry, which keeps its precise binding.
func TestKernelSummaryDirect_ABindingPatternMixesAScalarAndATopEntryMember(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f({ a, b }: { a: number, b: number[] }) { return a; }")
	lowered, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
	if !bOk || b.Key != "" || b.Sort != BindingKindUnknown || b.TypeofTag != TypeofTagNone || !b.TopEntry {
		t.Errorf("entry b = %+v (ok %v), want no Key, sort unknown, typeof none, TopEntry true — b's member expanded to nested leaves with no depth-1 row", b, bOk)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("mixed scalar/TOP-entry pattern: outcome=%q construct=%q", outcome, construct)
	if construct == "a binding-pattern parameter" {
		t.Errorf("construct = %q — the pattern must bind its entries (a precisely, b as a TOP entry) rather than refuse", construct)
	}
}
