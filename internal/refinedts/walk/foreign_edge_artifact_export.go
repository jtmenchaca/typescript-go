// Resolving and running the producer that fills a missing or stale
// artifact: the project root, the producer binary's own resolution
// order, the auto-export cycle guard, and the export run itself.

package walk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ForeignCacheArtifactPath resolves the target's cache entry: the
// nearest ancestor holding `.git` is the project root (the target's
// own directory when none is found), and the entry mirrors the
// target's path relative to that root. Exported: service/export_fact.go
// derives its own default `-o` from this same rule, so the two
// checkers meet at one file without either being told where.
func ForeignCacheArtifactPath(targetPath string) string {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		return targetPath + ForeignArtifactSuffix
	}
	root := projectRootOf(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(abs)
	}
	return filepath.Join(root, ForeignCacheDir, rel+ForeignArtifactSuffix)
}

// projectRootOverrideMu guards projectRootOverride, mirroring
// explicitProducerPyPath's discipline: a plain setter, never an
// environment variable.
var (
	projectRootOverrideMu sync.Mutex
	projectRootOverride   string
)

// SetProjectRootOverride states the project root outright (typically
// cmd/refinedts-check's `-project-root` flag, set by a caller — the
// `refined` front door — that already resolved it), bypassing the
// `.git`-walk below for both the cache path and producer resolution.
// "" (the default) restores the walk.
func SetProjectRootOverride(root string) {
	projectRootOverrideMu.Lock()
	defer projectRootOverrideMu.Unlock()
	projectRootOverride = root
}

// projectRootOf is the nearest ancestor of an absolute path holding
// `.git` — the target's own directory when none is found — unless
// SetProjectRootOverride named the root outright. Shared by
// ForeignCacheArtifactPath (where the cache entry lives) and
// exportForeignArtifact (where a project-local producer build lives),
// so the two never derive the root two different ways.
func projectRootOf(abs string) string {
	projectRootOverrideMu.Lock()
	override := projectRootOverride
	projectRootOverrideMu.Unlock()
	if override != "" {
		return override
	}
	root := filepath.Dir(abs)
	for dir := filepath.Dir(abs); ; {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return root
}

// explicitProducerPyPathMu guards explicitProducerPyPath, mirroring
// kernelbridge.SetDylibPath's own discipline: a plain setter, never an
// environment variable — behavior is configured by arguments (a
// binary's `-producer-py` flag) or the binary's own layout, never by
// ambient process state.
var (
	explicitProducerPyPathMu sync.Mutex
	explicitProducerPyPath   string
)

// SetPythonProducerPath states where the refinedpy-check binary lives,
// for a caller that already knows (typically cmd/refinedts-check's
// `-producer-py` flag). Resolution otherwise falls through to a
// project-root build, then PATH — see exportForeignArtifact.
func SetPythonProducerPath(path string) {
	explicitProducerPyPathMu.Lock()
	defer explicitProducerPyPathMu.Unlock()
	explicitProducerPyPath = path
}

// resolveProducerPyPath answers the refinedpy-check binary to run, in
// order: the caller-stated path (SetPythonProducerPath), a release
// build under the project root, a debug build under the project root,
// then whatever `refinedpy-check` PATH resolves to. "" means none of
// the four held — no environment variable is read at any step (the
// standing rule: ambient process state never configures behavior).
func resolveProducerPyPath(targetPath string) string {
	explicitProducerPyPathMu.Lock()
	explicit := explicitProducerPyPath
	explicitProducerPyPathMu.Unlock()
	if explicit != "" {
		return explicit
	}
	if abs, err := filepath.Abs(targetPath); err == nil {
		root := projectRootOf(abs)
		for _, profile := range []string{"release", "debug"} {
			candidate := filepath.Join(root, "packages", "refinedpy", "pyrefly", "target", profile, "refinedpy-check")
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate
			}
		}
	}
	if found, err := exec.LookPath("refinedpy-check"); err == nil {
		return found
	}
	return ""
}

// ExportChainEnvVar is the environment variable carrying the
// cross-process auto-export chain: a colon-separated list of absolute
// target paths, one per export hop already in flight. This is internal
// state no invocation reads on purpose — it governs WHETHER an
// auto-export spawns, never WHICH binary runs, and is therefore a
// wholly separate concern from resolveProducerPyPath's own
// no-environment-variable rule for the PRODUCER'S identity (ambient
// process state never configures WHICH binary runs). A TypeScript
// checker auto-exporting a Python target whose own auto-export
// recurses back to a TypeScript target already on this chain would
// otherwise spawn forever, each hop a fresh process neither side's own
// in-memory recursion guard can see across.
const ExportChainEnvVar = "REFINED_EXPORT_CHAIN"

// exportChainContains answers whether targetPath's absolute form
// already appears as a hop in chain (the colon-separated
// REFINED_EXPORT_CHAIN value read at this process's own entry point) —
// true means spawning the producer for this target would recurse back
// through a hop already in flight, and the caller must decline rather
// than spawn.
func exportChainContains(chain string, targetPath string) bool {
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		absoluteTarget = targetPath
	}
	for _, hop := range strings.Split(chain, ":") {
		if hop == "" {
			continue
		}
		if hop == absoluteTarget {
			return true
		}
	}
	return false
}

// exportChainCycleSentence is the sentence a chain-marked decline
// states: names the recursing target and the whole chain that led back
// to it, so a reader sees the cycle rather than a generic refusal.
func exportChainCycleSentence(chain string, targetPath string) string {
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		absoluteTarget = targetPath
	}
	hops := make([]string, 0, 4)
	for _, hop := range strings.Split(chain, ":") {
		if hop != "" {
			hops = append(hops, hop)
		}
	}
	hops = append(hops, absoluteTarget)
	return "the export of " + absoluteTarget + " recurses back through a target already in flight " +
		"— the auto-export chain is " + strings.Join(hops, " → ")
}

// exportForeignArtifact runs the resolved producer into the cache
// entry, answering "" on success and one sentence naming what stopped
// it. Resolution: resolveProducerPyPath's three-step order, above.
// exportChain is this process's own REFINED_EXPORT_CHAIN value (read
// once, at the point the spawn decision is made, and threaded down here
// as a plain parameter — never re-read from the environment inside
// this function, which is what keeps it directly testable) — when
// targetPath already appears on it, this declines with the cycle
// sentence rather than spawning; otherwise the CHILD's own environment
// carries the chain plus targetPath appended, so a nested auto-export
// the child triggers sees the extended chain in turn.
func exportForeignArtifact(targetPath string, artifactPath string, exportChain string) string {
	if exportChainContains(exportChain, targetPath) {
		return exportChainCycleSentence(exportChain, targetPath)
	}
	producer := resolveProducerPyPath(targetPath)
	if producer == "" {
		return "no -producer-py flag, no built refinedpy-check under the project root, and none on PATH"
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return "the cache directory could not be created: " + err.Error()
	}
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		absoluteTarget = targetPath
	}
	childChain := absoluteTarget
	if exportChain != "" {
		childChain = exportChain + ":" + absoluteTarget
	}
	command := exec.Command(producer, "--export-fact", targetPath, "-o", artifactPath)
	command.Env = append(os.Environ(), ExportChainEnvVar+"="+childChain)
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "the export run failed: " + message
	}
	return ""
}
