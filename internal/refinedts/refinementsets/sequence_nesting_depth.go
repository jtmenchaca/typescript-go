// The sequence-shape SIZE measure: the total count of sequence-shaped
// form nodes (Concatenation, Star, Repeat, RepeatWord, Union,
// Difference) a set's syntax carries, walking every operand and every
// sibling form. A loop whose body reassigns a string across repeated
// `.replace()`/`+` derivations builds a Concatenation/Star tree that
// grows one (or more) node per iteration with nothing to fold it back
// down -- unbounded in the pathological case (ReduceCSSCalc.ts's
// evaluateExpression: a while-loop feeding calculateArithmetic, then a
// SECOND calculateArithmetic call over the loop's own output).
//
// A pure NESTING-DEPTH reading (the longest root-to-leaf chain alone)
// undercounts this shape: the minimized reproducer's pathological ask
// carries a Star whose OWN element is a flat list of several repeated
// (integer, union) pairs -- BREADTH, not depth, since a Star's forms
// list is one set's sibling forms, and a depth reading takes the
// deepest sibling alone, blind to how many siblings repeat. The
// kernel's own derivative engine (refined_sets/automata.lean) walks
// every node in the tree once per derivative step, so total node COUNT
// is what tracks the cost that actually grows -- this measure sums
// across every operand AND every sibling form, so three repeated
// pairs count three times over, not once.
//
// This is what sequenceConcatenationWidenBound (abstractdomain) reads
// to decide when a set has grown past what the kernel's deciders can
// walk in bounded time -- see that file's constant and comment for the
// number and its justification.
package refinementsets

// SequenceNestingDepth is the total count of sequence-shaped form
// nodes (Concatenation, Star, Repeat, RepeatWord, Union, Difference)
// anywhere in set's syntax -- every operand, and every sibling form in
// every nested set's own Forms list. A leaf form (AtLeast/Above/
// AtMost/Below/Integer/MultipleOf/OneOf/EmptyTuple) contributes zero.
// The name is kept from the nesting-depth reading this measure
// replaced; the file comment above states the size-vs-depth
// distinction plainly for the next reader.
func SequenceNestingDepth(set RefinedSet) int {
	total := 0
	for _, form := range set.Forms {
		total += refinementNestingDepth(form)
	}
	return total
}

func refinementNestingDepth(form Refinement) int {
	switch form.Form {
	case FormConcatenation, FormUnion, FormDifference:
		if form.A_ == nil || form.B == nil {
			return 1
		}
		return 1 + SequenceNestingDepth(*form.A_) + SequenceNestingDepth(*form.B)
	case FormStar, FormRepeat, FormRepeatWord:
		if form.A_ == nil {
			return 1
		}
		return 1 + SequenceNestingDepth(*form.A_)
	default:
		// AtLeast, Above, AtMost, Below, Integer, MultipleOf, OneOf,
		// EmptyTuple -- every leaf form the grammar has
		return 0
	}
}
