// DIAGNOSIS pins (not fix pins) for the census's "for" rows — the shape
// the earlier diagnosis called "a call to a local callee after a
// continue" (ir_loop_stmts_transfer_pins_test.go's last case). Measured
// here: neither the continue nor the callee's locality is implicated.
//
// The mechanism, bracketed by the three cases below:
//
//   - a whole-array ARGUMENT (`otherUpdate(tree, target)`) is a use
//     usesAreAllArrayFormsFrom (ir_array_parameters.go) does not admit,
//     so the array parameter `tree` never flattens for the WHOLE body;
//   - every `tree.length` / `tree[i]` read then resolves no slot, so a
//     for whose initializer reads `len = tree.length` (or whose body
//     reads an element) fails its statement-bodied route and the whole
//     `for` takes the havoc floor — porous naming "for";
//   - with no such read anywhere (the bare counting loop), nothing
//     needed the flattening and the same call serves — the body
//     completes, ambient or local callee alike.
//
// The total-or-decline is load-bearing AS IT STANDS: admitting an
// argument use without more would let a served blob call leave the
// caller's len/elem slots untouched across a callee that writes the
// array (summaryCallStatement threads array entries IN but maps no
// array-entry exits back). The sound narrowing needs both halves:
// admit the argument position in usesAreAllArrayFormsFrom AND havoc (or
// thread back) the pair at every call statement the array rides into.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// arrayArgumentBody builds the probe body around one loop spelling.
func arrayArgumentDiagnosis(t *testing.T, loop string) (outcome SummaryOutcome, construct string) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function otherUpdate(tree: number[], cur: number): void {
			tree[0] = cur;
		}
		function f(tree: number[]): void {
			`+loop+`
		}
	`)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	callee := entryEnvFunctionNamed(t, p, "otherUpdate")
	symbol := p.Checker.GetSymbolAtLocation(callee.AsFunctionDeclaration().Name())
	if symbol == nil {
		t.Fatalf("no symbol for otherUpdate")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: callee}
	declaration := entryEnvFunctionNamed(t, p, "f")
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		t.Fatalf("the body declined whole — every variant here must at least lower")
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	return outcome, construct
}

func TestArrayArgumentDiagnosis_BareCountingLoopWithTheCallServes(t *testing.T) {
	outcome, construct := arrayArgumentDiagnosis(t, `
		for (let i = 0; i < 3; i++) {
			otherUpdate(tree, 1);
		}
	`)
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (%q) — the call must not refuse the body", construct)
	}
	t.Logf("bare counting loop + whole-array argument: outcome=%q construct=%q", outcome, construct)
}

func TestArrayArgumentDiagnosis_LengthReadPlusTheCallFloorsTheWholeFor(t *testing.T) {
	outcome, construct := arrayArgumentDiagnosis(t, `
		for (let i = 0, len = tree.length; i < len; i++) {
			otherUpdate(tree, 1);
		}
	`)
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (%q) — the floor must still stand in", construct)
	}
	// today: porous naming "for" — the argument use refused the
	// flattening, so `len = tree.length` failed the for's own route.
	// The narrowing (usesAreAllArrayFormsFrom + call-site pair havoc)
	// moves this to a per-statement answer; this log is the gauge.
	t.Logf("length-read loop + whole-array argument: outcome=%q construct=%q", outcome, construct)
}

func TestArrayArgumentDiagnosis_LengthReadWithoutTheCallServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f(tree: number[]): number {
			let s = 0;
			for (let i = 0, len = tree.length; i < len; i++) {
				s = s + 1;
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	if _, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		t.Fatalf("the call-free length-read loop declined whole")
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (%q)", construct)
	}
	t.Logf("length-read loop, no call: outcome=%q construct=%q", outcome, construct)
}
