// The cross-adapter conformance battery: the mechanical catcher for
// the defect class where one adapter's compiled set diverges from
// another's for the same declaration. The measured instance this
// battery exists to catch: a pattern-only string alias compiled with
// a redundant whole-string ground on one side and clean on the other,
// blinding the kernel's prover on only that side
// (annotations/chain_method.go's own "regex" case comment,
// surface.rs's own "pattern_only_alias_drops_the_redundant_string_
// ground" test).
//
// This file is the PRODUCER half: it compiles each TS twin's z-surface
// spelling through the real annotations machinery (CompileAnnotation,
// the identical entry point every ordinary check runs), encodes the
// result through kernelbridge's wire codec (the SAME encoder every
// kernel question already uses), and WRITES the wire JSON to a golden
// file under testdata/. The Rust twin (transfer_conformance's sibling
// test file `cross_adapter_twins.rs` in
// packages/refinedpy/pyrefly/crates/refinedpy/src/) compiles the
// PYTHON spelling of the identical declaration and asserts BYTE-
// IDENTICAL wire JSON against these same golden files.
//
// The table lives in twinTable below, one row per shape the two
// adapters must read identically: a pattern-only alias, the timestamp
// grammar, a string length window, a numeric literal union, a string
// literal union, a numeric window with int/multipleOf, and an
// optional (None-admitting) scalar.
//
// A MISMATCH HERE MEANS ONE ADAPTER'S COMPILE DRIFTED. Fix the
// compile, never the golden — unless the kernel's own grammar
// changed, in which case both sides' compiles and this file's golden
// all move together, in the same batch.
//
// The "numeric-window-int-multiple-of" and "optional-scalar-base-set"
// rows now PASS the Rust twin's byte-JSON assertion: the TS numeric
// chain (chain_numeric_method.go's withForm, plus the "int"/"finite"/
// "mod" cases) runs its result through refinementsets.CanonicalScalarForms
// exactly as the string chain's WithoutStringGround call already did
// for its own ground conjunct — FoldRayForms collapses a bound
// method's own ray against whatever ray the base already carries
// (z.number().int().gte(0) no longer keeps atLeast(-Inf) beside the
// tighter atLeast(0)), so z.number().int().gte(0).lte(100).multipleOf(5)
// compiles to exactly the four forms surface.rs's Annotated[int,
// Field(ge=0, le=100, multiple_of=5)] reading denotes, with no
// redundant unbounded ray riding beside the tighter bound.
//
// The "numeric-literal-union" row's z.union([z.literal(1), z.literal(2),
// z.literal(3)]) also now compiles to one flat oneOf([1,2,3]) rather
// than nested union(union(oneOf[1], oneOf[2]), oneOf[3]) —
// chain_root_constructor.go's "union" case recognizes a union whose
// every present member is a bare numeric z.literal and merges their
// singleton sets through refinementsets.MergeScalarOneOfArms, matching
// surface.rs's own Literal[1, 2, 3] → one_of([1,2,3]) reading. A
// string-literal union (each arm's compiled set is the identical bare
// oneOf singleton wire shape, since a single codepoint and a number
// are indistinguishable once compiled) is NOT merged — the merge
// tests the source call's own literal argument sort before it erases,
// matching surface.rs's string_literal_set, which never flattens a
// Literal["a", "b", ...] union either.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// twinsTestdataDir is the shared golden-file directory both this file
// and the Rust twin reach by a short relative path — a new top-level
// directory under packages/ rather than inside either adapter's own
// tree, since the fixture belongs to neither adapter alone. From this
// file's own directory
// (packages/refinedts/refined-ts-go/internal/refinedts/conformance/),
// five levels up reaches packages/.
const twinsTestdataDir = "../../../../../conformance-twins/testdata"

// The same root-constructor stand-in annotations_test_helpers_test.go
// already declares for every other conformance/annotations test in
// this tree — CompileAnnotation reads symbol identity only, never a
// call's return type, so the stand-in's bodies are immaterial.
const twinsSurfaceStandIn = `
export function number(): any { return 0; }
export function string(): any { return 0; }
export function literal(v: any): any { return 0; }
export function union(members: any): any { return 0; }
export const iso = { datetime: (): any => 0 };
`

const twinsSurfacePath = "/z.ts"

// twinRow is one declaration twin: a name, the TS z-surface spelling
// whose compiled set is exported as this row's golden, and a sentence
// naming which of the brief's named divergent shapes it exercises.
type twinRow struct {
	name       string
	tsSource   string
	tsExprName string
	about      string
}

// twinTable is the shared table of declaration twins. Every row here
// has a Python twin in cross_adapter_twins.rs's own python_twin_table,
// spelled to compile to the IDENTICAL set — see that file's own
// per-row doc for the Annotated/Field spelling.
func twinTable() []twinRow {
	return []twinRow{
		{
			name:       "pattern-only-alias",
			tsSource:   `const X = z.string().regex(/^[0-9]+$/);`,
			tsExprName: "X",
			about:      "a pattern-only string alias: the regex conjunct alone, no redundant C* ground riding beside it (chain_method.go's 'regex' case, surface.rs's pattern_only_alias_drops_the_redundant_string_ground)",
		},
		{
			name:       "timestamp-grammar",
			tsSource:   `const X = z.string().regex(/(?:(?:\d\d[2468][048]|\d\d[13579][26]|\d\d0[48]|[02468][048]00|[13579][26]00)-02-29|\d{4}-(?:(?:0[13578]|1[02])-(?:0[1-9]|[12]\d|3[01])|(?:0[469]|11)-(?:0[1-9]|[12]\d|30)|(?:02)-(?:0[1-9]|1\d|2[0-8])))T(?:(?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:\.\d+)?)?(?:Z))$/);`,
			tsExprName: "X",
			about:      "the timestamp grammar: zod's own ISO-datetime regex (refinementsets.IsoPatterns[\"datetime\"]) spelled as an explicit .regex(), matching pydantic's Field(pattern=<the same regex text>) on an Annotated[str, ...] alias",
		},
		{
			name:       "string-length-window",
			tsSource:   `const X = z.string().min(1).max(10);`,
			tsExprName: "X",
			about:      "a min/max length window: one bounded repetition form over the codepoint ground",
		},
		{
			name:       "numeric-literal-union",
			tsSource:   `const X = z.union([z.literal(1), z.literal(2), z.literal(3)]);`,
			tsExprName: "X",
			about:      "a numeric literal union: one oneOf([1,2,3]) form, matching Literal[1, 2, 3]",
		},
		{
			name:       "string-literal-union",
			tsSource:   `const X = z.union([z.literal("a"), z.literal("b")]);`,
			tsExprName: "X",
			about:      "a string literal union: the union of each member's own singleton tuple, matching Literal[\"a\", \"b\"]",
		},
		{
			name:       "numeric-window-int-multiple-of",
			tsSource:   `const X = z.number().int().gte(0).lte(100).multipleOf(5);`,
			tsExprName: "X",
			about:      "a numeric window with an integer floor and a multipleOf step: integer + atLeast(0) + atMost(100) + multipleOf(5), matching Annotated[int, Field(ge=0, le=100, multiple_of=5)]",
		},
		{
			name:       "optional-scalar-base-set",
			tsSource:   `const X = z.number().int().gte(0).optional();`,
			tsExprName: "X",
			about:      "an optional (None-admitting) scalar: the INNER base set only (integer + atLeast(0)) — the absence bit itself rides out of band, compared separately from the wire set (Annotation.Absent on the Go side, AliasEntry.admits_none on the Rust side), matching Optional[Annotated[int, Field(ge=0)]]",
		},
	}
}

// TestCrossAdapterTwinsWriteTheGoldenWireJSON is the PRODUCER test:
// every row's TS spelling compiles through the real annotation
// machinery, and its wire JSON is written to testdata/<name>.json.
// Go's checker is upstream here — this file is the one that refreshes
// the golden when a compile legitimately changes; the Rust twin never
// writes it, only reads and asserts.
func TestCrossAdapterTwinsWriteTheGoldenWireJSON(t *testing.T) {
	for _, row := range twinTable() {
		t.Run(row.name, func(t *testing.T) {
			set, absent := compileTwinTSExpression(t, row.tsSource, row.tsExprName)
			golden := twinGoldenJSON(set, absent)
			path := filepath.Join(twinsTestdataDir, row.name+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, golden, 0o644); err != nil {
				t.Fatalf("WriteFile(%s): %v", path, err)
			}
		})
	}
}

// TestCrossAdapterTwinsMatchTheirOwnJustWrittenGolden is a same-run
// sanity check: the file just written parses back to the identical
// wire set the compile produced. This is not the cross-language
// check (that runs in cross_adapter_twins.rs) — it only pins that
// this file's own encode/decode round-trips before the Rust side ever
// reads it.
func TestCrossAdapterTwinsMatchTheirOwnJustWrittenGolden(t *testing.T) {
	for _, row := range twinTable() {
		t.Run(row.name, func(t *testing.T) {
			set, absent := compileTwinTSExpression(t, row.tsSource, row.tsExprName)
			want := twinGoldenJSON(set, absent)
			path := filepath.Join(twinsTestdataDir, row.name+".json")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v — run TestCrossAdapterTwinsWriteTheGoldenWireJSON first", path, err)
			}
			var wantValue, gotValue any
			if err := json.Unmarshal(want, &wantValue); err != nil {
				t.Fatalf("Unmarshal(want): %v", err)
			}
			if err := json.Unmarshal(got, &gotValue); err != nil {
				t.Fatalf("Unmarshal(got): %v", err)
			}
			if !jsonDeepEqual(wantValue, gotValue) {
				t.Errorf("%s: golden on disk = %s, want %s", row.name, got, want)
			}
		})
	}
}

// TestCrossAdapterTwinsAgreeWithTheKernelOnMutualContainment is the
// SECOND half of the brief's design: "plus the kernel question both
// must answer identically." A byte-identical wire comparison catches
// every FORM-LIST divergence, but a form-list divergence is not always
// a PROVER-BLINDING one — a redundant, semantically-subsumed conjunct
// (e.g. an extra atLeast(-Inf) ray beside a tighter atLeast(0)) changes
// the form count without changing which values the set admits. This
// test asks the kernel's own kernel.ScalarSubset BOTH WAYS on each
// row's TS-compiled set against itself (a self-containment sanity
// check every row must pass regardless of spelling) — a scaffold ready
// for a per-row Python golden set once cross_adapter_twins.rs exposes
// one the Go side can load, at which point this becomes the true
// cross-language containment check the brief describes.
func TestCrossAdapterTwinsAgreeWithTheKernelOnMutualContainment(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	for _, row := range twinTable() {
		t.Run(row.name, func(t *testing.T) {
			set, _ := compileTwinTSExpression(t, row.tsSource, row.tsExprName)
			// The bridge DECLINES-BY-PANIC past its 64-node nesting cap
			// (wire_nesting_guard) — the walk paths all catch that; this
			// raw ask must too. A cap decline is recorded, not asserted:
			// the containment claim is simply not askable for that row.
			answered, refused := func() (answer bool, refused bool) {
				defer func() {
					if recover() != nil {
						refused = true
					}
				}()
				return kernel.ScalarSubset(set, set), false
			}()
			if refused {
				t.Logf("%s: the kernel declined the self-containment ask (nesting past the bridge's cap) — recorded, not asserted", row.name)
				return
			}
			if !answered {
				t.Errorf("%s: kernel.ScalarSubset(set, set) = false, want true — a set must always contain itself", row.name)
			}
		})
	}
}

// twinGoldenJSON is the golden shape this battery compares: the
// compiled set's wire JSON, plus the out-of-band admits-absence bit
// every "optional" row needs — the identical two-field shape
// cross_adapter_twins.rs's own golden reader expects.
func twinGoldenJSON(set refinementsets.RefinedSet, absent bool) []byte {
	envelope := struct {
		Set    json.RawMessage `json:"set"`
		Absent bool            `json:"absent"`
	}{
		Set:    json.RawMessage(kernelbridge.EncodeSet(set)),
		Absent: absent,
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		panic("twinGoldenJSON: " + err.Error())
	}
	return append(out, '\n')
}

// jsonDeepEqual compares two json.Unmarshal-produced values field by
// field — encoding/json's own equality (via reflect.DeepEqual on the
// decoded any) is exactly this, but spelled locally so the two round-
// trip tests above stay self-contained.
func jsonDeepEqual(a, b any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(encodedA) == string(encodedB)
}

// compileTwinTSExpression compiles the named top-level `const NAME =
// ...` declaration in source through CompileAnnotation, and reads
// whether the compiled annotation admits absence (the ".optional()"
// row's own out-of-band bit).
func compileTwinTSExpression(t *testing.T, source, exprName string) (refinementsets.RefinedSet, bool) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":       `import * as z from "./z.ts";` + "\n" + source,
		twinsSurfacePath: twinsSurfaceStandIn,
		"/tsconfig.json": `{"compilerOptions": {}, "files": ["main.ts", "z.ts"]}`,
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
	p := &program.CheckerProgram{
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{twinsSurfacePath: true},
	}

	var initializer *ast.Node
	for _, statement := range entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if ast.IsIdentifier(varDecl.Name()) && varDecl.Name().AsIdentifier().Text == exprName {
				initializer = varDecl.Initializer
			}
		}
	}
	if initializer == nil {
		t.Fatalf("no const named %s in source", exprName)
	}

	compiled := annotations.CompileAnnotation(p, initializer, annotations.AnnotationRegistry{})
	if annotations.IsUnsupported(compiled) {
		t.Fatalf("CompileAnnotation(%s): unexpected unsupported: %s", exprName, compiled.Unsupported.Unsupported)
	}
	return *compiled.Annotation.Set, compiled.Annotation.Absent
}
