// The static-member chains behind e-class-and-function.ts's
// staticPrivateFieldAndMethod and staticMethodAndAccessors rows: a
// static private field read through static methods, and a static
// get/set accessor pair over a static backing field.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// exactly200Or40 asserts an exact single-value answer in either exact
// spelling (the accessor e2e pin's rule).
func assertExactValue(t *testing.T, returned abstractdomain.AbstractValue, want float64) {
	t.Helper()
	exact := false
	switch returned.Kind {
	case abstractdomain.KindValues:
		exact = len(returned.Values) == 1 && returned.Values[0] == want
	case abstractdomain.KindSet:
		exact = len(returned.Set.Forms) == 1 &&
			returned.Set.Forms[0].Form == refinementsets.FormOneOf &&
			len(returned.Set.Forms[0].W) == 1 && returned.Set.Forms[0].W[0] == want
	}
	if !exact {
		t.Fatalf("determined %+v, want exactly %v", returned, want)
	}
}

func staticChainReturn(t *testing.T, source string) abstractdomain.AbstractValue {
	t.Helper()
	kernel := yieldContractKernel(t)
	contract, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)
	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	return JoinSinkSummarized(sink)
}

// the DIRECT static private field read — ReadStaticFieldAccess's own
// invariant answer, no method hop
func TestStaticMemberChain_ADirectStaticPrivateFieldReadAnswersItsInvariant(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static #ceiling = 40;\n"+
			"  static read(): number { return Box.#ceiling; }\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return Box.read();\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// the same two-hop chain with a PUBLIC inner method — separates the
// depth question from the private-name question
func TestStaticMemberChain_AStaticFieldThroughTwoPublicStaticMethodsCarries(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static #ceiling = 40;\n"+
			"  static readInner(): number { return Box.#ceiling; }\n"+
			"  static years(): number { return Box.readInner(); }\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return Box.years();\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// two hops where the INNER static method returns a CONSTANT — no field
// read at all; isolates depth-2 static-method chaining itself
func TestStaticMemberChain_TwoStaticHopsOverAConstantCarry(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static readInner(): number { return 40; }\n"+
			"  static years(): number { return Box.readInner(); }\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return Box.years();\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// two hops through FREE functions with the static field read at the
// bottom — isolates the static-METHOD spelling from the depth
func TestStaticMemberChain_TwoFreeFunctionHopsOverAStaticFieldCarry(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static #ceiling = 40;\n"+
			"  static read(): number { return Box.#ceiling; }\n"+
			"}\n"+
			"function inner(): number { return Box.read(); }\n"+
			"function outer(): number { return inner(); }\n"+
			"function caller(): number {\n"+
			"  return outer();\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// e-435's exact shape: the field read sits TWO static-method hops deep
func TestStaticMemberChain_AStaticPrivateFieldThroughTwoStaticMethodsCarries(t *testing.T) {
	returned := staticChainReturn(t,
		"class StaticPrivateAge {\n"+
			"  static #ceiling = 40;\n"+
			"  static #read(): number {\n"+
			"    return StaticPrivateAge.#ceiling;\n"+
			"  }\n"+
			"  static years(): number {\n"+
			"    return StaticPrivateAge.#read();\n"+
			"  }\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return StaticPrivateAge.years();\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// a PUBLIC static field with no outside write keeps its invariant
func TestStaticMemberChain_AnUntouchedPublicStaticFieldAnswersItsInvariant(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static total = 40;\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return Box.total;\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}

// a PUBLIC static field written from MODULE text: the flow's own value
// answers where the write is in view…
func TestStaticMemberChain_AnOutsideWrittenPublicStaticReadsTheFlowValue(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static total = 40;\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  Box.total = 200;\n"+
			"  return Box.total;\n"+
			"}\n")
	assertExactValue(t, returned, 200)
}

// …and where it is NOT in view, the invariant must not stand in — 40
// would be a stale claim about a field module text moves
func TestStaticMemberChain_AnOutsideWrittenPublicStaticKeepsNoInvariant(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static total = 40;\n"+
			"}\n"+
			"function bump(): void {\n"+
			"  Box.total = 200;\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  return Box.total;\n"+
			"}\n")
	if returned.Kind == abstractdomain.KindValues || returned.Kind == abstractdomain.KindSet {
		words, _ := abstractdomain.FormatAbstractValue(returned)
		if words == "40" || words == "{40}" {
			t.Fatalf("an outside-written public static answered its initializer %q — the stale invariant survived", words)
		}
	}
}

// the write carried through a HELPER hop: the callee's static write
// rides the inline write-back into the caller's world
func TestStaticMemberChain_AHelperHopStaticWriteCarriesToTheCaller(t *testing.T) {
	returned := staticChainReturn(t,
		"class Box {\n"+
			"  static total = 40;\n"+
			"}\n"+
			"function bump(): void {\n"+
			"  Box.total = 200;\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  bump();\n"+
			"  return Box.total;\n"+
			"}\n")
	assertExactValue(t, returned, 200)
}

// e-497's exact shape: a static setter write then a static getter read
// — the write's exact value must be what the read answers
func TestStaticMemberChain_AStaticSetterWriteThenGetterReadCarriesTheValue(t *testing.T) {
	returned := staticChainReturn(t,
		"class StaticAccessAge {\n"+
			"  static #held = 0;\n"+
			"  static get ceiling(): number {\n"+
			"    return StaticAccessAge.#held;\n"+
			"  }\n"+
			"  static set ceiling(value: number) {\n"+
			"    StaticAccessAge.#held = value;\n"+
			"  }\n"+
			"}\n"+
			"function caller(): number {\n"+
			"  StaticAccessAge.ceiling = 40;\n"+
			"  return StaticAccessAge.ceiling;\n"+
			"}\n")
	assertExactValue(t, returned, 40)
}
