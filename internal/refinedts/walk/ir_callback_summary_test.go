// Arrow callbacks, closure-converted: the free-name scan and its
// capture layout, the decline rules, and the collection statements a
// converted callback lowers to.
//
// The scan and the capture resolution read syntax and the caller's slot
// vector alone, so those probe without a kernel. The statement shapes
// compile the arrow's blob, which the kernel writes — those gate on the
// dylib the same way every other walk test here does, and are skipped
// rather than faked when it is absent.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// callbackArrowOf parses `xs.m(cb)` as an expression statement and
// answers the callback argument — the arrow the scan reads.
func callbackArrowOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := loweringParse(t, source)
	if len(statements) != 1 {
		t.Fatalf("len(statements) = %d, want 1", len(statements))
	}
	call, ok := collectionCallOf(Unwrapped(statements[0].AsExpressionStatement().Expression))
	if !ok {
		t.Fatalf("collectionCallOf(%q) declined", source)
	}
	arrow := arrowFunctionOf(call.Callback)
	if arrow == nil {
		t.Fatalf("the callback of %q is not an arrow", source)
	}
	return arrow
}

// callbackContext is the caller layout the conversion tests share: a
// flattened source array, a flattened target array, and whatever
// scalars the case captures.
func callbackContext(bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings:     bindings,
		Sorts:        sorts,
		Typeofs:      make([]TypeofTag, len(bindings)),
		SummaryTable: &SummaryTableBuilder{},
	}
}

func TestCallbackScan_AnArrowReadingOnlyItsParameterIsFreeOfCaptures(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + 1);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames(x => x + 1).Ok = false, want true")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none — x is the arrow's own parameter", scan.Reads)
	}
}

func TestCallbackScan_AReadCaptureBecomesOneEntryAfterTheParameters(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + factor);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames(x => x + factor).Ok = false, want true")
	}
	if len(scan.Reads) != 1 || scan.Reads[0] != "factor" {
		t.Fatalf("free reads = %v, want [factor]", scan.Reads)
	}
	context := callbackContext(
		[]string{"xs.len", "xs.elem", "factor"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	captures, slots, ok := capturesOf(context, scan)
	if !ok {
		t.Fatalf("capturesOf(factor) ok = false, want true — factor has a slot")
	}
	if len(captures) != 1 || captures[0].Name != "factor" {
		t.Fatalf("captures = %v, want one named factor", captures)
	}
	if captures[0].Sort != BindingKindNumber {
		t.Errorf("capture sort = %q, want %q — a capture wears its caller slot's sort", captures[0].Sort, BindingKindNumber)
	}
	if len(slots) != 1 || slots[0] != 2 {
		t.Errorf("capture slots = %v, want [2] — the caller slot factor resolves to", slots)
	}
}

func TestCallbackScan_TheCaptureOrderIsSourceOrderOfFirstRead(t *testing.T) {
	// the layout the call site fills depends on this order being the
	// scan's own report, not a map's iteration
	arrow := callbackArrowOf(t, `xs.map(x => x * scale + offset - scale);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames ok = false, want true")
	}
	if len(scan.Reads) != 2 || scan.Reads[0] != "scale" || scan.Reads[1] != "offset" {
		t.Errorf("free reads = %v, want [scale offset] — first-read order, each name once", scan.Reads)
	}
}

func TestCallbackScan_AWrittenCaptureDeclinesTheArrow(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.forEach(x => { total += x; });`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow writing the captured `total` converted — a capture is READ-ONLY")
	}
}

func TestCallbackScan_AStepOnACapturedNameDeclinesTheArrow(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.forEach(x => { count++; });`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow stepping the captured `count` converted — a capture is READ-ONLY")
	}
}

func TestCallbackScan_AWriteToTheArrowsOwnLocalIsNotACapturedWrite(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => { let y = x; y = y + 1; return y; });`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("an arrow writing its OWN local declined — y is bound inside")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none", scan.Reads)
	}
}

func TestCallbackScan_ANestedFunctionInsideTheArrowDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => (y => y + x)(x));`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("an arrow holding a nested function converted — its own captures are unconverted")
	}
}

func TestCallbackScan_AThisRootedCallContributesNoFreeName(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => this.scale(x));`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("a `this`-rooted method call declined the arrow")
	}
	if len(scan.Reads) != 0 {
		t.Errorf("free reads = %v, want none — the receiver is consumed by the resolution", scan.Reads)
	}
}

func TestCallbackScan_AThisReadAsAValueDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + this.factor);`)
	if scanFreeNames(arrow).Ok {
		t.Errorf("`this` read as a value converted — no slot holds a whole object")
	}
}

func TestCallbackCaptures_AFreeNameWithNoSlotDeclines(t *testing.T) {
	arrow := callbackArrowOf(t, `xs.map(x => x + imported);`)
	scan := scanFreeNames(arrow)
	if !scan.Ok {
		t.Fatalf("scanFreeNames ok = false, want true — the scan itself admits the read")
	}
	context := callbackContext(
		[]string{"xs.len", "xs.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, _, ok := capturesOf(context, scan); ok {
		t.Errorf("a capture with no caller slot converted — there is no var to bind its entry to")
	}
}
