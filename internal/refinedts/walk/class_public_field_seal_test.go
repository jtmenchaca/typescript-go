// Pins the public-field seal's provenance-narrowed OutsideWritten veto
// — the syntax-wave divergence (AGENT-BRIEF.md, b-body-expressions.ts's
// thisFieldRead / ThisPerson.years()): nonThisWrittenFieldNames used to
// veto a sealed class's public field by NAME ALONE, so a `delete
// person.age` on an unrelated local in a totally different function —
// b-body-expressions.ts's own deleteExpression — stripped ThisPerson's
// `age` invariant class-wide. The narrowing is PROVENANCE, never the
// stated type (structural assignability lets `{ age?: number }` and
// `any` alike carry an instance): a write's receiver is excused only
// where it is a CONST born from an object LITERAL, a value that can
// never be a class instance (receiverCouldHoldAnInstance).
package walk

import (
	"testing"
)

// TestFieldInvariants_ANameMatchedWriteOnAnUnrelatedTypeDoesNotVetoTheSeal
// is the direct reproduction: `delete person.age` where `person` is a
// locally typed `{ age?: number }` object, in a function with no
// relation to ThisPerson at all, sitting BESIDE ThisPerson in the same
// file — the exact shape b-body-expressions.ts carries (its own
// deleteExpression function ahead of ThisPerson/thisFieldRead). Before
// the provenance fix, `age`'s appearance in nonThisWrittenFieldNames
// vetoed EVERY sealed class's `age` field in the file, ThisPerson
// included, though `person` is a const born from its own object
// literal — a value that can never be a ThisPerson instance.
func TestFieldInvariants_ANameMatchedWriteOnAnUnrelatedTypeDoesNotVetoTheSeal(t *testing.T) {
	p := classFieldValuesProgram(t,
		"function deleteExpression(): void {\n"+
			"  const person: { age?: number } = { age: 40 };\n"+
			"  delete person.age;\n"+
			"}\n"+
			"class ThisPerson {\n"+
			"  age = 40;\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"const ok = new ThisPerson().years();\n"+
			"void ok;\n"+
			"deleteExpression();\n")
	declaration := classFieldValuesClassNamed(t, p, "ThisPerson")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	held, has := invariants["age"]
	if !has {
		t.Fatalf("ThisPerson.age lost its seal to a same-named field write on an unrelated, disjoint type")
	}
	classFieldValuesExactNumber(t, held, 40)
}

// TestFieldInvariants_AWriteOnTheSameClassStillVetoesTheSeal is the
// regression guard beside the fix above: the veto must still fire when
// the outside write's receiver really COULD be the sealed class — a
// parameter, whose provenance proves nothing, written through a
// non-`this` receiver. Narrowing by provenance must never turn into
// narrowing by "no write anywhere ever vetoes anything".
func TestFieldInvariants_AWriteOnTheSameClassStillVetoesTheSeal(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class ThisPerson {\n"+
			"  age = 40;\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"function poke(p: ThisPerson): void {\n"+
			"  p.age = 7;\n"+
			"}\n"+
			"const ok = new ThisPerson().years();\n"+
			"void ok;\n"+
			"void poke;\n")
	declaration := classFieldValuesClassNamed(t, p, "ThisPerson")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("ThisPerson.age kept its seal though a same-typed parameter writes it outside `this`")
	}
}
