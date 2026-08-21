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
