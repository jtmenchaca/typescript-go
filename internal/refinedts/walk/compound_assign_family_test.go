// The compound-assign family the i-more-expressions.ts fixture pins:
// bitwise compounds on a refined scalar (compoundOperator/
// compoundBitwiseOperator, assignment_operators.go), logical assigns
// on an identifier (ReadAssignment's ||=/&&=/??= arm), element
// compounds on a refined-element array (ReadIndexedCompoundWrite,
// index_operators.go), and the accessor read-modify-write on the walk
// route (AccessorWalkReadModifyWrite, ir_accessor_calls_read_modify_write.go).
//
// Every case runs the real checker-backed FlowContext (entryEnvTestProgram's
// recipe, PORT.md's canonical program-from-source) with a `Declared["age"]`
// window standing in for `z.number().int().min(0).max(120)` — the same
// stand-in with_scope_narrowing_gate_test.go and with_statement_reach_test.go
// already use, so a test does not have to compile a real zod annotation to
// pin a set the CheckAssignability door reads identically either way.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// compoundAssignAgeWindow is Age's own window — z.number().int().min(0).max(120)
// read as a plain refinement set, the exact shape CheckAssignability judges
// a WriteBinding/WriteElement/WriteProperty call against.
func compoundAssignAgeWindow() *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// compoundAssignContext is the walk context every case in this file
// shares: a real checker program, an alias store, and a diagnostic
// sink — no Declared entries by default, so each case states only the
// binding names its own row needs. Kernel is the loaded kernel from
// compoundAssignLoadKernel — CheckAssignability's own membership
// questions (checkExactValues/CheckWornSet, set_membership.go) read
// ctx.Kernel directly with no nil guard of their own, the same way
// every other test file reaching assignability seats it
// (ctx.Kernel = kernel, e.g. call_shape_contracts_test.go); leaving it
// nil here let every Declared-window case fall into set_membership.go's
// recover() and report a decline instead of running the judge.
func compoundAssignContext(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, sink *[]assignability.RefinementDiagnostic, declared map[string]*annotations.DeclaredRefinement) *FlowContext {
	if declared == nil {
		declared = map[string]*annotations.DeclaredRefinement{}
	}
	return &FlowContext{
		P:         p,
		Kernel:    kernel,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  declared,
		Report: func(d assignability.RefinementDiagnostic) {
			*sink = append(*sink, d)
		},
	}
}

// compoundAssignLoadKernel loads the native kernel or skips, and wires
// the three kernel seats TransferBinary/TransferBitwise/TransferPow and
// the accessor read-modify-write read — entry_env_test.go's
// super_and_array_ctor_test.go-documented pattern (AGENT-BRIEF.md).
func compoundAssignLoadKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	return kernel
}

// compoundAssignFunctionStatements is the statement list of the named
// top-level function's body — entryEnvFunctionNamed one step further
// in, since every case here walks a function body statement by
// statement rather than reading a single expression.
func compoundAssignFunctionStatements(t *testing.T, p *program.CheckerProgram, name string) []*ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, name)
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("function %s has no block body", name)
	}
	return body.AsBlock().Statements.Nodes
}

/* ── group 1: bitwise compounds on a refined scalar ──────────────── */

// `age &= 0xff; age |= 0; age ^= 0; age <<= 0; age >>= 0; age >>>= 0`
// on age = 40: every one an identity, so the walk stays silent through
// all six — compoundBitwiseOperator maps every compound bitwise token
// to TransferBitwise, which was missing before this fix (compoundOperator
// alone answered `("", false)` for a bitwise token, and the arm's own
// `default: next = silence.Residue()` degraded EVERY bitwise compound to
// unknown, poisoning `age` for every later read in the same body).
func TestCompoundAssignFamily_BitwiseIdentitiesOnARefinedScalarStaySilent(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let age = 40;\n"+
		"  age &= 0xff;\n"+
		"  age |= 0;\n"+
		"  age ^= 0;\n"+
		"  age <<= 0;\n"+
		"  age >>= 0;\n"+
		"  age >>>= 0;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) != 0 {
		t.Errorf("six bitwise identities on age=40 raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age is untracked after six bitwise identity compounds")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("age after six bitwise identities = %q, %v, want exactly \"40\"", formatted, formatOk)
	}
}

// The marked row: `age |= 200` on age = 40 leaves Age's [0, 120]
// window (232 past the ceiling) — the judge WriteBinding already runs
// for every compound must fire here exactly as it does for `age += 190`.
func TestCompoundAssignFamily_ABitwiseCompoundPastTheCeilingFires(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let age = 40;\n"+
		"  age |= 200;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Errorf("`age |= 200` on age=40 (232, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

/* ── group 2: logical assigns on an identifier ────────────────────── */

// `age = 0; age ||= 20` — 0 is exactly falsy, so the runtime writes the
// right side outright: the post-state is exactly {20}, never a join
// with the (unreachable) kept-0 branch.
func TestCompoundAssignFamily_OrEqualsOnAnExactFalsyLeftWritesTheRightSideExactly(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let age = 0;\n"+
		"  age ||= 20;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age is untracked after `age ||= 20`")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "20" {
		t.Errorf("age after `age ||= 20` on age=0 = %q, %v, want exactly \"20\"", formatted, formatOk)
	}
	if len(diagnostics) != 0 {
		t.Errorf("`age ||= 20` on age=0 (in-set) raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}

// `let unset: Age | undefined; unset ??= 40` — unset starts exactly
// absent, so `??=` assigns unconditionally: the post-state is exactly
// {40}, and a later read typed Age must go silent.
func TestCompoundAssignFamily_NullishEqualsOnAnExactAbsentLeftAssignsExactly(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let unset: number | undefined;\n"+
		"  unset ??= 40;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{"unset": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("unset", abstractdomain.Undef)
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	held, ok := env.Get("unset")
	if !ok {
		t.Fatalf("unset is untracked after `unset ??= 40`")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("unset after `unset ??= 40` = %q, %v, want exactly \"40\"", formatted, formatOk)
	}
	if len(diagnostics) != 0 {
		t.Errorf("`unset ??= 40` (in-set) raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}

// The marked row: `zeroed = 0; zeroed ||= 200` — 200 leaves the [0, 120]
// window, so the judge must fire exactly as the identifier-compound
// arm already fires for `age += 190`.
func TestCompoundAssignFamily_AnOrEqualsPastTheCeilingFires(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let zeroed = 0;\n"+
		"  zeroed ||= 200;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{"zeroed": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("zeroed", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Errorf("`zeroed ||= 200` on zeroed=0 (200, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

/* ── group 3: element compounds on a refined-element array ───────── */

// `ages[0] += 190` on a declared `Age[]` whose slot 0 sits at 2 (deep
// in-range before this row): 192 leaves Age's [0, 120] window. Before
// ReadIndexedCompoundWrite existed, a compound through an element
// access matched no arm — ReadAssignment gates on identifier/property
// LEFT, ReadIndexedWrite gates on ast.KindEqualsToken — so the write
// fell to ReadForgottenAssignment, which forgets the receiver and
// returns with no judge ever run.
func TestCompoundAssignFamily_AnElementCompoundPastTheCeilingFires(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const ages = [2, 20];\n"+
		"  ages[0] += 190;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	element := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	windows := refinementsets.Repetition(element, 0, nil)
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{
		"ages": {Kind: annotations.DeclaredSet, Set: &windows},
	})
	env := NewEnv()
	env.Set("ages", abstractdomain.KnownValues([]float64{2, 20}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Errorf("`ages[0] += 190` on ages[0]=2 (192, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

// `overAges[0] **= 2` where overAges[0] = 11: 121 leaves the [0, 120]
// window — the power compound's own arm (compoundResult's
// KindAsteriskAsteriskEqualsToken branch) must run through
// ReadIndexedCompoundWrite exactly as the arithmetic compounds do.
func TestCompoundAssignFamily_AnElementPowerCompoundPastTheCeilingFires(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const overAges = [11, 20];\n"+
		"  overAges[0] **= 2;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	element := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	windows := refinementsets.Repetition(element, 0, nil)
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{
		"overAges": {Kind: annotations.DeclaredSet, Set: &windows},
	})
	env := NewEnv()
	env.Set("overAges", abstractdomain.KnownValues([]float64{11, 20}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Errorf("`overAges[0] **= 2` on overAges[0]=11 (121, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

/* ── group 4: the accessor compound read-modify-write ─────────────── */

// `box.age += 5` where age is a get/set PAIR over a backing field
// `held`: the setter's write must land in `held`, so a later read of
// `box.held` sees the STEPPED value, not the pre-compound one. Before
// AccessorWalkReadModifyWrite existed, the property-compound arm read
// `box.age` (finding no key — ConstructedInstance never census-keys an
// accessor) and WriteProperty then ADDED "age" as a fresh unknown-valued
// key; the setter never ran, so `held` never moved.
func TestCompoundAssignFamily_AnAccessorCompoundRunsTheSetterAndMovesTheBackingField(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "class AccessorBox {\n"+
		"  held = 10;\n"+
		"  get age(): number { return this.held; }\n"+
		"  set age(value: number) { this.held = value; }\n"+
		"}\n"+
		"function f(): void {\n"+
		"  const box = new AccessorBox();\n"+
		"  box.age += 5;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	held, ok := env.Get("box")
	if !ok {
		t.Fatalf("box is untracked after `box.age += 5`")
	}
	if held.Kind != abstractdomain.KindObject {
		t.Fatalf("box's tracked value is %v, want an object", held.Kind)
	}
	var fieldValue *abstractdomain.AbstractValue
	for _, key := range held.Keys {
		if key.Name == "held" {
			v := key.Value
			fieldValue = &v
		}
	}
	if fieldValue == nil {
		t.Fatalf("box has no tracked \"held\" key after the accessor compound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(*fieldValue)
	if !formatOk || formatted != "15" {
		t.Errorf("box.held after `box.age += 5` (held started at 10) = %q, %v, want exactly \"15\"", formatted, formatOk)
	}
}

// The marked row: `overBox.age += 195` where overBox.held starts at
// 10: the setter writes 205, past Age's 120 ceiling — a later
// `overBox.held` read typed Age must fire, the same judge a direct
// `overBox.held = 205` write already fires under WriteProperty.
func TestCompoundAssignFamily_AnAccessorCompoundPastTheCeilingFiresOnTheBackingFieldRead(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "class AccessorBox {\n"+
		"  held = 10;\n"+
		"  get age(): number { return this.held; }\n"+
		"  set age(value: number) { this.held = value; }\n"+
		"}\n"+
		"function f(): number {\n"+
		"  const overBox = new AccessorBox();\n"+
		"  overBox.age += 195;\n"+
		"  return overBox.held;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	result := compoundAssignAgeWindow()
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, result)
	if len(diagnostics) == 0 {
		t.Errorf("`return overBox.held` after `overBox.age += 195` (205, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

/* ── group 5: the short-circuit logical compound through an accessor ── */

// ageBoxSource mirrors q-decline-names.ts's own AgeBox exactly: a
// getter/setter pair over a private backing field, no plain field beside
// it — so a route that ever falls back to WriteProperty's plain-key
// rebuild would add a fresh "age" key rather than moving "#age".
const ageBoxSource = "class AgeBox {\n" +
	"  #age = 10;\n" +
	"  get age(): number { return this.#age; }\n" +
	"  set age(value: number) { this.#age = value; }\n" +
	"}\n"

// ageBoxField reads the named key off a `box`-named object in env,
// failing the test if the object or the key is missing. Used for both
// "#age" (the real backing field the setter writes) and "age" (the
// accessor's own construction-time snapshot key, ConstructedInstance's
// "a GET ACCESSOR is a key too" pass) — a PLAIN read of `box.age`
// (ReadObjectKeyAccess) answers this SECOND key directly and never
// re-runs the getter, so a write route that moved "#age" without also
// refreshing "age" would leave a later `box.age` read stale.
func ageBoxField(t *testing.T, env Env, key string) abstractdomain.AbstractValue {
	t.Helper()
	held, ok := env.Get("box")
	if !ok {
		t.Fatalf("box is untracked")
	}
	if held.Kind != abstractdomain.KindObject {
		t.Fatalf("box's tracked value is %v, want an object", held.Kind)
	}
	for _, k := range held.Keys {
		if k.Name == key {
			return k.Value
		}
	}
	t.Fatalf("box has no tracked %q key", key)
	return abstractdomain.AbstractValue{}
}

// `box.age = 0; box.age ||= 40;` — the getter reads 0 (exactly falsy),
// so the runtime writes the setter with 40: the backing field ends
// exactly 40, and a later read typed Age must stay silent. Before
// AccessorLogicalReadModifyWrite existed, `||=` through a property
// matched only the plain-property compound arm (ReadAssignment), which
// evaluated both sides unconditionally and called WriteProperty — never
// running the getter or the setter, so "#age" stayed at its constructor
// value and a fresh unknown-valued "age" key sat beside it.
func TestCompoundAssignFamily_OrEqualsThroughAnAccessorRunsTheGetterThenTheSetterOnTheWriteBranch(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, ageBoxSource+
		"function f(): number {\n"+
		"  const box = new AgeBox();\n"+
		"  box.age = 0;\n"+
		"  box.age ||= 40;\n"+
		"  return box.age;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	result := compoundAssignAgeWindow()
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, result)
	field := ageBoxField(t, env, "#age")
	formatted, formatOk := abstractdomain.FormatAbstractValue(field)
	if !formatOk || formatted != "40" {
		t.Fatalf("box's \"#age\" after `box.age = 0; box.age ||= 40;` = %q, %v, want exactly \"40\"", formatted, formatOk)
	}
	// the accessor's OWN construction-time snapshot key ("age") must move
	// WITH the backing field — a plain `box.age` read answers this key
	// directly (ReadObjectKeyAccess), never the getter
	snapshot := ageBoxField(t, env, "age")
	snapshotFormatted, snapshotOk := abstractdomain.FormatAbstractValue(snapshot)
	if !snapshotOk || snapshotFormatted != "40" {
		t.Fatalf("box's \"age\" snapshot after `box.age ||= 40;` = %q, %v, want exactly \"40\" — the snapshot must refresh, not stay at its construction-time 10", snapshotFormatted, snapshotOk)
	}
	if len(diagnostics) != 0 {
		t.Errorf("`return box.age` after the in-set `||=` raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}

// The marked row: `box.age = 0; box.age ||= 200;` runs the same
// getter-then-setter route on the same falsy getter read — the setter
// writes 200, past Age's 120 ceiling, so `return box.age` must fire.
// Both legs run the SAME route (AccessorLogicalReadModifyWrite); this
// case is what proves the in-set leg's silence above is not a route that
// happens to answer nothing rather than a route that answers correctly.
func TestCompoundAssignFamily_AnOrEqualsThroughAnAccessorPastTheCeilingFires(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, ageBoxSource+
		"function f(): number {\n"+
		"  const box = new AgeBox();\n"+
		"  box.age = 0;\n"+
		"  box.age ||= 200;\n"+
		"  return box.age;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	result := compoundAssignAgeWindow()
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, result)
	field := ageBoxField(t, env, "#age")
	formatted, formatOk := abstractdomain.FormatAbstractValue(field)
	if !formatOk || formatted != "200" {
		t.Fatalf("box's \"#age\" after `box.age = 0; box.age ||= 200;` = %q, %v, want exactly \"200\"", formatted, formatOk)
	}
	snapshot := ageBoxField(t, env, "age")
	snapshotFormatted, snapshotOk := abstractdomain.FormatAbstractValue(snapshot)
	if !snapshotOk || snapshotFormatted != "200" {
		t.Fatalf("box's \"age\" snapshot after `box.age ||= 200;` = %q, %v, want exactly \"200\"", snapshotFormatted, snapshotOk)
	}
	if len(diagnostics) == 0 {
		t.Errorf("`return box.age` after `box.age ||= 200` (200, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

// `box.age = 40; box.age ||= 999;` — the getter reads 40 (truthy), so
// the KEPT branch runs: no PutValue, no evaluation of the right side,
// no setter call at all. The backing field must stay exactly 40, never
// join with the unevaluated 999.
func TestCompoundAssignFamily_OrEqualsThroughAnAccessorKeepsTheGetterValueOnATruthyRead(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, ageBoxSource+
		"function f(): number {\n"+
		"  const box = new AgeBox();\n"+
		"  box.age = 40;\n"+
		"  box.age ||= 999;\n"+
		"  return box.age;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	result := compoundAssignAgeWindow()
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, result)
	field := ageBoxField(t, env, "#age")
	formatted, formatOk := abstractdomain.FormatAbstractValue(field)
	if !formatOk || formatted != "40" {
		t.Fatalf("box's \"#age\" after `box.age = 40; box.age ||= 999;` = %q, %v, want exactly \"40\" — the kept branch never runs the setter", formatted, formatOk)
	}
	if len(diagnostics) != 0 {
		t.Errorf("`return box.age` after the kept-branch `||=` raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}

/* ── group 6: the nil-kernel decline, set_membership.go's own recover ── */

// `age = 200` against Age's [0, 120] window with ctx.Kernel left nil —
// every other case in this file now seats a real loaded kernel
// (compoundAssignLoadKernel), so none of them reach
// checkSetMembershipQuestions' recover() anymore. This case deliberately
// leaves it unset: ctx.Kernel.Member panics with a nil-pointer
// dereference (RefinedTSKernel's question fields are funcs, not
// methods — a nil *RefinedTSKernel has no Member to call), and
// checkSetMembershipOfArm's decline branch must report the tree's own
// plain sentence (walk.KernelDeclinedAlertText) — never the raw Go
// panic text ("runtime error: invalid memory address or nil pointer
// dereference") that caused it.
func TestCompoundAssignFamily_ANilKernelDeclinesWithThePlainSentenceNeverTheRawPanicText(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let age = 40;\n"+
		"  age = 200;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": compoundAssignAgeWindow()})
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("`age = 200` against a nil kernel raised no diagnostic, want the decline")
	}
	for _, d := range diagnostics {
		if strings.Contains(d.MessageText, "runtime error") {
			t.Errorf("diagnostic leaked the raw panic text: %q", d.MessageText)
		}
		if strings.Contains(d.MessageText, "nil pointer") {
			t.Errorf("diagnostic leaked the raw panic text: %q", d.MessageText)
		}
	}
	if diagnostics[0].MessageText != KernelDeclinedAlertText {
		t.Errorf("diagnostic MessageText = %q, want the plain decline sentence %q", diagnostics[0].MessageText, KernelDeclinedAlertText)
	}
}
