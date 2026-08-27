// Ports control_flow/entry_env.test.ts. Interface tests for entry
// env: stated, call-site join, plain type. Hover and judgment both
// bind through BindEntryEnv — a row here is a disagreement that
// cannot happen.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// entryEnvTestProgram mirrors the TS test's programFromSource,
// following typereading/read_type_test.go's programFromSource /
// call_site_bindings_test.go's callSiteTestProgram pattern (PORT.md's
// canonical recipe). No surface stand-in is needed here.
func entryEnvTestProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	return entryEnvTestProgramWithOptions(t, entrySource, "")
}

// entryEnvTestProgramWithOptions is entryEnvTestProgram with the
// tsconfig's compilerOptions body spelled by the caller — the flag
// gates a walk answer (element_in_bounds.go reads
// noUncheckedIndexedAccess), so a test of that answer has to state the
// project the answer belongs to. optionsBody is the comma-separated
// interior of the compilerOptions object; "" is the empty object
// entryEnvTestProgram has always used.
func entryEnvTestProgramWithOptions(t *testing.T, entrySource string, optionsBody string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts": entrySource,
		"/tsconfig.json": `{
			"compilerOptions": {` + optionsBody + `},
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

// entryEnvFunctionNamed mirrors the TS test's functionNamed: the
// top-level FunctionDeclaration named text.
func entryEnvFunctionNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
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

func TestBindEntryEnv_APlainBooleanArrayParameterWearsTheStar(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(xs: boolean[]) { xs; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, _ := env.Get("xs")
	formatted, ok := abstractdomain.FormatAbstractValue(held)
	// the ruled container grammar (old wording, pre restructure:
	// "{each 0 | 1}")
	if !ok || formatted != "Array<number {0 | 1}>" {
		t.Errorf("env[xs] = %q, %v, want %q, true", formatted, ok, "Array<number {0 | 1}>")
	}
}

func TestBindEntryEnv_ACallSiteJoinOutranksThePlainType(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(xs: boolean[]) { xs; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:            p,
		Env:          env,
		Parameters:   fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams: nil,
		CallSiteInitialStates: map[string]abstractdomain.AbstractValue{
			"xs": abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved),
		},
	})
	held, _ := env.Get("xs")
	formatted, ok := abstractdomain.FormatAbstractValue(held)
	if !ok || formatted != "true" {
		t.Errorf("env[xs] = %q, %v, want %q, true", formatted, ok, "true")
	}
}

func TestBindEntryEnv_AnEnclosingPinOnTheEnvIsKeptWhenNothingBetterArrives(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(xs: boolean[]) { xs; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	env.Set("xs", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved))
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, _ := env.Get("xs")
	formatted, ok := abstractdomain.FormatAbstractValue(held)
	if !ok || formatted != "false" {
		t.Errorf("env[xs] = %q, %v, want %q, true", formatted, ok, "false")
	}
}

func TestBindEntryEnv_AnExportedAnyParameterIsOpaque(t *testing.T) {
	p := entryEnvTestProgram(t, "export function f(x: any) { x; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, _ := env.Get("x")
	if held.Kind != abstractdomain.KindUnknown {
		t.Fatalf("held.Kind = %v, want KindUnknown", held.Kind)
	}
	if !held.Opaque {
		t.Errorf("held.Opaque = false, want true")
	}
}

// TestBindEntryEnv_APlainUnknownParameterRecordsAWrittenTouch pins the
// entry-binding chokepoint (A12.sink.assign's own gap): a plain
// `v: unknown` parameter enters its body a bare unknown with no
// reader's sentence attached, and nothing ever narrows it before a read
// — so the read finds no last-touch record and falls back to the bare
// "the walk holds nothing that pins this value" alert, naming the
// reader and nothing about why. BindEntryEnv's own bindParam now
// records the declaration itself as the write that produced that
// unbounded value, while a Recorder is active, so a later read can name
// it instead.
func TestBindEntryEnv_APlainUnknownParameterRecordsAWrittenTouch(t *testing.T) {
	_, closer := derivation.BeginRecording("ts", "f.ts:1", 0)
	defer closer()

	p := entryEnvTestProgram(t, "function f(v: unknown) { v; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, ok := env.Get("v")
	if !ok || held.Kind != abstractdomain.KindUnknown || held.Opaque || held.ResidueReason != "" {
		t.Fatalf("env[v] = %+v, %v, want a bare unopaque unknown with no residue of its own", held, ok)
	}
	// The seed names its own site — the parameter declaration and its
	// range — rather than the bare kind word, which is what lets a later
	// read of `v` say WHERE the unbounded value came from.
	touch := derivation.LastTouchOf("v")
	if !strings.HasPrefix(touch, "written by v: unknown  @") {
		t.Fatalf("LastTouchOf(v) = %q, want the written kind naming the parameter declaration", touch)
	}
	if !strings.Contains(touch, ":1:12-1:22") {
		t.Fatalf("LastTouchOf(v) = %q, want the parameter declaration's own range", touch)
	}
}
