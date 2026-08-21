// The premise-conformance corpus, run through THIS side's reader.
//
// docs/one-checker/premise-unification.md names the deliverable: a
// hand-built fact-artifact corpus — valid and broken, each broken row
// naming exactly ONE premise — read by BOTH consumers' test suites.
// Cross-language code cannot be shared between the Go and Rust
// readers; the corpus (packages/refinedts/edge-premise-fixtures/) and
// its manifest are the shared part.
//
// SCOPE: every row this file exercises is a premise
// ReadForeignArtifact itself discharges — the envelope, target
// integrity, runtime band, surface shape, and the kernel's set
// decoder. Two manifest rows (crossing-fit, NaN-admission) are NOT
// reader premises at all: they are judged against the WALK's crossing
// value by checkOutboundLeg, which needs a live kernel and an Env this
// reader-focused file does not construct — those rows are exercised by
// the PRE-EXISTING TestCheckOutboundLeg_ElementsOutsideTheStatedEntryFireAtTheCall
// and TestCheckOutboundLeg_APossiblyNaNCrossingFiresNamingTheStringifyBehaviour
// above in foreign_edge_test.go. The stdout-not-pure row IS read here,
// but only for the flag ReadForeignArtifact carries through — the
// decline sentence naming channel purity is built one layer up, in
// ForeignEdgeAt, and is exercised by that function's own existing
// tests, not duplicated here. See the corpus's own manifest.json and
// README.md for the full accounting.
package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// conformanceFixturesDir answers the corpus directory's absolute path,
// derived from THIS test file's own location via runtime.Caller —
// never an environment variable (ambient process state must never
// configure behavior; the standing rule every producer/consumer path
// resolution in this package already follows).
func conformanceFixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) did not resolve this test file's path")
	}
	// this file: .../packages/refinedts/refined-ts-go/internal/refinedts/walk/foreign_edge_artifact_conformance_test.go
	// the corpus: .../packages/refinedts/edge-premise-fixtures/
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "edge-premise-fixtures")
	if _, statErr := os.Stat(filepath.Join(dir, "manifest.json")); statErr != nil {
		t.Fatalf("the conformance corpus manifest was not found at %s: %v", dir, statErr)
	}
	return dir
}

// conformanceManifest mirrors manifest.json's shape — only the fields
// this test reads.
type conformanceManifest struct {
	Rows []conformanceRow `json:"rows"`
}

type conformanceRow struct {
	ID            string            `json:"id"`
	Artifact      string            `json:"artifact"`
	TargetSource  string            `json:"targetSource"`
	PremiseBroken string            `json:"premiseBroken"`
	Verdict       string            `json:"verdict"`
	KeyPhrase     map[string]string `json:"keyPhrase"`
	ExercisedBy   []string          `json:"exercisedBy"`
}

// loadConformanceManifest reads and parses manifest.json from the
// corpus directory.
func loadConformanceManifest(t *testing.T, dir string) conformanceManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("reading manifest.json: %v", err)
	}
	var manifest conformanceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing manifest.json: %v", err)
	}
	return manifest
}

// exercisedByGoReader answers whether a manifest row names this side
// ("go-reader") among what exercises it — the rows whose premise lives
// one layer above the reader (crossing-fit, NaN-admission) name only
// the existing edge-level tests, and this loop must skip them rather
// than force them through a harness that cannot construct a crossing
// value.
func exercisedByGoReader(row conformanceRow) bool {
	for _, who := range row.ExercisedBy {
		if strings.HasPrefix(who, "go-reader") {
			return true
		}
	}
	return false
}

// materializeConformanceRow writes the row's artifact (with {{HASH}}
// substituted against the REAL sha256 of its paired target-source,
// computed at run time — a hash cannot be hand-pasted and stay
// honest) into a fresh temp project root, and answers the target path
// to read the artifact against.
func materializeConformanceRow(t *testing.T, dir string, row conformanceRow) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marking the temp root: %v", err)
	}
	SetProjectRootOverride(root)
	t.Cleanup(func() { SetProjectRootOverride("") })

	artifactText, err := os.ReadFile(filepath.Join(dir, row.Artifact))
	if err != nil {
		t.Fatalf("reading %s: %v", row.Artifact, err)
	}

	// the target's extension follows the artifact's OWN declared language —
	// a row with no target-source (it declines before target integrity)
	// still needs a real file at the resolved cache path, and defaults to
	// .py, since the placeholder's content is never actually read
	targetName := "audio_level.py"
	if strings.Contains(string(artifactText), `"language": "typescript"`) {
		targetName = "audio_level.ts"
	}
	targetPath := filepath.Join(root, targetName)

	if row.TargetSource != "" {
		sourceText, sourceErr := os.ReadFile(filepath.Join(dir, row.TargetSource))
		if sourceErr != nil {
			t.Fatalf("reading %s: %v", row.TargetSource, sourceErr)
		}
		if err := os.WriteFile(targetPath, sourceText, 0o644); err != nil {
			t.Fatalf("writing the target: %v", err)
		}
		sum := sha256.Sum256(sourceText)
		hash := "sha256:" + hex.EncodeToString(sum[:])
		artifactText = []byte(strings.ReplaceAll(string(artifactText), "{{HASH}}", hash))
	} else {
		// rows that decline before the hash check (kind/version) carry no
		// paired target-source; write a placeholder target so a read
		// attempt that DID reach the hash check would fail loudly rather
		// than silently pass against a missing file
		if err := os.WriteFile(targetPath, []byte("placeholder — this row declines before target integrity\n"), 0o644); err != nil {
			t.Fatalf("writing the placeholder target: %v", err)
		}
	}

	artifactPath := ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache directory: %v", err)
	}
	if err := os.WriteFile(artifactPath, artifactText, 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })
	forgetForeignArtifact(targetPath)
	return targetPath
}

// TestConformanceCorpus_EveryRowReadsAsTheManifestExpects iterates
// packages/refinedts/edge-premise-fixtures/manifest.json and runs each
// reader-level row through ReadForeignArtifact, asserting the verdict
// class (consumed vs. declined) and — for a decline — that the
// sentence contains the manifest's own recorded key phrase.
func TestConformanceCorpus_EveryRowReadsAsTheManifestExpects(t *testing.T) {
	dir := conformanceFixturesDir(t)
	manifest := loadConformanceManifest(t, dir)

	for _, row := range manifest.Rows {
		if !exercisedByGoReader(row) {
			continue
		}
		t.Run(row.ID, func(t *testing.T) {
			targetPath := materializeConformanceRow(t, dir, row)
			artifact, sentence := ReadForeignArtifact(targetPath)

			switch row.Verdict {
			case "consumed":
				if sentence != "" {
					t.Fatalf("row %q (premise %q) expected to be consumed, but declined: %s", row.ID, row.PremiseBroken, sentence)
				}
				if artifact == nil {
					t.Fatalf("row %q expected a fact, got nil with no decline sentence", row.ID)
				}
			case "declined":
				if sentence == "" {
					t.Fatalf("row %q (premise %q) expected to decline, but was consumed", row.ID, row.PremiseBroken)
				}
				phrase := row.KeyPhrase["go"]
				if phrase == "" {
					t.Fatalf("row %q declined verdict has no manifest keyPhrase.go to check against", row.ID)
				}
				if !strings.Contains(sentence, phrase) {
					t.Errorf("row %q sentence %q does not contain the manifest's key phrase %q", row.ID, sentence, phrase)
				}
			case "consumed-with-flag-false":
				if sentence != "" {
					t.Fatalf("row %q expected to be consumed (with stdoutPure false carried through), but declined: %s", row.ID, sentence)
				}
				if artifact == nil {
					t.Fatalf("row %q expected a fact, got nil with no decline sentence", row.ID)
				}
				if artifact.Called.Return.StdoutPure {
					t.Errorf("row %q expected Called.Return.StdoutPure == false, got true", row.ID)
				}
			default:
				t.Fatalf("row %q states an unhandled verdict class %q", row.ID, row.Verdict)
			}
		})
	}
}
