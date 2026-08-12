// Test helper: a two-file program (entry + a surface stand-in)
// registered through program.CheckerProgram.SurfacePaths, mirroring
// typereading/read_type_test.go's testProgram pattern. The real
// surface module (surface/z.ts) is not ported (service/ tier); this
// stand-in declares the same root-constructor NAMES the TS surface
// exports, which is all CompileAnnotation reads (symbol identity, not
// return types) -- ported tests exercise plain TS sources against
// this stand-in rather than the TS test suite's real z.ts import, per
// PORT.md's testing note.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// surfaceStandIn declares every root-constructor name the schema
// compiler recognizes, each returning `any` -- CompileAnnotation
// never asks the checker for a call's return type, only which SYMBOL
// the receiver identifier resolves to, so the body's shape does not
// matter.
const surfaceStandIn = `
export function number(): any { return 0; }
export function string(): any { return 0; }
export function boolean(): any { return 0; }
export function bigint(): any { return 0; }
export function symbol(): any { return 0; }
export function unknown(): any { return 0; }
export function json(): any { return 0; }
export function date(): any { return 0; }
export function literal(v: any): any { return 0; }
export function enum_(members: any): any { return 0; }
export { enum_ as enum };
export function union(members: any): any { return 0; }
export function intersection(a: any, b: any): any { return 0; }
export function exclude(a: any, b: any): any { return 0; }
export function array(item: any): any { return 0; }
export function tuple(items: any): any { return 0; }
export function object(shape: any): any { return 0; }
export function strictObject(shape: any): any { return 0; }
export function looseObject(shape: any): any { return 0; }
export function ref(target: any): any { return 0; }
export function map(k: any, v: any): any { return 0; }
export function set(v: any): any { return 0; }
export function promise(inner: any): any { return 0; }
export function plainDate(): any { return 0; }
export function plainDateTime(): any { return 0; }
export function plainTime(): any { return 0; }
export function plainYearMonth(): any { return 0; }
export function plainMonthDay(): any { return 0; }
export function instant(): any { return 0; }
export function zonedDateTime(): any { return 0; }
export function duration(): any { return 0; }
export function timeZone(): any { return 0; }
export function calendar(): any { return 0; }
export const iso = { datetime: (): any => 0, date: (): any => 0, time: (): any => 0, duration: (): any => 0 };
export const coerce = { number: (): any => 0 };
export type infer<T> = T;
export type Gte<Name extends string> = any;
export type Gt<Name extends string> = any;
export type Lte<Name extends string> = any;
export type Lt<Name extends string> = any;
`

const surfacePath = "/z.ts"

type testProgram struct {
	t       *testing.T
	program *program.CheckerProgram
}

func newTestProgram(t *testing.T, entrySource string) testProgram {
	t.Helper()
	return newTestProgramWithSource(t, `import * as z from "./z.ts";`+"\n"+entrySource)
}

// newTestProgramWithSource builds a program from the entry source
// VERBATIM -- no forced surface import -- for tests (like the
// shadowing-z case) that specifically exercise the absence of a real
// surface import in scope.
func newTestProgramWithSource(t *testing.T, entrySource string) testProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":  entrySource,
		surfacePath: surfaceStandIn,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts", "z.ts"]
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
	return testProgram{t: t, program: &program.CheckerProgram{
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{surfacePath: true},
	}}
}

func (p testProgram) checker() *checker.Checker { return p.program.Checker }

// newMultiFileTestProgram builds a program from an arbitrary file
// map (each entry a path -> source) plus the surface stand-in,
// entered at /main.ts -- for tests (program_graph.go's ReachableFiles/
// ImportedUserFiles) that need real import edges between user files.
func newMultiFileTestProgram(t *testing.T, files map[string]string) testProgram {
	t.Helper()
	full := map[string]string{surfacePath: surfaceStandIn}
	fileList := []string{"main.ts", "z.ts"}
	for path, source := range files {
		full[path] = source
		fileList = append(fileList, path[1:])
	}
	tsconfig := `{"compilerOptions": {}, "files": [`
	for i, f := range fileList {
		if i > 0 {
			tsconfig += ", "
		}
		tsconfig += `"` + f + `"`
	}
	tsconfig += `]}`
	full["/tsconfig.json"] = tsconfig

	fs := vfstest.FromMap(full, false /*useCaseSensitiveFileNames*/)
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
	return testProgram{t: t, program: &program.CheckerProgram{
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{surfacePath: true},
	}}
}

// namedConstInitializer returns the initializer expression of the
// top-level `const <name> = ...` declaration.
func namedConstInitializer(t *testing.T, p testProgram, name string) *ast.Node {
	t.Helper()
	return namedConstDeclaration(t, p, name).Initializer
}

// namedConstDeclaration returns the top-level `const <name> = ...`
// VariableDeclaration itself, for tests that also need the name
// node's symbol (registering an ObjectAnnotation under it).
func namedConstDeclaration(t *testing.T, p testProgram, name string) *ast.VariableDeclaration {
	t.Helper()
	for _, statement := range p.program.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if ast.IsIdentifier(varDecl.Name()) && varDecl.Name().AsIdentifier().Text == name {
				return varDecl
			}
		}
	}
	t.Fatalf("no const named %s", name)
	return nil
}
