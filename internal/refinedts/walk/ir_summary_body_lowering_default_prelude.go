// split from ir_summary_body_lowering.go — the default prelude

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// summaryDefaultPrelude builds THE DEFAULT PRELUDE: each defaulted
// parameter applies its default exactly where the runtime does — only
// when the call left the entry undefined. The branch tests the slot's
// definedness (the kernel's IrTest.defined, covered by walk_sound), and
// the else arm assigns the default; a supplied argument walks the empty
// then arm untouched.
//
// The default's VALUE is read where the effect grammar can spell it
// (`= 0`, `= null`, `= other`), and is UNKNOWN where it cannot —
// `= new ApplicationConfig()`, `= createContextId()`,
// `= this.container.getModules()`. Unknown is exactly the opaque
// call's own admission: the slot's value is unconstrained and the
// branch structure around it is still the runtime's own, so a
// supplied argument keeps everything the entry state promised and
// only the defaulted run loses the value.
//
// A default that RUNS code — a `new`, a call, an await — also runs
// whatever a stored closure of this body can run, so it brackets the
// capture-havoc set exactly as a code-running statement in the body
// does (lowering_to_kernel_ir.go's bracketing). The bracket goes
// OUTSIDE the branch: it must hold on both arms, because the caller
// chooses which arm runs and neither may be believed across the
// initializer's code. The whole prelude runs before any statement, so
// the statement walk's own leading bracket is not enough — nothing
// has yet forced those slots to forget.
func summaryDefaultPrelude(
	context *LoweringContext,
	defaultedSlots []defaultedParameterSlot,
) ([]kernelbridge.IrStatement, map[int]kernelbridge.LoopEffect) {
	captureHavocPrelude := map[int]struct{}{}
	for _, slot := range context.CaptureHavocSlots {
		if slot >= 0 {
			captureHavocPrelude[slot] = struct{}{}
		}
	}
	var prelude []kernelbridge.IrStatement
	defaultEffects := map[int]kernelbridge.LoopEffect{}
	for _, defaulted := range defaultedSlots {
		effect, lowered := RhsEffect(context, context.Sorts[defaulted.Slot], defaulted.Initializer)
		// A DEFAULT THAT IS A CALL — `= createContextId()`,
		// `= this.container.getModules()` — is SERVED where the callee has
		// a summary, instead of taking unknown.
		//
		// The earlier refusal said the prelude has no statement stream, and
		// that predates the branch-shaped prelude: the else arm below IS a
		// statement list, which is exactly the position SummaryCallOrHavoc
		// needs, and it writes the call's value into the parameter's own
		// slot. The summary TABLE is live too — `table` and `context` are
		// built before this loop, and SummaryBlobFor builds a callee's blob
		// on demand — so a callee's blob is reachable here on the same
		// terms it is reachable from any body statement.
		//
		// The call goes in the ELSE ARM alone, which is where the runtime
		// runs it: a supplied argument never evaluates the default, so
		// putting the call on the then arm would run code the real run does
		// not. The bracketing around the branch is unchanged and still
		// required — the call runs code, so a stored closure of this body
		// may run inside it.
		var defaultCall []kernelbridge.IrStatement
		if !lowered && context.SummaryTable != nil {
			if head := Unwrapped(defaulted.Initializer); head != nil &&
				(ast.IsCallExpression(head) || ast.IsNewExpression(head)) {
				// SummaryCallOrHavoc's own OPAQUE tier would name the havoc
				// after the CALLEE ("call make") — the right name for a body
				// statement, but this call sits in the default's own
				// prelude, ahead of every body statement, so the door should
				// read "a defaulted parameter" instead: the porous cause is
				// the default running code, not the callee's own name.
				// SummaryCallOrHavocNamed says that AT THE CALL, first-wins
				// through NoteFirstHavoc exactly as the derived name would,
				// so there is nothing to snapshot or patch afterward.
				if served, servedOk := SummaryCallOrHavocNamed(context, head, defaulted.Slot, "a defaulted parameter"); servedOk {
					defaultCall = served
				}
			}
		}
		if lowered {
			defaultEffects[defaulted.Slot] = effect
		} else {
			// the default is a construct the effect grammar cannot spell.
			// The slot takes unknown on the arm the default runs on; no
			// value is claimed, so nothing said here is wrong. A SERVED
			// call replaces that unknown outright — the statements it built
			// write the slot themselves.
			effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
		}
		runsCode := StatementRunsCode(defaulted.Initializer)
		if runsCode && len(captureHavocPrelude) > 0 {
			prelude = append(prelude, havocAssignments(captureHavocPrelude)...)
		}
		// A DEFAULT HOLDING A CLOSURE — `cb = () => { this.count++ }` —
		// hands the arrow to whoever the parameter goes on to, and calling
		// it writes this body's names. StatementRunsCode does not see it
		// (building an arrow runs nothing), and this prelude admits every
		// default rather than declining, so the havoc floor's walk into the
		// arrow never happens for it. The names go unknown here instead —
		// inside the same else arm, since only the run that took the default
		// built the closure. ClosureEscapesTrackedWrite states the boundary
		// rule this shares with the census.
		defaultArm := []kernelbridge.IrStatement{{
			Kind:   kernelbridge.IrStatementAssign,
			Target: defaulted.Slot,
			Effect: effect,
		}}
		if len(defaultCall) > 0 {
			// the served call's own statements write the slot; the unknown
			// assign above would only overwrite what the call answered
			defaultArm = defaultCall
		}
		if ClosureEscapesTrackedWrite(context, defaulted.Initializer) {
			if written, enumerable := havocSlotsOfStatement(context, defaulted.Initializer); enumerable {
				defaultArm = append(defaultArm, havocAssignments(written)...)
			}
		}
		prelude = append(prelude, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   defaulted.Slot,
			Test: kernelbridge.IrTestDefined,
			Else: defaultArm,
		})
		if runsCode && len(captureHavocPrelude) > 0 {
			prelude = append(prelude, havocAssignments(captureHavocPrelude)...)
		}
	}
	return prelude, defaultEffects
}
