// AbsentFlavor's own behavior: the wrapper's absent side split into
// UndefOnly / NullOnly / conflated (the zero value). This file pins
// PossiblyAbsent's normalization rules directly (wrapping Undef always
// collapses, wrapping Null with an undef-admitting flavor is the
// existing Inner=Null wrapper, a NullOnly wrapper around Null collapses
// to Null itself), plus the pieces JoinKnown/SameKnown/memo_spell must
// carry now that the field exists — zero-value default behavior is
// pinned as identical to the pre-flavor wrapper everywhere it applies.

package abstractdomain

import "testing"

func fiveOrAbsent(flavor AbsentFlavor) AbstractValue {
	return PossiblyAbsent(KnownValues([]float64{5}, PrimitiveNumber, TrustProved), flavor, "", false, false)
}

// TestPossiblyAbsentZeroValueMatchesPossiblyUndefined pins the
// delegation: PossiblyAbsent with AbsentFlavorConflated (the zero
// value) builds EXACTLY what PossiblyUndefined already builds — every
// existing call site (none of which knows about flavors) is unaffected.
func TestPossiblyAbsentZeroValueMatchesPossiblyUndefined(t *testing.T) {
	inner := KnownValues([]float64{5}, PrimitiveNumber, TrustProved)
	viaAbsent := PossiblyAbsent(inner, AbsentFlavorConflated, "", false, false)
	viaUndefined := PossiblyUndefined(inner, "", false, false)
	if !SameKnown(viaAbsent, viaUndefined) {
		t.Errorf("PossiblyAbsent(_, AbsentFlavorConflated, ...) != PossiblyUndefined(...): %+v vs %+v", viaAbsent, viaUndefined)
	}
	if viaAbsent.AbsentSide != AbsentFlavorConflated {
		t.Errorf("viaAbsent.AbsentSide = %v, want AbsentFlavorConflated (the zero value)", viaAbsent.AbsentSide)
	}
}

// TestPossiblyAbsentWrappingUndefAlwaysCollapses pins the first
// normalization rule regardless of flavor: PossiblyAbsent(Undef, _)
// is always the bare Undef value — there is nothing left for a flavor
// to say once the inner value is already exactly-undefined.
func TestPossiblyAbsentWrappingUndefAlwaysCollapses(t *testing.T) {
	for _, flavor := range []AbsentFlavor{AbsentFlavorConflated, AbsentFlavorUndefOnly, AbsentFlavorNullOnly} {
		got := PossiblyAbsent(Undef, flavor, "", false, false)
		if got.Kind != KindUndef {
			t.Errorf("PossiblyAbsent(Undef, %v, ...).Kind = %v, want KindUndef", flavor, got.Kind)
		}
	}
}

// TestPossiblyAbsentWrappingNullWithUndefAdmittingFlavorIsInnerNull
// pins the second normalization rule: conflated or UndefOnly around
// Null is the existing Inner=Null wrapper (which already states "null
// or undefined" through Inner alone) — the wrapper's OWN AbsentSide
// carries nothing further and reads back conflated.
func TestPossiblyAbsentWrappingNullWithUndefAdmittingFlavorIsInnerNull(t *testing.T) {
	for _, flavor := range []AbsentFlavor{AbsentFlavorConflated, AbsentFlavorUndefOnly} {
		got := PossiblyAbsent(Null, flavor, "", false, false)
		if got.Kind != KindPossiblyUndefined {
			t.Fatalf("PossiblyAbsent(Null, %v, ...).Kind = %v, want KindPossiblyUndefined", flavor, got.Kind)
		}
		if got.Inner == nil || got.Inner.Kind != KindNull {
			t.Errorf("PossiblyAbsent(Null, %v, ...).Inner = %+v, want KindNull", flavor, got.Inner)
		}
		if got.AbsentSide != AbsentFlavorConflated {
			t.Errorf("PossiblyAbsent(Null, %v, ...).AbsentSide = %v, want AbsentFlavorConflated", flavor, got.AbsentSide)
		}
	}
}

// TestPossiblyAbsentWrappingNullWithNullOnlyCollapsesToNull pins the
// third normalization rule: a NullOnly-flavored wrapper around Null
// would claim "null, or null" — exactly Null itself, so it collapses
// rather than building a redundant wrapper.
func TestPossiblyAbsentWrappingNullWithNullOnlyCollapsesToNull(t *testing.T) {
	got := PossiblyAbsent(Null, AbsentFlavorNullOnly, "", false, false)
	if got.Kind != KindNull {
		t.Errorf("PossiblyAbsent(Null, AbsentFlavorNullOnly, ...).Kind = %v, want KindNull", got.Kind)
	}
}

// TestPossiblyAbsentOverAPresentValueCarriesItsFlavor pins the ordinary
// case both StateOfKnown/KnownOfState and the join lattice depend on: a
// flavor stamped over a genuinely present inner value survives on the
// built wrapper's AbsentSide field untouched.
func TestPossiblyAbsentOverAPresentValueCarriesItsFlavor(t *testing.T) {
	undefOnly := fiveOrAbsent(AbsentFlavorUndefOnly)
	if undefOnly.Kind != KindPossiblyUndefined || undefOnly.AbsentSide != AbsentFlavorUndefOnly {
		t.Errorf("fiveOrAbsent(UndefOnly) = %+v, want KindPossiblyUndefined/AbsentFlavorUndefOnly", undefOnly)
	}
	nullOnly := fiveOrAbsent(AbsentFlavorNullOnly)
	if nullOnly.Kind != KindPossiblyUndefined || nullOnly.AbsentSide != AbsentFlavorNullOnly {
		t.Errorf("fiveOrAbsent(NullOnly) = %+v, want KindPossiblyUndefined/AbsentFlavorNullOnly", nullOnly)
	}
}

// TestSameKnownDistinguishesAbsentSide pins SameKnown's own gate: two
// wrappers alike but for AbsentSide are not the same knowledge (a
// NullOnly wrapper admits a different runtime set than an UndefOnly
// one), while two wrappers with the same flavor (including two
// conflated ones, the pre-existing case) still compare equal.
func TestSameKnownDistinguishesAbsentSide(t *testing.T) {
	undefOnly := fiveOrAbsent(AbsentFlavorUndefOnly)
	nullOnly := fiveOrAbsent(AbsentFlavorNullOnly)
	conflated := fiveOrAbsent(AbsentFlavorConflated)
	if SameKnown(undefOnly, nullOnly) {
		t.Errorf("SameKnown(UndefOnly, NullOnly) = true, want false")
	}
	if SameKnown(undefOnly, conflated) {
		t.Errorf("SameKnown(UndefOnly, conflated) = true, want false")
	}
	if !SameKnown(conflated, fiveOrAbsent(AbsentFlavorConflated)) {
		t.Errorf("SameKnown(conflated, conflated) = false, want true")
	}
}

// TestJoinKnownFlavorLattice pins joinAbsentFlavor's three rules
// through the public JoinKnown surface: same flavor stays that flavor,
// UndefOnly meets NullOnly as conflated, and a flavor joined with
// conflated stays conflated.
func TestJoinKnownFlavorLattice(t *testing.T) {
	sameFlavor := JoinKnown(fiveOrAbsent(AbsentFlavorUndefOnly), fiveOrAbsent(AbsentFlavorUndefOnly))
	if sameFlavor.AbsentSide != AbsentFlavorUndefOnly {
		t.Errorf("JoinKnown(UndefOnly, UndefOnly).AbsentSide = %v, want AbsentFlavorUndefOnly", sameFlavor.AbsentSide)
	}
	crossFlavor := JoinKnown(fiveOrAbsent(AbsentFlavorUndefOnly), fiveOrAbsent(AbsentFlavorNullOnly))
	if crossFlavor.AbsentSide != AbsentFlavorConflated {
		t.Errorf("JoinKnown(UndefOnly, NullOnly).AbsentSide = %v, want AbsentFlavorConflated", crossFlavor.AbsentSide)
	}
	withConflated := JoinKnown(fiveOrAbsent(AbsentFlavorUndefOnly), fiveOrAbsent(AbsentFlavorConflated))
	if withConflated.AbsentSide != AbsentFlavorConflated {
		t.Errorf("JoinKnown(UndefOnly, conflated).AbsentSide = %v, want AbsentFlavorConflated", withConflated.AbsentSide)
	}
}

// TestJoinKnownBareUndefWithPresentValuePinsUndefOnly pins the flavor a
// join derives from scratch (no wrapper on either side yet): a branch
// returning exactly Undef joined with a branch returning a present
// value states UndefOnly, not the pre-flavor conflated default —
// nothing has proved a null admission exists.
func TestJoinKnownBareUndefWithPresentValuePinsUndefOnly(t *testing.T) {
	five := KnownValues([]float64{5}, PrimitiveNumber, TrustProved)
	forward := JoinKnown(Undef, five)
	if forward.Kind != KindPossiblyUndefined || forward.AbsentSide != AbsentFlavorUndefOnly {
		t.Errorf("JoinKnown(Undef, five) = %+v, want KindPossiblyUndefined/AbsentFlavorUndefOnly", forward)
	}
	backward := JoinKnown(five, Undef)
	if backward.Kind != KindPossiblyUndefined || backward.AbsentSide != AbsentFlavorUndefOnly {
		t.Errorf("JoinKnown(five, Undef) = %+v, want KindPossiblyUndefined/AbsentFlavorUndefOnly", backward)
	}
}

// TestJoinKnownBareNullWithPresentValuePinsNullOnly is
// TestJoinKnownBareUndefWithPresentValuePinsUndefOnly's Null twin.
func TestJoinKnownBareNullWithPresentValuePinsNullOnly(t *testing.T) {
	five := KnownValues([]float64{5}, PrimitiveNumber, TrustProved)
	forward := JoinKnown(Null, five)
	if forward.Kind != KindPossiblyUndefined || forward.AbsentSide != AbsentFlavorNullOnly {
		t.Errorf("JoinKnown(Null, five) = %+v, want KindPossiblyUndefined/AbsentFlavorNullOnly", forward)
	}
	backward := JoinKnown(five, Null)
	if backward.Kind != KindPossiblyUndefined || backward.AbsentSide != AbsentFlavorNullOnly {
		t.Errorf("JoinKnown(five, Null) = %+v, want KindPossiblyUndefined/AbsentFlavorNullOnly", backward)
	}
}

// TestMemoSpellDistinguishesFlavors pins the memo cache's own
// requirement (mirrors TestMemoSpellNullAndUndefDiffer's pattern): an
// UndefOnly and a NullOnly wrapper around the same inner value must
// spell to distinct memo keys, or an inline replay cache built while
// walking one flavor could serve its cached answer to the other.
func TestMemoSpellDistinguishesFlavors(t *testing.T) {
	undefKey, ok := SpellForMemoKey(fiveOrAbsent(AbsentFlavorUndefOnly))
	if !ok {
		t.Fatalf("SpellForMemoKey(UndefOnly) refused, want a spelling")
	}
	nullKey, ok := SpellForMemoKey(fiveOrAbsent(AbsentFlavorNullOnly))
	if !ok {
		t.Fatalf("SpellForMemoKey(NullOnly) refused, want a spelling")
	}
	conflatedKey, ok := SpellForMemoKey(fiveOrAbsent(AbsentFlavorConflated))
	if !ok {
		t.Fatalf("SpellForMemoKey(conflated) refused, want a spelling")
	}
	if undefKey == nullKey {
		t.Errorf("SpellForMemoKey(UndefOnly) and SpellForMemoKey(NullOnly) both = %q, want distinct spellings", undefKey)
	}
	if undefKey == conflatedKey {
		t.Errorf("SpellForMemoKey(UndefOnly) and SpellForMemoKey(conflated) both = %q, want distinct spellings", undefKey)
	}
	if nullKey == conflatedKey {
		t.Errorf("SpellForMemoKey(NullOnly) and SpellForMemoKey(conflated) both = %q, want distinct spellings", nullKey)
	}
}

// TestMemoSpellConflatedWrapperUnchanged pins the OTHER side of the
// memo-key change: a conflated wrapper (every wrapper built before this
// field existed) must spell EXACTLY as it did before AbsentSide was
// added — the new field appends "" (AbsentFlavorConflated's own string
// value) between two semicolons that were already there for Grade,
// so an old cache entry for a conflated wrapper still hits.
func TestMemoSpellConflatedWrapperUnchanged(t *testing.T) {
	inner := KnownValues([]float64{5}, PrimitiveNumber, TrustProved)
	key, ok := SpellForMemoKey(PossiblyUndefined(inner, "", false, false))
	if !ok {
		t.Fatalf("SpellForMemoKey(conflated) refused, want a spelling")
	}
	want := "possiblyUndefined(v(5;number;);false;;)"
	if key != want {
		t.Errorf("SpellForMemoKey(conflated) = %q, want %q", key, want)
	}
}
