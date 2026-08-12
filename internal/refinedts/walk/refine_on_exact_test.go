// from assignability/refine_on_exact.test.ts
//
// Exact-value `.refine` evaluator: true, false, and null where the
// body leaves the language.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// predicateOf mirrors the TS test's predicateOf: parse `const f = <src>;`
// and hand back the arrow function initializer. A bare parse (no
// checker, no program) is enough — RefineDecidedOnExact reads only
// syntax, exactly like the TS test's ts.createSourceFile.
func predicateOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/t.ts", Path: "/t.ts"}
	file := parser.ParseSourceFile(opts, "const f = "+source+";", core.ScriptKindTS)
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsArrowFunction(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	file.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no arrow function in %q", source)
	}
	return found
}

func TestRefineDecidedOnExact_NumericComparisonOnOneValue(t *testing.T) {
	pred := predicateOf(t, "(n) => n > 0")
	decided, ok := RefineDecidedOnExact(pred, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if !ok || decided != true {
		t.Errorf("RefineDecidedOnExact(n=1) = %v, %v, want true, true", decided, ok)
	}
	decided, ok = RefineDecidedOnExact(pred, abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if !ok || decided != false {
		t.Errorf("RefineDecidedOnExact(n=0) = %v, %v, want false, true", decided, ok)
	}
}

func TestRefineDecidedOnExact_StringAffixAndLength(t *testing.T) {
	starts := predicateOf(t, `(s) => s.startsWith("ab")`)
	decided, ok := RefineDecidedOnExact(starts, abstractdomain.KnownValues([]float64{97, 98, 99}, abstractdomain.PrimitiveString, abstractdomain.TrustProved))
	if !ok || decided != true {
		t.Errorf("RefineDecidedOnExact(startsWith) = %v, %v, want true, true", decided, ok)
	}
	length := predicateOf(t, "(s) => s.length === 2")
	decided, ok = RefineDecidedOnExact(length, abstractdomain.KnownValues([]float64{97, 98}, abstractdomain.PrimitiveString, abstractdomain.TrustProved))
	if !ok || decided != true {
		t.Errorf("RefineDecidedOnExact(length===2) = %v, %v, want true, true", decided, ok)
	}
}

func TestRefineDecidedOnExact_OneReturnBlockWorksExtraParamOrNonValuesIsNull(t *testing.T) {
	block := predicateOf(t, "(n) => { return n > 0; }")
	decided, ok := RefineDecidedOnExact(block, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if !ok || decided != true {
		t.Errorf("RefineDecidedOnExact(block) = %v, %v, want true, true", decided, ok)
	}
	two := predicateOf(t, "(a, b) => a > 0")
	_, ok = RefineDecidedOnExact(two, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if ok {
		t.Errorf("RefineDecidedOnExact(two params) ok = true, want false")
	}
	setKnown := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	_, ok = RefineDecidedOnExact(predicateOf(t, "(n) => n > 0"), setKnown)
	if ok {
		t.Errorf("RefineDecidedOnExact(non-values known) ok = true, want false")
	}
}
