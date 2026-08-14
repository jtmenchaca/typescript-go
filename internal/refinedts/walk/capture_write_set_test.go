// The method-calling capture: the census collects the called methods,
// and CaptureWriteSet answers the fields those methods can write,
// transitively — or refuses when any link is unenumerable. Until a
// consumer wires the havoc machinery, Believable() refuses the shape
// outright (pinned here), so nothing serves from it.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func captureClassOf(t *testing.T, source string) (*ast.Node, []BundleField, FieldCensus) {
	t.Helper()
	statements := bundleParse(t, source)
	classLike := statements[0]
	fields, ok := ClassFieldsOf(nil, classLike)
	if !ok {
		t.Fatalf("ClassFieldsOf declined %q", source)
	}
	census := FieldCensusOf(bundleMethodBodyOf(t, source), "this", BundleFieldsAs("this", fields))
	return classLike, fields, census
}

func TestCaptureWriteSet_ACapturedMethodsWritesAreTheHavocSet(t *testing.T) {
	classLike, fields, census := captureClassOf(t, `
		class C {
			count: number;
			other: number;
			m(xs: number[]) { xs.forEach(x => this.insert(x)); return this.other; }
			insert(x: number) { this.count = x; }
		}
	`)
	if census.Escapes {
		t.Fatalf("a method-calling capture escaped at the census: %+v", census)
	}
	if len(census.CapturedMethodCalls) != 1 || census.CapturedMethodCalls[0] != "insert" {
		t.Fatalf("CapturedMethodCalls = %v, want [insert]", census.CapturedMethodCalls)
	}
	if census.Believable() {
		t.Errorf("a method-calling capture answered believable — no consumer has the havoc machinery yet")
	}
	writes, ok := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
	if !ok {
		t.Fatalf("the write set was incomputable — insert is a plain declared method")
	}
	if len(writes) != 1 || writes[0].Name != "count" {
		t.Errorf("write set = %v, want [count] — insert writes count and nothing else", fieldNames(writes))
	}
}

func TestCaptureWriteSet_TheClosureFollowsDirectCalls(t *testing.T) {
	// insert calls register DIRECTLY; register writes count — the
	// closure must carry the write through the direct call
	classLike, fields, census := captureClassOf(t, `
		class C {
			count: number;
			m(xs: number[]) { xs.forEach(x => this.insert(x)); return 1; }
			insert(x: number) { this.register(x); }
			register(x: number) { this.count = x; }
		}
	`)
	writes, ok := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
	if !ok {
		t.Fatalf("the write set was incomputable across a direct call")
	}
	if len(writes) != 1 || writes[0].Name != "count" {
		t.Errorf("write set = %v, want [count] through insert -> register", fieldNames(writes))
	}
}

func TestCaptureWriteSet_ARecursiveMethodTerminates(t *testing.T) {
	classLike, fields, census := captureClassOf(t, `
		class C {
			count: number;
			m(xs: number[]) { xs.forEach(x => this.walk(x)); return 1; }
			walk(x: number) { this.count = x; this.walk(x - 1); }
		}
	`)
	writes, ok := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
	if !ok {
		t.Fatalf("a self-recursive method made the set incomputable")
	}
	if len(writes) != 1 || writes[0].Name != "count" {
		t.Errorf("write set = %v, want [count]", fieldNames(writes))
	}
}

func TestCaptureWriteSet_AnUndeclaredMethodRefuses(t *testing.T) {
	// the capture calls a method the class does not declare (inherited,
	// or a field-held arrow) — its writes are unenumerable
	classLike, fields, census := captureClassOf(t, `
		class C {
			count: number;
			m(xs: number[]) { xs.forEach(x => this.missing(x)); return 1; }
		}
	`)
	if _, ok := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls); ok {
		t.Errorf("an undeclared method produced a write set — nothing bounds its writes")
	}
}

// THE SOUNDNESS PIN for the whole admission: across a code-running
// statement, a HAVOCKED field's entry value is gone — the stored closure
// may have run and written it — while a field no captured method can
// write keeps its value. Read off the lowered IR walked directly.
func TestCaptureWriteSet_TheHavockedFieldIsNotBelievedAcrossCalls(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := labelMethodOf(t,
		"class C { count: number; other: number; "+
			"m(xs: number[]): number { xs.forEach(x => this.insert(x)); return this.count + this.other; } "+
			"insert(x: number) { this.count = x; } }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	lowered, ok := RelowerSummaryBody(ctx, declaration)
	if !ok {
		t.Fatalf("a method-calling capture declined the lowering — the admission did not engage")
	}
	countIndex, otherIndex := -1, -1
	countWritten := false
	for _, entry := range lowered.BundleEntries {
		if entry.Path == "this.count" {
			countIndex = entry.Index
			countWritten = entry.Written
		}
		if entry.Path == "this.other" {
			otherIndex = entry.Index
		}
	}
	if countIndex < 0 || otherIndex < 0 {
		t.Fatalf("bundle entries missing: %+v — the bundle did not expand", lowered.BundleEntries)
	}
	if !countWritten {
		t.Errorf("this.count is not marked written — the caller would keep its own belief across the call")
	}
	entries := make([]kernelbridge.KnownStateWire, lowered.SlotCount)
	for i := range entries {
		entries[i] = absentState
	}
	entries[countIndex] = kernelbridge.KnownStateWire{
		Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5})),
	}
	entries[otherIndex] = kernelbridge.KnownStateWire{
		Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7})),
	}
	entries[lowered.DoneIndex] = doneDownState
	exits := kernel.Walk(entries, lowered.Stmts)
	if countIndex >= len(exits) || otherIndex >= len(exits) {
		t.Fatalf("the walk answered %d states", len(exits))
	}
	if !exits[countIndex].Top {
		t.Errorf("this.count exits still holding %+v — the forEach could have run insert and written it", exits[countIndex])
	}
	if exits[otherIndex].Top {
		t.Errorf("this.other exits TOP — no captured method writes it, so its value must survive")
	}
	if !kernel.Member(exits[otherIndex].Set, []float64{7}) {
		t.Errorf("this.other exits excluding 7: %+v", exits[otherIndex].Set)
	}
}

func TestCaptureWriteSet_AnEscapingCalledMethodRefuses(t *testing.T) {
	// the called method hands `this` away — past that point no write
	// set bounds what moves
	classLike, fields, census := captureClassOf(t, `
		class C {
			count: number;
			m(xs: number[]) { xs.forEach(x => this.insert(x)); return 1; }
			insert(x: number) { register(this); }
		}
	`)
	if _, ok := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls); ok {
		t.Errorf("an escaping called method produced a write set")
	}
}
