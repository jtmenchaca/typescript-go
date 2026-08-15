// The havoc floor: a statement no route reads lowers as `unknown` into
// every slot it could have written, and the body keeps its route.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
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
