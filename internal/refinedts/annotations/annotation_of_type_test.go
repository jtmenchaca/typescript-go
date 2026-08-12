// Interface tests for AnnotationOfType: z.infer<typeof X> reading,
// the mapped utilities (Partial/Pick), unions with absence, and the
// refinement-variable reading of a bounded type parameter.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// typeOfParam returns the type node of the named function's first
// parameter.
func typeOfParam(t *testing.T, p testProgram, functionName string) *ast.Node {
	t.Helper()
	for _, statement := range p.program.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		fn := statement.AsFunctionDeclaration()
		if fn.Name() == nil || fn.Name().AsIdentifier().Text != functionName {
			continue
		}
		params := statement.Parameters()
		if len(params) == 0 {
			t.Fatalf("function %s has no parameters", functionName)
		}
		return params[0].AsParameterDeclaration().Type
	}
	t.Fatalf("no function named %s", functionName)
	return nil
}

func compileAndRegisterAnnotation(t *testing.T, p testProgram, registry AnnotationRegistry, objects ObjectRegistry, name string) {
	t.Helper()
	init := namedConstInitializer(t, p, name)
	compiled := CompileAnnotation(p.program, init, registry)
	if IsUnsupported(compiled) {
		t.Fatalf("compiling %s: %s", name, compiled.Unsupported.Unsupported)
	}
	symbol := p.program.Checker.GetSymbolAtLocation(namedConstDeclaration(t, p, name).Name())
	registry[symbol] = compiled.Annotation
}

func TestAnnotationOfType_ZInferReadsTheRegisteredSet(t *testing.T) {
	p := newTestProgram(t,
		"const zPct = z.number().min(0).max(100);\n"+
			"function f(p: z.infer<typeof zPct>): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zPct")

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredSet {
		t.Fatalf("Kind = %v, want set", result.Stated.Kind)
	}
	if len(derefSet(result.Stated.Set).Forms) == 0 {
		t.Errorf("expected the zPct set's forms to carry through")
	}
}

func TestAnnotationOfType_PartialMakesEveryKeyAbsentAdmitting(t *testing.T) {
	p := newTestProgram(t,
		"const zUser = z.object({ id: z.number(), name: z.string() });\n"+
			"type User = z.infer<typeof zUser>;\n"+
			"function f(u: Partial<User>): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	userInit := namedConstInitializer(t, p, "zUser")
	compiled := CompileObject(p.program, userInit, registry, objects)
	if compiled.Unsupported != "" {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported)
	}
	symbol := p.program.Checker.GetSymbolAtLocation(namedConstDeclaration(t, p, "zUser").Name())
	objects[symbol] = compiled.Object

	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredObject {
		t.Fatalf("Kind = %v, want object", result.Stated.Kind)
	}
	for _, key := range result.Stated.Object.Keys {
		if !key.MayBeAbsent {
			t.Errorf("key %s: MayBeAbsent = false, want true under Partial<>", key.Name)
		}
	}
}

func TestAnnotationOfType_TypeParameterExtendsNumberIsTheRootUngrounded(t *testing.T) {
	// `T extends number` is bounded by R-bar itself -- the TS source's
	// special case for the NumberKeyword constraint -- and stays
	// UNGROUNDED (per the file comment: "extends number bounds by
	// R-bar without grounding"); only a bound read from a STATED
	// annotation sets BoundGrounded.
	p := newTestProgram(t, "function f<T extends number>(x: T): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredVariable {
		t.Fatalf("Kind = %v, want variable", result.Stated.Kind)
	}
	if result.Stated.BoundGrounded {
		t.Errorf("BoundGrounded = true, want false for the NumberKeyword special case")
	}
	if len(derefSet(result.Stated.Bound).Forms) == 0 {
		t.Errorf("expected the bound to be R-bar (a non-empty forms list)")
	}
}

func TestAnnotationOfType_TypeParameterBoundByAStatedAnnotationIsGrounded(t *testing.T) {
	p := newTestProgram(t,
		"const zPct = z.number().min(0).max(100);\n"+
			"type Pct = z.infer<typeof zPct>;\n"+
			"function f<T extends Pct>(x: T): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zPct")
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredVariable {
		t.Fatalf("Kind = %v, want variable", result.Stated.Kind)
	}
	if !result.Stated.BoundGrounded {
		t.Errorf("BoundGrounded = false, want true when the constraint is a stated annotation")
	}
}

func TestAnnotationOfType_UnconstrainedTypeParameterIsUngrounded(t *testing.T) {
	p := newTestProgram(t, "function f<T>(x: T): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.BoundGrounded {
		t.Errorf("BoundGrounded = true, want false for an unconstrained T")
	}
}
