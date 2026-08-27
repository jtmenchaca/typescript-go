// from assignability/check_assignability.ts
//
// One checked position: what is known against what is stated. Every
// obligation in the program ends here — a call argument, a returned
// value, a write to a declared binding, a key of an object — and each
// one is a single question to the proved kernel, never entailment
// computed on this side.
//
// An object target is the PER-KEY subset check (TERMS.md term 10 —
// objects live in the graph, so assignability is key by key, which is
// exactly TypeScript's structural subtyping); a set target is one
// membership or subset question.
//
// The three outcomes are the vocabulary's: proved says nothing, a
// refutation reports 7001 with the counterexample spelled, and an
// undetermined verdict reports 7002 — the alert, which blocks.
//
// This file is the gate and the dispatcher: cast wrapping, unread
// refine, trust-level, unknown. Each arm lives in its own module.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// CheckAssignability is checkAssignability in the TS source.
// positionType is the position's DECLARED type where the caller
// knows it better than the node's contextual type (a property
// write's target) — nil for the TS default parameter.
func CheckAssignability(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) {
	// knowledge that crossed a cast still judges — and every refutation
	// it produces says so, in the value's own words
	judging := ctx
	if abstractdomain.TrustLevelOf(known) == abstractdomain.TrustAsserted {
		originalReport := ctx.Report
		withCastNote := *ctx
		withCastNote.Report = func(d assignability.RefinementDiagnostic) {
			if d.Code == 7001 {
				d.MessageText = d.MessageText + " — the cast doesn't change what the value can be"
			}
			originalReport(d)
		}
		judging = &withCastNote
	}
	if diagnose.EventOn("walk.assign") {
		reported := new(bool)
		judging = withAssignabilityDiagnosis(judging, known, target, what, reported)
		defer logAssignabilityVerdict(known, target, what, reported)
	}
	// THE JUDGE SEAM of the derivation trace (DERIVATION-TRACE.md,
	// "Threading: dispatchers, not readers"). Every obligation in the
	// program ends here, so this is where one judged position's trace
	// begins: the root span names the position and the whole derivation
	// nests under it. Off is one atomic load; on is per POSITION —
	// only the line -explain asked about opens a root.
	if derivation.Active() {
		if recorder := derivation.CurrentRecorder(); recorder != nil &&
			recorder.WantsLine(derivation.LineOf(node)) {
			root := derivation.BeginNode("checkAssignability", node)
			derivation.SetPosition(derivation.PositionOf(node))
			// the expression walk that produced this value already closed
			// its own traces before the judge opened — they are the judged
			// position's sub-reads by derivation, so the judge reclaims
			// every one whose range sits inside its own node
			derivation.AbsorbInto(derivation.Range(node))
			// and the guard that proved what this position carries hangs
			// beside them: an ANSWERED trace has to show the guard fact
			// MEETING the read, not just the set the read came out with
			for _, place := range PlacesReadIn(node) {
				derivation.AttachGuard(place)
			}
			// the judge answers unless something below declines: the 7002
			// paths call declineJudgment, which flips this to declined
			root.Answer(spellDeclared(&target))
			defer root.End()
		}
	}
	if !tracing.Recording(tracing.GrainStep) {
		CheckAssignabilityAgainst(judging, known, target, node, what, positionType)
		return
	}
	tracing.Span("checkAssignability", func() struct{} {
		CheckAssignabilityAgainst(judging, known, target, node, what, positionType)
		return struct{}{}
	}, tracing.GrainStep)
}

// withAssignabilityDiagnosis wraps ctx.Report so a refutation (7001)
// or alert (7002) at this one check logs its verdict and marks
// *reported — the site kind (`what`), the flowing value, and the
// declared target all logged alongside so a divergent run's first bad
// verdict names its position.
func withAssignabilityDiagnosis(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	what string,
	reported *bool,
) *FlowContext {
	knownSpelling := spellValue(known)
	targetSpelling := spellDeclared(&target)
	originalReport := ctx.Report
	withDiagnosis := *ctx
	withDiagnosis.Report = func(d assignability.RefinementDiagnostic) {
		*reported = true
		verdict := "undetermined"
		if d.Code == 7001 {
			verdict = "refused"
		}
		diagnose.Log("walk.assign",
			"site", what,
			"known", knownSpelling,
			"target", targetSpelling,
			"verdict", verdict,
		)
		originalReport(d)
	}
	return &withDiagnosis
}

// logAssignabilityVerdict logs the "admitted" line for a check that
// never reported — deferred in CheckAssignability so it runs after
// CheckAssignabilityAgainst actually finishes, unlike a line placed
// inside the wrapper above (which would fire before dispatch, not
// after).
func logAssignabilityVerdict(known abstractdomain.AbstractValue, target annotations.DeclaredRefinement, what string, reported *bool) {
	if *reported {
		return
	}
	diagnose.Log("walk.assign",
		"site", what,
		"known", spellValue(known),
		"target", spellDeclared(&target),
		"verdict", "admitted",
	)
}

// CheckAssignabilityAgainst is checkAssignabilityAgainst in the TS
// source.
func CheckAssignabilityAgainst(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) {
	CheckAssignabilityOfArm(ctx, known, target, node, what, positionType, false)
}

// CheckAssignabilityOfArm is checkAssignabilityAgainst carrying the
// one extra fact the union dispatcher holds: this known is ONE ARM of
// a wider union the value may take, so a refutation below is a
// possibility about the value rather than a verdict on it. No TS twin
// — the TS source has no arm channel, which is what lets one arm's
// definite sentence surface at a union position.
func CheckAssignabilityOfArm(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	target annotations.DeclaredRefinement,
	node *ast.Node,
	what string,
	positionType *checker.Type,
	oneArmOf bool,
) {
	tracing.Count("checkAssignability", 0)
	// an UNREAD refine rides the statement: the parse checks more
	// than the set says, so the position stays undetermined even
	// where the set alone would prove — refutations below still land.
	// An EXACT value can RUN the carried predicate, though: a decided
	// TRUE discharges the unread obstacle for this value (the set
	// still judges below), a decided FALSE refutes outright.
	if target.Kind == annotations.DeclaredSet && target.Unread {
		var decided bool
		hasDecided := false
		if target.Refine != nil {
			decided, hasDecided = RefineDecidedOnExact(target.Refine, known)
		}
		if hasDecided && !decided {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" fails the statement's own .refine predicate",
			))
			return
		}
		if !(hasDecided && decided) {
			ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		}
	}
	// the strictness dial: knowledge whose boundary the workspace
	// distrusts fires no judgment — the honest alert, never a verdict
	// built on an inadmissible derivation (a no-op at "full")
	if known.Kind != abstractdomain.KindUnknown && !abstractdomain.TrustLevelAdmitted(abstractdomain.TrustLevelOf(known)) {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText))
		return
	}
	if known.Kind == abstractdomain.KindUnknown {
		// a stated set that only RESTATES the sort's ground — the whole
		// scalar line, the star of every string, an array of either —
		// adds nothing beyond the host type, and tsc's own shape already
		// enforces that; unknown knowledge against it alerts nowhere
		// (the coverage report's "adds nothing" verdict, applied to the
		// judge). Dependent bounds still judge on their own row.
		if target.Kind == annotations.DeclaredSet && target.Temporal == nil &&
			!target.Unread && AddsNothingSet(*target.Set) {
			return
		}
		// a VARIABLE target whose bound is not grounded states no set of
		// its own — only a grounded bound is ever checked against
		// (declared_refinement.go's rule) — so the position's whole
		// content is tsc's shape check, and unknown knowledge against it
		// alerts nowhere: the same "adds nothing" verdict, for the bare
		// generic spellings (`T`, `ReadonlyArray<T>`, `ChartData` whose
		// element defaults to unknown)
		if target.Kind == annotations.DeclaredVariable && !target.Unread &&
			!target.BoundGrounded && target.BoundObject == nil {
			return
		}
		// the position's OWN static type already lies within the stated
		// set: tsc proved membership for every value this position can
		// hold, so the row is determined by the host's own check
		// (static_type_within.go's doc) — no alert
		if StaticTypeWithinTarget(ctx, node, target) {
			return
		}
		fix, hasFix := GuardFix(node, target)
		messageText := assignability.AlertText
		if known.ResidueReason != "" {
			messageText = known.ResidueReason
		}
		// THE DECLINE HELPER, adopted at the judge's generic undetermined
		// site — the highest-frequency 7002-family sentence in the tree.
		// The reader's own first-blocker sentence is the gate; the judged
		// node is the operand; what it held is the value the walk carried
		// here. When a trace is running the printed sentence IS the
		// projection of this span, so the two cannot drift; with no trace
		// the site prints exactly what it printed before.
		if projected := DeclineSentence(declineGateOf(known), node, spellUnknownHeld(known)); projected != "" {
			messageText = projected
		}
		if ContainsPow(node) {
			messageText = PowAlert(node, target)
		}
		base := assignability.At(node, 7002, messageText)
		if hasFix {
			base.Fix = &assignability.RefinementFix{Title: fix.Title, NewText: fix.NewText, InsertAt: fix.InsertAt}
		}
		ctx.Report(base)
		return
	}

	if known.Kind == abstractdomain.KindNaN {
		CheckPinnedNan(ctx, target, node, what)
		return
	}
	if target.Kind == annotations.DeclaredObjectArray {
		CheckObjectArrayTarget(ctx, known, target, node, what)
		return
	}
	// a TUPLE target judges the ARITY and then each slot at its own
	// statement — placed here, beside the object-array arm, because both
	// are sequence-shaped targets whose own count is part of the claim
	// and neither is answerable by the scalar membership road below.
	if target.Kind == annotations.DeclaredTuple {
		CheckTupleTarget(ctx, known, target, node, what)
		return
	}
	if target.Kind == annotations.DeclaredPossiblyUndefined {
		CheckMaybeTarget(ctx, known, target, node, what, positionType)
		return
	}
	if known.Kind == abstractdomain.KindUndef || known.Kind == abstractdomain.KindNull ||
		known.Kind == abstractdomain.KindPossiblyUndefined {
		// KindNull rides the same refutation: exactly-null is a member
		// of no refined set either (JSON.parse("null")'s own value at a
		// scalar-declared sink — A2.edge.json's NaN-crosses-as-null arm).
		RefutePossiblyAbsent(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindKindUnion {
		CheckKindUnion(ctx, known, target, node, what, positionType)
		return
	}
	if target.Kind == annotations.DeclaredVariable {
		CheckVariableTarget(ctx, known, target, node)
		return
	}
	if known.Kind == abstractdomain.KindVariable {
		CheckVariableKnown(ctx, known, target, node, what, positionType)
		return
	}
	if target.Kind == annotations.DeclaredObject {
		CheckObjectTarget(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindObject {
		CheckObjectKnown(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindPossiblyNaN {
		CheckPossiblyNaN(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindBigints {
		CheckBigints(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindSymbol {
		CheckSymbol(ctx, known, target, node, what)
		return
	}
	if known.Kind == abstractdomain.KindHostFunction {
		CheckHostFunction(ctx, target, node, what)
		return
	}
	if CheckListOrStructured(ctx, known, target, node, what) {
		return
	}
	if CheckAdmittedSort(ctx, known, node, what, positionType) {
		return
	}
	checkSetMembershipOfArm(ctx, known, target, node, what, oneArmOf)
}
