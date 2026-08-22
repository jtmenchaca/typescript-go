// The cross-process export-chain cycle guard, tested at the
// parameterized function directly. exportForeignArtifact takes the
// chain as a plain string parameter (never reading REFINED_EXPORT_CHAIN
// itself) so these tests exercise it with no process-environment
// mutation at all — the mirror of the Rust side's own testable-design
// choice in foreign_edge_artifact.rs.
package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A target whose absolute path already appears as a hop on the chain is
// recognized regardless of a relative spelling at the call site — the
// comparison is absolute-to-absolute.
func TestExportChainContains_FindsAHopByItsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.ts")
	if err := os.WriteFile(target, []byte("// placeholder\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	if !exportChainContains(absolute, target) {
		t.Fatalf("expected the chain to contain %q", target)
	}
	if exportChainContains(absolute, filepath.Join(root, "b.ts")) {
		t.Fatalf("expected the chain to NOT contain an unrelated target")
	}
	if exportChainContains("", target) {
		t.Fatalf("an empty chain must contain no hop")
	}
}

// A multi-hop chain is read as ':'-separated absolute paths; a target
// matching any hop (not only the last) is recognized.
func TestExportChainContains_ChecksEveryHopNotOnlyTheLast(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.ts")
	b := filepath.Join(root, "b.py")
	if err := os.WriteFile(a, []byte("// placeholder\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	absoluteA, _ := filepath.Abs(a)
	absoluteB, _ := filepath.Abs(b)
	chain := absoluteA + ":" + absoluteB

	if !exportChainContains(chain, a) {
		t.Fatalf("the first hop must be recognized")
	}
	if !exportChainContains(chain, b) {
		t.Fatalf("the last hop must be recognized")
	}
}

// The cycle sentence names the recursing target and renders the whole
// chain that led back to it, the recursing target appended last — a
// reader sees the exact cycle, not a generic refusal.
func TestExportChainCycleSentence_NamesTheRecursingTargetAndTheWholeChain(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.ts")
	b := filepath.Join(root, "b.py")
	if err := os.WriteFile(a, []byte("// placeholder\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	absoluteA, _ := filepath.Abs(a)
	absoluteB, _ := filepath.Abs(b)
	chain := absoluteA + ":" + absoluteB

	sentence := exportChainCycleSentence(chain, a)
	if !strings.Contains(sentence, "recurses back") {
		t.Errorf("sentence = %q", sentence)
	}
	if !strings.Contains(sentence, absoluteA) {
		t.Errorf("sentence = %q", sentence)
	}
	if !strings.Contains(sentence, absoluteB) {
		t.Errorf("sentence = %q", sentence)
	}
}

// A target already on the chain declines the spawn outright — the
// producer is never resolved or invoked, and the sentence names the
// cycle, exactly the ruling this guard exists to apply. The temp root
// carries no producer at all: if the guard failed to short-circuit, the
// failure would read as an unresolved-producer sentence instead, which
// this test's own assertion distinguishes from the cycle sentence it
// requires.
func TestExportForeignArtifact_AChainMarkedTargetDeclinesWithTheCycleSentence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marking the temp root: %v", err)
	}
	SetProjectRootOverride(root)
	t.Cleanup(func() { SetProjectRootOverride("") })

	target := filepath.Join(root, "a.py")
	if err := os.WriteFile(target, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	artifactPath := ForeignCacheArtifactPath(target)
	absoluteTarget, _ := filepath.Abs(target)

	sentence := exportForeignArtifact(target, artifactPath, absoluteTarget)
	if sentence == "" {
		t.Fatal("a target already on the chain must decline, not spawn")
	}
	if !strings.Contains(sentence, "recurses back") {
		t.Errorf("sentence = %q", sentence)
	}
	if !strings.Contains(sentence, absoluteTarget) {
		t.Errorf("sentence = %q", sentence)
	}
}

// A clean chain (no hop matching the target) behaves exactly as before
// this guard existed: with no producer resolvable in this temp root,
// the decline names the ordinary "no producer" reason, never the cycle
// sentence — the guard contributes nothing when the target is not
// already in flight.
func TestExportForeignArtifact_ACleanChainSpawnsAsTodayAndDeclinesOnTheOrdinaryNoProducerReason(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marking the temp root: %v", err)
	}
	SetProjectRootOverride(root)
	t.Cleanup(func() { SetProjectRootOverride("") })
	SetPythonProducerPath("")

	target := filepath.Join(root, "a.py")
	if err := os.WriteFile(target, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	artifactPath := ForeignCacheArtifactPath(target)

	sentence := exportForeignArtifact(target, artifactPath, "")
	if sentence == "" {
		t.Fatal("no producer resolves in this empty temp root, so the spawn attempt must decline")
	}
	if strings.Contains(sentence, "recurses back") {
		t.Errorf("sentence = %q", sentence)
	}
	if !strings.Contains(sentence, "refinedpy-check") {
		t.Errorf("sentence = %q", sentence)
	}
}
