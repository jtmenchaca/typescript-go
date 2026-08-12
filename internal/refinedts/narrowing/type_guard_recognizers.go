// The kernel seam the leaves pose their questions through, and the
// coverage-report composer that asks each leaf what it declined.
// Split from condition_analysis.ts per the v2 tree.
//
// BLOCKED: the TS source's setNarrowKernel also calls
// control_flow/kernel_delegation.ts's setEngineKernel, handing the same
// kernel instance to the engine route in one setup call. control_flow/
// is not ported yet (go-port-tracker.md: pending wave 3), so that
// handover is dropped here — SetNarrowKernel sets only this package's
// kernel. When control_flow ports, its own setup must call
// controlflow.SetEngineKernel alongside this, or this function gains
// the call back.
package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// NarrowKernel is narrowKernel in the TS source: the kernel the
// narrowings pose their questions through — set by the checker's run()
// before any walk. Without one (a unit test that skipped setup), a
// condition narrows nothing — degraded loudly by the alerts that
// causes, never wrong.
var NarrowKernel *kernelbridge.RefinedTSKernel

// SetNarrowKernel is setNarrowKernel in the TS source.
func SetNarrowKernel(kernel *kernelbridge.RefinedTSKernel) {
	NarrowKernel = kernel
}

// Other is OTHER in the TS source.
var Other = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindOther}

// GuardReadElsewhere mirrors "" | "relation" | "verdict" — the elsewhere
// a guard's read may have already happened.
type GuardReadElsewhere string

const (
	GuardReadNowhere  GuardReadElsewhere = ""
	GuardReadRelation GuardReadElsewhere = "relation"
	GuardReadVerdict  GuardReadElsewhere = "verdict"
)

// GuardReason is guardReason in the TS source: what to say about a
// guard that narrowed nothing, and the verdict — composing per-leaf
// decline helpers (finding 9): every sentence lives next to the
// recognizer that declined. Teaching a recognizer a new test means
// changing its decline in that same leaf file, so the coverage report
// can never describe a test the recognizers no longer decline.
//
// typeof and instanceof tests narrow the sort, which tsc already
// narrows — the guard determines nothing more at refinement level;
// everything else is unsupported.
func GuardReason(e *ast.Node, readElsewhere GuardReadElsewhere) (said string, unsupported bool) {
	// the walk COMPUTED the condition's verdict (an inlined call, a
	// known value): the guard was read, and the dead branch never
	// walks — nothing here went unread
	if readElsewhere == GuardReadVerdict {
		return "the condition computes its verdict — the branch it " +
			"refutes never walks", false
	}
	if said, unsupported, ok := StructuralReason(e); ok {
		return said, unsupported
	}
	if said, unsupported, ok := ArrayShapeReason(e); ok {
		return said, unsupported
	}
	if said, unsupported, ok := NumberTestReason(e); ok {
		return said, unsupported
	}
	if said, unsupported, ok := StringTestReason(e, readElsewhere); ok {
		return said, unsupported
	}
	if said, unsupported, ok := TypeofReason(e); ok {
		return said, unsupported
	}
	if said, unsupported, ok := InstanceofReason(e); ok {
		return said, unsupported
	}
	if said, unsupported, ok := ComparisonReason(e, readElsewhere); ok {
		return said, unsupported
	}
	if ast.IsCallExpression(e) {
		return "a call as a guard is not read — deciding it needs the " +
			"callee's body", true
	}
	if ast.IsBinaryExpression(e) {
		// tokenToString, because scanner.TokenToString answers alias
		// names for operator tokens (`<` reads FirstBinaryOperator) —
		// falling back to the raw Kind name when the scanner has no
		// text for it (an empty map lookup), mirroring the TS source's
		// `tokenToString(kind) ?? SyntaxKind[kind]`.
		op := e.AsBinaryExpression().OperatorToken.Kind
		text := scanner.TokenToString(op)
		if text == "" {
			text = op.String()
		}
		return "the walk has no reading for this test (" + text + ")", true
	}
	return "the walk has no reading for this test (" + e.Kind.String() + ")", true
}
