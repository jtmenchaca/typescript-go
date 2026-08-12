// Interface test for InterfaceHashOfSets: a hash is stable across
// re-computation of an unchanged registry, and changes when an
// annotation's set changes -- the property the whole caching scheme
// depends on.

package annotations

import "testing"

func TestInterfaceHashOfSets_StableOnRepeatIdenticalInput(t *testing.T) {
	p := newTestProgram(t, "const zPct = z.number().min(0).max(100);\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zPct")

	first := InterfaceHashOfSets(registry, objects, nil)
	second := InterfaceHashOfSets(registry, objects, nil)
	if first != second {
		t.Errorf("InterfaceHashOfSets is not stable across identical calls: %q vs %q", first, second)
	}
}

func TestInterfaceHashOfSets_ChangesWhenTheSetChanges(t *testing.T) {
	pA := newTestProgram(t, "const zPct = z.number().min(0).max(100);\n")
	registryA := AnnotationRegistry{}
	objectsA := ObjectRegistry{}
	compileAndRegisterAnnotation(t, pA, registryA, objectsA, "zPct")
	hashA := InterfaceHashOfSets(registryA, objectsA, nil)

	pB := newTestProgram(t, "const zPct = z.number().min(0).max(50);\n")
	registryB := AnnotationRegistry{}
	objectsB := ObjectRegistry{}
	compileAndRegisterAnnotation(t, pB, registryB, objectsB, "zPct")
	hashB := InterfaceHashOfSets(registryB, objectsB, nil)

	if hashA == hashB {
		t.Errorf("InterfaceHashOfSets did not change when the annotation's bound changed: both %q", hashA)
	}
}

func TestInterfaceHashOfSets_ImportHashesFoldIn(t *testing.T) {
	p := newTestProgram(t, "const zPct = z.number();\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compileAndRegisterAnnotation(t, p, registry, objects, "zPct")

	without := InterfaceHashOfSets(registry, objects, nil)
	with := InterfaceHashOfSets(registry, objects, map[string]string{"/other.ts": "abc123"})
	if without == with {
		t.Errorf("InterfaceHashOfSets ignored importHashes: both %q", without)
	}
}
