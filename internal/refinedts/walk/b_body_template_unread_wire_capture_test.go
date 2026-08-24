// A DIAGNOSTIC CAPTURE, not a pin — coordinator-requested, read-only:
// b-body-expressions.ts's templateUnreadSubstitution (row 569) reports
// RTS7002 "The kernel declined the question" even though the ScalarSubset/
// SeqSubset routing for Concatenation(Word "n=", Star) ⊆ Repeat(_, 0, 8)
// refutes correctly when asked in isolation (kernel_bridge_test.go-style
// direct calls) — so the WIRE the adapter actually sends for this row's
// live ask must differ from that isolated shape. This test drives the real
// row through the real pipeline (CompileAnnotationFileFacts +
// CompileContractFileFacts + AnalyzeFunction, the same order runRefinements
// uses) with kernel.SeqSubset wrapped to record every (a, b) pair, and logs
// the two operands' VERBATIM wire JSON (kernelbridge.EncodeSet — the same
// encoder the real ask site calls) rather than a formatted summary. No fix
// lives here: the Lean lane extends seqWindowExact once it has the real
// shape.
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
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// bBodyWireCaptureProgram mirrors g_timestamp_live_ask_capture_test.go's
// gTimestampProgram, minus the child_process stand-in this row does not
// need: a synthetic vfs carrying the entry source and the z surface
// stand-in, with the surface path registered so annotation compilation
// recognizes it.
func bBodyWireCaptureProgram(t *testing.T, mainPath, surfacePath, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		mainPath:    entrySource,
		surfacePath: bBodySurfaceStandIn,
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

// bBodySurfaceStandIn mirrors gTimestampSurfaceStandIn
// (g_timestamp_live_ask_capture_test.go): CompileAnnotation reads only
// the root constructor NAMES and the chain method names/argument
// shapes, never a runtime body, so this stand-in compiles
// `z.string().min(1).max(8)` through the identical annotations/
// chain_method.go path the real surface/z.ts chain does.
const bBodySurfaceStandIn = `
export interface AnyChain {
  min(a: number): AnyChain;
  max(a: number): AnyChain;
  int(): AnyChain;
}
export function string(): AnyChain { return {} as AnyChain; }
export type infer<T> = any;
`

// capturedBBodySeqSubsetAsk mirrors g_timestamp_live_ask_capture_test.go's
// capturedSeqSubsetAsk.
type capturedBBodySeqSubsetAsk struct {
	a, b     refinementsets.RefinedSet
	answer   bool
	declined bool
}

// TestBBodyTemplateUnreadSubstitution_CapturesTheLiveSeqSubsetWireStrings
// drives templateUnreadSubstitution's own shape (`` `n=${unreadNumber()}` ``
// returned against `Label = z.string().min(1).max(8)`) through the real
// pipeline, wraps kernel.SeqSubset to record every wire-encoded (a, b)
// pair, and logs the two operands' VERBATIM EncodeSet JSON strings for
// the ask CheckWornSet makes (worn_set_membership.go:136).
func TestBBodyTemplateUnreadSubstitution_CapturesTheLiveSeqSubsetWireStrings(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	var captured []capturedBBodySeqSubsetAsk
	realSeqSubset := kernel.SeqSubset
	kernel.SeqSubset = func(a, b refinementsets.RefinedSet) (answer bool) {
		entry := capturedBBodySeqSubsetAsk{a: a, b: b}
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

	entrySource := `import * as z from "./surface/z.ts";

const zLabel = z.string().min(1).max(8);
type Label = z.infer<typeof zLabel>;

declare function unreadNumber(): number;

function templateUnreadSubstitution(): Label {
  return ` + "`n=${unreadNumber()}`" + `;
}
`
	p := bBodyWireCaptureProgram(t, "/main.ts", "/surface/z.ts", entrySource)

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
	fn := entryEnvFunctionNamed(t, p, "templateUnreadSubstitution")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for templateUnreadSubstitution")
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for templateUnreadSubstitution")
	}
	if !contract.Grounded {
		t.Fatalf("templateUnreadSubstitution's contract is UNGROUNDED — the Label return annotation did not compile to a stated set")
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

	AnalyzeFunction(ctx, contract, nil)

	for _, d := range reported {
		t.Logf("return-leg diagnostic: RTS%d %s", d.Code, d.MessageText)
	}
	if len(captured) == 0 {
		t.Fatalf("NO kernel.SeqSubset ask was ever made — the return leg never reached the sequence-subset route at all")
	}

	last := captured[len(captured)-1]
	t.Logf("kernel entry point asked: SeqSubset (CheckWornSet, worn_set_membership.go:136)")
	t.Logf("live operand A wire JSON (EncodeSet, %d asks total): %s", len(captured), kernelbridge.EncodeSet(last.a))
	t.Logf("live operand B wire JSON (EncodeSet):                %s", kernelbridge.EncodeSet(last.b))
	if last.declined {
		t.Logf("kernel.SeqSubset(A, B) = DECLINED (panicked)")
	} else {
		t.Logf("kernel.SeqSubset(A, B) = %v", last.answer)
	}
}
