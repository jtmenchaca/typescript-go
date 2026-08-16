// Companion to loop_unroll.go and callback_outcome.go's
// forEachExactFold: a literal-bounded loop's running total and loop
// count determine exactly — the fixpoint's widened invariant never
// runs where the trip count or the iterated elements are exactly
// known. The stubs below drive the orchestration (step order, the
// one reporting pass under joins, the exact commit) without a
// checker; the fixture files under
// tsc-vscode/fixtures/language/syntax-coverage/ pin the end-to-end
// verdicts.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// foldTestContext is a bare walk context: no checker (every reader on
// the exercised paths is nil-checker safe for exactly known values),
// empty contracts, fresh alias classes, a swallowed report sink.
func foldTestContext() *FlowContext {
	return &FlowContext{
		P:         &program.CheckerProgram{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) {},
	}
}

// singleValue answers a knowledge state's one exact value, false when
// it is anything but one exact number.
func singleValue(v abstractdomain.AbstractValue, ok bool) (float64, bool) {
	if !ok || v.Kind != abstractdomain.KindValues || len(v.Values) != 1 {
		return 0, false
	}
	return v.Values[0], true
}

// accumulationRecorder is the stub statement walk for a body of the
// shape `NAME_ACC = NAME_ACC + NAME_STEP`: when the step binding
// holds ONE exact value it performs the step (a silent per-trip
// pass); when it holds a join it only records (the one reporting
// pass).
type accumulationRecorder struct {
	acc     string
	step    string
	unit    bool      // the body adds 1 per trip (`acc = acc + 1`), not the step value
	steps   []float64 // the step values seen, in order
	reports int       // walks where the step binding held a join
}

func (r *accumulationRecorder) analyze(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	held, hasStep := env.Get(r.step)
	if value, exact := singleValue(held, hasStep); exact {
		r.steps = append(r.steps, value)
		added := value
		if r.unit {
			added = 1
		}
		if total, ok := singleValue(env.Get(r.acc)); ok {
			env.Set(r.acc, abstractdomain.KnownValues([]float64{total + added}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
		}
		return false
	}
	r.reports++
	return false
}

/* ── LiteralTripCount: the assignment-spelled unit steps ─────────── */

func TestLiteralBoundedFolds_TripCountReadsAssignmentSteps(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		count   int
		counted bool
	}{
		{"i = i + 1", "for (let i = 0; i < 2; i = i + 1) { }", 2, true},
		{"i += 1", "for (let i = 0; i < 2; i += 1) { }", 2, true},
		{"i = 1 + i", "for (let i = 0; i < 2; i = 1 + i) { }", 2, true},
		{"inclusive bound", "for (let i = 0; i <= 2; i = i + 1) { }", 3, true},
		{"postfix ++ still reads", "for (let i = 0; i < 2; i++) { }", 2, true},
		{"a two step is not a unit step", "for (let i = 0; i < 2; i = i + 2) { }", 0, false},
		{"a foreign name is not the index", "for (let i = 0; i < 2; i = j + 1) { }", 0, false},
		{"a break unpins the count", "for (let i = 0; i < 2; i = i + 1) { break; }", 0, false},
		{"a body write to the index unpins it", "for (let i = 0; i < 2; i = i + 1) { i = 5; }", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loop := kernelDelegationStatementOf(t, c.source)
			count, counted := LiteralTripCount(loop)
			if counted != c.counted || count != c.count {
				t.Errorf("LiteralTripCount(%q) = (%d, %v), want (%d, %v)", c.source, count, counted, c.count, c.counted)
			}
		})
	}
}

/* ── the counted for: exact stepping, one reporting pass ─────────── */

func TestLiteralBoundedFolds_ForLoopRunningTotalEndsExact(t *testing.T) {
	ctx := foldTestContext()
	loop := kernelDelegationStatementOf(t, "for (let i = 0; i < 2; i = i + 1) { age = age + 1; }")
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	recorder := &accumulationRecorder{acc: "age", step: "i", unit: true}
	handled := UnrollLiteralBoundedLoop(ctx, env, loop, nil, LoopAnalyzers{
		AnalyzeStatement: recorder.analyze,
		EvaluateExpression: func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
			return silence.Residue()
		},
	}, nil)
	if !handled {
		t.Fatalf("UnrollLiteralBoundedLoop handled = false, want true")
	}
	if total, ok := singleValue(env.Get("age")); !ok || total != 2 {
		t.Errorf("env[age] after the unrolled loop = %+v, want exactly 2", mustGet(t, env, "age"))
	}
	if len(recorder.steps) != 2 || recorder.steps[0] != 0 || recorder.steps[1] != 1 {
		t.Errorf("silent step entries saw i = %v, want [0 1]", recorder.steps)
	}
	if recorder.reports != 1 {
		t.Errorf("reporting passes = %d, want exactly 1 (under the joined entries)", recorder.reports)
	}
	// the head index never entered the walked environment, so the
	// commit must not add it
	if _, held := env.Get("i"); held {
		t.Errorf("env[i] present after the loop, want absent (the head binding is loop-scoped)")
	}
}

func TestLiteralBoundedFolds_ZeroTripForLoopRunsAndReportsNothing(t *testing.T) {
	ctx := foldTestContext()
	loop := kernelDelegationStatementOf(t, "for (let i = 0; i < 0; i = i + 1) { age = age + 1; }")
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	recorder := &accumulationRecorder{acc: "age", step: "i", unit: true}
	handled := UnrollLiteralBoundedLoop(ctx, env, loop, nil, LoopAnalyzers{
		AnalyzeStatement: recorder.analyze,
		EvaluateExpression: func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
			return silence.Residue()
		},
	}, nil)
	if !handled {
		t.Fatalf("UnrollLiteralBoundedLoop handled = false, want true")
	}
	if total, ok := singleValue(env.Get("age")); !ok || total != 0 {
		t.Errorf("env[age] after a zero-trip loop = %+v, want exactly 0", mustGet(t, env, "age"))
	}
	if len(recorder.steps) != 0 || recorder.reports != 0 {
		t.Errorf("a zero-trip body walked (%d steps, %d reports), want none", len(recorder.steps), recorder.reports)
	}
}

/* ── the exact-sequence for-of ───────────────────────────────────── */

func forOfArrayEvaluator(elements []float64) func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	return func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
		if ast.IsArrayLiteralExpression(e) {
			return abstractdomain.KnownValues(elements, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
		}
		return silence.Residue()
	}
}

func TestLiteralBoundedFolds_ForOfSumOfLiteralElementsEndsExact(t *testing.T) {
	ctx := foldTestContext()
	loop := kernelDelegationStatementOf(t, "for (const age of [10, 20, 30]) { sum = sum + age; }")
	env := NewEnv()
	env.Set("sum", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	recorder := &accumulationRecorder{acc: "sum", step: "age"}
	handled := UnrollLiteralBoundedLoop(ctx, env, loop, nil, LoopAnalyzers{
		AnalyzeStatement:   recorder.analyze,
		EvaluateExpression: forOfArrayEvaluator([]float64{10, 20, 30}),
	}, nil)
	if !handled {
		t.Fatalf("UnrollLiteralBoundedLoop handled = false, want true")
	}
	if total, ok := singleValue(env.Get("sum")); !ok || total != 60 {
		t.Errorf("env[sum] after the unrolled for-of = %+v, want exactly 60", mustGet(t, env, "sum"))
	}
	if len(recorder.steps) != 3 || recorder.steps[0] != 10 || recorder.steps[1] != 20 || recorder.steps[2] != 30 {
		t.Errorf("silent step entries saw age = %v, want [10 20 30]", recorder.steps)
	}
	if recorder.reports != 1 {
		t.Errorf("reporting passes = %d, want exactly 1 (element joined)", recorder.reports)
	}
	if _, held := env.Get("age"); held {
		t.Errorf("env[age] present after the loop, want absent (the element binding is loop-scoped)")
	}
}

func TestLiteralBoundedFolds_ForOfDeclinesWhenBodyWritesTheIterable(t *testing.T) {
	ctx := foldTestContext()
	loop := kernelDelegationStatementOf(t, "for (const age of xs) { xs = []; }")
	env := NewEnv()
	env.Set("xs", abstractdomain.KnownValues([]float64{10, 20}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
	handled := UnrollLiteralBoundedLoop(ctx, env, loop, nil, LoopAnalyzers{
		AnalyzeStatement: func(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
			return false
		},
		EvaluateExpression: func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
			if ast.IsIdentifier(e) && e.Text() == "xs" {
				return abstractdomain.KnownValues([]float64{10, 20}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
			}
			return silence.Residue()
		},
	}, nil)
	if handled {
		t.Errorf("UnrollLiteralBoundedLoop handled a body that rewrites its iterable, want a decline")
	}
}

func TestLiteralBoundedFolds_ForOfDeclinesWhenBodyBreaks(t *testing.T) {
	ctx := foldTestContext()
	loop := kernelDelegationStatementOf(t, "for (const age of [10, 20]) { break; }")
	env := NewEnv()
	handled := UnrollLiteralBoundedLoop(ctx, env, loop, nil, LoopAnalyzers{
		AnalyzeStatement: func(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
			return false
		},
		EvaluateExpression: forOfArrayEvaluator([]float64{10, 20}),
	}, nil)
	if handled {
		t.Errorf("UnrollLiteralBoundedLoop handled a body that breaks, want a decline (the trip count no longer runs)")
	}
}

/* ── the forEach exact fold ──────────────────────────────────────── */

// forEachWalkOf builds the CallbackWalk forEachExactFold reads, over a
// parsed `RECEIVER.forEach(ARROW)` statement, with the accumulation
// recorder as the body walk.
func forEachWalkOf(t *testing.T, source string, receiver abstractdomain.AbstractValue, env Env, recorder *accumulationRecorder) (*CallbackWalk, *ast.CallExpression) {
	t.Helper()
	// a REAL program: the fold's parameter binding seeds through
	// SeededBinding, which asks the checker for the parameter's type —
	// a nil checker is not on this path's menu
	p := entryEnvTestProgram(t,
		"declare let sum: number;\ndeclare let ages: Map<number, number>;\n"+source)
	var callNode *ast.Node
	for _, statement := range p.Entry.Statements.Nodes {
		if ast.IsExpressionStatement(statement) &&
			ast.IsCallExpression(statement.AsExpressionStatement().Expression) {
			callNode = statement.AsExpressionStatement().Expression
			break
		}
	}
	if callNode == nil {
		t.Fatalf("no call statement in %q", source)
	}
	call := callNode.AsCallExpression()
	if len(call.Arguments.Nodes) == 0 {
		t.Fatalf("call in %q has no arguments", source)
	}
	arrow := call.Arguments.Nodes[0]
	ctx := foldTestContext()
	ctx.P = p
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}
	parameters := arrow.Parameters()
	parameterAt := func(index int) *ast.Node {
		if index < 0 || index >= len(parameters) {
			return nil
		}
		return parameters[index]
	}
	analyze := func(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
		return false
	}
	if recorder != nil {
		analyze = recorder.analyze
	}
	walk := &CallbackWalk{
		Ctx:      ctx,
		Env:      env,
		Receiver: receiver,
		Method:   "forEach",
		Call:     callNode,
		Arrow:    arrow,
		Analyzers: LoopAnalyzers{
			AnalyzeStatement: analyze,
			EvaluateExpression: func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
				return silence.Residue()
			},
		},
		Body:             arrow.Body(),
		Silent:           &silent,
		PreboundBindings: map[string]abstractdomain.AbstractValue{},
		ParameterAt:      parameterAt,
	}
	return walk, call
}

func TestLiteralBoundedFolds_ForEachArrayAccumulationEndsExact(t *testing.T) {
	env := NewEnv()
	env.Set("sum", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	recorder := &accumulationRecorder{acc: "sum", step: "age"}
	receiver := abstractdomain.KnownValues([]float64{40, 41}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	walk, call := forEachWalkOf(t, "[40, 41].forEach((age) => { sum = sum + age; });", receiver, env, recorder)
	_, handled := forEachExactFold(walk, call)
	if !handled {
		t.Fatalf("forEachExactFold handled = false, want true")
	}
	if total, ok := singleValue(env.Get("sum")); !ok || total != 81 {
		t.Errorf("env[sum] after the exact fold = %+v, want exactly 81", mustGet(t, env, "sum"))
	}
	if len(recorder.steps) != 2 || recorder.steps[0] != 40 || recorder.steps[1] != 41 {
		t.Errorf("silent step entries saw age = %v, want [40 41]", recorder.steps)
	}
	if recorder.reports != 1 {
		t.Errorf("reporting passes = %d, want exactly 1 (element joined)", recorder.reports)
	}
	// the parameter binding never leaks into the outer environment
	if _, held := env.Get("age"); held {
		t.Errorf("env[age] present after the fold, want absent (a callback parameter)")
	}
}

func TestLiteralBoundedFolds_ForEachMapValuesAccumulationEndsExact(t *testing.T) {
	env := NewEnv()
	env.Set("sum", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	recorder := &accumulationRecorder{acc: "sum", step: "age"}
	receiver := abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorMap,
		Complete:         true,
		Entries: []abstractdomain.CollectionEntry{
			{
				Key:   abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
				Value: abstractdomain.KnownValues([]float64{100}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
			},
			{
				Key:   abstractdomain.KnownValues([]float64{2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
				Value: abstractdomain.KnownValues([]float64{101}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
			},
		},
	}
	walk, call := forEachWalkOf(t, "ages.forEach((age) => { sum = sum + age; });", receiver, env, recorder)
	_, handled := forEachExactFold(walk, call)
	if !handled {
		t.Fatalf("forEachExactFold handled = false, want true")
	}
	if total, ok := singleValue(env.Get("sum")); !ok || total != 201 {
		t.Errorf("env[sum] after the exact Map fold = %+v, want exactly 201", mustGet(t, env, "sum"))
	}
	if len(recorder.steps) != 2 || recorder.steps[0] != 100 || recorder.steps[1] != 101 {
		t.Errorf("silent step entries saw the Map values %v, want [100 101]", recorder.steps)
	}
}

func TestLiteralBoundedFolds_ForEachDeclinesOnOwnerWrite(t *testing.T) {
	env := NewEnv()
	receiver := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	walk, call := forEachWalkOf(t, "[1, 2].forEach((v, i, arr) => { arr.push(v); });", receiver, env, nil)
	walk.OwnerParameter = "arr"
	walk.HasOwnerParameter = true
	if _, handled := forEachExactFold(walk, call); handled {
		t.Errorf("forEachExactFold handled a body writing through the owner parameter, want a decline (the exact-tuple path models that)")
	}
}

func TestLiteralBoundedFolds_ForEachDeclinesOnThisArg(t *testing.T) {
	env := NewEnv()
	receiver := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	walk, call := forEachWalkOf(t, "[1].forEach((v) => { }, self);", receiver, env, nil)
	if _, handled := forEachExactFold(walk, call); handled {
		t.Errorf("forEachExactFold handled a call carrying a thisArg, want a decline")
	}
}

// mustGet reads a binding for a failure message, tolerating absence.
func mustGet(t *testing.T, env Env, name string) abstractdomain.AbstractValue {
	t.Helper()
	v, _ := env.Get(name)
	return v
}
