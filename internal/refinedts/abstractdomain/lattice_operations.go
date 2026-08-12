// The LATTICE operations over abstract values — join, meet,
// truthiness, structural equality, the denoted set, and provenance-
// carrying unknowns. "Join" and "meet" are the lattice-theory
// standard; the join is EXACT (union forms the kernel decides), and
// conformance holds it to the kernel's joinState.

package abstractdomain

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MeetKnown is meetKnown in the TS source.
func MeetKnown(a, b AbstractValue) AbstractValue {
	if a.Kind == KindUnknown {
		return b
	}
	if b.Kind == KindUnknown {
		return a
	}
	// a claim that excludes absence drops the other side's absent
	// half: defined ∧ (X ∨ absent) is X
	if a.Kind == KindPossiblyUndefined && b.Kind != KindPossiblyUndefined {
		return MeetKnown(*a.Inner, b)
	}
	if b.Kind == KindPossiblyUndefined && a.Kind != KindPossiblyUndefined {
		return MeetKnown(a, *b.Inner)
	}
	if a.Kind == KindValues {
		return a
	}
	if b.Kind == KindValues {
		return b
	}
	if a.Kind == KindSet && b.Kind == KindSet &&
		a.Temporal == nil && b.Temporal == nil &&
		a.SetKindTag == SetKindTagNone && b.SetKindTag == SetKindTagNone {
		combined := append(append([]refinementsets.Refinement{}, a.Set.Forms...), b.Set.Forms...)
		measures := a.Measures
		if measures == nil {
			measures = b.Measures
		}
		return KnownWithMeasures(
			KnownSet(
				refinementsets.MakeRefinedSet(combined...),
				nil,
				MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b)),
				SetKindTagNone,
			),
			measures,
		)
	}
	return a
}

// Truthiness is truthiness in the TS source.
//
// The three-valued result (true / false / unknown) is (bool, bool):
// (value, known).
func Truthiness(k AbstractValue) (bool, bool) {
	switch k.Kind {
	case KindValues:
		if k.KindTag == PrimitiveString {
			return len(k.Values) > 0, true
		}
		if k.KindTag == PrimitiveArray {
			return true, true // arrays are objects
		}
		if len(k.Values) == 1 {
			return k.Values[0] != 0, true
		}
		return false, false
	case KindObject, KindList, KindCollection, KindPromise, KindDate, KindRegex:
		return true, true // an object — always truthy
	case KindSymbol:
		return true, true // every symbol is truthy
	case KindHostFunction:
		return true, true // every function object is truthy
	case KindBigints:
		// 0n is the one falsy bigint
		if len(k.BigintValues) == 1 {
			return k.BigintValues[0] != 0, true
		}
		return false, false
	case KindNaN:
		return false, true
	case KindUndef:
		return false, true
	case KindKindUnion:
		// decided only when EVERY arm agrees. A kindUnion with zero arms
		// does not occur in practice (KindUnionOf collapses that case to
		// Unknown before construction); this guard keeps the function
		// total (the TS source's `verdicts[0]` on an empty array reads
		// as `undefined`, which the surrounding ternary then treats as
		// non-null and returns as-is — behavior this port does not
		// reproduce since it is unreached).
		if len(k.Arms) == 0 {
			return false, false
		}
		first, firstKnown := Truthiness(k.Arms[0])
		if !firstKnown {
			return false, false
		}
		for _, arm := range k.Arms {
			v, known := Truthiness(arm)
			if !known || v != first {
				return false, false
			}
		}
		return first, true
	case KindSet, KindVariable, KindPossiblyUndefined, KindPossiblyNaN, KindUnknown:
		return false, false
	default:
		return false, false
	}
}

// SameKnown is sameKnown in the TS source: structural sameness. (Not
// JSON: an object's stated annotation can carry compiler symbols; it
// compares by identity.)
func SameKnown(a, b AbstractValue) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindValues:
		return a.KindTag == b.KindTag && sameFloats(a.Values, b.Values)
	case KindSet:
		return sameRefinedSet(a.Set, b.Set) &&
			sameTemporal(a.Temporal, b.Temporal) &&
			a.SetKindTag == b.SetKindTag
	case KindObject:
		if a.Stated != b.Stated {
			return false
		}
		if a.Complete != b.Complete {
			return false
		}
		if a.BareProto != b.BareProto {
			return false
		}
		if len(a.Keys) != len(b.Keys) {
			return false
		}
		for _, key := range a.Keys {
			other, ok := lookupKey(b.Keys, key.Name)
			if !ok || !SameKnown(key.Value, other) {
				return false
			}
		}
		return true
	case KindVariable:
		// the variable, by identity — its compiler symbol
		return a.Symbol == b.Symbol && a.StarDepth == b.StarDepth
	case KindList:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i, item := range a.Items {
			if !SameKnown(item, b.Items[i]) {
				return false
			}
		}
		return true
	case KindCollection:
		if a.CollectionFlavor != b.CollectionFlavor || a.Complete != b.Complete {
			return false
		}
		if len(a.Entries) != len(b.Entries) {
			return false
		}
		for i, e := range a.Entries {
			if !SameKnown(e.Key, b.Entries[i].Key) || !SameKnown(e.Value, b.Entries[i].Value) {
				return false
			}
		}
		return true
	case KindUndef:
		return true
	case KindNaN:
		return true
	case KindPossiblyUndefined:
		return SameKnown(*a.Inner, *b.Inner)
	case KindPromise:
		return SameKnown(*a.Inner, *b.Inner)
	case KindPossiblyNaN:
		return SameKnown(*a.Inner, *b.Inner)
	case KindDate:
		return SameKnown(*a.Millis, *b.Millis)
	case KindSymbol:
		return a.HasSymbolKey == b.HasSymbolKey && a.SymbolKey == b.SymbolKey &&
			a.HasDescription == b.HasDescription && a.Description == b.Description
	case KindHostFunction:
		return true // the sort is the whole claim
	case KindBigints:
		if len(a.BigintValues) != len(b.BigintValues) {
			return false
		}
		for i, v := range a.BigintValues {
			if v != b.BigintValues[i] {
				return false
			}
		}
		return true
	case KindRegex:
		return a.Source == b.Source && a.Flags == b.Flags
	case KindKindUnion:
		if len(a.Arms) != len(b.Arms) {
			return false
		}
		for i, arm := range a.Arms {
			if !SameKnown(arm, b.Arms[i]) {
				return false
			}
		}
		return true
	case KindUnknown:
		// the opaque marker is provenance, not the same fact — joining
		// an opaque path with a plain-unknown one stays plain
		return a.Opaque == b.Opaque
	default:
		return false
	}
}

func lookupKey(keys []ObjectKey, name string) (AbstractValue, bool) {
	for _, key := range keys {
		if key.Name == name {
			return key.Value, true
		}
	}
	return AbstractValue{}, false
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// sameTemporal compares the TS source's `JSON.stringify(a.temporal ??
// null) === JSON.stringify(b.temporal ?? null)` — a deep-equality
// fallback the TS source reaches for because TemporalAnnotation has no
// hand-written equality. refinementsets.TemporalAnnotation is a plain
// value struct (refinement_sets/calendar_interpreter.go), so a Go value
// comparison over the dereferenced structs is the same deep-equality
// check without a JSON round trip.
func sameTemporal(a, b *refinementsets.TemporalAnnotation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameRefinedSet compares the TS source's `JSON.stringify(a.set) ===
// JSON.stringify(b.set)` structurally, since refinementsets.RefinedSet
// holds pointer fields (A_, B) that a Go `==` cannot compare.
func sameRefinedSet(a, b refinementsets.RefinedSet) bool {
	if len(a.Forms) != len(b.Forms) {
		return false
	}
	for i, f := range a.Forms {
		if !sameRefinement(f, b.Forms[i]) {
			return false
		}
	}
	return true
}

func sameRefinement(a, b refinementsets.Refinement) bool {
	if a.Form != b.Form {
		return false
	}
	if a.A != b.A {
		return false
	}
	if !sameFloats(a.W, b.W) {
		return false
	}
	if (a.A_ == nil) != (b.A_ == nil) {
		return false
	}
	if a.A_ != nil && !sameRefinedSet(*a.A_, *b.A_) {
		return false
	}
	if (a.B == nil) != (b.B == nil) {
		return false
	}
	if a.B != nil && !sameRefinedSet(*a.B, *b.B) {
		return false
	}
	if a.Lo != b.Lo {
		return false
	}
	if (a.Hi == nil) != (b.Hi == nil) {
		return false
	}
	if a.Hi != nil && *a.Hi != *b.Hi {
		return false
	}
	return true
}

// SetOfKnown is setOfKnown in the TS source: the refined set a
// knowledge state denotes: a single value is its singleton, a tuple of
// values the concatenation of singletons. A variable denotes its BOUND
// (T ⊆ bound, and every finite subset of the bound is an admissible T,
// so the bound is exact for the universal reading).
func SetOfKnown(k AbstractValue) (refinementsets.RefinedSet, bool) {
	switch k.Kind {
	case KindObject:
		return refinementsets.RefinedSet{}, false // objects live in the graph, not the tuple layer
	case KindList, KindCollection, KindPromise, KindDate:
		return refinementsets.RefinedSet{}, false // nested structure the tuple layer cannot formatAt
	case KindVariable:
		set := k.Bound
		for i := 0; i < k.StarDepth; i++ {
			set = refinementsets.MakeRefinedSet(refinementsets.Star(set))
		}
		return set, true
	case KindUndef, KindPossiblyUndefined, KindPossiblyNaN, KindSymbol,
		KindHostFunction, KindBigints, KindRegex, KindKindUnion:
		// absence, NaN, symbols, functions, bigints leave ℝ̄; a sort
		// union has no ONE set — its arms would reread each other's
		// words
		return refinementsets.RefinedSet{}, false
	case KindNaN:
		return refinementsets.RefinedSet{}, false // NaN is not an element of ℝ̄ — no set holds it
	case KindSet:
		// a worn set's members are not doubles — it stays out of ℝ̄
		if k.SetKindTag == SetKindTagNone {
			return k.Set, true
		}
		return refinementsets.RefinedSet{}, false
	case KindValues:
		if len(k.Values) == 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[0]})), true
		}
		var set *refinementsets.RefinedSet
		for i := len(k.Values) - 1; i >= 0; i-- {
			singleton := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{k.Values[i]}))
			if set == nil {
				set = &singleton
			} else {
				combined := refinementsets.MakeRefinedSet(refinementsets.Concatenation(singleton, *set))
				set = &combined
			}
		}
		// the empty tuple has no 1-tuple spelling; leave it to membership
		if set == nil {
			return refinementsets.RefinedSet{}, false
		}
		return *set, true
	case KindUnknown:
		return refinementsets.RefinedSet{}, false
	default:
		return refinementsets.RefinedSet{}, false
	}
}

// UnknownOver is unknownOver in the TS source: the unknown result of an
// operation, wearing outside provenance when every operand that failed
// to pin it is itself opaque: a computation over only outside-determined
// inputs is determined outside too. One PLAIN unknown operand keeps the
// result plain — the walk's own gap stays visible as one.
func UnknownOver(operands []AbstractValue) AbstractValue {
	sawOpaque := false
	for _, operand := range operands {
		if operand.Kind == KindUnknown {
			if !operand.Opaque {
				return Unknown
			}
			sawOpaque = true
		}
	}
	if sawOpaque {
		return Opaque
	}
	return Unknown
}

// statesOnlyLongSequences is the TS source's statesOnlyLongSequences:
// whether every member the set admits is a sequence of length two or
// more — every form spells tuples, and no tuple is short enough to
// double as a scalar. The positive test the string-word join rests on.
func statesOnlyLongSequences(set refinementsets.RefinedSet) bool {
	if len(set.Forms) == 0 {
		return false
	}
	for _, f := range set.Forms {
		if f.Form == refinementsets.FormConcatenation {
			continue
		}
		if f.Form == refinementsets.FormUnion &&
			statesOnlyLongSequences(*f.A_) && statesOnlyLongSequences(*f.B) {
			continue
		}
		return false
	}
	return true
}

// stringWordSet is the TS source's stringWordSet: the side's strings as
// a set, where the side is DEMONSTRABLY a string-sorted word of length
// two or more (its tuple can never be reread as a scalar), or an
// untagged set all of whose members are such sequences. (nil, false)
// anywhere else.
func stringWordSet(k AbstractValue) (refinementsets.RefinedSet, bool) {
	if k.Kind == KindValues && k.KindTag == PrimitiveString {
		if len(k.Values) >= 2 {
			return refinementsets.StringTuple(stringOf(k.Values)), true
		}
		return refinementsets.RefinedSet{}, false
	}
	if k.Kind == KindSet && k.SetKindTag == SetKindTagNone && statesOnlyLongSequences(k.Set) {
		return k.Set, true
	}
	return refinementsets.RefinedSet{}, false
}

// JoinKnown is joinKnown in the TS source: the looser of two facts
// about one name — exact where both sides are known (the union form for
// sets, key-wise for objects), unknown where either is.
func JoinKnown(a, b AbstractValue) AbstractValue {
	// either path may run, so the joined grade is the weaker one
	grade := MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b))
	if SameKnown(a, b) {
		return AtTrustLevel(a, grade)
	}
	// absence joins as the maybe wrapper — a branch returning undefined
	// and a branch returning a value join to "that value, or absent".
	// NO provenance bit here: the walk has not proved the undefined
	// branch reachable, so the joined maybe is not a derived absence
	if a.Kind == KindUndef {
		return AtTrustLevel(PossiblyUndefined(b, "", false, false), grade)
	}
	if b.Kind == KindUndef {
		return AtTrustLevel(PossiblyUndefined(a, "", false, false), grade)
	}
	// NaN joins the same way on its own wrapper
	if a.Kind == KindNaN {
		return AtTrustLevel(PossiblyNaN(b), grade)
	}
	if b.Kind == KindNaN {
		return AtTrustLevel(PossiblyNaN(a), grade)
	}
	if a.Kind == KindPossiblyNaN || b.Kind == KindPossiblyNaN {
		innerA := a
		if a.Kind == KindPossiblyNaN {
			innerA = *a.Inner
		}
		innerB := b
		if b.Kind == KindPossiblyNaN {
			innerB = *b.Inner
		}
		return AtTrustLevel(PossiblyNaN(JoinKnown(innerA, innerB)), grade)
	}
	if a.Kind == KindPossiblyUndefined || b.Kind == KindPossiblyUndefined {
		innerA := a
		if a.Kind == KindPossiblyUndefined {
			innerA = *a.Inner
		}
		innerB := b
		if b.Kind == KindPossiblyUndefined {
			innerB = *b.Inner
		}
		// the provenance bit survives the join: a proved-absent side
		// keeps the joined value provably maybe-absent
		proved := (a.Kind == KindPossiblyUndefined && a.ProvedAbsent) ||
			(b.Kind == KindPossiblyUndefined && b.ProvedAbsent)
		return AtTrustLevel(
			PossiblyUndefined(JoinKnown(innerA, innerB), "", false, proved),
			grade,
		)
	}
	// an unknown side degrades the join in two steps: a PLAIN unknown
	// is the walk's own gap and stays visible as one; an OPAQUE side
	// joined with anything still means the type is everything this
	// file determines — the outside provenance survives
	if a.Kind == KindUnknown || b.Kind == KindUnknown {
		plain := (a.Kind == KindUnknown && !a.Opaque) || (b.Kind == KindUnknown && !b.Opaque)
		if plain {
			return Unknown
		}
		return Opaque
	}
	// a SORT UNION joins arm-wise: a sorted claim lands on the arm its
	// sort names (number and boolean share a bucket — their words
	// share the ground), and two unions merge arm against arm.
	// Anything unplaceable degrades to unknown, the visible gap.
	if a.Kind == KindKindUnion || b.Kind == KindKindUnion {
		armsOf := func(k AbstractValue) []AbstractValue {
			if k.Kind == KindKindUnion {
				return k.Arms
			}
			return []AbstractValue{k}
		}
		bucket := func(k AbstractValue) (ClaimSort, bool) {
			s := KindOfClaim(k)
			if s == ClaimSortNone {
				return ClaimSortNone, false
			}
			if s == ClaimSortBoolean {
				return ClaimSortNumber, true
			}
			return s, true
		}
		merged := append([]AbstractValue{}, armsOf(a)...)
		for _, incoming := range armsOf(b) {
			key, ok := bucket(incoming)
			if !ok {
				return Unknown
			}
			idx := -1
			for i, held := range merged {
				if heldKey, heldOK := bucket(held); heldOK && heldKey == key {
					idx = i
					break
				}
			}
			if idx < 0 {
				merged = append(merged, incoming)
				continue
			}
			joined := JoinKnown(merged[idx], incoming)
			if joined.Kind == KindUnknown {
				return Unknown
			}
			merged[idx] = joined
		}
		for _, arm := range merged {
			if _, ok := bucket(arm); !ok {
				return Unknown
			}
		}
		return AtTrustLevel(KindUnionOf(merged), grade)
	}
	if a.Kind == KindValues && b.Kind == KindValues &&
		a.KindTag == PrimitiveBoolean && b.KindTag == PrimitiveBoolean {
		// boolean words keep their sort through a join — a union SET
		// would reread the words as numbers
		merged := append([]float64{}, a.Values...)
		for _, v := range b.Values {
			if !floatsInclude(merged, v) {
				merged = append(merged, v)
			}
		}
		return KnownValues(merged, PrimitiveBoolean, grade)
	}
	if a.Kind == KindCollection && b.Kind == KindCollection {
		// the same keys in the same insertion order join per entry;
		// anything else claims nothing — no partial-collection
		// membership (a key present on one side only may or may not
		// exist after either path)
		sameKeys := a.CollectionFlavor == b.CollectionFlavor && a.Complete && b.Complete &&
			len(a.Entries) == len(b.Entries)
		if sameKeys {
			for i, e := range a.Entries {
				if !SameKnown(e.Key, b.Entries[i].Key) {
					sameKeys = false
					break
				}
			}
		}
		if sameKeys {
			entries := make([]CollectionEntry, len(a.Entries))
			for i, e := range a.Entries {
				entries[i] = CollectionEntry{Key: e.Key, Value: JoinKnown(e.Value, b.Entries[i].Value)}
			}
			return AtTrustLevel(AbstractValue{
				Kind:             KindCollection,
				CollectionFlavor: a.CollectionFlavor,
				Entries:          entries,
				Complete:         true,
			}, grade)
		}
		return Unknown
	}
	if a.Kind == KindBigints && b.Kind == KindBigints {
		merged := append([]int64{}, a.BigintValues...)
		for _, v := range b.BigintValues {
			found := false
			for _, m := range merged {
				if m == v {
					found = true
					break
				}
			}
			if !found {
				merged = append(merged, v)
			}
		}
		return AtTrustLevel(AbstractValue{Kind: KindBigints, BigintValues: merged}, grade)
	}
	if a.Kind == KindList && b.Kind == KindList {
		if len(a.Items) != len(b.Items) {
			return Unknown
		}
		items := make([]AbstractValue, len(a.Items))
		for i, item := range a.Items {
			items[i] = JoinKnown(item, b.Items[i])
		}
		return KnownList(items, grade)
	}
	if a.Kind == KindObject && b.Kind == KindObject {
		bByName := make(map[string]AbstractValue, len(b.Keys))
		for _, key := range b.Keys {
			bByName[key.Name] = key.Value
		}
		var keys []ObjectKey
		for _, key := range a.Keys {
			if other, ok := bByName[key.Name]; ok {
				keys = append(keys, ObjectKey{Name: key.Name, Value: JoinKnown(key.Value, other)})
			}
		}
		// completeness survives only when both sides are complete AND
		// name the same keys — a key present on one side only may exist
		sameNames := len(a.Keys) == len(b.Keys)
		if sameNames {
			for _, key := range a.Keys {
				if _, ok := bByName[key.Name]; !ok {
					sameNames = false
					break
				}
			}
		}
		// bare prototype survives only when BOTH sides are bare; a MIXED
		// join drops completeness outright — a complete claim would let
		// a missing-key read pick one arm's prototype story (inherited
		// function vs exact undefined), and neither alone is sound
		bareAgree := a.BareProto == b.BareProto
		stated := a.Stated
		if a.Stated != b.Stated {
			stated = nil
		}
		joint := KnownObject(
			keys,
			stated,
			a.Complete && b.Complete && sameNames && bareAgree,
			grade,
			a.BareProto && b.BareProto,
		)
		// two joined shapes stay selectable: the runtime value came
		// through one arm or the other, so the arms ride as variants
		// (bounded — past four the correlation stops paying for itself)
		var arms []AbstractValue
		sides := append(append([]AbstractValue{}, sidesOf(a)...), sidesOf(b)...)
		for _, side := range sides {
			found := false
			for _, held := range arms {
				if SameKnown(held, side) {
					found = true
					break
				}
			}
			if !found {
				arms = append(arms, side)
			}
		}
		if len(arms) >= 2 && len(arms) <= 4 {
			return KnownWithVariants(joint, arms)
		}
		return joint
	}
	// two STRING-SORTED sides join into the union of their tuples —
	// but only when every member is at least two characters long, so
	// every member is unambiguously sequence-shaped and no scalar
	// position can reread a 1-tuple as a number (the admitted-language
	// rule). A one-character or empty word declines; so does any set
	// holding a non-sequence form.
	{
		words, wordsOK := stringWordSet(a)
		if wordsOK {
			other, otherOK := stringWordSet(b)
			if otherOK {
				return KnownSet(refinementsets.MakeRefinedSet(refinementsets.Union(words, other)), nil, grade, SetKindTagNone)
			}
		}
	}
	// a string- or array-sorted word does not build a union SET: the
	// set is sortless, and its 1-tuple members would be reread as
	// numbers at a scalar position (the admitted-language rule)
	if !IsNumericKind(a) || !IsNumericKind(b) {
		return Unknown
	}
	left, leftOK := SetOfKnown(a)
	right, rightOK := SetOfKnown(b)
	if !leftOK || !rightOK {
		return Unknown
	}
	// measures both sides share survive the join: the runtime value
	// came through one arm or the other, and each arm wore them
	var sharedMeasures *Measures
	if a.Kind == KindSet && b.Kind == KindSet && a.Measures != nil && b.Measures != nil &&
		a.Measures.HasSum == b.Measures.HasSum && a.Measures.Sum == b.Measures.Sum &&
		a.Measures.Sorted == b.Measures.Sorted {
		sharedMeasures = a.Measures
	}
	// two contiguous-integer readings that overlap or adjoin union to
	// their hull EXACTLY — the count-loop's join ({0} ∪ [1,∞) ∩ ℤ)
	// stays one window instead of stacking a union form per walk pass,
	// which grew the kernel's questions without bound until the wasm
	// heap died on them
	leftRun, leftRunOK := integerRunOf(left)
	rightRun, rightRunOK := integerRunOf(right)
	// exact-values sides stay ENUMERABLE (a window decides fewer
	// questions than a small finite set); the collapse serves the
	// window-bearing joins, where the union form would otherwise
	// stack without bound
	if leftRunOK && rightRunOK && !(leftRun.fromValues && rightRun.fromValues) {
		// b begins inside a, or immediately after it — the +1 is asked
		// only where it is exact
		touches := func(a, b integerRun) bool {
			if b.lo <= a.hi {
				return true
			}
			return isSafeInteger(a.hi) && b.lo <= a.hi+1
		}
		if touches(leftRun, rightRun) && touches(rightRun, leftRun) {
			lo := math.Min(leftRun.lo, rightRun.lo)
			hi := math.Max(leftRun.hi, rightRun.hi)
			var infs []float64
			if leftRun.negInf || rightRun.negInf {
				infs = append(infs, math.Inf(-1))
			}
			if leftRun.posInf || rightRun.posInf {
				infs = append(infs, math.Inf(1))
			}
			var forms []refinementsets.Refinement
			if lo != math.Inf(-1) {
				forms = append(forms, refinementsets.AtLeast(lo))
			}
			if hi != math.Inf(1) {
				forms = append(forms, refinementsets.AtMost(hi))
			}
			if len(infs) == 0 {
				forms = append(forms, refinementsets.Integer)
			} else {
				forms = append(forms, refinementsets.Union(
					refinementsets.MakeRefinedSet(refinementsets.Integer),
					refinementsets.MakeRefinedSet(refinementsets.OneOf(infs)),
				))
			}
			return KnownWithMeasures(
				KnownSet(refinementsets.MakeRefinedSet(forms...), nil, grade, SetKindTagNone),
				sharedMeasures,
			)
		}
	}
	return KnownWithMeasures(
		KnownSet(refinementsets.MakeRefinedSet(refinementsets.Union(left, right)), nil, grade, SetKindTagNone),
		sharedMeasures,
	)
}

// sidesOf is the TS source's `a.variants ?? [a]` read at each join call
// site.
func sidesOf(k AbstractValue) []AbstractValue {
	if k.Variants != nil {
		return k.Variants
	}
	return []AbstractValue{k}
}

func floatsInclude(values []float64, x float64) bool {
	for _, v := range values {
		if v == x {
			return true
		}
	}
	return false
}

func isSafeInteger(x float64) bool {
	return x == math.Trunc(x) && math.Abs(x) <= 9007199254740991 // Number.MAX_SAFE_INTEGER
}

// integerRun is IntegerRun in the TS source: an exact contiguous-integer
// reading of a scalar set: the integer members are exactly the window's,
// and negInf/posInf say whether the matching infinity is also a member
// (the integers-or-±∞ mark with an unbounded window admits it; a bounded
// window shuts it out). Read from a pure non-strict window wearing the
// integer form or the mark, or from a oneOf whose integers tile an
// interval — the shapes the loop walk joins. (integerRun{}, false)
// anywhere else.
type integerRun struct {
	lo, hi     float64
	negInf     bool
	posInf     bool
	fromValues bool // the reading came from an exact oneOf, not a window
}

// integerRunOf is integerRunOf in the TS source.
func integerRunOf(set refinementsets.RefinedSet) (integerRun, bool) {
	lo := math.Inf(-1)
	hi := math.Inf(1)
	isInt := false
	intOrInf := false
	var values []float64
	haveValues := false
	isIntegerAlone := func(s refinementsets.RefinedSet) bool {
		return len(s.Forms) == 1 && s.Forms[0].Form == refinementsets.FormInteger
	}
	isInfinities := func(s refinementsets.RefinedSet) bool {
		if len(s.Forms) != 1 || s.Forms[0].Form != refinementsets.FormOneOf {
			return false
		}
		for _, v := range s.Forms[0].W {
			if v != math.Inf(1) && v != math.Inf(-1) {
				return false
			}
		}
		return true
	}
	for _, form := range set.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast:
			lo = math.Max(lo, form.A)
		case refinementsets.FormAtMost:
			hi = math.Min(hi, form.A)
		case refinementsets.FormInteger:
			isInt = true
		case refinementsets.FormOneOf:
			if haveValues {
				return integerRun{}, false
			}
			values = form.W
			haveValues = true
		case refinementsets.FormUnion:
			// the integers-or-±∞ mark (the arithmetic transfers emit it)
			if (isIntegerAlone(*form.A_) && isInfinities(*form.B)) ||
				(isIntegerAlone(*form.B) && isInfinities(*form.A_)) {
				intOrInf = true
				break
			}
			return integerRun{}, false
		default:
			return integerRun{}, false
		}
	}
	if haveValues {
		if lo != math.Inf(-1) || hi != math.Inf(1) || isInt || intOrInf {
			return integerRun{}, false
		}
		if len(values) == 0 {
			return integerRun{}, false
		}
		for _, v := range values {
			if !isSafeInteger(v) {
				return integerRun{}, false
			}
		}
		min := values[0]
		max := values[0]
		seen := map[float64]bool{}
		for _, v := range values {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			seen[v] = true
		}
		if max-min+1 != float64(len(seen)) {
			return integerRun{}, false
		}
		return integerRun{lo: min, hi: max, negInf: false, posInf: false, fromValues: true}, true
	}
	if (!isInt && !intOrInf) || lo > hi {
		return integerRun{}, false
	}
	return integerRun{
		lo:         lo,
		hi:         hi,
		negInf:     intOrInf && lo == math.Inf(-1),
		posInf:     intOrInf && hi == math.Inf(1),
		fromValues: false,
	}, true
}
