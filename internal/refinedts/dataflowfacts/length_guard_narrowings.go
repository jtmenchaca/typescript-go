// from dataflow_facts/length_guard_narrowings.ts
//
// Length guards over repetition sequences: what `xs.length >= k`
// (and family) says about a repetition-shaped binding, plus the
// kernel division helper shared with product-guard inversion.
//
// QuotientLow stays BLOCKED: it needs evaluation/number_range.ts's
// rangeOfSet, which ported into package walk (PORT.md's walk-package
// split), not reachable from dataflowfacts (walk imports
// dataflowfacts, not the reverse). Answers ok=false always — walk's
// InverseFactorNarrowings (walk/inverse_factor_narrowings.go,
// relocated there for its own cycle reason) already treats a
// nil/ok=false QuotientLow as "no row", so this is the same sound
// fallback (walk-integration-punchlist.md item 15's own note).

package dataflowfacts

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LengthGuardNarrowing is one row LengthGuardNarrowings answers: a
// binding's length-guard-tightened knowledge.
type LengthGuardNarrowing struct {
	Binding string
	Known   abstractdomain.AbstractValue
}

// lengthSide is the lengthSide callback's return shape in the TS
// source: `<name>.length`'s binding, and the receiver expression
// stringPlace reads.
type lengthSide struct {
	Binding string
	Value   *ast.Node
}

// LengthGuardNarrowings is what `xs.length >= k` says when it holds
// of a repetition-shaped sequence: the length is a natural, the
// comparison against a literal is exact on naturals, so the
// repetition's COUNTING FLOOR rises to k (⌈k⌉, or ⌊k⌋+1 under the
// strict form). The rebuilt knowledge is what lets an index below the
// floor read as a defined element of the repetition's element set.
//
// stringPlace: whether the guarded place is a STRING — a string's
// length window counts codepoints, so a pattern-shaped set (a
// startsWith or endsWith chain with no repetition form of its own)
// gains the window as a conjoined codepoint repetition. A non-string
// place keeps the repetition-form-only reading. Pass nil for the TS
// default (`() => false`).
//
// negated: read the FALSE side instead: `xs.length < k` refuted
// proves length ≥ k — a length is a natural, never NaN, so the
// refuted comparison decides cleanly (¬(len < k) on naturals IS
// len ≥ k).
// held answers a binding's current knowledge — walk hands its Env's
// own Get, so no whole-environment copy crosses this boundary (the
// old map parameter forced one per guard on the assignment path).
func LengthGuardNarrowings(
	held func(name string) (abstractdomain.AbstractValue, bool),
	condition *ast.Node,
	stringPlace func(e *ast.Node) bool,
	negated bool,
) []LengthGuardNarrowing {
	var rows []LengthGuardNarrowing

	lengthSideOf := func(e *ast.Node) *lengthSide {
		if !ast.IsPropertyAccessExpression(e) {
			return nil
		}
		pa := e.AsPropertyAccessExpression()
		if !ast.IsIdentifier(pa.Expression) || pa.Name().Text() != "length" {
			return nil
		}
		return &lengthSide{Binding: pa.Expression.Text(), Value: pa.Expression}
	}

	guardRow := func(place *ast.Node, k float64, strict bool, direction string) {
		side := lengthSideOf(place)
		if side == nil || math.IsNaN(k) {
			return
		}
		heldValue, ok := held(side.Binding)
		if !ok || heldValue.Kind != abstractdomain.KindSet {
			return
		}
		var conjoin *refinementsets.RefinedSet
		if stringPlace != nil && stringPlace(side.Value) {
			conjoin = &refinementsets.Codepoints
		}
		var tightened refinementsets.RefinedSet
		var tightenedOk bool
		switch direction {
		case "min":
			floor := math.Ceil(k)
			if strict {
				floor = math.Floor(k) + 1
			}
			if !isFiniteNumber(floor) || floor <= 0 {
				return
			}
			tightened, tightenedOk = refinementsets.TightenRepetition(heldValue.Set, "min", int(floor), conjoin)
		case "max":
			// `len ≤ k` caps the count at ⌊k⌋; the strict form one under
			ceiling := math.Floor(k)
			if strict {
				ceiling = math.Ceil(k) - 1
			}
			if !isFiniteNumber(ceiling) || ceiling < 0 {
				return
			}
			tightened, tightenedOk = refinementsets.TightenRepetition(heldValue.Set, "max", int(ceiling), conjoin)
		default:
			if !isInteger(k) || k < 0 {
				return
			}
			tightened, tightenedOk = refinementsets.TightenRepetition(heldValue.Set, "length", int(k), conjoin)
		}
		if !tightenedOk {
			return
		}
		// measures are facts about the VALUE, so the tightened
		// knowledge keeps them
		tightenedKnown := abstractdomain.KnownWithMeasures(
			abstractdomain.KnownSet(tightened, nil, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(heldValue), abstractdomain.TrustSpec), abstractdomain.SetKindTagNone),
			heldValue.Measures,
		)
		// a length guard narrows the SAME array `xs` the held value
		// already named -- `xs.length >= k` says more about the count of
		// that one runtime value, never a different value -- so a density
		// proof heldValue already carried (SeqDenseKnown && SeqDense) is
		// still a true fact of the narrower window: every position the
		// wider window proved present, the narrower one still counts.
		if heldValue.SeqDenseKnown && heldValue.SeqDense {
			tightenedKnown = abstractdomain.KnownSetDense(tightenedKnown)
		}
		rows = append(rows, LengthGuardNarrowing{
			Binding: side.Binding,
			Known:   tightenedKnown,
		})
	}

	readLeaf := func(e *ast.Node, flipped bool) {
		if !ast.IsBinaryExpression(e) {
			return
		}
		bin := e.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if ast.IsNumericLiteral(bin.Right) {
			k := numericLiteralValue(bin.Right)
			if !flipped {
				if kind == ast.KindGreaterThanEqualsToken {
					guardRow(bin.Left, k, false, "min")
				}
				if kind == ast.KindGreaterThanToken {
					guardRow(bin.Left, k, true, "min")
				}
				// the ceiling direction: `len ≤ k` / `len < k` held
				if kind == ast.KindLessThanEqualsToken {
					guardRow(bin.Left, k, false, "max")
				}
				if kind == ast.KindLessThanToken {
					guardRow(bin.Left, k, true, "max")
				}
				// equality pins the count both ways; `len !== 0` held (or
				// `len === 0` refuted) proves at least one element
				if kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken {
					guardRow(bin.Left, k, false, "length")
				}
				if (kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken) && k == 0 {
					guardRow(bin.Left, 1, false, "min")
				}
			} else {
				// ¬(len < k) is len ≥ k; ¬(len ≤ k) is len > k
				if kind == ast.KindLessThanToken {
					guardRow(bin.Left, k, false, "min")
				}
				if kind == ast.KindLessThanEqualsToken {
					guardRow(bin.Left, k, true, "min")
				}
				// ¬(len > k) is len ≤ k; ¬(len ≥ k) is len < k
				if kind == ast.KindGreaterThanToken {
					guardRow(bin.Left, k, false, "max")
				}
				if kind == ast.KindGreaterThanEqualsToken {
					guardRow(bin.Left, k, true, "max")
				}
				if (kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken) && k == 0 {
					guardRow(bin.Left, 1, false, "min")
				}
				if kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken {
					guardRow(bin.Left, k, false, "length")
				}
			}
		}
		if ast.IsNumericLiteral(bin.Left) {
			k := numericLiteralValue(bin.Left)
			if !flipped {
				if kind == ast.KindLessThanEqualsToken {
					guardRow(bin.Right, k, false, "min")
				}
				if kind == ast.KindLessThanToken {
					guardRow(bin.Right, k, true, "min")
				}
				// `k ≥ len` / `k > len` held cap the count
				if kind == ast.KindGreaterThanEqualsToken {
					guardRow(bin.Right, k, false, "max")
				}
				if kind == ast.KindGreaterThanToken {
					guardRow(bin.Right, k, true, "max")
				}
				if kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken {
					guardRow(bin.Right, k, false, "length")
				}
				if (kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken) && k == 0 {
					guardRow(bin.Right, 1, false, "min")
				}
			} else {
				// ¬(k > len) is len ≥ k; ¬(k ≥ len) is len > k
				if kind == ast.KindGreaterThanToken {
					guardRow(bin.Right, k, false, "min")
				}
				if kind == ast.KindGreaterThanEqualsToken {
					guardRow(bin.Right, k, true, "min")
				}
				// ¬(k < len) is len ≤ k; ¬(k ≤ len) is len < k
				if kind == ast.KindLessThanToken {
					guardRow(bin.Right, k, false, "max")
				}
				if kind == ast.KindLessThanEqualsToken {
					guardRow(bin.Right, k, true, "max")
				}
				if (kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken) && k == 0 {
					guardRow(bin.Right, 1, false, "min")
				}
				if kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken {
					guardRow(bin.Right, k, false, "length")
				}
			}
		}
	}

	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}

// isFiniteNumber mirrors Number.isFinite.
func isFiniteNumber(v float64) bool {
	return !math.IsInf(v, 0) && !math.IsNaN(v)
}

// QuotientLow is a sound LOWER bound on the real quotients {a/d : a >
// k}, read from the kernel division transfer's outward-rounded low
// edge. The dividend is posed as the OPEN RAY above k — a singleton
// dividend could answer the rounded quotient as an exact value, which
// may sit above the real bound; the ray keeps the answer an enclosure
// whose corners are directed-rounded outward. ok=false where the
// kernel answers nothing usable.
//
// BLOCKED (see file banner): needs walk.RangeOfSet, unreachable from
// this package (walk imports dataflowfacts, not the reverse). Answers
// ok=false always — the sound "nothing vouched" fallback.
func QuotientLow(kernel *kernelbridge.RefinedTSKernel, k, d float64) (float64, bool) {
	return 0, false
}
