// The SEAM GUARD: a depth cap on the encoded wire, checked right
// before any question crosses to the native kernel — defense in depth
// beside the walk's own widening (abstractdomain's
// sequenceConcatenationWidenBound). A wire that slips past the walk
// still deep — a construction path this file does not know about, a
// future caller that builds a RefinedSet directly — is declined here
// rather than handed to the kernel's derivative-based deciders, which
// grow the term on every nullable-left step with nothing collapsing
// the repeated substructure (refined_sets/automata.lean's Concatenation
// case) and can spend unbounded time (measured: the ReduceCSSCalc.ts
// hang ran past an hour before the kernel thread's own stack
// overflowed — packages/refined-lean/native/kernel_wrapper.c's cgo
// call, SIGBUS).
//
// The measure counts occurrences of the sequence-shaped form tags
// (concatenation, star, repeat, repeatWord, union, difference) in the
// wire's own JSON text. Every one of those forms nests its operand(s)
// as a NESTED wire object one level deeper (wire_format.go's
// wireFormJSON: `{"form":"concatenation","A":%s,"B":%s}`), so a chain
// of N such tags in one wire's text is a lower bound on that wire's
// deepest nesting — cheap (one string scan, no JSON decode) and never
// an UNDER-count for the pathological shape this guards against (a
// long right- or left-nested chain, where every tag sits at a
// different depth and the count IS the depth). A wire built from
// several independent shallow branches could in principle over-count
// relative to its true max depth; over-counting only makes the guard
// MORE conservative, never lets a deep wire through uncaught.
package kernelbridge

import "strings"

// wireSequenceFormTags are the JSON form-tag spellings of the
// sequence-family forms — the shapes the kernel's DERIVATIVE-based
// deciders walk, where the measured non-termination lived (the
// automata Concatenation case grows the term on every nullable-left
// step). Scalar/leaf forms (atLeast, oneOf, integer, …) are excluded:
// they carry no nested RefinedSet operand of their own.
var wireSequenceFormTags = []string{
	`"form":"concatenation"`,
	`"form":"star"`,
	`"form":"repeat"`,
	`"form":"repeatWord"`,
}

// wireBranchingFormTags nest operands too, but a wire built ONLY of
// these over scalar leaves never reaches the derivative deciders at
// all — the scalar DNF machinery decides it, and its disjunctions
// deduplicate at every union and negation step
// (set_functions/emptiness.lean dedupD), so a self-similar scalar
// tower collapses instead of squaring per layer. The kernel answers
// every readable question (JT's ruling, 2026-08-09); these tags gate
// only in the company of a sequence-family tag, where a branching
// node genuinely deepens the derivative walk.
var wireBranchingFormTags = []string{
	`"form":"union"`,
	`"form":"difference"`,
}

// wireNestingCount is the seam guard's cheap depth proxy over the
// shapes the derivative deciders walk. A wire with NO sequence-family
// tag counts zero — it is a scalar question the DNF deciders collapse,
// not a shape this guard exists for. A wire with any sequence-family
// tag counts every nesting tag, branching included — conservative for
// exactly the chains the measured hang was made of. See the file
// comment for why an occurrence count is a sound lower bound on depth
// for those chains.
func wireNestingCount(wire string) int {
	sequence := 0
	for _, tag := range wireSequenceFormTags {
		sequence += strings.Count(wire, tag)
	}
	if sequence == 0 {
		return 0
	}
	total := sequence
	for _, tag := range wireBranchingFormTags {
		total += strings.Count(wire, tag)
	}
	return total
}

// wireNestingCapBytes-scale question: the cap this guard enforces,
// matched to abstractdomain's sequenceConcatenationWidenBound (64) —
// the adapter widens before a set this deep is ever built, so a wire
// still this deep at the FFI seam is a shape the adapter's own
// widening did not (or could not) catch, and the guard declines it
// rather than trust it to the kernel's own recursion. Kept as an
// independent named constant, not an import of the adapter's, since
// kernelbridge sits BELOW abstractdomain in the import graph (the
// adapter imports kernelbridge, not the reverse) — the two constants
// are pinned to the same number by this comment, not by a shared
// symbol.
const wireNestingCap = 64

// wireExceedsNestingCap is the seam guard's test: does this wire's
// sequence-shaped form-tag count exceed what the kernel's deciders are
// measured to walk safely. Called on every ask1/ask2 wire argument,
// ahead of the cache lookup and the FFI call — a decline here costs
// nothing the cache would have paid for a real answer, and never
// reaches the native call at all.
func wireExceedsNestingCap(wire string) bool {
	return wireNestingCount(wire) > wireNestingCap
}
