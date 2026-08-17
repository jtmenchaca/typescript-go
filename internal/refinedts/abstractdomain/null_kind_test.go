// KindNull's own behavior, mirroring KindUndef's pattern per the domain
// half of the Lean kernel's AbsentMark split: KindUndef comes to mean
// exactly-undefined, KindNull exactly-null. This file pins the pieces
// that are NEW rather than shared with KindUndef's existing coverage:
// the kind itself, the join with its own kind, the join with KindUndef
// (which must not collapse to either exact flavor), the "null" spelling
// in both the hover formatter and the memo cache, and that the memo
// spellings of Null and Undef never collide.

package abstractdomain

import "testing"

// TestNullKind pins the bare constructor: Null carries the KindNull tag
// and nothing else, the same shape Undef carries for KindUndef.
func TestNullKind(t *testing.T) {
	if Null.Kind != KindNull {
		t.Errorf("Null.Kind = %v, want KindNull", Null.Kind)
	}
}

// TestJoinKnownNullWithNullStaysNull pins JoinKnown's SameKnown fast
// path: two agreeing Null values join to Null exactly, not a wrapper.
func TestJoinKnownNullWithNullStaysNull(t *testing.T) {
	joined := JoinKnown(Null, Null)
	if joined.Kind != KindNull {
		t.Errorf("JoinKnown(Null, Null).Kind = %v, want KindNull", joined.Kind)
	}
}

// TestJoinKnownNullWithUndefIsNeitherExactFlavor is the soundness pin
// step 3 of the split calls for directly: a branch that returns null and
// a branch that returns undefined join to a value that may be either at
// runtime, so the join must answer neither KindNull nor KindUndef
// outright — claiming one flavor for a value that may be the other
// would be an unsound narrowing.
func TestJoinKnownNullWithUndefIsNeitherExactFlavor(t *testing.T) {
	forward := JoinKnown(Null, Undef)
	if forward.Kind == KindNull || forward.Kind == KindUndef {
		t.Errorf("JoinKnown(Null, Undef).Kind = %v, want neither KindNull nor KindUndef", forward.Kind)
	}
	backward := JoinKnown(Undef, Null)
	if backward.Kind == KindNull || backward.Kind == KindUndef {
		t.Errorf("JoinKnown(Undef, Null).Kind = %v, want neither KindNull nor KindUndef", backward.Kind)
	}
}

// TestFormatAbstractValueNull pins the value-specific rendering: null
// prints as "null" (never "absent", which stays KindUndef's own word).
func TestFormatAbstractValueNull(t *testing.T) {
	shown, ok := FormatAbstractValue(Null)
	if !ok {
		t.Fatalf("FormatAbstractValue(Null) refused, want a rendering")
	}
	if shown != "{null}" {
		t.Errorf(`FormatAbstractValue(Null) = %q, want "{null}" (braced at top, the same convention KindUndef's "{absent}" follows)`, shown)
	}
	inline, ok := FormatAbstractValueInline(Null)
	if !ok {
		t.Fatalf("FormatAbstractValueInline(Null) refused, want a rendering")
	}
	if inline != "null" {
		t.Errorf(`FormatAbstractValueInline(Null) = %q, want "null"`, inline)
	}
}

// TestMemoSpellNullAndUndefDiffer pins the memo-cache requirement: Null
// and Undef must spell to distinct keys, or a cache built while walking
// a null-typed body could serve its answer to an undefined-typed one.
func TestMemoSpellNullAndUndefDiffer(t *testing.T) {
	nullKey, ok := SpellForMemoKey(Null)
	if !ok {
		t.Fatalf("SpellForMemoKey(Null) refused, want a spelling")
	}
	undefKey, ok := SpellForMemoKey(Undef)
	if !ok {
		t.Fatalf("SpellForMemoKey(Undef) refused, want a spelling")
	}
	if nullKey == undefKey {
		t.Errorf("SpellForMemoKey(Null) and SpellForMemoKey(Undef) both = %q, want distinct spellings", nullKey)
	}
}

// TestTruthinessNull pins ToBoolean(null) = false (sec-toboolean): null
// is one of the seven falsy values, decided the same way KindUndef's
// own Truthiness arm already is.
func TestTruthinessNull(t *testing.T) {
	value, known := Truthiness(Null)
	if !known {
		t.Fatalf("Truthiness(Null) = (_, false), want known")
	}
	if value {
		t.Errorf("Truthiness(Null) = (true, _), want false")
	}
}

// TestTypeofWordOfKnownNull pins typeof null = "object" (sec-typeof-
// operator's historical quirk) — unlike KindUndef, which answers no
// single word because it conflates two flavors, KindNull is exactly one
// flavor and so pins one word.
func TestTypeofWordOfKnownNull(t *testing.T) {
	word := TypeofWordOfKnown(Null)
	if word != "object" {
		t.Errorf(`TypeofWordOfKnown(Null) = %q, want "object"`, word)
	}
}

// TestTypeofPluralNullIsFalse pins the contrast with KindUndef directly:
// KindUndef answers plural (true) because it conflates "undefined" and
// "object" under one marker; KindNull, now split out, pins the single
// word "object" and so must NOT read as plural.
func TestTypeofPluralNullIsFalse(t *testing.T) {
	if TypeofPlural(Null) {
		t.Errorf("TypeofPlural(Null) = true, want false — null pins exactly one typeof word")
	}
}
