// A DIAGNOSTIC CAPTURE, not a pin — coordinator-requested: the Lean
// lane's alignedSegSubsetB now PROVES the probe test's own operand
// pair (timestamp_operand_probe_test.go, owned by another lane and
// left untouched here), yet the live gauge (pnpm ts:check on
// g-strings-and-formats.ts) still reports RTS7002 "The kernel declined
// the question" at timestampViaPython's return leg (rows 200:28 and
// 201:10). Both the dylib and refinedts-check-bin are freshly built —
// staleness is ruled out — so the LIVE ask must differ from the
// probe's. This file drives the REAL g-strings-and-formats.ts shape
// (the real execFileSync crossing to the real targets/text_timestamp.py,
// whose already-exported .refined.json this test reads off disk
// unchanged — the same cache entry ReadForeignArtifact/the probe both
// read) through the REAL pipeline — CompileAnnotationFileFacts +
// CompileContractFileFacts + AnalyzeFunction, exactly runRefinements'
// own pass order (service/check.go) — to the exact return-leg
// CheckWornSet ask (worn_set_membership.go's ctx.Kernel.SeqSubset
// call), and prints/asserts the two operands, which kernel entry
// point is asked, and its verbatim answer.
package walk

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// gTimestampSurfaceStandIn is parse_and_chain_vocabulary_test.go's own
// parseVocabSurfaceStandIn: CompileAnnotation reads only the root
// constructor NAMES and the chain method names/argument shapes (never
// a method's runtime body), so this stand-in compiles zTimestamp's
// `z.string().regex(...)` chain through the IDENTICAL annotations/
// chain_method.go code path the real refined-ts-typescript/surface/z.ts
// does — the real g-strings-and-formats.ts imports the real surface
// file, but nothing in the annotation compile reads past its type
// signatures, so the two are compile-equivalent for this capture.
const gTimestampSurfaceStandIn = `
export interface AnyChain {
  min(a: number): AnyChain;
  max(a: number): AnyChain;
  regex(pattern: RegExp): AnyChain;
}
export function string(): AnyChain { return {} as AnyChain; }
export type infer<T> = any;
`

// gTimestampChildProcessDeclaration mirrors foreign_edge_listwalk_test.go's
// own childProcessDeclaration — the one execFileSync signature this
// row's call needs.
const gTimestampChildProcessDeclaration = `
declare module "child_process" {
  export function execFileSync(
    file: string,
    args?: readonly string[],
    options?: { input?: string; encoding?: string }
  ): string | Buffer;
}
`

// gTimestampProgram stands up a program carrying BOTH the real
// child_process.d.ts placement (resolvesToChildProcessMember's own
// declaring-file test, exactly as foreign_edge_listwalk_test.go's
// listWalkTestProgramWithNodeTypes builds it) and the z surface
// stand-in above (registered as a real SurfacePath) — the union
// neither existing helper carries alone, since the diamond/loop
// listwalk tests never needed a zod-refined return type and the
// parse-vocabulary tests never needed a real cross-language call.
//
// mainPath sits INSIDE the real edge-coverage fixture directory
// itself (a real OS path, not a synthetic t.TempDir() root) so the
// row's own relative argv element ("./targets/text_timestamp.py")
// resolves to the SAME real .py file the live gauge reads, and
// ReadForeignArtifact's project-root walk lands on this repo's own
// .git — reading the REAL, already-exported
// .refined/cache/.../text_timestamp.py.refined.json, unchanged.
func gTimestampProgram(t *testing.T, mainPath string, entrySource string) *program.CheckerProgram {
	t.Helper()
	nodeTypesDir := filepath.Join(filepath.Dir(mainPath), "node_modules", "@types", "node")
	surfacePath := filepath.Join(filepath.Dir(mainPath), "surface", "z.ts")
	tsconfigPath := filepath.Join(filepath.Dir(mainPath), "tsconfig.gtimestamp.json")
	fs := vfstest.FromMap(map[string]string{
		mainPath:    entrySource,
		surfacePath: gTimestampSurfaceStandIn,
		filepath.Join(nodeTypesDir, "child_process.d.ts"): gTimestampChildProcessDeclaration,
		filepath.Join(nodeTypesDir, "index.d.ts"):          `/// <reference path="child_process.d.ts" />` + "\n",
		filepath.Join(nodeTypesDir, "package.json"):        `{"name": "@types/node", "version": "1.0.0", "types": "index.d.ts"}`,
		tsconfigPath: `{
			"compilerOptions": {"types": ["node"]},
			"files": [` + strconv.Quote(mainPath) + `, ` + strconv.Quote(surfacePath) + `]
		}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost(filepath.Dir(mainPath), fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile(tsconfigPath, &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig.gtimestamp.json: %v", errors)
	}
	compilerProgram := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	compilerProgram.BindSourceFiles()
	c, done := compilerProgram.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := compilerProgram.GetSourceFile(mainPath)
	if entry == nil {
		t.Fatalf("no entry source file at %s", mainPath)
	}
	return &program.CheckerProgram{
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{surfacePath: true},
	}
}

// capturedSeqSubsetAsk is one kernel.SeqSubset call this test's
// wrapped kernel field recorded: the two operands as wired, and the
// verbatim answer (or PANIC, this codebase's own "declined" signal
// for a question the kernel's decider does not read).
type capturedSeqSubsetAsk struct {
	a, b     refinementsets.RefinedSet
	answer   bool
	declined bool
}

// TestGTimestampLiveAsk_CapturesTheRealReturnLegSeqSubsetOperands drives
// timestampViaPython's own shape (execFileSync → text_timestamp.py,
// `const level: Timestamp = JSON.parse(stdout); return level;`) through
// CompileAnnotationFileFacts + CompileContractFileFacts + AnalyzeFunction
// — runRefinements' own real pass order — with the loaded kernel's
// SeqSubset field wrapped to record every (a, b) pair asked and its
// answer before delegating to the real native call. Reports the exact
// live A/B (compact summaries), which entry point fired (SeqSubset,
// the sequence-shape route CheckWornSet takes once the artifact's own
// Repeat/Concatenation shape fails OnOneTupleLayer), and the kernel's
// verbatim answer — then names the first difference from the probe
// test's own hand-built operand pair.
func TestGTimestampLiveAsk_CapturesTheRealReturnLegSeqSubsetOperands(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	var captured []capturedSeqSubsetAsk
	realSeqSubset := kernel.SeqSubset
	kernel.SeqSubset = func(a, b refinementsets.RefinedSet) (answer bool) {
		entry := capturedSeqSubsetAsk{a: a, b: b}
		defer func() {
			if r := recover(); r != nil {
				entry.declined = true
				captured = append(captured, entry)
				panic(r) // the real ask-site's own recover() still needs this
			}
		}()
		answer = realSeqSubset(a, b)
		entry.answer = answer
		captured = append(captured, entry)
		return answer
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)

	// the real edge-coverage fixture directory — the SAME directory the
	// live gauge's own g-strings-and-formats.ts sits in, so the row's
	// relative argv element resolves to the REAL text_timestamp.py and
	// ReadForeignArtifact's project-root walk lands on this repo's own
	// .git, reading the REAL already-exported .refined.json unchanged.
	fixtureDir := "/Users/jtmenchaca/TypeRefinery/packages/refinedts/tsc-vscode/fixtures/language/edge-coverage"
	mainPath := filepath.Join(fixtureDir, "g_timestamp_live_ask_capture_entry.ts")
	entrySource := `import { execFileSync } from "child_process";
import * as z from "./surface/z.ts";

const zTimestamp = z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
type Timestamp = z.infer<typeof zTimestamp>;

function timestampViaPython(): Timestamp {
  const year = 2024;
  const stdout = execFileSync("python3", ["./targets/text_timestamp.py"], {
    input: JSON.stringify(year),
    encoding: "utf8",
  });
  const level: Timestamp = JSON.parse(stdout);
  return level;
}
`
	p := gTimestampProgram(t, mainPath, entrySource)

	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	var annotationDiagnostics []assignability.RefinementDiagnostic
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, true, func(d assignability.RefinementDiagnostic) { annotationDiagnostics = append(annotationDiagnostics, d) })
	for _, d := range annotationDiagnostics {
		t.Logf("annotation-pass diagnostic: RTS%d %s", d.Code, d.MessageText)
	}

	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, "timestampViaPython")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for timestampViaPython")
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for timestampViaPython")
	}
	if !contract.Grounded {
		t.Fatalf("timestampViaPython's contract is UNGROUNDED — the Timestamp return annotation did not compile to a stated set; " +
			"nothing would be judged at the return, which is itself a candidate ask-site defect distinct from a kernel refusal")
	}

	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    kernel,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	var sink []interface{}
	_ = sink
	AnalyzeFunction(ctx, contract, nil)

	for _, d := range reported {
		t.Logf("return-leg diagnostic: RTS%d %s", d.Code, d.MessageText)
	}
	if len(captured) == 0 {
		t.Fatalf("NO kernel.SeqSubset ask was ever made — the return leg never reached the sequence-subset route at all " +
			"(hypothesis 2: the flowing value is wrapped/routed away before the ask; check ScalarSubset asks too, and whether " +
			"the crossing bound a value at all)")
	}

	// the LAST captured ask is the one this return statement's own
	// CheckWornSet call made (an earlier ask, if any, belongs to a
	// different position this same AnalyzeFunction run judged, e.g. the
	// `const level: Timestamp = JSON.parse(stdout)` variable declaration
	// itself, which judges the identical pair a second time before the
	// return statement re-judges it).
	last := captured[len(captured)-1]
	t.Logf("kernel entry point asked: SeqSubset (CheckWornSet, worn_set_membership.go:136 — known.Set is not OnOneTupleLayer, so the scalar route never runs)")
	t.Logf("live operand A (flowing set, %d asks total): %s", len(captured), refinementsets.FormatForDiagnostics(last.a))
	t.Logf("live operand B (declared set):                %s", refinementsets.FormatForDiagnostics(last.b))
	if last.declined {
		t.Logf("kernel.SeqSubset(A, B) = DECLINED (panicked)")
	} else {
		t.Logf("kernel.SeqSubset(A, B) = %v", last.answer)
	}

	// the probe's own operand B, built the SAME way FormatGrammar builds
	// it — bare, no base chain forms prepended — for a byte-for-byte
	// diff against the live B this test just captured.
	probePattern := `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`
	probeCompiled := refinementsets.FormatGrammar(probePattern, "")
	if !probeCompiled.Ok {
		t.Fatalf("FormatGrammar(%q): unsupported — %s", probePattern, probeCompiled.Unsupported)
	}
	probeOperandB := probeCompiled.Set
	t.Logf("probe operand B (FormatGrammar alone):        %s", refinementsets.FormatForDiagnostics(probeOperandB))

	liveBFormCount := len(last.b.Forms)
	probeBFormCount := len(probeOperandB.Forms)
	if liveBFormCount != probeBFormCount {
		t.Logf("FIRST DIFFERENCE FROM THE PROBE: live operand B carries %d top-level form(s), the probe's hand-built "+
			"operand B carries %d — chain_method.go's \"regex\" case (annotations/chain_method.go) builds "+
			"MakeRefinedSet(append(base.Forms, compiled.Set.Forms...)...), prepending the chain ROOT's own base "+
			"forms (z.string() compiles to Star(Codepoints) — refinementsets/codepoint_sets.go's `Strings` — "+
			"BEFORE .regex(...) narrows it) ahead of the grammar's own forms, where the probe's operandB is "+
			"FormatGrammar's output ALONE, with no base form prepended at all",
			liveBFormCount, probeBFormCount)
	} else {
		t.Logf("live and probe operand B carry the SAME form count (%d) — the base-form-prepend hypothesis is not the difference; "+
			"the first difference lies elsewhere (compare the logged summaries above form by form)",
			liveBFormCount)
	}
}
