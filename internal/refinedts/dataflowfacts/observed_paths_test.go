package dataflowfacts

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
)

// observedPathsFunctionOf mirrors walk/kernel_delegation_test.go's
// statementOf: a bare parsed source, no checker — ObservedPathsOf's
// scan is purely syntactic, so a throwaway parse is enough to drive
// it, the same way ObservedNamesOf's own tests would.
func observedPathsFunctionOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/o.ts", Path: "/o.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 {
		t.Fatalf("no statements parsed from %q", source)
	}
	return file.Statements.Nodes[0]
}

func TestObservedPathsOf_TwoFieldReadsMapToTheirSortedKeys(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: { lo: number; hi: number }) {
  return cfg.hi + cfg.lo;
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	want := []string{"hi", "lo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ObservedPathsOf(cfg) = %v, want %v (sorted)", got, want)
	}
}

func TestObservedPathsOf_PassingCfgWholeToACallMapsToNil(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: { lo: number; hi: number }) {
  helper(cfg);
  return cfg.lo;
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	if got != nil {
		t.Errorf("ObservedPathsOf(cfg) = %v, want nil (whole-value: passed to helper)", got)
	}
}

func TestObservedPathsOf_WritingCfgLoMapsToNil(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: { lo: number; hi: number }) {
  cfg.lo = 1;
  return cfg.hi;
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	if got != nil {
		t.Errorf("ObservedPathsOf(cfg) = %v, want nil (whole-value: a write target)", got)
	}
}

func TestObservedPathsOf_ComputedIndexMapsToNil(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: Record<string, number>, k: string) {
  return cfg[k];
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	if got != nil {
		t.Errorf("ObservedPathsOf(cfg) = %v, want nil (whole-value: computed index)", got)
	}
	// k itself is read whole, as the index expression
	if kPaths, ok := paths["k"]; !ok || kPaths != nil {
		t.Errorf("ObservedPathsOf(k) = %v, ok=%v, want nil, true (whole-value: used as an index)", kPaths, ok)
	}
}

func TestObservedPathsOf_LiteralStringIndexReadsAsAKey(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: Record<string, number>) {
  return cfg["lo"];
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	want := []string{"lo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ObservedPathsOf(cfg) = %v, want %v", got, want)
	}
}

func TestObservedPathsOf_NameUsedBareAnywhereMapsToNil(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
function f(cfg: { lo: number }) {
  const alias = cfg;
  return cfg.lo + (alias ? 1 : 0);
}
`)
	paths := ObservedPathsOf(fn)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	if got != nil {
		t.Errorf("ObservedPathsOf(cfg) = %v, want nil (whole-value: aliased)", got)
	}
}

func TestObservedPathsOfNode_SameScanForAnArrowCallback(t *testing.T) {
	fn := observedPathsFunctionOf(t, `
const cb = (cfg: { lo: number; hi: number }) => cfg.lo + cfg.hi;
`)
	varStatement := fn.AsVariableStatement()
	decl := varStatement.DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
	arrow := decl.AsVariableDeclaration().Initializer
	paths := ObservedPathsOfNode(arrow)
	got, ok := paths["cfg"]
	if !ok {
		t.Fatalf("expected cfg to be observed, got %v", paths)
	}
	want := []string{"hi", "lo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ObservedPathsOfNode(cfg) = %v, want %v", got, want)
	}
}
