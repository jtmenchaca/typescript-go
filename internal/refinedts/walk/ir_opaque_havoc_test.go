// The havoc floor: a statement no route reads lowers as `unknown` into
// every slot it could have written, and the body keeps its route.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// havocParse is the whole-file parse the havoc tests read statements
// from — syntax alone, no checker.
func havocParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/havoc.ts", Path: "/havoc.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

// havocTargets is the slots a havoc answer writes, in the order the
// statements came out — every statement must be an assign of `unknown`,
// which is what the floor is allowed to be.
func havocTargets(t *testing.T, statements []kernelbridge.IrStatement) []int {
	t.Helper()
	out := make([]int, 0, len(statements))
	for _, statement := range statements {
		if statement.Kind != kernelbridge.IrStatementAssign {
			t.Fatalf("a havoc statement is %v, want an assign — the floor writes nothing else", statement.Kind)
		}
		if statement.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("a havoc effect is %v, want unknown — the floor never guesses a set", statement.Effect.Kind)
		}
		out = append(out, statement.Target)
	}
	return out
}

/* ── (a) assignment targets ──────────────────────────────────────── */

func TestOpaqueHavoc_AnUnreadableAssignmentHavocsItsTargetSlot(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	// no route reads a tagged template; the write to x is still visible
	statements := havocParse(t, "x = tag`a${y}b`;")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("an unreadable assignment declined — its target slot is nameable")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 1 || targets[0] != 0 {
		t.Errorf("targets = %v, want [0] — x's slot alone (y is a scalar, read by value)", targets)
	}
}

func TestOpaqueHavoc_CompoundsAndStepsHavocTheirTargets(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a", "b", "c"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
	}
	// one statement writing three slots through three spellings
	statements := havocParse(t, "{ a **= 2; b++; --c; }")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a block of writes declined — each target slot is nameable")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 3 || targets[0] != 0 || targets[1] != 1 || targets[2] != 2 {
		t.Errorf("targets = %v, want [0 1 2] — every written slot, in slot order", targets)
	}
}

/* ── (b) mentioned flattened locals ──────────────────────────────── */

func TestOpaqueHavoc_MentioningAFlattenedRecordHavocsEveryLeaf(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"p.lo", "p.hi", "n"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
	}
	// p is handed to code the lowering cannot see: any leaf may move
	statements := havocParse(t, "sink(p, n);")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a mention of a flattened record declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — p's two leaves; n is a scalar passed by value", targets)
	}
}

func TestOpaqueHavoc_MentioningAFlattenedArrayOrCollectionHavocsEverySlotItHolds(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"xs.len", "xs.elem", "m.size", "m.vals", "m.keys"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber,
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := havocParse(t, "sink(xs, m);")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a mention of an array and a Map declined")
	}
	targets := havocTargets(t, lowered)
	want := []int{0, 1, 2, 3, 4}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — both slots of the array and all three of the Map", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
}

func TestOpaqueHavoc_AScalarMentionAloneHavocsNothing(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	// scalars pass BY VALUE: a callee cannot write back through one, so
	// a mention that writes nothing havocs nothing at all
	statements := havocParse(t, "sink(x, y);")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a scalar-only call declined")
	}
	if len(lowered) != 0 {
		t.Errorf("lowered = %+v, want no statements — nothing the callee could have written", lowered)
	}
}

/* ── (c) declared names ──────────────────────────────────────────── */

func TestOpaqueHavoc_ADeclarationFromAnUnreadableRightSideHavocsItsOwnSlot(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindUnknown},
		Typeofs:  []TypeofTag{TypeofTagNone},
	}
	statements := havocParse(t, "const x = someUnreadableThing`raw`;")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("an unreadable declaration declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 1 || targets[0] != 0 {
		t.Errorf("targets = %v, want [0] — the declared name's own slot", targets)
	}
}

func TestOpaqueHavoc_ABoundNameWithNoSlotNeedsNoHavoc(t *testing.T) {
	// nothing lowered can READ such a name later either: a read of a
	// name the slot vector never laid out declines wherever it appears,
	// so there is no knowledge for an unread write to falsify
	context := &LoweringContext{
		Bindings: []string{"kept"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	statements := havocParse(t, "const untracked = weird`x`;")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a declaration of an untracked name declined")
	}
	if len(lowered) != 0 {
		t.Errorf("lowered = %+v, want no statements — the bound name has no slot", lowered)
	}
	// and the slot that WAS laid out is untouched
	for _, statement := range lowered {
		if statement.Target == 0 {
			t.Errorf("the unrelated slot 0 was havocked")
		}
	}
}

func TestOpaqueHavoc_ADestructuringTargetWithASlotIsHavocked(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"lo", "hi"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "const { lo, hi } = opaque`src`;")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("an unreadable destructuring declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — both bound names' slots", targets)
	}
}

/* ── dedup and order ─────────────────────────────────────────────── */

func TestOpaqueHavoc_TheSameSlotNamedTwiceIsWrittenOnce(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"p.lo", "p.hi"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	// p is mentioned three times and its leaves are named once each
	statements := havocParse(t, "sink(p, p, p);")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a repeated mention declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — deduplicated, in slot order", targets)
	}
}

func TestOpaqueHavoc_TheAnswerIsInSlotOrderWhateverOrderTheSyntaxNamedThem(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a", "b", "c"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
	}
	// written c, a, b in source; answered 0, 1, 2 in slot order
	statements := havocParse(t, "{ c **= 1; a **= 1; b **= 1; }")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("the block declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 3 || targets[0] != 0 || targets[1] != 1 || targets[2] != 2 {
		t.Errorf("targets = %v, want [0 1 2] — slot order, not source order", targets)
	}
}

/* ── loops and try, unioned once ─────────────────────────────────── */

func TestOpaqueHavoc_AnUnreadableLoopHavocsHeadAndBodyOnceEach(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"k", "total", "m.size", "m.vals", "m.keys"},
		Sorts: []BindingKind{
			BindingKindString, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagString, TypeofTagNumber,
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	// a for-in has no lowering at all; its head mentions the collection
	// and its body writes a scalar. A loop is its statements repeated and
	// havoc is idempotent, so ONE pass covers every trip count.
	statements := havocParse(t, "for (const k in m) { total **= 2; }")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a for-in declined — its head and body slots are nameable")
	}
	targets := havocTargets(t, lowered)
	want := []int{0, 1, 2, 3, 4}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — k, total, and m's three slots, each once", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
}

func TestOpaqueHavoc_ATryCatchFinallyHavocsTheUnionOfAllThree(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a", "b", "c", "untouched"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	// any PREFIX of the try may have run, the catch may or may not, and
	// the finally always does — so every slot any block writes is one the
	// statement could have written
	statements := havocParse(t, `
		try { a **= 1; } catch (e) { b **= 1; } finally { c **= 1; }
	`)
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a try/catch/finally declined")
	}
	targets := havocTargets(t, lowered)
	want := []int{0, 1, 2}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — the union of all three blocks", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
	for _, target := range targets {
		if target == 3 {
			t.Errorf("the untouched slot 3 was havocked — the union is what the blocks write, not everything")
		}
	}
}

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

/* ── the opaque call ─────────────────────────────────────────────── */

func TestOpaqueCallHavoc_AnUnresolvableCalleeHavocsItsTargetAndTheLeavesItWasHanded(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"out", "p.lo", "p.hi", "n"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := havocParse(t, "out = fetchish(p, n);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression).AsBinaryExpression().Right
	lowered, ok := OpaqueCallHavoc(context, Unwrapped(call), 0)
	if !ok {
		t.Fatalf("an unresolvable call declined")
	}
	targets := havocTargets(t, lowered)
	// the mentioned leaves first, the call's own value last
	want := []int{1, 2, 0}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — p's leaves and the target slot", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
}

func TestOpaqueCallHavoc_ABareCallStillHavocsTheObjectsItWasHanded(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"xs.len", "xs.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "consume(xs);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a bare unresolvable call declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the array's two slots; nothing reads the value", targets)
	}
}

func TestOpaqueCallHavoc_AReceiverThatIsAFlattenedLocalIsHavockedToo(t *testing.T) {
	// `p.y.then(cb)` hands p out through the RECEIVER, not an argument
	context := &LoweringContext{
		Bindings: []string{"p.a", "p.b"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "p.a.then(cb);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a call on a flattened local's member declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the receiver's leaves", targets)
	}
}

func TestOpaqueHavoc_ACallbackThatWritesAnOuterSlotHavocsIt(t *testing.T) {
	// the callee may CALL the callback, and the callback writes this
	// body's slot. Stopping the scan at the function boundary would leave
	// `total`'s knowledge standing across a write the walk never saw.
	context := &LoweringContext{
		Bindings: []string{"total", "xs.len", "xs.elem", "untouched"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := havocParse(t, "xs.forEachish(v => { total **= v; });")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a callback-taking call declined")
	}
	targets := havocTargets(t, lowered)
	want := []int{0, 1, 2}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — total (written inside the callback) and xs's two slots", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
	for _, target := range targets {
		if target == 3 {
			t.Errorf("the untouched slot 3 was havocked")
		}
	}
}

func TestOpaqueCallHavoc_ACallbackArgumentsWritesRideThroughTheCallRouteToo(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"seen", "p.a"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "mystery(p, () => { seen **= 1; });")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a call with a writing callback argument declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the callback's write and p's leaf", targets)
	}
}

func TestOpaqueHavoc_ACallbacksOwnReturnDoesNotDeclineTheStatement(t *testing.T) {
	// a return inside a CALLBACK returns from the callback, not from this
	// body, so it is not the control transfer the impossibility scan
	// refuses
	context := &LoweringContext{
		Bindings: []string{"xs.len", "xs.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "xs.mapish(function (v) { return v + 1; });")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a callback containing a return declined — the return is the callback's, not the body's")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — xs's two slots", targets)
	}
}

func TestOpaqueHavoc_ThisBodysOwnReturnDeclines(t *testing.T) {
	// a havoc writes slots and falls through; a return raises the done
	// flag and stops the block. Swallowing one would leave the flag down
	// and every later statement walked as if it ran.
	context := &LoweringContext{
		Bindings: []string{"x", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Result:   &LoweringResult{Done: 1, Ret: 2},
	}
	statements := havocParse(t, "try { return weird`x`; } finally { }")
	if _, ok := OpaqueHavocStatements(context, statements[0]); ok {
		t.Errorf("a statement carrying this body's own return havocked — the raise would be dropped")
	}
}

/* ── FirstHavoc ──────────────────────────────────────────────────── */

func TestOpaqueHavoc_FirstHavocIsSetOnceByTheEarliestHavockedConstruct(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "m.size", "m.vals", "m.keys"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	if context.FirstHavoc != "" {
		t.Fatalf("FirstHavoc starts %q, want empty", context.FirstHavoc)
	}
	statements := havocParse(t, `
		fetchish(m);
		for (const k in m) { x **= 1; }
	`)
	if _, ok := OpaqueHavocStatements(context, statements[0]); !ok {
		t.Fatalf("the call statement declined")
	}
	if context.FirstHavoc != "call fetchish" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "call fetchish")
	}
	// the SECOND havoc leaves the name where it stands — first wins, so
	// the name always points at the earliest place the body stopped being
	// read whole
	if _, ok := OpaqueHavocStatements(context, statements[1]); !ok {
		t.Fatalf("the for-in declined")
	}
	if context.FirstHavoc != "call fetchish" {
		t.Errorf("FirstHavoc = %q after a second havoc, want it unchanged at %q",
			context.FirstHavoc, "call fetchish")
	}
}

func TestOpaqueHavoc_NoteFirstHavocIgnoresAnEmptyNameAndANilContext(t *testing.T) {
	context := &LoweringContext{}
	NoteFirstHavoc(context, "")
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q after an empty note, want empty", context.FirstHavoc)
	}
	NoteFirstHavoc(nil, "for-in") // must not panic
	NoteFirstHavoc(context, "for-in")
	if context.FirstHavoc != "for-in" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "for-in")
	}
}

func TestOpaqueHavoc_TheConstructNameSpellsTheSourceShape(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "p.a"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	cases := []struct {
		source string
		want   string
	}{
		{"for (const k in p) { }", "for-in"},
		{"for await (const v of src) { }", "for await"},
		{"try { x **= 1; } finally { }", "try"},
		{"o[key] = p;", "computed member"},
		{"this.injector.load(p);", "call this.injector.load"},
	}
	for _, held := range cases {
		fresh := *context
		fresh.FirstHavoc = ""
		statements := havocParse(t, held.source)
		if _, ok := OpaqueHavocStatements(&fresh, statements[0]); !ok {
			t.Errorf("%q declined", held.source)
			continue
		}
		if fresh.FirstHavoc != held.want {
			t.Errorf("%q named %q, want %q", held.source, fresh.FirstHavoc, held.want)
		}
	}
}

/* ── the whole body, through the kernel ──────────────────────────── */

func TestOpaqueHavoc_ReadableKnowledgeSurvivesAroundAnOpaqueCallInAWholeBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// x is written readably, then an unresolvable call havocs ONLY what
	// it was handed, then y is written readably again. The body keeps its
	// route, and both readable writes reach the exit intact.
	context := &LoweringContext{
		Bindings: []string{"x", "y", "z"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		x = 5;
		z = mysteryCall(x);
		y = x + 1;
	`))
	if !ok {
		t.Fatalf("the body declined — an opaque call should have havocked, not declined")
	}
	if context.FirstHavoc != "call mysteryCall" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "call mysteryCall")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{{Top: true}, {Top: true}, {Top: true}},
		stmts,
	)
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want the readable write to have survived the havoc")
	}
	xSet := loweringSetOf(t, x)
	if !kernel.Member(xSet, []float64{5}) {
		t.Errorf("member(x, [5]) = false, want true — x was written readably and never havocked")
	}
	if kernel.Member(xSet, []float64{6}) {
		t.Errorf("member(x, [6]) = true, want false — the havoc must not have widened x")
	}
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want the arithmetic after the havoc to have been read")
	}
	ySet := loweringSetOf(t, y)
	if !kernel.Member(ySet, []float64{6}) {
		t.Errorf("member(y, [6]) = false, want true — y = x + 1 with x still pinned at 5")
	}
	if kernel.Member(ySet, []float64{7}) {
		t.Errorf("member(y, [7]) = true, want false")
	}
	// z took the call's value, which claims nothing
	if !exit[2].Top {
		t.Errorf("z.Top = false, want true — the havocked slot claims nothing")
	}
}

func TestOpaqueHavoc_AHavockedFlattenedLocalLosesItsLeavesAndOnlyThose(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := &LoweringContext{
		Bindings: []string{"p.lo", "p.hi", "keep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		keep = 3;
		handOut(p);
	`))
	if !ok {
		t.Fatalf("the body declined")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
			{Top: true},
		},
		stmts,
	)
	if !exit[0].Top {
		t.Errorf("p.lo.Top = false, want true — the leaf was handed to unseen code")
	}
	if !exit[1].Top {
		t.Errorf("p.hi.Top = false, want true — the leaf was handed to unseen code")
	}
	keep := exit[2]
	if keep.Top {
		t.Fatalf("keep.Top = true, want the unrelated slot's knowledge to have survived")
	}
	if !kernel.Member(loweringSetOf(t, keep), []float64{3}) {
		t.Errorf("member(keep, [3]) = false, want true")
	}
}
