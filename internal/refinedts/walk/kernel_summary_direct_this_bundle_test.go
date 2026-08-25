// The widened summary route's `this` BUNDLE: a method's read/written
// `this.<field>` accesses lay out as their own leaf entries after the
// declared parameters (thisBundleOf), and the apply side fills them
// from the receiver's own field knowledge. See
// kernel_summary_direct_test.go's header for the sibling map; this
// file owns bundleMethodOf/bundleEntryNamed/thisEntryProbe.
//
// NOTE ON WHAT THESE PROBE. The layout below builds "this.a"-spelled
// entry slots, and the apply side fills them. What does NOT yet resolve
// is the body's own READ of `this.a`: both slot resolvers reject a
// ThisKeyword root — SpelledNameOf (tracked_bindings.go) requires an
// identifier receiver, and propertyPathOf (ir_object_slots.go) requires
// an identifier at the root of the chain. So a method body reading a
// this-field still declines its statement today, and the layout cases
// probe the layout function itself while the apply cases drive the
// entries through the summary the layout produced.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// bundleMethodOf parses a source whose first statement is a class and
// answers the method named `text`. The parent link a bundle reads the
// enclosing class through is what the parser sets, so the node comes
// from a real parse rather than being assembled.
func bundleMethodOf(t *testing.T, source string, text string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/m.ts", Path: "/m.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	for _, statement := range file.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.AsClassDeclaration().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			name := member.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return member
			}
		}
	}
	t.Fatalf("no method named %s in %q", text, source)
	return nil
}

// bundleEntryNamed is the BundleEntries row spelled under path.
func bundleEntryNamed(summary LoweredSummary, path string) (BundleEntry, bool) {
	for _, entry := range summary.BundleEntries {
		if entry.Path == path {
			return entry, true
		}
	}
	return BundleEntry{}, false
}

func TestKernelSummaryDirect_AMethodsReadThisFieldsBecomeEntriesAfterTheParameters(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// a, b are READ and become entries; c is declared and never mentioned,
	// so it takes no entry — only read fields expand
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			c: number;
			load(n: number): number { return this.a + this.b + n; }
		}
	`, "load")
	bundle := thisBundleOf(nil, declaration)
	if !bundle.Expanded {
		t.Fatalf("the bundle did not expand — a, b are read and every mention is a declared field")
	}
	if bundle.Escaped {
		t.Errorf("Escaped = true, want false — nothing carries the receiver out of sight")
	}
	if len(bundle.Entries) != 2 {
		t.Fatalf("entries = %+v, want two — this.a and this.b", bundle.Entries)
	}
	wantName := []string{"this.a", "this.b"}
	for index, entry := range bundle.Entries {
		if entry.Name != wantName[index] {
			t.Errorf("entry %d name = %q, want %q", index, entry.Name, wantName[index])
		}
		if entry.Sort != BindingKindNumber {
			t.Errorf("entry %d sort = %q, want number — the field's own annotation", index, entry.Sort)
		}
	}
	if len(bundle.Written) != 0 {
		t.Errorf("Written = %v, want none — the body only reads", bundle.Written)
	}
}

func TestKernelSummaryDirect_AWrittenThisFieldIsNamedInTheBundleRows(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// depth is read AND written (a compound write reads the old value), so
	// it carries an entry and the row names it written — that flag is what
	// the call sites map back out through rets
	declaration := bundleMethodOf(t, `
		class Holder {
			depth: number;
			label: number;
			load(): number { this.depth += 1; return this.label; }
		}
	`, "load")
	bundle := thisBundleOf(nil, declaration)
	if !bundle.Expanded {
		t.Fatalf("the bundle did not expand")
	}
	if _, written := bundle.Written["this.depth"]; !written {
		t.Errorf("this.depth is not named written — the body moves it")
	}
	if _, written := bundle.Written["this.label"]; written {
		t.Errorf("this.label is named written — the body only reads it")
	}
}

func TestKernelSummaryDirect_TheBundleRowsRideOutOnTheSummaryWithTheirSlotIndices(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// the body's own reads do not resolve yet (see the note above), so it
	// lowers by way of the havoc floor — but the LAYOUT still runs, and the
	// rows it built ride out on the summary, which is what the call sites
	// read to map written fields back
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b = 1; return n; }
		}
	`, "load")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("the body declined at %q", construct)
	}
	// one declared parameter, then BOTH this-fields: a is read, and the
	// write-only b takes a row too — the row is what its write-back
	// rides (the caller's own b slot kept a stale value across the call
	// when the write had no row; thisBundleOf's write-only-entry comment)
	if summary.ParamCount != 3 {
		t.Fatalf("ParamCount = %d, want 3 — the declared parameter plus the read AND the written this-entries", summary.ParamCount)
	}
	entry, has := bundleEntryNamed(summary, "this.a")
	if !has {
		t.Fatalf("no bundle row for this.a — BundleEntries = %+v", summary.BundleEntries)
	}
	if entry.Index != 1 {
		t.Errorf("this.a index = %d, want 1 — the this-entries lay out AFTER the declared parameters", entry.Index)
	}
	if entry.Written {
		t.Errorf("this.a Written = true, want false — only b is written")
	}
	written, hasWritten := bundleEntryNamed(summary, "this.b")
	if !hasWritten {
		t.Fatalf("no bundle row for the write-only this.b — BundleEntries = %+v", summary.BundleEntries)
	}
	if written.Index != 2 {
		t.Errorf("this.b index = %d, want 2 — after the read entry", written.Index)
	}
	if !written.Written {
		t.Errorf("this.b Written = false, want true — the body stores into it")
	}
}

func TestKernelSummaryDirect_AnEscapingReceiverDeclinesTheExpansionAndGoesPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// the receiver is handed to a callee the lowering cannot follow, so
	// its slots could move through a name the census never saw. The body
	// still lowers — the this-reads hit the opaque floor — and the outcome
	// is POROUS naming the escape.
	declaration := bundleMethodOf(t, `
		class Holder {
			depth: number;
			load(n: number): number { hand(this); return n; }
		}
	`, "load")
	// the layout refuses the EXPANSION, and says why
	bundle := thisBundleOf(nil, declaration)
	if bundle.Expanded {
		t.Errorf("an escaped bundle expanded — its slots may move through a name the census never saw")
	}
	if !bundle.Escaped {
		t.Fatalf("Escaped = false, want true — `hand(this)` carries the receiver out of sight")
	}
	if len(bundle.Entries) != 0 {
		t.Errorf("entries = %+v, want none", bundle.Entries)
	}
	// and the BODY still lowers, porous, naming the escape
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("an escaping receiver DECLINED the body at %q — the expansion declines, the body still lowers", construct)
	}
	if len(summary.BundleEntries) != 0 {
		t.Errorf("BundleEntries = %+v, want none — an escaped bundle does not expand", summary.BundleEntries)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want porous — the receiver left the lowering's sight", outcome)
	}
	if construct != "this escapes" {
		t.Errorf("construct = %q, want %q — the earliest reason the body stopped being read whole", construct, "this escapes")
	}
}

func TestKernelSummaryDirect_ANonMethodHasNoThisBundle(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n + 1; }")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		t.Fatalf("a plain function declined")
	}
	if len(summary.BundleEntries) != 0 {
		t.Errorf("BundleEntries = %+v, want none — only a method has a this bundle", summary.BundleEntries)
	}
	if summary.ParamCount != 1 {
		t.Errorf("ParamCount = %d, want 1", summary.ParamCount)
	}
}

/* ── the apply side fills the this-entries ───────────────────────── */

// thisEntryProbe lowers a method and answers the entry states one
// receiver produces for it — the apply side's own filling, end to end
// from a real declaration's layout.
func thisEntryProbe(
	t *testing.T,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) (LoweredSummary, []kernelbridge.KnownStateWire) {
	t.Helper()
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	summary, ok := RelowerSummaryBody(ctx, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("the body declined at %q", construct)
	}
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, receiver)
	if !statesOk {
		t.Fatalf("the entry states declined")
	}
	return summary, states
}

func TestKernelSummaryDirect_AReceiversFieldKnowledgeFillsTheThisEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	receiver := recordObject(t, map[string]float64{"a": 2, "b": 5}, []string{"a", "b"})
	summary, states := thisEntryProbe(t, declaration, []abstractdomain.AbstractValue{exactNumber(t, 9)}, receiver)
	if len(states) != summary.ParamCount {
		t.Fatalf("len(states) = %d, want ParamCount %d — one state per entry", len(states), summary.ParamCount)
	}
	// each this-entry took the receiver's own knowledge of that field
	for _, held := range []struct {
		path  string
		value float64
	}{{"this.a", 2}, {"this.b", 5}} {
		entry, has := bundleEntryNamed(summary, held.path)
		if !has {
			t.Errorf("no bundle row for %q", held.path)
			continue
		}
		state := states[entry.Index]
		if state.Top || state.Undef || state.Null {
			t.Errorf("%q entered top=%v undef=%v null=%v, want the receiver's own field state", held.path, state.Top, state.Undef, state.Null)
			continue
		}
		if !kernel.Member(state.Set, []float64{held.value}) {
			t.Errorf("%q entered a set excluding the receiver's %v: %+v", held.path, held.value, state.Set)
		}
	}
}

func TestKernelSummaryDirect_AReceiverWithoutTheFieldFillsThatEntryTop(t *testing.T) {
	SetEngineKernel(kernelDelegationLoadKernel(t))
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	// b is not among the receiver's keys: that entry fills TOP, never
	// absent — an unknown field is unknown, and absent would claim the
	// field is undefined
	receiver := recordObject(t, map[string]float64{"a": 2}, []string{"a"})
	summary, states := thisEntryProbe(t, declaration, []abstractdomain.AbstractValue{exactNumber(t, 9)}, receiver)
	known, hasKnown := bundleEntryNamed(summary, "this.a")
	missing, hasMissing := bundleEntryNamed(summary, "this.b")
	if !hasKnown || !hasMissing {
		t.Fatalf("BundleEntries = %+v, want rows for this.a and this.b", summary.BundleEntries)
	}
	if states[known.Index].Top {
		t.Errorf("this.a entered TOP — the receiver names it")
	}
	if !states[missing.Index].Top {
		t.Errorf("this.b entered top=false, want TOP — the receiver does not name it")
	}
	if states[missing.Index].Undef || states[missing.Index].Null {
		t.Errorf("this.b entered ABSENT — absent would claim the field is undefined, which no receiver said")
	}
}

func TestKernelSummaryDirect_ANonObjectReceiverFillsEveryThisEntryTop(t *testing.T) {
	SetEngineKernel(kernelDelegationLoadKernel(t))
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	// silence names no fields at all; every this-entry fills TOP, which
	// the entry quantifier already covers — sound, less precise
	summary, states := thisEntryProbe(t,
		declaration, []abstractdomain.AbstractValue{exactNumber(t, 1)}, unknownReceiver())
	for _, entry := range summary.BundleEntries {
		if !states[entry.Index].Top {
			t.Errorf("%q entered top=false on an unknown receiver, want TOP", entry.Path)
		}
		if states[entry.Index].Undef || states[entry.Index].Null {
			t.Errorf("%q entered ABSENT on an unknown receiver, want TOP", entry.Path)
		}
	}
	// the DECLARED parameter is untouched by the receiver rule — it still
	// takes its own argument's knowledge
	if states[0].Top {
		t.Errorf("the declared parameter entered TOP — the receiver rule reached past the this-entries")
	}
}
