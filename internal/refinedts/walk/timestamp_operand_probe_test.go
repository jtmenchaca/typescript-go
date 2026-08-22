// A DIAGNOSTIC PROBE, not a pin: the two operands the g fixture's
// return-leg fit ask sends the kernel, and the kernel's own verbatim
// SeqSubset answer over them — asked directly, past the walk's own
// dispatch, to say whether the g-strings-and-formats.ts undetermined
// row (timestampViaPython, RTS7002 "the kernel declined the
// question") is an ASK-SITE gap (wrong operands, wrong route) or a
// genuine kernel-shape gap (the kernel refuses the real operands
// too). Skipped, never faked, when the native dylib is absent — the
// same gate every other kernel-backed walk test in this package
// uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestTimestampOperandProbe_ReturnLegSeqSubsetAgainstTheRealArtifactAndDeclaredSet
// builds operand A the same way the return leg actually does — reading
// targets/text_timestamp.py's own exported artifact through
// ReadForeignArtifact, the exact function foreign_edge.go's return-leg
// code calls — and operand B the same way the declared zTimestamp
// annotation actually does — refinementsets.FormatGrammar over the
// fixture's own regex literal text, the exact function
// annotations/chain_method.go's "regex" case calls. Then asks
// kernel.SeqSubset(A, B) directly and reports what comes back.
func TestTimestampOperandProbe_ReturnLegSeqSubsetAgainstTheRealArtifactAndDeclaredSet(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	// operand A: the artifact return set make_timestamp's own exported
	// fact carries — read through the SAME function foreign_edge.go's
	// return leg calls, off the SAME cache entry a real check run fills
	// (pnpm ts:check on g-strings-and-formats.ts, or pnpm export:fact:ts,
	// populates .refined/cache/.../text_timestamp.py.refined.json).
	targetPath := "/Users/jtmenchaca/TypeRefinery/packages/refinedts/tsc-vscode/fixtures/language/edge-coverage/targets/text_timestamp.py"
	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact == nil {
		t.Fatalf("ReadForeignArtifact(%q): no artifact — %s", targetPath, sentence)
	}
	if len(artifact.Called.Return.Cases) != 1 {
		t.Fatalf("make_timestamp's return states %d cases, want exactly 1", len(artifact.Called.Return.Cases))
	}
	operandA := artifact.Called.Return.Cases[0].Set

	// operand B: zTimestamp's own declared set — the SAME compile
	// chain_method.go's "regex" case runs: strip the /pattern/flags
	// delimiters, then FormatGrammar.
	pattern := `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`
	compiled := refinementsets.FormatGrammar(pattern, "")
	if !compiled.Ok {
		t.Fatalf("FormatGrammar(%q): unsupported — %s", pattern, compiled.Unsupported)
	}
	operandB := compiled.Set

	t.Logf("operand A (artifact return set, make_timestamp): %s", refinementsets.FormatForDiagnostics(operandA))
	t.Logf("operand B (declared zTimestamp set):              %s", refinementsets.FormatForDiagnostics(operandB))

	answer := func() (subset bool, refused bool) {
		defer func() {
			if recover() != nil {
				refused = true
			}
		}()
		return kernel.SeqSubset(operandA, operandB), false
	}
	subset, refused := answer()
	// PINNED: SUBSET HOLDS. The kernel used to REFUSE this exact pair —
	// A's sixteen right-nested singleton codepoint segments (after the
	// four-digit head) against B's grouped `Repeat(digit, exactly 2)`
	// segments, where B's segment boundaries do not fall at A's (each
	// B segment covers TWO of A's singletons). That was the genuine
	// kernel-shape gap this probe was written to distinguish from an
	// ask-site gap. `alignedSegSubsetB` (set_functions/
	// subset_seq_align.lean) now reads both sides as ordered
	// (element set, exact length) segment lists and proves the subset
	// by consuming length-matching RUNS of A's segments against each
	// of B's — the span split neither `seqLeB`'s one-to-one zip nor
	// the window/grammar-length routes could state.
	if refused {
		t.Fatalf("kernel.SeqSubset(A, B) REFUSED (panicked) — expected SUBSET HOLDS (alignedSegSubsetB)")
	}
	if !subset {
		t.Fatalf("kernel.SeqSubset(A, B) = false — expected true (SUBSET HOLDS, alignedSegSubsetB)")
	}
	t.Logf("kernel.SeqSubset(A, B) = %v", subset)
}
