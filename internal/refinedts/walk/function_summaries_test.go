// Pins the `this`-read gap in scanBody's effect summary
// (AGENT-BRIEF.md's syntax-wave facts, the propertyPrivate /
// privateFieldSlotLaidOut / privateFieldThroughConstructor /
// privateAccessorSlotLaidOut rows): a method whose body reads `this`
// (a private field, an accessor, any receiver-held state) was marked
// SelfContained — "a function of its arguments alone" — because
// scanBody's identifier scan only ever tested ast.IsIdentifier, and
// `this` parses as ast.KindThisKeyword, a different node kind
// entirely.
//
// SelfContained routes a call through RecoverPure
// (evaluate_call_expression.go's EffectFree branch), and
// recoverPureBody (function_summaries.go) calls SummaryResultIn, which
// always fills a method's this-entries from unknownReceiver()
// (kernel_summaries.go) — RecoverPure has no receiver parameter to
// carry the call's real one. So a `this`-reading method that read as
// self-contained served the class-wide TOP-derived answer ("number, or
// NaN") instead of the exact field value ConstructedInstance/
// SummaryCallReceiver already resolve correctly — a divergence proven
// only end to end, since the receiver-construction and entry-fill unit
// tests (class_field_values_test.go,
// private_field_summary_lowering_test.go) each supply their own
// receiver directly and never exercise the routing decision that drops
// it.
//
// The fix: scanBody now marks ast.KindThisKeyword non-self-contained,
// which sends a `this`-reading method to the full inline
// (InlineContractCall -> InlineContractBody), whose SummaryCallReceiver
// read and KernelSummaryDirectOn/ClassMethodWalkCall threading already
// answer the receiver's own exact field.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// requireNoEngineKernel unseats the three global kernel seats
// (SetEngineKernel/SetTransferKernel/narrowing.SetNarrowKernel) for the
// duration of the calling test, restoring whatever a sibling test in
// this SAME package binary had already seated — every test in package
// walk runs in one process, and none of the kernel-loading helpers
// (superArrayLoadKernel, parseVocabKernel) unseat themselves afterward,
// so a "no kernel dylib" test that runs AFTER one of them inherits a
// live global kernel it never asked for. That is not a defect in those
// other tests (a leftover seated kernel is exactly what the NEXT
// kernel-backed test wants); it is a missing isolation guarantee here,
// for a test whose own point is proving the WALK route's answer with no
// kernel-served route in play at all.
func requireNoEngineKernel(t *testing.T) {
	t.Helper()
	priorEngine := EngineKernelHeld()
	priorTransfer := currentTransferKernel()
	priorNarrow := narrowing.NarrowKernel()
	SetEngineKernel(nil)
	SetTransferKernel(nil)
	narrowing.SetNarrowKernel(nil)
	t.Cleanup(func() {
		SetEngineKernel(priorEngine)
		SetTransferKernel(priorTransfer)
		narrowing.SetNarrowKernel(priorNarrow)
	})
}

// TestSummarize_AThisReadIsNotSelfContained pins the mechanism
// directly: a method whose ONLY body statement reads `this.#age` must
// summarize EffectFree (no write construct) but NOT SelfContained (the
// result depends on the receiver, which no parameter names).
func TestSummarize_AThisReadIsNotSelfContained(t *testing.T) {
	declaration := bundleMethodOf(t, `
		class Person {
			#age = 40;
			years(): number { return this.#age; }
		}
	`, "years")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	summary := Summarize(ctx, FunctionContract{Declaration: declaration})
	if !summary.EffectFree {
		t.Errorf("EffectFree = false, want true — years() writes nothing")
	}
	if summary.SelfContained {
		t.Errorf("SelfContained = true, want false — years() reads `this`, so its result is not a function of its (zero) arguments alone")
	}
}

// TestSummarize_APlainMethodWithNoThisReadStaysSelfContained is the
// negative control: a method that reads nothing beyond its own
// parameters must still summarize SelfContained — the this-keyword
// check must not overreach into methods that never mention `this`.
func TestSummarize_APlainMethodWithNoThisReadStaysSelfContained(t *testing.T) {
	declaration := bundleMethodOf(t, `
		class Box {
			double(n: number): number { return n * 2; }
		}
	`, "double")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	summary := Summarize(ctx, FunctionContract{Declaration: declaration})
	if !summary.EffectFree || !summary.SelfContained {
		t.Errorf("summary = %+v, want EffectFree and SelfContained both true — double() reads only its own parameter", summary)
	}
}

// TestSummarize_AGetAccessorReadThroughThisIsNotSelfContained pins the
// privateAccessorSlotLaidOut row's own mechanism: `this.#age` where
// `#age` is a private ACCESSOR, not a field, hits the exact same
// ast.KindThisKeyword node the field case does — scanBody's gate is
// syntactic on `this`, indifferent to what the read resolves to.
func TestSummarize_AGetAccessorReadThroughThisIsNotSelfContained(t *testing.T) {
	declaration := bundleMethodOf(t, `
		class PrivateAccessorHolder {
			#raw = 40;
			get #age(): number { return this.#raw; }
			read(): number { return this.#age; }
		}
	`, "read")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	summary := Summarize(ctx, FunctionContract{Declaration: declaration})
	if summary.SelfContained {
		t.Errorf("SelfContained = true, want false — read() reads `this.#age` through an accessor")
	}
}

// TestEvaluateCallExpression_APrivateFieldReaderServesTheExactReceiverNotTop
// is the full end-to-end pin for the privateFieldSlotLaidOut row
// (i-more-expressions.ts:105) on the WALK route alone (no kernel
// dylib): before the scanBody fix, `new PrivateHolder().read()` summarized
// self-contained, so EvaluateCallExpression's EffectFree branch sent it
// to RecoverPure/SummaryResultIn, which fills `this.#age` from
// unknownReceiver() and answers TOP — silence.Residue() here, since
// applySummary always declines with no kernel loaded and RecoverPure's
// own inline fallback runs with `this` unbound. After the fix, the
// call routes to the full inline instead, whose ClassMethodWalkCall
// binds `this` to THIS CALL's own constructed receiver and reads the
// exact 40 straight off it.
func TestEvaluateCallExpression_APrivateFieldReaderServesTheExactReceiverNotTop(t *testing.T) {
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, ""+
		"class PrivateHolder {\n"+
		"  #age = 40;\n"+
		"  read(): number {\n"+
		"    return this.#age;\n"+
		"  }\n"+
		"}\n"+
		"function f(): number { return new PrivateHolder().read(); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayNewMethodCall(t, p.Entry.AsNode(), "PrivateHolder", "read")
	value := evaluateExpression(ctx, NewEnv(), call)
	if value.Kind != abstractdomain.KindValues {
		t.Fatalf("value.Kind = %v, want KindValues — a self-contained (unfixed) reading answers KindUnknown from an unmodeled/TOP receiver instead of the exact field", value.Kind)
	}
	if len(value.Values) != 1 || value.Values[0] != 40 {
		t.Errorf("value.Values = %v, want [40] — new PrivateHolder().read() must answer the constructed instance's own #age, not a class-wide join", value.Values)
	}
}

// TestEvaluateCallExpression_APrivateAccessorReaderServesTheExactReceiverNotTop
// is the same end-to-end pin as the field-reader test above, for the
// privateAccessorSlotLaidOut row's own shape (i-more-expressions.ts:131):
// `#age` is a private ACCESSOR (`get #age()`) over a backing `#raw`
// field, not a PropertyDeclaration itself. ConstructedInstance's own
// getter pass (constructed_instance.go) already seeds a `#age` key from
// the accessor's return before this fix; what this test isolates is
// whether the scanBody routing fix actually lets that receiver reach
// `read()`'s call — the accessor shape hits the identical
// ast.KindThisKeyword node the field case does, so the same fix covers
// it, but the RECEIVER-BUILDING side (a getter, never a field slot) is
// different enough to warrant its own end-to-end pin rather than
// assuming the field case's coverage extends silently.
func TestEvaluateCallExpression_APrivateAccessorReaderServesTheExactReceiverNotTop(t *testing.T) {
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, ""+
		"class PrivateAccessorHolder {\n"+
		"  #raw = 40;\n"+
		"  get #age(): number {\n"+
		"    return this.#raw;\n"+
		"  }\n"+
		"  read(): number {\n"+
		"    return this.#age;\n"+
		"  }\n"+
		"}\n"+
		"function f(): number { return new PrivateAccessorHolder().read(); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayNewMethodCall(t, p.Entry.AsNode(), "PrivateAccessorHolder", "read")
	value := evaluateExpression(ctx, NewEnv(), call)
	if value.Kind != abstractdomain.KindValues {
		t.Fatalf("value.Kind = %v, want KindValues — a self-contained (unfixed) reading answers KindUnknown, not the accessor's own backing value", value.Kind)
	}
	if len(value.Values) != 1 || value.Values[0] != 40 {
		t.Errorf("value.Values = %v, want [40] — new PrivateAccessorHolder().read() must answer the constructed instance's own accessor-computed #age", value.Values)
	}
}
