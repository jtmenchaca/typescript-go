// The kernel seam this package's lattice operations ask through.
//
// JoinKnown and Truthiness take no kernel parameter — they are called
// from six production sites across walk/ and from the domain's own
// constructors, and threading a handle through every one of them would
// reach far past the lattice. The package holds the kernel the same way
// narrowing/ does (type_guard_recognizers.go's narrowKernelHolder): one
// process-wide pointer, set by the checker's run() before any walk,
// read atomically so a parallel sweep's writers (all storing the SAME
// pointer) are safe.
//
// Without a kernel set — a unit test that skipped setup — every ask
// here answers "not decided" and the caller keeps the local answer it
// had before. Degraded, never wrong.
package abstractdomain

import (
	"sync/atomic"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var latticeKernelHolder atomic.Pointer[kernelbridge.RefinedTSKernel]

// LatticeKernel is the kernel the lattice operations pose their
// questions through, or nil where none was handed in.
func LatticeKernel() *kernelbridge.RefinedTSKernel {
	return latticeKernelHolder.Load()
}

// SetLatticeKernel hands this package the kernel, alongside the
// narrowing package's own SetNarrowKernel at the same setup point.
func SetLatticeKernel(kernel *kernelbridge.RefinedTSKernel) {
	latticeKernelHolder.Store(kernel)
}

// kernelNoScalarReread asks the kernel whether a set's language misses
// the 1-tuple layer entirely — the reread-safety property the string-
// word join rests on. (safe=true, ok=true) is the kernel's theorem;
// ok=false is a refusal (no kernel, a question it declined, OR the
// kernel's own `false` answer — kernel_interface.go's SeqNoScalarReread
// field states its OWN contract plainly: "`false` is a decline that
// proves nothing, and the caller keeps its own conservative answer
// there" — the kernel asks one recognized shape's own recursion and
// answers `false` for a shape it did not walk (e.g. a union of two
// concatenations, this join's own everyday shape), not for a shape it
// proved UNSAFE. Reading that `false` as a proof let a plain three-
// member string-literal array join to Unknown the moment a kernel was
// seated (empty_word_join_test.go's own literal-array cases determined
// fine with no kernel; the identical join failed once
// SetLatticeKernel ran) — the local recursion (statesOnlyLongSequences)
// already proves the SAME shape safe, so a kernel `false` must fall
// through to it exactly as an outright refusal would, never override
// it as though the kernel had proved the opposite.
//
// A refused question panics through the bridge, exactly as every other
// kernel ask does; recover() turns that back into ok=false.
func kernelNoScalarReread(set refinementsets.RefinedSet) (safe bool, ok bool) {
	kernel := LatticeKernel()
	if kernel == nil || kernel.SeqNoScalarReread == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			safe, ok = false, false
		}
	}()
	if kernel.SeqNoScalarReread(set) {
		return true, true
	}
	return false, false
}

// kernelBounds asks the kernel's Bounds question — the integral hull of
// a scalar set (refined_bounds, boundary/exports_bounds.lean): the
// set's own EMPTY verdict, or its least/greatest members for a
// nonempty integral set with finite edges, else its proved enclosure
// unchanged. (result, ok=true) is the kernel's answer; ok=false is a
// refusal (no kernel, or the set is not scalar-shaped — kernelBounds's
// own "bounds are decided for scalar sets today"), and the caller keeps
// its own syntactic reading there, never a guess.
//
// A refused question panics through the bridge, exactly as every other
// kernel ask does; recover() turns that back into ok=false.
func kernelBounds(set refinementsets.RefinedSet) (result kernelbridge.BoundsResult, ok bool) {
	kernel := LatticeKernel()
	if kernel == nil || kernel.Bounds == nil {
		return kernelbridge.BoundsResult{}, false
	}
	defer func() {
		if recover() != nil {
			result, ok = kernelbridge.BoundsResult{}, false
		}
	}()
	return kernel.Bounds(set), true
}

// kernelSeqSubset asks the kernel whether a ⊆ b over recognized
// sequence shapes — JoinKnown's string-ground absorption arm
// (`"xxx"` ⊆ Strings, so the join answers Strings rather than
// declining) rests on this. (contains=true, ok=true) is the kernel's
// theorem (seqSubsetB_true); ok=false is a refusal (no kernel, or
// either side not a recognized sequence shape), and the caller keeps
// its own path — never a syntactic guess at which side is bigger.
//
// A refused question panics through the bridge, exactly as every
// other kernel ask does; recover() turns that back into ok=false.
func kernelSeqSubset(a, b refinementsets.RefinedSet) (contains bool, ok bool) {
	kernel := LatticeKernel()
	if kernel == nil || kernel.SeqSubset == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			contains, ok = false, false
		}
	}()
	return kernel.SeqSubset(a, b), true
}

// TruthinessDecided is Truthiness with the kernel's proved truthiness
// narrowing standing behind it.
//
// Truthiness itself takes no kernel and stays exactly as it is: every
// arm it DECIDES is unchanged, and no decided answer costs a question.
// This variant differs only where Truthiness declines on a set- or
// multi-value-shaped operand — the determination gaps the conformance
// ledger names (truthiness_conformance_test.go's gap-1..gap-6) — and
// asks the kernel there.
//
// The verdict is read off the two filtered sides exactly as the
// conformance harness's kernelTruthiness reads it: a side that admits
// NOTHING decides the verdict outright, and two live sides are the
// honest undecided. A refusal anywhere leaves the local answer.
func TruthinessDecided(k AbstractValue) (bool, bool) {
	if value, known := Truthiness(k); known {
		return value, known
	}
	set, sort, ok := truthinessOperand(k)
	if !ok {
		return false, false
	}
	return kernelTruthiness(set, sort)
}

// truthinessOperand reads the set and the narrowing sort for the value
// shapes whose truthiness the kernel can decide: a plain untagged set,
// or a multi-value word. A bigint- or symbol-tagged set, an object
// kind, and everything else answer ok=false — the kernel's value
// vocabulary stops at scalars and sequences, so there is no question to
// ask for them.
//
// The sort comes from TypeofWordOfKnown, the domain's own sort reader:
// a sequence-shaped set reads "string" and takes js.truthyStr; a
// scalar set takes js.truthyNum.
func truthinessOperand(k AbstractValue) (refinementsets.RefinedSet, string, bool) {
	switch k.Kind {
	case KindSet:
		if k.SetKindTag != SetKindTagNone {
			return refinementsets.RefinedSet{}, "", false
		}
		if TypeofWordOfKnown(k) == "string" {
			return k.Set, "js.truthyStr", true
		}
		return k.Set, "js.truthyNum", true
	case KindValues:
		// the arm Truthiness answers for a SINGLE value only; a
		// multi-value word declines there even when every member agrees
		if k.KindTag == PrimitiveString {
			return refinementsets.RefinedSet{}, "", false
		}
		if k.KindTag == PrimitiveArray {
			return refinementsets.RefinedSet{}, "", false
		}
		if len(k.Values) < 2 {
			return refinementsets.RefinedSet{}, "", false
		}
		return refinementsets.MakeRefinedSet(refinementsets.OneOf(k.Values)),
			"js.truthyNum", true
	default:
		return refinementsets.RefinedSet{}, "", false
	}
}

// kernelTruthiness reads the kernel's proved verdict for one set under
// one truthiness sort — the same reading the conformance harness makes:
// NarrowState answers the two filtered sides, and a side admitting
// nothing decides the verdict.
//
// The state carries no absent or NaN admission: the shapes
// truthinessOperand hands over are plain sets and words, whose absent
// halves live in a wrapper kind Truthiness has already declined.
func kernelTruthiness(set refinementsets.RefinedSet, op string) (value bool, known bool) {
	kernel := LatticeKernel()
	if kernel == nil || kernel.NarrowState == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			value, known = false, false
		}
	}()
	whenTrue, whenFalse := kernel.NarrowState(
		kernelbridge.KnownStateWire{Set: set}, op, 0, false)
	trueDead := sideAdmitsNothing(kernel, whenTrue, op)
	falseDead := sideAdmitsNothing(kernel, whenFalse, op)
	if falseDead && !trueDead {
		return true, true
	}
	if trueDead && !falseDead {
		return false, true
	}
	// both live is the honest undecided; both dead admits nothing at
	// all, which is not a verdict either
	return false, false
}

// sideAdmitsNothing is whether a filtered side holds no value at all —
// the kernel's own emptiness decider on the side's set, AND the side
// carrying no absent or NaN admission (undefined and NaN are falsy
// values a set-empty side would still admit).
func sideAdmitsNothing(
	kernel *kernelbridge.RefinedTSKernel,
	side kernelbridge.KnownStateWire,
	op string,
) bool {
	if side.Top {
		return false
	}
	if side.Undef || side.Null || side.Nan {
		return false
	}
	if op == "js.truthyStr" {
		return kernel.SeqEmpty(side.Set)
	}
	return kernel.ScalarEmpty(side.Set)
}
