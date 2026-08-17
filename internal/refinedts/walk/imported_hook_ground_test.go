// The IMPORTED hook shape, faithfully: recharts' useAppSelector is a
// const ALIAS (`export const useAppSelector: Hook = useSelector`) of a
// bodiless library function, imported across files — so the calling
// file's environment holds the name as an OPAQUE binding, and the
// opaque-callee-root arm of UnmodeledCallResult used to answer Opaque
// before the declared-return ground could speak. The ambient-declare
// mirrors in return_type_worn_call_test.go never caught this: an
// ambient name is not an env-held opaque. These pins build the
// two-file program the corpus actually has.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// twoFileTestProgram is entryEnvTestProgram's two-file twin: /main.ts
// beside /hooks.ts, both in the program, the checker over both.
func twoFileTestProgram(t *testing.T, entrySource string, hooksSource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":  entrySource,
		"/hooks.ts": hooksSource,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts", "hooks.ts"]
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

const importedHookHooksSource = `
	declare function useSelector<T>(selector: (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => T): T;
	export const useAppSelectorish: <T>(selector: (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => T) => T = useSelector;
`

func importedHookDiagnostics(t *testing.T, entrySource string, functionName string) []assignability.RefinementDiagnostic {
	t.Helper()
	kernel := superArrayLoadKernel(t)
	p := twoFileTestProgram(t, entrySource, importedHookHooksSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	var diagnostics []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	declaration := entryEnvFunctionNamed(t, p, functionName)
	AnalyzeFunction(ctx, &FunctionContract{Declaration: declaration}, nil)
	return diagnostics
}

// TestImportedHookGround_ReturnPositionDetermines pins the
// usePolarChartLayout / useTooltipEventType shape: a cross-file
// imported const-alias hook called directly in return position, its
// instantiated type the selector's own union.
func TestImportedHookGround_ReturnPositionDetermines(t *testing.T) {
	diagnostics := importedHookDiagnostics(t, `
		import { useAppSelectorish } from './hooks';
		const selectPolarish = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }): 'centric' | 'radial' | undefined => {
			const layout = state.layout;
			if (layout === 'centric' || layout === 'radial') {
				return layout;
			}
			return undefined;
		};
		export function usePolarish(): 'centric' | 'radial' | undefined {
			return useAppSelectorish(selectPolarish);
		}
	`, "usePolarish")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("usePolarish fired 7002 (%s) — the imported hook's instantiated union should wear through the opaque binding", d.MessageText)
		}
	}
}

// TestImportedHookGround_NarrowedDeclarationDetermines pins the
// ErrorBar shape: the imported hook's result feeds a narrowing and an
// annotated local.
func TestImportedHookGround_NarrowedDeclarationDetermines(t *testing.T) {
	diagnostics := importedHookDiagnostics(t, `
		import { useAppSelectorish } from './hooks';
		const selectLayoutish = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		function useDirectionish(d: 'x' | 'y' | undefined): 'x' | 'y' {
			const layout = useAppSelectorish(selectLayoutish);
			if (d != null) {
				return d;
			}
			if (layout != null) {
				return layout === 'horizontal' ? 'y' : 'x';
			}
			return 'x';
		}
		export function componentish(d: 'x' | 'y' | undefined): 'x' | 'y' {
			const realDirection: 'x' | 'y' = useDirectionish(d);
			return realDirection;
		}
	`, "componentish")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("componentish fired 7002 (%s) — the imported hook's union should serve through the same-file inline", d.MessageText)
		}
	}
}

// entryEnvArrowConstNamed finds a const-bound arrow by its declared
// name — the corpus spells its hooks as arrow consts.
func entryEnvArrowConstNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			vd := declaration.AsVariableDeclaration()
			name := vd.Name()
			if name == nil || !ast.IsIdentifier(name) || name.Text() != text {
				continue
			}
			if vd.Initializer != nil && ast.IsArrowFunction(vd.Initializer) {
				return vd.Initializer
			}
		}
	}
	t.Fatalf("no const-bound arrow named %s in the entry source", text)
	return nil
}

// TestImportedHookGround_ArrowConstReturnPositionDetermines pins the
// exact corpus spelling: the enclosing hook is itself an ARROW CONST
// (`export const usePolarChartLayout = (): PolarLayout | undefined =>
// { return useAppSelector(...); }`).
func TestImportedHookGround_ArrowConstReturnPositionDetermines(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := twoFileTestProgram(t, `
		import { useAppSelectorish } from './hooks';
		const selectPolarish = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }): 'centric' | 'radial' | undefined => {
			const layout = state.layout;
			if (layout === 'centric' || layout === 'radial') {
				return layout;
			}
			return undefined;
		};
		export const usePolarish = (): 'centric' | 'radial' | undefined => {
			return useAppSelectorish(selectPolarish);
		};
	`, importedHookHooksSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	var diagnostics []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	declaration := entryEnvArrowConstNamed(t, p, "usePolarish")
	AnalyzeFunction(ctx, &FunctionContract{Declaration: declaration}, nil)
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("usePolarish (arrow const) fired 7002 (%s) — the imported hook's instantiated union should wear through return position", d.MessageText)
		}
	}
}
