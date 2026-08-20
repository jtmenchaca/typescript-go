// Kernel-backed word operations landed alongside string ordering
// (cmp.7): containment (str.15-17, SeqStartsWith/SeqEndsWith/
// SeqIncludes) and StringToNumber (conv.2, TransferOpStringToNumber
// via ReadUnary's `+x`). Containment already decides natively in Go
// (string_method_models.go's readStringMethods, strings.HasPrefix/
// HasSuffix/Contains) for every concrete receiver+needle pair, so
// these tests pin the NEW kernel path directly — the same word-set
// operands CompareKnown's ordering rows build — rather than routing
// through the unchanged native containment model.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestKernelSeqStartsWithTrueAndFalse pins str.15 through the new
// kernel ask directly: "hello".startsWith("he") is true,
// "hello".startsWith("lo") is false.
func TestKernelSeqStartsWithTrueAndFalse(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	hello := stringOfCompareKnown("hello")
	he, lo := stringOfCompareKnown("he"), stringOfCompareKnown("lo")
	helloSet, ok := abstractdomain.SetOfKnown(hello)
	if !ok {
		t.Fatalf("SetOfKnown(hello) refused")
	}
	heSet, _ := abstractdomain.SetOfKnown(he)
	loSet, _ := abstractdomain.SetOfKnown(lo)
	if !kernel.SeqStartsWith(helloSet, heSet) {
		t.Errorf(`"hello".startsWith("he") = false, want true`)
	}
	if kernel.SeqStartsWith(helloSet, loSet) {
		t.Errorf(`"hello".startsWith("lo") = true, want false`)
	}
}

// TestKernelSeqEndsWithTrueAndFalse pins str.16: "hello".endsWith("lo")
// is true, "hello".endsWith("he") is false.
func TestKernelSeqEndsWithTrueAndFalse(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	hello := stringOfCompareKnown("hello")
	he, lo := stringOfCompareKnown("he"), stringOfCompareKnown("lo")
	helloSet, _ := abstractdomain.SetOfKnown(hello)
	heSet, _ := abstractdomain.SetOfKnown(he)
	loSet, _ := abstractdomain.SetOfKnown(lo)
	if !kernel.SeqEndsWith(helloSet, loSet) {
		t.Errorf(`"hello".endsWith("lo") = false, want true`)
	}
	if kernel.SeqEndsWith(helloSet, heSet) {
		t.Errorf(`"hello".endsWith("he") = true, want false`)
	}
}

// TestKernelSeqIncludesTrueAndFalse pins str.17: "hello".includes("ell")
// is true, "hello".includes("xyz") is false.
func TestKernelSeqIncludesTrueAndFalse(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	hello := stringOfCompareKnown("hello")
	ell, xyz := stringOfCompareKnown("ell"), stringOfCompareKnown("xyz")
	helloSet, _ := abstractdomain.SetOfKnown(hello)
	ellSet, _ := abstractdomain.SetOfKnown(ell)
	xyzSet, _ := abstractdomain.SetOfKnown(xyz)
	if !kernel.SeqIncludes(helloSet, ellSet) {
		t.Errorf(`"hello".includes("ell") = false, want true`)
	}
	if kernel.SeqIncludes(helloSet, xyzSet) {
		t.Errorf(`"hello".includes("xyz") = true, want false`)
	}
}

// TestStringToNumberOfKnownDecidesFive pins conv.2's headline case
// through ReadUnary's new route: `+"5"` decides to the known number 5
// (was UnknownOver before this unit — evaluate_operators.go's `+x`
// declined every string operand).
func TestStringToNumberOfKnownDecidesFive(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	five := stringOfCompareKnown("5")
	got, ok := stringToNumberOfKnown(ctx, five)
	if !ok {
		t.Fatalf(`+"5" declined, want a decided number`)
	}
	if got.Kind != abstractdomain.KindValues || got.KindTag != abstractdomain.PrimitiveNumber || len(got.Values) != 1 {
		t.Fatalf(`+"5" = %+v, want a decided number`, got)
	}
	if got.Values[0] != 5 {
		t.Errorf(`+"5" = %v, want 5`, got.Values[0])
	}
}

// TestStringToNumberOfKnownUnparseableIsNaN pins conv.2's grammar
// exclusion: a numeric separator makes the literal not a
// |StrNumericLiteral| at all, so `+"1_2"` is NaN — the same outcome an
// unrecognizable literal like `+"abc"` gets. StringToNumber is total;
// this is a decided NaN, not a decline.
func TestStringToNumberOfKnownUnparseableIsNaN(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	underscored := stringOfCompareKnown("1_2")
	got, ok := stringToNumberOfKnown(ctx, underscored)
	if !ok {
		t.Fatalf(`+"1_2" declined, want a decided NaN`)
	}
	if got.Kind != abstractdomain.KindNaN {
		t.Errorf(`+"1_2" = %+v, want KindNaN`, got)
	}
}
