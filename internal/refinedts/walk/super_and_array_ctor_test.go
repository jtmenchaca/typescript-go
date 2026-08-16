// The two call shapes the syntax-coverage fixtures pin at
// b-body-expressions.ts:129 and c-reads-and-values.ts:1073:
//
//   - `new PersonChild().years()` where the child's body runs
//     `super.years() + 1` — the method summary crosses the super call
//     to the base body and answers exactly, instead of falling to the
//     return-type ground ("number, or NaN");
//   - `new Array(40).length` — the Array constructor with one exact
//     number argument builds an array of exactly that length
//     (sec-array), so the length read answers the argument.
//
// The resolution halves (SuperCallContract without the override gate,
// ContractOf's direct-`new` bypass, ReadArrayConstruction's rows) test
// without the kernel; the end-to-end walks skip — never a faked pass —
// when the native kernel dylib is absent, the same gate the other
// kernel-backed walk tests use.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// superFixtureSource mirrors the b-body-expressions.ts rows: a base
// answering 40, a child answering the base plus one, and the same pair
// answering 200 through the super call.
const superFixtureSource = "class PersonBase {\n" +
	"  years(): number { return 40; }\n" +
	"}\n" +
	"class PersonChild extends PersonBase {\n" +
	"  override years(): number { return super.years() + 1; }\n" +
	"}\n" +
	"class OverBase {\n" +
	"  years(): number { return 199; }\n" +
	"}\n" +
	"class OverChild extends OverBase {\n" +
	"  override years(): number { return super.years() + 1; }\n" +
	"}\n" +
	"function f(): number { return new PersonChild().years(); }\n" +
	"function g(): number { return new OverChild().years(); }\n" +
	"function h(p: PersonBase): number { return p.years(); }\n"

// superArrayClassMethod is the method named methodName of the class
// declaration named className — per class, unlike methodNamed, which
// reads the first class only.
func superArrayClassMethod(t *testing.T, p *program.CheckerProgram, className string, methodName string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		name := statement.AsClassDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) || name.Text() != className {
			continue
		}
		for _, member := range statement.AsClassDeclaration().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			memberName := member.Name()
			if memberName != nil && ast.IsIdentifier(memberName) && memberName.Text() == methodName {
				return member
			}
		}
	}
	t.Fatalf("no method %s.%s in the program", className, methodName)
	return nil
}

// superArrayFirstNode is the first node under root the predicate
// accepts, walked in source order.
func superArrayFirstNode(t *testing.T, root *ast.Node, what string, accept func(node *ast.Node) bool) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil || node == nil {
			return
		}
		if accept(node) {
			found = node
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(root)
	if found == nil {
		t.Fatalf("no %s under the given root", what)
	}
	return found
}

// superArrayNewMethodCall is the call spelled `new C().m()` for the
// given class and method names.
func superArrayNewMethodCall(t *testing.T, root *ast.Node, className string, methodName string) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call new "+className+"()."+methodName+"()", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		if access.Name() == nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != methodName {
			return false
		}
		receiver := access.Expression
		if !ast.IsNewExpression(receiver) {
			return false
		}
		constructor := receiver.AsNewExpression().Expression
		return ast.IsIdentifier(constructor) && constructor.Text() == className
	})
}

// superArrayContracts compiles the entry file's contracts the way the
// production walk does, and the FlowContext the call routes read.
func superArrayContracts(t *testing.T, p *program.CheckerProgram) *FlowContext {
	t.Helper()
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	return &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
}

// superArrayLoadKernel loads the native kernel or skips, and wires the
// three kernel seats the walk reads.
func superArrayLoadKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
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

// superArrayExactScalar asserts the value pins EXACTLY the one number:
// the spelled state admits it and excludes both neighbors.
func superArrayExactScalar(t *testing.T, kernel *kernelbridge.RefinedTSKernel, value abstractdomain.AbstractValue, want float64, label string) {
	t.Helper()
	state, ok := StateOfKnown(value)
	if !ok || state.Top {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Fatalf("%s did not spell as a scalar state: %q (%+v)", label, spelled, value)
	}
	if !kernel.Member(state.Set, []float64{want}) {
		t.Errorf("%s excludes the true value %v: %+v", label, want, state.Set)
	}
	for _, excluded := range []float64{want - 1, want + 1} {
		if kernel.Member(state.Set, []float64{excluded}) {
			t.Errorf("%s admits %v — the answer is not exact", label, excluded)
		}
	}
}

/* ── the super-call resolution, no kernel ────────────────────────── */

func TestSuperCallContract_TheBaseBodyAnswersThoughItsNameIsOverriddenInView(t *testing.T) {
	p := entryEnvTestProgram(t, superFixtureSource)
	ctx := superArrayContracts(t, p)
	child := superArrayClassMethod(t, p, "PersonChild", "years")
	superCall := superArrayFirstNode(t, child.Body(), "super.years() call", func(node *ast.Node) bool {
		return ast.IsCallExpression(node) && SuperRootedCallee(node.AsCallExpression().Expression)
	})
	contract := SuperCallContract(ctx, superCall.AsCallExpression().Expression)
	if contract == nil {
		t.Fatalf("SuperCallContract answered nil — the super call's static dispatch was refused")
	}
	base := superArrayClassMethod(t, p, "PersonBase", "years")
	if contract.Declaration != base {
		t.Errorf("SuperCallContract resolved a declaration other than PersonBase.years")
	}
}

func TestContractOf_ADirectNewReceiverKeepsItsOverrideNamedMethodContract(t *testing.T) {
	p := entryEnvTestProgram(t, superFixtureSource)
	ctx := superArrayContracts(t, p)
	call := superArrayNewMethodCall(t, p.Entry.AsNode(), "PersonChild", "years")
	contract := ContractOf(ctx, call.AsCallExpression().Expression)
	if contract == nil {
		t.Fatalf("ContractOf answered nil for `new PersonChild().years` — the exact-class receiver was refused")
	}
	child := superArrayClassMethod(t, p, "PersonChild", "years")
	if contract.Declaration != child {
		t.Errorf("ContractOf resolved a declaration other than PersonChild.years")
	}
}

// The NEGATIVE CONTROL the bypass needs: a receiver whose runtime
// class is NOT pinned — a parameter typed at the base — still refuses,
// because the dispatch is virtual and the resolved base body stands
// for no instance.
func TestContractOf_AVirtualReceiverStillRefusesTheOverriddenName(t *testing.T) {
	p := entryEnvTestProgram(t, superFixtureSource)
	ctx := superArrayContracts(t, p)
	virtualCall := superArrayFirstNode(t, p.Entry.AsNode(), "p.years() call", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return ast.IsIdentifier(access.Expression) && access.Expression.Text() == "p" &&
			access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "years"
	})
	if ContractOf(ctx, virtualCall.AsCallExpression().Expression) != nil {
		t.Errorf("ContractOf answered a contract for a virtual `p.years()` receiver — the override gate must stand there")
	}
}

/* ── the super-call summary, through the kernel ──────────────────── */

func TestLowerSummaryBody_ABodyWithASuperCallStillLowers(t *testing.T) {
	superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, superFixtureSource)
	ctx := superArrayContracts(t, p)
	child := superArrayClassMethod(t, p, "PersonChild", "years")
	if _, lowered := LowerSummaryBody(ctx, child); !lowered {
		t.Errorf("PersonChild.years declined to lower — the super call blocked the summary")
	}
}

func TestEvaluateCallExpression_TheSummaryCrossesTheSuperCall(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, superFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	// the unmarked fixture row: `new PersonChild().years()` answers
	// exactly 41 = the base's 40 crossed through `super.years() + 1`
	okCall := superArrayNewMethodCall(t, p.Entry.AsNode(), "PersonChild", "years")
	okValue := evaluateExpression(ctx, NewEnv(), okCall)
	superArrayExactScalar(t, kernel, okValue, 41, "new PersonChild().years()")

	// the marked twin: `new OverChild().years()` answers exactly 200 —
	// out of Age, so the fixture's expect-error row keeps firing, now
	// with the determined value rather than the return-type ground
	overCall := superArrayNewMethodCall(t, p.Entry.AsNode(), "OverChild", "years")
	overValue := evaluateExpression(ctx, NewEnv(), overCall)
	superArrayExactScalar(t, kernel, overValue, 200, "new OverChild().years()")
}

/* ── the Array constructor's result, model-level ─────────────────── */

// superArrayNewIn is the first NewExpression inside the named
// function's body.
func superArrayNewIn(t *testing.T, p *program.CheckerProgram, functionName string) *ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, functionName)
	return superArrayFirstNode(t, fn.Body(), "new expression in "+functionName, ast.IsNewExpression)
}

// superArrayCallIn is the first CallExpression inside the named
// function's body.
func superArrayCallIn(t *testing.T, p *program.CheckerProgram, functionName string) *ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, functionName)
	return superArrayFirstNode(t, fn.Body(), "call expression in "+functionName, ast.IsCallExpression)
}

func TestReadArrayConstruction_OneExactNumberArgumentBuildsTheHoleListOfThatLength(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(40).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(40)`")
	}
	if built.Kind != abstractdomain.KindList || len(built.Items) != 40 {
		t.Fatalf("`new Array(40)` = %+v, want the 40-slot list", *built)
	}
	for i, item := range built.Items {
		if item.Kind != abstractdomain.KindUndef {
			t.Fatalf("slot %d = %+v, want the hole's undefined", i, item)
		}
	}
}

func TestReadArrayConstruction_TheCallSpellingBuildsTheSameArray(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return Array(3).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayCallIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for the call spelling `Array(3)`")
	}
	if built.Kind != abstractdomain.KindList || len(built.Items) != 3 {
		t.Errorf("`Array(3)` = %+v, want the 3-slot list", *built)
	}
}

func TestReadArrayConstruction_NoArgumentsBuildTheEmptyArray(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array().length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array()`")
	}
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray || len(built.Values) != 0 {
		t.Errorf("`new Array()` = %+v, want the empty exact array", *built)
	}
}

func TestReadArrayConstruction_SeveralNumberArgumentsBuildTheExactTuple(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(1, 2, 3).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(1, 2, 3)`")
	}
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray ||
		len(built.Values) != 3 || built.Values[0] != 1 || built.Values[1] != 2 || built.Values[2] != 3 {
		t.Errorf("`new Array(1, 2, 3)` = %+v, want the exact tuple [1 2 3]", *built)
	}
}

func TestReadArrayConstruction_OnePinnedNonNumberArgumentBuildsTheOneElementArray(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(\"a\").length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(\"a\")`")
	}
	if built.Kind != abstractdomain.KindList || len(built.Items) != 1 {
		t.Fatalf("`new Array(\"a\")` = %+v, want the one-element list", *built)
	}
	item := built.Items[0]
	if item.Kind != abstractdomain.KindValues || item.KindTag != abstractdomain.PrimitiveString {
		t.Errorf("the one element = %+v, want the exact string", item)
	}
}

// The NEGATIVE CONTROLS: a throwing length models no array, and an
// unpinned length pins none.
func TestReadArrayConstruction_AThrowingOrUnpinnedLengthAnswersNothing(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(n: number): number { return new Array(-1).length + new Array(n).length; }\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	throwing := superArrayFirstNode(t, fn.Body(), "new Array(-1)", func(node *ast.Node) bool {
		if !ast.IsNewExpression(node) {
			return false
		}
		arguments := node.AsNewExpression().Arguments
		return arguments != nil && len(arguments.Nodes) == 1 && ast.IsPrefixUnaryExpression(arguments.Nodes[0])
	})
	if ReadArrayConstruction(ctx, NewEnv(), throwing) != nil {
		t.Errorf("`new Array(-1)` answered an array — the construction throws a RangeError and has no value")
	}
	unpinned := superArrayFirstNode(t, fn.Body(), "new Array(n)", func(node *ast.Node) bool {
		if !ast.IsNewExpression(node) {
			return false
		}
		arguments := node.AsNewExpression().Arguments
		return arguments != nil && len(arguments.Nodes) == 1 && ast.IsIdentifier(arguments.Nodes[0])
	})
	if ReadArrayConstruction(ctx, NewEnv(), unpinned) != nil {
		t.Errorf("`new Array(n)` with an unpinned length answered an array — no row claims it")
	}
}

/* ── the Array constructor's length read, through the walk ───────── */

func TestEvaluate_TheArrayConstructorLengthReadsExactly(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(40).length; }\n"+
			"function g(): number { return new Array(200).length; }\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	// the unmarked fixture row: length exactly 40, in Age
	fLength := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .length read", ast.IsPropertyAccessExpression)
	fValue := evaluateExpression(ctx, NewEnv(), fLength)
	superArrayExactScalar(t, kernel, fValue, 40, "new Array(40).length")

	// the marked twin: length exactly 200 — out of Age, so the
	// expect-error row keeps firing on the determined value
	gLength := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "g").Body(), "the .length read", ast.IsPropertyAccessExpression)
	gValue := evaluateExpression(ctx, NewEnv(), gLength)
	superArrayExactScalar(t, kernel, gValue, 200, "new Array(200).length")
}
