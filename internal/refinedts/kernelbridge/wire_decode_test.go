package kernelbridge

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func parseWire(t *testing.T, wire string) any {
	t.Helper()
	var parsed any
	if err := json.Unmarshal([]byte(wire), &parsed); err != nil {
		t.Fatalf("unparseable wire %q: %v", wire, err)
	}
	return parsed
}

func TestDecodeWireNumberTheDyadicPairAndTheInfinities(t *testing.T) {
	cases := []float64{0, math.Copysign(0, -1), 1, -8, 0.5, math.Inf(1), math.Inf(-1)}
	for _, x := range cases {
		wire := marshalWireValue(WireNumberOf(x))
		var raw any
		if err := json.Unmarshal([]byte(wire), &raw); err != nil {
			t.Fatalf("unparseable wire %q: %v", wire, err)
		}
		got := DecodeWireNumber(raw)
		// `!=` alone would not catch a sign erasure: -0 != 0 is false for
		// IEEE floats under Go's ordinary comparison, so +0 and -0 round
		// off as equal there — math.Signbit is what tells them apart,
		// same as WireNumberOf's own encode-side check.
		if got != x || math.Signbit(got) != math.Signbit(x) {
			t.Errorf("DecodeWireNumber(WireNumberOf(%v)) = %v (signbit %v), want %v (signbit %v)",
				x, got, math.Signbit(got), x, math.Signbit(x))
		}
	}
}

func TestDecodeWireSetEncodeSetRoundTrip(t *testing.T) {
	countLike := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
	got := DecodeWireSet(parseWire(t, EncodeSet(countLike)))
	if !reflect.DeepEqual(got, countLike) {
		t.Errorf("round trip countLike: got %+v, want %+v", got, countLike)
	}

	// the empty set — OneOf(nil) — is boundary/exports.lean's own
	// bottom-enclosure spelling (encodeEnclosure: a BOTTOM result wires
	// as oneOf over an EMPTY w list, never through don't-care bounds).
	// The Go encoder must produce that exact wire shape, and the Go
	// decoder must read it back to the same empty OneOf every other
	// provably-empty position already builds (walk/kernel_delegation.go's
	// package-private emptySet).
	empty := refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))
	emptyWire := EncodeSet(empty)
	wantEmptyWire := `{"forms":[{"form":"oneOf","w":[]}]}`
	if emptyWire != wantEmptyWire {
		t.Errorf("EncodeSet(OneOf(nil)) = %q, want %q", emptyWire, wantEmptyWire)
	}
	got = DecodeWireSet(parseWire(t, emptyWire))
	if !reflect.DeepEqual(got, empty) {
		t.Errorf("round trip empty OneOf: got %+v, want %+v", got, empty)
	}
	if len(got.Forms) != 1 || got.Forms[0].Form != refinementsets.FormOneOf || len(got.Forms[0].W) != 0 {
		t.Errorf("round trip empty OneOf did not survive as a single empty-W oneOf form: %+v", got)
	}

	window := refinementsets.MakeRefinedSet(refinementsets.Above(-1), refinementsets.AtMost(10))
	got = DecodeWireSet(parseWire(t, EncodeSet(window)))
	if !reflect.DeepEqual(got, window) {
		t.Errorf("round trip window: got %+v, want %+v", got, window)
	}

	words := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2, 3}))
	got = DecodeWireSet(parseWire(t, EncodeSet(words)))
	if !reflect.DeepEqual(got, words) {
		t.Errorf("round trip words: got %+v, want %+v", got, words)
	}

	joined := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1)})),
	))
	got = DecodeWireSet(parseWire(t, EncodeSet(joined)))
	if !reflect.DeepEqual(got, joined) {
		t.Errorf("round trip joined: got %+v, want %+v", got, joined)
	}
}

func TestDecodeJudgeAnswerFaultsAndTheWitnessBound(t *testing.T) {
	answered := DecodeJudgeAnswer(map[string]any{
		"structural":   true,
		"witnessed":    false,
		"witnessBound": float64(4),
		"faults": []any{
			map[string]any{"path": float64(1), "code": float64(2), "messageText": "empty"},
		},
	})
	if !answered.Structural {
		t.Errorf("Structural = false, want true")
	}
	if answered.Witnessed {
		t.Errorf("Witnessed = true, want false")
	}
	if answered.WitnessBound != 4 {
		t.Errorf("WitnessBound = %v, want 4", answered.WitnessBound)
	}
	want := []KernelFault{{Path: 1, Code: 2, MessageText: "empty"}}
	if !reflect.DeepEqual(answered.Faults, want) {
		t.Errorf("Faults = %+v, want %+v", answered.Faults, want)
	}
}

func TestDecodeValidateChainTheThreeKinds(t *testing.T) {
	got := DecodeValidateChain(map[string]any{"kind": "set"})
	if got.Kind != ValidateChainSet {
		t.Errorf("kind set: got %+v", got)
	}
	got = DecodeValidateChain(map[string]any{"kind": "answer", "answer": true})
	if got.Kind != ValidateChainAnswer || !got.Answer {
		t.Errorf("kind answer: got %+v", got)
	}
	got = DecodeValidateChain(map[string]any{"kind": "declined", "why": "cost"})
	if got.Kind != ValidateChainDeclined || got.Why != "cost" {
		t.Errorf("kind declined: got %+v", got)
	}
}

func TestDecodeBoundsAndDecodeMembers(t *testing.T) {
	got := DecodeBounds(map[string]any{"empty": true})
	if !got.Empty {
		t.Errorf("DecodeBounds(empty) = %+v, want Empty true", got)
	}

	hull := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer,
	)
	got = DecodeBounds(map[string]any{"set": parseWire(t, EncodeSet(hull))})
	if got.Empty || !reflect.DeepEqual(got.Hull, hull) {
		t.Errorf("DecodeBounds(hull) = %+v, want Empty false Hull %+v", got, hull)
	}

	members := DecodeMembers(map[string]any{
		"members": []any{
			parseWire(t, marshalWireValue(WireNumberOf(0))),
			parseWire(t, marshalWireValue(WireNumberOf(1))),
			parseWire(t, marshalWireValue(WireNumberOf(2))),
		},
	})
	want := []float64{0, 1, 2}
	if !reflect.DeepEqual(members, want) {
		t.Errorf("DecodeMembers = %v, want %v", members, want)
	}
}
