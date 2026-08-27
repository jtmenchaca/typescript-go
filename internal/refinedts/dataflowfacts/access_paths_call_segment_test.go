package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestA6_guard_lt_ADateReaderCallNamesAPlace pins what lets a guard on
// `d.getTime()` reach a LATER read of the same spelling: the call is a
// place, rooted at the receiver, with the method spelled as a call
// segment. Without this the two occurrences were unrelated expressions
// and each answered the bare spec window again.
func TestA6_guard_lt_ADateReaderCallNamesAPlace(t *testing.T) {
	c, file := checkerFor(t, `
declare const d: Date;
declare const s: string;
if (d.getTime() < 5) {}
if (d.getUTCFullYear() < 5) {}
if (d.getTimezoneOffset() < 5) {}
if (d.setHours(1) < 5) {}
if (s.charCodeAt(0) < 5) {}
`)
	conditions := ifConditions(file)
	anyName := func(string) bool { return true }
	placeOf := func(condition *ast.Node) *TrackedPlace {
		return TrackedPlaceOfWith(c, condition.AsBinaryExpression().Left, anyName)
	}

	for _, row := range []struct {
		index   int
		segment string
	}{
		{0, "getTime()"},
		{1, "getUTCFullYear()"},
	} {
		place := placeOf(conditions[row.index])
		if place == nil {
			t.Fatalf("condition %d: a pure Date reader named no place", row.index)
		}
		if place.Binding != "d" {
			t.Errorf("condition %d: binding = %q, want %q", row.index, place.Binding, "d")
		}
		if len(place.Path) != 1 || place.Path[0] != row.segment {
			t.Errorf("condition %d: path = %v, want [%s]", row.index, place.Path, row.segment)
		}
	}

	// getTimezoneOffset is not a pure [[DateValue]] reader in this table,
	// a setX MOVES the slot, and a non-Date receiver's method is not on
	// the table at all — none of the three names a place.
	for index, why := range map[int]string{
		2: "getTimezoneOffset is off the pure-reader table",
		3: "a setX mutator moves the slot",
		4: "a string method is not a Date reader",
	} {
		if place := placeOf(conditions[index]); place != nil {
			t.Errorf("condition %d named place %+v — %s", index, place, why)
		}
	}
}

// TestA6_guard_lt_CallSegmentsRoundTripAndNeverCollideWithKeys pins the
// spelling rule the read side depends on: a call segment reads back as
// the method it runs, and no key or index segment is ever mistaken for
// one (property names carry no parentheses).
func TestA6_guard_lt_CallSegmentsRoundTripAndNeverCollideWithKeys(t *testing.T) {
	for _, method := range []string{"getTime", "valueOf", "getUTCFullYear"} {
		segment := CallSegment(method)
		readBack, isCall := CallSegmentOf(segment)
		if !isCall || readBack != method {
			t.Errorf("CallSegmentOf(%q) = (%q, %v), want (%q, true)", segment, readBack, isCall, method)
		}
	}
	for _, notACall := range []string{"k", "total", "[0]", "", "()", "get()x"} {
		if method, isCall := CallSegmentOf(notACall); isCall {
			t.Errorf("CallSegmentOf(%q) read %q as a call segment", notACall, method)
		}
	}
}
