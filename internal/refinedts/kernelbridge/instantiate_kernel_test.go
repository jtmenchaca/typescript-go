// The round trip on the proved code itself, through cgo: the same
// first questions kernel_bridge.test.ts asks, wires spelled by hand
// (wire_format's own JSON), answers decoded from the kernel's
// envelope. Skipped when the dylib is absent — `pnpm kernel:native`
// builds it.

package kernelbridge

import (
	"encoding/json"
	"os"
	"testing"
)

const dylibPath = "../../../../../refined-lean/native/build/librefined_kernel.dylib"

func loadForTest(t *testing.T) *NativeKernel {
	t.Helper()
	if _, err := os.Stat(dylibPath); err != nil {
		t.Skip("native kernel dylib absent — run `pnpm kernel:native`")
	}
	kernel, err := InstantiateNative(dylibPath)
	if err != nil {
		t.Fatalf("InstantiateNative: %v", err)
	}
	t.Cleanup(kernel.Close)
	return kernel
}

// booleanField mirrors wire_decode.ts: an { "error": string } answer
// throws; a boolean rides its named field.
func booleanField(t *testing.T, raw string, field string) bool {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("kernel answered non-JSON: %q (%v)", raw, err)
	}
	if message, held := parsed["error"].(string); held {
		t.Fatalf("kernel: %s", message)
	}
	value, held := parsed[field].(bool)
	if !held {
		t.Fatalf("kernel answered without a boolean %q: %s", field, raw)
	}
	return value
}

// z.number().min(0).int() — refinedSet(atLeast(0), integer)
const countLike = `{"forms":[{"form":"atLeast","a":{"num":0,"exp":0}},{"form":"integer"}]}`

// z.intersection(z.number().max(5), z.number().min(10)) — ∅
const impossible = `{"forms":[{"form":"atMost","a":{"num":5,"exp":0}},{"form":"atLeast","a":{"num":10,"exp":0}}]}`

func TestMembershipTheRuntimeCheckOverTheWire(t *testing.T) {
	kernel := loadForTest(t)
	cases := []struct {
		name  string
		tuple string
		want  bool
	}{
		{"3 is a count", `[{"num":3,"exp":0}]`, true},
		{"-1 is not", `[{"num":-1,"exp":0}]`, false},
		{"0.5 is not an integer", `[{"num":1,"exp":-1}]`, false},
		{"the empty tuple is not a scalar", `[]`, false},
		{"a pair is not a scalar", `[{"num":1,"exp":0},{"num":2,"exp":0}]`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := kernel.Call2("kernel_member", countLike, c.tuple)
			if err != nil {
				t.Fatalf("kernel_member: %v", err)
			}
			if got := booleanField(t, raw, "member"); got != c.want {
				t.Errorf("member(countLike, %s) = %v, want %v",
					c.tuple, got, c.want)
			}
		})
	}
}

func TestEmptinessOnTheOneTupleLayer(t *testing.T) {
	kernel := loadForTest(t)
	raw, err := kernel.Call1("kernel_scalar_empty", impossible)
	if err != nil {
		t.Fatalf("kernel_scalar_empty: %v", err)
	}
	if !booleanField(t, raw, "empty") {
		t.Errorf("scalarEmpty(max 5 ∧ min 10) = false, want true")
	}
	inhabited := `{"forms":[{"form":"atMost","a":{"num":10,"exp":0}},{"form":"atLeast","a":{"num":5,"exp":0}}]}`
	raw, err = kernel.Call1("kernel_scalar_empty", inhabited)
	if err != nil {
		t.Fatalf("kernel_scalar_empty: %v", err)
	}
	if booleanField(t, raw, "empty") {
		t.Errorf("scalarEmpty(max 10 ∧ min 5) = true, want false")
	}
}

func TestSubsetAgreesWithTheTSBridge(t *testing.T) {
	kernel := loadForTest(t)
	// integers ≥ 0 ⊆ integers; integers ⊄ integers ≥ 0
	ints := `{"forms":[{"form":"integer"}]}`
	raw, err := kernel.Call2("kernel_scalar_subset", countLike, ints)
	if err != nil {
		t.Fatalf("kernel_scalar_subset: %v", err)
	}
	if !booleanField(t, raw, "subset") {
		t.Errorf("countLike ⊆ ints = false, want true")
	}
	raw, err = kernel.Call2("kernel_scalar_subset", ints, countLike)
	if err != nil {
		t.Fatalf("kernel_scalar_subset: %v", err)
	}
	if booleanField(t, raw, "subset") {
		t.Errorf("ints ⊆ countLike = true, want false")
	}
}
