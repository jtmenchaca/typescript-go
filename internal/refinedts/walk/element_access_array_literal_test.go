// Pins ElementAccessOf's gap over a receiver that is a FRESH array
// literal built in place — `[...expr][0]` — rather than a tracked
// name or a call/element-access chain. Every arm in ElementAccessOf
// before this fix gated on `elem.Expression` being an identifier (the
// tracked-binding arm) or a CallExpression/ElementAccessExpression
// (the call-result arm); an ArrayLiteralExpression receiver matched
// neither, so the literal's own value (what EvaluateArrayLiteral
// computes for the spread) was never even evaluated — the whole
// element read fell through to the type-seeded fallback, which knows
// only the host TYPE at the position, not the walk's own exact
// knowledge.
//
// Widening the call-result arm's gate (element_access.go) to also
// admit an ArrayLiteralExpression receiver closes it: the arm already
// evaluates `elem.Expression` generically and indexes whatever
// KindList/KindValues comes back, which is exactly the shape
// EvaluateArrayLiteral produces for `[...ages.values()]` (a flat
// PrimitiveArray) and for `[...aSet]` (drained through
// collectionSpreadItems into the same flat shape).
//
// Four c-reads-and-values.ts §C rows share this exact construct:
//
//   - arrayKeysValuesEntries (806): `[...ages.values()][0]` — a
//     values() view over a tracked array, spread into a fresh literal.
//   - setAdd (850): `[...ages][0]` — a Set spread directly.
//   - setAlgebra (942): `[...a.union(b)][0]` — a Set algebra producer's
//     result (itself a KindCollection) spread.
//   - setConstructorForms (1126): `[...fromIterable][0]` — a
//     literal-constructed Set spread.
//
// Each case runs the real AnalyzeFunction pass, the same route the
// fixture's own @refinedts-expect-error rows are judged by, and
// asserts on reported diagnostics: an in-set case wants silence, an
// out-of-set case wants exactly one 7001 refutation. This file reuses
// collectionLeftoversKernel/Run/WantSilent/WantRefuted from
// collection_read_leftovers_test.go (same package) rather than
// redefining the harness.
package walk

import "testing"

// TestElementAccessOf_SpreadOfArrayValuesViewIntoAFreshLiteralIndexes
// pins arrayKeysValuesEntries (c-reads-and-values.ts:806/810):
// `[...ages.values()][0]`.
func TestElementAccessOf_SpreadOfArrayValuesViewIntoAFreshLiteralIndexes(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const ages = [40, 41];\n" +
		"  return [...ages.values()][0];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overs = [200, 201];\n" +
		"  return [...overs.values()][0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[...[40,41].values()][0] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[...[200,201].values()][0] (want 200, out of {40,41})")
}

// TestElementAccessOf_SpreadOfABuiltSetIntoAFreshLiteralIndexes pins
// setAdd (c-reads-and-values.ts:850/855): `[...ages][0]` after
// `ages.add(40)`.
func TestElementAccessOf_SpreadOfABuiltSetIntoAFreshLiteralIndexes(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const ages = new Set<number>();\n" +
		"  ages.add(40);\n" +
		"  return [...ages][0];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overs = new Set<number>();\n" +
		"  overs.add(200);\n" +
		"  return [...overs][0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[...Set{40}][0] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[...Set{200}][0] (want 200, out of {40,41})")
}

// TestElementAccessOf_SpreadOfASetUnionResultIntoAFreshLiteralIndexes
// pins setAlgebra (c-reads-and-values.ts:942/945):
// `[...a.union(b)][0]` where a={40} and b={200} — union's own order is
// this'-entries-then-other's (sec-set.prototype.union), so index 0 is
// always a's member (40) and index 1 is always b's (200); the target
// here is 40 only, so reading slot 1 is the out-of-set row, matching
// the fixture's own g()/setAlgebra shape (a fixed a/b pair, two reads
// at two different slots — never two different sets).
func TestElementAccessOf_SpreadOfASetUnionResultIntoAFreshLiteralIndexes(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 {\n" +
		"  const a = new Set<number>([40]);\n" +
		"  const b = new Set<number>([200]);\n" +
		"  return [...a.union(b)][0];\n" +
		"}\n" +
		"function g(): 40 {\n" +
		"  const a = new Set<number>([40]);\n" +
		"  const b = new Set<number>([200]);\n" +
		"  return [...a.union(b)][1];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[...{40}.union({200})][0] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[...{40}.union({200})][1] (want 200, out of {40})")
}

// TestElementAccessOf_SpreadOfAnIterableConstructedSetIntoAFreshLiteralIndexes
// pins setConstructorForms (c-reads-and-values.ts:1126/1130):
// `[...fromIterable][0]` where fromIterable = new Set<number>([40]).
func TestElementAccessOf_SpreadOfAnIterableConstructedSetIntoAFreshLiteralIndexes(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const fromIterable = new Set<number>([40]);\n" +
		"  return [...fromIterable][0];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overs = new Set<number>([200]);\n" +
		"  return [...overs][0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[...Set([40])][0] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[...Set([200])][0] (want 200, out of {40,41})")
}
