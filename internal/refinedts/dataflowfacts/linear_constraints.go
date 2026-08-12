// The order ledger: difference bounds BETWEEN places, recorded from
// a guard (or a dependent signature) and consumed where the pair is
// read together. A row says "on every run reaching the consumption
// scope, minuend − subtrahend ≥ bound (strictly, when strict)" —
// the joint fact the per-position sets discard. A row is recorded
// only when both places stay STABLE through the consumption scope
// (places.ts): nothing there, and no closure of the function, can
// rewrite them — so the guard's runtime comparison still binds
// every later read. Consumption compares places by symbol and path,
// so shadowing can never confuse a row.
//
// BLOCKED: constraintsImply, mixedConstraintsImply, and the
// kernel-composed fallback half of decideComparison all need
// kernel_bridge/kernel_interface.ts's RefinedTSKernel
// (linearImplies) — kernel_bridge has landed instantiate_kernel.go,
// wire_decode.go, wire_format.go, and question_costs.go, but not yet
// kernel_interface.go, so no Go RefinedTSKernel type exists to type
// against. Everything not needing the kernel is ported below.
package dataflowfacts

// DifferenceConstraint is a DIFFERENCE BOUND between two places, vouched
// by a dominating guard or a dependent signature: on every run inside
// the consumption scope, minuend − subtrahend ≥ bound (strictly, when
// Strict). Plain comparisons record bound 0. A literal offset
// (`x <= y - k`) joins the bound when the offset arithmetic is provably
// exact, or — past the safe range — with the bound WIDENED by a
// kernel-proved envelope on |fl(y ± k) − (y ± k)|, so the row again
// speaks about the real pair.
type DifferenceConstraint struct {
	Minuend    PlaceKey
	Subtrahend PlaceKey
	Bound      float64
	Strict     bool
	// Computed: the claim is about the COMPUTED double
	// fl(minuend − subtrahend), not the real difference: the guard
	// tested that very expression (`if (a - b < K) return`), and with
	// both places stable the consumption recomputes the same double —
	// so the vouched window intersects the difference's set directly,
	// with no rounding argument at all. Consumed ONLY by the
	// subtraction reader; never by decideComparison or the
	// ordered-subtraction proof, whose premises speak about the real
	// pair.
	Computed bool
	// Dead: RETIRED — a write to either place (or an alias of its base)
	// landed after the row was recorded — the row spoke about values
	// that moved. Set by AliasClasses.invalidate at the write, in walk
	// order; a loop's condition rows revalidate at each body entry.
	Dead bool
}

// SumConstraint is a SUM ROW: what a guard vouches about the COMPUTED
// double of a two-place sum against a single anchor place —
// `if (offset + length > s.length) return` refuted leaves
// fl(offset + length) ≤ s.length. The claim is about the computed
// double, so emission needs no rounding argument; the consumption
// supplies one (a nonneg-integer sum equals its rounding wherever the
// anchor keeps it under 2^53).
type SumConstraint struct {
	Terms [2]PlaceKey
	// Offset: the literal offset carried on the sum side.
	Offset float64
	Anchor PlaceKey
	// SumAtMost: true means fl(sum + offset) ≤ anchor; false means ≥.
	SumAtMost bool
	Strict    bool
	// Dead: RETIRED — a write to any rooting name (or an alias) landed
	// after the row was recorded — the same flow-sensitive half order
	// rows carry (AliasClasses.invalidate).
	Dead bool
}

// DifferenceConstraintFor is the strongest difference row the ledger
// holds for minuend − subtrahend; nil when it holds none.
func DifferenceConstraintFor(
	rows []DifferenceConstraint,
	minuend, subtrahend PlaceKey,
) *DifferenceConstraint {
	var held *DifferenceConstraint
	for i := range rows {
		row := &rows[i]
		if row.Dead {
			continue
		}
		// a COMPUTED row speaks about the fl-difference, not the real
		// pair — only computedWindowFor consumes it
		if row.Computed {
			continue
		}
		if !SamePlace(row.Minuend, minuend) || !SamePlace(row.Subtrahend, subtrahend) {
			continue
		}
		if held == nil || row.Bound > held.Bound ||
			(row.Bound == held.Bound && row.Strict && !held.Strict) {
			held = row
		}
	}
	return held
}

// AnyRowMentions reports whether any LIVE row mentions the place at all.
// A system with no fact about a place can imply nothing about it — the
// caller skips the kernel's composition outright (measured: every
// unvouched element read otherwise pays one kernel ask for a guaranteed
// no).
func AnyRowMentions(rows []DifferenceConstraint, place PlaceKey) bool {
	for _, row := range rows {
		if row.Dead || row.Computed {
			continue
		}
		if SamePlace(row.Minuend, place) || SamePlace(row.Subtrahend, place) {
			return true
		}
	}
	return false
}

// ComputedWindow is the vouched WINDOW on the computed double
// fl(minuend − subtrahend).
type ComputedWindow struct {
	Lo *ComputedWindowEdge
	Hi *ComputedWindowEdge
}

// ComputedWindowEdge is one edge of a ComputedWindow.
type ComputedWindowEdge struct {
	Value  float64
	Strict bool
}

// ComputedWindowFor is the vouched WINDOW on the computed double
// fl(minuend − subtrahend), from the computed rows in both spellings: a
// row on (a,b) floors fl(a−b) at its bound, and a row on (b,a) ceils it
// at the negated bound (fl(b−a) = −fl(a−b) exactly). Nil where no row
// speaks.
func ComputedWindowFor(
	rows []DifferenceConstraint,
	minuend, subtrahend PlaceKey,
) *ComputedWindow {
	var lo, hi *ComputedWindowEdge
	for i := range rows {
		row := &rows[i]
		if row.Dead || !row.Computed {
			continue
		}
		if SamePlace(row.Minuend, minuend) && SamePlace(row.Subtrahend, subtrahend) {
			if lo == nil || row.Bound > lo.Value || (row.Bound == lo.Value && row.Strict) {
				lo = &ComputedWindowEdge{Value: row.Bound, Strict: row.Strict}
			}
		}
		if SamePlace(row.Minuend, subtrahend) && SamePlace(row.Subtrahend, minuend) {
			ceil := -row.Bound
			if hi == nil || ceil < hi.Value || (ceil == hi.Value && row.Strict) {
				hi = &ComputedWindowEdge{Value: ceil, Strict: row.Strict}
			}
		}
	}
	if lo == nil && hi == nil {
		return nil
	}
	return &ComputedWindow{Lo: lo, Hi: hi}
}

// ComparisonOp is one of the six comparison operators decideComparison
// answers.
type ComparisonOp string

const (
	ComparisonLt ComparisonOp = "lt"
	ComparisonGt ComparisonOp = "gt"
	ComparisonLe ComparisonOp = "le"
	ComparisonGe ComparisonOp = "ge"
	ComparisonEq ComparisonOp = "eq"
	ComparisonNe ComparisonOp = "ne"
)

// DecideComparison is a comparison decided by the ledger alone: the rows
// say what the guard proved about the pair, and the comparison's
// verdict follows without reading either value. Nil where the rows do
// not decide. A held row proves both sides real (NaN passes no
// comparison), so every verdict here is a theorem about the actual
// runtime pair.
//
// The TS signature accepts an optional kernel and falls back to the
// linear decider over ALL live rows when the direct read alone does not
// decide (`i < j` and `j < k` decide `i < k` the way one row would).
// That fallback needs RefinedTSKernel.linearImplies, which is not yet
// ported (see the file banner) — this port only reads the direct,
// single-row-pair path.
func DecideComparison(
	rows []DifferenceConstraint,
	left, right PlaceKey,
	op ComparisonOp,
) *bool {
	leftBelow := DifferenceConstraintFor(rows, right, left)
	rightBelow := DifferenceConstraintFor(rows, left, right)
	strictlyBelow := leftBelow != nil && (leftBelow.Strict || leftBelow.Bound > 0)
	atMost := leftBelow != nil && leftBelow.Bound >= 0
	strictlyAbove := rightBelow != nil && (rightBelow.Strict || rightBelow.Bound > 0)
	atLeast := rightBelow != nil && rightBelow.Bound >= 0
	t := true
	f := false
	switch op {
	case ComparisonLt:
		if strictlyBelow {
			return &t
		}
		if atLeast {
			return &f
		}
		return nil
	case ComparisonLe:
		if atMost {
			return &t
		}
		if strictlyAbove {
			return &f
		}
		return nil
	case ComparisonGt:
		if strictlyAbove {
			return &t
		}
		if atMost {
			return &f
		}
		return nil
	case ComparisonGe:
		if atLeast {
			return &t
		}
		if strictlyBelow {
			return &f
		}
		return nil
	case ComparisonEq:
		// bounded from BOTH sides without strictness: the pair is
		// pinned equal (contradictory rows reach no run, so any
		// verdict is sound there)
		if atMost && atLeast && !strictlyBelow && !strictlyAbove {
			return &t
		}
		if strictlyBelow || strictlyAbove {
			return &f
		}
		return nil
	case ComparisonNe:
		if atMost && atLeast && !strictlyBelow && !strictlyAbove {
			return &f
		}
		if strictlyBelow || strictlyAbove {
			return &t
		}
		return nil
	}
	return nil
}
