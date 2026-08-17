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
		// the temp read crosses as the verbatim copy since the census
		// flip — the callee's entry takes the temp's whole state
		if arg.Kind == kernelbridge.LoopEffectVarState && arg.Index == innerTemp {
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
