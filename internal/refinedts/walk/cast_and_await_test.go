// EvaluateCast's sort-crossing branch (cast_and_await.go): a
// KindUnknown operand crossing sorts keeps its incoming ResidueReason
// rather than dropping it into a bare silence.Residue(). A cast
// changes the host-type story the position reads under, not why the
// walk had no value for the operand in the first place.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// castExpressionIn walks the entry source for its first AsExpression
// node — the `x as number` shape EvaluateCast's sort-crossing branch
// reads.
func castExpressionIn(t *testing.T, source *ast.SourceFile) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsAsExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	source.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no as-expression found")
	}
	return found
}

// TestEvaluateCast_ASortCrossingCarriesTheOperandsIncomingReason pins
// the defect: `x as number` over a string-sorted `x` crosses sorts
// (from=string, to=number), and the pre-seeded operand — a
// KindUnknown carrying a ResidueReason via silence.ResidueOf — must
// keep that reason on the cast's own unknown result, not lose it to a
// bare silence.Residue().
func TestEvaluateCast_ASortCrossingCarriesTheOperandsIncomingReason(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: unknown): void { x as number; }\n")
	castExpr := castExpressionIn(t, p.Entry)
	env := NewEnv()
	env.Set("x", silence.ResidueOf("a probe reason naming the walk's own first blocker"))
	ctx := &FlowContext{P: p}
	got := EvaluateCast(ctx, env, castExpr)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("EvaluateCast(x as number) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "a probe reason naming the walk's own first blocker") {
		t.Errorf("ResidueReason = %q, want it to carry the operand's own incoming reason", got.ResidueReason)
	}
}

// TestEvaluateCast_ASortCrossingWithNoIncomingReasonStaysBare pins the
// companion case: a reasonless KindUnknown operand (silence.Residue())
// crossing sorts still builds a bare residue — nothing to carry
// forward.
func TestEvaluateCast_ASortCrossingWithNoIncomingReasonStaysBare(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: unknown): void { x as number; }\n")
	castExpr := castExpressionIn(t, p.Entry)
	env := NewEnv()
	env.Set("x", silence.Residue())
	ctx := &FlowContext{P: p}
	got := EvaluateCast(ctx, env, castExpr)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("EvaluateCast(x as number) = %+v, want KindUnknown", got)
	}
	if got.ResidueReason != "" {
		t.Errorf("ResidueReason = %q, want empty for a reasonless operand", got.ResidueReason)
	}
}
