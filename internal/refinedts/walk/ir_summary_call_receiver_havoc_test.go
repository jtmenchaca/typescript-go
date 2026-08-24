// Pins the receiver-based STATEMENT-POSITION call family the census
// names: a call whose CALLEE is a property access — `document.getElementById(...)`,
// `window.addEventListener(...)`, a parameter's own method
// (`listenerApi.getState()`), `Children.forEach(...)` — none of which
// importedHookCallOf (ir_summary_imported_hook_calls.go) recognizes,
// since that recognizer's gate is a BARE identifier callee. Before this
// file's arm, every one of these fell straight to the opaque call
// floor — sound, but the floor's own decline-to-havoc question is
// answered structurally (SummaryCallOrHavocNamed's own doc), so the
// gap here was never soundness, only that these sites were never TRIED
// against the write-and-call-free proof at all before falling through.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestReceiverCalleeCallStatement_DocumentGetElementByIdCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		export function f(): number {
			let measurementSpan = document.getElementById("x");
			if (!measurementSpan) {
				return 0;
			}
			return 1;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — document.getElementById's argument is a string literal, write-and-call-free", outcome, construct, ok)
	}
}

func TestReceiverCalleeCallStatement_WindowAddEventListenerCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		export function f(): number {
			window.addEventListener("resize", () => {});
			return 1;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the listener closure writes no tracked slot", outcome, construct, ok)
	}
}

func TestReceiverCalleeCallStatement_ParameterMethodCallCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// state.count is read inside an IF condition rather than a bare
	// `return state.count`: a member read in RETURN position takes its
	// own separate route (lowering_to_kernel_ir_return.go, out of this
	// file's scope) whose own gap is pre-existing and unrelated to
	// whether the CALL statement itself served. This pin isolates the
	// call statement this file's recognizer owns.
	p := entryEnvTestProgram(t, `
		interface ListenerApi { getState(): { count: number } }
		export function f(listenerApi: ListenerApi): number {
			const state = listenerApi.getState();
			if (state.count > 0) {
				return 1;
			}
			return 0;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — getState takes no arguments to prove free over", outcome, construct, ok)
	}
}

func TestReceiverCalleeCallStatement_ChildrenForEachCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		import { Children, ReactNode } from "react";
		export function f(children: ReactNode): number {
			let count = 0;
			Children.forEach(children, () => { count = count + 1; });
			return count;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	// count is havocked (the callback writes it, not proven free) — the
	// body must NOT claim complete while pretending count kept its value
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want NOT complete — the callback writes `count`, which must be havocked", outcome, construct, ok)
	}
}

// TestReceiverCalleeCallStatement_AWritingArgumentDeclines pins the
// negative: a receiver-callee call whose argument WRITES a tracked slot
// must still havoc, not silently drop the write.
func TestReceiverCalleeCallStatement_AWritingArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		export function f(): number {
			let total = 0;
			window.addEventListener("resize", () => { total = total + 1; });
			return total;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want NOT complete — the listener writes `total`, which must be havocked, not silently preserved", outcome, construct, ok)
	}
}
