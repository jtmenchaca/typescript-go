// Bisect instrument for the arraySort kernel-loaded divergence
// (AGENT-BRIEF.md's "syntax-wave facts"; c-reads-and-values.ts:780).
//
// Five exhaustive static traces across three waves found no defect on
// the walk route: collection_read_leftovers_test.go pins the isolated
// shape passing kernel-loaded; the dispatch chain, place-entries,
// aliasing, scheduling, and kernel Decimal have all been ruled out; the
// sibling arrayReverse row (same reader, same gates, same file) passes.
// This file does not re-trace — it runs the checker's own SERVICE door
// (Check, the judge's door) over a MATRIX of reproductions, each
// isolating one variable the static trace could not rule out by
// reading alone: sibling-function contract registration, the sorted
// pair's own order, and the local binding's name. A case that fires
// where its isolated walk twin is silent localizes the divergence to
// whatever that case changed; a case that stays silent rules its own
// variable out.
//
// Skipped without the native kernel dylib (requireDylib,
// syntax_wave_kernel_divergence_test.go, same package) — every case
// here is ABOUT the kernel-loaded pipeline, so there is nothing to
// reproduce without it.

package service

import (
	"fmt"
	"strings"
	"testing"
)

// TestArraySortBisect_ExactTextAlone reproduces arraySort's own fixture
// text with NO sibling function in the file at all — the minimal
// single-function case. A firing here would confirm the divergence
// needs no sibling contract at all, ruling out every cross-function
// registration hypothesis (b) below would otherwise carry; a SILENT
// result here still leaves those hypotheses open, since the judge's
// own multi-function fixture is what the brief's traces never isolated
// down to one function.
func TestArraySortBisect_ExactTextAlone(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arraySort(): Age {\n" +
		"  const ages = [41, 40];\n" +
		"  ages.sort();\n" +
		"  const good: Age = ages[0];\n" +
		"  return good;\n" +
		"}\n" +
		"void arraySort;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("arraySort alone (no siblings) reported %+v — want silence; a fire here rules OUT every sibling-contract hypothesis", d)
	}
}

// TestArraySortBisect_WithFixtureNeighbors reproduces arraySort beside
// its EXACT fixture neighbors, verbatim: arrayConcat immediately
// before it, arrayReverse immediately after — the same three functions
// sitting together in c-reads-and-values.ts. If this fires but the
// alone case above does not, the divergence needs one of these two
// specific neighbors present (their own contracts registering, their
// own summaries composing) — confirming a cross-function hypothesis
// the brief's single-function traces could not see. If this STAYS
// silent, the divergence needs the FULL fixture file (every function
// in c-reads-and-values.ts, not just these two neighbors) to trigger,
// which would point at scheduling order or a farther-away contract
// rather than adjacency.
func TestArraySortBisect_WithFixtureNeighbors(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arrayConcat(): Age {\n" +
		"  const ages = [40, 41];\n" +
		"  const more = ages.concat([42]);\n" +
		"  const good: Age = more[0];\n" +
		"  void good;\n" +
		"  const overMore = ages.concat([200]);\n" +
		"  return overMore[2];\n" +
		"}\n" +
		"function arraySort(): Age {\n" +
		"  const ages = [41, 40];\n" +
		"  ages.sort();\n" +
		"  const good: Age = ages[0];\n" +
		"  void good;\n" +
		"  const overs = [201, 200];\n" +
		"  overs.sort();\n" +
		"  return overs[0];\n" +
		"}\n" +
		"function arrayReverse(): Age {\n" +
		"  const ages = [40, 41];\n" +
		"  ages.reverse();\n" +
		"  const good: Age = ages[0];\n" +
		"  void good;\n" +
		"  const overs = [200, 201];\n" +
		"  overs.reverse();\n" +
		"  return overs[0];\n" +
		"}\n" +
		"void arrayConcat;\n" +
		"void arraySort;\n" +
		"void arrayReverse;\n"
	result := checkFor(t, source)
	// only arraySort's own good leg (the `ages[0]` in `const good: Age =
	// ages[0];`, right after `ages.sort();` — arraySort's own marked
	// over-leg `overs.sort()`/`overs[0]` further down is EXPECTED to
	// carry an error) is under test here; arrayConcat's own marked
	// over-leg expects an error too, and arrayReverse's good leg shares the SAME
	// `const good: Age = ages[0];` text (a different function, the same
	// local name), so the needle below includes the preceding
	// `ages.sort();` line to stay unique to arraySort's own occurrence
	assertSilentOnSubstring(t, source, result, "ages.sort();\n  const good: Age = ages[0];",
		"arraySort's good leg (ages[0]) beside its exact fixture neighbors")
}

// TestArraySortBisect_AscendingPairMovesNothing reproduces arraySort
// with the literal already in sorted order — [40, 41] instead of
// [41, 40] — so .sort() moves NOTHING at runtime (both elements
// compare in their already-ascending places under the default
// string-coercion comparator, since "40" < "41"). If THIS case stays
// silent while the original [41, 40] case fires, the divergence is
// keyed to the sort actually PERMUTING the backing slots (a weak
// update reading the wrong pre-sort/post-sort pairing); if this ALSO
// fires, the divergence has nothing to do with permutation at all —
// it is in .sort() being called, or in the array's later index read,
// regardless of what sort.
func TestArraySortBisect_AscendingPairMovesNothing(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arraySort(): Age {\n" +
		"  const ages = [40, 41];\n" +
		"  ages.sort();\n" +
		"  const good: Age = ages[0];\n" +
		"  return good;\n" +
		"}\n" +
		"void arraySort;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("arraySort with an already-ascending pair ([40, 41]) reported %+v — want silence; a fire here rules OUT the permutation hypothesis", d)
	}
}

// TestArraySortBisect_ArrayReverseAlone is the PASSING CONTROL:
// arrayReverse alone, the same single-function shape as
// TestArraySortBisect_ExactTextAlone but with .reverse() in place of
// .sort() — the sibling row AGENT-BRIEF.md and the prior waves' traces
// found passing in the full fixture file. Reproduced here in the SAME
// minimal single-function shape as the sort case above so the two
// results are comparable apples-to-apples: if arraySort-alone fires
// and arrayReverse-alone does not, the divergence is in .sort()'s own
// reading (or its wire lowering) and not in anything upstream both
// methods share (array construction, the element read, the Age
// annotation, the kernel-loaded dispatch chain itself).
func TestArraySortBisect_ArrayReverseAlone(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arrayReverse(): Age {\n" +
		"  const ages = [40, 41];\n" +
		"  ages.reverse();\n" +
		"  const good: Age = ages[0];\n" +
		"  return good;\n" +
		"}\n" +
		"void arrayReverse;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("arrayReverse alone reported %+v — want silence; this is the passing control both other cases are measured against", d)
	}
}

// TestArraySortBisect_RenamedBinding reproduces arraySort's exact
// shape with the local renamed from `ages` to `years` — a fresh
// spelling with no other function in the corpus using it. If this
// stays silent while the ORIGINAL name (`ages`) fires in the alone
// case above, the divergence is keyed to something NAME-INDEXED
// (a memo cache keyed by spelling, a slot-name collision with another
// tracked binding somewhere in the checker's own shared state) rather
// than to the shape of the sort-then-read construct itself.
func TestArraySortBisect_RenamedBinding(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arraySort(): Age {\n" +
		"  const years = [41, 40];\n" +
		"  years.sort();\n" +
		"  const good: Age = years[0];\n" +
		"  return good;\n" +
		"}\n" +
		"void arraySort;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("arraySort with the local renamed to `years` reported %+v — want silence; a fire here rules OUT the name-keyed-state hypothesis, a silent result here while the `ages`-named case fires rules it IN", d)
	}
}

// assertSilentOnSubstring asserts no reported diagnostic's own Start
// offset (RefinementDiagnostic carries a byte Start/Length, never a
// line) falls inside the given substring's span in source — used
// where a source file carries OTHER spans expected to carry errors (a
// marked @refinedts-expect-error row beside the one good leg under
// test), so a bare "want zero diagnostics" loop would be the wrong
// assertion. needle must be unique in source, or the wrong occurrence
// could be checked silently.
func assertSilentOnSubstring(t *testing.T, source string, result CheckResult, needle string, label string) {
	t.Helper()
	start := strings.Index(source, needle)
	if start < 0 {
		t.Fatalf("%s: needle %q not found in source — the test's own source text drifted from the assertion", label, needle)
	}
	if strings.Count(source, needle) != 1 {
		t.Fatalf("%s: needle %q is not unique in source — the assertion cannot tell which occurrence a diagnostic belongs to", label, needle)
	}
	end := start + len(needle)
	for _, d := range result.Refinements {
		if d.Start >= start && d.Start < end {
			t.Errorf("%s: a diagnostic landed inside %q (offsets [%d,%d)): %+v, want silence", label, needle, start, end, d)
		}
	}
}

// byteRange locates needle's own [start,end) byte span in source —
// the same uniqueness discipline assertSilentOnSubstring's own lookup
// uses, factored out so assertFiresOnSubstring below can share it.
// needle must be unique in source, or the wrong occurrence could be
// checked silently.
func byteRange(source, needle string) (start, end int) {
	start = strings.Index(source, needle)
	if start < 0 {
		panic(fmt.Sprintf("byteRange: needle %q not found in source", needle))
	}
	if strings.Count(source, needle) != 1 {
		panic(fmt.Sprintf("byteRange: needle %q is not unique in source", needle))
	}
	return start, start + len(needle)
}

// assertFiresOnSubstring asserts exactly one reported diagnostic's own
// Start offset falls inside [start,end) and that its Code matches want
// — the positive twin of assertSilentOnSubstring, for a leg the
// source's own marked position expects to fire.
func assertFiresOnSubstring(t *testing.T, result CheckResult, label string, want int, start, end int) {
	t.Helper()
	var inside []int
	for _, d := range result.Refinements {
		if d.Start >= start && d.Start < end {
			inside = append(inside, d.Code)
		}
	}
	if len(inside) != 1 {
		t.Fatalf("%s: %d diagnostics landed inside offsets [%d,%d) (codes %v), want exactly one", label, len(inside), start, end, inside)
	}
	if inside[0] != want {
		t.Errorf("%s: diagnostic inside [%d,%d) has Code %d, want %d", label, start, end, inside[0], want)
	}
}
