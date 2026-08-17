// Pin for the keyed write behind MEMBER-THREADING through a reduce
// callback (h-object-literal-members.ts's reduceObjectKeysWritesDefaults
// shape): ReadIndexedWrite's object branch (index_operators.go) used to
// require every WRITTEN key already present on the receiver before it
// would touch it (`allPresent`) — sound for a UNION of candidate keys
// (which one fired at runtime is unknown, so inventing an absent key
// would be a guess), but wrong for a SINGLE exactly-known key (a
// literal, or an index expression whose own static type has exactly one
// string-literal member): there the runtime key is certain, and the
// write belongs exactly where WriteProperty (assignments.go) already
// puts a direct `acc.age = v` — added when absent, replaced when
// present.
//
// The fixture row's END-TO-END determination remains open past this
// fix, with the wall now named: the guard `acc[key] === undefined` on
// an exactly-absent read cannot decide, because the absent marker
// CONFLATES null and undefined (comparison_decision.go's own strict
// absent-vs-absent rule) — deciding it needs the null/undefined split
// in the absence vocabulary, which is its own unit. The corpus row is
// that gap's pin.

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

// memberThreadingProgram mirrors keyedSlotProgram (keyed_slot_reads_test.go)
// — a one-file program, no surface stand-in.
func memberThreadingProgram(t *testing.T, entrySource string) *program.CheckerProgram {
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

// memberThreadingFunctionNamed mirrors keyedSlotFunctionNamed.
func memberThreadingFunctionNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
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

// memberThreadingReturnValue walks the named function's body and hands
// back the value its ReturnSink collected — mirrors keyedSlotBodyEnv's
// kernel-gated setup, but reads the RETURNED value directly (the
// fixture's own row is a returned expression, not a plain binding).
func memberThreadingReturnValue(t *testing.T, p *program.CheckerProgram, functionName string) abstractdomain.AbstractValue {
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
	fn := memberThreadingFunctionNamed(t, p, functionName)
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
	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	env := NewEnv()
	AnalyzeStatements(ctx, env, body.AsBlock().Statements.Nodes, nil)
	if len(sink) == 0 {
		t.Fatalf("function %s returned nothing", functionName)
	}
	joined := sink[0]
	for _, v := range sink[1:] {
		joined = abstractdomain.JoinKnown(joined, v)
	}
	return joined
}


func TestReadIndexedWrite_ASingleExactKeyAddsTheAbsentMember(t *testing.T) {
	// the direct unit pin: acc[key] = v where key's own static type is
	// the one-member literal "age" and acc does not carry that key yet
	p := memberThreadingProgram(t, `
function f() {
	const acc: { age?: number } = {};
	const key: "age" = "age";
	acc[key] = 41;
	return acc.age;
}
`)
	fn := memberThreadingFunctionNamed(t, p, "f")
	body := fn.AsFunctionDeclaration().Body
	var assignment *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken &&
			ast.IsElementAccessExpression(node.AsBinaryExpression().Left) {
			assignment = node
			return true
		}
		node.ForEachChild(visit)
		return assignment != nil
	}
	body.ForEachChild(visit)
	if assignment == nil {
		t.Fatalf("no element-access assignment found")
	}
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
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
	env.Set("acc", abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false))
	// key's own runtime value is irrelevant to the object branch's
	// type-resolved arm — it reads the CHECKER's static type at the
	// argument node (`const key: "age" = "age"`), not env's binding —
	// but a tracked binding still makes the receiver's WriteElement
	// pass (an untracked identifier is not this test's concern) clean.
	env.Set("key", abstractdomain.KnownValues(refinementsets.CodepointsOf("age"), abstractdomain.PrimitiveString, abstractdomain.TrustProved))
	if _, matched := ReadIndexedWrite(ctx, env, assignment); !matched {
		t.Fatalf("ReadIndexedWrite did not match `acc[key] = 41`")
	}
	held, tracked := env.Get("acc")
	if !tracked || held.Kind != abstractdomain.KindObject {
		t.Fatalf("acc after the write: tracked=%v kind=%v, want a tracked object", tracked, held.Kind)
	}
	idx, hasKey := objectKeyIndex(held, "age")
	if !hasKey {
		t.Fatalf("acc.Keys = %v, want an age member added", held.Keys)
	}
	formatted, hasFormat := abstractdomain.FormatAbstractValue(held.Keys[idx].Value)
	if !hasFormat || formatted != "41" {
		t.Errorf("acc.age after the write = %q, want %q", formatted, "41")
	}
}
