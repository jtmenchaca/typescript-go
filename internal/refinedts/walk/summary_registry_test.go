// Registry semantics, with no kernel in sight: the store's job is to
// build a declaration's summary AT MOST ONCE, to remember a decline
// so it is never rebuilt, and to answer a cycle without poisoning the
// entry the outer build is still computing. Each test replaces the
// builder seam so it can count builds directly.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// withSummaryBuilder swaps the registry's builder for the duration of
// one test, and clears the store on both sides so neither a previous
// test's answers nor this one's leak.
func withSummaryBuilder(t *testing.T, builder func(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, int, bool)) {
	t.Helper()
	clearSummaryStore()
	held := summaryBuilder
	summaryBuilder = builder
	t.Cleanup(func() {
		summaryBuilder = held
		clearSummaryStore()
	})
}

func clearSummaryStore() {
	summaryBlobsMu.Lock()
	summaryBlobs = map[*ast.Node]summaryBlobEntry{}
	summaryBuilding = map[*ast.Node]struct{}{}
	summaryCycled = map[*ast.Node]struct{}{}
	summarySelfBlobs = map[*ast.Node]kernelbridge.SummaryBlob{}
	summaryBlobsMu.Unlock()
}

func TestSummaryBlobFor_OneDeclarationBuildsOnceAndIsSharedAcrossAsks(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n + 1; }")
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		return "blob-f", 7, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	first, firstOk := SummaryBlobFor(ctx, declaration)
	// a SECOND check's context asking the same declaration: the store
	// is keyed by declaration alone, so it must not build again
	other := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	second, secondOk := SummaryBlobFor(other, declaration)

	if !firstOk || !secondOk {
		t.Fatalf("SummaryBlobFor ok = (%v, %v), want (true, true)", firstOk, secondOk)
	}
	if first != "blob-f" || second != "blob-f" {
		t.Errorf("blobs = (%q, %q), want both %q", first, second, "blob-f")
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1 — a summary is context-free, so two asks share one build", builds)
	}
}

func TestSummaryOutShapeFor_AnswersTheBuildersReturnSlotWithoutRebuilding(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n + 1; }")
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		return "blob-f", 4, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	if _, ok := SummaryBlobFor(ctx, declaration); !ok {
		t.Fatalf("SummaryBlobFor ok = false, want true")
	}
	index, ok := SummaryOutShapeFor(ctx, declaration)
	if !ok {
		t.Fatalf("SummaryOutShapeFor ok = false, want true")
	}
	if index != 4 {
		t.Errorf("out index = %d, want 4", index)
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1 — the shape reads the stored entry", builds)
	}
}

func TestSummaryOutShapeFor_BuildsWhenAskedBeforeTheBlob(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		return "blob-f", 3, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	index, ok := SummaryOutShapeFor(ctx, declaration)
	if !ok || index != 3 {
		t.Fatalf("SummaryOutShapeFor = (%d, %v), want (3, true)", index, ok)
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1", builds)
	}
}

func TestSummaryBlobFor_ADeclineIsRememberedSoTheBodyIsLoweredOnce(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(o: { k: number }) { o.k = 1; return 1; }")
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		return "", 0, false
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	for i := 0; i < 3; i++ {
		if _, ok := SummaryBlobFor(ctx, declaration); ok {
			t.Fatalf("ask %d: SummaryBlobFor ok = true, want false", i)
		}
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1 — a decline is remembered, not retried at every call", builds)
	}
	if index, ok := SummaryOutShapeFor(ctx, declaration); ok {
		t.Errorf("SummaryOutShapeFor on a declined declaration = (%d, true), want ok false", index)
	}
}

func TestSummaryBlobFor_ACycleAnswersFalseWithoutStoringADecline(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	var reentryOk bool
	reentered := false
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		// the lowering demanding its OWN callee blob — the recursion the
		// cycle guard has to catch
		if !reentered {
			reentered = true
			_, reentryOk = SummaryBlobFor(ctx, d)
		}
		return "blob-f", 2, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	blob, ok := SummaryBlobFor(ctx, declaration)
	if !reentered {
		t.Fatalf("the builder never re-entered — the test proves nothing")
	}
	if reentryOk {
		t.Errorf("the re-entry answered true; a cycle must answer false so the lowering havocs that call")
	}
	// the cycle is remembered, so the fixpoint upgrade is attempted once
	// after this build — with no kernel loaded it declines immediately and
	// the floor blob below is what stands
	if !ok || blob != "blob-f" {
		t.Fatalf("outer SummaryBlobFor = (%q, %v), want (%q, true) — the cycle must not poison the entry", blob, ok, "blob-f")
	}
	// the re-entry stored nothing, so the outer build's answer is what
	// stands: a later ask hits it without building again
	again, againOk := SummaryBlobFor(ctx, declaration)
	if !againOk || again != "blob-f" {
		t.Errorf("second ask = (%q, %v), want (%q, true)", again, againOk, "blob-f")
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1", builds)
	}
}

func TestSummaryBlobFor_ANilDeclarationDeclines(t *testing.T) {
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		t.Errorf("the builder ran for a nil declaration")
		return "", 0, false
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := SummaryBlobFor(ctx, nil); ok {
		t.Errorf("SummaryBlobFor(nil) ok = true, want false")
	}
	if _, ok := SummaryOutShapeFor(ctx, nil); ok {
		t.Errorf("SummaryOutShapeFor(nil) ok = true, want false")
	}
}
