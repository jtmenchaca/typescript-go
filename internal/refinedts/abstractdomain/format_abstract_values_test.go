// FormatAbstractValue's own hover-word choice for the maybe wrapper's
// absent side. The conflated flavor (the zero value, every wrapper
// built before AbsentSide existed) keeps the original "or absent"
// spelling — no existing pinned fixture moves. A flavored wrapper
// speaks its own flavor: UndefOnly says "or undefined", NullOnly says
// "or null".

package abstractdomain

import "testing"

func fiveOrAbsentFlavored(flavor AbsentFlavor) AbstractValue {
	return PossiblyAbsent(KnownValues([]float64{5}, PrimitiveNumber, TrustProved), flavor, "", false, false)
}

// TestFormatAbstractValueConflatedWrapperStillReadsOrAbsent pins the
// unchanged case: AbsentFlavorConflated keeps the exact "or absent"
// spelling at both positions.
func TestFormatAbstractValueConflatedWrapperStillReadsOrAbsent(t *testing.T) {
	wrapper := fiveOrAbsentFlavored(AbsentFlavorConflated)
	top, ok := FormatAbstractValue(wrapper)
	if !ok {
		t.Fatalf("FormatAbstractValue(conflated) refused, want a rendering")
	}
	if top != "{5, or absent}" {
		t.Errorf(`FormatAbstractValue(conflated) = %q, want "{5, or absent}"`, top)
	}
	inline := inlineOrUnknown(wrapper)
	if inline != "5, or absent" {
		t.Errorf(`FormatAbstractValueInline(conflated) = %q, want "5, or absent"`, inline)
	}
}

// TestFormatAbstractValueUndefOnlyWrapperReadsOrUndefined pins the
// UndefOnly spelling at both positions.
func TestFormatAbstractValueUndefOnlyWrapperReadsOrUndefined(t *testing.T) {
	wrapper := fiveOrAbsentFlavored(AbsentFlavorUndefOnly)
	top, ok := FormatAbstractValue(wrapper)
	if !ok {
		t.Fatalf("FormatAbstractValue(UndefOnly) refused, want a rendering")
	}
	if top != "{5, or undefined}" {
		t.Errorf(`FormatAbstractValue(UndefOnly) = %q, want "{5, or undefined}"`, top)
	}
	inline := inlineOrUnknown(wrapper)
	if inline != "5, or undefined" {
		t.Errorf(`FormatAbstractValueInline(UndefOnly) = %q, want "5, or undefined"`, inline)
	}
}

// TestFormatAbstractValueNullOnlyWrapperReadsOrNull pins the NullOnly
// spelling at both positions.
func TestFormatAbstractValueNullOnlyWrapperReadsOrNull(t *testing.T) {
	wrapper := fiveOrAbsentFlavored(AbsentFlavorNullOnly)
	top, ok := FormatAbstractValue(wrapper)
	if !ok {
		t.Fatalf("FormatAbstractValue(NullOnly) refused, want a rendering")
	}
	if top != "{5, or null}" {
		t.Errorf(`FormatAbstractValue(NullOnly) = %q, want "{5, or null}"`, top)
	}
	inline := inlineOrUnknown(wrapper)
	if inline != "5, or null" {
		t.Errorf(`FormatAbstractValueInline(NullOnly) = %q, want "5, or null"`, inline)
	}
}
