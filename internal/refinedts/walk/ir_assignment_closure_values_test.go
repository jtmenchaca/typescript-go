// A stored closure's write set: the census reads the closure NODE, so
// the closure's own top-level writes are havocked at its declaration —
// the under-count that let them pass unhavocked is pinned closed here.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestClosureValues_AStoredClosuresOwnWriteIsHavockedAtTheDeclaration(t *testing.T) {
	// `g`'s body writes `total` at its own top level — no nesting. The
	// declaration must havoc total's slot: g may run at a time no
	// statement here places, so nothing after the declaration may
	// believe total. Before the census was handed the closure node,
	// this write set read empty and total kept its stale value.
	context := &LoweringContext{
		Bindings: []string{"total", "g"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindUnknown},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNone},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		total = 1;
		const g = () => { total = 2; };
	`))
	if !ok {
		t.Fatalf("a stored writing closure declined the body — the write set covers it")
	}
	havocked := false
	for _, statement := range stmts {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 0 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			havocked = true
		}
	}
	if !havocked {
		t.Errorf("total's slot was not havocked at the declaration — the closure's own write went unseen: %+v", stmts)
	}
}

func TestClosureValues_ClosureWriteSlotsReadsTheClosureNodeNotItsBody(t *testing.T) {
	// The contract pinned directly: handed the closure NODE the census
	// collects the body's own top-level write; handed the bare BODY
	// block it collects nothing (the census walks a non-function
	// subtree looking for closures INSIDE it).
	statements := loweringParse(t, `const g = () => { total = 2; };`)
	if len(statements) != 1 || !ast.IsVariableStatement(statements[0]) {
		t.Fatalf("parse did not yield the one variable statement")
	}
	declarations := statements[0].AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	closure := Unwrapped(declarations[0].AsVariableDeclaration().Initializer)
	context := &LoweringContext{
		Bindings: []string{"total"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	if slots := ClosureWriteSlots(context, closure); len(slots) != 1 {
		t.Errorf("ClosureWriteSlots(closure) = %v, want total's one slot", slots)
	}
	if slots := ClosureWriteSlots(context, closure.Body()); len(slots) != 0 {
		t.Errorf("ClosureWriteSlots(body) = %v — a bare body block holds no closures, the empty set documents why the argument is the closure node", slots)
	}
}
