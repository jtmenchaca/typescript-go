package narrowing

import "testing"

// A shape-only narrowing (HasShape set, Forms empty) is exactly the
// `typeof x === "number"` case that motivated this file: the true arm
// carries no Forms of its own, only the proven ground. Before the
// HasShape clause existed, spellNarrowed ignored Shape entirely and
// this printed "no set" even though a real shape rode the claim.
func TestSpellNarrowedPrintsShape(t *testing.T) {
	ground, ok := GroundOfTypeofWord("number")
	if !ok {
		t.Fatal("GroundOfTypeofWord(\"number\") returned ok=false")
	}
	n := Narrowed{
		Binding:  "first",
		Shape:    ground,
		HasShape: true,
	}
	spelled := spellNarrowed(n)
	if spelled == "no set" {
		t.Fatalf("spellNarrowed dropped a HasShape narrowing's shape, printed %q", spelled)
	}
	want := "shape >= −∞, or NaN"
	if spelled != want {
		t.Errorf("spellNarrowed(shape-only narrowing) = %q, want %q", spelled, want)
	}
}

// A Refuting narrowing applies only to values already known real, and
// that changes what the claim means — the trace names the difference
// rather than printing identical text for a strong and a weak claim
// that share the same Forms.
func TestSpellNarrowedMarksWeakClaim(t *testing.T) {
	strong := Narrowed{Binding: "x", Truthiness: "truthy"}
	weak := Narrowed{Binding: "x", Truthiness: "truthy", Refuting: true}
	strongSpelled := spellNarrowed(strong)
	weakSpelled := spellNarrowed(weak)
	if strongSpelled == weakSpelled {
		t.Errorf("spellNarrowed(strong) = spellNarrowed(weak) = %q, want distinct text for Refuting", strongSpelled)
	}
}
