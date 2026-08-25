// The JOIN operation over abstract values: the looser of two facts
// about one name — exact where both sides are known (the union form
// for sets, key-wise for objects), unknown where either is. Includes
// the scalar-set collapse machinery (contiguous-integer runs, kernel
// hull questions) that keeps a loop's repeated join from stacking an
// ever-larger union term.

package abstractdomain

import (
	"math"
	"math/big"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// JoinKnown is joinKnown in the TS source: the looser of two facts
// about one name — exact where both sides are known (the union form for
// sets, key-wise for objects), unknown where either is.
func JoinKnown(a, b AbstractValue) AbstractValue {
	// either path may run, so the joined grade is the weaker one
	grade := MinTrustLevel(TrustLevelOf(a), TrustLevelOf(b))
	if SameKnown(a, b) {
		return AtTrustLevel(a, grade)
	}
	// null joined with undefined (either order): a branch that returns
	// null and a branch that returns undefined join to "null, or
	// undefined" — which is exactly the maybe wrapper around Null, so
	// neither exact flavor is ever claimed. Must be checked BEFORE the
	// plain KindUndef arms below, or the Null side would be wrapped as
	// the value part of the wrong branch.
	if (a.Kind == KindUndef && b.Kind == KindNull) || (a.Kind == KindNull && b.Kind == KindUndef) {
		return AtTrustLevel(PossiblyAbsent(Null, AbsentFlavorConflated, "", false, false), grade)
	}
	// absence joins as the maybe wrapper — a branch returning undefined
	// and a branch returning a value join to "that value, or absent".
	// NO provenance bit here: the walk has not proved the undefined
	// branch reachable, so the joined maybe is not a derived absence.
	// The undef side alone pins the flavor UndefOnly, UNLESS the other
	// side is itself a wrapper whose own absent side must join in too.
	if a.Kind == KindUndef {
		otherFlavor, otherOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(AbsentFlavorUndefOnly, true, otherFlavor, otherOK)
		inner := b
		if b.Kind == KindPossiblyUndefined {
			inner = *b.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	if b.Kind == KindUndef {
		otherFlavor, otherOK := absentFlavorOf(a)
		flavor := joinAbsentFlavor(otherFlavor, otherOK, AbsentFlavorUndefOnly, true)
		inner := a
		if a.Kind == KindPossiblyUndefined {
			inner = *a.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	// null joins the same way as undefined does: a branch returning null
	// and a branch returning a value join to "that value, or absent" —
	// the wrapper's own absent side is exactly NullOnly, unless the
	// other side is itself a wrapper whose flavor must join in too.
	if a.Kind == KindNull {
		otherFlavor, otherOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(AbsentFlavorNullOnly, true, otherFlavor, otherOK)
		inner := b
		if b.Kind == KindPossiblyUndefined {
			inner = *b.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
	}
	if b.Kind == KindNull {
		otherFlavor, otherOK := absentFlavorOf(a)
		flavor := joinAbsentFlavor(otherFlavor, otherOK, AbsentFlavorNullOnly, true)
		inner := a
		if a.Kind == KindPossiblyUndefined {
			inner = *a.Inner
		}
		return AtTrustLevel(PossiblyAbsent(inner, flavor, "", false, false), grade)
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
		// the flavor lattice: a side that is not itself a wrapper
		// contributes no absent side of its own (its whole claim is
		// PRESENT knowledge — the OTHER side's flavor is the join's
		// whole answer); where both sides are wrappers, their own
		// flavors join per joinAbsentFlavor
		aFlavor, aOK := absentFlavorOf(a)
		bFlavor, bOK := absentFlavorOf(b)
		flavor := joinAbsentFlavor(aFlavor, aOK, bFlavor, bOK)
		return AtTrustLevel(
			PossiblyAbsent(JoinKnown(innerA, innerB), flavor, "", false, proved),
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
		merged := append([]*big.Int{}, a.BigintValues...)
		for _, v := range b.BigintValues {
			found := false
			for _, m := range merged {
				if m.Cmp(v) == 0 {
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
	// two OBJECT-STARS join element-wise: the runtime value came through
	// one arm or the other, and each arm's positions hold its own
	// element, so every position of the joined value holds one of the
	// two — their join. Neither side stated a length, so the joined
	// value states none either and nothing is lost there.
	if a.Kind == KindObjectStar && b.Kind == KindObjectStar {
		elementA, okA := ElementOfObjectStar(a)
		elementB, okB := ElementOfObjectStar(b)
		if okA && okB {
			if joined, ok := KnownObjectStar(JoinKnown(elementA, elementB), grade); ok {
				return joined
			}
		}
		// the elements joined to something the graph does not hold (two
		// unrelated shapes whose join keeps no key): no position claim
		// survives, and the visible gap is the honest answer
		return Unknown
	}
	// an OBJECT-STAR joined with an exact LIST: the list's own count
	// does not survive (the star side states none), but every position
	// of either arm holds a value admitted by the star's element joined
	// with all of the list's items — so the joined value is the star of
	// that join. An EMPTY list contributes no item and joins as the
	// star unchanged: a zero-length array vacuously satisfies every
	// per-position claim.
	if a.Kind == KindObjectStar && b.Kind == KindList {
		return joinObjectStarWithList(a, b, grade)
	}
	if b.Kind == KindObjectStar && a.Kind == KindList {
		return joinObjectStarWithList(b, a, grade)
	}
	// an object-star beside anything else — a set, a bare object, a
	// collection — shares no position claim with it: one is a sequence
	// and the other is not, and there is no reading true of both. The
	// walk's own gap is the answer, and it stays visible as one.
	if a.Kind == KindObjectStar || b.Kind == KindObjectStar {
		return Unknown
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
	// two array-holes of the SAME length: every slot is a hole on both
	// arms, so the joined value states the same length and the same
	// hole claim. A length mismatch loses the exactness — the walk's
	// own gap, same as two mismatched KindLists. Density: the
	// Length/ElementSet claim (every read except Object.keys/values/
	// entries) survives regardless of whether the two arms agree on
	// density, so a density disagreement (or either side's density
	// being unestablished) only drops the Dense claim itself, via
	// DenseKnown — it does NOT degrade the whole join to Unknown the
	// way a length mismatch does, because every OTHER claim the kind
	// carries is still exactly true of both arms. (The length-AND-
	// density-agreeing case never reaches here: SameKnown's own
	// KindArrayHoles arm already requires DenseKnown/Dense agreement
	// alongside the length, so JoinKnown's SameKnown(a, b) fast path
	// above returns first whenever both would hold.)
	if a.Kind == KindArrayHoles && b.Kind == KindArrayHoles {
		aLength, aOk := LengthOfArrayHoles(a)
		bLength, bOk := LengthOfArrayHoles(b)
		if !aOk || !bOk || aLength != bLength {
			return Unknown
		}
		lengthClaim := KnownValues([]float64{float64(aLength)}, PrimitiveNumber, grade)
		out := AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet}
		if grade != TrustProved {
			out.Grade = grade
		}
		return out
	}
	// an array-holes side beside an exact KindList of the SAME length:
	// every KindList slot already claims undefined-or-more than a hole
	// (KnownList's own Items may hold non-undef knowledge), so the join
	// is holes at every position both arms could differ on — which is
	// exactly the array-holes claim, unless the list's own item at a
	// position is provably NOT undef, in which case that slot's join
	// with a hole is "that value, or absent" and the whole sequence is
	// no longer a pure hole claim. Conservative: only a list whose every
	// item IS a hole itself joins cleanly to array-holes; anything else
	// is the walk's own gap.
	//
	// Density: a plain KindList's KindUndef items carry no own-property
	// fact at all (ReadArrayConstruction's below-ceiling `new Array(n)`
	// and readArrayFrom's below-ceiling `Array.from({length:n})` both
	// build the identical n-slot list of Undef — a pre-existing gap
	// this join inherits, not one it introduces). Neither Dense=true
	// nor Dense=false is provable from the list side, so the joined
	// value carries DenseKnown=false — the Length/ElementSet claim
	// (every pre-existing read except Object.keys/values/entries)
	// stays exactly as determined as it already was; only the new
	// Object.keys-relevant claim declines.
	joinArrayHolesWithList := func(holes, list AbstractValue) AbstractValue {
		length, ok := LengthOfArrayHoles(holes)
		if !ok || length != len(list.Items) {
			return Unknown
		}
		for _, item := range list.Items {
			if item.Kind != KindUndef {
				return Unknown
			}
		}
		lengthClaim := KnownValues([]float64{float64(length)}, PrimitiveNumber, grade)
		out := AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet}
		if grade != TrustProved {
			out.Grade = grade
		}
		return out
	}
	if a.Kind == KindArrayHoles && b.Kind == KindList {
		return joinArrayHolesWithList(a, b)
	}
	if b.Kind == KindArrayHoles && a.Kind == KindList {
		return joinArrayHolesWithList(b, a)
	}
	// an array-holes side beside anything else that is not itself a
	// sequence of the exact same shape: no position claim survives —
	// the same refusal an object-star beside a non-sequence makes
	if a.Kind == KindArrayHoles || b.Kind == KindArrayHoles {
		return Unknown
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
	// A JOIN THAT WOULD STACK PAST THE WIDENING BOUND answers the
	// string ground directly instead of building the deeper
	// Concatenation/Union/Star the arms below would produce -- a
	// PRECISION policy, not a crash guard (sequence_concatenation_widen.go's
	// file comment has the full history: the kernel's mkUnion now
	// canonicalizes away the term growth that used to hang on this
	// shape). A loop body that reassigns a string across repeated
	// derivations (a `.replace()`/`+` chain re-joined every iteration,
	// with nothing to fold the accumulated structure back down) stacks
	// one more Concatenation/Star layer onto the candidate each round;
	// past sequenceConcatenationWidenBound layers deep, the JOIN
	// ITSELF widens to Strings (C*, the sound "any string" claim every
	// string value already sits inside) rather than handing the
	// kernel's seqSubset decider an ever-larger exact term to encode,
	// cache-key, and answer questions about for no precision a real
	// program shape needs. Checked ahead of the union-building and
	// absorption arms below, on either operand independently, so this
	// widening triggers before either arm's own Concatenation/Union
	// construction would grow the term further.
	if aSet, aIsSet := SetOfKnown(a); aIsSet && refinementsets.StatesSequence(aSet) &&
		refinementsets.SequenceNestingDepth(aSet) > sequenceConcatenationWidenBound {
		return KnownSet(refinementsets.Strings, nil, grade, SetKindTagNone)
	}
	if bSet, bIsSet := SetOfKnown(b); bIsSet && refinementsets.StatesSequence(bSet) &&
		refinementsets.SequenceNestingDepth(bSet) > sequenceConcatenationWidenBound {
		return KnownSet(refinementsets.Strings, nil, grade, SetKindTagNone)
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
	// STRING-GROUND ABSORPTION: one side may fail stringWordSet's own
	// 1-tuple-reread gate (the whole string ground C* — Star(Codepoints)
	// — genuinely admits 1-character words, so it can never pass that
	// gate) while still PROVABLY containing the other side outright —
	// `"xxx"` (a 2+-char word) union Strings is Strings itself, no new
	// 1-tuple risk introduced, since Strings already stated every 1-char
	// word before this join ever ran. The kernel's own seqSubset ask
	// (kernel_seq_subset — a proved theorem in the TRUE direction,
	// seqSubsetB_true) certifies containment; this never DECIDES
	// syntactically which side is bigger. A refused question (no
	// kernel, or a non-sequence shape on either side) falls through to
	// the unchanged path below — the deliberately pinned two-1-codepoint
	// refusal (empty_word_join_test.go) stays exactly as strict, since
	// neither "a" nor "b" contains the other.
	if aSet, aOK := stringSideSet(a); aOK {
		if bSet, bOK := stringSideSet(b); bOK {
			if contains, ok := kernelSeqSubset(bSet, aSet); ok && contains {
				return KnownSet(aSet, nil, grade, SetKindTagNone)
			}
			if contains, ok := kernelSeqSubset(aSet, bSet); ok && contains {
				return KnownSet(bSet, nil, grade, SetKindTagNone)
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
	// canonical SPELLING first (CanonicalScalarForms — a structural
	// equality, never an approximation): a meet-concatenated side wears
	// duplicate conjuncts and vacuous ±inf bounds that blind the run
	// collapse below, and a blinded collapse stacks a union form per
	// walk pass — the same unbounded growth the collapse exists to
	// stop, resurfacing one spelling away (createCategoricalInverse.ts
	// hung a kernel invariant ask on exactly that stack)
	left = refinementsets.CanonicalScalarForms(left)
	right = refinementsets.CanonicalScalarForms(right)
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
	// the syntactic run-collapse above declined — a shape it does not
	// read (an Above/Below ray, a conjunction the local reader cannot
	// prove contiguous) — so the KERNEL's own Bounds question
	// (kernelBounds, refined_bounds) gets asked before this join settles
	// for stacking a raw Union term. An EMPTY side is the join identity
	// (∅ ∪ B = B exactly — sound with no approximation: whatever forms
	// an empty side wears, they admit nothing, so the join is the other
	// side's own hull alone) — this is what repairs a scalar conjunction
	// that turned out self-contradictory (a place-value narrowing met
	// against a value's own tighter bound, e.g. `Above(5)` met with
	// `[0,3]`): the meet had no spelling for "no such value," so the
	// contradiction rides as a live-looking KindSet until this join
	// finally asks whether it holds anything at all. A NONEMPTY pair
	// whose kernel-stated hulls touch or overlap collapses the same way
	// the syntactic runs above do — the hull CONTAINS the union (every
	// member either side admits sits inside its own kernel-proved
	// enclosure), and integer is kept only where BOTH sides' hulls are
	// integral, so the collapse is weaker-true, never a wrong answer.
	if leftBounds, leftBoundsOK := kernelBounds(left); leftBoundsOK {
		if leftBounds.Empty {
			return KnownWithMeasures(KnownSet(right, nil, grade, SetKindTagNone), sharedMeasures)
		}
		if rightBounds, rightBoundsOK := kernelBounds(right); rightBoundsOK {
			if rightBounds.Empty {
				return KnownWithMeasures(KnownSet(left, nil, grade, SetKindTagNone), sharedMeasures)
			}
			// a hull replaces the UNION's spelling with a window — sound
			// (the hull contains every member either side admits) but a
			// widened SPELLING, not merely a widened claim, and several
			// readers depend on the exact-points spelling surviving a join
			// untouched (hover text, diagnostic sentences that list
			// members, and TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot's
			// own {0, 1, 2} pin — three plain writes joined pairwise, each
			// side a bare oneOf singleton the whole way through, denoting
			// the SAME set a window would, but wearing the wrong shape for
			// a caller that lists members rather than reads bounds).
			// Collapsing only earns its keep where the syntactic
			// integerRunOf path above declined on a GENUINE window shape
			// (an Above/Below ray, a conjunction) — a would-be union where
			// BOTH sides are already exact points (a bare oneOf, the shape
			// integerRunOf's own fromValues arm already reads and the line
			// above already skips when BOTH sides are fromValues) has
			// nothing for the hull to repair, so the gate below declines
			// there and lets the plain Union fallback keep the flat
			// point-set spelling every enumerating reader needs.
			if !(exactPointsOnly(left) && exactPointsOnly(right)) {
				if collapsed, ok := collapseTouchingHulls(leftBounds.Hull, rightBounds.Hull); ok {
					return KnownWithMeasures(KnownSet(collapsed, nil, grade, SetKindTagNone), sharedMeasures)
				}
			}
		}
	}
	return KnownWithMeasures(
		KnownSet(
			refinementsets.CanonicalScalarForms(
				refinementsets.MakeRefinedSet(refinementsets.Union(left, right))),
			nil, grade, SetKindTagNone),
		sharedMeasures,
	)
}

// collapseTouchingHulls folds two kernel-proved integral hulls into
// their own hull EXACTLY, when they touch or overlap — the Bounds-driven
// twin of the syntactic integerRunOf collapse above, reached only where
// that syntactic reading declined. Both hulls must be INTEGER windows
// with finite edges (kernelBounds's own scalar-set contract: a
// non-integral or unbounded enclosure rides back unchanged, which this
// function does not try to read); anything else declines rather than
// guess at a bound the kernel did not state as integral.
func collapseTouchingHulls(a, b refinementsets.RefinedSet) (refinementsets.RefinedSet, bool) {
	aLo, aHi, aOK := integralClosedWindow(a)
	bLo, bHi, bOK := integralClosedWindow(b)
	if !aOK || !bOK {
		return refinementsets.RefinedSet{}, false
	}
	touches := func(loA, hiA, loB float64) bool {
		if loB <= hiA {
			return true
		}
		return isSafeInteger(hiA) && loB <= hiA+1
	}
	if !touches(aLo, aHi, bLo) || !touches(bLo, bHi, aLo) {
		return refinementsets.RefinedSet{}, false
	}
	lo := math.Min(aLo, bLo)
	hi := math.Max(aHi, bHi)
	return refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(lo), refinementsets.AtMost(hi), refinementsets.Integer,
	), true
}

// exactPointsOnly is whether a set spells NOTHING but exact points —
// a bare oneOf leaf, or a union tree whose every leaf is one. This is
// the shape three plain writes joined pairwise keep producing (`{0}`
// join `{1}` is `Union(oneOf[0], oneOf[1])`, itself join `{2}` is
// `Union(oneOf[2], Union(oneOf[0], oneOf[1]))` — CanonicalScalarForms
// dedupes and reorders arms but never flattens distinct oneOf leaves
// into one, so the tree stays points-only all the way down) — the gate
// the Bounds-driven hull collapse checks before it runs: collapsing
// TWO such sides into a window would replace an enumerable point-set
// spelling that costs the caller nothing to widen back to a window
// later with one that has already thrown the exact membership list
// away, and callers that list members (hover text, a diagnostic
// sentence, a test reading the join back through SetOfKnown) need that
// list, not a bound. A set carrying ANY other form (a window, a
// pattern, the Integer/MultipleOf marks) is not points-only, and the
// collapse is free to run wherever at least one side isn't.
func exactPointsOnly(set refinementsets.RefinedSet) bool {
	if len(set.Forms) != 1 {
		return false
	}
	form := set.Forms[0]
	switch form.Form {
	case refinementsets.FormOneOf:
		return true
	case refinementsets.FormUnion:
		return form.A_ != nil && form.B != nil && exactPointsOnly(*form.A_) && exactPointsOnly(*form.B)
	default:
		return false
	}
}

// integralClosedWindow reads a kernel-stated hull back as a closed
// [lo, hi] integer window — the shape kernelBounds answers for a
// nonempty integral scalar set with finite edges (its own doc: "for a
// nonempty integral set with finite edges, the least and greatest
// members"). Declines on anything else (an unbounded enclosure, a
// non-integral one) rather than misread a wider claim as this narrow
// shape.
func integralClosedWindow(hull refinementsets.RefinedSet) (lo, hi float64, ok bool) {
	hasLo, hasHi, isInt := false, false, false
	for _, form := range hull.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast:
			lo, hasLo = form.A, true
		case refinementsets.FormAtMost:
			hi, hasHi = form.A, true
		case refinementsets.FormInteger:
			isInt = true
		default:
			return 0, 0, false
		}
	}
	if !hasLo || !hasHi || !isInt || !isSafeInteger(lo) || !isSafeInteger(hi) {
		return 0, 0, false
	}
	return lo, hi, true
}

// joinObjectStarWithList joins an object-star with an exact list: the
// element claim has to hold at every position of EITHER arm, so it is
// the star's element joined with each of the list's items. The list's
// count does not survive — the star side states none.
func joinObjectStarWithList(star, list AbstractValue, grade TrustLevel) AbstractValue {
	element, ok := ElementOfObjectStar(star)
	if !ok {
		return Unknown
	}
	for _, item := range list.Items {
		element = JoinKnown(element, item)
	}
	if joined, ok := KnownObjectStar(element, grade); ok {
		return joined
	}
	// the list held a non-graph item (a number, a set), so no ONE
	// position claim covers both arms
	return Unknown
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
	// isInfinities recognizes the mark's ±∞ arm. The flag derivation
	// below (negInf/posInf) reads a match as "BOTH infinities are
	// members" — that is the only shape any producer emits: the
	// kernel's intOrInfForm hardcodes w: ["-inf", "+inf"]
	// (encode_sets.lean, the authority), and the Go-side mirror in
	// coercion_models.go emits OneOf{+Inf, -Inf} the same way; nothing
	// narrows one side off a mark once built. A one-sided or empty
	// all-infinities list is not that shape, so it must decline here
	// rather than let the caller assert an infinity the set does not
	// hold.
	isInfinities := func(s refinementsets.RefinedSet) bool {
		if len(s.Forms) != 1 || s.Forms[0].Form != refinementsets.FormOneOf {
			return false
		}
		hasNegInf := false
		hasPosInf := false
		for _, v := range s.Forms[0].W {
			if v == math.Inf(-1) {
				hasNegInf = true
			} else if v == math.Inf(1) {
				hasPosInf = true
			} else {
				return false
			}
		}
		return hasNegInf && hasPosInf
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
