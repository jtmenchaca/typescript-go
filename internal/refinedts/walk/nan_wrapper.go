// from assignability/nan_wrapper.ts
//
// NaN at a checked position: pinned NaN is a member of no stated
// set; possibly-NaN knowledge refutes when the real half already
// sits outside, or when the real half fits and NaN is the obstacle.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CheckPinnedNan is checkPinnedNan in the TS source: pinned NaN — not
// an element of ℝ̄, so a member of NO stated set.
func CheckPinnedNan(
	ctx *FlowContext,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	var messageText string
	if target.Kind == annotations.DeclaredSet {
		messageText = what + " of type 'NaN' is not assignable to type " +
			"'" + StatedSetWords(*target.Set, target.Word) + "'"
	} else {
		messageText = what + " of type 'NaN' is not assignable — NaN is a member " +
			"of no refined set"
	}
	ctx.Report(assignability.At(node, 7001, messageText))
}

// CheckPossiblyNaN is checkPossiblyNaN in the TS source:
// possibly-NaN knowledge — the real half may already refute; when it
// fits, NaN is the one obstacle and a refined set never holds NaN.
func CheckPossiblyNaN(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	// the wrapper only ever wraps numeric knowledge — against a
	// position whose stated set DEMONSTRABLY states a sequence (a
	// string or an array: a star, a concatenation, a repetition) no
	// run satisfies the statement, so the position refutes in plain
	// words. The test is positive: a relation-shaped or empty target
	// proves nothing here and keeps its own path.
	if target.Kind == annotations.DeclaredSet && target.KindTag == "" && StatesSequence(*target.Set) {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" is a number, and the position states a string or an "+
				"array — a number is not allowed here",
		))
		return
	}
	if target.Kind == annotations.DeclaredSet && target.KindTag == "" {
		// the DECLARED side itself ADDS NOTHING beyond the number sort's
		// own ground — a bare, unrefined `number` position, which the
		// language admits NaN into (every refined set EXCLUDES NaN unless
		// it restates the ground, the same line AddsNothingSet already
		// draws for the source half below). NaN is then not an obstacle
		// at all: the target already holds it, so the NaN arm adds no
		// failure case and the whole question reduces to the plain real-
		// half subset X ⊆ D — which a bare number ground trivially
		// answers true for any numeric X, real or otherwise undetermined.
		// The identity case (bare number source into bare number target)
		// is the point of the rule: it answers SILENT, not the 7002 the
		// AddsNothingSet-on-the-SOURCE gate below used to fall through to
		// for lack of a decidable question — the question was always
		// decidable once the target side is read too.
		if AddsNothingSet(*target.Set) {
			return
		}
		var innerSet refinementsets.RefinedSet
		hasInnerSet := false
		if known.Inner.Kind == abstractdomain.KindSet && known.Inner.SetKindTag == abstractdomain.SetKindTagNone {
			innerSet = known.Inner.Set
			hasInnerSet = true
		} else if known.Inner.Kind == abstractdomain.KindValues && known.Inner.KindTag == abstractdomain.PrimitiveNumber {
			innerSet, hasInnerSet = abstractdomain.SetOfKnown(*known.Inner)
		}
		// a real half that ADDS NOTHING beyond the number sort's own
		// ground (AddsNothingSet — the same "no information beyond the
		// host type" test check_assignability.go's KindUnknown arm
		// already asks of the TARGET) carries no more information than
		// KindUnknown does: "any real, or NaN" is not a derived fact
		// about this value, it is the un-narrowed sort itself, most
		// often AfterReaders' own fallback seed for an expression this
		// walk determined nothing about (silence/after_readers.go's
		// NumberWithNaN — the ONLY producer of the maximal
		// AtLeast(-Infinity) real half). A "subset of the target" question
		// against that real half is a foregone false for every bounded
		// target — refusing it as a genuine 7001 refutation would report
		// "this value is out of range" about a value the walk never
		// actually examined. Undetermined stays undetermined: skip
		// straight to the 7002 alert below, the same verdict KindUnknown
		// itself takes. (The declared side was already checked above: this
		// gate is reached only where the TARGET excludes NaN — a genuinely
		// refined set — so NaN remains a live obstacle here.)
		if hasInnerSet && refinementsets.OnOneTupleLayer(innerSet) && !AddsNothingSet(innerSet) {
			if checkPossiblyNaNSubset(ctx, innerSet, target, node, what) {
				return // reported (either the sort refutation or the NaN refutation)
			}
			// a refused question keeps the alert below
		}
	}
	fix, hasFix := GuardFix(node, target)
	base := assignability.At(node, 7002, assignability.AlertText)
	if hasFix {
		base.Fix = &assignability.RefinementFix{Title: fix.Title, NewText: fix.NewText, InsertAt: fix.InsertAt}
	}
	ctx.Report(base)
}

// checkPossiblyNaNSubset is the TS source's try/catch around
// ctx.kernel.scalarSubset for the possibly-NaN path: true when a
// diagnostic was reported (the sort refutation or the NaN
// refutation) and the caller should return; false mirrors the TS
// catch — a refused question keeps the caller's alert below.
func checkPossiblyNaNSubset(
	ctx *FlowContext,
	innerSet refinementsets.RefinedSet,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) (reported bool) {
	defer func() {
		if recover() != nil {
			reported = false
		}
	}()
	if !ctx.Kernel.ScalarSubset(innerSet, *target.Set) {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+refinementsets.FormatForDiagnostics(innerSet)+", or NaN' "+
				"is not assignable to type '"+StatedSetWords(*target.Set, target.Word)+"'",
		))
		return true
	}
	// the real half fits: NaN alone violates — refuted, since a
	// refined set never holds NaN
	fixForNaN, hasFix := GuardFix(node, target)
	refuted := assignability.At(
		node,
		7001,
		what+" may be NaN, which is not assignable to type '"+
			StatedSetWords(*target.Set, target.Word)+"' — no refined set holds NaN",
	)
	if hasFix {
		refuted.Fix = &assignability.RefinementFix{Title: fixForNaN.Title, NewText: fixForNaN.NewText, InsertAt: fixForNaN.InsertAt}
	}
	ctx.Report(refuted)
	return true
}
