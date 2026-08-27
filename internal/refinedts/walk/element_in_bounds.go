// from evaluation/element_in_bounds.ts
//
// In-bounds proofs for repetition-shaped sequence element reads:
// counting floors, length-ledger rows, sum indexes, heterogeneous
// tuple layers, and unvouched number-typed indexes.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// isStringGroundElement is the TS source's isStringGroundElement:
// whether an element set is the bare STRING GROUND — every string, no
// constraint. Such an element carries no information beyond the
// sort, and answering it whole re-spells the alphabet into every
// memo key and join (measured in the TS source: the inline walks went
// 1.5s → 59s on recharts when string-array reads began answering);
// the read answers the sort's own unknown instead.
//
// The TS source memoizes this by RefinedSet OBJECT IDENTITY
// (WeakMap<RefinedSet, boolean>) — a real speed optimization, not a
// correctness requirement (the recomputed answer is always the same
// for the same value). Go's RefinedSet is a value type with no stable
// identity to key a memo on without risking staleness across copies,
// so — mirroring kernelbridge's canonicalKeyOf memo drop
// (go-port-tracker.md: "no correctness loss, only the memo-hit speed
// win") — this recomputes every call instead of memoizing.
func isStringGroundElement(set refinementsets.RefinedSet) bool {
	if len(set.Forms) != 1 {
		return false
	}
	if set.Forms[0].Form != refinementsets.FormStar {
		return false
	}
	return sameStringGroundAlphabet(*set.Forms[0].A_)
}

// sameStringGroundAlphabet compares a set to refinementsets.Codepoints
// structurally — refinementsets exposes no exported equality
// (sameSetJSON is unexported, package-internal), so this walks the
// two known-shape sets field by field. Codepoints is
// `integer, (atLeast(0)∧atMost(0xD7FF)) ∪ (atLeast(0xE000)∧atMost(0x10FFFF))`
// (codepoint_sets.go).
func sameStringGroundAlphabet(set refinementsets.RefinedSet) bool {
	codepoints := refinementsets.Codepoints
	if len(set.Forms) != len(codepoints.Forms) {
		return false
	}
	for i, f := range set.Forms {
		other := codepoints.Forms[i]
		if f.Form != other.Form {
			return false
		}
		switch f.Form {
		case refinementsets.FormInteger:
			// nothing further to compare
		case refinementsets.FormUnion:
			if f.A_ == nil || f.B == nil || other.A_ == nil || other.B == nil {
				return false
			}
			if !sameFormSlice(f.A_.Forms, other.A_.Forms) || !sameFormSlice(f.B.Forms, other.B.Forms) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func sameFormSlice(a, b []refinementsets.Refinement) bool {
	if len(a) != len(b) {
		return false
	}
	for i, f := range a {
		if f.Form != b[i].Form || f.A != b[i].A {
			return false
		}
	}
	return true
}

// UncheckedIndexedAccessHonored: does the project under check state
// noUncheckedIndexedAccess? That flag decides what the SHAPE CHANNEL
// answers for an indexed read — `T | undefined` with it on, `T` with
// it off (service/program_project.go adopts it for exactly that
// reason). The refinement layer's own absence at an in-bounds element
// read tracks it, so the two layers state the same thing about the
// same read rather than one refusing what the other admits.
//
// tsc's `strict` does NOT imply the flag, so the common case — every
// project that does not name it, this corpus included — is off, and
// an in-bounds read answers the element outright.
//
// False wherever the program handle is absent (a unit-test context
// built straight from a checker, program/checker_program.go's nil
// Program), which is the flag's own default.
func UncheckedIndexedAccessHonored(ctx *FlowContext) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Program == nil {
		return false
	}
	options := ctx.P.Program.Options()
	if options == nil {
		return false
	}
	return options.NoUncheckedIndexedAccess == core.TSTrue
}

// OutOfBoundsEvidenceParams is the destructured-parameters struct for
// OutOfBoundsEvidence.
type OutOfBoundsEvidenceParams struct {
	Ctx        *FlowContext
	Env        Env
	Expression *ast.Node // ElementAccessExpression
}

// OutOfBoundsEvidence is outOfBoundsEvidence in the TS source:
// POSITIVE out-of-bounds evidence at an element read — never mere
// ignorance. Two facts qualify, each vouched by the program's own
// guards: the loop's certified integer window admits negative
// indexes while staying bounded above (a decrementing index), or the
// guard's own row admits index == length (`i <= xs.length`) with no
// stricter proof anywhere. "" (with ok=false) when neither holds.
func OutOfBoundsEvidence(p OutOfBoundsEvidenceParams) (string, bool) {
	elem := p.Expression.AsElementAccessExpression()
	argument := elem.ArgumentExpression
	for ast.IsParenthesizedExpression(argument) {
		argument = argument.AsParenthesizedExpression().Expression
	}
	// the index's held knowledge, read without re-evaluating it
	var heldIndex abstractdomain.AbstractValue
	hasHeldIndex := false
	if ast.IsIdentifier(argument) {
		heldIndex, hasHeldIndex = p.Env.Get(argument.Text())
	}
	if hasHeldIndex && heldIndex.Kind == abstractdomain.KindSet && heldIndex.SetKindTag == abstractdomain.SetKindTagNone {
		r := RangeOfSet(heldIndex.Set)
		if r != nil && r.Int && r.Lo < 0 && isFinite(r.Hi) {
			return "the index's certified window admits negative values — " +
				"the read can be out of bounds", true
		}
	}
	receiverPlace := dataflowfacts.PlaceKeyOf(p.Ctx.P.Checker, elem.Expression)
	indexSide := dataflowfacts.OffsetPlaceOf(p.Ctx.P.Checker, argument)
	if receiverPlace == nil || indexSide == nil {
		return "", false
	}
	lengthPlace := dataflowfacts.PlaceKey{Base: receiverPlace.Base, Path: receiverPlace.Path + ".length", BaseName: receiverPlace.BaseName}
	row := dataflowfacts.DifferenceConstraintFor(p.Ctx.DifferenceConstraints, lengthPlace, indexSide.Place)
	if row == nil || row.Strict || row.Bound-float64(indexSide.Offset) != 0 {
		return "", false
	}
	// the non-strict row is the guard's own vouching that the index
	// reaches the length; only a stricter proof elsewhere retires it
	strict := dataflowfacts.ConstraintsImply(
		p.Ctx.Kernel, p.Ctx.DifferenceConstraints, lengthPlace, indexSide.Place, float64(indexSide.Offset), true,
	)
	if strict {
		return "", false
	}
	return "the guard admits an index equal to the length — the read can " +
		"be out of bounds on the last pass", true
}

// indexHeldFor is the element access's own index argument, read the
// same way OutOfBoundsEvidence reads it — without re-evaluating the
// expression — so the decline sentence's "held" slot states what the
// walk actually carried for the index, not the receiver or the whole
// read. Unknown where the argument is not a plain identifier the
// environment tracks.
func indexHeldFor(ctx *FlowContext, env Env, expression *ast.Node) abstractdomain.AbstractValue {
	elem := expression.AsElementAccessExpression()
	argument := elem.ArgumentExpression
	for ast.IsParenthesizedExpression(argument) {
		argument = argument.AsParenthesizedExpression().Expression
	}
	if ast.IsIdentifier(argument) {
		if held, ok := env.Get(argument.Text()); ok {
			return held
		}
	}
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindUnknown}
}

// IndexBelowLengthPlace: does a `.length` ledger row (a guard's `i <
// xs.length`, or a sum row for `xs[a + b]`) prove the index strictly
// below the receiver's length, whatever SHAPE the receiver's own
// AbstractValue carries? Factored out of InBoundsElementOf's
// repetition-only reading so an OBJECT-STAR receiver (an unread array
// parameter, which carries no Repetition set to floor against at
// all) can ask the identical relational question — the length ledger
// a guard establishes is a claim about the PLACE `xs.length` denotes,
// independent of whether the walk also proved a counting floor on
// xs's own shape. Mirrors InBoundsElementOf's own underLength/
// underSum arms exactly (row lookup, then the kernel's linear
// composition, then the two-place sum case); the floor arm
// (window.Hi < rep.Lo) stays there, since only a Repetition receiver
// carries a floor to compare against.
func IndexBelowLengthPlace(ctx *FlowContext, env Env, receiverExpression, argumentExpression *ast.Node) bool {
	receiverPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, receiverExpression)
	indexSide := dataflowfacts.OffsetPlaceOf(ctx.P.Checker, argumentExpression)
	if receiverPlace == nil || indexSide == nil {
		return false
	}
	lengthPlace := dataflowfacts.PlaceKey{Base: receiverPlace.Base, Path: receiverPlace.Path + ".length", BaseName: receiverPlace.BaseName}
	var row *dataflowfacts.DifferenceConstraint
	if dataflowfacts.SamePlace(indexSide.Place, lengthPlace) {
		// the index rooted at the length ITSELF (`xs[xs.length - 1]`) is
		// the identity row — length − length ≥ 0 holds of every run
		row = &dataflowfacts.DifferenceConstraint{Minuend: lengthPlace, Subtrahend: indexSide.Place, Bound: 0, Strict: false}
	} else {
		row = dataflowfacts.DifferenceConstraintFor(ctx.DifferenceConstraints, lengthPlace, indexSide.Place)
	}
	underLength := false
	if row != nil {
		underLength = row.Bound-float64(indexSide.Offset) > 0 ||
			(row.Strict && row.Bound-float64(indexSide.Offset) >= 0)
	} else if dataflowfacts.AnyRowMentions(ctx.DifferenceConstraints, lengthPlace) {
		underLength = dataflowfacts.ConstraintsImply(
			ctx.Kernel, ctx.DifferenceConstraints, lengthPlace, indexSide.Place, float64(indexSide.Offset), true,
		)
	}
	if underLength {
		return true
	}
	return SumIndexInBounds(ctx, env, argumentExpression, lengthPlace)
}

// InBoundsElementOfParams is the destructured-parameters struct for
// InBoundsElementOf.
type InBoundsElementOfParams struct {
	Ctx        *FlowContext
	Env        Env
	Expression *ast.Node // ElementAccessExpression
	// Receiver is an AbstractValue known to be Kind == KindSet — the
	// TS source's `Extract<AbstractValue, { kind: "set" }>` narrowing.
	Receiver abstractdomain.AbstractValue
	Index    abstractdomain.AbstractValue
}

// InBoundsElementOf is inBoundsElementOf in the TS source: element of
// a tracked set-shaped sequence, or nil when this branch does not
// answer.
func InBoundsElementOf(p InBoundsElementOfParams) *abstractdomain.AbstractValue {
	elem := p.Expression.AsElementAccessExpression()
	// a repetition-shaped sequence with the index PROVABLY in
	// bounds: below the raised counting floor, or below the actual
	// length by a ledger row (i < xs.length carried from a guard or
	// a loop condition) — either way the read is defined on every
	// run and yields the repetition's element set. Arrays only — a
	// string's element is a one-character STRING (a UTF-16 unit),
	// not the element set's number
	window := IndexWindow(p.Index)
	if window != nil {
		rep, repOk := refinementsets.AsRepetition(p.Receiver.Set)
		if repOk {
			underFloor := window.Hi < float64(rep.Lo)
			// the relational bound reads the index as place + offset:
			// `i - 1` with i < xs.length is below the length too (an
			// array index sits under 2^32, so the offset arithmetic is
			// exact) — IndexBelowLengthPlace carries the length-ledger
			// row lookup, the kernel's linear composition, and the
			// two-place sum case; only the FLOOR (this shape's own
			// counting proof, which no other receiver shape carries)
			// stays local to this arm.
			underLengthOrSum := !underFloor && IndexBelowLengthPlace(p.Ctx, p.Env, elem.Expression, elem.ArgumentExpression)
			if underFloor || underLengthOrSum {
				// a bare-ground string element states nothing beyond the
				// sort — the in-bounds read answers unknown, cheaply
				if isStringGroundElement(rep.Element) {
					out := silence.Residue()
					return &out
				}
				// A STRING receiver is dense by construction: its positions
				// are UTF-16 units of an immutable primitive, so an in-range
				// index always names a unit and the read is exactly the
				// element. Arrays get the absence below.
				receiverIsString := primitives.IsStringKind(p.Ctx.P.Checker, elem.Expression)
				// a SUM measure bounds nonnegative elements: the fold of
				// nonnegatives is monotone under fl (fl(a+b) ≥ max(a,b)
				// for a, b ≥ 0), so no element exceeds the stated total
				element := rep.Element
				if p.Receiver.Measures != nil && p.Receiver.Measures.HasSum {
					elementRange := RangeOfSet(rep.Element)
					if elementRange != nil && elementRange.Lo >= 0 {
						forms := append(append([]refinementsets.Refinement{}, rep.Element.Forms...), refinementsets.AtMost(p.Receiver.Measures.Sum))
						element = refinementsets.MakeRefinedSet(forms...)
					}
				}
				read := abstractdomain.KnownSet(element, nil, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(p.Receiver), abstractdomain.TrustLevelOf(p.Index)), abstractdomain.SetKindTagNone)
				// NaN rides beside the element where the sequence may
				// hold it
				if p.Receiver.NaNElements {
					out := abstractdomain.PossiblyNaN(read)
					return &out
				}
				// The index is IN BOUNDS, which is not the same as the slot
				// being POPULATED. Every arm above (underFloor, underLength,
				// underSum) is a claim about the LENGTH — the floor is raised
				// by a length guard (assume_condition.go's
				// LengthGuardNarrowings), the other two read a `.length`
				// ledger row. On an array with holes, length COUNTS the holes:
				// `const t = []; t[3] = x` has length 4 with slots 0..2
				// absent, so `i < t.length` holds at i = 1 while `t[1]` is
				// undefined. A repetition set is a membership claim about the
				// positions that ARE there ("every element lies in this set,
				// count between lo and hi") and states nothing about density,
				// so a receiver that ARRIVED as a repetition — a parameter, a
				// value read back through a summary — is unproved and the
				// read wears the absence.
				//
				// This is the same answer the honored noUncheckedIndexedAccess
				// gives type-side (`T | undefined`), so the two layers agree
				// rather than one contradicting the other. A receiver the walk
				// itself built element-by-element is a KindList, not a
				// repetition, and keeps its exact bare read
				// (element_access.go).
				if receiverIsString {
					return &read
				}
				// SeqDenseKnown/SeqDense (abstractdomain/abstract_value.go,
				// KindArrayHoles's Dense/DenseKnown mirrored onto a
				// repetition-shaped KindSet) is the proof this arm was
				// missing: a receiver PROVED dense at this window --
				// MapOutcome's own write-every-counted-index construction,
				// callback_element_outcome.go -- has no hole to answer.
				// In bounds under the window IS populated, and the read is
				// exactly the element, no maybe wrapper at all.
				if p.Receiver.SeqDenseKnown && p.Receiver.SeqDense {
					return &read
				}
				// The absence above is the SHAPE CHANNEL's own answer, and
				// the shape channel is the project's tsc. With
				// noUncheckedIndexedAccess ON, GetTypeAtLocation answers
				// `T | undefined` at every indexed read and this wrapper
				// agrees with it. With the flag OFF — the default, and what
				// tsc's own `strict` leaves it at (program_project.go's
				// banner) — the host answers `T`, and wrapping anyway makes
				// the refinement layer contradict the shape channel at every
				// in-bounds read: `arr.length > 0` then `arr[0]` reads as
				// `Age | undefined` where tsc reads `Age`, so a return into
				// an Age position is refused against a value the host has
				// already accepted. The absence rides only where the host
				// puts one.
				if !UncheckedIndexedAccessHonored(p.Ctx) {
					return &read
				}
				// sec-ordinaryget: a hole (no own property at that index)
				// falls through to the prototype, and Array.prototype has
				// no numeric slots, so the get answers exactly undefined —
				// never null. The wrapper's own absent side is UndefOnly.
				out := abstractdomain.PossiblyAbsent(read, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, false)
				return &out
			}
		}
	}
	// a HETEROGENEOUS tuple (nested concatenation of scalar
	// layers) reads its i-th layer at an exact in-range index —
	// the uniform case peeled as a repetition above, this walks
	// the spine the tuple compiler nested
	{
		var exactIndex float64
		hasExact := false
		if p.Index.Kind == abstractdomain.KindValues && len(p.Index.Values) == 1 && isInteger(p.Index.Values[0]) && p.Index.Values[0] >= 0 {
			exactIndex, hasExact = p.Index.Values[0], true
		}
		if hasExact {
			layer := p.Receiver.Set
			remaining := exactIndex
			descended := false // whether the loop moved past the receiver's own set at least once
			for {
				var only *refinementsets.Refinement
				if len(layer.Forms) == 1 {
					only = &layer.Forms[0]
				}
				if only == nil || only.Form != refinementsets.FormConcatenation {
					break
				}
				if remaining == 0 {
					if refinementsets.OnOneTupleLayer(*only.A_) {
						out := abstractdomain.KnownSet(*only.A_, nil, abstractdomain.TrustLevelOf(p.Receiver), abstractdomain.SetKindTagNone)
						return &out
					}
					break
				}
				remaining--
				layer = *only.B
				descended = true
			}
			// the innermost layer IS the last element's set
			if remaining == 0 && descended && len(layer.Forms) > 0 {
				allowed := true
				for _, f := range layer.Forms {
					if f.Form == refinementsets.FormConcatenation || f.Form == refinementsets.FormStar ||
						f.Form == refinementsets.FormRepeat || f.Form == refinementsets.FormEmptyTuple ||
						f.Form == refinementsets.FormWord {
						allowed = false
						break
					}
				}
				if allowed && refinementsets.OnOneTupleLayer(layer) {
					out := abstractdomain.KnownSet(layer, nil, abstractdomain.TrustLevelOf(p.Receiver), abstractdomain.SetKindTagNone)
					return &out
				}
			}
		}
	}
	// no branch PROVED the read in bounds — where the guards
	// themselves vouch the index can leave the bounds, the read says
	// so before any maybe-absent answer papers over it
	//
	// THE DECLINE HELPER, adopted here — this site sits outside the
	// judge (CheckAssignability never opens over an element-access
	// read), so there is no root span for a bare AlertText fallback
	// to leave answered; but the same one-carrier rule still applies
	// to the printed sentence itself: the evidence ALREADY names the
	// blocking construct (the certified window, or the guard's own
	// equal-to-length admission), so gluing the generic alert text
	// onto it doubled the sentence instead of replacing it. The
	// projection composes the one sentence from the gate and what
	// the index held, with no second generic clause trailing it.
	evidence, hasEvidence := OutOfBoundsEvidence(OutOfBoundsEvidenceParams{Ctx: p.Ctx, Env: p.Env, Expression: p.Expression})
	if hasEvidence {
		heldIndex := indexHeldFor(p.Ctx, p.Env, p.Expression)
		messageText := evidence + ". " + assignability.AlertText
		if projected := DeclineSentence(evidence, p.Expression, spellUnknownHeld(heldIndex)); projected != "" {
			messageText = projected
		}
		p.Ctx.Report(assignability.At(p.Expression, 7002, messageText))
	}
	// an UNVOUCHED read of a repetition-shaped sequence at a
	// NUMBER-typed index is still an element OR undefined — out of
	// range (or fractional) answers the absent value, and in range the
	// element set or, on an array with holes, undefined again
	// (sec-array-exotic-objects). Absence then narrows like any other
	// maybe. A string-typed index could name "length" or a method — no
	// claim.
	//
	// The earlier wording here said "a validated array has no holes",
	// which claimed more than the repetition proves: a repetition states
	// what the present positions hold and how many there are, never that
	// the slots between them are populated. It does not matter to THIS
	// arm's answer — the index is unvouched, so the read wears the
	// absence either way — but the same sentence was doing load-bearing
	// work at the vouched arm above, where it is now retired.
	rep, repOk := refinementsets.AsRepetition(p.Receiver.Set)
	if repOk && !p.Receiver.NaNElements &&
		(typereading.TypeAtLocation(p.Ctx.P.Checker, elem.ArgumentExpression).Flags()&checker.TypeFlagsNumberLike) != 0 {
		// POSITIVELY derived absence: an unvouched index may sit out
		// of range, where a get answers undefined. A bare-ground
		// string element wraps the sort's own unknown — the absence
		// is the whole claim
		var inner abstractdomain.AbstractValue
		if isStringGroundElement(rep.Element) {
			inner = silence.Residue()
		} else {
			inner = abstractdomain.KnownSet(rep.Element, nil, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(p.Receiver), abstractdomain.TrustSpec), abstractdomain.SetKindTagNone)
		}
		// sec-ordinaryget: an out-of-range get answers exactly undefined,
		// never null — the wrapper's own absent side is UndefOnly.
		out := abstractdomain.PossiblyAbsent(inner, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true)
		return &out
	}
	return nil
}
