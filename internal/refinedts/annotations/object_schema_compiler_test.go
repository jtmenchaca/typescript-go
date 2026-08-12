// Interface tests for CompileObject: the compile-time half of
// object_schema_compiler.test.ts -- key shapes, .pick/.extend
// surgery, and the .refine dependent-row fold. The kernel-driven
// assignability half (service/check.ts) is service-tier and unported;
// see this package's port report.

package annotations

import (
	"testing"
)

func TestCompileObject_KeysCompileWithCardinalityAndMayBeAbsent(t *testing.T) {
	p := newTestProgram(t,
		"const User = z.object({\n"+
			"  id: z.number().int().min(1),\n"+
			"  nickname: z.string().optional(),\n"+
			"});\n")
	initializer := namedConstInitializer(t, p, "User")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compiled := CompileObject(p.program, initializer, registry, objects)
	if compiled.Unsupported != "" {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported)
	}
	if len(compiled.Object.Keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(compiled.Object.Keys))
	}
	byName := map[string]ObjectKeySpec{}
	for _, key := range compiled.Object.Keys {
		byName[key.Name] = key
	}
	id, ok := byName["id"]
	if !ok {
		t.Fatalf("no id key")
	}
	if id.MayBeAbsent {
		t.Errorf("id.MayBeAbsent = true, want false")
	}
	if id.Value.Kind != KeyValueSet {
		t.Errorf("id.Value.Kind = %v, want set", id.Value.Kind)
	}
	nickname, ok := byName["nickname"]
	if !ok {
		t.Fatalf("no nickname key")
	}
	if !nickname.MayBeAbsent {
		t.Errorf("nickname.MayBeAbsent = false, want true")
	}
}

func TestCompileObject_PickKeepsExactlyTheMaskedKeys(t *testing.T) {
	p := newTestProgram(t,
		"const User = z.object({ id: z.number(), name: z.string() });\n"+
			"const Picked = User.pick({ id: true });\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	userInit := namedConstInitializer(t, p, "User")
	userCompiled := CompileObject(p.program, userInit, registry, objects)
	if userCompiled.Unsupported != "" {
		t.Fatalf("unexpected unsupported compiling User: %s", userCompiled.Unsupported)
	}
	userSymbol := p.program.Checker.GetSymbolAtLocation(namedConstDeclaration(t, p, "User").Name())
	objects[userSymbol] = userCompiled.Object

	pickedInit := namedConstInitializer(t, p, "Picked")
	picked := CompileObject(p.program, pickedInit, registry, objects)
	if picked.Unsupported != "" {
		t.Fatalf("unexpected unsupported compiling Picked: %s", picked.Unsupported)
	}
	if len(picked.Object.Keys) != 1 || picked.Object.Keys[0].Name != "id" {
		t.Errorf("Picked.Keys = %+v, want exactly [id]", picked.Object.Keys)
	}
}

func TestCompileObject_ExtendMergesIncomingOverBase(t *testing.T) {
	p := newTestProgram(t,
		"const Base = z.object({ id: z.number() });\n"+
			"const Extended = Base.extend({ id: z.string(), name: z.string() });\n")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	baseInit := namedConstInitializer(t, p, "Base")
	baseCompiled := CompileObject(p.program, baseInit, registry, objects)
	if baseCompiled.Unsupported != "" {
		t.Fatalf("unexpected unsupported compiling Base: %s", baseCompiled.Unsupported)
	}
	baseSymbol := p.program.Checker.GetSymbolAtLocation(namedConstDeclaration(t, p, "Base").Name())
	objects[baseSymbol] = baseCompiled.Object

	extendedInit := namedConstInitializer(t, p, "Extended")
	extended := CompileObject(p.program, extendedInit, registry, objects)
	if extended.Unsupported != "" {
		t.Fatalf("unexpected unsupported compiling Extended: %s", extended.Unsupported)
	}
	if len(extended.Object.Keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(extended.Object.Keys))
	}
	byName := map[string]ObjectKeySpec{}
	for _, key := range extended.Object.Keys {
		byName[key.Name] = key
	}
	// incoming id: string() WINS over the base's number()
	idSet := derefSet(byName["id"].Value.Set)
	if idSet.Forms[0].Form != "star" {
		t.Errorf("extended id set forms[0] = %v, want star (the incoming string())", idSet.Forms[0].Form)
	}
}

func TestCompileObject_RefineFoldsDependentRowsAndPerKeyForms(t *testing.T) {
	p := newTestProgram(t,
		"const Range = z.object({ lo: z.number(), hi: z.number() })\n"+
			"  .refine((r) => r.hi >= r.lo);\n")
	initializer := namedConstInitializer(t, p, "Range")
	registry := AnnotationRegistry{}
	objects := ObjectRegistry{}
	compiled := CompileObject(p.program, initializer, registry, objects)
	if compiled.Unsupported != "" {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported)
	}
	var hi ObjectKeySpec
	found := false
	for _, key := range compiled.Object.Keys {
		if key.Name == "hi" {
			hi = key
			found = true
		}
	}
	if !found {
		t.Fatalf("no hi key")
	}
	if len(hi.Value.Depends) != 1 {
		t.Fatalf("hi.Value.Depends = %+v, want one dependent bound", hi.Value.Depends)
	}
	if hi.Value.Depends[0].Op != "ge" || hi.Value.Depends[0].Param != "lo" {
		t.Errorf("hi.Value.Depends[0] = %+v, want {ge, lo}", hi.Value.Depends[0])
	}
}
