// Interface tests for type reading: both adapters and the join.
// A form the join misses is a false unknown.

package typereading

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// testProgram stands in for the TS test's programFromSource: an
// in-memory single-file program with its checker. Unlike the TS
// source's CheckerProgram, there is no separate host/surface layer to
// build -- the checker IS the host, in-process (PORT.md's adapter
// rule), so this helper builds the plain tsgo compiler.Program and
// hands back its checker and entry file directly.
type testProgram struct {
	checker *checker.Checker
	entry   *ast.SourceFile
}

func programFromSource(t *testing.T, source string) testProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts": source,
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
	p := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	p.BindSourceFiles()
	c, done := p.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := p.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	return testProgram{checker: c, entry: entry}
}

// identifierIn is identifierIn in the TS test: the which-th identifier
// named text, walked in source order.
func identifierIn(t *testing.T, p testProgram, text string, which int) *ast.Node {
	t.Helper()
	seen := 0
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsIdentifier(node) && node.AsIdentifier().Text == text {
			if seen == which {
				found = node
				return true
			}
			seen++
		}
		node.ForEachChild(visit)
		return found != nil
	}
	p.entry.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no identifier named %s", text)
	}
	return found
}

// parameterNamed is parameterNamed in the TS test.
func parameterNamed(t *testing.T, p testProgram, text string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsParameterDeclaration(node) && ast.IsIdentifier(node.Name()) && node.Name().AsIdentifier().Text == text {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	p.entry.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no parameter %s", text)
	}
	return found
}

func hostAt(p testProgram, token *ast.Node) (abstractdomain.AbstractValue, bool) {
	return ReadHostType(p.checker, p.checker.GetTypeAtLocation(token), token, 0)
}

func TestReadHostType_NumberArrayIsAStarOfNumbersWithNaNOnTheElements(t *testing.T) {
	p := programFromSource(t, "export function f(xs: number[]): void { console.log(xs); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "xs", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set, got %v", worn.Kind)
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	if !worn.NaNElements {
		t.Errorf("NaNElements = false, want true")
	}
}

func TestReadHostType_StringArrayIsAStarOfStringsNoNaNMark(t *testing.T) {
	p := programFromSource(t, "export function f(xs: string[]): void { console.log(xs); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "xs", 1))
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	if worn.NaNElements {
		t.Errorf("NaNElements = true, want false")
	}
}

func TestReadHostType_AnArrayArmNoLongerDissolvesItsUnion(t *testing.T) {
	p := programFromSource(t, "export function f(v: string | number[]): void { console.log(v); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "v", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindKindUnion {
		t.Errorf("Kind = %v, want kindUnion", worn.Kind)
	}
}

func TestReadHostType_ATemplateLiteralTypeIsItsConcatenation(t *testing.T) {
	p := programFromSource(t, "export function f(t: `px-${string}`): void { console.log(t); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "t", 1))
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "concatenation" {
		t.Errorf("Set.Forms[0].Form = %v, want concatenation", worn.Set.Forms[0].Form)
	}
}

func TestReadHostType_ATypeParameterReadsItsConstraint(t *testing.T) {
	p := programFromSource(t, "export function f<T extends string>(x: T): void { console.log(x); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "x", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSet {
		t.Errorf("Kind = %v, want set", worn.Kind)
	}
}

func TestReadHostType_AnUnconstrainedTypeParameterClaimsNothing(t *testing.T) {
	p := programFromSource(t, "export function f<T>(x: T): void { console.log(x); }\n")
	if _, ok := hostAt(p, identifierIn(t, p, "x", 1)); ok {
		t.Errorf("expected no value")
	}
}

func TestReadHostType_ANumberLiteralTypeReadsItsOwnValue(t *testing.T) {
	// tsgo stores a literal's value as jsnum.Number, a NAMED float64:
	// the plain float64 assertion this used to make never matched, so
	// every number literal type -- enum members included -- read
	// nothing.
	p := programFromSource(t, "export function f(n: 7): void { console.log(n); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "n", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindValues || len(worn.Values) != 1 || worn.Values[0] != 7 {
		t.Errorf("Kind = %v, Values = %v, want values [7]", worn.Kind, worn.Values)
	}
}

func TestReadHostType_AnEnumTypedPositionReadsItsMemberValues(t *testing.T) {
	p := programFromSource(t, "export enum M { GET = 0, POST, PUT }\nexport function f(m: M): void { console.log(m); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "m", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	words, hasWords := abstractdomain.FormatAbstractValue(worn)
	if !hasWords || words != "{(0, 1, 2)}" {
		t.Errorf("FormatAbstractValue = %q, %v, want %q, true", words, hasWords, "{(0, 1, 2)}")
	}
}

func TestReadHostType_AStringEnumTypedPositionReadsItsMemberWords(t *testing.T) {
	p := programFromSource(t, "export enum S { A = 'a', B = 'b' }\nexport function f(s: S): void { console.log(s); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "s", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSet {
		t.Errorf("Kind = %v, want set", worn.Kind)
	}
}

func TestReadHostType_ADepthCutUnionArmJoinsAtItsSortGround(t *testing.T) {
	// the inner arms sit past the union branch's depth: each still
	// names a sort, so the union answers its arms rather than nothing
	p := programFromSource(t, "type A = 'a' | 'b';\ntype B = A | 'c';\ntype C = B | 'd';\ntype D = C | 'e';\nexport function g(v: number | D): void { console.log(v); }\n")
	if _, ok := hostAt(p, identifierIn(t, p, "v", 1)); !ok {
		t.Errorf("expected a value")
	}
}

func TestReadHostType_AMaybeArrayTwoMembersDeepStillReadsItsElement(t *testing.T) {
	// state.options.list: object -> object -> union(present/absent) ->
	// array-like -> element union — five hops under the shared depth
	// counter, an ordinary shape with nothing recursive about it
	p := programFromSource(t, `export function f(state: {
		options: { list: ReadonlyArray<"axis" | "item"> | undefined };
	}): void { console.log(state); }
	`)
	worn, ok := hostAt(p, identifierIn(t, p, "state", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindObject {
		t.Fatalf("Kind = %v, want object", worn.Kind)
	}
	var optionsValue abstractdomain.AbstractValue
	found := false
	for _, k := range worn.Keys {
		if k.Name == "options" {
			optionsValue = k.Value
			found = true
		}
	}
	if !found {
		t.Fatalf("Keys = %v, want an options member", worn.Keys)
	}
	if optionsValue.Kind != abstractdomain.KindObject {
		t.Fatalf("options Kind = %v, want object — the depth gate cut the read before this", optionsValue.Kind)
	}
	listFound := false
	for _, k := range optionsValue.Keys {
		if k.Name == "list" {
			listFound = true
		}
	}
	if !listFound {
		t.Errorf("options Keys = %v, want a list member", optionsValue.Keys)
	}
}

func TestReadHostType_AnArmNamingNoSortStillDissolvesItsUnion(t *testing.T) {
	// a record arm has no widest sort reading, and the union of a
	// stated set with the unknown IS the unknown
	p := programFromSource(t, "type Odd = { a: { b: { c: { d: { e: string } } } } };\nexport function f(v: string | Odd): void { console.log(v); }\n")
	if _, ok := hostAt(p, identifierIn(t, p, "v", 1)); ok {
		t.Errorf("expected no value")
	}
}

func TestReadHostType_ALibDeclaredRecordSeedsItsReadableKeys(t *testing.T) {
	p := programFromSource(t, "export function f(e: Error): void { console.log(e); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "e", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindObject {
		t.Fatalf("Kind = %v, want object", worn.Kind)
	}
	if worn.Complete {
		t.Errorf("Complete = true, want false — a host record names keys it does not spell")
	}
	seen := map[string]bool{}
	for _, k := range worn.Keys {
		seen[k.Name] = true
	}
	if !seen["message"] || !seen["name"] {
		t.Errorf("Keys = %v, want message and name among them", worn.Keys)
	}
}

// TestReadHostType_AFixedTupleReadsEachPositionExactly pins the gap
// behind c-reads-and-values.ts's ternarySpreadCopiesNullableArray
// (item: [10, 20] | null): before this fix, a fixed tuple type fell
// into the SAME IsArrayLikeType branch a plain `number[]` uses — every
// element type joined into ONE star claim (length unstated, every
// position possibly either member). A tuple's own per-position
// exactness (`item[0]` is ALWAYS 10, never 20) was lost, so narrowing
// `item` under `!= null` and spreading it left the copy only a star,
// not the exact two-item list. The new branch (host_type.go, checked
// before the general array-like one) reads a tuple whose every element
// is ElementFlagsRequired positionally instead, building an exact
// KindList.
func TestReadHostType_AFixedTupleReadsEachPositionExactly(t *testing.T) {
	p := programFromSource(t, "export function f(item: [10, 20]): void { console.log(item); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "item", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindList {
		t.Fatalf("Kind = %v, want list (an exact per-position tuple)", worn.Kind)
	}
	if len(worn.Items) != 2 {
		t.Fatalf("Items = %v, want exactly 2 positions", worn.Items)
	}
	if worn.Items[0].Kind != abstractdomain.KindValues || len(worn.Items[0].Values) != 1 || worn.Items[0].Values[0] != 10 {
		t.Errorf("Items[0] = %v, want the exact scalar 10", worn.Items[0])
	}
	if worn.Items[1].Kind != abstractdomain.KindValues || len(worn.Items[1].Values) != 1 || worn.Items[1].Values[0] != 20 {
		t.Errorf("Items[1] = %v, want the exact scalar 20", worn.Items[1])
	}
}

// TestReadHostType_AFixedTupleBehindNullReadsExactlyOnceNarrowed pins
// the exact fixture shape: a `[10, 20] | null` parameter reads as the
// maybe-wrapped exact tuple, not a maybe-wrapped star — the wrapper's
// Inner is what a `!= null` guard hands back (narrowAt's "defined"
// case, apply_narrowing.go), and only an exact Inner lets the spread
// that follows carry the exact per-position values through.
func TestReadHostType_AFixedTupleBehindNullReadsExactlyOnceNarrowed(t *testing.T) {
	p := programFromSource(t, "export function f(item: [10, 20] | null): void { console.log(item); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "item", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindPossiblyUndefined || worn.Inner == nil {
		t.Fatalf("Kind = %v, want the maybe-absent wrapper (null in the union)", worn.Kind)
	}
	if worn.Inner.Kind != abstractdomain.KindList || len(worn.Inner.Items) != 2 {
		t.Fatalf("Inner = %v, want an exact 2-item list, not a star", *worn.Inner)
	}
}

// TestReadHostType_ATupleWithAnOptionalElementClaimsNoExactList pins
// the fallback: a tuple that is NOT every-position-required (an
// optional, rest, or variadic slot) must never read as an exact
// per-position list — the new branch only strengthens the fully-fixed
// case, and a partial tuple keeps whatever the general array-like
// reading answered before it existed (today, a refusal).
func TestReadHostType_ATupleWithAnOptionalElementClaimsNoExactList(t *testing.T) {
	p := programFromSource(t, "export function f(item: [number, number?]): void { console.log(item); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "item", 1))
	if ok && worn.Kind == abstractdomain.KindList {
		t.Errorf("a partial tuple read as an exact list — the branch must not claim positions it cannot prove")
	}
}

func TestReadTypeNode_BooleanArrayIsAStarOfTheTwoCodes(t *testing.T) {
	p := programFromSource(t, "export function f(xs: boolean[]): void { console.log(xs); }\n")
	parameter := parameterNamed(t, p, "xs")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	worn, ok := ReadTypeNode(p.checker, typeNode, parameter.Name(), 0)
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	formatted, ok := abstractdomain.FormatAbstractValue(worn)
	if !ok || formatted != "{each 0 | 1}" {
		t.Errorf("FormatAbstractValue = %q, %v, want %q, true", formatted, ok, "{each 0 | 1}")
	}
}

func TestReadDeclaredType_ObjectWithArrayBooleanFillsTheKeySyntaxSkipped(t *testing.T) {
	p := programFromSource(t, "export function f(o: { name: string; flags: Array<boolean> }): void { console.log(o); }\n")
	parameter := parameterNamed(t, p, "o")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	token := parameter.Name()
	syntaxOnly, syntaxOk := ReadTypeNode(p.checker, typeNode, token, 0)
	host, hostOk := ReadHostType(p.checker, p.checker.GetTypeAtLocation(token), token, 0)
	joined, joinedOk := ReadDeclaredType(p.checker, typeNode, token)
	if !syntaxOk || syntaxOnly.Kind != abstractdomain.KindObject {
		t.Fatalf("expected syntaxOnly to be an object")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(syntaxOnly); !ok || formatted != "{name: string}" {
		t.Errorf("FormatAbstractValue(syntaxOnly) = %q, %v, want %q, true", formatted, ok, "{name: string}")
	}
	if !hostOk {
		t.Fatalf("expected host to be a value")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(host); !ok || formatted != "{name: string, flags: each 0 | 1}" {
		t.Errorf("FormatAbstractValue(host) = %q, %v, want %q, true", formatted, ok, "{name: string, flags: each 0 | 1}")
	}
	if !joinedOk {
		t.Fatalf("expected joined to be a value")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(joined); !ok || formatted != "{name: string, flags: each 0 | 1}" {
		t.Errorf("FormatAbstractValue(joined) = %q, %v, want %q, true", formatted, ok, "{name: string, flags: each 0 | 1}")
	}
}

func TestReadDeclaredType_ArrayStringFallsThroughToHostStar(t *testing.T) {
	p := programFromSource(t, "export function f(xs: Array<string>): void { console.log(xs); }\n")
	parameter := parameterNamed(t, p, "xs")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	token := parameter.Name()
	_, syntaxOk := ReadTypeNode(p.checker, typeNode, token, 0)
	if syntaxOk {
		t.Errorf("expected syntax-only reading to hold nothing")
	}
	joined, joinedOk := ReadDeclaredType(p.checker, typeNode, token)
	if !joinedOk || joined.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if joined.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", joined.Set.Forms[0].Form)
	}
}

func TestGate_OneCopyThroughANamedConstLaundersNothing(t *testing.T) {
	p := programFromSource(
		t,
		"const raw = JSON.parse(\"0\");\n"+
			"const b: number = raw;\n"+
			"const a = b;\n"+
			"console.log(a);\n",
	)
	aDeclaration := identifierIn(t, p, "a", 0).Parent
	if !UncheckedDeclaration(p.checker, aDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(a) = false, want true")
	}
	bDeclaration := identifierIn(t, p, "b", 0).Parent
	if !UncheckedDeclaration(p.checker, bDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(b) = false, want true")
	}
}

func TestGate_ACheckedChainOfCopiesStaysSeedable(t *testing.T) {
	p := programFromSource(
		t,
		"const b: number = 1;\n"+
			"const a = b;\n"+
			"console.log(a);\n",
	)
	aDeclaration := identifierIn(t, p, "a", 0).Parent
	if UncheckedDeclaration(p.checker, aDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(a) = true, want false")
	}
}
