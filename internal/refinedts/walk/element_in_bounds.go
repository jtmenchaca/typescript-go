// from evaluation/element_in_bounds.ts
//
// In-bounds proofs for repetition-shaped sequence element reads:
// counting floors, length-ledger rows, sum indexes, heterogeneous
// tuple layers, and unvouched number-typed indexes.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
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
			// array index sits under 2^32, so the offset arithmetic
			// is exact)
			receiverPlace := dataflowfacts.PlaceKeyOf(p.Ctx.P.Checker, elem.Expression)
			indexSide := dataflowfacts.OffsetPlaceOf(p.Ctx.P.Checker, elem.ArgumentExpression)
			var lengthPlace *dataflowfacts.PlaceKey
			if receiverPlace != nil {
				lp := dataflowfacts.PlaceKey{Base: receiverPlace.Base, Path: receiverPlace.Path + ".length", BaseName: receiverPlace.BaseName}
				lengthPlace = &lp
			}
			// the index rooted at the length ITSELF (`xs[xs.length - 1]`)
			// is the identity row — length − length ≥ 0 holds of every
			// run, an array length being a real number by construction
			var row *dataflowfacts.DifferenceConstraint
			if lengthPlace != nil && indexSide != nil {
				if dataflowfacts.SamePlace(indexSide.Place, *lengthPlace) {
					row = &dataflowfacts.DifferenceConstraint{Minuend: *lengthPlace, Subtrahend: indexSide.Place, Bound: 0, Strict: false}
				} else {
					row = dataflowfacts.DifferenceConstraintFor(p.Ctx.DifferenceConstraints, *lengthPlace, indexSide.Place)
				}
			}
			// length − (i + offset) ≥ bound − offset, strict carried:
			// the read is in bounds when that gap clears zero. Where
			// no single row says it, the kernel's linear decider
			// composes the live rows (i < j and j < xs.length reach
			// the read the one-row lookup cannot).
			underLength := false
			if indexSide != nil {
				if row != nil {
					underLength = row.Bound-float64(indexSide.Offset) > 0 ||
						(row.Strict && row.Bound-float64(indexSide.Offset) >= 0)
				} else if lengthPlace != nil &&
					// no live row even MENTIONS the length: the system
					// can imply nothing about it — skip the kernel's
					// composition, it would answer no at one ask each
					dataflowfacts.AnyRowMentions(p.Ctx.DifferenceConstraints, *lengthPlace) {
					// the decider speaks about REALS, so the question is
					// the strict form — length − place > offset — and
					// the index's integrality (indexWindow) closes the
					// gap to the last slot
					underLength = dataflowfacts.ConstraintsImply(
						p.Ctx.Kernel, p.Ctx.DifferenceConstraints, *lengthPlace, indexSide.Place, float64(indexSide.Offset), true,
					)
				}
			}
			// a TWO-PLACE index (`s[offset + length - 1]`): the sum
			// row speaks about fl(offset + length). Both terms
			// nonnegative integers and the anchor an array length
			// (< 2^32, sec-array-exotic-objects) make the computed
			// sum EQUAL the real one — rounding an integer past 2^53
			// lands at or past 2^53, which the anchor's bound rules
			// out — so the row holds of the real sum and the read is
			// in bounds when the offsets clear the last slot
			underSum := false
			if !underFloor && !underLength && receiverPlace != nil {
				anchorPlace := dataflowfacts.PlaceKey{Base: receiverPlace.Base, Path: receiverPlace.Path + ".length", BaseName: receiverPlace.BaseName}
				underSum = SumIndexInBounds(p.Ctx, p.Env, elem.ArgumentExpression, anchorPlace)
			}
			if underFloor || underLength || underSum {
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
						f.Form == refinementsets.FormRepeat || f.Form == refinementsets.FormEmptyTuple {
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
	evidence, hasEvidence := OutOfBoundsEvidence(OutOfBoundsEvidenceParams{Ctx: p.Ctx, Env: p.Env, Expression: p.Expression})
	if hasEvidence {
		p.Ctx.Report(assignability.At(p.Expression, 7002, evidence+". "+assignability.AlertText))
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
		(p.Ctx.P.Checker.GetTypeAtLocation(elem.ArgumentExpression).Flags()&checker.TypeFlagsNumberLike) != 0 {
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
