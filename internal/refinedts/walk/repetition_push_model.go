// `xs.push(v)` on a REPETITION-shaped receiver — a declared `Age[]`
// parameter, or anything else whose positions the walk never
// enumerated but whose element set and counting window it holds.
//
// sec-array.prototype.push (specifications/javascript/spec.html) writes
// each argument at the end (step 5.a's `Set(obj, ToString(𝔽(length)),
// item, true)`, with length rising once per item) and then sets the
// array's own length to the new total. Two facts follow for a
// repetition, and they are the whole of what this reader claims:
//
//   - THE ELEMENT SET GROWS BY UNION — WHEN THE RECEIVER STATES NO
//     DECLARED ELEMENT OBLIGATION. After the push, a position holds
//     either what a position held before (the untouched ones) or one of
//     the pushed values (the new ones). So the element set is the union
//     of the two, and never anything narrower.
//
//   - A DECLARED ELEMENT SET IS AN INVARIANT, JUDGED AT THE PUSH. Where
//     the receiver's own DECLARATION states an element set (`xs:
//     Age[]`), that set is an obligation on every write, exactly the
//     way WriteBinding's declared set is (assignments.go). Pushing 200
//     onto such an `xs` is refused AT THE PUSH — not two lines later at
//     a read of the widened `Age ∪ 200` — and THE REFUSED-WRITE LAW
//     applies here too: the tracked element stays the meet of the
//     pushed value with the declared element, so a refused push cannot
//     smuggle 200 into the tracked set for a later read to refuse a
//     second time. An admitted push is unchanged by the meet (it was
//     already inside), so no exactness is lost on the silent path.
//
//   - BOTH COUNT BOUNDS RISE BY THE ARGUMENT COUNT. The array is
//     strictly longer by argCount, so a floor of lo becomes lo +
//     argCount and a ceiling of hi becomes hi + argCount. An unbounded
//     ceiling stays unbounded.
//
// Only push. pop/shift/splice REMOVE positions, and which element left
// decides what the remaining set is — a repetition states what its
// positions hold, never which position holds which, so the removed
// value cannot be named and the remaining set cannot be narrowed. Those
// keep the havoc the caller already gives them. `fill` replaces every
// position, which IS stateable, but its own reader would have to answer
// the exact-count case the flat-tuple path already covers; it is left
// to that path rather than split across two.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readRepetitionPush answers `xs.push(...)` for a tracked,
// repetition-shaped receiver: it updates the binding to the widened
// sequence and answers the new length. The receiver is known KindSet
// and the name known tracked — its caller checked both.
func readRepetitionPush(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver := site.Ctx, site.Env, site.E, site.Receiver
	trackedName := site.TrackedName
	if receiver.SetKindTag != abstractdomain.SetKindTagNone {
		return nil
	}
	rep, repOk := refinementsets.AsRepetition(receiver.Set)
	if !repOk {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if len(arguments) == 0 {
		// pushing nothing changes nothing — the array and its length are
		// exactly what they were (the step 5 loop runs zero times)
		out := lengthClaimOfRepetition(rep, receiver)
		return out
	}
	// the receiver's own DECLARATION — `xs: Age[]` — states an element
	// obligation on every write, the same invariant WriteBinding judges
	// a plain declared binding against. An array with no such
	// declaration (a tracked local the walk built no annotation for)
	// states no obligation, and the loop below judges nothing.
	var declaredElement *refinementsets.RefinedSet
	if declared, hasDeclared := ctx.Declared[trackedName]; hasDeclared && declared.Kind == annotations.DeclaredSet && declared.Set != nil {
		if declaredArms, ok := RepetitionArmsOf(*declared.Set); ok && len(declaredArms) == 1 {
			declaredElement = &declaredArms[0].Element
		}
	}
	element := rep.Element
	grade := abstractdomain.TrustLevelOf(receiver)
	for _, argument := range arguments {
		if ast.IsSpreadElement(argument) {
			// a spread's own item COUNT is not read here, so neither the
			// widened element set nor the raised window can be stated —
			// the class forgets rather than claim a count it does not have
			HavocEnv(ctx.Aliases, env, trackedName)
			out := silence.Residue()
			return &out
		}
		pushed := evaluateExpression(ctx, env, argument)
		if declaredElement != nil {
			// the write is judged AT THE PUSH — one refusal here, never a
			// second one at a later read of the widened set. The report is
			// captured rather than trusted blind: a REFUSAL (7001) is what
			// tells THE REFUSED-WRITE LAW below to hold the declared
			// element rather than let the pushed value widen it — an
			// UNDETERMINED verdict (7002) still alerts, but carries no
			// counterexample to narrow against, so it is not a refusal for
			// this purpose.
			refused := false
			judging := *ctx
			originalReport := ctx.Report
			judging.Report = func(d assignability.RefinementDiagnostic) {
				if d.Code == 7001 {
					refused = true
				}
				originalReport(d)
			}
			CheckAssignability(
				&judging,
				pushed,
				annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: declaredElement},
				argument,
				"the pushed value",
				nil,
			)
			// THE REFUSED-WRITE LAW, for a repetition's element exactly as
			// WriteBinding keeps it for a plain declared binding: a refused
			// push cannot widen the tracked element past what the
			// declaration already allowed, so it wears the declaration's
			// own element instead of the value that just got refused. An
			// admitted push keeps its own knowledge, which may be sharper
			// than the declaration (an exact value inside it).
			if refused {
				pushed = abstractdomain.KnownSet(*declaredElement, nil, abstractdomain.TrustLevelOf(pushed), abstractdomain.SetKindTagNone)
			}
		}
		pushedSet, ok := abstractdomain.SetOfKnown(pushed)
		if !ok {
			// a pushed value with no set spelling widens the element to
			// something this reader cannot name — forgetting is the only
			// sound answer, since keeping the old element would claim the
			// array still holds only what it held
			HavocEnv(ctx.Aliases, env, trackedName)
			out := silence.Residue()
			return &out
		}
		element = refinementsets.MakeRefinedSet(refinementsets.Union(element, pushedSet))
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(pushed))
	}
	// both bounds rise by the number of items written
	lo := rep.Lo + len(arguments)
	var hi *int
	if rep.Hi != nil {
		raised := *rep.Hi + len(arguments)
		hi = &raised
	}
	widened := abstractdomain.KnownSet(
		refinementsets.Repetition(element, lo, hi), nil, grade, abstractdomain.SetKindTagNone,
	)
	// pushing writes an OWN property at each new index (step 5.a's Set),
	// so a receiver already proved dense stays dense — the new positions
	// are populated by construction
	if receiver.SeqDenseKnown && receiver.SeqDense {
		widened = abstractdomain.KnownSetDense(widened)
	}
	UpdateTrackedEnv(ctx.Aliases, env, trackedName, widened)
	nextRep, _ := refinementsets.AsRepetition(widened.Set)
	return lengthClaimOfRepetition(nextRep, widened)
}

// lengthClaimOfRepetition is the length push answers: the exact count
// where the window pins one, and the window itself otherwise —
// sec-array.prototype.push returns 𝔽(length), the array's own new
// length, so the claim is exactly what the window says about it.
func lengthClaimOfRepetition(
	rep refinementsets.Repeated,
	of abstractdomain.AbstractValue,
) *abstractdomain.AbstractValue {
	grade := abstractdomain.TrustLevelOf(of)
	if rep.Hi != nil && *rep.Hi == rep.Lo {
		out := abstractdomain.KnownValues([]float64{float64(rep.Lo)}, abstractdomain.PrimitiveNumber, grade)
		return &out
	}
	forms := []refinementsets.Refinement{
		refinementsets.AtLeast(float64(rep.Lo)),
		refinementsets.Integer,
	}
	if rep.Hi != nil {
		forms = append(forms, refinementsets.AtMost(float64(*rep.Hi)))
	}
	out := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(forms...), nil, grade, abstractdomain.SetKindTagNone,
	)
	return &out
}
