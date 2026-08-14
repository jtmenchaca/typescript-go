// from dataflow_facts/inverse_factor_narrowings.ts
//
// Inverting a product guard: what `x * y > k` says about x when the
// cofactor sits in a nonnegative window.
//
// PLACEMENT: this file's own package, dataflowfacts, cannot hold
// InverseFactorNarrowings — narrowing.SideBounds/narrowing.Narrowed
// are narrowing-owned, and narrowing already imports dataflowfacts in
// several files (comparison_leaf.go, condition_analysis.go, …): a
// true cross-package cycle (dataflowfacts -> narrowing -> dataflowfacts)
// Go bans outright. walk already imports both dataflowfacts and
// narrowing one-way, so the function lands here instead —
// dataflowfacts/inverse_factor_narrowings.go's absence (no banner
// file left behind — the whole file relocated) is intentional; every
// symbol below is unchanged from the TS source. QuotientLow itself
// (length_guard_narrowings.ts) stays in dataflowfacts, since it needs
// no narrowing types — called here as dataflowfacts.QuotientLow,
// which is still blocked on evaluation/number_range.ts's rangeOfSet
// (walk-owned; see dataflowfacts/length_guard_narrowings.go's
// banner), so every row here answers none until that lands.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// InverseFactorNarrowings is what `x * y > k` says about x when it
// holds: with k a nonnegative literal and y provably inside a
// nonnegative window [lo, hi], the guard's truth forces y > 0 (a zero
// or negative y cannot make the product exceed k ≥ 0), and dividing
// gives x > k/y ≥ k/hi. The bound k/hi is posed to the kernel's
// division transfer and read at its LOW edge — outward-rounded, so it
// never overstates. Strict comparisons only: `>=` would need x·y ≥ k,
// and rounding admits fl(x·y) = k with the real product just under k,
// which breaks the division step. The guard's truth also rules the
// factor out of NaN (NaN products fail every comparison) and out of
// −∞ (a negative product is not above k ≥ 0), so the claim is strong:
// x lies in (bound, +∞].
// The comparison's constant side is read through const-to-const links
// (const_chain_literal.go), so the checker comes in alongside the
// kernel; a nil checker reads literal tokens only, exactly as before.
func InverseFactorNarrowings(
	c *checker.Checker,
	kernel *kernelbridge.RefinedTSKernel,
	condition *ast.Node,
	isTracked func(name string) bool,
	sideBounds narrowing.SideBounds,
) []narrowing.Narrowed {
	var rows []narrowing.Narrowed
	if sideBounds == nil {
		return rows
	}
	productRow := func(product *ast.Node, k float64) {
		if !ast.IsBinaryExpression(product) || product.AsBinaryExpression().OperatorToken.Kind != ast.KindAsteriskToken {
			return
		}
		if !(k >= 0) {
			return
		}
		bin := product.AsBinaryExpression()
		sides := [2][2]*ast.Node{{bin.Left, bin.Right}, {bin.Right, bin.Left}}
		for _, side := range sides {
			factor, cofactor := side[0], side[1]
			if !ast.IsIdentifier(factor) || !isTracked(factor.Text()) {
				continue
			}
			window, ok := sideBounds(cofactor)
			if !ok || window.Lo < 0 {
				continue
			}
			bound, ok := dataflowfacts.QuotientLow(kernel, k, window.Hi)
			if !ok {
				continue
			}
			rows = append(rows, narrowing.Narrowed{
				Binding:  factor.Text(),
				Path:     nil,
				Forms:    []refinementsets.Refinement{refinementsets.Above(bound)},
				Refuting: false,
			})
		}
	}
	var visit func(e *ast.Node)
	visit = func(e *ast.Node) {
		if ast.IsParenthesizedExpression(e) {
			visit(e.AsParenthesizedExpression().Expression)
			return
		}
		if !ast.IsBinaryExpression(e) {
			return
		}
		bin := e.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken {
			visit(bin.Left)
			visit(bin.Right)
			return
		}
		// the constant side follows its const-to-const links to the
		// literal it names, so `x * y > LIMIT` with `const LIMIT = 10`
		// reads the same as the literal written in place — a const holds
		// its literal at every reachable point. The strict-comparison and
		// nonneg-cofactor gates above are untouched: only how k is READ
		// changes, never which comparisons qualify.
		if kind == ast.KindGreaterThanToken {
			if k, ok := dataflowfacts.ConstChainNumber(c, bin.Right); ok {
				productRow(bin.Left, k)
			}
		}
		if kind == ast.KindLessThanToken {
			if k, ok := dataflowfacts.ConstChainNumber(c, bin.Left); ok {
				productRow(bin.Right, k)
			}
		}
	}
	visit(condition)
	return rows
}
