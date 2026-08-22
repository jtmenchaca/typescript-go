// The runtime depth probe ISSUES.md's wall-crash entry left open: the
// Lean deciders' recursion was audited structural (no elaboration
// depth pressure exists in the sequence family), but whether the
// COMPILED dylib answers at the bridge's own 64-node admission cap
// (kernelbridge's wireNestingCap) was never measured. This test feeds
// a genuinely 64-deep right-nested concatenation through the native
// kernel and pins that the ask ANSWERS — no native stack overflow, no
// refusal — at exactly the deepest wire the bridge admits. Skipped,
// never faked, when the dylib is absent.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestKernelDepthProbe_ASixtyFourDeepWireAnswersAtTheAdmissionCap(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	// A: a 64-codepoint literal — FormatGrammar compiles it to a
	// right-nested concatenation of 64 singleton segments, the deepest
	// chain shape the bridge's 64-node cap admits.
	literal := "^" + strings.Repeat("a", 64) + "$"
	compiledA := refinementsets.FormatGrammar(literal, "")
	if !compiledA.Ok {
		t.Fatalf("FormatGrammar(64-codepoint literal): unsupported — %s", compiledA.Unsupported)
	}

	// B: the same 64 positions as one exact-length alphabet segment —
	// the aligned walk must consume all 64 of A's singletons against
	// B's one run, the deepest runtime recursion this family reaches.
	compiledB := refinementsets.FormatGrammar(`^[a-z]{64}$`, "")
	if !compiledB.Ok {
		t.Fatalf("FormatGrammar([a-z]{64}): unsupported — %s", compiledB.Unsupported)
	}

	answer := func() (subset bool, refused bool) {
		defer func() {
			if recover() != nil {
				refused = true
			}
		}()
		return kernel.SeqSubset(compiledA.Set, compiledB.Set), false
	}
	subset, refused := answer()
	if refused {
		t.Fatalf("kernel.SeqSubset REFUSED (panicked) on the 64-deep wire — the runtime depth ceiling sits at or below the bridge's own admission cap")
	}
	if !subset {
		t.Fatalf("kernel.SeqSubset(64×'a', [a-z]×64) = false — want true; the kernel answered but wrongly")
	}
}
