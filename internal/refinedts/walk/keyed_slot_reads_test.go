// Pins for values stored under NON-PLAIN KEYS (keyed_slot_reads.go,
// object_literal.go, element_access.go, collection_models.go):
//
//   - a stable symbol key's slot reads back its value — function-local
//     `Symbol.for` consts and module-level `Symbol` consts both;
//   - a computed key that is one of several exact strings reads the
//     JOIN of the named slots;
//   - `Map.getOrInsert` answers the present value, else the inserted
//     default — through a cast receiver too.
//
// Every accepting pin carries its refusing control: the sibling case
// that must NOT determine (a different symbol const, an aliasable
// registry pair, a loop-declared const, a `let` binding).

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// keyedSlotProgram builds a one-file program, mirroring
// call_site_bindings_test.go's callSiteTestProgram (PORT.md's
// canonical recipe) without the surface stand-in.
func keyedSlotProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":       entrySource,
		"/tsconfig.json": `{"compilerOptions": {}, "files": ["main.ts"]}`,
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
	return &program.CheckerProgram{Program: compilerProgram, Checker: c, Entry: entry}
}

// keyedSlotFunctionNamed is the top-level FunctionDeclaration named
// text, mirroring callSiteFunctionNamed.
func keyedSlotFunctionNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if ast.IsFunctionDeclaration(statement) {
			name := statement.AsFunctionDeclaration().Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return statement
			}
		}
	}
	t.Fatalf("no function named %s", text)
	return nil
}

// keyedSlotBodyEnv walks the named function's body statements on a
// fresh environment — the walk fillSnapshotsUnder runs, minus the
// contract seeding — and hands the environment back for inspection.
// The native kernel loads first, the same gate callSiteCtxOf uses:
// skipped (never a faked pass) when the dylib is absent.
func keyedSlotBodyEnv(t *testing.T, p *program.CheckerProgram, functionName string) Env {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	fn := keyedSlotFunctionNamed(t, p, functionName)
	body := fn.AsFunctionDeclaration().Body
	if body == nil {
		t.Fatalf("function %s has no body", functionName)
	}
	ctx := &FlowContext{
		P:         p,
		Kernel:    kernel,
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Report:    func(d assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	env := NewEnv()
	AnalyzeStatements(ctx, env, body.AsBlock().Statements.Nodes, nil)
	return env
}

// keyedSlotFormatted is the formatted knowledge a walked binding holds.
func keyedSlotFormatted(t *testing.T, env Env, name string) string {
	t.Helper()
	held, tracked := env.Get(name)
	if !tracked {
		t.Fatalf("no tracked value for %s", name)
	}
	formatted, hasFormat := abstractdomain.FormatAbstractValue(held)
	if !hasFormat {
		t.Fatalf("value for %s does not format (kind %v)", name, held.Kind)
	}
	return formatted
}

// keyedSlotElementAccesses is every ElementAccessExpression in source
// order.
func keyedSlotElementAccesses(p *program.CheckerProgram) []*ast.Node {
	var out []*ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsElementAccessExpression(node) {
			out = append(out, node)
		}
		node.ForEachChild(visit)
		return false
	}
	p.Entry.AsNode().ForEachChild(visit)
	return out
}

/* ── the stable symbol slot, function-local ──────────────────────── */

func TestKeyedSlotReads_AFunctionLocalSymbolForKeyReadsBack(t *testing.T) {
	p := keyedSlotProgram(t, `
function elementStableSymbol() {
	const key = Symbol.for("syntax-coverage-age");
	const person = { [key]: 40 };
	const ok = person[key];
	const over = { [key]: 200 };
	const bad = over[key];
	return ok + bad;
}
`)
	env := keyedSlotBodyEnv(t, p, "elementStableSymbol")
	if got := keyedSlotFormatted(t, env, "ok"); got != "40" {
		t.Errorf("person[key] = %q, want %q", got, "40")
	}
	if got := keyedSlotFormatted(t, env, "bad"); got != "200" {
		t.Errorf("over[key] = %q, want %q", got, "200")
	}
}

func TestKeyedSlotReads_AFunctionLocalFreshSymbolKeyReadsBack(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const key = Symbol("age");
	const person = { [key]: 40 };
	const ok = person[key];
	return ok;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "ok"); got != "40" {
		t.Errorf("person[key] = %q, want %q", got, "40")
	}
}

/* ── the stable symbol slot, module-level ────────────────────────── */

func TestKeyedSlotReads_AModuleLevelSymbolKeyReadsBack(t *testing.T) {
	p := keyedSlotProgram(t, `
const ageKey = Symbol("age");
function readComputedStableSymbolKey() {
	const person = { [ageKey]: 40 };
	const good = person[ageKey];
	const over = { [ageKey]: 200 };
	const bad = over[ageKey];
	return good + bad;
}
`)
	env := keyedSlotBodyEnv(t, p, "readComputedStableSymbolKey")
	if got := keyedSlotFormatted(t, env, "good"); got != "40" {
		t.Errorf("person[ageKey] = %q, want %q", got, "40")
	}
	if got := keyedSlotFormatted(t, env, "bad"); got != "200" {
		t.Errorf("over[ageKey] = %q, want %q", got, "200")
	}
}

/* ── the refusing controls for the symbol slot ───────────────────── */

func TestKeyedSlotReads_ADifferentSymbolConstReadClaimsNothing(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const key = Symbol.for("x");
	const other = Symbol.for("y");
	const person = { [key]: 40 };
	const value = person[other];
	return value;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	held, tracked := env.Get("value")
	if !tracked {
		t.Fatalf("no tracked value for value")
	}
	// the spelled slot misses, and a miss claims NOTHING — not even
	// absence — because two consts can spell one registry symbol
	if held.Kind != abstractdomain.KindUnknown {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Errorf("person[other] = kind %v (%q), want the walk's own unknown", held.Kind, formatted)
	}
}

func TestKeyedSlotReads_TwoRegistryConstsWithOneKeyDropTheOverwrittenSlot(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const k1 = Symbol.for("x");
	const k2 = Symbol.for("x");
	const o = { [k1]: 40, [k2]: 200 };
	const first = o[k1];
	const second = o[k2];
	return first + second;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	// k1 and k2 are the SAME runtime symbol (sec-symbol.for), so the
	// second row overwrote the first at runtime: the first slot's exact
	// claim must not survive
	held, tracked := env.Get("first")
	if !tracked {
		t.Fatalf("no tracked value for first")
	}
	if formatted, hasFormat := abstractdomain.FormatAbstractValue(held); hasFormat && formatted == "40" {
		t.Errorf("o[k1] = %q — the aliasable slot served its stale value", formatted)
	}
	if got := keyedSlotFormatted(t, env, "second"); got != "200" {
		t.Errorf("o[k2] = %q, want %q", got, "200")
	}
}

func TestKeyedSlotReads_TwoRegistryConstsWithDistinctKeysBothServe(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const k1 = Symbol.for("x");
	const k2 = Symbol.for("y");
	const o = { [k1]: 40, [k2]: 200 };
	const first = o[k1];
	const second = o[k2];
	return first + second;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "first"); got != "40" {
		t.Errorf("o[k1] = %q, want %q", got, "40")
	}
	if got := keyedSlotFormatted(t, env, "second"); got != "200" {
		t.Errorf("o[k2] = %q, want %q", got, "200")
	}
}

func TestKeyedSlotReads_ALoopDeclaredSymbolConstDeclines(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	let out = 0;
	for (let i = 0; i < 2; i++) {
		const key = Symbol.for("x");
		const person = { [key]: 40 };
		out = person[key];
	}
	return out;
}
`)
	accesses := keyedSlotElementAccesses(p)
	if len(accesses) != 1 {
		t.Fatalf("element accesses = %d, want 1", len(accesses))
	}
	argument := accesses[0].AsElementAccessExpression().ArgumentExpression
	// a fresh symbol per iteration would alias one spelled slot across
	// iterations, so the loop-declared const is not a stable key
	if slot, _, stable := stableSymbolSlotOf(p.Checker, argument); stable {
		t.Errorf("stableSymbolSlotOf(loop-declared const) = %q, want a decline", slot)
	}
}

func TestKeyedSlotReads_ACrossFunctionLocalConstDeclines(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const key = Symbol("x");
	return () => {
		const person = { [key]: 40 };
		return person[key];
	};
}
`)
	accesses := keyedSlotElementAccesses(p)
	if len(accesses) != 1 {
		t.Fatalf("element accesses = %d, want 1", len(accesses))
	}
	argument := accesses[0].AsElementAccessExpression().ArgumentExpression
	// the access sits in the arrow, the const in f — two activation
	// stories, not the one-activation argument the local slot rests on
	if slot, _, stable := stableSymbolSlotOf(p.Checker, argument); stable {
		t.Errorf("stableSymbolSlotOf(outer function's const) = %q, want a decline", slot)
	}
}

func TestKeyedSlotReads_ALetBindingDeclines(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	let key = Symbol("x");
	const person = { [key]: 40 };
	const value = person[key];
	return value;
}
`)
	accesses := keyedSlotElementAccesses(p)
	if len(accesses) != 1 {
		t.Fatalf("element accesses = %d, want 1", len(accesses))
	}
	argument := accesses[0].AsElementAccessExpression().ArgumentExpression
	if slot, _, stable := stableSymbolSlotOf(p.Checker, argument); stable {
		t.Errorf("stableSymbolSlotOf(let binding) = %q, want a decline", slot)
	}
}

/* ── the known-string-union computed key ─────────────────────────── */

func TestKeyedSlotReads_AKnownStringUnionKeyJoinsTheSlots(t *testing.T) {
	p := keyedSlotProgram(t, `
function readComputedOtherKey(flag: boolean) {
	const key = flag ? "age" : "years";
	const person = { age: 40, years: 41 };
	const good = person[key];
	return good;
}
`)
	env := keyedSlotBodyEnv(t, p, "readComputedOtherKey")
	if got := keyedSlotFormatted(t, env, "good"); got != "{40 | 41}" {
		t.Errorf("person[key] = %q, want %q", got, "{40 | 41}")
	}
}

func TestKeyedSlotReads_AUnionKeyOverBothSlotsHoldingOneValueIsExact(t *testing.T) {
	// the c-reads-and-values.ts:109 shape: both slots hold 40, so the
	// join is the one value
	p := keyedSlotProgram(t, `
function f(flag: boolean) {
	const key = flag ? "age" : "years";
	const person = { age: 40, years: 40 };
	const good = person[key];
	return good;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "good"); got != "40" {
		t.Errorf("person[key] = %q, want %q", got, "40")
	}
}

func TestKeyedSlotReads_AUnionKeyMemberMissingFromACompleteObjectWearsAbsence(t *testing.T) {
	p := keyedSlotProgram(t, `
function f(flag: boolean) {
	const key = flag ? "age" : "years";
	const person = { age: 40 };
	const value = person[key];
	return value;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	held, tracked := env.Get("value")
	if !tracked {
		t.Fatalf("no tracked value for value")
	}
	// "years" misses a COMPLETE plain object: that member reads exactly
	// undefined, so the join wears the absent arm — never a bare {40}
	if held.Kind != abstractdomain.KindPossiblyUndefined {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Fatalf("person[key] = kind %v (%q), want the maybe-absent wrapper", held.Kind, formatted)
	}
	inner, hasInner := abstractdomain.FormatAbstractValue(*held.Inner)
	if !hasInner || inner != "40" {
		t.Errorf("present arm = %q, want %q", inner, "40")
	}
}

/* ── Map.getOrInsert ─────────────────────────────────────────────── */

func TestKeyedSlotReads_MapGetOrInsertThroughACastAnswersThePresentValue(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const ages = new Map([["ann", 40]]);
	const good = (ages as unknown as {
		getOrInsert(k: string, v: number): number;
	}).getOrInsert("ann", 41);
	return good;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	// the present 40 wins over the 41 default
	if got := keyedSlotFormatted(t, env, "good"); got != "40" {
		t.Errorf("ages.getOrInsert(\"ann\", 41) = %q, want %q", got, "40")
	}
}

func TestKeyedSlotReads_MapGetOrInsertPresentValueBeatsTheDefault(t *testing.T) {
	// the c-reads-and-values.ts:901 control: the present 200 must win,
	// so the marked twin keeps firing
	p := keyedSlotProgram(t, `
function f() {
	const overs = new Map([["bea", 200]]);
	const bad = (overs as unknown as {
		getOrInsert(k: string, v: number): number;
	}).getOrInsert("bea", 40);
	return bad;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "bad"); got != "200" {
		t.Errorf("overs.getOrInsert(\"bea\", 40) = %q, want %q", got, "200")
	}
}

func TestKeyedSlotReads_MapGetOrInsertInsertsIntoACompleteMap(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const ages = new Map([["ann", 40]]);
	const inserted = (ages as unknown as {
		getOrInsert(k: string, v: number): number;
	}).getOrInsert("bea", 7);
	const readBack = ages.get("bea");
	return inserted;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "inserted"); got != "7" {
		t.Errorf("getOrInsert on a provably absent key = %q, want the inserted %q", got, "7")
	}
	// the insertion is a real write: the map's own get finds the entry
	if got := keyedSlotFormatted(t, env, "readBack"); got != "7" {
		t.Errorf("ages.get(\"bea\") after the insert = %q, want %q", got, "7")
	}
}

func TestKeyedSlotReads_MapGetOrInsertOnABareReceiverStillServes(t *testing.T) {
	// no cast at all: the dispatcher's own tracked-name path carries the
	// call (the walk reads syntax, so the host's own view of whether
	// getOrInsert exists on Map does not gate it)
	p := keyedSlotProgram(t, `
function f() {
	const m = new Map([["k", 3]]);
	const r = m.getOrInsert("k", 9);
	return r;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "r"); got != "3" {
		t.Errorf("m.getOrInsert(\"k\", 9) = %q, want %q", got, "3")
	}
}

/* ── the pure pieces ─────────────────────────────────────────────── */

func TestKeyedSlotReads_ExactWordsOfAJoinedStringPair(t *testing.T) {
	age := abstractdomain.KnownValues(refinementsets.CodepointsOf("age"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	years := abstractdomain.KnownValues(refinementsets.CodepointsOf("years"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	joined := abstractdomain.JoinKnown(age, years)
	if joined.Kind != abstractdomain.KindSet {
		t.Fatalf("join of two exact strings = kind %v, want a set", joined.Kind)
	}
	words, enumerable := exactWordsOfSet(joined.Set)
	if !enumerable || len(words) != 2 {
		t.Fatalf("exactWordsOfSet(join) = %d words, %v — want 2, true", len(words), enumerable)
	}
	if stringOf(words[0]) != "age" || stringOf(words[1]) != "years" {
		t.Errorf("words = %q, %q — want %q, %q", stringOf(words[0]), stringOf(words[1]), "age", "years")
	}
}

func TestKeyedSlotReads_ExactWordsDeclineNonWordForms(t *testing.T) {
	// a ray is not a finite word list
	if _, enumerable := exactWordsOfSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))); enumerable {
		t.Errorf("exactWordsOfSet(ray) enumerated — want a decline")
	}
	// a two-member oneOf is not a word (WordOf reads single-member
	// oneOf only)
	if _, enumerable := exactWordsOfSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))); enumerable {
		t.Errorf("exactWordsOfSet(oneOf pair) enumerated — want a decline")
	}
}

func TestKeyedSlotReads_ProvablyDistinctSymbolKeys(t *testing.T) {
	fresh := symbolKeyConstruction{}
	registryX := symbolKeyConstruction{Registry: true, RegistryKeyKnown: true, RegistryKey: "x"}
	registryY := symbolKeyConstruction{Registry: true, RegistryKeyKnown: true, RegistryKey: "y"}
	registryUnknown := symbolKeyConstruction{Registry: true}
	cases := []struct {
		name string
		a, b symbolKeyConstruction
		want bool
	}{
		{"fresh vs fresh", fresh, fresh, true},
		{"fresh vs registry", fresh, registryX, true},
		{"registry vs fresh", registryX, fresh, true},
		{"one registry key twice", registryX, registryX, false},
		{"two registry keys", registryX, registryY, true},
		{"registry vs unspelled registry", registryX, registryUnknown, false},
	}
	for _, c := range cases {
		if got := provablyDistinctSymbolKeys(c.a, c.b); got != c.want {
			t.Errorf("provablyDistinctSymbolKeys(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

/* ── the enumeration discipline over symbol slots ────────────────── */

func TestKeyedSlotReads_ObjectKeysNeverSurfaceASymbolSlot(t *testing.T) {
	p := keyedSlotProgram(t, `
function f() {
	const key = Symbol.for("x");
	const person = { [key]: 40, age: 1 };
	const names = Object.keys(person);
	const count = names.length;
	return count;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	// runtime Object.keys yields ["age"] — the symbol key is not a
	// String-valued key
	if got := keyedSlotFormatted(t, env, "count"); got != "1" {
		t.Errorf("Object.keys length = %q, want %q", got, "1")
	}
}
