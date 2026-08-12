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
package dataflowfacts

import "github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"

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

// MixedConstraintsTarget is the target row mixedConstraintsImply poses to
// the kernel — the TS source's inline `{ plus, minus, bound, strict }`
// parameter object.
type MixedConstraintsTarget struct {
	Plus   []PlaceKey
	Minus  []PlaceKey
	Bound  float64
	Strict bool
}

// ConstraintsImply is constraintsImply in the TS source: do the live rows
// imply minuend − subtrahend ≥ bound? Where no single row says it, the
// kernel's linear decider composes rows — transitive chains the one-row
// reader cannot see. A true answer rides linImpliesB_sound; anything else
// claims nothing.
func ConstraintsImply(
	kernel *kernelbridge.RefinedTSKernel,
	rows []DifferenceConstraint,
	minuend, subtrahend PlaceKey,
	bound float64,
	strict bool,
) bool {
	return MixedConstraintsImply(kernel, rows, nil, MixedConstraintsTarget{
		Plus:   []PlaceKey{minuend},
		Minus:  []PlaceKey{subtrahend},
		Bound:  bound,
		Strict: strict,
	})
}

// MixedConstraintsImply is mixedConstraintsImply in the TS source: do the
// live rows — difference rows AND sum rows in ONE system — imply
// Σ plus − Σ minus ≥ bound (strict when marked)? Both row kinds are
// linear facts over places, so the kernel's one decider composes them:
// `offset + length ≤ s.length` and `k < length` reach
// `offset + k < s.length`, which neither row alone says.
//
// The PREMISE is the caller's: every difference row speaks about a real
// pair by construction, but a sum row speaks about a COMPUTED double —
// the caller passes only sum rows it has vouched real (nonnegative-integer
// terms under an array-length anchor, the fl-exactness argument the
// element read supplies). A true answer rides linImpliesB_sound over the
// vouched facts.
func MixedConstraintsImply(
	kernel *kernelbridge.RefinedTSKernel,
	orderRows []DifferenceConstraint,
	vouchedSumConstraints []SumConstraint,
	target MixedConstraintsTarget,
) bool {
	var liveOrder []DifferenceConstraint
	for _, row := range orderRows {
		if !row.Dead && !row.Computed {
			liveOrder = append(liveOrder, row)
		}
	}
	// an invalidated sum row spoke about values that moved — same as an
	// order row, through the same invalidation
	var sums []SumConstraint
	for _, row := range vouchedSumConstraints {
		if !row.Dead {
			sums = append(sums, row)
		}
	}
	if len(liveOrder) == 0 && len(sums) == 0 {
		return false
	}
	var places []PlaceKey
	indexOf := func(place PlaceKey) int {
		for i, held := range places {
			if SamePlace(held, place) {
				return i
			}
		}
		places = append(places, place)
		return len(places) - 1
	}
	factOf := func(plus, minus []PlaceKey, b float64, st bool) kernelbridge.LinearFact {
		var coefs []float64
		bump := func(i int, c float64) {
			for len(coefs) <= i {
				coefs = append(coefs, 0)
			}
			coefs[i] += c
		}
		for _, place := range plus {
			bump(indexOf(place), 1)
		}
		for _, place := range minus {
			bump(indexOf(place), -1)
		}
		return kernelbridge.LinearFact{Coefs: coefs, Bound: b, Strict: st}
	}
	posedTarget := factOf(target.Plus, target.Minus, target.Bound, target.Strict)
	facts := make([]kernelbridge.LinearFact, 0, len(liveOrder)+len(sums))
	for _, row := range liveOrder {
		facts = append(facts, factOf([]PlaceKey{row.Minuend}, []PlaceKey{row.Subtrahend}, row.Bound, row.Strict))
	}
	for _, row := range sums {
		// fl(t₁ + t₂ + offset) ≤ anchor, vouched real: anchor − t₁ − t₂
		// ≥ offset — and the ≥ direction mirrored
		if row.SumAtMost {
			facts = append(facts, factOf([]PlaceKey{row.Anchor}, row.Terms[:], row.Offset, row.Strict))
		} else {
			facts = append(facts, factOf(row.Terms[:], []PlaceKey{row.Anchor}, -row.Offset, row.Strict))
		}
	}
	// the TS source's try/catch: LinearImplies panics on a kernel-call
	// error (kernel_asks.go), which the TS twin catches and treats as a
	// refusal to claim
	return callLinearImplies(kernel, facts, posedTarget)
}

// callLinearImplies is the TS source's `try { return kernel.linearImplies(...) }
// catch { return false }` — LinearImplies panics on a kernel-call error
// (kernel_asks.go's `panic(err.Error())`), mirroring the TS `throw`; this
// recovers it the same way the TS catch does.
func callLinearImplies(kernel *kernelbridge.RefinedTSKernel, facts []kernelbridge.LinearFact, target kernelbridge.LinearFact) (implied bool) {
	defer func() {
		if recover() != nil {
			implied = false
		}
	}()
	return kernel.LinearImplies(facts, target)
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
// With a kernel passed (non-nil), a pair no SINGLE row orders falls back
// to the linear decider over ALL live rows — `i < j` and `j < k` decide
// `i < k` the way one row would. Sum rows stay out here: they speak about
// computed doubles and need the caller's vouching (MixedConstraintsImply's
// premise); a site holding vouched sum rows poses its question through
// MixedConstraintsImply directly. Pass nil for the TS `kernel === undefined`
// case (the direct-only read).
func DecideComparison(
	rows []DifferenceConstraint,
	left, right PlaceKey,
	op ComparisonOp,
	kernel *kernelbridge.RefinedTSKernel,
) *bool {
	leftBelow := DifferenceConstraintFor(rows, right, left)
	rightBelow := DifferenceConstraintFor(rows, left, right)
	strictlyBelow := leftBelow != nil && (leftBelow.Strict || leftBelow.Bound > 0)
	atMost := leftBelow != nil && leftBelow.Bound >= 0
	strictlyAbove := rightBelow != nil && (rightBelow.Strict || rightBelow.Bound > 0)
	atLeast := rightBelow != nil && rightBelow.Bound >= 0
	t := true
	f := false
	direct := func() *bool {
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
	}()
	if direct != nil || kernel == nil {
		return direct
	}
	// b − a ≥ 0 (strict: b − a > 0) composed from every live row
	implies := func(a, b PlaceKey, strict bool) bool {
		return MixedConstraintsImply(kernel, rows, nil, MixedConstraintsTarget{
			Plus:   []PlaceKey{b},
			Minus:  []PlaceKey{a},
			Bound:  0,
			Strict: strict,
		})
	}
	switch op {
	case ComparisonLt:
		if implies(left, right, true) {
			return &t
		}
		if implies(right, left, false) {
			return &f
		}
		return nil
	case ComparisonLe:
		if implies(left, right, false) {
			return &t
		}
		if implies(right, left, true) {
			return &f
		}
		return nil
	case ComparisonGt:
		if implies(right, left, true) {
			return &t
		}
		if implies(left, right, false) {
			return &f
		}
		return nil
	case ComparisonGe:
		if implies(right, left, false) {
			return &t
		}
		if implies(left, right, true) {
			return &f
		}
		return nil
	case ComparisonEq:
		if implies(left, right, true) || implies(right, left, true) {
			return &f
		}
		if implies(left, right, false) && implies(right, left, false) {
			return &t
		}
		return nil
	case ComparisonNe:
		if implies(left, right, true) || implies(right, left, true) {
			return &t
		}
		if implies(left, right, false) && implies(right, left, false) {
			return &f
		}
		return nil
	}
	return nil
}
