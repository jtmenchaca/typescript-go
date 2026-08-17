// Pins for the switch family's sound-weaker lowering
// (lowering_to_kernel_ir_switch.go, lowering_to_kernel_ir_switch_labels.go):
// a non-literal case label, an untracked discriminant, and a boolean
// label each still lower the body — the branch-both/hoisted-testable
// routes documented in LowerSwitch's own header, exercised here against
// the exact shapes the recharts census names ("switch on a case label
// that is not a literal" x5, "switch on an untracked discriminant" x1).
//
// Every pin here already holds against the current tree — recorded as
// pins rather than a probe because the census's own construct rows for
// this family have no reproducible SummaryDeclined shape: every
// specimen this file tries (a const-object member label, a let-bound
// non-literal label, a call discriminant) already lowers via the
// branch-both join and settles porous, not declined.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestSwitchPins_ConstObjectMemberLabelLowersViaBranchBoth pins a case
// label built from a plain `const` object's member — not an enum, so
// switchLabelLiteral's enum-member route does not fire and the label
// is genuinely non-literal to this reading. The switch still lowers:
// every arm's statements ride the branch-both join LowerSwitch builds
// when hoistedTestable is false, and the body settles porous naming
// "switch" — not declined.
func TestSwitchPins_ConstObjectMemberLabelLowersViaBranchBoth(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const ANIMATION_KEYS = { linear: 'linear', ease: 'ease' } as const;
		function f(k: string): number {
			let s = 0;
			switch (k) {
				case ANIMATION_KEYS.linear:
					s = 1;
					break;
				case ANIMATION_KEYS.ease:
					s = 2;
					break;
				default:
					s = 3;
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a const-object-member case label declined the whole switch at %q — "+
			"LowerSwitch's branch-both fallback should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — a non-literal label must still join, not refuse the body", construct)
	}
}

// TestSwitchPins_LetBoundNonLiteralLabelLowersViaBranchBoth is
// q-decline-names.ts's switchOnNonLiteralLabel shape: `case bound:`
// where bound is a plain parameter. ConstChainLiteral declines it (a
// parameter is not a const), so the label is genuinely non-literal,
// and the switch still lowers through the branch-both join.
func TestSwitchPins_LetBoundNonLiteralLabelLowersViaBranchBoth(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f(k: string, bound: string): number {
			let s = 0;
			switch (k) {
				case bound:
					s = 1;
					break;
				default:
					s = 2;
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a parameter-valued case label declined the whole switch at %q — "+
			"LowerSwitch's branch-both fallback should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — a non-literal label must still join, not refuse the body", construct)
	}
}

// TestSwitchPins_CallDiscriminantLowersViaHoistedBranchBoth is
// q-decline-names.ts's switchOnUntrackedDiscriminant shape:
// `switch (unreadNumber())`. The discriminant hoists to a temp
// (ConditionTestSlot) and, wearing no number/string sort, keeps the
// untested branch-both chain — every arm's effects ride, no claim
// about which label matched.
func TestSwitchPins_CallDiscriminantLowersViaHoistedBranchBoth(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare function unreadNumber(): number;
		function f(): number {
			let s = 0;
			switch (unreadNumber()) {
				case 1:
					s = 1;
					break;
				default:
					s = 2;
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("an untracked call discriminant declined the whole switch at %q — "+
			"the hoisted branch-both fallback should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — an untracked discriminant must still join, not refuse the body", construct)
	}
}

// TestSwitchPins_BooleanLabelLowersComplete pins `case true:`/`case
// false:` against a boolean-typed discriminant: switchLabelLiteral's
// boolean arm (KindTrueKeyword/KindFalseKeyword) plus the typeof-
// boolean gate on the discriminant give both labels a wire equality,
// so the switch is COMPLETE — every construct served, nothing
// havocked.
func TestSwitchPins_BooleanLabelLowersComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f(flag: boolean): number {
			let s = 0;
			switch (flag) {
				case true:
					s = 1;
					break;
				case false:
					s = 2;
					break;
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a boolean case label declined the switch at %q — switchLabelLiteral's boolean arm should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — both boolean labels wear a wire equality under the typeof gate", outcome, construct)
	}
}
