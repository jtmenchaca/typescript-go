// The pooling seam (issue #35): a file's verdict must be a pure
// function of the file, its imports, and the kernel — never of which
// OTHER files happen to share a checker instance during a batch sweep.
// CheckFiles now hands every entry its own freshly built checker
// (check.go's per-row checker.NewChecker(p, nil) call), closing the
// drift class CHECKER-SEAMS.md's "Pooling mechanics" section
// documents. This test pins the observable contract: checking a file
// alone (CheckFile) and checking it alongside several siblings
// (CheckFiles) must report the identical diagnostics for that file,
// for every entry in the batch.

package service

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// seamSurfacePath is exportFactSurfacePath under a name local to this
// test's own concern — the same real on-disk surface/z.ts every disk
// fixture in this package recognizes, resolved once at package init.
var seamSurfacePath = exportFactSurfacePath

// writeSeamFixture writes one .ts fixture into dir, importing the real
// on-disk surface at seamSurfacePath — the same import shape
// writeHarnessFixture uses, minus the stdin/stdout harness wrapper
// this test does not need.
func writeSeamFixture(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	source := `import * as z from "` + seamSurfacePath + `";

` + body
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
	return path
}

// seamCodes reduces a CheckResult to a sorted list of refinement
// codes — enough to compare "alone" against "in a batch" without
// pinning byte-exact message text this test does not otherwise care
// about.
func seamCodes(result CheckResult) []int {
	codes := make([]int, len(result.Refinements))
	for i, d := range result.Refinements {
		codes[i] = d.Code
	}
	sort.Ints(codes)
	return codes
}

func codesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCheckFiles_AloneAndInABatchAgree is the issue #35 reproducer,
// made a permanent regression gate: a band-refinement fixture (the
// same in-range/out-of-range shape as the e2e A1.guard.band row) whose
// designated error must fire whether it is checked by itself or
// alongside nine unrelated sibling files sharing the batch's program.
// Before the per-entry fresh checker, a shared checker's accumulated
// caches (cachedTypes, narrowedTypes, subtypeReductionCache, and the
// rest of checker.NewChecker's per-instance maps) could carry from one
// file's walk into the next's, and this exact case missed its
// designated error once folded into a multi-file batch.
func TestCheckFiles_AloneAndInABatchAgree(t *testing.T) {
	requireExportFactKernel(t)
	dir := t.TempDir()

	// the target: an Age-typed return outside the declared band fires
	// RTS7001 at the marked line — identical to A1.guard.band.ts's
	// bandOutside shape, minus the expect-error marker (this test reads
	// the raw diagnostics itself rather than riding the marker
	// comparator).
	targetBody := `const zAge = z.number().min(0).max(150);
type Age = z.infer<typeof zAge>;
const zWide = z.number().min(0).max(200);
type Wide = z.infer<typeof zWide>;

function bandOutside(x: Wide): Age {
  if (100 <= x && x <= 200) {
    return x;
  }
  return 0;
}
void bandOutside;
`
	targetPath := writeSeamFixture(t, dir, "target.ts", targetBody)

	// alone: this is the ground truth the batch must match.
	alone, err := CheckFile(targetPath, seamSurfacePath)
	if err != nil {
		t.Fatalf("CheckFile(target alone): %v", err)
	}
	aloneCodes := seamCodes(alone)
	sawFire := false
	for _, code := range aloneCodes {
		if code == 7001 {
			sawFire = true
		}
	}
	if !sawFire {
		t.Fatalf("the target fixture must fire 7001 when checked alone (fixture drifted from the A1.guard.band shape), got codes %v", aloneCodes)
	}

	// nine unrelated siblings — each independently well-typed, each
	// importing the same surface, so they land in the target's program
	// group and the target's batch worker gets a checker that has
	// already answered questions for some of them (longest-first
	// scheduling in CheckFiles puts the biggest file first; several
	// short siblings here to vary which goroutine reaches the target).
	entries := []string{targetPath}
	for i := 0; i < 9; i++ {
		siblingBody := `const zN` + string(rune('A'+i)) + ` = z.number().min(0).max(` + siblingBound(i) + `);
type N` + string(rune('A'+i)) + ` = z.infer<typeof zN` + string(rune('A'+i)) + `>;

function identity` + string(rune('A'+i)) + `(x: N` + string(rune('A'+i)) + `): N` + string(rune('A'+i)) + ` {
  return x;
}
void identity` + string(rune('A'+i)) + `;
`
		siblingPath := writeSeamFixture(t, dir, "sibling"+string(rune('A'+i))+".ts", siblingBody)
		entries = append(entries, siblingPath)
	}

	results := CheckFiles(entries, seamSurfacePath)

	for _, entry := range entries {
		result, ok := results[entry]
		if !ok {
			t.Fatalf("CheckFiles reported no row for %s", entry)
		}
		if entry != targetPath {
			continue
		}
		batchCodes := seamCodes(result)
		if !codesEqual(aloneCodes, batchCodes) {
			t.Fatalf(
				"the target's verdict drifted inside the batch — alone: %v, in a %d-file batch: %v (checker pooling contaminated the target's walk)",
				aloneCodes, len(entries), batchCodes,
			)
		}
	}
}

// siblingBound spreads the sibling fixtures' bounds so their checker
// work is not byte-identical to each other or to the target.
func siblingBound(i int) string {
	bounds := []string{"10", "20", "30", "40", "50", "60", "70", "80", "90"}
	return bounds[i%len(bounds)]
}
