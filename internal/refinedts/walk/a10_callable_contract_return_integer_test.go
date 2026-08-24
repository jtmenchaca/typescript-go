// Pins the e2e membership corpus's A10 (callable contracts) false
// positives: a call through a value whose declared type is a
// FUNCTION TYPE naming an aliased refinement — `f: (x: Age) => Age`,
// `declare function f(x: Age): Age` — returns a value the checker
// reports as the range alone ('>= 0 && <= 150'), dropping the alias's
// `integer` conjunct, so the very next return into an Age-declared
// sink refuses with RTS7001 ("not provably an integer") even though
// the callee's own signature already states Age, integer included.
//
// A2.guard.eq's direct-narrowing sibling (an Age-typed local narrowed
// to a literal and returned) carries the conjunct through cleanly, so
// the loss is specific to the CALL path: EvaluateCallExpression's
// contract-less branches (a parameter bound to a callback, a
// `declare function`) fall through to UnmodeledCallResult, which asks
// AnnotationOfReturnType (return_type_ground.go) first — that reader
// SHOULD resolve the call's own resolved return type back through the
// Age alias's declaration and read integer with it. These cases pin
// that it currently does not, for both a function-typed PARAMETER
// call (A10.guard.eq's own shape) and a bodiless `declare function`
// call (A10.seed.declaration's shape) — the e2e rows this fix closes.
package walk

import (
	"testing"
)

const a10CallableContractHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(150);
type Age = z.infer<typeof zAge>;
`

// TestA10_CallThroughAgeTypedParameterKeepsIntegerConjunct pins
// A10.guard.eq: a call through a PARAMETER typed `(x: Age) => Age`
// must return the WHOLE of Age (integer, [0, 150]) — the range alone
// is not the callee's stated set.
func TestA10_CallThroughAgeTypedParameterKeepsIntegerConjunct(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := a10CallableContractHeader + `
function afterIdentityInside(f: (x: Age) => Age, x: Age): Age {
  return f(x);
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source, "afterIdentityInside"), "return f(x) through a (x: Age) => Age parameter")
}

// TestA10_CallThroughDeclaredAgeFunctionKeepsIntegerConjunct pins
// A10.seed.declaration: calling a body-less `declare function`
// returning Age must return the WHOLE of Age, not the range alone.
func TestA10_CallThroughDeclaredAgeFunctionKeepsIntegerConjunct(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := a10CallableContractHeader + `
declare function declaredIdentity(x: Age): Age;
function useDeclaredInside(x: Age): Age {
  return declaredIdentity(x);
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source, "useDeclaredInside"), "return declaredIdentity(x), a bodiless declared Age function")
}
