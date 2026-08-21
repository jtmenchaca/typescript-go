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
// ok=false is a refusal (no kernel, or a question it declined), and the
// caller falls back to its own conservative recursion there.
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
	return kernel.SeqNoScalarReread(set), true
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
