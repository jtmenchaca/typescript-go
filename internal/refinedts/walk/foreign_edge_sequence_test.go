// The file-carried shape (a JSON payload named at an argv position
// rather than piped on stdin), the no-payload-at-a-stdin-json-target
// determination, and the sequence-crossing conversions for a
// mixed-element array literal (KindList, not the flat exact-tuple
// path) — both the conversion itself and its consumer-side dense mark.
// Split out of foreign_edge_crossing_test.go, which still holds the
// shared foreignEdgeFixture type and the stdin/array-literal outbound
// fixture builders every foreign_edge_*_test.go file in this package
// reuses.

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the file-carried shape, against a real kernel and real syntax ──── */

// foreignFileFixture builds a file-json artifact (level_from_file.py's own
// real anatomy) and an edge whose Payload/FilePath are set directly — the
// grain checkOutboundLeg itself judges at, mirroring foreignOutboundFixture's
// own direct-edge-construction style rather than round-tripping through
// fileCrossingOf's own syntax recognition (pinned separately, below).
func foreignFileFixture(t *testing.T, samplesLiteral string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignFileTarget(t)
	writeForeignArtifact(t, targetPath, foreignFileJSONArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = "+samplesLiteral+";\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[1].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	env := NewEnv()
	AnalyzeVariableStatement(ctx, env, statements[0])
	// FilePath only needs to be non-nil here to route checkOutboundLeg's
	// dispatch to checkFileCrossing — its own value never enters the fit
	// judged below (only a channel-mismatch DeclineNode, not exercised by
	// this fixture's green/fit-only cases). statements[1] (a real, distinct
	// node) stands in rather than aliasing Payload to two different roles.
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, FilePath: statements[1], StdoutName: "stdout"},
		reported: &reported,
	}
}

// TestCheckFileCrossing_AFittingPayloadIsSilent pins the green path: the
// SAME stdin fit chain the pure-stdin shape uses, applied to a file-json
// target — samples (0.5, -0.3, 0.2) fit -2 … 2 at length 3 >= 1, so the
// crossing passes with nothing reported.
func TestCheckFileCrossing_AFittingPayloadIsSilent(t *testing.T) {
	fixture := foreignFileFixture(t, "[0.5, -0.3, 0.2]")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting file-carried crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting file-carried crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_NoPayloadAgainstAStdinJSONTargetDeterminesRatherThanDeclines(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	edge := &ForeignEdge{Call: fixture.edge.Call, TargetPath: fixture.edge.TargetPath, StdoutName: "stdout"}
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, edge, fixture.artifact)
	if outcome != nil {
		t.Fatalf("a no-payload call against a stdin-json target declined/fired: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a no-payload call against a stdin-json target reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

/* ── construct: a mixed-element array literal reads as a sequence crossing (KindList) ── */
//
// checkSequenceCrossing previously judged only KindValues{PrimitiveArray}
// (every element an exact literal number, sequenceCrossingOfExactTuple) —
// a literal with ANY non-exact element (a range, a parameter's declared
// window) evaluates through EvaluateArrayLiteral's non-flat path to
// KindList, which SetOfKnown explicitly refuses (lattice_operations.go).
// sequenceCrossingOfKindList reads each slot the same per-position way
// array_literal.go's own scalarPositionSet already does and rebuilds the
// Repetition window checkSequenceCrossing judges every other sequence
// shape through.

func TestSequenceCrossingOfKindList_EveryScalarSlotUnionsIntoARepetitionWindow(t *testing.T) {
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	converted, ok := sequenceCrossingOfKindList(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfKindList declined a list of scalar-shaped slots")
	}
	window, windowOk := refinementsets.AsRepetition(converted.Set)
	if !windowOk {
		t.Fatalf("converted.Set is not a Repetition: %+v", converted)
	}
	if window.Lo != 3 || window.Hi == nil || *window.Hi != 3 {
		t.Errorf("window = %+v, want an exact 3-element repetition", window)
	}
}

func TestSequenceCrossingOfKindList_AnObjectShapedSlotDeclines(t *testing.T) {
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	if _, ok := sequenceCrossingOfKindList(crossing); ok {
		t.Errorf("sequenceCrossingOfKindList converted a list with an object-shaped slot")
	}
}

/* ── construct: both crossing converters mark their rebuilt window PROVED DENSE ── */
//
// sequenceCrossingOfExactTuple and sequenceCrossingOfKindList both rebuild
// their Repetition window from a source AbstractValue built element-by-element
// with no hole grammar (a KindValues{PrimitiveArray} tuple, a KindList array
// literal) — the same "every counted index is an own property" proof
// MapOutcome's own KnownSetDense mark rests on (callback_element_outcome.go).
// Both converters now carry that mark forward, and InBoundsElementOf's
// proved-in-bounds arm (element_in_bounds.go) reads it to skip the
// PossiblyAbsent wrap it otherwise always applies to a repetition-shaped
// receiver.

func TestSequenceCrossingOfExactTuple_ConvertedWindowCarriesSeqDense(t *testing.T) {
	crossing := abstractdomain.KnownValues([]float64{0.5, -0.3, 0.2}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	converted, ok := sequenceCrossingOfExactTuple(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfExactTuple declined an exact 3-element tuple")
	}
	if !converted.SeqDenseKnown || !converted.SeqDense {
		t.Fatalf("converted = %+v, want SeqDenseKnown && SeqDense", converted)
	}
}

func TestSequenceCrossingOfKindList_ConvertedWindowCarriesSeqDense(t *testing.T) {
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	converted, ok := sequenceCrossingOfKindList(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfKindList declined a list of scalar-shaped slots")
	}
	if !converted.SeqDenseKnown || !converted.SeqDense {
		t.Fatalf("converted = %+v, want SeqDenseKnown && SeqDense", converted)
	}
}

// TestInBoundsElementOf_ExactTupleCrossingDropsTheAbsenceWrapper pins the
// consumer side for sequenceCrossingOfExactTuple's dense mark: an in-bounds
// read of the converted window determines the bare element set outright,
// mirroring TestMapProductLengthFloor_IndexZeroReadIsDetermined's own
// MapOutcome pin (map_product_length_floor_test.go) but built directly from
// the converter rather than through a full .map() pipeline.
func TestInBoundsElementOf_ExactTupleCrossingDropsTheAbsenceWrapper(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, nil)
	env := NewEnv()
	crossing := abstractdomain.KnownValues([]float64{0.5, -0.3, 0.2}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	receiver, ok := sequenceCrossingOfExactTuple(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfExactTuple declined an exact 3-element tuple")
	}
	index := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := InBoundsElementOf(InBoundsElementOfParams{Ctx: ctx, Env: env, Expression: node, Receiver: receiver, Index: index})
	if got == nil {
		t.Fatalf("InBoundsElementOf(exact-tuple crossing, in bounds) = nil, want a determined result")
	}
	if got.Kind == abstractdomain.KindPossiblyUndefined {
		t.Errorf("got = %+v, want the bare element with no absence wrapper — the source tuple is hole-free by construction", *got)
	}
}

// TestInBoundsElementOf_KindListCrossingDropsTheAbsenceWrapper is the same
// consumer-side pin for sequenceCrossingOfKindList's dense mark.
func TestInBoundsElementOf_KindListCrossingDropsTheAbsenceWrapper(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, nil)
	env := NewEnv()
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	receiver, ok := sequenceCrossingOfKindList(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfKindList declined a list of scalar-shaped slots")
	}
	index := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := InBoundsElementOf(InBoundsElementOfParams{Ctx: ctx, Env: env, Expression: node, Receiver: receiver, Index: index})
	if got == nil {
		t.Fatalf("InBoundsElementOf(KindList crossing, in bounds) = nil, want a determined result")
	}
	if got.Kind == abstractdomain.KindPossiblyUndefined {
		t.Errorf("got = %+v, want the bare element with no absence wrapper — the source list is hole-free by construction", *got)
	}
}

func TestCheckOutboundLeg_AMixedElementArrayLiteralInsideTheStatedEntryPasses(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	fixture.env.Set("boosted", abstractdomain.KnownList([]abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}, abstractdomain.TrustProved))
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome != nil {
		t.Fatalf("a mixed-element literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting mixed-element crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_AnObjectShapedSlotInAMixedLiteralStaysUndetermined(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	fixture.env.Set("boosted", abstractdomain.KnownList([]abstractdomain.AbstractValue{
		abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}, abstractdomain.TrustProved))
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline == "" {
		t.Fatalf("a list with an object-shaped slot did not decline: %+v", outcome)
	}
	if !strings.Contains(outcome.Decline, "not read as one here") {
		t.Errorf("Decline = %q, want the ordinary sequence-shape decline", outcome.Decline)
	}
}

// TestCheckOutboundLeg_AWholeObjectPayloadFiresRatherThanDeclines pins
// d-data-legs.ts's objectKeysReduceUndetermined row: the WHOLE crossing
// value (not one slot inside a list, TestCheckOutboundLeg_
// AnObjectShapedSlotInAMixedLiteralStaysUndetermined's own case) is
// itself KindObject — `Object.keys(defaults).reduce(...)` building an
// accumulator object, exactly the shape callback_outcome.go's reduce
// reading answers. This is a DETERMINED shape mismatch (an object is
// never a sequence), so checkSequenceCrossing fires 7001 through
// ctx.Report rather than returning the undetermined "not read as one
// here" Decline — the same distinction checkScalarCrossing already
// draws between a decided refutation and an unreadable crossing.
func TestCheckOutboundLeg_AWholeObjectPayloadFiresRatherThanDeclines(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	fixture.env.Set("boosted", abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "gain", Value: abstractdomain.KnownValues([]float64{0.5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
			{Name: "offset", Value: abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
		}, nil, true, abstractdomain.TrustProved, false))
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a whole-object payload answered nil — want a fired outcome")
	}
	if outcome.Decline != "" {
		t.Fatalf("a whole-object payload declined (%q) — want a determined fire, not an undetermined decline", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("a whole-object payload reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "is of type 'object'") {
		t.Errorf("MessageText = %q, want it to name the object type explicitly", (*fixture.reported)[0].MessageText)
	}
}
