// Pins for the promise shapes: the statics (all / race / allSettled
// and their declines), the instance methods (then / catch / finally
// on a walk-built promise), and the constructor's executor. Program
// builds mirror entry_env_test.go's canonical recipe; the scan-only
// rows parse without a checker, like kernel_summary_direct_test.go.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// promiseShapesProgram builds a checked program from one entry source
// (the canonical recipe — entry_env_test.go's entryEnvTestProgram).
func promiseShapesProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts": entrySource,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts"]
		}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig.json: %v", errors)
	}
	compilerProgram := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	compilerProgram.BindSourceFiles()
	c, done := compilerProgram.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := compilerProgram.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	return &program.CheckerProgram{
		Program: compilerProgram,
		Checker: c,
		Entry:   entry,
	}
}

func promiseShapesCtx(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
	}
}

// promiseShapesCall finds the index-th CallExpression whose callee is
// a property access naming method, in source order.
func promiseShapesCall(t *testing.T, root *ast.Node, method string, index int) *ast.Node {
	t.Helper()
	var found *ast.Node
	seen := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := Unwrapped(node.AsCallExpression().Expression)
			if ast.IsPropertyAccessExpression(callee) &&
				callee.AsPropertyAccessExpression().Name().Text() == method {
				if seen == index {
					found = node
					return true
				}
				seen++
			}
		}
		node.ForEachChild(visit)
		return found != nil
	}
	root.ForEachChild(visit)
	if found == nil {
		t.Fatalf("no call of .%s at index %d", method, index)
	}
	return found
}

// promiseShapesNew finds the index-th NewExpression in source order.
func promiseShapesNew(t *testing.T, root *ast.Node, index int) *ast.Node {
	t.Helper()
	var found *ast.Node
	seen := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsNewExpression(node) {
			if seen == index {
				found = node
				return true
			}
			seen++
		}
		node.ForEachChild(visit)
		return found != nil
	}
	root.ForEachChild(visit)
	if found == nil {
		t.Fatalf("no new expression at index %d", index)
	}
	return found
}

func promiseOfValues(values ...float64) abstractdomain.AbstractValue {
	inner := abstractdomain.KnownValues(values, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
}

// valuesHold answers whether a value spells exactly these number
// members, order-agnostic — as an exact word list (KindValues) or as
// the union-of-words set a numeric JoinKnown builds (KindSet, read
// back through exactWordsOfSet).
func valuesHold(value abstractdomain.AbstractValue, members ...float64) bool {
	var held []float64
	switch {
	case value.Kind == abstractdomain.KindValues && value.KindTag == abstractdomain.PrimitiveNumber:
		held = value.Values
	case value.Kind == abstractdomain.KindSet:
		words, ok := exactWordsOfSet(value.Set)
		if !ok {
			return false
		}
		for _, word := range words {
			if len(word) != 1 {
				return false
			}
			held = append(held, word[0])
		}
	default:
		return false
	}
	value = abstractdomain.KnownValues(held, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	if len(value.Values) != len(members) {
		return false
	}
	for _, member := range members {
		held := false
		for _, v := range value.Values {
			if v == member {
				held = true
				break
			}
		}
		if !held {
			return false
		}
	}
	return true
}

/* ── the statics ─────────────────────────────────────────────────── */

func TestPromiseStatics_AllWrapsTheSettledTupleInElementOrder(t *testing.T) {
	p := promiseShapesProgram(t, `
		async function shapes() {
			Promise.all([Promise.resolve(40), Promise.resolve(41)]);
		}
	`)
	call := promiseShapesCall(t, p.Entry.AsNode(), "all", 0)
	answered := readPromiseStatics(promiseShapesCtx(p), NewEnv(), call)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseStatics(Promise.all) = %+v, want a promise wrap", answered)
	}
	inner := *answered.Inner
	if inner.Kind != abstractdomain.KindList || len(inner.Items) != 2 {
		t.Fatalf("Promise.all inner = %+v, want a two-item list", inner)
	}
	if !valuesHold(inner.Items[0], 40) || !valuesHold(inner.Items[1], 41) {
		t.Errorf("Promise.all items = %+v, want [40] then [41] in element order", inner.Items)
	}
}

func TestPromiseStatics_RaceJoinsEveryRacersSettledValue(t *testing.T) {
	p := promiseShapesProgram(t, `
		async function shapes() {
			Promise.race([Promise.resolve(40), Promise.resolve(41)]);
		}
	`)
	call := promiseShapesCall(t, p.Entry.AsNode(), "race", 0)
	answered := readPromiseStatics(promiseShapesCtx(p), NewEnv(), call)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseStatics(Promise.race) = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40, 41) {
		t.Errorf("Promise.race inner = %+v, want the join {40, 41}", *answered.Inner)
	}
}

func TestPromiseStatics_AllSettledSnapshotsCarryTheValueKey(t *testing.T) {
	p := promiseShapesProgram(t, `
		async function shapes() {
			Promise.allSettled([Promise.resolve(40)]);
		}
	`)
	call := promiseShapesCall(t, p.Entry.AsNode(), "allSettled", 0)
	answered := readPromiseStatics(promiseShapesCtx(p), NewEnv(), call)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseStatics(Promise.allSettled) = %+v, want a promise wrap", answered)
	}
	inner := *answered.Inner
	if inner.Kind != abstractdomain.KindList || len(inner.Items) != 1 {
		t.Fatalf("Promise.allSettled inner = %+v, want a one-item list", inner)
	}
	snapshot := inner.Items[0]
	if snapshot.Kind != abstractdomain.KindObject || !snapshot.Complete {
		t.Fatalf("snapshot = %+v, want a complete object", snapshot)
	}
	sawStatus, sawValue := false, false
	for _, key := range snapshot.Keys {
		switch key.Name {
		case "status":
			sawStatus = true
		case "value":
			sawValue = true
			if !valuesHold(key.Value, 40) {
				t.Errorf("snapshot value key = %+v, want 40", key.Value)
			}
		}
	}
	if !sawStatus || !sawValue {
		t.Errorf("snapshot keys = %+v, want status and value", snapshot.Keys)
	}
}

func TestPromiseStatics_AnUnreadableFieldAnswersTheResidueNotAGuess(t *testing.T) {
	p := promiseShapesProgram(t, `
		declare const g: unknown;
		async function shapes() {
			Promise.all(g as never);
		}
	`)
	call := promiseShapesCall(t, p.Entry.AsNode(), "all", 0)
	answered := readPromiseStatics(promiseShapesCtx(p), NewEnv(), call)
	if answered == nil {
		t.Fatalf("readPromiseStatics(Promise.all(g)) = nil, want the residue — the call IS the recognized shape")
	}
	if answered.Kind != abstractdomain.KindUnknown {
		t.Errorf("readPromiseStatics(Promise.all(g)) = %+v, want unknown (the residue)", answered)
	}
}

func TestPromiseStatics_AllSettledOfAHeldPromiseDeclines(t *testing.T) {
	// only a literal `Promise.resolve(…)` element provably FULFILLS —
	// a promise reaching allSettled any other way could reject, and
	// its snapshot would carry a reason, not a value
	p := promiseShapesProgram(t, `
		async function shapes(q: Promise<number>) {
			Promise.allSettled([q]);
		}
	`)
	call := promiseShapesCall(t, p.Entry.AsNode(), "allSettled", 0)
	env := NewEnv()
	env.Set("q", promiseOfValues(40))
	answered := readPromiseStatics(promiseShapesCtx(p), env, call)
	if answered == nil || answered.Kind != abstractdomain.KindUnknown {
		t.Errorf("readPromiseStatics(Promise.allSettled([q])) = %+v, want unknown (the residue)", answered)
	}
}

/* ── the instance methods ────────────────────────────────────────── */

func promiseInstanceSite(t *testing.T, p *program.CheckerProgram, method string, receiver abstractdomain.AbstractValue) MethodCallSite {
	t.Helper()
	call := promiseShapesCall(t, p.Entry.AsNode(), method, 0)
	pa := Unwrapped(call.AsCallExpression().Expression).AsPropertyAccessExpression()
	return MethodCallSite{
		Ctx:                promiseShapesCtx(p),
		Env:                NewEnv(),
		E:                  call,
		ReceiverExpression: pa.Expression,
		Receiver:           receiver,
		Method:             method,
	}
}

func TestPromiseInstance_ThenRunsTheHandlerOverTheSettledValue(t *testing.T) {
	p := promiseShapesProgram(t, `
		declare const q: Promise<number>;
		async function shapes() {
			q.then((age) => age);
		}
	`)
	site := promiseInstanceSite(t, p, "then", promiseOfValues(40))
	answered := readPromiseInstanceMethod(site)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseInstanceMethod(then) = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40) {
		t.Errorf("then inner = %+v, want the identity 40", *answered.Inner)
	}
}

func TestPromiseInstance_ThenCarriesAnOutOfSetSettlementThrough(t *testing.T) {
	// the marked twins depend on 200 RIDING the then — the model must
	// carry it, never launder it
	p := promiseShapesProgram(t, `
		declare const q: Promise<number>;
		async function shapes() {
			q.then((age) => age);
		}
	`)
	site := promiseInstanceSite(t, p, "then", promiseOfValues(200))
	answered := readPromiseInstanceMethod(site)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseInstanceMethod(then, 200) = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 200) {
		t.Errorf("then inner = %+v, want 200 carried through", *answered.Inner)
	}
}

func TestPromiseInstance_CatchJoinsTheSettlementWithTheHandlersResult(t *testing.T) {
	p := promiseShapesProgram(t, `
		declare const q: Promise<number>;
		async function shapes() {
			q.catch(() => 0);
		}
	`)
	site := promiseInstanceSite(t, p, "catch", promiseOfValues(40))
	answered := readPromiseInstanceMethod(site)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseInstanceMethod(catch) = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40, 0) {
		t.Errorf("catch inner = %+v, want the join {40, 0} — the settled value rides past the catch", *answered.Inner)
	}
}

func TestPromiseInstance_FinallyForwardsTheReceiversSettlement(t *testing.T) {
	p := promiseShapesProgram(t, `
		declare const q: Promise<number>;
		async function shapes() {
			q.finally(() => {});
		}
	`)
	site := promiseInstanceSite(t, p, "finally", promiseOfValues(40))
	answered := readPromiseInstanceMethod(site)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("readPromiseInstanceMethod(finally) = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40) {
		t.Errorf("finally inner = %+v, want the untouched 40", *answered.Inner)
	}
}

func TestPromiseInstance_ANonPromiseReceiverIsNotClaimed(t *testing.T) {
	p := promiseShapesProgram(t, `
		declare const q: { then(f: (x: number) => number): number };
		async function shapes() {
			q.then((age) => age);
		}
	`)
	site := promiseInstanceSite(t, p, "then",
		abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if answered := readPromiseInstanceMethod(site); answered != nil {
		t.Errorf("readPromiseInstanceMethod over a non-promise receiver = %+v, want nil — the chain moves on", answered)
	}
}

/* ── the constructor ─────────────────────────────────────────────── */

func TestPromiseConstruction_TheExecutorsResolveFillsTheInnerValue(t *testing.T) {
	p := promiseShapesProgram(t, `
		async function shapes() {
			new Promise<number>((resolve) => resolve(40));
		}
	`)
	node := promiseShapesNew(t, p.Entry.AsNode(), 0)
	answered := ReadPromiseConstruction(promiseShapesCtx(p), NewEnv(), node)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("ReadPromiseConstruction = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40) {
		t.Errorf("constructed inner = %+v, want the resolved 40", *answered.Inner)
	}
}

func TestPromiseConstruction_TwoResolveCallsJoin(t *testing.T) {
	p := promiseShapesProgram(t, `
		async function shapes(cond: boolean) {
			new Promise<number>((resolve) => {
				if (cond) resolve(40);
				else resolve(41);
			});
		}
	`)
	node := promiseShapesNew(t, p.Entry.AsNode(), 0)
	answered := ReadPromiseConstruction(promiseShapesCtx(p), NewEnv(), node)
	if answered == nil || answered.Kind != abstractdomain.KindPromise || answered.Inner == nil {
		t.Fatalf("ReadPromiseConstruction = %+v, want a promise wrap", answered)
	}
	if !valuesHold(*answered.Inner, 40, 41) {
		t.Errorf("constructed inner = %+v, want the join {40, 41}", *answered.Inner)
	}
}

func TestPromiseConstruction_AnEscapingResolveDeclines(t *testing.T) {
	// resolve handed to another call could fulfill with a value this
	// read never saw — total-or-decline over the parameter's uses
	p := promiseShapesProgram(t, `
		async function shapes() {
			new Promise<number>((resolve) => setTimeout(resolve, 10));
		}
	`)
	node := promiseShapesNew(t, p.Entry.AsNode(), 0)
	if answered := ReadPromiseConstruction(promiseShapesCtx(p), NewEnv(), node); answered != nil {
		t.Errorf("ReadPromiseConstruction over an escaping resolve = %+v, want nil", answered)
	}
}

/* ── Promise.withResolvers ───────────────────────────────────────── */

// TestPromiseWithResolvers_TheDestructuredResolveCallFillsThePromise
// pins c-reads-and-values.ts's promiseWithResolvers row's destructured
// half: `const { promise, resolve } = Promise.withResolvers<number>();
// resolve(40); await promise` must read exactly 40 — before the fix,
// resolve/reject were bound as bare abstractdomain.HostFunction values
// with nothing pairing them to their own promise binding, so a later
// resolve(40) call reached no model at all (readUnmodeledMethod's
// generic fallback) and `promise` stayed the walk's own residue
// forever.
func TestPromiseWithResolvers_TheDestructuredResolveCallFillsThePromise(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "async function caller(): Promise<number> {\n" +
		"  const { promise, resolve } = Promise.withResolvers<number>();\n" +
		"  resolve(40);\n" +
		"  return await promise;\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if !valuesHold(returned, 40) {
		t.Errorf("resolve(40) then await promise determined %+v, want exactly 40", returned)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("an in-range withResolvers round trip reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// TestPromiseWithResolvers_ThePropertyFormResolveCallFillsThePromise
// is the same row's property-form half: `const over =
// Promise.withResolvers<number>(); over.resolve(200); await
// over.promise` — the whole capability kept as ONE binding, so the
// write lands on that binding's own "promise" key directly
// (readPromiseWithResolversPropertyCall) rather than through the
// ResolverTargets pairing the destructured form needs.
func TestPromiseWithResolvers_ThePropertyFormResolveCallFillsThePromise(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "async function caller(): Promise<number> {\n" +
		"  const over = Promise.withResolvers<number>();\n" +
		"  over.resolve(200);\n" +
		"  return await over.promise;\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if !valuesHold(returned, 200) {
		t.Errorf("over.resolve(200) then await over.promise determined %+v, want exactly 200", returned)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("an in-range withResolvers round trip reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

/* ── the resolve-call scan, parser only ──────────────────────────── */

// promiseShapesArrow parses a throwaway source and answers its first
// arrow function.
func promiseShapesArrow(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/s.ts", Path: "/s.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsArrowFunction(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	file.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no arrow parsed from %q", source)
	}
	return found
}

func TestPromiseResolveCallsOf_CollectsEveryDirectCallAndTheBareOne(t *testing.T) {
	arrow := promiseShapesArrow(t, `const q = new Promise((resolve) => { resolve(40); resolve(); });`)
	arguments, total := promiseResolveCallsOf(arrow.Body(), "resolve")
	if !total {
		t.Fatalf("promiseResolveCallsOf total = false, want true — every use is a direct call")
	}
	if len(arguments) != 2 || arguments[0] == nil || arguments[1] != nil {
		t.Errorf("promiseResolveCallsOf arguments = %+v, want one node then one nil (the bare call)", arguments)
	}
}

func TestPromiseResolveCallsOf_AnAliasedResolveAnswersFalse(t *testing.T) {
	arrow := promiseShapesArrow(t, `const q = new Promise((resolve) => { const alias = resolve; alias(40); });`)
	if _, total := promiseResolveCallsOf(arrow.Body(), "resolve"); total {
		t.Errorf("an aliased resolve scanned as total — the alias escapes the read")
	}
}

func TestPromiseResolveCallsOf_AResolveInsideAnothersArgumentIsStillSeen(t *testing.T) {
	arrow := promiseShapesArrow(t, `const q = new Promise((resolve) => { resolve(f(resolve)); });`)
	if _, total := promiseResolveCallsOf(arrow.Body(), "resolve"); total {
		t.Errorf("a resolve hidden inside another's argument scanned as total — the inner use escapes")
	}
}
