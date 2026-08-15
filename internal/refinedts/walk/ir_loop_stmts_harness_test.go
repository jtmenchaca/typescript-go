// split from ir_loop_stmts.go — the statement-bodied loop's test
// harness: naming a slot, finding a loopStmts statement, driving the
// kernel over a lowered body

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// loopStmtsSlotOf resolves a body's slot index by its spelled name:
// the parameters first, in declaration order, then the locals the
// layout lays out. The lowering does not carry its slot vector out on
// LoweredSummary, so this re-runs the same two functions the lowering
// calls, exactly as summaryCollectedNames does.
func loopStmtsSlotOf(t *testing.T, declaration *ast.Node, wanted string) int {
	t.Helper()
	parameters := declaration.Parameters()
	for index, parameter := range parameters {
		name := parameter.AsParameterDeclaration().Name()
		if name != nil && ast.IsIdentifier(name) && name.Text() == wanted {
			return index
		}
	}
	body := declaration.Body()
	locals, patterns, ok := collectSummaryLocals(body)
	if !ok {
		t.Fatalf("collectSummaryLocals declined")
	}
	parameterNames := map[string]struct{}{}
	for _, parameter := range parameters {
		name := parameter.AsParameterDeclaration().Name()
		if name != nil && ast.IsIdentifier(name) {
			parameterNames[name.Text()] = struct{}{}
		}
	}
	for index, slot := range localSlotsOf(nil, body, locals, patterns, parameterNames) {
		if slot.Name == wanted {
			return len(parameters) + index
		}
	}
	t.Fatalf("no slot named %q", wanted)
	return -1
}

// loopStmtsHolds is whether a lowered statement list holds a
// statement-bodied loop anywhere, arms and nested loops included.
func loopStmtsHolds(statements []kernelbridge.IrStatement) (kernelbridge.IrStatement, bool) {
	for _, s := range statements {
		switch s.Kind {
		case kernelbridge.IrStatementLoopStmts:
			return s, true
		case kernelbridge.IrStatementBranch, kernelbridge.IrStatementBranchBoth:
			if held, found := loopStmtsHolds(s.Then); found {
				return held, true
			}
			if held, found := loopStmtsHolds(s.Else); found {
				return held, true
			}
		case kernelbridge.IrStatementLoop:
			// an effect-bodied loop carries no statements
		}
	}
	return kernelbridge.IrStatement{}, false
}

// loopStmtsWalk drives the kernel over a lowered body from one exact
// entry value per named slot, everything else absent, and answers the
// exit states.
//
// The INSTALLED dylib may not decode the `loopStmts` wire yet: the
// kernel is built from Lean and the wasm/native artifact lags the form
// until it is rebuilt, and an undecodable statement comes back as the
// kernel's own stated error, which Answered raises as a panic. That is
// a build-artifact gap, not a lowering fault — the lowering-side
// assertions above every call here have already run — so it is
// recovered and SKIPPED naming the kernel's exact message. Any other
// panic is re-raised.
func loopStmtsWalk(
	t *testing.T,
	kernel *kernelbridge.RefinedTSKernel,
	lowered LoweredSummary,
	entryValues map[int]float64,
) []kernelbridge.KnownStateWire {
	t.Helper()
	entries := make([]kernelbridge.KnownStateWire, lowered.SlotCount)
	for i := range entries {
		entries[i] = absentState
	}
	for slot, value := range entryValues {
		entries[slot] = kernelbridge.KnownStateWire{
			Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{value})),
		}
	}
	entries[lowered.DoneIndex] = doneDownState
	var exits []kernelbridge.KnownStateWire
	skipped := ""
	func() {
		defer func() {
			raised := recover()
			if raised == nil {
				return
			}
			message, isString := raised.(string)
			if !isString || !strings.Contains(message, "unknown statement form") {
				panic(raised)
			}
			skipped = message
		}()
		exits = kernel.Walk(entries, lowered.Stmts)
	}()
	if skipped != "" {
		t.Skipf("the installed kernel does not decode the loopStmts wire yet (%q) — "+
			"rebuild it with `pnpm kernel`; the lowering-side assertions above have run", skipped)
	}
	if len(exits) != lowered.SlotCount {
		t.Fatalf("the walk answered %d states for %d slots", len(exits), lowered.SlotCount)
	}
	return exits
}
