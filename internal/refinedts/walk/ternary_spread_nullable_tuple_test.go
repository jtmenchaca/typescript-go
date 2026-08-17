// Pins ternarySpreadCopiesNullableArray (c-reads-and-values.ts:1200):
// `item: [10, 20] | null`; `item != null ? [...item] : fallback`.
//
// Before typereading/host_type.go's tuple branch, a FIXED tuple type
// read through the same IsArrayLikeType branch a plain `number[]`
// uses — every element type joined into one star claim at an unstated
// length, so `item`'s seeded value (behind the `| null` maybe wrapper)
// was PossiblyUndefined(star), not PossiblyUndefined(exact tuple). The
// `!= null` guard's narrowing (narrowAt's "defined" case,
// narrowing/apply_narrowing.go) strips the wrapper and hands back
// exactly what Inner held — a star gave a star, so `[...item]` spread
// a star into the literal and `rows[0]` read only the joined {10, 20}
// set, undetermined against a target excluding one of them.
//
// The fix reads a tuple whose every position is ElementFlagsRequired
// positionally instead, building an exact KindList — `item`'s Inner is
// now the exact [10, 20] tuple, the guard hands it back whole, the
// spread flattens it into the literal exactly (array_literal.go's
// existing KindList spread arm, unchanged), and the element read off
// the tracked `rows` binding answers the exact slot
// (element_access.go's KindList arm, also unchanged).
package walk

import "testing"

// TestTernarySpreadCopiesNullableArray_ElementReadsExactly pins the
// fixture's own two functions: item narrowed under != null reads
// exactly 10 at rows[0]; a same-shaped tuple holding 200/201 (never
// actually null, so the ternary's true arm alone decides) reads
// exactly 200 at overRows[0], out of a target that admits only 10.
func TestTernarySpreadCopiesNullableArray_ElementReadsExactly(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(item: [10, 20] | null): 10 | 11 {\n" +
		"  const fallback: [10, 20] = [10, 20];\n" +
		"  const rows = item != null ? [...item] : fallback;\n" +
		"  return rows[0];\n" +
		"}\n" +
		"function g(): 10 | 11 {\n" +
		"  const fallback: [10, 20] = [10, 20];\n" +
		"  const over: [200, 201] | null = [200, 201];\n" +
		"  const overRows = over != null ? [...over] : fallback;\n" +
		"  return overRows[0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "item != null ? [...item] : fallback; rows[0] (want 10)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "over != null ? [...over] : fallback; overRows[0] (want 200, out of {10,11})")
}
