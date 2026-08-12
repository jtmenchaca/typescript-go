// Ports condition_analysis.test.ts. The narrowing sets are the
// KERNEL's answers — skipped (reported as ignored, never a faked pass)
// when the artifacts are absent, the same gate kernelbridge's own
// round-trip tests use.
//
// NOT PORTED: the two `check()`-based cases ("excluding a kind sheds
// that arm from a kind union", "the negated form excludes on the true
// branch") — service/check.ts's whole-file check entry and the z.ts
// surface fixture have no Go twin yet (service/ is a later wave); the
// narrowing behavior they exercise is otherwise covered by this file's
// direct Narrowings() calls.
package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func ofCondition(t *testing.T, condition string) BranchNarrowings {
	t.Helper()
	c, file := checkerFor(t, "function f(x: number, y: number) { if ("+condition+") { return x; } return y; }")
	cond := firstIfCondition(file)
	if cond == nil {
		t.Fatalf("no if statement found")
	}
	return Narrowings(c, cond, func(string) bool { return true }, nil, GuardReadNowhere)
}

func loadNarrowKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetNarrowKernel(kernel)
}

func TestComparisonsNarrowBothBranchesWithStrictness(t *testing.T) {
	loadNarrowKernel(t)
	ge := ofCondition(t, "x >= 10")
	if len(ge.WhenTrue) != 1 || ge.WhenTrue[0].Binding != "x" || ge.WhenTrue[0].Refuting {
		t.Fatalf("ge.WhenTrue = %+v", ge.WhenTrue)
	}
	if len(ge.WhenTrue[0].Forms) != 1 || ge.WhenTrue[0].Forms[0].Form != "atLeast" || ge.WhenTrue[0].Forms[0].A != 10 {
		t.Errorf("ge.WhenTrue[0].Forms = %+v", ge.WhenTrue[0].Forms)
	}
	if len(ge.WhenFalse) != 1 || !ge.WhenFalse[0].Refuting {
		t.Fatalf("ge.WhenFalse = %+v", ge.WhenFalse)
	}
	if len(ge.WhenFalse[0].Forms) != 1 || ge.WhenFalse[0].Forms[0].Form != "below" || ge.WhenFalse[0].Forms[0].A != 10 {
		t.Errorf("ge.WhenFalse[0].Forms = %+v", ge.WhenFalse[0].Forms)
	}

	lt := ofCondition(t, "x < 0")
	if len(lt.WhenTrue[0].Forms) != 1 || lt.WhenTrue[0].Forms[0].Form != "below" || lt.WhenTrue[0].Forms[0].A != 0 {
		t.Errorf("lt.WhenTrue[0].Forms = %+v", lt.WhenTrue[0].Forms)
	}
	if len(lt.WhenFalse[0].Forms) != 1 || lt.WhenFalse[0].Forms[0].Form != "atLeast" || lt.WhenFalse[0].Forms[0].A != 0 {
		t.Errorf("lt.WhenFalse[0].Forms = %+v", lt.WhenFalse[0].Forms)
	}
}

func TestRefutationsMarkThemselves(t *testing.T) {
	loadNarrowKernel(t)
	// ¬(x > 0): the narrowing exists but knows it came from a refutation
	negated := ofCondition(t, "!(x > 0)")
	if !negated.WhenTrue[0].Refuting {
		t.Errorf("negated.WhenTrue[0].Refuting = false, want true")
	}
	if negated.WhenFalse[0].Refuting {
		t.Errorf("negated.WhenFalse[0].Refuting = true, want false")
	}
}

func TestALiteralOnTheLeftMirrorsTheOperator(t *testing.T) {
	loadNarrowKernel(t)
	mirrored := ofCondition(t, "10 >= x")
	if len(mirrored.WhenTrue[0].Forms) != 1 || mirrored.WhenTrue[0].Forms[0].Form != "atMost" || mirrored.WhenTrue[0].Forms[0].A != 10 {
		t.Errorf("mirrored.WhenTrue[0].Forms = %+v", mirrored.WhenTrue[0].Forms)
	}
}

func TestEqualityIsOneOfItsRefutationIsTheDifferenceForm(t *testing.T) {
	loadNarrowKernel(t)
	eq := ofCondition(t, "x === 5")
	if len(eq.WhenTrue[0].Forms) != 1 || eq.WhenTrue[0].Forms[0].Form != "oneOf" || len(eq.WhenTrue[0].Forms[0].W) != 1 || eq.WhenTrue[0].Forms[0].W[0] != 5 {
		t.Errorf("eq.WhenTrue[0].Forms = %+v", eq.WhenTrue[0].Forms)
	}
	if len(eq.WhenFalse[0].Forms) == 0 || eq.WhenFalse[0].Forms[0].Form != "difference" {
		t.Errorf("eq.WhenFalse[0].Forms = %+v", eq.WhenFalse[0].Forms)
	}
}

func TestNegationSwapsTheBranches(t *testing.T) {
	loadNarrowKernel(t)
	negated := ofCondition(t, "!(x > 0)")
	if len(negated.WhenTrue[0].Forms) != 1 || negated.WhenTrue[0].Forms[0].Form != "atMost" || negated.WhenTrue[0].Forms[0].A != 0 {
		t.Errorf("negated.WhenTrue[0].Forms = %+v", negated.WhenTrue[0].Forms)
	}
}

func TestAndConjoinsIntoOneClaimItsRefutationIsTheUnion(t *testing.T) {
	loadNarrowKernel(t)
	// the kernel intersects the conjuncts into one answered set —
	// one narrowing carrying both forms, strong (either conjunct's
	// truth vouches the value real)
	both := ofCondition(t, "x >= 0 && x <= 10")
	if len(both.WhenTrue) != 1 {
		t.Fatalf("both.WhenTrue = %+v", both.WhenTrue)
	}
	if len(both.WhenTrue[0].Forms) != 2 {
		t.Fatalf("both.WhenTrue[0].Forms = %+v", both.WhenTrue[0].Forms)
	}
	if both.WhenTrue[0].Refuting {
		t.Errorf("both.WhenTrue[0].Refuting = true, want false")
	}
	if len(both.WhenFalse) != 1 {
		t.Fatalf("both.WhenFalse = %+v", both.WhenFalse)
	}
	if len(both.WhenFalse[0].Forms) == 0 || both.WhenFalse[0].Forms[0].Form != "union" {
		t.Errorf("both.WhenFalse[0].Forms = %+v", both.WhenFalse[0].Forms)
	}
	if !both.WhenFalse[0].Refuting {
		t.Errorf("both.WhenFalse[0].Refuting = false, want true")
	}
}

func TestOrOnOneBindingFoldsToAUnion(t *testing.T) {
	loadNarrowKernel(t)
	either := ofCondition(t, "x === 1 || x === 2")
	if len(either.WhenTrue) != 1 {
		t.Fatalf("either.WhenTrue = %+v", either.WhenTrue)
	}
	if len(either.WhenTrue[0].Forms) == 0 || either.WhenTrue[0].Forms[0].Form != "union" {
		t.Errorf("either.WhenTrue[0].Forms = %+v", either.WhenTrue[0].Forms)
	}
	// both branches pin exactly, so the union is a STRONG claim
	if either.WhenTrue[0].Refuting {
		t.Errorf("either.WhenTrue[0].Refuting = true, want false")
	}
	// the refuted side: one claim, different from both — the
	// intersection of the two differences
	if len(either.WhenFalse) != 1 {
		t.Fatalf("either.WhenFalse = %+v", either.WhenFalse)
	}
	if len(either.WhenFalse[0].Forms) != 2 {
		t.Errorf("either.WhenFalse[0].Forms = %+v", either.WhenFalse[0].Forms)
	}
	if either.WhenFalse[0].Forms[0].Form != "difference" {
		t.Errorf("either.WhenFalse[0].Forms[0].Form = %v, want difference", either.WhenFalse[0].Forms[0].Form)
	}
}

func TestOrAcrossBindingsKeepsNoTrueBranchNarrowing(t *testing.T) {
	loadNarrowKernel(t)
	across := ofCondition(t, "x === 1 || y === 2")
	if len(across.WhenTrue) != 0 {
		t.Errorf("across.WhenTrue = %+v, want empty", across.WhenTrue)
	}
	if len(across.WhenFalse) != 2 {
		t.Errorf("across.WhenFalse = %+v, want 2", across.WhenFalse)
	}
}

func TestNumberIsIntegerNarrowsToTheIntegerForm(t *testing.T) {
	loadNarrowKernel(t)
	test := ofCondition(t, "Number.isInteger(x)")
	if len(test.WhenTrue[0].Forms) != 1 || test.WhenTrue[0].Forms[0].Form != "integer" {
		t.Errorf("test.WhenTrue[0].Forms = %+v", test.WhenTrue[0].Forms)
	}
	if len(test.WhenFalse[0].Forms) == 0 || test.WhenFalse[0].Forms[0].Form != "difference" {
		t.Errorf("test.WhenFalse[0].Forms = %+v", test.WhenFalse[0].Forms)
	}
}

func TestAGuardOnAPropertyNarrowsThatKeyByPath(t *testing.T) {
	loadNarrowKernel(t)
	c, file := checkerFor(t, "function f(o: { total: number }) { if (o.total >= 5) { return o; } return o; }")
	cond := firstIfCondition(file)
	p := Narrowings(c, cond, func(string) bool { return true }, nil, GuardReadNowhere)
	if len(p.WhenTrue) != 1 || p.WhenTrue[0].Binding != "o" || len(p.WhenTrue[0].Path) != 1 || p.WhenTrue[0].Path[0] != "total" {
		t.Fatalf("p.WhenTrue = %+v", p.WhenTrue)
	}
	if len(p.WhenTrue[0].Forms) != 1 || p.WhenTrue[0].Forms[0].Form != "atLeast" || p.WhenTrue[0].Forms[0].A != 5 {
		t.Errorf("p.WhenTrue[0].Forms = %+v", p.WhenTrue[0].Forms)
	}

	c2, file2 := checkerFor(t, "function f(o: { line: { qty: number } }) { if (o.line.qty < 3) { return o; } return o; }")
	cond2 := firstIfCondition(file2)
	deep := Narrowings(c2, cond2, func(string) bool { return true }, nil, GuardReadNowhere)
	if deep.WhenTrue[0].Binding != "o" {
		t.Fatalf("deep.WhenTrue[0].Binding = %v", deep.WhenTrue[0].Binding)
	}
	if len(deep.WhenTrue[0].Path) != 2 || deep.WhenTrue[0].Path[0] != "line" || deep.WhenTrue[0].Path[1] != "qty" {
		t.Errorf("deep.WhenTrue[0].Path = %+v", deep.WhenTrue[0].Path)
	}
}

func TestARefutedTypeofClaimsTheExcludedKind(t *testing.T) {
	loadNarrowKernel(t)
	positive := ofCondition(t, `typeof x === "boolean"`)
	if len(positive.WhenFalse) != 1 {
		t.Fatalf("positive.WhenFalse = %+v", positive.WhenFalse)
	}
	if positive.WhenFalse[0].ExcludesKind != "boolean" {
		t.Errorf("positive.WhenFalse[0].ExcludesKind = %v, want boolean", positive.WhenFalse[0].ExcludesKind)
	}
	if len(positive.WhenFalse[0].Forms) != 0 {
		t.Errorf("positive.WhenFalse[0].Forms = %+v, want empty", positive.WhenFalse[0].Forms)
	}
	negative := ofCondition(t, `typeof x !== "number"`)
	if len(negative.WhenTrue) != 1 {
		t.Fatalf("negative.WhenTrue = %+v", negative.WhenTrue)
	}
	if negative.WhenTrue[0].ExcludesKind != "number" {
		t.Errorf("negative.WhenTrue[0].ExcludesKind = %v, want number", negative.WhenTrue[0].ExcludesKind)
	}
}

func pinOfMakeCall(t *testing.T, source string) *ast.Node {
	t.Helper()
	c, file := checkerFor(t, source)
	var found *ast.Node
	var foundAny bool
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if foundAny {
			return true
		}
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "make" {
				found = FactoryPinnedFunction(c, node)
				foundAny = true
				return true
			}
		}
		node.ForEachChild(visit)
		return foundAny
	}
	file.AsNode().ForEachChild(visit)
	if !foundAny {
		t.Fatalf("no make() call found")
	}
	return found
}

func TestAFactoryWhoseOneReturnIsAnArrowPinsThatArrow(t *testing.T) {
	pinned := pinOfMakeCall(t,
		"function make(): (n: number) => number { return (n: number) => n; }\n"+
			"const add = make();\n")
	if pinned == nil || !ast.IsArrowFunction(pinned) {
		t.Errorf("pinned = %v, want an arrow function", pinned)
	}
}

func TestAConstBoundReturnPinsThroughTheName(t *testing.T) {
	pinned := pinOfMakeCall(t,
		"const id = (n: number) => n;\n"+
			"function make(): (n: number) => number { return id; }\n"+
			"const add = make();\n")
	if pinned == nil || !ast.IsArrowFunction(pinned) {
		t.Errorf("pinned = %v, want an arrow function", pinned)
	}
}

func TestTwoBranchDependentReturnsPinNothing(t *testing.T) {
	pinned := pinOfMakeCall(t,
		"function make(flag: boolean): (n: number) => number {\n"+
			"  if (flag) return (n: number) => n;\n"+
			"  return (n: number) => n + 1000;\n"+
			"}\n"+
			"const pick = make(true);\n")
	if pinned != nil {
		t.Errorf("pinned = %v, want nil", pinned)
	}
}

func TestAReturnCapturingTheFactorysOwnStatePinsNothing(t *testing.T) {
	pinned := pinOfMakeCall(t,
		"function make(base: number): (n: number) => number {\n"+
			"  return (n: number) => n + base;\n"+
			"}\n"+
			"const add = make(7);\n")
	if pinned != nil {
		t.Errorf("pinned = %v, want nil", pinned)
	}
}

func TestABareReturnAmongTheExitsPinsNothing(t *testing.T) {
	pinned := pinOfMakeCall(t,
		"function make(flag: boolean) {\n"+
			"  if (flag) return;\n"+
			"  return (n: number) => n;\n"+
			"}\n"+
			"const add = make(false);\n")
	if pinned != nil {
		t.Errorf("pinned = %v, want nil", pinned)
	}
}
