// Pins the fix this unit lands for tsc-vscode/fixtures/language/
// syntax-coverage/m-zod-schema.ts (7 markers) and
// o-grammar-refinements.ts (11 markers): a plain `.parse(arg)` call
// that EvaluateParseOutcome proves throws now reports 7001 at the
// argument (schema_runtime_models.go), and the exact-parse pipeline
// now recognizes a chain rooted in the checker's OWN vendored surface
// (refined-ts-typescript/surface/z.ts), not only real npm zod
// (isZodRootHere, schema_runtime_models.go) — before this unit,
// EvaluateParseOutcome answered "no claim" for every `.parse` call
// the m/o fixtures exercise, in-set or out, because `run()`'s root
// case never matched the surface's own `z.number()` / `z.object()` /
// `z.enum()` / … roots.
//
// service/check.ts (the whole-file `check()` the fixture's own
// @refinedts-expect-error rows are judged by) has no Go twin yet
// (go-port-tracker.md: "service | pending (last)") — every other
// kernel-gated walk test in this package (yield_contract_test.go,
// collection_read_leftovers_test.go) runs the same substitute route
// this file takes instead: CompileContractFileFacts + AnalyzeFunction
// directly, asserting on the reported diagnostics. The surface
// stand-in below is annotations_test_helpers_test.go's own
// surfaceStandIn (the root-constructor NAMES CompileAnnotation reads,
// each returning `any` so the chain methods that follow — .min/.max/
// .int/.gte/.gt/.lte/.lt/.regex/.startsWith/.refine/.parse/… — type-
// check on the `any` return the way the TS surface types check on the
// real one) with .parse/.safeParse/.refine/.optional added, since the
// annotations-package stand-in never needed them (CompileAnnotation
// never walks past a runtime-vocabulary method).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// parseVocabSurfaceStandIn is annotations_test_helpers_test.go's own
// surfaceStandIn, plus the runtime-vocabulary methods that compiler
// package never needed (CompileAnnotation stops at a runtime method —
// zod_runtime_vocabulary.go's own ZodRuntimeMethods list — so the
// annotations-package stand-in has no .parse/.refine/.optional; this
// file's EvaluateParseOutcome walks straight through the chain, so
// they need real call shapes here). Every method returns `any` — the
// TS checker's OWN resolution of the receiver identifier back to this
// file is what isZodRootHere reads (symbol identity, never the
// declared return type), so run()'s syntactic walk over the AST reads
// the same chain shape whether the return type is `any` or the real
// NumberSchema/StringSchema/ObjectSchema/Schema union.
const parseVocabSurfaceStandIn = `
export interface AnyChain {
  min(a: number): AnyChain;
  max(a: number): AnyChain;
  gte(a: number): AnyChain;
  lte(a: number): AnyChain;
  gt(a: number): AnyChain;
  lt(a: number): AnyChain;
  int(): AnyChain;
  regex(pattern: RegExp): AnyChain;
  startsWith(s: string): AnyChain;
  optional(): AnyChain;
  refine(predicate: (value: any) => boolean): AnyChain;
  parse(x: unknown): any;
  safeParse(x: unknown): any;
}
export function number(): AnyChain { return {} as AnyChain; }
export function string(): AnyChain { return {} as AnyChain; }
export function boolean(): AnyChain { return {} as AnyChain; }
export function bigint(): AnyChain { return {} as AnyChain; }
export function symbol(): AnyChain { return {} as AnyChain; }
export function unknown(): AnyChain { return {} as AnyChain; }
export function json(): AnyChain { return {} as AnyChain; }
export function date(): AnyChain { return {} as AnyChain; }
export function literal(v: any): AnyChain { return {} as AnyChain; }
export function enum_(members: any): AnyChain { return {} as AnyChain; }
export { enum_ as enum };
export function union(members: any): AnyChain { return {} as AnyChain; }
export function intersection(a: any, b: any): AnyChain { return {} as AnyChain; }
export function exclude(a: any, b: any): AnyChain { return {} as AnyChain; }
export function array(item: any): AnyChain { return {} as AnyChain; }
export function tuple(items: any): AnyChain { return {} as AnyChain; }
export function object(shape: any): AnyChain { return {} as AnyChain; }
export function strictObject(shape: any): AnyChain { return {} as AnyChain; }
export function looseObject(shape: any): AnyChain { return {} as AnyChain; }
export function ref(target: any): AnyChain { return {} as AnyChain; }
export function map(k: any, v: any): AnyChain { return {} as AnyChain; }
export function set(v: any): AnyChain { return {} as AnyChain; }
export function promise(inner: any): AnyChain { return {} as AnyChain; }
export const iso = { datetime: (): AnyChain => ({} as AnyChain), date: (): AnyChain => ({} as AnyChain), time: (): AnyChain => ({} as AnyChain), duration: (): AnyChain => ({} as AnyChain) };
export const coerce = { number: (): AnyChain => ({} as AnyChain) };
export type infer<T> = any;
export type Gte<Name extends string> = any;
export type Gt<Name extends string> = any;
export type Lte<Name extends string> = any;
export type Lt<Name extends string> = any;
`

const parseVocabSurfacePath = "/surface/z.ts"

// parseVocabProgram builds a two-file program (entry + the surface
// stand-in above, registered under SurfacePaths the way
// resolvesToSurface/isZodRootHere both read it) — entryEnvTestProgram
// mirrored with the one addition this file needs.
func parseVocabProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":              entrySource,
		parseVocabSurfacePath:   parseVocabSurfaceStandIn,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts", "surface/z.ts"]
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
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{parseVocabSurfacePath: true},
	}
}

// parseVocabFunctionNamed mirrors entryEnvFunctionNamed against this
// file's own program builder.
func parseVocabFunctionNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
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

// parseVocabKernel loads the native kernel the way every other
// kernel-backed walk test in this package does, or skips — the
// return-type judgment (Age/Label/…) still asks the kernel a
// membership question even though EvaluateParseOutcome itself never
// does.
func parseVocabKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
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

// parseVocabAssignabilityKernel loads the native kernel WITHOUT seating
// any of the three global seats (SetEngineKernel/SetTransferKernel/
// narrowing.SetNarrowKernel) — for a walk-route-isolation test that
// wants to keep EngineKernelHeld() nil (so applySummary's kernel-served
// route keeps declining and can never mask the walk-route regression
// the test pins) while still giving ctx.Kernel a real kernel to ask:
// CheckAssignability's own membership question (set_membership.go's
// checkExactValues, ctx.Kernel.Member) needs a non-nil kernel to report
// a 7001 refutation at all — with ctx.Kernel left nil, that question
// panics on a nil pointer dereference (RefinedTSKernel's fields are
// funcs, not methods), recovers into KernelDeclinedAlertText's 7002 (or,
// for the in-set leg, an inconsistent silent decline) — a decline is
// not the refutation these rows exist to prove. Skips (never a faked
// pass) when the native kernel dylib is absent, per every other
// kernel-gated walk test's own recipe.
func parseVocabAssignabilityKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

// parseVocabRun compiles source's contracts (CompileContractFileFacts,
// the same pass 2 door yield_contract_test.go/
// collection_read_leftovers_test.go take), runs AnalyzeFunction for
// one named function, and hands back the diagnostics it reported.
func parseVocabRun(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string, name string) []assignability.RefinementDiagnostic {
	t.Helper()
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := parseVocabFunctionNamed(t, p, name)
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for %s", name)
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for %s", name)
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report: func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
		Kernel:   kernel,
	}
	AnalyzeFunction(ctx, contract, nil)
	return diagnostics
}

func parseVocabWantSilent(t *testing.T, diagnostics []assignability.RefinementDiagnostic, label string) {
	t.Helper()
	if len(diagnostics) != 0 {
		t.Errorf("%s reported %d diagnostic(s), want none (in-set argument, .parse determined it passes): %+v", label, len(diagnostics), diagnostics)
	}
}

func parseVocabWantRefusedAtArgument(t *testing.T, diagnostics []assignability.RefinementDiagnostic, label string) {
	t.Helper()
	if len(diagnostics) == 0 {
		t.Fatalf("%s reported no diagnostics, want a 7001 refutation at the .parse argument (EvaluateParseOutcome proves the runtime throw)", label)
	}
	found := false
	for _, d := range diagnostics {
		if d.Code == 7001 {
			found = true
		}
	}
	if !found {
		t.Errorf("%s reported %+v, want a 7001 among them (7002 would mean the argument stayed undetermined instead of a proven throw)", label, diagnostics)
	}
}

// ── m-zod-schema.ts §M rows ─────────────────────────────────────────

// TestParseVocab_NumberChain pins m's parseNumberChainOk (34, silent)
// / parseNumberChainOverCeiling (40, @refinedts-expect-error): a
// `z.number().int().min(0).max(120)` chain's `.parse` argument judged
// against the compiled ray-and-integer conjunction.
func TestParseVocab_NumberChain(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function ok(): Age { return zAge.parse(40); }
function over(): Age { return zAge.parse(200); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zAge.parse(40)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "over"), "zAge.parse(200)")
}

// TestParseVocab_StringChain pins m's parseStringChainOk /
// parseStringChainOverLength: a `z.string().min(1).max(8)` chain's
// length window judged at `.parse`.
func TestParseVocab_StringChain(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zLabel = z.string().min(1).max(8);
type Label = z.infer<typeof zLabel>;
function ok(): Label { return zLabel.parse("abcdef"); }
function over(): Label { return zLabel.parse("too-long-string"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zLabel.parse("abcdef")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "over"), `zLabel.parse("too-long-string")`)
}

// TestParseVocab_RegexConjunct pins m's parseRegexOk / parseRegexRefused:
// a `.regex(/^[0-9]+$/)` pattern conjunct judged at `.parse`.
func TestParseVocab_RegexConjunct(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zDigits = z.string().min(1).max(4).regex(/^[0-9]+$/);
type Digits = z.infer<typeof zDigits>;
function ok(): Digits { return zDigits.parse("42"); }
function refused(): Digits { return zDigits.parse("ab"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zDigits.parse("42")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "refused"), `zDigits.parse("ab")`)
}

// TestParseVocab_UnionOfLiterals pins m's parseUnionOk / parseUnionOutside
// and o's unionOfNumberLiteralsOk / unionOfNumberLiteralsOutside: a
// `z.union([z.literal(10), z.literal(20), z.literal(30)])` judged
// member-by-member at `.parse`.
func TestParseVocab_UnionOfLiterals(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zPick = z.union([z.literal(10), z.literal(20), z.literal(30)]);
type Pick = z.infer<typeof zPick>;
function ok(): Pick { return zPick.parse(20); }
function outside(): Pick { return zPick.parse(25); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zPick.parse(20)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "outside"), "zPick.parse(25)")
}

// TestParseVocab_ObjectKeyByKey pins m's parseObjectOk /
// parseObjectKeyRefused: `z.object({age, label})`'s `.parse` judges
// EVERY stated key against its own chain — one out-of-set key
// (age: 200) throws, even though label ("ann") is in-set.
func TestParseVocab_ObjectKeyByKey(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zPerson = z.object({
  age: z.number().int().min(0).max(120),
  label: z.string().min(1).max(8),
});
type Person = z.infer<typeof zPerson>;
function ok(): Person { return zPerson.parse({ age: 40, label: "ann" }); }
function keyRefused(): Person { return zPerson.parse({ age: 200, label: "ann" }); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zPerson.parse({age:40,label:"ann"})`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "keyRefused"), `zPerson.parse({age:200,label:"ann"})`)
}

// TestParseVocab_RefinePredicate pins m's parseRefineOk /
// parseRefineRefused: `.refine((r) => r.hi >= r.lo)` on an object
// schema — the predicate itself decides the exact-argument outcome
// (run()'s "refine" case inlines the callback on the exact object).
func TestParseVocab_RefinePredicate(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zRange = z.object({
  lo: z.number().int().min(0).max(120),
  hi: z.number().int().min(0).max(120),
}).refine((r) => r.hi >= r.lo);
type Range = z.infer<typeof zRange>;
function ok(): Range { return zRange.parse({ lo: 10, hi: 20 }); }
function refused(): Range { return zRange.parse({ lo: 10, hi: 5 }); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zRange.parse({lo:10,hi:20})")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "refused"), "zRange.parse({lo:10,hi:5})")
}

// TestParseVocab_OptionalKey pins m's parseOptionalAbsentOk /
// parseOptionalPresentRefused: an absent optional key never reaches
// its own chain (run()'s hasWrapperTail check), while a PRESENT
// optional key still judges against it.
func TestParseVocab_OptionalKey(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zProfile = z.object({
  age: z.number().int().min(0).max(120),
  nickname: z.string().min(1).max(8).optional(),
});
type Profile = z.infer<typeof zProfile>;
function absentOk(): Profile { return zProfile.parse({ age: 40 }); }
function presentRefused(): Profile { return zProfile.parse({ age: 40, nickname: "too-long-nickname" }); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "absentOk"), `zProfile.parse({age:40})`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "presentRefused"), `zProfile.parse({age:40,nickname:"too-long-nickname"})`)
}

// ── o-grammar-refinements.ts §O rows ────────────────────────────────

// TestParseVocab_EnumOfStringLiterals pins o's unionOfLiteralsOk /
// unionOfLiteralsOutside: `z.enum(["A","B","C"])` is the union of the
// members' codepoint-tuple singletons (z.ts's own enumOf) — judged at
// `.parse`.
func TestParseVocab_EnumOfStringLiterals(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zGrade = z.enum(["A", "B", "C"]);
type Grade = z.infer<typeof zGrade>;
function ok(): Grade { return zGrade.parse("B"); }
function outside(): Grade { return zGrade.parse("F"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zGrade.parse("B")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "outside"), `zGrade.parse("F")`)
}

// TestParseVocab_ClosedLowerRay pins o's atLeastRayOk / atLeastRayBelow:
// `.gte(18)` — the CLOSED ray (refinementsets.AtLeast).
func TestParseVocab_ClosedLowerRay(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zLowerRay = z.number().gte(18);
type LowerRay = z.infer<typeof zLowerRay>;
function ok(): LowerRay { return zLowerRay.parse(18); }
function below(): LowerRay { return zLowerRay.parse(17); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zLowerRay.parse(18)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "below"), "zLowerRay.parse(17)")
}

// TestParseVocab_OpenLowerRay pins o's aboveRayOk / aboveRayAtEndpoint:
// `.gt(0)` — the STRICT ray (refinementsets.Above); the endpoint
// itself is excluded.
func TestParseVocab_OpenLowerRay(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zOpenLowerRay = z.number().gt(0);
type OpenLowerRay = z.infer<typeof zOpenLowerRay>;
function ok(): OpenLowerRay { return zOpenLowerRay.parse(1); }
function atEndpoint(): OpenLowerRay { return zOpenLowerRay.parse(0); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zOpenLowerRay.parse(1)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "atEndpoint"), "zOpenLowerRay.parse(0)")
}

// TestParseVocab_ClosedUpperRay pins o's atMostRayOk / atMostRayAbove:
// `.lte(100)` — the CLOSED ray (refinementsets.AtMost).
func TestParseVocab_ClosedUpperRay(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zUpperRay = z.number().lte(100);
type UpperRay = z.infer<typeof zUpperRay>;
function ok(): UpperRay { return zUpperRay.parse(100); }
function above(): UpperRay { return zUpperRay.parse(101); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zUpperRay.parse(100)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "above"), "zUpperRay.parse(101)")
}

// TestParseVocab_OpenUpperRay pins o's belowRayOk / belowRayAtEndpoint:
// `.lt(10)` — the STRICT ray (refinementsets.Below); the endpoint
// itself is excluded.
func TestParseVocab_OpenUpperRay(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zOpenUpperRay = z.number().lt(10);
type OpenUpperRay = z.infer<typeof zOpenUpperRay>;
function ok(): OpenUpperRay { return zOpenUpperRay.parse(9); }
function atEndpoint(): OpenUpperRay { return zOpenUpperRay.parse(10); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zOpenUpperRay.parse(9)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "atEndpoint"), "zOpenUpperRay.parse(10)")
}

// TestParseVocab_IntegerConjunction pins o's integerConjunctionOk /
// integerConjunctionFractional: `.int()` conjoined with the bounds —
// a fractional argument refuses even inside the numeric window.
func TestParseVocab_IntegerConjunction(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function ok(): Age { return zAge.parse(40); }
function fractional(): Age { return zAge.parse(40.5); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), "zAge.parse(40)")
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "fractional"), "zAge.parse(40.5)")
}

// TestParseVocab_StringLengthWindow pins o's stringLengthWindowOk /
// stringLengthWindowTooShort / stringLengthWindowTooLong: `.min(2).max(6)`
// — REPETITION over codepoints, both edges judged at `.parse`.
func TestParseVocab_StringLengthWindow(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zHandle = z.string().min(2).max(6);
type Handle = z.infer<typeof zHandle>;
function ok(): Handle { return zHandle.parse("abcd"); }
function tooShort(): Handle { return zHandle.parse("a"); }
function tooLong(): Handle { return zHandle.parse("too-long-handle"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zHandle.parse("abcd")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "tooShort"), `zHandle.parse("a")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "tooLong"), `zHandle.parse("too-long-handle")`)
}

// TestParseVocab_RegexCompiledPatternSet pins o's regexSetOk /
// regexSetOutside: `.regex(/^[0-9a-f]+$/)` compiled through
// FormatGrammar, judged at `.parse`.
func TestParseVocab_RegexCompiledPatternSet(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zHex = z.string().min(1).max(6).regex(/^[0-9a-f]+$/);
type Hex = z.infer<typeof zHex>;
function ok(): Hex { return zHex.parse("1a2b"); }
function outside(): Hex { return zHex.parse("zz"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zHex.parse("1a2b")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "outside"), `zHex.parse("zz")`)
}

// TestParseVocab_AnchoredConcatenation pins o's
// anchoredConcatenationOk / anchoredConcatenationOutside:
// `.startsWith("id-")` — the anchored concatenation form
// (refinementsets.StartsWithSet), judged at `.parse`.
func TestParseVocab_AnchoredConcatenation(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAnchored = z.string().min(3).max(10).startsWith("id-");
function ok(): z.infer<typeof zAnchored> { return zAnchored.parse("id-42"); }
function outside(): z.infer<typeof zAnchored> { return zAnchored.parse("no-42"); }
`
	parseVocabWantSilent(t, parseVocabRun(t, kernel, source, "ok"), `zAnchored.parse("id-42")`)
	parseVocabWantRefusedAtArgument(t, parseVocabRun(t, kernel, source, "outside"), `zAnchored.parse("no-42")`)
}
