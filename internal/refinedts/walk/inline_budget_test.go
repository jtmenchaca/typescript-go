// Unit tests for inlineBudgetOpen's two axes: depth and volume. No
// kernel, no parsing — a bare FlowContext exercises both limits.

package walk

import "testing"

func TestInlineBudgetOpen_DepthAtTheLimitClosesDown(t *testing.T) {
	ctx := &FlowContext{InlineDepth: inlineDepthLimit}
	if inlineBudgetOpen(ctx) {
		t.Fatalf("inlineBudgetOpen(depth=%d) = true, want false", inlineDepthLimit)
	}
}

func TestInlineBudgetOpen_DepthBelowTheLimitStaysOpen(t *testing.T) {
	ctx := &FlowContext{InlineDepth: inlineDepthLimit - 1}
	if !inlineBudgetOpen(ctx) {
		t.Fatalf("inlineBudgetOpen(depth=%d) = false, want true", inlineDepthLimit-1)
	}
}

func TestInlineBudgetOpen_VolumeOpensOnceThenCloses(t *testing.T) {
	budget := 1
	ctx := &FlowContext{InlineBudget: &budget}
	if !inlineBudgetOpen(ctx) {
		t.Fatal("inlineBudgetOpen(budget=1) first call = false, want true")
	}
	if inlineBudgetOpen(ctx) {
		t.Fatal("inlineBudgetOpen(budget=1) second call = true, want false")
	}
}

func TestInlineBudgetOpen_NilBudgetWithShallowDepthStaysOpen(t *testing.T) {
	ctx := &FlowContext{}
	if !inlineBudgetOpen(ctx) {
		t.Fatal("inlineBudgetOpen(nil budget, depth=0) = false, want true")
	}
}
