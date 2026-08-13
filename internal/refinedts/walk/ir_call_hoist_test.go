// Call hoisting: a call inside an EXPRESSION lowers to a temp-slot call
// statement emitted BEFORE the statement that held it, and the expression
// reads the temp. The gates — a statement stream must exist, and the
// reordering must be observable by nothing — are what these pin.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// hoistParse is the statement list of a throwaway source, the same parse
// every lowering test in this directory uses.
func hoistParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/hoist.ts", Path: "/hoist.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

// hoistProgram is a checker-backed program whose functions the registry
// can compile summaries for — the recipe kernel_summary_direct_test.go
// and ir_summary_call_test.go both use.
func hoistProgram(t *testing.T, source string) *program.CheckerProgram {
	t.Helper()
	return entryEnvTestProgram(t, source)
}

// hoistFunctionNamed is the top-level function declaration of that name.
func hoistFunctionNamed(t *testing.T, p *program.CheckerProgram, name string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		declarationName := statement.Name()
		if declarationName != nil && ast.IsIdentifier(declarationName) && declarationName.Text() == name {
			return statement
		}
	}
	t.Fatalf("no function named %s", name)
	return nil
}

// hoistContext is the caller layout the hoist tests share: whatever
// scalars the case names, a done/ret pair, a table, a flow context, and an
// Allocate that grows the context's own vectors (the summary lowering's
// own allocate, restated so the temp slots are readable at the indices
// they were handed).
func hoistContext(
	kernel *kernelbridge.RefinedTSKernel,
	bindings []string,
	sorts []BindingKind,
	ctx *FlowContext,
	resolve func(callee *ast.Node) *ast.Node,
) *LoweringContext {
	typeofs := make([]TypeofTag, len(bindings))
	for index, sort := range sorts {
		switch sort {
		case BindingKindNumber:
			typeofs[index] = TypeofTagNumber
		case BindingKindString:
			typeofs[index] = TypeofTagString
		default:
			typeofs[index] = TypeofTagNone
		}
	}
	context := &LoweringContext{
		Bindings:      bindings,
		Sorts:         sorts,
		Typeofs:       typeofs,
		Flow:          ctx,
		SummaryTable:  &SummaryTableBuilder{},
		ResolveCallee: resolve,
		Inlining:      map[*ast.Node]struct{}{},
	}
	if kernel != nil {
		context.Narrow = kernel.Narrow
	}
	context.Allocate = func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, typeofTag)
		return len(context.Bindings) - 1, true
	}
	return context
}

// callStatementsOf counts the call statements in a lowered list.
func callStatementsOf(statements []kernelbridge.IrStatement) []kernelbridge.IrStatement {
	var out []kernelbridge.IrStatement
	for _, statement := range statements {
		if statement.Kind == kernelbridge.IrStatementCall {
			out = append(out, statement)
		}
	}
	return out
}

/* ── the gates ───────────────────────────────────────────────────── */

func TestCallHoist_WithoutAStatementStreamNothingHoists(t *testing.T) {
	// CanHoist false is the loop solver's world: FoldBody reads the same
	// effect grammar into an effect-per-binding vector with no statement
	// stream, so a hoisted statement there would vanish
	context := hoistContext(nil, []string{"x"}, []BindingKind{BindingKindNumber}, nil, nil)
	context.CanHoist = false
	statements := hoistParse(t, `x = f(1);`)
	call := callOfStatement(t, statements[0])
	if _, ok := HoistCallEffect(context, call); ok {
		t.Errorf("a call hoisted with no statement stream to hoist into")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) = %d after a refused hoist, want 0", len(context.Hoisted))
	}
}

func TestCallHoist_WithoutAResolvableBlobNothingHoists(t *testing.T) {
	// only a blob-backed callee has a call statement to build; an opaque
	// callee's answer is the statement floor's havoc, which the statement
	// route reaches on its own
	context := hoistContext(nil, []string{"x"}, []BindingKind{BindingKindNumber},
		&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, nil)
	context.CanHoist = true
	statements := hoistParse(t, `x = fetch("nope");`)
	context.HoistStatement = statements[0]
	if _, ok := HoistCallEffect(context, callOfStatement(t, statements[0])); ok {
		t.Errorf("a call with no resolvable blob hoisted")
	}
}

func TestCallHoist_ANonCallNeverHoists(t *testing.T) {
	context := hoistContext(nil, []string{"x"}, []BindingKind{BindingKindNumber},
		&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, nil)
	context.CanHoist = true
	statements := hoistParse(t, `x = y + 1;`)
	context.HoistStatement = statements[0]
	rhs := Unwrapped(statements[0].AsExpressionStatement().Expression.AsBinaryExpression().Right)
	if _, ok := HoistCallEffect(context, rhs); ok {
		t.Errorf("a non-call expression hoisted")
	}
}

/* ── the accumulation's bookkeeping ──────────────────────────────── */

func TestCallHoist_TakeEmptiesTheAccumulationAndDropTruncatesToTheMark(t *testing.T) {
	context := &LoweringContext{}
	if taken := TakeHoisted(context); taken != nil {
		t.Errorf("TakeHoisted on an empty accumulation = %v, want nil", taken)
	}
	if mark := HoistedMark(context); mark != 0 {
		t.Errorf("HoistedMark on an empty accumulation = %d, want 0", mark)
	}
	context.Hoisted = []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementCall, Callee: 0},
		{Kind: kernelbridge.IrStatementCall, Callee: 1},
	}
	mark := HoistedMark(context)
	if mark != 2 {
		t.Fatalf("HoistedMark = %d, want 2", mark)
	}
	context.Hoisted = append(context.Hoisted, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementCall, Callee: 2})
	DropHoistedFrom(context, mark)
	if len(context.Hoisted) != 2 {
		t.Errorf("len(Hoisted) after the drop = %d, want 2 — only what came after the mark goes", len(context.Hoisted))
	}
	taken := TakeHoisted(context)
	if len(taken) != 2 {
		t.Errorf("len(taken) = %d, want 2", len(taken))
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the take = %d, want 0 — the take empties", len(context.Hoisted))
	}
	// an out-of-range mark leaves the accumulation alone rather than
	// panicking on a slice bound
	context.Hoisted = []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementCall}}
	DropHoistedFrom(context, 9)
	DropHoistedFrom(context, -1)
	if len(context.Hoisted) != 1 {
		t.Errorf("len(Hoisted) after out-of-range drops = %d, want 1", len(context.Hoisted))
	}
	DropHoistedFrom(nil, 0)
}

/* ── the hoist itself ────────────────────────────────────────────── */

// hoistCalleeProgram gives a resolvable, blob-backed `g` and a caller
// body that mentions it — the shared setup for the cases below.
func hoistCalleeProgram(t *testing.T, source string) (*FlowContext, *program.CheckerProgram, *ast.Node) {
	t.Helper()
	p := hoistProgram(t, source)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	g := hoistFunctionNamed(t, p, "g")
	symbol := p.Checker.GetSymbolAtLocation(g.Name())
	if symbol == nil {
		t.Fatalf("g has no symbol to register a contract under")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: g}
	return ctx, p, g
}

func TestCallHoist_ANestedCallPairHoistsInnerThenOuterThenTheAssignmentReadsTheTemp(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { let x = g(g(y)) + 1; return x; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body; the hoist route has no blob-backed callee to lower")
	}
	context := hoistContext(kernel,
		[]string{"x", "y", "#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		ctx,
		func(callee *ast.Node) *ast.Node { return g },
	)
	context.Result = &LoweringResult{Done: 2, Ret: 3}
	lowered, ok := LowerStatements(context, hoistParse(t, `x = g(g(y)) + 1;`))
	if !ok {
		t.Fatalf("the statement declined whole")
	}
	calls := callStatementsOf(lowered)
	if len(calls) != 2 {
		t.Fatalf("call statements = %d, want 2 — the inner g and the outer g each hoist: %+v", len(calls), lowered)
	}
	// the two call statements come FIRST, in the order the reader met them
	// (inner before outer — the outer's argument is the inner's temp), and
	// the assignment that reads the outer temp comes last
	if lowered[0].Kind != kernelbridge.IrStatementCall || lowered[1].Kind != kernelbridge.IrStatementCall {
		t.Errorf("lowered[0..1] = %+v %+v, want the two hoisted call statements first", lowered[0], lowered[1])
	}
	last := lowered[len(lowered)-1]
	if last.Kind != kernelbridge.IrStatementAssign || last.Target != 0 {
		t.Errorf("the last statement = %+v, want the assignment into x's slot 0", last)
	}
	// the OUTER call's argument reads the INNER call's temp: the temps are
	// the slots allocated past the caller's own four
	innerTemp := lowered[0].Rets[len(lowered[0].Rets)-1]
	if innerTemp < 4 {
		t.Errorf("the inner call's ret target = %d, want a fresh temp past the caller's own slots", innerTemp)
	}
	foundInnerRead := false
	for _, arg := range lowered[1].Args {
		if arg.Kind == kernelbridge.LoopEffectVar && arg.Index == innerTemp {
			foundInnerRead = true
		}
	}
	if !foundInnerRead {
		t.Errorf("the outer call's args = %+v, want one reading the inner call's temp %d", lowered[1].Args, innerTemp)
	}
	// nothing is left in the accumulation once the statement is lowered
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the statement = %d, want 0 — the flush empties it", len(context.Hoisted))
	}
}

func TestCallHoist_AnAwaitWrappedCallHoistsIdentically(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	// the ret-as-inner convention: an async callee's ret slot already holds
	// the SETTLED value, so `await g(y)` carries exactly the slot `g(y)`
	// carries and the await peels off
	ctx, _, g := hoistCalleeProgram(t,
		"async function g(n: number): Promise<number> { return n + 1; }\n"+
			"async function caller(y: number): Promise<number> { let x = (await g(y)) + 1; return x; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body; there is no blob-backed callee to hoist")
	}
	context := hoistContext(kernel,
		[]string{"x", "y", "#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		ctx,
		func(callee *ast.Node) *ast.Node { return g },
	)
	context.Result = &LoweringResult{Done: 2, Ret: 3}
	lowered, ok := LowerStatements(context, hoistParse(t, `x = (await g(y)) + 1;`))
	if !ok {
		t.Fatalf("the statement declined whole")
	}
	calls := callStatementsOf(lowered)
	if len(calls) != 1 {
		t.Fatalf("call statements = %d, want 1 — the awaited call hoists: %+v", len(calls), lowered)
	}
	last := lowered[len(lowered)-1]
	if last.Kind != kernelbridge.IrStatementAssign || last.Target != 0 {
		t.Errorf("the last statement = %+v, want the assignment into x's slot 0", last)
	}
}

/* ── the ordering gate ───────────────────────────────────────────── */

func TestCallHoist_TheOrderingGateRefusesACallWritingASlotTheStatementAlsoReads(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedRecordMembers()
	// `this.bump()` WRITES this.count. A statement that also READS
	// this.count around the call — `total = this.count + this.bump()` —
	// would read the POST-call value if the call were hoisted, where the
	// real run reads the pre-call one. The gate refuses.
	p := hoistProgram(t,
		"class Counter {\n"+
			"  count: number = 0;\n"+
			"  total: number = 0;\n"+
			"  bump(): number { this.count = this.count + 1; return this.count; }\n"+
			"  run(): void { this.total = this.count + this.bump(); }\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	bump := methodNamed(t, p, "bump")
	symbol := p.Checker.GetSymbolAtLocation(bump.Name())
	if symbol == nil {
		t.Fatalf("bump has no symbol to register a contract under")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: bump}
	shape, known := LowerSummaryBody(ctx, bump)
	if !known {
		t.Skipf("bump's body did not lower; there is no written bundle row for the gate to read")
	}
	writtenCount := false
	for _, entry := range shape.BundleEntries {
		if entry.Path == "this.count" && entry.Written {
			writtenCount = true
		}
	}
	if !writtenCount {
		t.Skipf("bump's layout has no WRITTEN this.count row; the gate has nothing to refuse on")
	}
	context := hoistContext(kernel,
		[]string{"this.count", "this.total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber},
		ctx,
		func(callee *ast.Node) *ast.Node { return bump },
	)
	context.CanHoist = true
	statements := hoistParse(t, `this.total = this.count + this.bump();`)
	context.HoistStatement = statements[0]
	call := hoistCallInside(t, statements[0])
	if _, ok := HoistCallEffect(context, call); ok {
		t.Errorf("the gate admitted a hoist of a call that writes this.count, which the statement also reads")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after a refused hoist = %d, want 0", len(context.Hoisted))
	}
	// and a caller that carries NO slot for the written field has nothing
	// for the reordering to falsify: the write set is empty, so the gate
	// admits. This is the other side of the same rule — the gate turns on
	// whether the caller holds knowledge the call could invalidate, not on
	// whether the callee writes at all.
	unheld := hoistContext(kernel,
		[]string{"other"},
		[]BindingKind{BindingKindNumber},
		ctx,
		func(callee *ast.Node) *ast.Node { return bump },
	)
	unheld.CanHoist = true
	unheldStatements := hoistParse(t, `other = other + this.bump();`)
	unheld.HoistStatement = unheldStatements[0]
	if !hoistingIsOrderSafe(unheld, hoistCallInside(t, unheldStatements[0]), bump) {
		t.Errorf("the gate refused a hoist whose write set the caller holds no slot for")
	}
}

func TestCallHoist_WithNoStatementNamedTheGateRefuses(t *testing.T) {
	// a reordering cannot be proved safe against a statement nobody named
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return g(y) + 1; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	context := hoistContext(kernel, []string{"x", "y"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	context.CanHoist = true
	context.HoistStatement = nil
	statements := hoistParse(t, `x = g(y) + 1;`)
	if _, ok := HoistCallEffect(context, hoistCallInside(t, statements[0])); ok {
		t.Errorf("the gate admitted a hoist with no statement to be ordered against")
	}
}

// hoistCallInside is the first call expression anywhere inside a
// statement — what the gate tests hand to the seam directly.
func hoistCallInside(t *testing.T, statement *ast.Node) *ast.Node {
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
	visit(statement)
	if found == nil {
		t.Fatalf("no call expression in the statement")
	}
	return found
}

/* ── loop bodies are unchanged ───────────────────────────────────── */

func TestCallHoist_ALoopBodyIsUnchangedByHoisting(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return y; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	context := hoistContext(kernel, []string{"i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	// a loop whose BODY holds a call: the fold has no statement stream, so
	// the body must not lower through the fold — it falls to the havoc
	// floor exactly as it did before hoisting existed, and no call
	// statement is emitted
	lowered, ok := LowerStatements(context, hoistParse(t, `while (i < 10) { x = g(i) + 1; i = i + 1; }`))
	if !ok {
		t.Fatalf("the loop declined whole")
	}
	if calls := callStatementsOf(lowered); len(calls) != 0 {
		t.Errorf("call statements inside a loop lowering = %d, want 0 — the fold has no statement stream: %+v",
			len(calls), lowered)
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the loop = %d, want 0", len(context.Hoisted))
	}
	// the flag is restored on the way out, so a later caller's own
	// bookkeeping is undisturbed
	if context.CanHoist {
		t.Errorf("CanHoist = true after LowerStatements returned, want the caller's own value restored")
	}
}

/* ── a declined statement leaves no orphan temps ─────────────────── */

func TestCallHoist_ADeclinedStatementLeavesNoCallStatementBehind(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return y; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	// the RHS holds a hoistable call AND a shape no reading lowers — a
	// computed member on an untracked receiver. The statement falls to the
	// havoc floor, and the floor's contract is assignments of unknown and
	// NOTHING else: no call statement may sit ahead of it.
	context := hoistContext(kernel, []string{"x", "y"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	lowered, ok := LowerStatements(context, hoistParse(t, `x = g(y) + obj[key].deep;`))
	if !ok {
		t.Fatalf("the statement declined whole rather than reaching the havoc floor")
	}
	for _, statement := range lowered {
		if statement.Kind == kernelbridge.IrStatementCall {
			t.Errorf("a declined statement leaked a hoisted call statement into the havoc floor's output: %+v", lowered)
		}
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the declined statement = %d, want 0 — the drop truncates", len(context.Hoisted))
	}
}

/* ── end to end, through the kernel ──────────────────────────────── */

func TestCallHoist_AHoistedCallBodyWalksThroughTheKernel(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { let x = g(y) + 1; return x; }\n")
	blob, has := SummaryBlobFor(ctx, g)
	if !has {
		t.Skipf("the registry declined g's body; there is no blob to splice")
	}
	context := hoistContext(kernel,
		[]string{"x", "y", "#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		ctx,
		func(callee *ast.Node) *ast.Node { return g },
	)
	context.Result = &LoweringResult{Done: 2, Ret: 3}
	lowered, ok := LowerStatements(context, hoistParse(t, `x = g(y) + 1;`))
	if !ok {
		t.Fatalf("the statement declined whole")
	}
	if len(callStatementsOf(lowered)) != 1 {
		t.Skipf("the site did not take the hoist route: %+v", lowered)
	}
	// the whole body walks kernel-side with the callee's blob in the table:
	// y in {5} means g(y) is {6} and x is {7}
	entry := make([]kernelbridge.KnownStateWire, len(context.Bindings))
	for index := range entry {
		entry[index] = kernelbridge.KnownStateWire{Top: true}
	}
	entry[1] = kernelbridge.KnownStateWire{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))}
	// the callee's blob rides as the walk's TABLE — what the hoisted call
	// statement's Callee field indexes
	exit := kernel.Walk(entry, lowered, context.SummaryTable.Blobs...)
	_ = blob
	if len(exit) <= 0 {
		t.Fatalf("the walk answered no states")
	}
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true after a hoisted-call body walk, want the kernel's own answer")
	}
	set := loweringSetOf(t, x)
	if !kernel.Member(set, []float64{7}) {
		t.Errorf("member(x, [7]) = false, want true — g(5) is 6 and x is 6 + 1")
	}
	if kernel.Member(set, []float64{6}) {
		t.Errorf("member(x, [6]) = true, want false")
	}
}
