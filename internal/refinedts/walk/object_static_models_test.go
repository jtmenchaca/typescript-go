// The syntax-coverage row j-stdlib-surfaces.ts:295 (objectFreeze):
// `Object.freeze({ age: 40 }).age` — Object.freeze sets [[Extensible]]
// false and returns the SAME object (sec-object.freeze); an ordinary
// [[Get]] of a data property is untouched by freezing. No model
// recognized `Object.freeze` at all before this fix — the call fell
// through to the unmodeled-call fallback, and a read off its result
// answered unknown. No kernel needed — the fix is a plain identity
// pass-through, read before any kernel-summary route runs.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

const objectFreezeFixtureSource = "function objectFreeze(): number {\n" +
	"  const person = Object.freeze({ age: 40 });\n" +
	"  return person.age;\n" +
	"}\n" +
	"function objectFreezeOver(): number {\n" +
	"  const over = Object.freeze({ age: 200 });\n" +
	"  return over.age;\n" +
	"}\n"

// TestReadObjectStaticMethods_FreezeReturnsTheSameObjectItsFieldReadsAnExact
// pins the in-set leg: `Object.freeze({ age: 40 }).age` reads back the
// exact 40 the literal held before freezing — freezing must not turn
// the read into unknown.
func TestReadObjectStaticMethods_FreezeReturnsTheSameObjectItsFieldReadsAnExact(t *testing.T) {
	p := entryEnvTestProgram(t, objectFreezeFixtureSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "objectFreeze")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	value := evaluateExpression(ctx, NewEnv(), returned.AsReturnStatement().Expression)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 40 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("Object.freeze({ age: 40 }).age = %q, want the exact stored value 40", spelled)
	}
}

// TestReadObjectStaticMethods_FreezeCarriesTheOutOfSetFieldToo is the
// marked twin: the fix must thread whatever the argument held, not
// clamp it — 200 rides through exactly the same way 40 does.
func TestReadObjectStaticMethods_FreezeCarriesTheOutOfSetFieldToo(t *testing.T) {
	p := entryEnvTestProgram(t, objectFreezeFixtureSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "objectFreezeOver")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	value := evaluateExpression(ctx, NewEnv(), returned.AsReturnStatement().Expression)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 200 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("Object.freeze({ age: 200 }).age = %q, want the exact stored value 200", spelled)
	}
}
