// from conformance/replay.test.ts
//
// The correspondence table, audited by replay (CORRESPONDENCE.md,
// definitional tier): sampled instances of the checker's own set
// algebra run side by side with the kernel's derived-chain seam, and
// the two answers must agree — narrowing is intersection, joining is
// union, refuted equality is difference. The sample grid is
// deterministic (boundaries on both sides of every bound), so CI
// runs are reproducible; the CI step keeps this audit routine rather
// than available.

package conformance

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var (
	replayLOS    = []float64{-10, 0, 3}
	replayHIS    = []float64{5, 10, 100}
	replayGUARDS = []float64{-5, 4, 50}
	replayPROBES = []float64{-20, -5, 0, 3, 4, 5, 7, 50, 100, 200}
)

func replayKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

func TestGuardNarrowingReplaysCheckerIntersectionEqualsKernelChain(t *testing.T) {
	kernel := replayKernel(t)
	for _, lo := range replayLOS {
		for _, hi := range replayHIS {
			if hi < lo {
				continue
			}
			root := refinementsets.MakeRefinedSet(refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
			for _, g := range replayGUARDS {
				guard := refinementsets.AtLeast(g)
				narrowedValue := abstractdomain.NarrowKnown(
					abstractdomain.KnownSet(root, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
					[]refinementsets.Refinement{guard},
				)
				narrowed, ok := abstractdomain.SetOfKnown(narrowedValue)
				if !ok {
					t.Fatalf("SetOfKnown(narrowed) = false, want true")
				}
				for _, probe := range replayPROBES {
					checker := kernel.Member(narrowed, []float64{probe})
					replayed := kernel.ValidateChain(kernelbridge.Chain{
						Root: root,
						Ops: []kernelbridge.ChainOp{
							{Op: kernelbridge.ChainOpIntersectWith, Set: refinementsets.MakeRefinedSet(guard)},
							{Op: kernelbridge.ChainOpMemberQ, Tuple: []float64{probe}},
						},
					})
					if replayed.Kind != kernelbridge.ValidateChainAnswer || replayed.Answer != checker {
						t.Errorf("validateChain(intersectWith(%v).memberQ(%v)) = %+v, want answer %v", g, probe, replayed, checker)
					}
				}
			}
		}
	}
}

func TestBranchJoinsReplayCheckerUnionEqualsKernelChain(t *testing.T) {
	kernel := replayKernel(t)
	for _, lo := range replayLOS {
		for _, hi := range replayHIS {
			if hi < lo {
				continue
			}
			left := refinementsets.MakeRefinedSet(refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
			for _, g := range replayGUARDS {
				right := refinementsets.MakeRefinedSet(refinementsets.AtLeast(g), refinementsets.AtMost(g+20))
				joinedValue := abstractdomain.JoinKnown(
					abstractdomain.KnownSet(left, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
					abstractdomain.KnownSet(right, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
				)
				joined, ok := abstractdomain.SetOfKnown(joinedValue)
				if !ok {
					t.Fatalf("SetOfKnown(joined) = false, want true")
				}
				for _, probe := range replayPROBES {
					checker := kernel.Member(joined, []float64{probe})
					replayed := kernel.ValidateChain(kernelbridge.Chain{
						Root: left,
						Ops: []kernelbridge.ChainOp{
							{Op: kernelbridge.ChainOpUnionWith, Set: right},
							{Op: kernelbridge.ChainOpMemberQ, Tuple: []float64{probe}},
						},
					})
					if replayed.Kind != kernelbridge.ValidateChainAnswer || replayed.Answer != checker {
						t.Errorf("validateChain(unionWith(%v,%v).memberQ(%v)) = %+v, want answer %v", g, g+20, probe, replayed, checker)
					}
				}
			}
		}
	}
}

// There is NO cost gate on either side (JT's ruling 2026-08-09):
// the kernel answers every readable question. The adversarial shape
// that used to be refused — and after the gate's removal briefly
// exhausted the module — now ANSWERS: the disjunction deduplicates
// at every union and negation step (set_functions/emptiness.lean
// dedupD, membership-preserving, so every satisfaction theorem
// carried unchanged), and a self-similar tower collapses instead of
// squaring per layer.
func TestAnAdversarialScalarDNFTowerAnswers(t *testing.T) {
	kernel := replayKernel(t)
	// a SCALAR union tower: 8 doublings of the same set — the dedup
	// collapses each layer, and the subset question decides
	tower := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(9), refinementsets.Integer,
		refinementsets.OneOf([]float64{1, 2, 3}),
	)
	for i := 0; i < 8; i++ {
		tower = refinementsets.MakeRefinedSet(refinementsets.Union(tower, tower), refinementsets.Integer)
	}
	if got := kernel.ScalarSubset(tower, tower); !got {
		t.Errorf("scalarSubset(tower, tower) = %v, want true", got)
	}
	// ...and refutation still works through the tower: {1,2,3} is
	// no subset of a set missing 2
	if got := kernel.ScalarSubset(tower, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 3}))); got {
		t.Errorf("scalarSubset(tower, {1,3}) = %v, want false", got)
	}
	// MEMBERSHIP walks derivatives linearly in the syntax — a star
	// tower of the same height answers outright
	starTower := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(9), refinementsets.Integer,
		refinementsets.OneOf([]float64{1, 2, 3}),
	)
	for i := 0; i < 6; i++ {
		starTower = refinementsets.MakeRefinedSet(refinementsets.Star(starTower))
	}
	if got := kernel.Member(starTower, []float64{1}); !got {
		t.Errorf("member(starTower, [1]) = %v, want true", got)
	}
}

func TestRefutedEqualityReplaysCheckerDifferenceEqualsKernelChain(t *testing.T) {
	kernel := replayKernel(t)
	for _, lo := range replayLOS {
		for _, hi := range replayHIS {
			if hi < lo {
				continue
			}
			root := refinementsets.MakeRefinedSet(refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
			for _, g := range replayGUARDS {
				removed := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{g}))
				// the rule's defining equation: narrowed = the difference
				narrowed := refinementsets.MakeRefinedSet(refinementsets.Difference(root, removed))
				for _, probe := range replayPROBES {
					checker := kernel.Member(narrowed, []float64{probe})
					replayed := kernel.ValidateChain(kernelbridge.Chain{
						Root: root,
						Ops: []kernelbridge.ChainOp{
							{Op: kernelbridge.ChainOpDifferenceWith, Set: removed},
							{Op: kernelbridge.ChainOpMemberQ, Tuple: []float64{probe}},
						},
					})
					if replayed.Kind != kernelbridge.ValidateChainAnswer || replayed.Answer != checker {
						t.Errorf("validateChain(differenceWith(%v).memberQ(%v)) = %+v, want answer %v", g, probe, replayed, checker)
					}
				}
			}
		}
	}
}
