// Pins annotationOfTypeSets' primitive-keyword arm (type_node_sets.go):
// a bare `string`/`number`/`boolean` parameter type -- no zod wrapper
// at all -- now states its own ground set, where it used to read as
// nil-nil ("plain TypeScript"). JT's ruling (2026-08-21) widens
// AnnotationOfType checker-wide so the ground rides into
// contract.Params, Grounded, assignability, and the fact exporter's
// entry rows uniformly, using the same ground constructors
// typereading/recipes.go already calls for evaluation.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestAnnotationOfType_BareStringKeywordStatesTheStringGround pins the
// new arm: `seed: string` with no zod wrapper reads as a DeclaredSet
// over refinementsets.Strings (C*) rather than nil-nil.
func TestAnnotationOfType_BareStringKeywordStatesTheStringGround(t *testing.T) {
	p := newTestProgram(t, "function f(seed: string): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredSet {
		t.Fatalf("Kind = %v, want set", result.Stated.Kind)
	}
	if !refinementsets.IsStringGround(derefSet(result.Stated.Set)) {
		t.Errorf("expected the bare string keyword's set to BE the string ground (C*)")
	}
	if result.Stated.KindTag != "" {
		t.Errorf("KindTag = %q, want empty (a bare string carries no sort tag)", result.Stated.KindTag)
	}
}

// TestAnnotationOfType_BareNumberKeywordStatesTheNumberGround pins the
// new arm: `code: number` with no zod wrapper reads as a DeclaredSet
// over refinementsets.Numbers (R-bar) -- the SAME set z.number()'s own
// root compiles to (chain_root_constructor.go's "number" case), so a
// bare keyword and a `z.number()`-derived alias ground identically.
func TestAnnotationOfType_BareNumberKeywordStatesTheNumberGround(t *testing.T) {
	p := newTestProgram(t, "function f(code: number): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredSet {
		t.Fatalf("Kind = %v, want set", result.Stated.Kind)
	}
	set := derefSet(result.Stated.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormAtLeast || set.Forms[0].A != negInf() {
		t.Errorf("set = %+v, want refinementsets.Numbers (AtLeast(-Inf))", set)
	}
	if result.Stated.KindTag != "" {
		t.Errorf("KindTag = %q, want empty (a bare number carries no sort tag)", result.Stated.KindTag)
	}
}

// TestAnnotationOfType_BareBooleanKeywordStatesTheTaggedTwoMemberSet
// pins the new arm: `flag: boolean` with no zod wrapper reads as a
// DeclaredSet over {0,1} TAGGED "boolean" -- mirroring z.boolean()'s
// own root (chain_root_constructor.go's "boolean" case) so a reader
// past this point (fact_export.go's entry rows) still tells a boolean
// from a two-member numeric literal set.
func TestAnnotationOfType_BareBooleanKeywordStatesTheTaggedTwoMemberSet(t *testing.T) {
	p := newTestProgram(t, "function f(flag: boolean): number { return 0; }\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	typeNode := typeOfParam(t, p, "f")
	result := AnnotationOfType(p.program, typeNode, registry, objects)
	if result.Stated == nil {
		t.Fatalf("expected a stated refinement, got unsupported=%q", result.Unsupported)
	}
	if result.Stated.Kind != DeclaredSet {
		t.Fatalf("Kind = %v, want set", result.Stated.Kind)
	}
	if result.Stated.KindTag != "boolean" {
		t.Errorf("KindTag = %q, want \"boolean\"", result.Stated.KindTag)
	}
	set := derefSet(result.Stated.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormOneOf ||
		len(set.Forms[0].W) != 2 || set.Forms[0].W[0] != 0 || set.Forms[0].W[1] != 1 {
		t.Errorf("set = %+v, want the OneOf({0,1}) form", set)
	}
}

// TestAnnotationOfType_TypeParameterExtendsNumberStaysUngrounded is a
// non-regression pin: the widened primitive-keyword arm must not
// touch `T extends number`'s own special case (type_node_aliases.go),
// which bounds by R-bar WITHOUT grounding — that path checks
// constraint.Kind == ast.KindNumberKeyword directly and never calls
// annotationOfType (hence never reaches this file's new arm) for a
// type-parameter constraint.
func TestAnnotationOfType_TypeParameterExtendsNumberStaysUngrounded(t *testing.T) {
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
		t.Errorf("BoundGrounded = true, want false — `extends number` bounds by R-bar without grounding, unchanged by the primitive-keyword arm")
	}
}

// negInf is -Inf as a refinementsets.Refinement.A value, spelled once
// here so the number-ground test's structural check reads plainly.
func negInf() float64 {
	return refinementsets.Numbers.Forms[0].A
}
