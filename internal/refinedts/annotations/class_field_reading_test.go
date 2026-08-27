// Pins for AnnotationOfType reading a CLASS-typed position (`p:
// Person`) as a DeclaredObject over its own data fields — the
// annotations-layer half of the class-instance field-read defect: a
// class declaration was never recognized in annotationOfTypeAliases,
// so a class-typed parameter's fields fell to typereading's host road
// alone and lost every refined-alias field the way any other
// declaration would (Age = z.infer<typeof zAge> resolves to plain
// number under the checker's resolved type).

package annotations

import "testing"

// TestAnnotationOfType_ClassPropertyDeclarationFieldReadsItsOwnStatement
// pins an ordinary `age: Age;` property declaration: the class-typed
// parameter states an object whose "age" key carries zAge's own set,
// not a bare number ground.
func TestAnnotationOfType_ClassPropertyDeclarationFieldReadsItsOwnStatement(t *testing.T) {
	p := newTestProgram(t,
		"const zAge = z.number().int().min(0).max(150);\n"+
			"type Age = z.infer<typeof zAge>;\n"+
			"class Person {\n"+
			"  age: Age;\n"+
			"  constructor(age: Age) { this.age = age; }\n"+
			"}\n"+
			"function f(p: Person): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zAge")

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredObject {
		t.Fatalf("Kind = %v, want object", result.Stated.Kind)
	}
	key := findKey(t, result.Stated.Object.Keys, "age")
	if key.Value.Kind != KeyValueSet {
		t.Fatalf("age key Kind = %v, want set", key.Value.Kind)
	}
	if len(derefSet(key.Value.Set).Forms) == 0 {
		t.Errorf("expected age's set to carry zAge's own forms, got an empty set")
	}
}

// TestAnnotationOfType_ClassConstructorParameterPropertyFieldReadsItsOwnStatement
// pins the OTHER spelling: `constructor(public age: Age) {}` declares
// a field with no property declaration anywhere in the class body.
func TestAnnotationOfType_ClassConstructorParameterPropertyFieldReadsItsOwnStatement(t *testing.T) {
	p := newTestProgram(t,
		"const zAge = z.number().int().min(0).max(150);\n"+
			"type Age = z.infer<typeof zAge>;\n"+
			"class Person {\n"+
			"  constructor(public age: Age) {}\n"+
			"}\n"+
			"function f(p: Person): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zAge")

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredObject {
		t.Fatalf("Kind = %v, want object", result.Stated.Kind)
	}
	key := findKey(t, result.Stated.Object.Keys, "age")
	if key.Value.Kind != KeyValueSet {
		t.Fatalf("age key Kind = %v, want set", key.Value.Kind)
	}
	if len(derefSet(key.Value.Set).Forms) == 0 {
		t.Errorf("expected age's set to carry zAge's own forms, got an empty set")
	}
}

// TestAnnotationOfType_ClassOptionalParameterPropertyFieldMayBeAbsent
// pins `nickname?: Age` on a parameter property: the key reads with
// MayBeAbsent set, the same "absence rides the key" rule an optional
// interface member's own reading already carries.
func TestAnnotationOfType_ClassOptionalParameterPropertyFieldMayBeAbsent(t *testing.T) {
	p := newTestProgram(t,
		"const zAge = z.number().int().min(0).max(150);\n"+
			"type Age = z.infer<typeof zAge>;\n"+
			"class Person {\n"+
			"  constructor(public age: Age, public nickname?: Age) {}\n"+
			"}\n"+
			"function f(p: Person): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zAge")

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	key := findKey(t, result.Stated.Object.Keys, "nickname")
	if !key.MayBeAbsent {
		t.Errorf("nickname key: MayBeAbsent = false, want true for an optional parameter property")
	}
}

// TestAnnotationOfType_ClassGetAccessorReadsItsOwnReturnType pins a
// get-accessor-only property (`get age(): Age`, backed by a `#age`
// private field): the accessor's OWN written return type states the
// key, so a class-typed parameter's read through the getter's name
// does not fall to the host's resolved-type fallback and lose Age's
// window — the regression this reading's own first draft introduced
// and this pin guards against returning.
func TestAnnotationOfType_ClassGetAccessorReadsItsOwnReturnType(t *testing.T) {
	p := newTestProgram(t,
		"const zAge = z.number().int().min(0).max(150);\n"+
			"type Age = z.infer<typeof zAge>;\n"+
			"class Person {\n"+
			"  #age: Age = 0;\n"+
			"  get age(): Age { return 5; }\n"+
			"}\n"+
			"function f(p: Person): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zAge")

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	key := findKey(t, result.Stated.Object.Keys, "age")
	if key.Value.Kind != KeyValueSet {
		t.Fatalf("age key Kind = %v, want set", key.Value.Kind)
	}
	if len(derefSet(key.Value.Set).Forms) == 0 {
		t.Errorf("expected age's set to carry the getter's own Age return type, got an empty set")
	}
}

// TestAnnotationOfType_ClassWithTypeParametersDeclinesRatherThanReadPartial
// pins the generic-class decline: a class carrying its own type
// parameters states nothing here, rather than a shape this reading
// cannot back.
func TestAnnotationOfType_ClassWithTypeParametersDeclinesRatherThanReadPartial(t *testing.T) {
	p := newTestProgram(t,
		"class Box<T> {\n"+
			"  constructor(public value: T) {}\n"+
			"}\n"+
			"function f(p: Box<number>): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated != nil {
		t.Fatalf("expected no stated refinement for a generic class, got Kind=%v", result.Stated.Kind)
	}
	if result.Unsupported != "" {
		t.Errorf("expected plain TypeScript (no loud refusal), got Unsupported=%q", result.Unsupported)
	}
}

// findKey is the named ObjectKeySpec in keys, or a test failure.
func findKey(t *testing.T, keys []ObjectKeySpec, name string) ObjectKeySpec {
	t.Helper()
	for _, key := range keys {
		if key.Name == name {
			return key
		}
	}
	t.Fatalf("no key named %q among %d keys", name, len(keys))
	return ObjectKeySpec{}
}
