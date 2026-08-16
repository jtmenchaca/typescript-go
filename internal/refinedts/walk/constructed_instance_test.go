// The syntax-coverage row b-body-expressions.ts:147 (callSuperConstructor):
// `new SuperCtorChild(40).age` where the child's constructor runs only
// `super(age)` — the base constructor's own `this.age = age` write must
// still land on the instance ConstructedInstance builds. A constructor
// declaration never registers as a FunctionContract
// (contract_file_facts.go's collector), so SuperCallContract can never
// resolve one for a bare `super(...)` call, and the ordinary
// EvaluateCallExpression walk of the derived constructor's body never
// inlines the base's writes. superConstructorFieldCandidates
// (constructed_instance.go) is the fix: it reads the base constructor's
// body directly, off the heritage chain, bound from the super call's
// own evaluated arguments. No kernel needed — ConstructedInstance's
// candidate join is plain Go, the same reason super_and_array_ctor_test's
// resolution-half tests skip the kernel gate.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

const superConstructorFixtureSource = "class SuperCtorBase {\n" +
	"  age: number;\n" +
	"  constructor(age: number) {\n" +
	"    this.age = age;\n" +
	"  }\n" +
	"}\n" +
	"class SuperCtorChild extends SuperCtorBase {\n" +
	"  constructor(age: number) {\n" +
	"    super(age);\n" +
	"  }\n" +
	"}\n"

// superConstructorNewExpression is the `new ClassName(args)` expression
// inside the named function's body.
func superConstructorNewExpression(t *testing.T, fn *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, fn.Body(), "new expression", ast.IsNewExpression)
}

// TestConstructedInstance_TheSuperConstructorsFieldWriteLandsOnTheInstance
// pins the in-set leg: `new SuperCtorChild(40).age` — the base
// constructor's `this.age = age` write, run through the super call's
// own argument, must answer the exact 40 ConstructedInstance builds.
func TestConstructedInstance_TheSuperConstructorsFieldWriteLandsOnTheInstance(t *testing.T) {
	p := entryEnvTestProgram(t, superConstructorFixtureSource)
	ctx := superArrayContracts(t, p)
	childDeclaration := superArrayFirstNode(t, p.Entry.AsNode(), "class SuperCtorChild", func(node *ast.Node) bool {
		return ast.IsClassDeclaration(node) && node.AsClassDeclaration().Name() != nil &&
			node.AsClassDeclaration().Name().Text() == "SuperCtorChild"
	})
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	instance := ConstructedInstance(ctx, childDeclaration, argKnowns)
	if instance.Kind != abstractdomain.KindObject {
		t.Fatalf("ConstructedInstance did not answer an object: %+v", instance)
	}
	idx, ok := objectKeyIndex(instance, "age")
	if !ok {
		t.Fatalf("the built instance carries no age key: %+v", instance.Keys)
	}
	age := instance.Keys[idx].Value
	if age.Kind != abstractdomain.KindValues || len(age.Values) != 1 || age.Values[0] != 40 {
		spelled, _ := abstractdomain.FormatAbstractValue(age)
		t.Errorf("new SuperCtorChild(40).age = %q, want the exact base-constructor write 40", spelled)
	}
}

// TestConstructedInstance_TheSuperConstructorsFieldWriteCarriesTheOutOfSetArgument
// is the marked twin: `new SuperCtorChild(200).age` must carry 200
// through the same path — the fix must not clamp or discard the
// argument, only thread it faithfully.
func TestConstructedInstance_TheSuperConstructorsFieldWriteCarriesTheOutOfSetArgument(t *testing.T) {
	p := entryEnvTestProgram(t, superConstructorFixtureSource)
	ctx := superArrayContracts(t, p)
	childDeclaration := superArrayFirstNode(t, p.Entry.AsNode(), "class SuperCtorChild", func(node *ast.Node) bool {
		return ast.IsClassDeclaration(node) && node.AsClassDeclaration().Name() != nil &&
			node.AsClassDeclaration().Name().Text() == "SuperCtorChild"
	})
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{200}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	instance := ConstructedInstance(ctx, childDeclaration, argKnowns)
	idx, ok := objectKeyIndex(instance, "age")
	if !ok {
		t.Fatalf("the built instance carries no age key: %+v", instance.Keys)
	}
	age := instance.Keys[idx].Value
	if age.Kind != abstractdomain.KindValues || len(age.Values) != 1 || age.Values[0] != 200 {
		spelled, _ := abstractdomain.FormatAbstractValue(age)
		t.Errorf("new SuperCtorChild(200).age = %q, want the exact base-constructor write 200", spelled)
	}
}

// The syntax-coverage row e-class-and-function.ts:140
// (privateFieldThroughConstructor): `#age: number;` carries NO
// initializer, and the constructor's own body writes it
// unconditionally (`this.#age = age;`, the only statement) before any
// caller can ever observe the instance. ConstructedInstance's field
// pass only ever adds a candidate for a PropertyDeclaration when
// `pd.Initializer != nil` (constructed_instance.go's member loop), so
// an uninitialized field seeds NO candidate of its own — the
// constructor's ThisWriteSink write is the field's only candidate,
// and the join is that one write alone, not a join against an absent
// seed. This test pins that reading directly against ConstructedInstance's
// own output, independent of the kernel-summary serving path a judge
// run would also cross.
const sealedPrivateFieldFixtureSource = "class Sealed {\n" +
	"  #age: number;\n" +
	"  constructor(age: number) {\n" +
	"    this.#age = age;\n" +
	"  }\n" +
	"}\n"

// TestConstructedInstance_UninitializedPrivateFieldConstructorWriteIsExact
// pins the in-set leg: `new Sealed(40).#age` (read through
// ConstructedInstance's own candidate join) must answer exactly 40 —
// no PossiblyUndefined wrapper from a phantom Undef candidate.
func TestConstructedInstance_UninitializedPrivateFieldConstructorWriteIsExact(t *testing.T) {
	p := entryEnvTestProgram(t, sealedPrivateFieldFixtureSource)
	ctx := superArrayContracts(t, p)
	sealedDeclaration := superArrayFirstNode(t, p.Entry.AsNode(), "class Sealed", func(node *ast.Node) bool {
		return ast.IsClassDeclaration(node) && node.AsClassDeclaration().Name() != nil &&
			node.AsClassDeclaration().Name().Text() == "Sealed"
	})
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	instance := ConstructedInstance(ctx, sealedDeclaration, argKnowns)
	if instance.Kind != abstractdomain.KindObject {
		t.Fatalf("ConstructedInstance did not answer an object: %+v", instance)
	}
	idx, ok := objectKeyIndex(instance, "#age")
	if !ok {
		t.Fatalf("the built instance carries no #age key: %+v", instance.Keys)
	}
	age := instance.Keys[idx].Value
	if age.Kind != abstractdomain.KindValues || len(age.Values) != 1 || age.Values[0] != 40 {
		spelled, _ := abstractdomain.FormatAbstractValue(age)
		t.Errorf("new Sealed(40).#age = %q, want the exact constructor write 40, not a possibly-undefined join", spelled)
	}
}

// TestConstructedInstance_UninitializedPrivateFieldConstructorWriteCarriesTheOutOfSetArgument
// is the marked twin: `new Sealed(200).#age` must carry 200 through the
// same path.
func TestConstructedInstance_UninitializedPrivateFieldConstructorWriteCarriesTheOutOfSetArgument(t *testing.T) {
	p := entryEnvTestProgram(t, sealedPrivateFieldFixtureSource)
	ctx := superArrayContracts(t, p)
	sealedDeclaration := superArrayFirstNode(t, p.Entry.AsNode(), "class Sealed", func(node *ast.Node) bool {
		return ast.IsClassDeclaration(node) && node.AsClassDeclaration().Name() != nil &&
			node.AsClassDeclaration().Name().Text() == "Sealed"
	})
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{200}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	instance := ConstructedInstance(ctx, sealedDeclaration, argKnowns)
	idx, ok := objectKeyIndex(instance, "#age")
	if !ok {
		t.Fatalf("the built instance carries no #age key: %+v", instance.Keys)
	}
	age := instance.Keys[idx].Value
	if age.Kind != abstractdomain.KindValues || len(age.Values) != 1 || age.Values[0] != 200 {
		spelled, _ := abstractdomain.FormatAbstractValue(age)
		t.Errorf("new Sealed(200).#age = %q, want the exact constructor write 200", spelled)
	}
}
