// split from ir_summary_call_test.go — end to end, through the kernel

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── end to end, through the kernel ──────────────────────────────── */

// methodNamed is the method `text` of the first class declaration in a
// checker-backed program.
func methodNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.ClassLikeData().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			name := member.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return member
			}
		}
	}
	t.Fatalf("no method named %s", text)
	return nil
}

func TestSummaryCallStatement_AWrittenFieldExitRidesRetsBackIntoTheCallersSlotThroughTheKernel(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `bump` READS and WRITES this.count, so its own layout gives that
	// field an entry the census marks Written. A caller that calls it on
	// `this` fills the entry from its own this.count slot, and the exit
	// rides back into that same slot.
	p := entryEnvTestProgram(t,
		"class Counter {\n"+
			"  count: number = 0;\n"+
			"  bump(): number { this.count = this.count + 1; return this.count; }\n"+
			"  run(): number { this.bump(); return this.count; }\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	bump := methodNamed(t, p, "bump")
	// the callee resolves only through a REGISTERED contract — the same
	// row the production registry holds, which is what ContractOf reads
	bumpSymbol := p.Checker.GetSymbolAtLocation(bump.Name())
	if bumpSymbol == nil {
		t.Fatalf("bump's declaration has no symbol to register a contract under")
	}
	ctx.Contracts[bumpSymbol] = &FunctionContract{Declaration: bump}

	// the callee's OWN LAYOUT is what the call site reads — one row per
	// this-field, Written taken from the census's write set
	bundle := thisBundleOf(ctx, bump)
	if !bundle.Expanded {
		t.Fatalf("the callee's this bundle did not expand — its read fields are the entries")
	}
	countEntry, foundEntry := bundleRowOf(bundleRowsOf(bump, bundle), "this.count")
	if !foundEntry {
		t.Fatalf("the callee's layout gave this.count no bundle row")
	}
	if !countEntry.Written {
		t.Fatalf("the callee's this.count row is not Written although the body assigns it")
	}

	// the CALLER's slots, spelled as the layout spells a this bundle. The
	// site resolves them BY SPELLING, so this is the caller a lowered
	// `run` presents to the door.
	context := &LoweringContext{
		Bindings:      []string{"this.count", "#done", "#ret"},
		Sorts:         []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:       []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Narrow:        kernel.Narrow,
		Result:        &LoweringResult{Done: 1, Ret: 2},
		Flow:          ctx,
		SummaryTable:  &SummaryTableBuilder{},
		ResolveCallee: func(callee *ast.Node) *ast.Node { return bump },
	}
	call := callInMethodBody(t, p, "run")
	shape := LoweredSummary{
		SlotCount:     len(bundle.Entries) + 2,
		BundleEntries: bundleRowsOf(bump, bundle),
	}
	args := make([]kernelbridge.LoopEffect, shape.SlotCount)
	for index := range args {
		args[index] = kernelbridge.AbsentConst()
	}
	rets := make([]int, shape.SlotCount)
	for index := range rets {
		rets[index] = -1
	}
	if !bundleRetsAndArgs(context, call, shape, args, rets) {
		t.Fatalf("the threading declined at a `this.bump()` site")
	}
	if countEntry.Index >= len(args) {
		t.Fatalf("the call carries %d entries, too few for the callee's entry %d", len(args), countEntry.Index)
	}
	filled := args[countEntry.Index]
	if filled.Kind != kernelbridge.LoopEffectVarState || filled.Index != 0 {
		t.Errorf("the callee's this.count entry = %+v, want a whole-state copy of the caller's own this.count slot 0", filled)
	}
	if rets[countEntry.Index] != 0 {
		t.Errorf("rets[%d] = %d, want the caller's this.count slot 0 — the write must ride back",
			countEntry.Index, rets[countEntry.Index])
	}

	// and the DOOR: until the callee's own `this.x = e` statements lower
	// (the layout's other half), the site takes the opaque tier — which
	// must still havoc the receiver's bundle rather than leave the
	// caller's this.count standing across a call that writes it
	lowered, ok := SummaryCallOrHavoc(context, call, -1)
	if !ok {
		t.Fatalf("the call site declined whole")
	}
	if lowered[0].Kind == kernelbridge.IrStatementCall {
		// the callee lowers now: the door's own statement must carry the
		// same threading the direct call above produced
		if lowered[0].Args[countEntry.Index].Kind != kernelbridge.LoopEffectVarState ||
			lowered[0].Args[countEntry.Index].Index != 0 {
			t.Errorf("the door's call entry = %+v, want the caller's this.count slot 0, whole-state", lowered[0].Args[countEntry.Index])
		}
		if lowered[0].Rets[countEntry.Index] != 0 {
			t.Errorf("the door's rets[%d] = %d, want 0", countEntry.Index, lowered[0].Rets[countEntry.Index])
		}
		return
	}
	havocked := false
	for _, statement := range lowered {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 0 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			havocked = true
		}
	}
	if !havocked {
		t.Errorf("the opaque tier left this.count standing across a call that writes it: %+v", lowered)
	}
}

// bundleRowsOf is the bundle rows a method's this bundle contributes:
// one per read field, indexed after the declared parameters, Written
// taken from the census's own write set — the layout's own arithmetic,
// restated here only because the test reads the callee side directly.
func bundleRowsOf(method *ast.Node, bundle thisBundleLayout) []BundleEntry {
	rows := make([]BundleEntry, 0, len(bundle.Entries))
	for index, entry := range bundle.Entries {
		_, written := bundle.Written[entry.Name]
		rows = append(rows, BundleEntry{
			Path:    entry.Name,
			Index:   len(method.Parameters()) + index,
			Written: written,
		})
	}
	return rows
}

// bundleRowOf is one bundle row by its slot spelling.
func bundleRowOf(entries []BundleEntry, spelled string) (BundleEntry, bool) {
	for _, entry := range entries {
		if entry.Path == spelled {
			return entry, true
		}
	}
	return BundleEntry{}, false
}

// callInMethodBody is the first call expression in the named method's
// body — the site the door is asked to lower.
func callInMethodBody(t *testing.T, p *program.CheckerProgram, method string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(methodNamed(t, p, method).Body())
	if found == nil {
		t.Fatalf("no call expression in %s's body", method)
	}
	return found
}
