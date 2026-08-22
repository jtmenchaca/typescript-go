// Pins WritesNothingToStdout against the shapes fact_export.rs's own
// writes_nothing_to_stdout test battery covers: arithmetic-only purity,
// a direct stdout write, the stderr carve-out, same-file transitive
// recursion, an opaque (imported) call, and a pure-builtin whose own
// callback is where the dirt actually is.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// factExportPurityFunction parses source and answers the top-level
// function declaration named name, reusing entryEnvTestProgram /
// entryEnvFunctionNamed's own recipe (entry_env_test.go).
func factExportPurityFunction(t *testing.T, source string, name string) (*program.CheckerProgram, *ast.Node) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, name)
	return p, fn
}

func TestWritesNothingToStdout_ArithmeticOnlyBodyIsPure(t *testing.T) {
	p, fn := factExportPurityFunction(t, "function f(x: number) { return x * x + 1; }\n", "f")
	if !WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = false, want true for an arithmetic-only body")
	}
}

func TestWritesNothingToStdout_ConsoleLogRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, "function f(x: number) { console.log(x); return x; }\n", "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false when the body calls console.log")
	}
}

func TestWritesNothingToStdout_ConsoleErrorIsStderrNotStdout(t *testing.T) {
	p, fn := factExportPurityFunction(t, "function f(x: number) { console.error(x); return x; }\n", "f")
	if !WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = false, want true — console.error writes stderr, not stdout")
	}
}

func TestWritesNothingToStdout_SameFileHelperThatLogsRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function helper(x: number) { console.log(x); return x; }
function f(x: number) { return helper(x); }
`, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — the same-file helper it calls logs to stdout")
	}
}

func TestWritesNothingToStdout_ImportedCallIsOpaque(t *testing.T) {
	p := entryEnvTestProgram(t, `
import { helper } from "./helper";
function f(x: number) { return helper(x); }
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — a call to an imported function is opaque")
	}
}

func TestWritesNothingToStdout_MathSqrtAndCleanMapIsPure(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(xs: number[]) {
  const roots = xs.map(x => Math.sqrt(x));
  return roots.length;
}
`, "f")
	if !WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = false, want true for Math.sqrt plus a clean .map callback")
	}
}

func TestWritesNothingToStdout_MapCallbackThatLogsRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(xs: number[]) {
  const roots = xs.map(x => { console.log(x); return x; });
  return roots.length;
}
`, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — the .map callback logs to stdout")
	}
}

// TestWritesNothingToStdout_PadStartIsPure pins the fix for the
// ledgered defect (padStart read as an opaque call, refusing channel
// purity): String(year).padStart(4, "0") must read as stdout-pure —
// padStart's own ECMA-262 algorithm (RequireObjectCoercible then
// StringPaddingBuiltinsImpl, sec-string.prototype.padstart) carries no
// I/O semantics, and isPureBuiltinCall's method allowlist now admits
// it alongside the other pure string methods.
func TestWritesNothingToStdout_PadStartIsPure(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(year: number) {
  const padded = String(year).padStart(4, "0");
  return padded + "-01-01T00:00:00Z";
}
`, "f")
	if !WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = false, want true — padStart carries no I/O semantics in ECMA-262")
	}
}

// TestWritesNothingToStdout_AnOpaqueMethodStillRefusesTheClaim pins
// the conservative-only-admits posture alongside the padStart fix: a
// method this scan does not recognize (an arbitrary receiver method
// outside the allowlist) still refuses the claim rather than being
// swept in by the widened list.
func TestWritesNothingToStdout_AnOpaqueMethodStillRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(x: SomeLogger) {
  x.emitToRemote("hello");
  return 1;
}
`, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — emitToRemote is not on the pure-builtin allowlist and must read as opaque")
	}
}

// TestWritesNothingToStdout_ExecFileSyncSpawnIsPure pins the fix for
// the ledgered conservative-wrong refusal (ISSUES.md, "Go export: the
// stdout-purity guard refuses any body that spawns a child"): a body
// whose only "impurity" is an execFileSync call must read as stdout-
// pure, since execFileSync's default stdio pipes the child's own
// stdout back as this call's return value rather than writing the
// parent's stdout — the chain_boost.ts shape, reproduced here as a
// small inline source rather than the corpus file.
func TestWritesNothingToStdout_ExecFileSyncSpawnIsPure(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(samples: number[]) {
  const stdout = execFileSync("python3", ["./leaf.py"], {
    input: JSON.stringify(samples),
    encoding: "utf8",
  });
  return JSON.parse(stdout);
}
`, "f")
	if !WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = false, want true — execFileSync pipes the child's stdout back as this call's own return value, never writing the parent's stdout")
	}
}

// TestWritesNothingToStdout_ExecFileSyncWithInheritedStdioRefusesTheClaim
// pins the one shape that defeats the captured-stdout admission: an
// explicit `stdio: "inherit"` sends the child's stdout to the SAME
// stdout the JSON wire channel must carry alone, so this must still
// refuse.
func TestWritesNothingToStdout_ExecFileSyncWithInheritedStdioRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(samples: number[]) {
  const stdout = execFileSync("python3", ["./leaf.py"], {
    input: JSON.stringify(samples),
    stdio: "inherit",
  });
  return stdout;
}
`, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — an explicit stdio: \"inherit\" sends the child's stdout to the parent's own stdout")
	}
}

// TestWritesNothingToStdout_SpawnSyncWithInheritedStdioArrayRefusesTheClaim
// pins the array form of the same defeat: `stdio: [..., "inherit",
// ...]` names index 1 (the stdout slot) as inherited.
func TestWritesNothingToStdout_SpawnSyncWithInheritedStdioArrayRefusesTheClaim(t *testing.T) {
	p, fn := factExportPurityFunction(t, `
function f(samples: number[]) {
  const result = spawnSync("python3", ["./leaf.py"], {
    input: JSON.stringify(samples),
    stdio: ["pipe", "inherit", "pipe"],
  });
  return result.stdout;
}
`, "f")
	if WritesNothingToStdout(p.Entry, fn) {
		t.Errorf("WritesNothingToStdout = true, want false — stdio[1] (the stdout slot) is \"inherit\"")
	}
}
