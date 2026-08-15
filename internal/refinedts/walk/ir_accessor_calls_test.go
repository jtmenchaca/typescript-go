// Accessors as calls: resolving a property access to its get/set
// declarations, the hoisted getter read, the setter's call statement,
// and the declines that keep each honest.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── the recipe ──────────────────────────────────────────────────── */

// accessorAccessIn is the FIRST `<receiver>.<name>` property access
// spelled inside the named method's body — the node the routes are
// asked to read. A checker-backed program, because the resolution goes
// through symbols.
func accessorAccessIn(t *testing.T, p *program.CheckerProgram, method string, name string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsPropertyAccessExpression(node) {
			accessed := node.AsPropertyAccessExpression().Name()
			if accessed != nil && ast.IsIdentifier(accessed) && accessed.Text() == name {
				found = node
				return true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(methodNamed(t, p, method).Body())
	if found == nil {
		t.Fatalf("no `.%s` property access in %s's body", name, method)
	}
	return found
}

// accessorDeclaredIn is the get or set accessor named `name` on the
// first class of a checker-backed program — the declaration a resolution
// must answer.
func accessorDeclaredIn(t *testing.T, p *program.CheckerProgram, name string, wantSetter bool) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.ClassLikeData().Members.Nodes {
			isSetter := ast.IsSetAccessorDeclaration(member)
			if !ast.IsGetAccessorDeclaration(member) && !isSetter {
				continue
			}
			if isSetter != wantSetter {
				continue
			}
			memberName := member.Name()
			if memberName != nil && ast.IsIdentifier(memberName) && memberName.Text() == name {
				return member
			}
		}
	}
	t.Fatalf("no accessor named %s (setter %v)", name, wantSetter)
	return nil
}

// accessorCtx is a checker-backed context with an empty contract
// registry, and the memos cleared — each case parses its own program, so
// a remembered expansion or outcome from another case must not survive
// into it.
func accessorCtx(t *testing.T, source string) (*FlowContext, *program.CheckerProgram) {
	t.Helper()
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, source)
	return &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, p
}

// accessorSource is the class every resolution case reads: a backing
// field, a get/set pair over it, and a method that reads and writes the
// accessor.
const accessorSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  get value(): number { return this.store; }\n" +
	"  set value(v: number) { this.store = v; }\n" +
	"  run(): number { this.value = 3; return this.value; }\n" +
	"}\n"

// loweringAccessorSource is the class the CALL-BUILDING cases read: its
// accessor bodies touch no `this` field, so each lowers to a blob today.
//
// WHY THE TWO SOURCES DIFFER, and it is not cosmetic. thisBundleOf
// (ir_summary_body.go) gives a `this` bundle only to a METHOD
// declaration, so an ACCESSOR's body gets no this-entries: its
// `this.store` read finds no slot, and — because the read sits inside a
// `return`, which havocEnumerable refuses — the body DECLINES rather
// than going porous. Until that layout admits accessors, an accessor
// whose body touches `this` has no blob for these routes to call.
//
// That gap is in a file this agent does not own; it is reported rather
// than worked around. The routes themselves are complete: they build the
// call from whatever LoweredSummary the layout produced, so an accessor
// that lowers today exercises every rule, and an accessor that lowers
// once the layout admits `this` will carry its bundle entries through the
// same fill.
const loweringAccessorSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  get value(): number { return 7; }\n" +
	"  set value(v: number) { const held = v + 1; }\n" +
	"  run(): number { this.value = 3; return this.value; }\n" +
	"}\n"

/* ── resolution ──────────────────────────────────────────────────── */

func TestAccessorCalls_APropertyAccessResolvesToBothAccessorDeclarations(t *testing.T) {
	ctx, p := accessorCtx(t, accessorSource)
	access := accessorAccessIn(t, p, "run", "value")
	getter, setter, ok := AccessorDeclarationsOf(ctx, access)
	if !ok {
		t.Fatalf("`this.value` did not resolve — its symbol declares a get/set pair")
	}
	if getter != accessorDeclaredIn(t, p, "value", false) {
		t.Errorf("the getter answered %p, want the class's own get accessor node", getter)
	}
	if setter != accessorDeclaredIn(t, p, "value", true) {
		t.Errorf("the setter answered %p, want the class's own set accessor node", setter)
	}
}

func TestAccessorCalls_AGetOnlyPropertyAnswersTheGetterAndNoSetter(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  run(): number { return this.value; }\n"+
		"}\n")
	getter, setter, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value"))
	if !ok || getter == nil {
		t.Fatalf("a get-only property did not resolve its getter")
	}
	if setter != nil {
		t.Errorf("a get-only property answered a setter — no such declaration exists")
	}
}

func TestAccessorCalls_AnOrdinaryFieldIsNotAnAccessor(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  run(): number { return this.store; }\n"+
		"}\n")
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "store")); ok {
		t.Errorf("a plain field resolved as an accessor — it is a SLOT, and the census owns it")
	}
}

func TestAccessorCalls_AnOptionalStepDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  other: Box | undefined;\n"+
		"  run(): number { return this.other?.value ?? 0; }\n"+
		"}\n")
	// `this.other?.value` may read a property of nothing at all, and no
	// call statement stands for the call that may not have happened
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value")); ok {
		t.Errorf("an optional step resolved — no call statement spells a call that may not run")
	}
}

func TestAccessorCalls_ABodylessAccessorDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, "declare class Box {\n"+
		"  get value(): number;\n"+
		"}\n"+
		"class User {\n"+
		"  b: Box = new Box();\n"+
		"  run(): number { return this.b.value; }\n"+
		"}\n")
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value")); ok {
		t.Errorf("a body-less accessor resolved — there is no code to summarize")
	}
}

func TestAccessorCalls_ANilToleranceHoldsWithoutACheckerOrAContext(t *testing.T) {
	_, p := accessorCtx(t, accessorSource)
	access := accessorAccessIn(t, p, "run", "value")
	for name, ctx := range map[string]*FlowContext{
		"a nil context":       nil,
		"no program":          {Contracts: map[*ast.Symbol]*FunctionContract{}},
		"a program no reader": {P: &program.CheckerProgram{}, Contracts: map[*ast.Symbol]*FunctionContract{}},
	} {
		if _, _, ok := AccessorDeclarationsOf(ctx, access); ok {
			t.Errorf("%s resolved an accessor — nothing there can resolve a symbol", name)
		}
	}
}

func TestAccessorCalls_ANonPropertyAccessDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, accessorSource)
	// a call expression, not a property access: nothing here spells a
	// property name for a symbol to carry accessor declarations under
	if _, _, ok := AccessorDeclarationsOf(ctx, methodNamed(t, p, "run")); ok {
		t.Errorf("a method declaration resolved as a property access")
	}
	if _, _, ok := AccessorDeclarationsOf(ctx, nil); ok {
		t.Errorf("a nil node resolved as a property access")
	}
}
