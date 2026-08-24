// from assignability/maybe_and_union.ts
//
// Maybe-target, possibly-absent refute, union arms, refinement
// variables, and bigint / symbol / function — each a named export
// answering one question at a checked position. NaN lives in
// nan_wrapper.ts.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CheckMaybeTarget is checkMaybeTarget in the TS source: a MAYBE
// target admits absence — the absent value passes, present
// knowledge judges against the inner statement.
func CheckMaybeTarget(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) {
	if known.Kind == abstractdomain.KindUndef {
		return
	}
	inner := known
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		inner = *known.Inner
	}
	CheckAssignability(ctx, inner, *target.Inner, node, what, positionType)
}

// RefutePossiblyAbsent is refutePossiblyAbsent in the TS source:
// possibly-absent knowledge at a position that admits no absence:
// the absent value is a member of no stated set and carries no
// stated object's keys, so the claim REFUTES — this is `p!` checked
// as the unwrap it is.
func RefutePossiblyAbsent(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	if target.Kind == annotations.DeclaredVariable {
		// an UNGROUNDED variable's bound is a plain-TS constraint
		// (`extends string`, a bare T) — the position states no
		// refinement, so the judge stays out of tsc's layer
		// (`undefined as DefaultValue` is nest's own idiom)
		if !target.BoundGrounded {
			return
		}
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	var statedWords string
	if target.Kind == annotations.DeclaredSet {
		if target.Temporal != nil {
			statedWords = "type '" + refinementsets.FormatTemporal(*target.Temporal) + "'"
		} else {
			statedWords = "type '" + StatedSetWords(*target.Set, target.Word) + "'"
		}
	} else {
		statedWords = "the stated object"
	}
	fix, hasFix := GuardFix(node, target)
	var messageText string
	if known.Kind == abstractdomain.KindUndef {
		messageText = what + " of type 'undefined' is not assignable to " +
			statedWords + " — the absent value is a member of no " +
			"refined set"
	} else {
		// The wrapper's own absent side names which runtime value the
		// refutation is about: NullOnly proves exactly 'null' admitted,
		// UndefOnly exactly 'undefined'; the zero value (conflated)
		// keeps the pre-flavor wording, since it may be either.
		switch known.AbsentSide {
		case abstractdomain.AbsentFlavorNullOnly:
			messageText = what + " may be 'null', which is not assignable to " + statedWords
		case abstractdomain.AbsentFlavorUndefOnly:
			messageText = what + " may be 'undefined', which is not assignable to " + statedWords
		default:
			messageText = what + " may be 'undefined', which is not assignable to " + statedWords
		}
	}
	base := assignability.At(node, 7001, messageText)
	if hasFix {
		base.Fix = &assignability.RefinementFix{Title: fix.Title, NewText: fix.NewText, InsertAt: fix.InsertAt}
	}
	ctx.Report(base)
}

// CheckKindUnion is checkKindUnion in the TS source: ONE of a few
// sort-distinguished claims: the union is assignable only when EVERY
// arm is — some arm provably outside refutes, and an arm the kernel
// cannot decide keeps the alert.
func CheckKindUnion(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) {
	var captured []assignability.RefinementDiagnostic
	probe := *ctx
	probe.Report = func(d assignability.RefinementDiagnostic) {
		captured = append(captured, d)
	}
	// each arm judges as an ARM: the value may take it, so a refutation
	// it earns is stated as a possibility, not as a verdict on the
	// whole value (the numeric enum's reverse read is values ∪ names,
	// and the names arm missing an enum-typed position does not make
	// the value a name)
	for _, arm := range known.Arms {
		CheckAssignabilityOfArm(&probe, arm, target, node, what, positionType, true)
	}
	var refuted *assignability.RefinementDiagnostic
	for i := range captured {
		if captured[i].Code == 7001 {
			refuted = &captured[i]
			break
		}
	}
	if refuted != nil {
		out := *refuted
		// the whole union traces to ONE reader worth naming (JSON.parse
		// of unknown text, and kin) — an individual arm's own refutation
		// sentence is about that arm's internal shape (e.g. "an object
		// is not allowed here"), which is true but hides which call
		// produced the claim in the first place. Prefer the union's own
		// sentence when the reader that built it left one.
		if known.ResidueReason != "" {
			out.MessageText = known.ResidueReason
		}
		ctx.Report(out)
		return
	}
	if len(captured) > 0 {
		fix, hasFix := GuardFix(node, target)
		messageText := assignability.AlertText
		if known.ResidueReason != "" {
			messageText = known.ResidueReason
		}
		base := assignability.At(node, 7002, messageText)
		if hasFix {
			base.Fix = &assignability.RefinementFix{Title: fix.Title, NewText: fix.NewText, InsertAt: fix.InsertAt}
		}
		ctx.Report(base)
	}
}

// CheckVariableTarget is checkVariableTarget in the TS source: a
// REFINEMENT VARIABLE target — proven exactly by identity.
func CheckVariableTarget(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
) {
	if known.Kind == abstractdomain.KindVariable && known.Symbol == target.Symbol &&
		known.StarDepth == target.StarDepth {
		return
	}
	// a GROUNDED bound is a real refinement claim and identity is
	// the only proof; an ungrounded one is plain TypeScript, and the
	// judge says nothing there (`R[]`, `'http' as TContext`)
	if !target.BoundGrounded {
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// CheckVariableKnown is checkVariableKnown in the TS source: a value
// wearing a variable judges THROUGH its bound: sound for the
// universal claim, since every finite subset of the bound — each
// singleton included — is an admissible T.
func CheckVariableKnown(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) {
	widened, ok := abstractdomain.SetOfKnown(known)
	if !ok {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	if len(widened.Forms) == 0 {
		// the root bound states nothing — unproven, honestly
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	CheckAssignability(
		ctx,
		abstractdomain.KnownSet(widened, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		target,
		node,
		what,
		positionType,
	)
}

// CheckBigints is checkBigints in the TS source: a bigint value
// where the statement wears the bigint kindTag: the kernel decides
// membership over its exact integers. Every OTHER declared-set kind
// tag ("" for the double sort, "symbol", "boolean") is a sound cross-
// sort refutation on its own — `typeof` alone separates bigint from
// every other sort, so no run of `10n` at a number-stated (or string-,
// boolean-, symbol-stated) position ever satisfies it, the same
// standing worn_set_membership.go's own scalar/sequence cross-sort
// check rests on. This mirrors checkExactValues/checkWornSet's own
// cross-sort branches rather than falling to the generic undetermined
// alert, which used to leave EVERY non-bigint-stated position silent
// about a value `typeof` alone already excludes.
func CheckBigints(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	if target.Kind == annotations.DeclaredSet && target.KindTag == "bigint" {
		for _, v := range known.BigintValues {
			if !ctx.Kernel.Member(*target.Set, []float64{float64(v)}) {
				ctx.Report(assignability.At(
					node,
					7001,
					what+" of type '"+formatJSNumberLocal(float64(v))+"n' is not assignable to type "+
						"'"+StatedSetWords(*target.Set, target.Word)+"'",
				))
				return
			}
		}
		return
	}
	if target.Kind == annotations.DeclaredSet {
		say := "a bigint"
		spelledValues := formatValues(func() []float64 {
			out := make([]float64, len(known.BigintValues))
			for i, v := range known.BigintValues {
				out[i] = float64(v)
			}
			return out
		}())
		targetSaid := "a number"
		switch target.KindTag {
		case "boolean":
			targetSaid = "a boolean"
		case "symbol":
			targetSaid = "a symbol"
		}
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+spelledValues+"n' is "+say+", and the position states "+
				targetSaid+" — "+say+" is not allowed here",
		))
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// CheckSymbol is checkSymbol in the TS source: a symbol where the
// statement wears the symbol kindTag — the sort is the whole claim,
// and it matches. A scalar-worded target (no kindTag at all — the
// numeric/boolean line) never admits a symbol on any run, the same
// law CheckHostFunction already states for a function: a symbol is a
// definite rejection there, not an unproven position.
func CheckSymbol(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	if target.Kind == annotations.DeclaredSet && target.KindTag == "symbol" {
		return
	}
	if target.Kind == annotations.DeclaredSet && target.KindTag == "" {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type 'a symbol' is not assignable to type '"+
				StatedSetWords(*target.Set, target.Word)+"' — no refined set built from scalar words holds a symbol",
		))
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}

// CheckHostFunction is checkHostFunction in the TS source: a
// FUNCTION at a set-stated position is wrong on every run — no
// refined set holds a function object.
func CheckHostFunction(
	ctx *FlowContext,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
) {
	if target.Kind == annotations.DeclaredSet && target.KindTag == "" {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type 'a function' is not assignable to type "+
				"'"+StatedSetWords(*target.Set, target.Word)+"' — no refined set "+
				"holds a function",
		))
		return
	}
	ctx.Report(assignability.At(node, 7002, assignability.AlertText))
}
