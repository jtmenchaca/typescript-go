// split from ir_accessor_calls_test.go — the shared statement builder's fill rules

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// THE FILL RULES, checked directly on the statement builder. The two
// public routes each build their statement through accessorCallStatement,
// and what it does with a bundle row is the wave-4 rule the call sites
// share: a known field is a var, an unknown one is the UNKNOWN effect,
// and a WRITTEN one rides back into the caller's slot. Handing the
// builder a shape lets the rules be read one at a time, the way
// ir_summary_call_test.go's threadedShape reads bundleRetsAndArgs'.
func TestAccessorCalls_TheBuilderFillsKnownUnknownAndWrittenBundleRowsByTheWaveFourRules(t *testing.T) {
	context := accessorLoweringContext(nil,
		[]string{"this.store", "n"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	// entry 0 is the setter's declared parameter; the bundle rows follow
	shape := LoweredSummary{
		SlotCount: 4,
		DoneIndex: 2,
		RetIndex:  3,
		BundleEntries: []BundleEntry{
			{Path: "this.store", Index: 1, Written: true},
			{Path: "this.absent", Index: 2, Written: true},
		},
	}
	value := varEffect(1)
	statement, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", &value, -1)
	if !built {
		t.Fatalf("the builder declined a well-shaped accessor callee")
	}
	if statement.Args[0].Kind != kernelbridge.LoopEffectVarState || statement.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want the value effect the caller lowered, whole-state", statement.Args[0])
	}
	// a field the caller HAS: a whole-state copy of that slot, and its
	// write rides back
	if statement.Args[1].Kind != kernelbridge.LoopEffectVarState || statement.Args[1].Index != 0 {
		t.Errorf("the known field's entry = %+v, want a whole-state copy of the caller's this.store slot 0", statement.Args[1])
	}
	if statement.Rets[1] != 0 {
		t.Errorf("rets[1] = %d, want the caller's this.store slot 0 — the setter moves it", statement.Rets[1])
	}
	// a field the caller has NO slot for: UNKNOWN, never absent — absent
	// would claim the field IS undefined, which no caller said. Its write
	// lands nowhere, because nothing lowered can read that spelling.
	if statement.Args[2].Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("the unknown field's entry = %+v, want the UNKNOWN effect", statement.Args[2])
	}
	if statement.Rets[2] != -1 {
		t.Errorf("rets[2] = %d, want -1 — the caller holds no slot for that field", statement.Rets[2])
	}
	// no ret: a setter's value is discarded by the language
	if statement.Rets[shape.RetIndex] != -1 {
		t.Errorf("rets[%d] = %d, want -1 for a setter", shape.RetIndex, statement.Rets[shape.RetIndex])
	}
}

func TestAccessorCalls_TheBuilderRefusesARecordParameterLeafRow(t *testing.T) {
	// an accessor's parameter is ONE value, never a record, so a layout
	// carrying a non-"this." bundle row is not this site's shape
	context := accessorLoweringContext(nil, []string{"q.lo"}, []BindingKind{BindingKindNumber})
	shape := LoweredSummary{
		SlotCount:     3,
		DoneIndex:     1,
		RetIndex:      2,
		BundleEntries: []BundleEntry{{Path: "p.lo", Index: 0}},
	}
	if _, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", nil, -1); built {
		t.Errorf("a record-parameter leaf row built an accessor call — the shapes do not match")
	}
}

func TestAccessorCalls_TheBuilderEntersTheDoneFlagDownAndEverythingElseAbsent(t *testing.T) {
	// the padding must match summaryCallStatement's exactly, or a spliced
	// compile would start with the flag already up
	context := accessorLoweringContext(nil, []string{"this.store"}, []BindingKind{BindingKindNumber})
	shape := LoweredSummary{SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	statement, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", nil, -1)
	if !built {
		t.Fatalf("the builder declined a bundle-free callee")
	}
	if statement.Args[shape.DoneIndex].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("the done flag entered %+v, want the constant {0}", statement.Args[shape.DoneIndex])
	}
	for index, arg := range statement.Args {
		if index == shape.DoneIndex {
			continue
		}
		if arg.Kind != kernelbridge.AbsentConst().Kind {
			t.Errorf("entry %d = %+v, want the absent constant the apply side's padding sends", index, arg)
		}
	}
}
