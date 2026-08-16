// Pins for iterator_spread_values.go's value halves — the AST-free
// cores the drains ride: collectionSpreadItems (what iterating a built
// collection yields), viewItemsOfHeld (what a values()/keys()/entries()
// view drains to), and setAlgebraEntries (the four producers' entry
// lists). The syntax halves and the fixture-visible behavior are pinned
// by the syntax-coverage fixtures (b-body-expressions.ts:141, 670;
// c-reads-and-values.ts:806, 850, 942, 954, 1118).

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func spreadTestNumber(n float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

// spreadTestSet builds a complete Set record over exact number members,
// in the given order.
func spreadTestSet(members ...float64) abstractdomain.AbstractValue {
	entries := make([]abstractdomain.CollectionEntry, 0, len(members))
	for _, m := range members {
		entries = append(entries, abstractdomain.CollectionEntry{Key: spreadTestNumber(m), Value: abstractdomain.Undef})
	}
	return abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorSet,
		Entries:          entries,
		Complete:         true,
	}
}

// spreadTestMap builds a complete Map record over exact number
// (key, value) pairs, in the given order.
func spreadTestMap(pairs ...[2]float64) abstractdomain.AbstractValue {
	entries := make([]abstractdomain.CollectionEntry, 0, len(pairs))
	for _, p := range pairs {
		entries = append(entries, abstractdomain.CollectionEntry{Key: spreadTestNumber(p[0]), Value: spreadTestNumber(p[1])})
	}
	return abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorMap,
		Entries:          entries,
		Complete:         true,
	}
}

func spreadTestWantNumbers(t *testing.T, items []abstractdomain.AbstractValue, want []float64) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("len(items) = %d, want %d", len(items), len(want))
	}
	for i, w := range want {
		if !abstractdomain.SameKnown(items[i], spreadTestNumber(w)) {
			t.Errorf("items[%d] = %v, want the exact number %v", i, items[i], w)
		}
	}
}

func spreadTestWantPair(t *testing.T, item abstractdomain.AbstractValue, key, value float64) {
	t.Helper()
	if item.Kind != abstractdomain.KindList || len(item.Items) != 2 {
		t.Fatalf("item = %v, want a two-slot list", item)
	}
	if !abstractdomain.SameKnown(item.Items[0], spreadTestNumber(key)) {
		t.Errorf("pair[0] = %v, want %v", item.Items[0], key)
	}
	if !abstractdomain.SameKnown(item.Items[1], spreadTestNumber(value)) {
		t.Errorf("pair[1] = %v, want %v", item.Items[1], value)
	}
}

// ── collectionSpreadItems ─────────────────────────────────────────

func TestCollectionSpreadItems_SetMembersOneApieceInEntryOrder(t *testing.T) {
	items, ok := collectionSpreadItems(spreadTestSet(40, 41))
	if !ok {
		t.Fatalf("a complete Set did not drain")
	}
	spreadTestWantNumbers(t, items, []float64{40, 41})
}

func TestCollectionSpreadItems_MapEntriesAreKeyValuePairs(t *testing.T) {
	items, ok := collectionSpreadItems(spreadTestMap([2]float64{1, 10}, [2]float64{2, 20}))
	if !ok {
		t.Fatalf("a complete Map did not drain")
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	spreadTestWantPair(t, items[0], 1, 10)
	spreadTestWantPair(t, items[1], 2, 20)
}

func TestCollectionSpreadItems_EmptyCompleteCollectionDrainsToNothing(t *testing.T) {
	items, ok := collectionSpreadItems(spreadTestSet())
	if !ok {
		t.Fatalf("an empty complete Set did not drain")
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
}

func TestCollectionSpreadItems_IncompleteRecordDeclines(t *testing.T) {
	partial := spreadTestSet(40)
	partial.Complete = false
	if _, ok := collectionSpreadItems(partial); ok {
		t.Fatalf("an incomplete record drained — it cannot speak for every entry the iteration yields")
	}
}

func TestCollectionSpreadItems_NonCollectionDeclines(t *testing.T) {
	if _, ok := collectionSpreadItems(spreadTestNumber(40)); ok {
		t.Fatalf("a non-collection drained")
	}
}

// ── viewItemsOfHeld ───────────────────────────────────────────────

func TestViewItemsOfHeld_ArrayTupleViews(t *testing.T) {
	tuple := abstractdomain.KnownValues([]float64{40, 41}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)

	values, ok := viewItemsOfHeld(tuple, "values")
	if !ok {
		t.Fatalf("values() over an exact tuple did not drain")
	}
	spreadTestWantNumbers(t, values, []float64{40, 41})

	keys, ok := viewItemsOfHeld(tuple, "keys")
	if !ok {
		t.Fatalf("keys() over an exact tuple did not drain")
	}
	spreadTestWantNumbers(t, keys, []float64{0, 1})

	entries, ok := viewItemsOfHeld(tuple, "entries")
	if !ok {
		t.Fatalf("entries() over an exact tuple did not drain")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	spreadTestWantPair(t, entries[0], 0, 40)
	spreadTestWantPair(t, entries[1], 1, 41)
}

func TestViewItemsOfHeld_ListValuesKeepEachItem(t *testing.T) {
	list := abstractdomain.KnownList([]abstractdomain.AbstractValue{
		spreadTestNumber(7), spreadTestNumber(8),
	}, abstractdomain.TrustProved)
	items, ok := viewItemsOfHeld(list, "values")
	if !ok {
		t.Fatalf("values() over a list did not drain")
	}
	spreadTestWantNumbers(t, items, []float64{7, 8})
}

func TestViewItemsOfHeld_SetValuesAndKeysAreBothItsMembers(t *testing.T) {
	set := spreadTestSet(40, 41)
	for _, view := range []string{"values", "keys"} {
		items, ok := viewItemsOfHeld(set, view)
		if !ok {
			t.Fatalf("%s() over a built Set did not drain", view)
		}
		spreadTestWantNumbers(t, items, []float64{40, 41})
	}
	entries, ok := viewItemsOfHeld(set, "entries")
	if !ok {
		t.Fatalf("entries() over a built Set did not drain")
	}
	spreadTestWantPair(t, entries[0], 40, 40)
	spreadTestWantPair(t, entries[1], 41, 41)
}

func TestViewItemsOfHeld_MapViews(t *testing.T) {
	m := spreadTestMap([2]float64{1, 10})
	keys, ok := viewItemsOfHeld(m, "keys")
	if !ok {
		t.Fatalf("keys() over a built Map did not drain")
	}
	spreadTestWantNumbers(t, keys, []float64{1})
	values, ok := viewItemsOfHeld(m, "values")
	if !ok {
		t.Fatalf("values() over a built Map did not drain")
	}
	spreadTestWantNumbers(t, values, []float64{10})
	entries, ok := viewItemsOfHeld(m, "entries")
	if !ok {
		t.Fatalf("entries() over a built Map did not drain")
	}
	spreadTestWantPair(t, entries[0], 1, 10)
}

func TestViewItemsOfHeld_UnansweringShapesDecline(t *testing.T) {
	partial := spreadTestSet(40)
	partial.Complete = false
	if _, ok := viewItemsOfHeld(partial, "values"); ok {
		t.Errorf("an incomplete collection's view drained")
	}
	str := abstractdomain.KnownValues([]float64{97}, abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	if _, ok := viewItemsOfHeld(str, "values"); ok {
		t.Errorf("a string tuple's view drained through the array reading")
	}
	if _, ok := viewItemsOfHeld(abstractdomain.Opaque, "values"); ok {
		t.Errorf("an opaque value's view drained")
	}
}

// ── setAlgebraEntries ─────────────────────────────────────────────

func spreadTestWantKeys(t *testing.T, entries []abstractdomain.CollectionEntry, want []float64) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if !abstractdomain.SameKnown(entries[i].Key, spreadTestNumber(w)) {
			t.Errorf("entries[%d].Key = %v, want %v", i, entries[i].Key, w)
		}
	}
}

// The fixture shape (c-reads-and-values.ts setAlgebra): a = Set([40]),
// b = Set([200]) — the union's entries are the two members in order,
// so [...a.union(b)][0] reads 40 and [...a.union(b)][1] reads 200.
func TestSetAlgebraEntries_UnionOfTheFixtureSets(t *testing.T) {
	entries := setAlgebraEntries("union", spreadTestSet(40).Entries, spreadTestSet(200).Entries)
	spreadTestWantKeys(t, entries, []float64{40, 200})
}

func TestSetAlgebraEntries_UnionKeepsThisOrderThenAppendsOthersNewMembers(t *testing.T) {
	entries := setAlgebraEntries("union", spreadTestSet(40, 41).Entries, spreadTestSet(41, 42).Entries)
	spreadTestWantKeys(t, entries, []float64{40, 41, 42})
}

func TestSetAlgebraEntries_IntersectionFollowsTheClausesSizeSplit(t *testing.T) {
	// |this| <= |other|: this' members that are in other, in this' order
	entries := setAlgebraEntries("intersection", spreadTestSet(3, 2).Entries, spreadTestSet(1, 2, 3).Entries)
	spreadTestWantKeys(t, entries, []float64{3, 2})
	// |this| > |other|: other's members that are in this, in other's order
	entries = setAlgebraEntries("intersection", spreadTestSet(1, 2, 3).Entries, spreadTestSet(3, 2).Entries)
	spreadTestWantKeys(t, entries, []float64{3, 2})
}

func TestSetAlgebraEntries_DifferenceKeepsThisMembersNotInOther(t *testing.T) {
	entries := setAlgebraEntries("difference", spreadTestSet(1, 2, 3).Entries, spreadTestSet(2).Entries)
	spreadTestWantKeys(t, entries, []float64{1, 3})
}

func TestSetAlgebraEntries_SymmetricDifferenceKeepsBothExclusiveSides(t *testing.T) {
	entries := setAlgebraEntries("symmetricDifference", spreadTestSet(1, 2).Entries, spreadTestSet(2, 3).Entries)
	spreadTestWantKeys(t, entries, []float64{1, 3})
}
