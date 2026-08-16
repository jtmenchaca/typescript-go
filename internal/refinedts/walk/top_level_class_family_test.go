// Pins the e-class-and-function.ts syntax-coverage rows for TOP-LEVEL
// class declarations — staticField and staticBlock — through
// class_static_field_invariants.go, the reader that fills the gap no
// prior file covered: nothing anywhere in the Go tree read
// `ClassName.field` before this one (ClassFieldsOf's own doc comment
// excludes statics by design — "it belongs to the constructor object,
// not to any instance" — and that exclusion is correct for the
// INSTANCE bundle; it left the constructor-object read with no home
// at all).
//
// staticField (unmarked, in-set): `Limits.ceiling` off `static ceiling
// = 40` must answer exactly 40, not the unknown the missing reader
// left it as (which fired the alert on a silent row).
//
// staticBlock (marked, out-of-set): `OverCounted.total` off `static
// total = 0` overwritten by a static block's `OverCounted.total =
// 200;` must answer 200 — the WRITE landing, not the stale
// initializer. A reader built only from initializers (skipping the
// static-block scan) would silence this row instead of firing it
// correctly.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestReadStaticFieldAccess_APlainStaticFieldAnswersItsInitializer(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Limits {\n"+
			"  static ceiling = 40;\n"+
			"}\n"+
			"function staticField(): number {\n"+
			"  return Limits.ceiling;\n"+
			"}\n"+
			"void staticField;\n")
	ctx := classFieldValuesContext(p)
	access := classFieldValuesFirstNode(t, p, "Limits.ceiling read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Limits" &&
			pa.Name() != nil && ast.IsIdentifier(pa.Name()) && pa.Name().Text() == "ceiling"
	})
	held := ReadStaticFieldAccess(ctx, access)
	if held == nil {
		t.Fatalf("ReadStaticFieldAccess answered nil for a plain static field read")
	}
	classFieldValuesExactNumber(t, *held, 40)
}

func TestReadStaticFieldAccess_AStaticBlockWriteLandsOverTheInitializer(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class OverCounted {\n"+
			"  static total = 0;\n"+
			"  static {\n"+
			"    OverCounted.total = 200;\n"+
			"  }\n"+
			"}\n"+
			"function staticBlock(): number {\n"+
			"  return OverCounted.total;\n"+
			"}\n"+
			"void staticBlock;\n")
	ctx := classFieldValuesContext(p)
	access := classFieldValuesFirstNode(t, p, "OverCounted.total read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "OverCounted" &&
			pa.Name() != nil && ast.IsIdentifier(pa.Name()) && pa.Name().Text() == "total" &&
			// the READ inside staticBlock()'s return, not the WRITE inside
			// the static block itself — both are PropertyAccessExpressions
			// spelling the same name, so this is the first one NOT inside a
			// binary assignment's left side
			!(node.Parent != nil && ast.IsBinaryExpression(node.Parent) &&
				node.Parent.AsBinaryExpression().Left == node)
	})
	held := ReadStaticFieldAccess(ctx, access)
	if held == nil {
		t.Fatalf("ReadStaticFieldAccess answered nil for a static field a static block writes")
	}
	classFieldValuesExactNumber(t, *held, 200)
}

func TestReadStaticFieldAccess_AStaticBlockWriteThroughThisAlsoLands(t *testing.T) {
	// inside a static block, `this` names the constructor object
	// itself (sec-runtime-semantics-classdefinitionevaluation) — the
	// same write, spelled the other way
	p := classFieldValuesProgram(t,
		"class Counted {\n"+
			"  static total = 0;\n"+
			"  static {\n"+
			"    this.total = 40;\n"+
			"  }\n"+
			"}\n"+
			"function staticBlockViaThis(): number {\n"+
			"  return Counted.total;\n"+
			"}\n"+
			"void staticBlockViaThis;\n")
	ctx := classFieldValuesContext(p)
	access := classFieldValuesFirstNode(t, p, "Counted.total read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Counted" &&
			pa.Name() != nil && ast.IsIdentifier(pa.Name()) && pa.Name().Text() == "total"
	})
	held := ReadStaticFieldAccess(ctx, access)
	if held == nil {
		t.Fatalf("ReadStaticFieldAccess answered nil for a static field a `this`-spelled static-block write touches")
	}
	classFieldValuesExactNumber(t, *held, 40)
}

func TestReadStaticFieldAccess_AnUnspellableStaticWritePoisonsTheField(t *testing.T) {
	// a compound assign inside the static block writes SOME number the
	// collection cannot pin — the field must answer nothing rather than
	// the stale initializer
	p := classFieldValuesProgram(t,
		"class Bumped {\n"+
			"  static total = 0;\n"+
			"  static {\n"+
			"    Bumped.total += 1;\n"+
			"  }\n"+
			"}\n"+
			"void Bumped;\n")
	declaration := classFieldValuesClassNamed(t, p, "Bumped")
	ctx := classFieldValuesContext(p)
	invariants := StaticFieldInvariantsOf(ctx, declaration)
	if _, has := invariants["total"]; has {
		t.Errorf("a compound-assigned static field kept an invariant — the stale initializer would have answered instead of nothing")
	}
}

func TestReadStaticFieldAccess_ThroughEvaluateExpression_ReadsThePropertyAccessDispatcher(t *testing.T) {
	// the wiring pin: ReadPropertyAccess (evaluate_property_access.go)
	// must actually reach ReadStaticFieldAccess for a bare class
	// reference — this exercises the SAME entry point
	// AnalyzeReturnStatement calls for `return Limits.ceiling;`
	p := classFieldValuesProgram(t,
		"class Limits {\n"+
			"  static ceiling = 40;\n"+
			"}\n"+
			"function staticField(): number {\n"+
			"  return Limits.ceiling;\n"+
			"}\n"+
			"void staticField;\n")
	ctx := classFieldValuesContext(p)
	access := classFieldValuesFirstNode(t, p, "Limits.ceiling read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Limits"
	})
	held := evaluateExpression(ctx, NewEnv(), access)
	if held.Kind != abstractdomain.KindValues {
		t.Fatalf("evaluateExpression(Limits.ceiling).Kind = %v, want KindValues — the property-access dispatcher never reached ReadStaticFieldAccess", held.Kind)
	}
	classFieldValuesExactNumber(t, held, 40)
}
