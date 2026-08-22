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

// wireNestingFormTags are the JSON form-tag spellings wireFormJSON
// gives the forms that nest an operand as a further wire object.
// Scalar/leaf forms (atLeast, oneOf, integer, …) are excluded: they
// carry no nested RefinedSet operand of their own, so they cannot
// deepen a chain.
var wireNestingFormTags = []string{
	`"form":"concatenation"`,
	`"form":"star"`,
	`"form":"repeat"`,
	`"form":"repeatWord"`,
	`"form":"union"`,
	`"form":"difference"`,
}

// wireNestingCount is the number of sequence-shaped form tags a wire's
// JSON text mentions — the seam guard's cheap depth proxy. See the
// file comment for why a plain occurrence count is a sound lower
// bound on nesting depth for the chains this guards against.
func wireNestingCount(wire string) int {
	total := 0
	for _, tag := range wireNestingFormTags {
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
