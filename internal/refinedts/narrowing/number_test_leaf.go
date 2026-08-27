// Number.isNaN / isFinite / isInteger / isSafeInteger, and the
// global isNaN over a number-typed argument.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// NumberTestLeaf is numberTestLeaf in the TS source: `Number.isInteger(x)`
// / `isFinite` / `isNaN` on this place — the global Number, resolved to
// the default library, never matched by name alone.
func NumberTestLeaf(c *checker.Checker, e *ast.Node, place dataflowfacts.TrackedPlace, isTracked func(name string) bool) kernelbridge.NarrowTree {
	call := e.AsCallExpression()
	callee := call.Expression
	// the GLOBAL isNaN — the spelling date code actually writes
	// (`if (isNaN(x)) return NaN`). It coerces first (sec-isnan), but
	// ToNumber of a number IS the number, so over a number-typed
	// argument the test reads exactly as Number.isNaN.
	if ast.IsIdentifier(callee) && callee.Text() == "isNaN" && resolvesToDefaultLib(c, callee) {
		if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
			return Other
		}
		argument := call.Arguments.Nodes[0]
		tracing.CountBy("host.typeAtLocation.direct", 1)
		argumentType := c.GetTypeAtLocation(argument)
		if (argumentType.Flags() & checker.TypeFlagsNumberLike) == 0 {
			return Other
		}
		tested := dataflowfacts.TrackedPlaceOfWith(c, argument, isTracked)
		if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
			return Other
		}
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsNaN}
	}
	if !ast.IsPropertyAccessExpression(callee) {
		return Other
	}
	propAccess := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(propAccess.Expression) {
		return Other
	}
	test := propAccess.Name().Text()
	if test != "isInteger" && test != "isFinite" && test != "isNaN" && test != "isSafeInteger" {
		return Other
	}
	if !resolvesToDefaultLib(c, propAccess.Expression) {
		return Other
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return Other
	}
	argument := call.Arguments.Nodes[0]
	tested := dataflowfacts.TrackedPlaceOfWith(c, argument, isTracked)
	if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
		return Other
	}
	if test == "isInteger" {
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsInt}
	}
	if test == "isFinite" {
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsFinite}
	}
	if test == "isSafeInteger" {
		// Number.isSafeInteger (sec-number.issafeinteger): an integral
		// Number within ±(2^53 − 1) — exactly the conjunction of the
		// integer test and the two bounds, so it lowers as that AND and
		// the kernel narrows it with the proved connective semantics.
		const maxSafeInteger = 9007199254740991
		isInt := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsInt}
		ge := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpGe, K: -maxSafeInteger}
		le := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpLe, K: maxSafeInteger}
		bounds := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindAnd, A: &ge, B: &le}
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindAnd, A: &isInt, B: &bounds}
	}
	return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsNaN}
}

// NumberTestReason is numberTestReason in the TS source: decline
// sentences for Number predicates and global isNaN (finding 9). Same
// coverage-report text the call fallback has always used.
func NumberTestReason(e *ast.Node) (said string, unsupported bool, ok bool) {
	if !ast.IsCallExpression(e) {
		return "", false, false
	}
	callee := e.AsCallExpression().Expression
	isGlobalIsNaN := ast.IsIdentifier(callee) && callee.Text() == "isNaN"
	isNumberPredicate := false
	if ast.IsPropertyAccessExpression(callee) {
		test := callee.AsPropertyAccessExpression().Name().Text()
		isNumberPredicate = test == "isInteger" || test == "isFinite" || test == "isNaN" || test == "isSafeInteger"
	}
	if !isGlobalIsNaN && !isNumberPredicate {
		return "", false, false
	}
	return "a call as a guard is not read — deciding it needs the " +
		"callee's body", true, true
}
