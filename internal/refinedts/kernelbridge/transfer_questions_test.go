package kernelbridge

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestTransferWireBinaryUnaryPowSubOrdGap(t *testing.T) {
	A := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
	B := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))

	add := parseWire(t, TransferWire(TransferQuestion{Op: TransferOpAdd, A: A, B: B})).(map[string]any)
	if add["op"] != "add" {
		t.Errorf("add.op = %v, want add", add["op"])
	}
	if !reflect.DeepEqual(add["A"], parseWire(t, EncodeSet(A))) {
		t.Errorf("add.A mismatch")
	}
	if !reflect.DeepEqual(add["B"], parseWire(t, EncodeSet(B))) {
		t.Errorf("add.B mismatch")
	}

	neg := parseWire(t, TransferWire(TransferQuestion{Op: TransferOpNeg, A: A})).(map[string]any)
	if neg["op"] != "neg" {
		t.Errorf("neg.op = %v, want neg", neg["op"])
	}
	if !reflect.DeepEqual(neg["A"], parseWire(t, EncodeSet(A))) {
		t.Errorf("neg.A mismatch")
	}
	if _, hasB := neg["B"]; hasB {
		t.Errorf("neg.B present, want absent")
	}

	pow := parseWire(t, TransferWire(TransferQuestion{
		Op:   TransferOpPow,
		Base: PowOperandWire{Kind: PowOperandNaN},
		Exp:  PowOperandWire{Kind: PowOperandSet, Set: B},
	})).(map[string]any)
	if pow["op"] != "pow" {
		t.Errorf("pow.op = %v, want pow", pow["op"])
	}
	base := pow["base"].(map[string]any)
	if base["kind"] != "nan" {
		t.Errorf("pow.base.kind = %v, want nan", base["kind"])
	}
	exp := pow["exp"].(map[string]any)
	if exp["kind"] != "set" {
		t.Errorf("pow.exp.kind = %v, want set", exp["kind"])
	}
	if !reflect.DeepEqual(exp["set"], parseWire(t, EncodeSet(B))) {
		t.Errorf("pow.exp.set mismatch")
	}

	gap := parseWire(t, TransferWire(TransferQuestion{Op: TransferOpSubOrdGap, A: A, B: B, C: 2})).(map[string]any)
	if gap["op"] != "subOrdGap" {
		t.Errorf("gap.op = %v, want subOrdGap", gap["op"])
	}
	c := gap["c"].(map[string]any)
	if c["num"].(float64)*pow2(c["exp"].(float64)) != 2 {
		t.Errorf("gap.c = %v, want 2", c)
	}
}

func TestDecodeTransferAnswerTheFourKinds(t *testing.T) {
	got := DecodeTransferAnswer(map[string]any{"kind": "nan"})
	if got.Kind != TransferAnswerNaN {
		t.Errorf("nan: got %+v", got)
	}
	got = DecodeTransferAnswer(map[string]any{"kind": "unknown"})
	if got.Kind != TransferAnswerUnknown {
		t.Errorf("unknown: got %+v", got)
	}
	got = DecodeTransferAnswer(map[string]any{
		"kind":   "values",
		"values": []any{map[string]any{"num": float64(3), "exp": float64(0)}},
	})
	if got.Kind != TransferAnswerValues || !reflect.DeepEqual(got.Values, []float64{3}) {
		t.Errorf("values: got %+v", got)
	}
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1), refinementsets.Integer)
	got = DecodeTransferAnswer(map[string]any{"kind": "set", "set": parseWire(t, EncodeSet(set))})
	if got.Kind != TransferAnswerSet || !reflect.DeepEqual(got.Set, set) {
		t.Errorf("set: got %+v, want %+v", got.Set, set)
	}
}

// TestDecodeTransferAnswerBottomEnclosureIsTheEmptySet exercises the
// exact wire boundary/exports.lean's encodeEnclosure sends for a
// BOTTOM arithmetic result: {"kind":"set","set":{"forms":[{"form":
// "oneOf","w":[]}]}} — an unreachable-result answer, not a
// hand-picked shape. The literal JSON here is the Lean encoder's own
// spelling (encodeEnclosure's `if e.bot then` arm), parsed the same
// way a real kernel answer would arrive (Answered → DecodeTransferAnswer),
// so this pins the Go decoder against the actual wire the kernel
// sends, not merely against the Go encoder's own output.
func TestDecodeTransferAnswerBottomEnclosureIsTheEmptySet(t *testing.T) {
	leanBottomWire := `{"kind":"set","set":{"forms":[{"form":"oneOf","w":[]}]}}`
	got := DecodeTransferAnswer(Answered(leanBottomWire))
	if got.Kind != TransferAnswerSet {
		t.Fatalf("bottom enclosure: got Kind %v, want TransferAnswerSet", got.Kind)
	}
	want := refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))
	if !reflect.DeepEqual(got.Set, want) {
		t.Errorf("bottom enclosure: got %+v, want the empty OneOf %+v", got.Set, want)
	}
}
