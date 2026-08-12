// from assignability/worn_set_membership.ts
//
// A worn set against a stated set: kind-tag agreement, scalar or
// sequence subset, the integer hint, NaN-bearing elements, identical
// sets, sequence measures, and flowing temporal bounds.

package walk

import (
	"encoding/json"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// WornSetParams is the TS source's inline destructured-object
// parameter to checkWornSet.
type WornSetParams struct {
	Ctx           *FlowContext
	Known         abstractdomain.AbstractValue   // kind == "set"
	Target        annotations.DeclaredRefinement // kind == "set"
	Node          *ast.Node
	What          string
	Stringy       bool
	SpelledSet    func(refinementsets.RefinedSet) string
	SpelledTarget string
	Temporal      *refinementsets.TemporalAnnotation
	BoundsStated  bool
}

// CheckWornSet is checkWornSet in the TS source.
func CheckWornSet(p WornSetParams) {
	ctx, known, target, node, what := p.Ctx, p.Known, p.Target, p.Node, p.What
	// a worn sort (bigint, symbol) must match the statement's before
	// any set question is even posed — a bigint window is not a
	// double window, whatever the numbers say. When BOTH sides name a
	// definite sort and one is scalar while the other is a sequence,
	// no run satisfies the statement — a number where a string is
	// stated fails at runtime whatever its digits spell — so the
	// position refutes in plain words rather than alerting.
	if known.SetKindTag != setKindTagOf(target.KindTag) {
		scalarish := func(t string) bool { return t == "" || t == "number" || t == "boolean" }
		sequenceish := func(t string) bool { return t == "string" || t == "array" }
		say := func(t string) string {
			switch t {
			case "", "number":
				return "a number"
			case "boolean":
				return "a boolean"
			case "string":
				return "a string"
			case "array":
				return "an array"
			default:
				return "a " + t
			}
		}
		knownSort := string(known.SetKindTag)
		targetSort := target.KindTag
		if (scalarish(knownSort) && sequenceish(targetSort)) ||
			(sequenceish(knownSort) && scalarish(targetSort)) {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" is "+say(knownSort)+", and the position states "+
					say(targetSort)+" — "+say(knownSort)+" is not allowed here",
			))
			return
		}
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	if refinementsets.OnOneTupleLayer(known.Set) && refinementsets.OnOneTupleLayer(*target.Set) {
		checkWornScalarSubset(p)
		return
	}
	// a scalar-shaped value against a statement that demonstrably
	// states a sequence (or the reverse) crosses sorts: a number
	// where a string or an array is stated fails on every run,
	// whatever its digits spell — plain refutation, never a refused
	// kernel question. Both tests are POSITIVE, so relation-shaped
	// sets keep their own path. (The sort discipline keeps every
	// string-sorted known TAGGED "string", so an untagged scalar set
	// is numeric.)
	if known.SetKindTag == abstractdomain.SetKindTagNone && target.KindTag == "" {
		if refinementsets.OnOneTupleLayer(known.Set) && StatesSequence(*target.Set) {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" is a number, and the position states a string or "+
					"an array — a number is not allowed here",
			))
			return
		}
		if StatesSequence(known.Set) && refinementsets.OnOneTupleLayer(*target.Set) &&
			// the sorted door: a bare scalar union inside the codepoint
			// alphabet IS the same set as a union of one-character
			// strings (tailwindcss's Combinator ' '|'>'|'+'|'~'), so no
			// cross-sort refutation may rest on its shape alone — a
			// library-rooted statement vouches numeric intent, values
			// outside the alphabet prove it
			(target.LibraryAdapter != "" || !WithinCodepointDoor(*target.Set, false)) {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" is a string or an array, and the position states "+
					"a number — it is not allowed here",
			))
			return
		}
	}
	// elements that may include NaN sit outside every set: even
	// where the sequence claim fits the target, the value is not
	// provably a member — alert, never accept (refutations below
	// stay sound: a NaN-bearing tuple is a member of nothing)
	if known.NaNElements {
		if checkWornNaNElements(p) {
			return
		}
		ctx.Report(assignability.At(
			node,
			7002,
			assignability.AlertText+" The elements may include NaN, which no stated "+
				"set admits.",
		))
		return
	}
	// syntactically IDENTICAL sets prove the LANGUAGE question
	// outright — A ⊆ A needs no kernel ask, and it is exactly the
	// guard-then-use shape a stated `.not(...)` difference produces.
	// BOUNDED temporal positions are not proven by the language
	// alone (their claim lives in the bounds rider), so they keep
	// the full path.
	identicalSets := target.Temporal == nil && sameSetJSON(known.Set, *target.Set)
	if identicalSets {
		return
	}
	// both directions are theorems on sequence shapes too
	// (seqSubsetB_true / seqSubsetB_false)
	if !ctx.Kernel.SeqSubset(known.Set, *target.Set) {
		var spelledKnown string
		if known.Temporal != nil {
			spelledKnown = refinementsets.FormatTemporal(*known.Temporal)
		} else {
			spelledKnown = p.SpelledSet(known.Set)
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+spelledKnown+"' is not assignable "+
				"to type '"+p.SpelledTarget+"'",
		))
		return
	}
	// a SEQUENCE MEASURE the target states (an exact reduce total,
	// non-decreasing order) is a claim beyond the language: an exact
	// tuple computes it and refutes on a mismatch, a value carrying
	// the same parse-checked measure proves it by identity, and
	// anything else keeps the alert — the language fitting alone
	// never proves a measure
	word, wordOk := refinementsets.WordOf(known.Set)
	if MeasureReported(ctx, target.Measures, word, wordOk, known.Measures, node, what, p.SpelledSet(known.Set)) {
		return
	}
	// the language fits; BOUNDED temporal positions never pass on
	// that alone — an annotation flow proves its bounds imply the
	// target's, anything else stays unproven, loudly
	if p.BoundsStated && target.Temporal != nil {
		CheckTemporalBounds(ctx, known, *target.Temporal, node, p.SpelledTarget)
	}
}

// checkWornScalarSubset is the TS source's onOneTupleLayer branch of
// checkWornSet, wrapped in its own try/catch (the "not provably an
// integer" hint's follow-up question).
func checkWornScalarSubset(p WornSetParams) {
	ctx, known, target, node, what := p.Ctx, p.Known, p.Target, p.Node, p.What
	if ctx.Kernel.ScalarSubset(known.Set, *target.Set) {
		return
	}
	// when "not provably an integer" is the ONE missing piece —
	// the same set WITH the integer form would be contained —
	// the message names the repair, like the `**` alert does
	hint := ""
	knownInt := false
	for _, f := range known.Set.Forms {
		if f.Form == refinementsets.FormInteger {
			knownInt = true
			break
		}
	}
	targetInt := false
	for _, f := range target.Set.Forms {
		if f.Form == refinementsets.FormInteger ||
			(f.Form == refinementsets.FormMultipleOf && f.A == float64(int64(f.A))) {
			targetInt = true
			break
		}
	}
	if targetInt && !knownInt && !p.Stringy {
		hint = wornIntegerHint(ctx, known.Set, target.Set)
	}
	ctx.Report(assignability.At(
		node,
		7001,
		what+" of type '"+p.SpelledSet(known.Set)+"' is not assignable "+
			"to type '"+p.SpelledTarget+"'"+hint,
	))
}

// wornIntegerHint is the TS source's try/catch around the follow-up
// scalarSubset question that decides which repair to name. Empty
// where the question was declined — the plain message stands.
func wornIntegerHint(ctx *FlowContext, knownSet refinementsets.RefinedSet, targetSet *refinementsets.RefinedSet) (hint string) {
	defer func() {
		if recover() != nil {
			hint = ""
		}
	}()
	withInteger := append(append([]refinementsets.Refinement{}, knownSet.Forms...), refinementsets.Integer)
	if ctx.Kernel.ScalarSubset(refinementsets.MakeRefinedSet(withInteger...), *targetSet) {
		// the overflow-honest shape (integer ∪ ±∞, the add/sub
		// transfer's exact ECMA reading) fails integer ONLY at
		// the infinities — Number.isInteger is the wrong repair
		// there; the finite guard is the right one. The ±∞ arm
		// is the spec's own: Number::add step 8 rounds by "the
		// Number value for" (number-value-for), whose rounding
		// set includes ±2^1024, replaced with ±∞ when chosen
		if IntegersOrInfinities(knownSet) {
			return " — an integer on every finite run, but the " +
				"arithmetic can round to ±∞ (ECMA-262 " +
				"number-value-for replaces a chosen ±2^1024 with " +
				"the infinity): guard with `Number.isFinite(…)`, " +
				"or bound the operands"
		}
		return " — the range fits, but the value is not " +
			"provably an integer: guard the input with " +
			"`Number.isInteger(…)`, or make it one with " +
			"`Math.floor`/`Math.round`"
	}
	return ""
}

// checkWornNaNElements is the TS source's try/catch around
// ctx.kernel.seqSubset for the NaN-bearing branch: true when a
// diagnostic was reported (the caller returns); false mirrors the TS
// catch — narrows nothing, and the caller's own alert follows.
func checkWornNaNElements(p WornSetParams) (reported bool) {
	ctx, known, target, node, what := p.Ctx, p.Known, p.Target, p.Node, p.What
	defer func() {
		if recover() != nil {
			reported = false
		}
	}()
	if !ctx.Kernel.SeqSubset(known.Set, *target.Set) {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+p.SpelledSet(known.Set)+"' is not "+
				"assignable to type '"+p.SpelledTarget+"'",
		))
		return true
	}
	return false
}

// sameSetJSON compares the TS source's `JSON.stringify(a) === JSON.stringify(b)`
// on two refinement sets — a deep structural equality mirroring
// abstractdomain's own sameRefinedSet, kept local since this file
// only needs it once (identicalSets) and abstractdomain does not
// export its equivalent.
func sameSetJSON(a, b refinementsets.RefinedSet) bool {
	ab, aErr := json.Marshal(a)
	bb, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return string(ab) == string(bb)
}
