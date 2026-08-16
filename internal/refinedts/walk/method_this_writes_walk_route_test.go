// The syntax-coverage row b-body-expressions.ts:431 (literalWritingMethod):
// `person.bump()` where `bump` writes `this.age = this.age + 1` off an
// object-literal receiver, called from the SAME function that declares
// `person` — the direct-interpreter path (EvaluateCallExpression ->
// InlineContractBody), not the kernel-IR summary/lowering path
// method_this_writes.go's older machinery (literalThisBundleOf,
// LiteralMethodWriteStatements) already covers. InlineContractBody's
// walk-route body walk never bound `this` at all before
// ObjectLiteralMethodWalkCall existed, so the write landed nowhere and
// `person` kept its stale entry. No kernel needed — the walk route
// this function fixes is plain Go, tried before the kernel-summary
// route in InlineContractBody.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

const objectLiteralMethodWalkFixtureSource = "function literalWritingMethod(): number {\n" +
	"  const person = {\n" +
	"    age: 40,\n" +
	"    bump(): void {\n" +
	"      this.age = this.age + 1;\n" +
	"    },\n" +
	"  };\n" +
	"  person.bump();\n" +
	"  return person.age;\n" +
	"}\n" +
	"function literalSpoilMethod(): number {\n" +
	"  const outlaw = {\n" +
	"    age: 40,\n" +
	"    spoil(): void {\n" +
	"      this.age = 200;\n" +
	"    },\n" +
	"  };\n" +
	"  outlaw.spoil();\n" +
	"  return outlaw.age;\n" +
	"}\n"

// objectLiteralMethodWalkCallRun drives fnName's WHOLE body through
// AnalyzeFunction — no kernel, no stated result (Grounded stays
// false, so AnalyzeFunction passes CheckAssignability no set to ask
// the kernel about) — so `const person = {...}` and `person.bump()`
// actually run before the return statement reads `person.age`.
// evaluateExpression on a hand-picked return expression alone, against
// a fresh NewEnv(), never bound `person` at all — the empty formatting
// the two callers below used to see was an unbound identifier read,
// not a walk-route regression.
func objectLiteralMethodWalkCallRun(t *testing.T, fnName string) abstractdomain.AbstractValue {
	t.Helper()
	p := entryEnvTestProgram(t, objectLiteralMethodWalkFixtureSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, fnName)
	var sink []abstractdomain.AbstractValue
	bodyCtx := *ctx
	bodyCtx.ReturnSink = &sink
	contract := &FunctionContract{Declaration: fn}
	AnalyzeFunction(&bodyCtx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("%s: AnalyzeFunction's ReturnSink caught nothing — the return statement never ran", fnName)
	}
	returned := sink[0]
	for _, v := range sink[1:] {
		returned = abstractdomain.JoinKnown(returned, v)
	}
	return returned
}

// TestObjectLiteralMethodWalkCall_TheReadAndWriteBumpLandsTheExactValue
// pins the walk-route (no kernel) read of `person.age` after
// `person.bump()`: the write must land as the exact 41, not an
// unknown/opaque residue left by an unbound `this`.
func TestObjectLiteralMethodWalkCall_TheReadAndWriteBumpLandsTheExactValue(t *testing.T) {
	value := objectLiteralMethodWalkCallRun(t, "literalWritingMethod")
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 41 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("person.age after person.bump() = %q, want the exact bumped value 41", spelled)
	}
}

// TestObjectLiteralMethodWalkCall_TheConstantWriteSpoilLandsExactly pins
// the constant-write twin beside the read-and-write case, so a
// regression in the shared fold-back shows on both at once.
func TestObjectLiteralMethodWalkCall_TheConstantWriteSpoilLandsExactly(t *testing.T) {
	value := objectLiteralMethodWalkCallRun(t, "literalSpoilMethod")
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 200 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("outlaw.age after outlaw.spoil() = %q, want the exact written value 200", spelled)
	}
}

// objectLiteralMethodWalkTargetClassCallSource is a class method called
// through a direct `new` receiver — the negative control
// objectLiteralMethodWalkTarget must decline for.
const objectLiteralMethodWalkTargetClassCallSource = "class ThisPerson {\n" +
	"  age = 40;\n" +
	"  years(): number {\n" +
	"    return this.age;\n" +
	"  }\n" +
	"}\n" +
	"function thisFieldRead(): number {\n" +
	"  return new ThisPerson().years();\n" +
	"}\n"

// TestObjectLiteralMethodWalkTarget_DeclinesForAClassMethod is the
// negative control: a class instance method's `this.key` reads already
// answer through the class field invariant (readThisFieldInvariant), so
// objectLiteralMethodWalkTarget must not claim one — only a method row
// of an OBJECT LITERAL qualifies.
func TestObjectLiteralMethodWalkTarget_DeclinesForAClassMethod(t *testing.T) {
	p := entryEnvTestProgram(t, objectLiteralMethodWalkTargetClassCallSource)
	ctx := superArrayContracts(t, p)
	years := superArrayClassMethod(t, p, "ThisPerson", "years")
	call := superArrayNewMethodCall(t, p.Entry.AsNode(), "ThisPerson", "years")
	if call == nil {
		t.Fatalf("no `new ThisPerson().years()` call in the fixture")
	}
	_, _, ok := objectLiteralMethodWalkTarget(ctx, NewEnv(), call, years)
	if ok {
		t.Errorf("objectLiteralMethodWalkTarget claimed a class method call — it must decline for anything but an object-literal method row")
	}
}
