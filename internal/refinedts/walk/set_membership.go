// from assignability/set_membership.ts
//
// Kernel membership at a stated set: exact values, a list against a
// repetition window, and stringy spelling. A worn set continues in
// worn_set_membership; temporal chart bounds in temporal_membership.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func formatValues(values []float64) string {
	if len(values) == 1 {
		return formatJSNumberLocal(values[0])
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = formatJSNumberLocal(v)
	}
	return "(" + joinCommaSpace(parts) + ")"
}

func joinCommaSpace(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// CheckListOrStructured is checkListOrStructured in the TS source:
// an exact list against a repetition-shaped statement, then a nested
// sequence / Map / Set / promise / date / regex that the tuple layer
// cannot formatAt. True when the caller should return.
func CheckListOrStructured(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) bool {
	if known.Kind == abstractdomain.KindList && target.Kind == annotations.DeclaredSet && target.KindTag == "" {
		window, hasWindow := refinementsets.AsRepetition(*target.Set)
		if hasWindow {
			count := len(known.Items)
			hiExceeded := window.Hi != nil && count > *window.Hi
			if count < window.Lo || hiExceeded {
				plural := "s"
				if count == 1 {
					plural = ""
				}
				ctx.Report(assignability.At(
					node,
					7001,
					what+" of "+strconv.Itoa(count)+" element"+plural+" is not "+
						"assignable to type '"+StatedSetWords(*target.Set, target.Word)+"'",
				))
				return true
			}
			// each exact item poses its own MEMBERSHIP question — the
			// word's tuple against the element set, directly, since the
			// item has no contextual position of its own
			undecided := false
			for _, item := range known.Items {
				var tuple []float64
				hasTuple := false
				if item.Kind == abstractdomain.KindValues &&
					(item.KindTag == abstractdomain.PrimitiveString || item.KindTag == abstractdomain.PrimitiveNumber || item.KindTag == abstractdomain.PrimitiveBoolean) {
					tuple = append([]float64{}, item.Values...)
					hasTuple = true
				}
				if !hasTuple {
					undecided = true
					continue
				}
				outcome := checkListMember(ctx, window.Element, tuple, node, what)
				if outcome == listMemberRefuted {
					return true
				}
				if outcome == listMemberDeclined {
					undecided = true
				}
			}
			if !undecided {
				return true // count and every element proved
			}
			// an undecided element keeps the alert below
		}
	}
	if known.Kind == abstractdomain.KindList || known.Kind == abstractdomain.KindCollection ||
		known.Kind == abstractdomain.KindPromise || known.Kind == abstractdomain.KindDate ||
		known.Kind == abstractdomain.KindRegex {
		// a nested sequence (or a Map/Set, or a promise) carries
		// structure the tuple layer cannot formatAt — unproven at a
		// set-stated position, honestly
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return true
	}
	return false
}

// listMemberOutcome is the outcome of one exact-list element's
// membership question against the repetition window's element set —
// the TS source's try/catch: a refusal falls through to
// `undecided = true` rather than a distinct return channel.
type listMemberOutcome int

const (
	listMemberOK listMemberOutcome = iota
	listMemberRefuted
	listMemberDeclined
)

func checkListMember(ctx *FlowContext, element refinementsets.RefinedSet, tuple []float64, node *ast.Node, what string) (outcome listMemberOutcome) {
	defer func() {
		if recover() != nil {
			outcome = listMemberDeclined
		}
	}()
	if !ctx.Kernel.Member(element, tuple) {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" holds an element that is not assignable to type '"+
				refinementsets.FormatForDiagnostics(element)+"'",
		))
		return listMemberRefuted
	}
	return listMemberOK
}

// CheckSetMembership is checkSetMembership in the TS source: values
// or a worn set against a stated set — one kernel membership or
// subset question, with stringy spelling.
func CheckSetMembership(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	checkSetMembershipOfArm(ctx, known, target, node, what, false)
}

// checkSetMembershipOfArm is checkSetMembership with the one extra
// fact the union dispatcher holds: this known is ONE ARM of a wider
// union, so a subset failure is a possibility about the value, not a
// verdict on it. No TS twin — the TS source states the definite
// sentence on every arm, which is the A2 misfire.
func checkSetMembershipOfArm(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	oneArmOf bool,
) {
	if target.Kind != annotations.DeclaredSet {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	// a string-sorted position reads its word AS the string — the
	// value as the quoted text, a literal-chain target as its quoted
	// text (the shapes are one word; only the sort tells them apart)
	stringy := (ctx.P.Checker.GetTypeAtLocation(node).Flags()&checker.TypeFlagsStringLike) != 0 ||
		(known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveString)
	spelledSet := func(set refinementsets.RefinedSet) string {
		if stringy {
			if literal, ok := refinementsets.FormatStringLiteral(set); ok {
				return literal
			}
		}
		return refinementsets.FormatForDiagnostics(set)
	}
	quoted := func(values []float64) (string, bool) {
		s, ok := stringFromCodepoints(values)
		if !ok {
			return "", false
		}
		return jsonQuote(s), true
	}

	// a TEMPORAL statement spells by its chart, never by the grammar's
	// syntax; the chart bounds resolve through the kernel's calendar
	temporal := target.Temporal
	var spelledTarget string
	switch {
	case temporal != nil:
		spelledTarget = refinementsets.FormatTemporal(*temporal)
	case target.Word != nil && target.Word.Covers == len(target.Set.Forms):
		spelledTarget = target.Word.Text
	default:
		spelledTarget = spelledSet(*target.Set)
	}
	boundsStated := temporal != nil && (temporal.HasMin || temporal.HasMax)

	reported := checkSetMembershipQuestions(ctx, known, target, node, what, stringy, quoted, spelledTarget, temporal, boundsStated, spelledSet, oneArmOf)
	if !reported.decided {
		// the kernel could not be asked at this position — a decline is
		// an outcome, stated in the tree's own plain sentence, never the
		// raw Go panic text (a nil kernel, an out-of-range wire) that
		// caused it
		ctx.Report(assignability.At(node, 7002, KernelDeclinedAlertText))
	}
}

type setMembershipOutcome struct {
	decided bool
}

// checkSetMembershipQuestions is the TS source's try/catch wrapping
// checkExactValues/checkWornSet's own kernel asks — kernelbridge's
// question methods panic on refusal (PORT.md), so this recovers into
// the TS catch(declinedQuestion) branch's outcome.
func checkSetMembershipQuestions(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	stringy bool,
	quoted func([]float64) (string, bool),
	spelledTarget string,
	temporal *refinementsets.TemporalAnnotation,
	boundsStated bool,
	spelledSet func(refinementsets.RefinedSet) string,
	oneArmOf bool,
) (outcome setMembershipOutcome) {
	outcome.decided = true
	defer func() {
		if recover() != nil {
			outcome.decided = false
		}
	}()
	if known.Kind == abstractdomain.KindValues {
		checkExactValues(ctx, known, target, node, what, stringy, quoted, spelledTarget, temporal, boundsStated)
		return outcome
	}
	if known.Kind != abstractdomain.KindSet {
		return outcome
	}
	CheckWornSet(WornSetParams{
		Ctx:           ctx,
		Known:         known,
		Target:        target,
		Node:          node,
		What:          what,
		Stringy:       stringy,
		SpelledSet:    spelledSet,
		SpelledTarget: spelledTarget,
		Temporal:      temporal,
		BoundsStated:  boundsStated,
		OneArmOf:      oneArmOf,
	})
	return outcome
}

func checkExactValues(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	stringy bool,
	quoted func([]float64) (string, bool),
	spelledTarget string,
	temporal *refinementsets.TemporalAnnotation,
	boundsStated bool,
) {
	// a NUMBER- or BOOLEAN-sorted values known is ONE OF its
	// words — each judges as its own 1-tuple; a string- or
	// array-sorted one IS the single tuple
	scalarWords := known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean
	// the admitted-language rule, decided from the two sides in hand:
	// the word carries the sort it was read under, and the stated
	// set's own forms state theirs. A scalar word never satisfies a
	// sequence-stating set, and a sequence word never satisfies a
	// scalar-stated one — the tuple pun ([65] is both 65 and "A") is
	// what membership alone cannot see. The codepoint door stays
	// open: a scalar window inside the codepoint range may BE a
	// single-character string statement, so only the sides outside it
	// refute here.
	if target.KindTag == "" {
		var say string
		switch {
		case known.KindTag == abstractdomain.PrimitiveNumber:
			say = "a number"
		case known.KindTag == abstractdomain.PrimitiveBoolean:
			say = "a boolean"
		case known.KindTag == abstractdomain.PrimitiveArray:
			say = "an array"
		default:
			say = "a string"
		}
		if scalarWords && StatesSequence(*target.Set) {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" is "+say+", and the position states a string or an "+
					"array — "+say+" is not allowed here",
			))
			return
		}
		// the target must be scalar-STATED, not merely scalar-membered:
		// a length-1 string statement (a repetition form) has 1-tuple
		// members too, and a string word there is legitimate — the
		// sequence forms and the codepoint door both keep membership in
		// charge
		if !scalarWords && refinementsets.OnOneTupleLayer(*target.Set) &&
			!StatesSequence(*target.Set) &&
			!WithinCodepointDoor(*target.Set, false) {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" is "+say+", and the position states a number — "+
					say+" is not allowed here",
			))
			return
		}
	}
	inside := true
	if scalarWords {
		for _, v := range known.Values {
			if !ctx.Kernel.Member(*target.Set, []float64{v}) {
				inside = false
				break
			}
		}
	} else {
		inside = ctx.Kernel.Member(*target.Set, known.Values)
	}
	if !inside {
		spelledValue := formatValues(known.Values)
		if stringy {
			if q, ok := quoted(known.Values); ok {
				spelledValue = q
			}
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+spelledValue+"' is not "+
				"assignable to type '"+spelledTarget+"'",
		))
		return
	}
	// the language fits; a stated sequence measure still judges
	// the exact elements (an array-sorted values known IS the
	// tuple)
	var elements []float64
	elementsOk := false
	if known.KindTag == abstractdomain.PrimitiveArray {
		elements = known.Values
		elementsOk = true
	}
	if MeasureReported(ctx, target.Measures, elements, elementsOk, nil, node, what, formatValues(known.Values)) {
		return
	}
	// the spelling is in the language; the chart's RANGE validity
	// and the stated bounds checkAssignability the VALUE it names
	if temporal != nil && known.KindTag == abstractdomain.PrimitiveString {
		text, ok := stringFromCodepoints(known.Values)
		if ok {
			CheckTemporalExact(ctx, *temporal, text, node, what, spelledTarget, boundsStated)
		}
	}
}

// stringFromCodepoints mirrors String.fromCodePoint(...values): a
// RangeError on any value outside 0..0x10FFFF (integer-checked)
// becomes ok=false, the TS source's catch. Within range, it is exactly
// stringOfCodepoints (refine_on_exact.go) — both build the UTF-16
// string from Unicode scalar values.
func stringFromCodepoints(values []float64) (string, bool) {
	for _, v := range values {
		if v != float64(int64(v)) || v < 0 || v > 0x10FFFF {
			return "", false
		}
	}
	return stringOfCodepoints(values), true
}
