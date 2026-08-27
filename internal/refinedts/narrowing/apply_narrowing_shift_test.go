package narrowing

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestA6_guard_band_AWindowCarriesAcrossAnExactShift pins the fact that
// lets a guard on `d.getTime()` reach `const off = d.getTime() - REF`:
// the window proved of the place is the same window, displaced, of the
// binding. Endpoints move by the shift; `integer` survives an integral
// shift.
func TestA6_guard_band_AWindowCarriesAcrossAnExactShift(t *testing.T) {
	held := Narrowed{
		Binding: "off",
		Forms: []refinementsets.Refinement{
			refinementsets.AtLeast(1000),
			refinementsets.Below(1151),
			refinementsets.Integer,
		},
	}
	shifted, exact := ShiftedNarrowed(held, -1000)
	if !exact {
		t.Fatalf("an integral shift of an integral window was refused")
	}
	if len(shifted.Forms) != 3 {
		t.Fatalf("forms = %v, want three", shifted.Forms)
	}
	if shifted.Forms[0].Form != refinementsets.FormAtLeast || shifted.Forms[0].A != 0 {
		t.Errorf("lower edge = %+v, want atLeast 0", shifted.Forms[0])
	}
	if shifted.Forms[1].Form != refinementsets.FormBelow || shifted.Forms[1].A != 151 {
		t.Errorf("upper edge = %+v, want below 151", shifted.Forms[1])
	}
	if shifted.Forms[2].Form != refinementsets.FormInteger {
		t.Errorf("third form = %+v, want integer", shifted.Forms[2])
	}
}

// TestA6_guard_band_AFractionalShiftDropsTheIntegerClaim pins the one
// form whose truth the displacement can destroy: an integer plus a
// fraction is not an integer, so the whole narrowing is refused rather
// than carried with a false conjunct.
func TestA6_guard_band_AFractionalShiftDropsTheIntegerClaim(t *testing.T) {
	held := Narrowed{Binding: "off", Forms: []refinementsets.Refinement{refinementsets.Integer}}
	if _, exact := ShiftedNarrowed(held, 0.5); exact {
		t.Errorf("a fractional shift carried an integer claim")
	}
}

// TestA6_guard_band_AnInexactEndpointRefusesTheWholeNarrowing pins the
// soundness gate: where the double addition rounds, the moved endpoint
// names a different number than the run produces, so the displacement
// states nothing at all.
func TestA6_guard_band_AnInexactEndpointRefusesTheWholeNarrowing(t *testing.T) {
	// 2^53 + 1 is not representable: adding 1 to 2^53 rounds back to 2^53,
	// so the round trip cannot return the original endpoint
	held := Narrowed{Binding: "off", Forms: []refinementsets.Refinement{refinementsets.AtLeast(1)}}
	if _, exact := ShiftedNarrowed(held, math.Pow(2, 53)); exact {
		t.Errorf("a rounding endpoint was carried as exact")
	}
}

// TestA6_guard_band_ANonSetClaimNeverDisplaces pins the refusal side:
// definedness, truthiness, shapes and words say nothing about a shifted
// number, so none of them is carried.
func TestA6_guard_band_ANonSetClaimNeverDisplaces(t *testing.T) {
	for name, held := range map[string]Narrowed{
		"definedness": {Binding: "off", Definedness: "defined"},
		"truthiness":  {Binding: "off", Truthiness: "truthy"},
		"kind":        {Binding: "off", ExcludesKind: "string"},
	} {
		if _, exact := ShiftedNarrowed(held, 10); exact {
			t.Errorf("%s was carried across a shift", name)
		}
	}
}

// TestA6_guard_band_AZeroShiftIsThePlainCopy pins that the displacement
// path costs the existing value-copy channel nothing: shift 0 is the
// narrowing itself, unchanged.
func TestA6_guard_band_AZeroShiftIsThePlainCopy(t *testing.T) {
	held := Narrowed{Binding: "off", Definedness: "defined", Truthiness: "truthy"}
	shifted, exact := ShiftedNarrowed(held, 0)
	if !exact {
		t.Fatalf("a zero shift was refused")
	}
	if shifted.Definedness != "defined" || shifted.Truthiness != "truthy" {
		t.Errorf("a zero shift altered the narrowing: %+v", shifted)
	}
}
