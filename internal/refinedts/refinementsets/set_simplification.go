// Making a set SAY what it is, rather than how it was built.
//
// A set accumulates forms as the analysis works: a loop's certified
// invariant is a union, the loop's condition narrows it, a guard adds
// another bound. Each step is exact and none of them tidies up, so a
// counter that is plainly {0, 1, 2} arrives carrying
// `< 3, (0 ∪ >= 1 && <= 3 && integer)`. Every fact is true; together
// they take a moment to read, and a hover does not have one.
//
// The rule here is that this side PROPOSES and the kernel DISPOSES. A
// simpler set is only ever accepted when the kernel has proved it
// EQUAL to the original -- subset in both directions, two questions it
// already answers. Nothing is simplified on this side's own authority,
// so a wrong proposal costs a refused replacement rather than a wrong
// answer.
//
// Two proposals, in order:
//
//	THE HULL. The kernel answers it (bounds): the least and greatest
//	members of a nonempty integral set, found on the proved code in
//	exact integer arithmetic -- every bisection step is a proved
//	emptiness verdict, so no float ever touches an edge.
//	`< 3, (0 ∪ [1,3]∩Z)` answers `[0,2]∩Z`, and the kernel proves the
//	proposal equal before it is accepted.
//
//	THE ENUMERATION. Where the hull is integral and small, ask which
//	of its members the set actually holds, and propose exactly those.
//	{0, 1, 2} comes back and the kernel proves it equal. A run of
//	consecutive integers then reads as a range again, so the display
//	gets `integer, 0 <= x <= 2` rather than `0 | 1 | 2`.
//
// Cost is one question for the hull plus two for its equality, and one
// per candidate member for the enumeration -- all cached by the
// question store, and the enumeration only runs on a set the hull
// already bounded.

package refinementsets

import "math"

// SimplificationKernel is the kernel seam this file needs: bounds,
// members, and the two-way subset check that proves equality. A
// locally-defined minimal interface, not the full RefinedTSKernel
// surface (kernel_bridge/kernel_interface.ts) -- kernel_bridge is
// ported after refinement_sets (see PORT.md's port order), so this
// package cannot import it. Whatever package does port kernel_bridge
// can satisfy this interface directly; nothing here needs to change.
type SimplificationKernel interface {
	// ScalarSubset is A subset-of B on the 1-tuple layer.
	ScalarSubset(a, b RefinedSet) bool
	// Bounds is the integral hull of a scalar set; Empty true means the
	// empty set, Hull is meaningful only when Empty is false. Panics on
	// a kernel refusal (the TS throw).
	Bounds(set RefinedSet) BoundsResult
	// Members is the members of a small integral scalar set, each
	// confirmed by the proved membership decider. Panics (the TS
	// throw) when the hull exceeds cap.
	Members(set RefinedSet, cap int) []float64
}

// BoundsResult is the kernel's bounds(...) answer: the TS source's
// `{ empty: true } | { empty: false; hull: RefinedSet }`.
type BoundsResult struct {
	Empty bool
	Hull  RefinedSet
}

// Cache-tuning constants transcribed from service/cache_tuning.ts
// (ENUMERATED_MEMBERS, NAMED_MEMBERS) -- speed/display-width settings,
// not architecture, so they are inlined here rather than pulled in via
// an import of a directory later in the port order (service/ is not
// ported yet, and is not on refinement_sets' side of the port-order
// dependency graph regardless). Keep these equal to the TS source's
// values if that file's numbers ever change.
const (
	// enumeratedMembers is how wide an integral set may be before the
	// checker stops asking which values it holds, one at a time, to say
	// it more plainly.
	enumeratedMembers = 64
	// namedMembers is how many values a set may be shown AS, when they
	// do not run consecutively.
	namedMembers = 6
)

// hull is Hull in the TS source.
type hull struct {
	lo       float64
	hi       float64
	integral bool
}

// hullOf is the outermost bounds the forms state, read syntactically.
// A union widens to the loosest of its sides; a difference reads only
// its left, since removing members never widens. ok=false where a
// form states no bound the reading can place.
func hullOf(r RefinedSet) (hull, bool) {
	lo := math.Inf(-1)
	hi := math.Inf(1)
	integral := false
	for _, f := range r.Forms {
		switch f.Form {
		case FormAtLeast, FormAbove:
			lo = math.Max(lo, f.A)
		case FormAtMost, FormBelow:
			hi = math.Min(hi, f.A)
		case FormInteger:
			integral = true
		case FormOneOf:
			if len(f.W) == 0 {
				return hull{}, false
			}
			lo = math.Max(lo, minFloatSlice(f.W))
			hi = math.Min(hi, maxFloatSlice(f.W))
			integral = integral || allIntegerValues(f.W)
		case FormMultipleOf:
			// narrows, never widens: no bound to read
		case FormUnion:
			a, aOk := hullOf(*f.A_)
			b, bOk := hullOf(*f.B)
			if !aOk || !bOk {
				return hull{}, false
			}
			lo = math.Max(lo, math.Min(a.lo, b.lo))
			hi = math.Min(hi, math.Max(a.hi, b.hi))
			integral = integral || (a.integral && b.integral)
		case FormDifference:
			a, aOk := hullOf(*f.A_)
			if !aOk {
				return hull{}, false
			}
			lo = math.Max(lo, a.lo)
			hi = math.Min(hi, a.hi)
			integral = integral || a.integral
		default:
			return hull{}, false // a sequence form: not a scalar hull
		}
	}
	if math.IsInf(lo, 0) || math.IsInf(hi, 0) || lo > hi {
		return hull{}, false
	}
	return hull{lo: lo, hi: hi, integral: integral}, true
}

func minFloatSlice(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxFloatSlice(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func allIntegerValues(xs []float64) bool {
	for _, x := range xs {
		if !isIntegerValue(x) {
			return false
		}
	}
	return true
}

// asHullSet is the set a hull states. One value is not a range -- it
// is that value, which the type language can say on its own. Named
// asHullSet (the TS source's asSet) to avoid colliding with this
// package's other set constructors.
func asHullSet(h hull) RefinedSet {
	if h.integral && h.lo == h.hi {
		return MakeRefinedSet(OneOf([]float64{h.lo}))
	}
	if h.integral {
		return MakeRefinedSet(AtLeast(h.lo), AtMost(h.hi), Integer)
	}
	return MakeRefinedSet(AtLeast(h.lo), AtMost(h.hi))
}

// provedEqual asks: does the kernel prove these two sets hold exactly
// the same values? Subset in both directions. A kernel panic (the TS
// throw) is a no.
func provedEqual(kernel SimplificationKernel, a, b RefinedSet) (result bool) {
	defer func() {
		if recover() != nil {
			result = false
		}
	}()
	return kernel.ScalarSubset(a, b) && kernel.ScalarSubset(b, a)
}

// membersOf is the members of an integral hull the set actually holds
// -- one kernel question, each member confirmed by the proved
// membership decider -- or ok=false where the hull is too wide to
// enumerate.
func membersOf(kernel SimplificationKernel, r RefinedSet) (result []float64, ok bool) {
	defer func() {
		if recover() != nil {
			result, ok = nil, false
		}
	}()
	return kernel.Members(r, enumeratedMembers), true
}

// SimplifyScalar is a set that holds the same values, said more
// plainly -- or the set itself where nothing simpler was proved.
func SimplifyScalar(kernel SimplificationKernel, r RefinedSet) RefinedSet {
	// one form is already as plain as it gets
	if len(r.Forms) <= 1 {
		return r
	}

	// the hull comes from the KERNEL: exact integer arithmetic, every
	// edge step justified by the proved emptiness decider -- one
	// question where a float-side search cost a hundred and diverged
	// past 2^53. A refusal proposes nothing.
	answered, ok := boundsSafe(kernel, r)
	if !ok {
		return r
	}
	// read the edges back off the answered wire -- flat closed forms --
	// for the proposal shape and the enumeration below
	h, hOk := hullOf(answered)
	if !hOk {
		return r
	}

	asHull := asHullSet(h)
	if provedEqual(kernel, r, asHull) {
		return asHull
	}

	if !h.integral {
		return r
	}
	members, membersOk := membersOf(kernel, r)
	if !membersOk || len(members) == 0 {
		return r
	}
	// one value is that value; a consecutive run is a range, however
	// long; a scattered handful is the values themselves -- but only a
	// handful, since a long list of them reads worse than the forms it
	// would replace
	consecutive := true
	for i := 1; i < len(members); i++ {
		if members[i] != members[i-1]+1 {
			consecutive = false
			break
		}
	}
	if consecutive || len(members) <= namedMembers {
		var proposal RefinedSet
		if len(members) == 1 || !consecutive {
			proposal = MakeRefinedSet(OneOf(members))
		} else {
			proposal = MakeRefinedSet(AtLeast(members[0]), AtMost(members[len(members)-1]), Integer)
		}
		if provedEqual(kernel, r, proposal) {
			return proposal
		}
		return r
	}

	// too many values to name -- but a set that is nearly a range is
	// better said as the range and what it LEAVES OUT. The integers 0
	// to 19 without 1 is two facts; naming nineteen values is nineteen.
	var missing []float64
	for v := members[0]; v <= members[len(members)-1]; v++ {
		if !floatsInclude(members, v) {
			missing = append(missing, v)
		}
	}
	if len(missing) == 0 || len(missing) > namedMembers {
		return r
	}
	withoutThem := MakeRefinedSet(Difference(
		MakeRefinedSet(AtLeast(members[0]), AtMost(members[len(members)-1]), Integer),
		MakeRefinedSet(OneOf(missing)),
	))
	if provedEqual(kernel, r, withoutThem) {
		return withoutThem
	}
	return r
}

// boundsSafe wraps the kernel's Bounds call, folding a panic (the TS
// throw) into ok=false, and an empty-set answer into ok=false too (the
// TS source's `if (bounds.empty) return R;`).
func boundsSafe(kernel SimplificationKernel, r RefinedSet) (result RefinedSet, ok bool) {
	defer func() {
		if recover() != nil {
			result, ok = RefinedSet{}, false
		}
	}()
	bounds := kernel.Bounds(r)
	if bounds.Empty {
		return RefinedSet{}, false
	}
	return bounds.Hull, true
}
