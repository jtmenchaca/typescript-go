// Pins the EXACT do-while exit value loop_fixpoint_do_while_guard_test.go
// left loose. That file's own comment says the guard-bounded exit reads
// {2,3} where the runtime-exact answer is {2}: the loop exits exactly
// when `age < 2` is FALSE, i.e. age >= 2, met against the post-step
// state — and since every trip of `age = age + 1` from the exact entry
// 0 produces an exact per-trip value (1, then 2), the union those trips
// build is itself exact, so the meet with the refuted guard is exact
// too: {1,2} ∩ {>= 2} = {2}.
//
// This file asks the question loop_fixpoint_do_while_guard_test.go
// deliberately left open (its own comment: "not proved exact"). It
// does not change SolveLoop — a companion pin, run by the parent, not
// by this agent (see the task's ban on go test/build/vet/run).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestSolveLoop_ADoWhileExitValueIsExactNotJustBounded exercises the
// same source as the guard test's second row (age: Age = 0..120
// integer, `do { age = age + 1 } while (age < 2)`), but asks for the
// EXACT runtime answer rather than the loose "well below the ceiling"
// bound the existing test settles for. Concretely: 0 -> 1 (test 1<2
// true) -> 2 (test 2<2 false, exit). The only value `age` can hold
// after the loop is 2.
func TestSolveLoop_ADoWhileExitValueIsExactNotJustBounded(t *testing.T) {
	env, reports := doWhileGuardTestBody(t, `
function f(): number {
  let age: number = 0;
  do {
    age = age + 1;
  } while (age < 2);
  return age;
}
`)
	if len(reports) != 0 {
		t.Fatalf("reports = %+v, want none", reports)
	}
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("env[age] missing after the loop")
	}
	formatted, _ := abstractdomain.FormatAbstractValue(held)
	t.Logf("age after the loop = %s", formatted)
	r := RangeOfKnown(held)
	if r == nil {
		t.Fatalf("RangeOfKnown(age) = nil, want a bounded range (held = %s)", formatted)
	}
	if r.Lo != 2 || r.Hi != 2 {
		t.Errorf("age after the loop = %s (lo=%v hi=%v), want exactly {2} — "+
			"the refuted guard (age < 2 failed) met against the exact "+
			"post-step union {1,2} leaves only 2", formatted, r.Lo, r.Hi)
	}
}
