// split from ir_opaque_havoc_test.go — the impossibilities and the decline's name

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the impossibilities ─────────────────────────────────────────── */

func TestOpaqueHavoc_TheGenuineEnumerationImpossibilitiesDecline(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	sources := map[string]string{
		// every free name inside may resolve into o's properties
		"a with statement": "with (o) { x **= 1; }",
		// the code it runs is a value and may write any binding in scope
		"a bare eval call": "eval(src);",
		// the flag the lowering would raise cannot tell a throw that
		// leaves the body from one an enclosing try catches
		"a throw":              "throw new Error('x');",
		"a throw inside a try": "try { throw e; } catch (e) { x **= 1; }",
	}
	for name, source := range sources {
		statements := havocParse(t, source)
		if _, ok := OpaqueHavocStatements(context, statements[0]); ok {
			t.Errorf("%s havocked — its written-slot set is not a syntactic question", name)
		}
	}
}

/* ── control that leaves, and control that does not ──────────────── */

func TestOpaqueHavoc_ATransferThatCannotLeaveTheStatementIsAdmitted(t *testing.T) {
	// the refusal is about control LEAVING the enumerated statement. A
	// break whose switch or loop sits inside the statement cannot leave
	// it: the run departs at the statement's own exit, which is exactly
	// where the havoc's writes sit, and the slot union already covers
	// every block the transfer skipped or repeated.
	context := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	sources := map[string]string{
		// the switch is inside the statement being enumerated
		"a bare break leaving a contained switch": "if (c) { switch (k) { case 1: x **= 1; break; } }",
		// so is the loop
		"a bare break leaving a contained loop":    "if (c) { while (c) { x **= 1; break; } }",
		"a bare continue in a contained loop":      "if (c) { while (c) { x **= 1; continue; } }",
		"a labelled break to a contained label":    "outer: while (c) { x **= 1; break outer; }",
		"a labelled continue to a contained label": "outer: while (c) { x **= 1; continue outer; }",
	}
	for name, source := range sources {
		statements := havocParse(t, source)
		if _, ok := OpaqueHavocStatements(context, statements[0]); !ok {
			t.Errorf("%s declined — the transfer cannot leave the statement", name)
		}
	}
}

func TestOpaqueHavoc_ATransferThatLeavesTheStatementDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// the enumerated statement is the INNER one in each case: the loop or
	// label it transfers to is outside what would be havocked
	cases := []struct {
		source string
		// the index chain from the parsed top-level statement down to the
		// statement handed to the floor
		reach func(t *testing.T, top *ast.Node) *ast.Node
		want  string
	}{
		{
			// `break outer` inside the INNER while: the label is outside it
			source: "outer: while (c) { while (c) { x **= 1; break outer; } }",
			reach: func(t *testing.T, top *ast.Node) *ast.Node {
				outer := top.AsLabeledStatement().Statement
				return outer.AsWhileStatement().Statement.AsBlock().Statements.Nodes[0]
			},
			want: "labeled break crossing out",
		},
		{
			// a BARE break inside an `if` that is itself inside a loop: the
			// loop is outside the enumerated if
			source: "while (c) { if (c) { x **= 1; break; } }",
			reach: func(t *testing.T, top *ast.Node) *ast.Node {
				return top.AsWhileStatement().Statement.AsBlock().Statements.Nodes[0]
			},
			want: "break crossing out",
		},
		{
			source: "while (c) { if (c) { x **= 1; continue; } }",
			reach: func(t *testing.T, top *ast.Node) *ast.Node {
				return top.AsWhileStatement().Statement.AsBlock().Statements.Nodes[0]
			},
			want: "continue crossing out",
		},
	}
	for _, held := range cases {
		statements := havocParse(t, held.source)
		inner := held.reach(t, statements[0])
		if _, ok := OpaqueHavocStatements(context, inner); ok {
			t.Errorf("%q havocked — the transfer leaves the enumerated statement", held.source)
		}
		if named := DeclinedHavocConstruct(inner); named != held.want {
			t.Errorf("%q named %q, want %q", held.source, named, held.want)
		}
	}
}

func TestOpaqueHavoc_TheDeclineNamesTheBlockingConstruct(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"with (o) { x **= 1; }", "with statement"},
		{"eval(src);", "eval call"},
		{"try { throw e; } catch (e) { x **= 1; }", "throw inside try"},
		{"if (c) { throw e; }", "throw inside if"},
		{"if (c) { return weird`x`; }", "return inside if"},
	}
	for _, held := range cases {
		statements := havocParse(t, held.source)
		if named := DeclinedHavocConstruct(statements[0]); named != held.want {
			t.Errorf("%q named %q, want %q", held.source, named, held.want)
		}
	}
}

func TestOpaqueHavoc_AMemberCalledEvalIsAnOrdinaryCall(t *testing.T) {
	// only the DIRECT `eval(…)` has the scope-piercing semantics; a
	// method named eval on some object is an ordinary call
	context := &LoweringContext{
		Bindings: []string{"p.lo"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	statements := havocParse(t, "o.eval(p);")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("`o.eval(p)` declined — a member call is not the direct eval")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 1 || targets[0] != 0 {
		t.Errorf("targets = %v, want [0] — p's one leaf", targets)
	}
}
