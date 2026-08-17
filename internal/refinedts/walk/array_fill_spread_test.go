// Pins two j-stdlib-surfaces.ts rows that judged wrong before this
// unit — both over `Array(count).fill(value)` spread into a fresh
// literal, where the receiver is an INLINE call result rather than a
// tracked identifier:
//
//   - arrayFillSpreadElement (374): `[40, ...Array(2).fill(41)][1]`.
//     `.fill(41)` on `Array(2)`'s own untracked value fell through
//     readArrayWriteMethods (gated on site.HasTrackedName) straight to
//     the unmodeled-method havoc, so the spread's own items were never
//     known and the element read stayed undetermined. readFreshArrayFill
//     (array_method_models.go) answers `.fill(value)`'s VALUE directly
//     off the receiver — no tracked name needed, no env write — so
//     EvaluateArrayLiteral's existing KindList spread arm
//     (array_literal.go) composes the exact [40, 41, 41] tuple.
//   - arrayFillSpreadLength (390): the same construct read for
//     `.length` — the composed length is exact regardless of the fill
//     value, and a fill count large enough on its own (`Array(200)`)
//     composes a length past the Age ceiling.
//
// Each case runs the real AnalyzeFunction pass and asserts on reported
// diagnostics, reusing collectionLeftoversKernel/Run/WantSilent/
// WantRefuted from collection_read_leftovers_test.go (same package).
package walk

import "testing"

// TestReadFreshArrayFill_SpreadIntoALiteralReadsTheElementExactly pins
// arrayFillSpreadElement (j-stdlib-surfaces.ts:374/376/378/380):
// `[40, ...Array(2).fill(41)][1]`.
func TestReadFreshArrayFill_SpreadIntoALiteralReadsTheElementExactly(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const filled = [40, ...Array(2).fill(41)];\n" +
		"  return filled[1];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overFilled = [40, ...Array(2).fill(200)];\n" +
		"  return overFilled[1];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[40, ...Array(2).fill(41)][1] (want 41)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[40, ...Array(2).fill(200)][1] (want 200, out of {40,41})")
}

// TestReadFreshArrayFill_SpreadIntoALiteralReadsTheLengthExactly pins
// arrayFillSpreadLength (j-stdlib-surfaces.ts:390/392/394/396):
// `[40, ...Array(2).fill(41)].length`.
func TestReadFreshArrayFill_SpreadIntoALiteralReadsTheLengthExactly(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 3 | 4 {\n" +
		"  const filled = [40, ...Array(2).fill(41)];\n" +
		"  return filled.length;\n" +
		"}\n" +
		"function g(): 3 | 4 {\n" +
		"  const wide = [40, ...Array(200).fill(41)];\n" +
		"  return wide.length;\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[40, ...Array(2).fill(41)].length (want 3)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[40, ...Array(200).fill(41)].length (want 201, out of {3,4})")
}
