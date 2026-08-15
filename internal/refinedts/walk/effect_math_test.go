// The Math floor-row closures on the loop-effect wire: Math.min/max at
// 0, 1, and 2+ arguments, and the eight Math value-property constants
// (E, LN2, LN10, LOG2E, LOG10E, PI, SQRT1_2, SQRT2). Every other Math
// unary/binary named in SYNTAX-COVERAGE.md's Math section stays floored
// here (see the comment block above mathOps in effect_expression.go):
// the loop-effect wire's LoopOp1/LoopOp2 vocabulary is a closed,
// kernel-proved enum with no constructor for asin, acos, sign, random,
// fround, cbrt, asinh, atanh, acosh, sinh, cosh, tanh, exp, expm1, log,
// log2, log10, log1p, hypot, clz32, or imul — those are already sound
// on the SEPARATE general-evaluator transfer wire (math_transfer.go,
// math_unary_transfer.go), which this file's grammar cannot delegate to
// mid-lowering.
package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// mathSummaryOf runs the whole summary route on a source's first
// declaration and answers the served value, exactly like
// booleanSummaryOf in effect_boolean_test.go.
func mathSummaryOf(t *testing.T, source string, args []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, source)
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	return KernelSummaryDirect(ctx, args, contract)
}

func TestMathEffect_MinWithTwoArgsFoldsAsBefore(t *testing.T) {
	// f(2, 5) = 2 at runtime; the two-arg fold must admit 2
	answer, ok := mathSummaryOf(t,
		"function f(a: number, b: number) { return Math.min(a, b); }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2), exactNumber(t, 5)})
	if !ok {
		t.Fatalf("Math.min(a, b) declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("Math.min(2, 5) excludes the true value 2: %+v", state.Set)
	}
}

func TestMathEffect_MaxWithTwoArgsFoldsAsBefore(t *testing.T) {
	// f(2, 5) = 5 at runtime; the two-arg fold must admit 5
	answer, ok := mathSummaryOf(t,
		"function f(a: number, b: number) { return Math.max(a, b); }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2), exactNumber(t, 5)})
	if !ok {
		t.Fatalf("Math.max(a, b) declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{5}) {
		t.Errorf("Math.max(2, 5) excludes the true value 5: %+v", state.Set)
	}
}

func TestMathEffect_MinWithOneArgIsToNumberOfTheArg(t *testing.T) {
	// Math.min(x) with exactly one argument answers ToNumber(x) directly
	// (sec-math.min: the fold's own first step, never a distinct claim)
	answer, ok := mathSummaryOf(t,
		"function f(a: number) { return Math.min(a); }",
		[]abstractdomain.AbstractValue{exactNumber(t, 7)})
	if !ok {
		t.Fatalf("Math.min(a) declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{7}) {
		t.Errorf("Math.min(7) excludes 7: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{8}) {
		t.Errorf("Math.min(7) admits 8: %+v", state.Set)
	}
}

func TestMathEffect_MaxWithOneArgIsToNumberOfTheArg(t *testing.T) {
	answer, ok := mathSummaryOf(t,
		"function f(a: number) { return Math.max(a); }",
		[]abstractdomain.AbstractValue{exactNumber(t, 7)})
	if !ok {
		t.Fatalf("Math.max(a) declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{7}) {
		t.Errorf("Math.max(7) excludes 7: %+v", state.Set)
	}
}

func TestMathEffect_MinWithZeroArgsIsPositiveInfinity(t *testing.T) {
	// sec-math.min step 3: the fold identity is +Infinity, and the loop
	// body never runs over zero coerced arguments
	answer, ok := mathSummaryOf(t,
		"function f() { return Math.min(); }",
		nil)
	if !ok {
		t.Fatalf("Math.min() declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{math.Inf(1)}) {
		t.Errorf("Math.min() excludes +Infinity: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{1e300}) {
		t.Errorf("Math.min() admits a large finite value: %+v", state.Set)
	}
}

func TestMathEffect_MaxWithZeroArgsIsNegativeInfinity(t *testing.T) {
	// sec-math.max step 3: the fold identity is -Infinity
	answer, ok := mathSummaryOf(t,
		"function f() { return Math.max(); }",
		nil)
	if !ok {
		t.Fatalf("Math.max() declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{math.Inf(-1)}) {
		t.Errorf("Math.max() excludes -Infinity: %+v", state.Set)
	}
}

// mathConstantCases: every Math value property and the exact Go
// constant its clause names, so one loop drives all eight.
var mathConstantCases = []struct {
	name  string
	value float64
}{
	{"E", math.E},
	{"LN10", math.Ln10},
	{"LN2", math.Ln2},
	{"LOG10E", math.Log10E},
	{"LOG2E", math.Log2E},
	{"PI", math.Pi},
	{"SQRT1_2", math.Sqrt(0.5)},
	{"SQRT2", math.Sqrt2},
}

func TestMathEffect_ValuePropertiesServeTheirExactConstant(t *testing.T) {
	for _, tc := range mathConstantCases {
		t.Run(tc.name, func(t *testing.T) {
			answer, ok := mathSummaryOf(t,
				"function f() { return Math."+tc.name+"; }",
				nil)
			if !ok {
				t.Fatalf("Math.%s declined the summary route", tc.name)
			}
			kernel := kernelDelegationLoadKernel(t)
			state, stateOk := StateOfKnown(answer)
			if !stateOk || state.Top {
				t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
			}
			if !kernel.Member(state.Set, []float64{tc.value}) {
				t.Errorf("Math.%s excludes its own constant %v: %+v", tc.name, tc.value, state.Set)
			}
			if kernel.Member(state.Set, []float64{tc.value + 1}) {
				t.Errorf("Math.%s admits a value one away from its constant: %+v", tc.name, state.Set)
			}
		})
	}
}
