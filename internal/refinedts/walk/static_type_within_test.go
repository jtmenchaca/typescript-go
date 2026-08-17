// Pins for the static-type-within escape (static_type_within.go):
// an UNCAST argument whose own static type is the parameter's stated
// union is determined by tsc's shape check and stays silent — and a
// CAST argument earns nothing from the cast's claim, so the smuggled
// string still refutes. The two fixtures mirror recharts'
// ErrorBar.tsx:289 and Sankey.tsx:530.
package walk

import (
	"testing"
)

// TestStaticTypeWithin_SameUnionArgumentStaysSilent pins the ErrorBar
// shape: the argument's static type IS the stated union (with the
// maybe wrap on both sides), so no alert fires at the position.
func TestStaticTypeWithin_SameUnionArgumentStaysSilent(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		type Direction = 'x' | 'y';
		function useDirection(d: Direction | undefined): Direction {
			if (d != null) {
				return d;
			}
			return 'x';
		}
		function component(outside: { direction: Direction | undefined }): Direction {
			const real: Direction = useDirection(outside.direction);
			return real;
		}
	`, "component")
	for _, d := range diagnostics {
		if d.Code == 7002 {
			t.Errorf("component fired 7002 (%s) — the argument's own static type is the stated union, tsc's proof", d.MessageText)
		}
	}
}

// TestStaticTypeWithin_ACastEarnsNothing pins the Sankey shape: a
// string cast into the union position must NOT ride the escape — the
// `as` suppressed exactly the membership check the escape's premise
// rests on, and the string still refutes.
func TestStaticTypeWithin_ACastEarnsNothing(t *testing.T) {
	diagnostics := wornCallDiagnostics(t, `
		type Kind = 'node' | 'link';
		function take(kind: Kind): Kind {
			return kind;
		}
		function smuggle(raw: string): Kind {
			return take(raw as Kind);
		}
	`, "smuggle")
	fired := false
	for _, d := range diagnostics {
		if d.Code == 7001 {
			fired = true
		}
	}
	if !fired {
		t.Errorf("smuggle fired no 7001 — the cast-stripped string must still refute the 'node' | 'link' position")
	}
}
