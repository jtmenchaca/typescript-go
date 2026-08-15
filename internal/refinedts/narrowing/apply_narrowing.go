// Applying narrowing effects to abstract values: narrowAt walks
// the tested path, shape effects meet without displacing sharper
// facts, truthiness filters finite word lists, and a refutation
// never narrows an unknown binding (the NaN smuggle). Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ApplyNarrowed is applyNarrowed in the TS source: apply one narrowing
// to what is known — refusing the NaN smuggle: a refutation narrows
// nothing about an unknown binding. Where the narrowing names a key, it
// rebuilds the object around that key.
func ApplyNarrowed(known abstractdomain.AbstractValue, n Narrowed) abstractdomain.AbstractValue {
	return narrowAt(known, n.Path, n)
}

// keepTruthy keeps only the truthy words of a finite list; NaN leaves
// through the wrapper (ToBoolean of NaN is false, so held truth
// refutes it). Shapes without a finite word list pass untouched.
func keepTruthy(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyNaN {
		return keepTruthy(*known.Inner)
	}
	if known.Kind == abstractdomain.KindValues &&
		(known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean) {
		var kept []float64
		for _, v := range known.Values {
			if v != 0 {
				kept = append(kept, v)
			}
		}
		return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
	}
	return known
}

// keepFalsy keeps only the falsy words; absence and NaN both stay —
// each is false under ToBoolean.
func keepFalsy(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindValues &&
		(known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean) {
		var kept []float64
		for _, v := range known.Values {
			if v == 0 {
				kept = append(kept, v)
			}
		}
		return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
	}
	return known
}

// variantConsistent is whether one variant's held knowledge can coexist
// with the test that HELD — a variant it contradicts is not the runtime
// shape. Unreadable combinations answer true: no claim, no discard.
func variantConsistent(variant abstractdomain.AbstractValue, path []string, n Narrowed) bool {
	if variant.Kind != abstractdomain.KindObject {
		return true
	}
	key, rest := path[0], path[1:]
	held, foundHeld := objectKeyValue(variant, key)
	if !foundHeld {
		// a complete variant without the key holds it absent
		return !variant.Complete || consistentAtLeaf(abstractdomain.Undef, n)
	}
	if len(rest) > 0 {
		return variantConsistent(held, rest, n)
	}
	return consistentAtLeaf(held, n)
}

// objectKeyValue reads an object AbstractValue's key by name — the
// ordered-slice equivalent of the TS record's `keys[key]`.
func objectKeyValue(obj abstractdomain.AbstractValue, key string) (abstractdomain.AbstractValue, bool) {
	for _, k := range obj.Keys {
		if k.Name == key {
			return k.Value, true
		}
	}
	return abstractdomain.AbstractValue{}, false
}

func consistentAtLeaf(held abstractdomain.AbstractValue, n Narrowed) bool {
	present := held
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present = *held.Inner
	}
	var words []float64
	hasWords := present.Kind == abstractdomain.KindValues &&
		(present.KindTag == abstractdomain.PrimitiveNumber || present.KindTag == abstractdomain.PrimitiveBoolean)
	if hasWords {
		words = present.Values
	}
	if n.Definedness == "defined" || n.Truthiness == "truthy" {
		if held.Kind == abstractdomain.KindUndef {
			return false
		}
		if n.Truthiness == "truthy" && hasWords {
			for _, v := range words {
				if v != 0 {
					return true
				}
			}
			return false
		}
		return true
	}
	if n.Definedness == "undefined" {
		return held.Kind == abstractdomain.KindUndef || held.Kind == abstractdomain.KindPossiblyUndefined ||
			held.Kind == abstractdomain.KindUnknown
	}
	if n.Truthiness == "falsy" {
		if held.Kind == abstractdomain.KindUndef || held.Kind == abstractdomain.KindPossiblyUndefined {
			return true
		}
		if hasWords {
			for _, v := range words {
				if v == 0 {
					return true
				}
			}
			return false
		}
		if present.Kind == abstractdomain.KindObject || present.Kind == abstractdomain.KindList ||
			present.Kind == abstractdomain.KindCollection {
			return false // objects are always truthy
		}
		return true
	}
	exactSort := n.ExactSort
	if exactSort == "" {
		exactSort = abstractdomain.PrimitiveNumber
	}
	if n.Exact != nil && hasWords && exactSort != abstractdomain.PrimitiveString {
		for _, v := range words {
			if floatsInclude(n.Exact, v) {
				return true
			}
		}
		return false
	}
	return true
}

func floatsInclude(xs []float64, v float64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// meetShape meets what is held with the shape a type test proved. An
// unknown takes the shape whole; an object gains the keys it lacked;
// every sharper fact stands, because the shape is the weakest thing the
// test proves. A COMPLETE object is left alone — its key set is already
// a theorem, and `in` walks the prototype chain.
func meetShape(held, shape abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// holding proves presence: typeof, instanceof, and `in` all answer
	// false (or "undefined") for the absent value
	present := held
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present = *held.Inner
	}
	// a SORT UNION keeps the arms the proved shape's sort names — a
	// held `typeof v === "string"` discards the number arm outright
	// (number and boolean stay bucketed together: their words share
	// the ground)
	if present.Kind == abstractdomain.KindKindUnion {
		wanted := abstractdomain.KindOfClaim(shape)
		if wanted == abstractdomain.ClaimSortNone {
			return present
		}
		same := func(x abstractdomain.ClaimSort) bool {
			if x == wanted {
				return true
			}
			return x != abstractdomain.ClaimSortNone &&
				(x == abstractdomain.ClaimSortNumber || x == abstractdomain.ClaimSortBoolean) &&
				(wanted == abstractdomain.ClaimSortNumber || wanted == abstractdomain.ClaimSortBoolean)
		}
		var kept []abstractdomain.AbstractValue
		for _, arm := range present.Arms {
			s := abstractdomain.KindOfClaim(arm)
			// a SEQUENCE-SHAPED SET denotes a string OR an array — the
			// same tuple encoding — so a "string"-classified set arm
			// survives an object want: shedding it dropped real arrays
			// through `typeof v === 'object'` guards (prisma's
			// collectSelectRefs froze its isArray branch dead)
			if arm.Kind == abstractdomain.KindSet && s == abstractdomain.ClaimSortString && wanted == abstractdomain.ClaimSortObject {
				kept = append(kept, arm)
				continue
			}
			if s == abstractdomain.ClaimSortNone || same(s) {
				kept = append(kept, arm)
			}
		}
		if len(kept) > 0 {
			return abstractdomain.KindUnionOf(kept)
		}
		return shape
	}
	// an OPAQUE value already says everything this file determines, and
	// a bare shape says less — the provenance stands, and reads through
	// it stay determined
	if present.Kind == abstractdomain.KindUnknown && present.Opaque {
		return present
	}
	if present.Kind == abstractdomain.KindUnknown {
		return shape
	}
	if present.Kind == abstractdomain.KindObject && shape.Kind == abstractdomain.KindObject && !present.Complete {
		keys := append([]abstractdomain.ObjectKey{}, present.Keys...)
		for _, sk := range shape.Keys {
			if _, found := objectKeyValue(present, sk.Name); !found {
				keys = append(keys, sk)
			}
		}
		merged := abstractdomain.KnownObject(keys, nil, present.Complete, abstractdomain.TrustLevelOf(present), present.BareProto)
		// what was held is the sharper side: only ITS brand ambiguity
		// survives the meet (the shape's ground proves nothing sharper)
		if present.MaybeArray && merged.Kind == abstractdomain.KindObject {
			merged.MaybeArray = true
		}
		return merged
	}
	return present
}

// excludeKind drops the arms a refuted type test rules out. The value
// may still be absent (falsity proves no presence), so a maybe wrapper
// stays; every arm whose claim speaks EXACTLY the refuted kind goes. An
// emptied union claims nothing (the branch is dead — kindUnion of
// nothing is the honest unknown).
func excludeKind(known abstractdomain.AbstractValue, kind string) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		return abstractdomain.PossiblyUndefined(excludeKind(*known.Inner, kind), "", false, known.ProvedAbsent)
	}
	if known.Kind != abstractdomain.KindKindUnion {
		return known
	}
	var kept []abstractdomain.AbstractValue
	changed := false
	for _, arm := range known.Arms {
		// the sequence ambiguity again: a set-shaped arm classified
		// "string" may be an array, so neither a refuted string test
		// nor a refuted object test proves it gone
		if arm.Kind == abstractdomain.KindSet && abstractdomain.KindOfClaim(arm) == abstractdomain.ClaimSortString &&
			(kind == "string" || kind == "object") {
			kept = append(kept, arm)
			continue
		}
		if string(abstractdomain.KindOfClaim(arm)) != kind {
			kept = append(kept, arm)
		} else {
			changed = true
		}
	}
	if !changed {
		return known
	}
	return abstractdomain.KindUnionOf(kept)
}

// unionOfWords is the union set spelling exactly these words.
func unionOfWords(words [][]float64) refinementsets.RefinedSet {
	set := refinementsets.StringTuple(runesOf(words[0]))
	for _, w := range words[1:] {
		set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(runesOf(w))))
	}
	return set
}

// runesOf is String.fromCodePoint(...words) — the TS source builds the
// word's string back from its codepoint tuple before re-spelling it as
// a StringTuple; this does the same via Go runes.
func runesOf(points []float64) string {
	runes := make([]rune, len(points))
	for i, p := range points {
		runes[i] = rune(int32(p))
	}
	return string(runes)
}

// sameWord reports whether two word tuples spell the same codepoints.
func sameWord(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keepWordSet keeps only the WORDS a held literal disjunction admits:
// exact word-listable claims intersect, a kind union drops arms sharing
// no word, and anything the word reading cannot spell keeps its own
// facts. Holding the test proves presence.
func keepWordSet(known abstractdomain.AbstractValue, wordSet [][]float64) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		return keepWordSet(*known.Inner, wordSet)
	}
	if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone {
		words, ok := refinementsets.WordTuplesOf(known.Set)
		if !ok {
			// the prior claim spells no word list (a plain string, a
			// pattern ground) — the held disjunction alone pins the value:
			// whatever it was before, it is now one of the admitted words
			return abstractdomain.KnownSet(unionOfWords(wordSet), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
		}
		var kept [][]float64
		for _, w := range words {
			for _, s := range wordSet {
				if sameWord(s, w) {
					kept = append(kept, w)
					break
				}
			}
		}
		if len(kept) == 0 || len(kept) == len(words) {
			return known
		}
		return abstractdomain.KnownSet(unionOfWords(kept), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
	}
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveString {
		// one exact word: in the admitted set it stays; outside, the
		// branch cannot run with it — but proving THAT is the caller's
		// dead-branch machinery, so the word passes untouched
		return known
	}
	if known.Kind == abstractdomain.KindKindUnion {
		armWords := func(arm abstractdomain.AbstractValue) ([][]float64, bool) {
			return armWordsOf(arm)
		}
		var kept []abstractdomain.AbstractValue
		changed := false
		for _, arm := range known.Arms {
			words, ok := armWords(arm)
			if !ok {
				kept = append(kept, arm)
				continue
			}
			admitted := false
			for _, w := range words {
				for _, s := range wordSet {
					if sameWord(s, w) {
						admitted = true
						break
					}
				}
				if admitted {
					break
				}
			}
			if admitted {
				kept = append(kept, arm)
			} else {
				changed = true
			}
		}
		if !changed || len(kept) == 0 {
			mapped := make([]abstractdomain.AbstractValue, len(known.Arms))
			for i, arm := range known.Arms {
				mapped[i] = keepWordSet(arm, wordSet)
			}
			return abstractdomain.KindUnionOf(mapped)
		}
		mapped := make([]abstractdomain.AbstractValue, len(kept))
		for i, arm := range kept {
			mapped[i] = keepWordSet(arm, wordSet)
		}
		return abstractdomain.KindUnionOf(mapped)
	}
	return known
}

// armWordsOf reads one kind-union arm's words: a NESTED union's words
// are its arms' words — the two-alias seeding builds
// kindUnion(kindUnion(words), kindUnion(words)).
func armWordsOf(arm abstractdomain.AbstractValue) ([][]float64, bool) {
	if arm.Kind == abstractdomain.KindValues && arm.KindTag == abstractdomain.PrimitiveString {
		return [][]float64{arm.Values}, true
	}
	if arm.Kind == abstractdomain.KindSet && arm.SetKindTag == abstractdomain.SetKindTagNone {
		return refinementsets.WordTuplesOf(arm.Set)
	}
	if arm.Kind == abstractdomain.KindKindUnion {
		var collected [][]float64
		for _, inner := range arm.Arms {
			words, ok := armWordsOf(inner)
			if !ok {
				return nil, false
			}
			collected = append(collected, words...)
		}
		return collected, true
	}
	return nil, false
}

// dropWordSet sheds the WORDS a refuted literal disjunction excludes —
// the mirror of keepWordSet. Falsity proves no presence, so a maybe
// wrapper stays.
func dropWordSet(known abstractdomain.AbstractValue, excluded [][]float64) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		return abstractdomain.PossiblyUndefined(dropWordSet(*known.Inner, excluded), "", false, known.ProvedAbsent)
	}
	if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone {
		words, ok := refinementsets.WordTuplesOf(known.Set)
		if !ok {
			return known
		}
		var kept [][]float64
		for _, w := range words {
			excludedHere := false
			for _, s := range excluded {
				if sameWord(s, w) {
					excludedHere = true
					break
				}
			}
			if !excludedHere {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 || len(kept) == len(words) {
			return known
		}
		return abstractdomain.KnownSet(unionOfWords(kept), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
	}
	if known.Kind == abstractdomain.KindKindUnion {
		var kept []abstractdomain.AbstractValue
		for _, arm := range known.Arms {
			words, ok := armWordsOf(arm)
			if !ok {
				kept = append(kept, arm)
				continue
			}
			survives := false
			for _, w := range words {
				excludedHere := false
				for _, s := range excluded {
					if sameWord(s, w) {
						excludedHere = true
						break
					}
				}
				if !excludedHere {
					survives = true
					break
				}
			}
			if survives {
				kept = append(kept, arm)
			}
		}
		if len(kept) == 0 {
			return known
		}
		mapped := make([]abstractdomain.AbstractValue, len(kept))
		for i, arm := range kept {
			mapped[i] = dropWordSet(arm, excluded)
		}
		return abstractdomain.KindUnionOf(mapped)
	}
	return known
}

func narrowAt(known abstractdomain.AbstractValue, path []string, n Narrowed) abstractdomain.AbstractValue {
	if len(path) == 0 {
		if n.HasWordSet {
			return keepWordSet(known, n.WordSet)
		}
		if n.HasWordSetExcluded {
			return dropWordSet(known, n.WordSetExcluded)
		}
		if n.ExcludesKind != "" {
			return excludeKind(known, n.ExcludesKind)
		}
		if n.HasShape {
			return meetShape(known, n.Shape)
		}
		if n.Definedness == "defined" {
			present := known
			if known.Kind == abstractdomain.KindPossiblyUndefined {
				present = *known.Inner
			}
			if n.Truthiness == "truthy" {
				return keepTruthy(present)
			}
			return present
		}
		if n.Definedness == "undefined" {
			if known.Kind == abstractdomain.KindPossiblyUndefined || known.Kind == abstractdomain.KindUndef {
				return abstractdomain.Undef
			}
			return known
		}
		if n.Exact != nil {
			exactSort := n.ExactSort
			if exactSort == "" {
				exactSort = abstractdomain.PrimitiveNumber
			}
			return abstractdomain.KnownValues(append([]float64{}, n.Exact...), exactSort, abstractdomain.TrustProved)
		}
		if n.Truthiness == "falsy" {
			return keepFalsy(known)
		}
		if n.Refuting && known.Kind == abstractdomain.KindPossiblyNaN {
			// NaN fails every comparison, so a REFUTED comparison keeps
			// it: the real part narrows, NaN rides on
			return abstractdomain.PossiblyNaN(ApplyNarrowed(*known.Inner, n))
		}
		if n.Refuting && (known.Kind == abstractdomain.KindUnknown || known.Kind == abstractdomain.KindPossiblyUndefined || known.Kind == abstractdomain.KindUndef) {
			return known
		}
		// a refuted WORD equality arrives as difference(C*, word): the
		// literal-union narrowing again, single-word this time — a
		// word-listable prior claim sheds exactly the removed words and
		// the survivors are spelled plainly. The stacked difference would
		// blind the pattern prover downstream (it reads one shape, not an
		// intersection), and the placement ask below cannot help: C*
		// minus a word sits inside no finite union.
		if len(n.Forms) > 0 && allDifferenceOfStringGround(n.Forms) {
			var removed [][]float64
			readable := true
			for _, f := range n.Forms {
				if f.Form != refinementsets.FormDifference {
					continue
				}
				words, ok := refinementsets.WordTuplesOf(*f.B)
				if !ok {
					readable = false
					break
				}
				removed = append(removed, words...)
			}
			if readable && len(removed) > 0 {
				dropped := dropWordSet(known, removed)
				if !abstractdomain.SameKnown(dropped, known) {
					return dropped
				}
			}
		}
		// a PATTERN-form narrowing over held sequence knowledge: stacked
		// forms have no reading in the kernel's pattern prover, but where
		// the PROVED subset says the narrowing's set already sits inside
		// what is held, the narrowing IS the intersection — spelled
		// plainly, so the pattern questions downstream stay askable.
		// (`strings ∩ startsWith("/")` is startsWith("/"); the kernel
		// decides the containment, this code only asks.)
		if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone &&
			NarrowKernel() != nil && len(n.Forms) > 0 && everySequenceForm(n.Forms) {
			// C* is the whole string ground: as a CONJUNCT beside other
			// sequence forms it adds nothing, and dropping a conjunct only
			// weakens a claim (value ∈ A ∩ C* implies value ∈ A) — while
			// keeping it stacked blinds the pattern prover, which reads one
			// shape, not an intersection
			candidate := refinementsets.MakeRefinedSet(refinementsets.WithoutStringGround(n.Forms)...)
			if refinementsets.IsStringGround(known.Set) {
				return abstractdomain.KnownSet(candidate, nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
			}
			if subset, refused := seqSubsetRefusable(candidate, known.Set); !refused && subset {
				return abstractdomain.KnownSet(candidate, nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
			}
		}
		return shedRemovedEndpoints(abstractdomain.NarrowKnown(known, n.Forms))
	}
	// a PATH narrowing that proves its leaf present or truthy proves
	// the ROOT present too — `o?.n` truthy required o to be there —
	// so the maybe strips and the walk continues inside
	if known.Kind == abstractdomain.KindPossiblyUndefined && (n.Definedness == "defined" || n.Truthiness == "truthy") {
		return narrowAt(*known.Inner, path, n)
	}
	// a keyed EXACT test on a SORT UNION of objects selects arms: an
	// arm whose key at the path provably differs from the pinned word
	// sheds, and the survivors narrow on — the discriminated-union
	// split for plain object-type unions.
	//
	// The step below reads a segment as an OBJECT KEY. An index segment
	// finds no such key, so the step answers not-reached and the arm is
	// KEPT — a list arm under an index shape sheds nothing, which is the
	// answer that discards no runtime value.
	if known.Kind == abstractdomain.KindKindUnion && n.Exact != nil {
		pinned := n.Exact
		var kept []abstractdomain.AbstractValue
		for _, arm := range known.Arms {
			held := &arm
			for _, step := range path {
				if held != nil && held.Kind == abstractdomain.KindObject {
					v, found := objectKeyValue(*held, step)
					if found {
						held = &v
					} else {
						held = nil
					}
				} else {
					held = nil
				}
			}
			if held == nil || held.Kind != abstractdomain.KindValues {
				kept = append(kept, arm)
				continue
			}
			exactSort := n.ExactSort
			if exactSort == "" {
				exactSort = abstractdomain.PrimitiveNumber
			}
			if exactSort != held.KindTag {
				kept = append(kept, arm)
				continue
			}
			if len(held.Values) == len(pinned) && sameWord(held.Values, pinned) {
				kept = append(kept, arm)
			}
		}
		if len(kept) > 0 && len(kept) != len(known.Arms) {
			return narrowAt(abstractdomain.KindUnionOf(kept), path, n)
		}
		return known
	}
	// a LIST root under an INDEX segment: the narrowing speaks about one
	// slot, and the list carries its items positionally, so the item at
	// that slot narrows in place and the rest stay as they were. A slot
	// past the items says nothing — the list holds no value there for the
	// narrowing to sharpen.
	//
	// The in-range slot needs no absence wrapper: a KindList is hole-free
	// by construction (walk/element_access.go states the argument — every
	// builder writes each slot from its own walked source, an elision
	// writes Undef, and an index write retires the whole receiver rather
	// than growing it). Narrowing an Undef slot is a no-op, which is the
	// right answer for a hole.
	if known.Kind == abstractdomain.KindList {
		slot, isIndex := dataflowfacts.IndexSegmentOf(path[0])
		if !isIndex || slot >= len(known.Items) {
			return known
		}
		items := make([]abstractdomain.AbstractValue, len(known.Items))
		copy(items, known.Items)
		items[slot] = narrowAt(items[slot], path[1:], n)
		return abstractdomain.KnownList(items, abstractdomain.TrustLevelOf(known))
	}
	if known.Kind != abstractdomain.KindObject {
		return known
	}
	// an INDEX segment names a list slot, and an object carries no such
	// slot — an object under an index segment stays as it was rather than
	// having the bracket text read as one of its keys
	if _, isIndex := dataflowfacts.IndexSegmentOf(path[0]); isIndex {
		return known
	}
	// a discriminated object narrows through its variants first: the
	// test that held discards every variant it contradicts, and a
	// lone survivor IS the runtime shape
	if known.Variants != nil {
		var survivors []abstractdomain.AbstractValue
		for _, v := range known.Variants {
			if variantConsistent(v, path, n) {
				survivors = append(survivors, v)
			}
		}
		if len(survivors) == 1 {
			return narrowAt(survivors[0], path, n)
		}
		if len(survivors) > 1 && len(survivors) < len(known.Variants) {
			joint := narrowKeyed(known, path, n)
			if joint.Kind == abstractdomain.KindObject {
				joint.Variants = survivors
			}
			return joint
		}
	}
	return narrowKeyed(known, path, n)
}

// allDifferenceOfStringGround reports whether every form is a
// difference whose A side is the string ground — `n.forms.every((f) =>
// f.form === "difference" && isStringGround(f.A))` in the TS source.
func allDifferenceOfStringGround(forms []refinementsets.Refinement) bool {
	for _, f := range forms {
		if f.Form != refinementsets.FormDifference || !refinementsets.IsStringGround(*f.A_) {
			return false
		}
	}
	return true
}

// everySequenceForm reports whether every form is one of the sequence
// forms the pattern channel reads.
func everySequenceForm(forms []refinementsets.Refinement) bool {
	for _, f := range forms {
		switch f.Form {
		case refinementsets.FormConcatenation, refinementsets.FormStar, refinementsets.FormRepeat,
			refinementsets.FormDifference, refinementsets.FormUnion, refinementsets.FormEmptyTuple:
			// on the reading
		default:
			return false
		}
	}
	return true
}

// seqSubsetRefusable asks the kernel's SeqSubset question, turning a
// refusal into an (answer, refused) pair — the TS source's try/catch
// around `narrowKernel.seqSubset(...)`.
func seqSubsetRefusable(a, b refinementsets.RefinedSet) (subset bool, refused bool) {
	defer func() {
		if recover() != nil {
			subset, refused = false, true
		}
	}()
	return NarrowKernel().SeqSubset(a, b), false
}

// shedRemovedEndpoints is the port of shedRemovedEndpoints in the TS
// source: a stacked difference that removes exactly a CLOSED INTEGER
// ENDPOINT of its own set is the bumped bound — {integer, ≥ −1, ≤ 2}
// minus {−1} IS {integer, ≥ 0, ≤ 2} — and the grid has nothing between.
// Said plainly here because the enclosure the transfers read keeps only
// closed bounds, so the stacked spelling would keep a refuted sentinel
// (`i === -1 ? … : len - i`) alive downstream. Repeats to a fixpoint:
// removing −1 can expose a removed 0.
func shedRemovedEndpoints(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind != abstractdomain.KindSet || known.SetKindTag != abstractdomain.SetKindTagNone {
		return known
	}
	original := known.Set.Forms
	hasInteger := false
	for _, f := range original {
		if f.Form == refinementsets.FormInteger {
			hasInteger = true
			break
		}
	}
	if !hasInteger {
		return known
	}
	var lo, hi *float64
	hasLo, hasHi := false, false
	for _, f := range original {
		if f.Form == refinementsets.FormAtLeast && !hasLo {
			v := f.A
			lo = &v
			hasLo = true
		}
		if f.Form == refinementsets.FormAtMost && !hasHi {
			v := f.A
			hi = &v
			hasHi = true
		}
	}
	// every removed point riding a plain difference-of-oneOf; the
	// bounds walk in from removed endpoints to a fixpoint
	removed := map[float64]struct{}{}
	for _, f := range original {
		if f.Form != refinementsets.FormDifference {
			continue
		}
		if len(f.B.Forms) != 1 || f.B.Forms[0].Form != refinementsets.FormOneOf {
			continue
		}
		for _, w := range f.B.Forms[0].W {
			removed[w] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return known
	}
	consumed := map[float64]struct{}{}
	for round := 0; round < len(removed)+1; round++ {
		moved := false
		if hasLo {
			if _, isRemoved := removed[*lo]; isRemoved {
				if _, isConsumed := consumed[*lo]; !isConsumed {
					consumed[*lo] = struct{}{}
					v := *lo + 1
					lo = &v
					moved = true
				}
			}
		}
		if hasHi {
			if _, isRemoved := removed[*hi]; isRemoved {
				if _, isConsumed := consumed[*hi]; !isConsumed {
					consumed[*hi] = struct{}{}
					v := *hi - 1
					hi = &v
					moved = true
				}
			}
		}
		if !moved {
			break
		}
	}
	if len(consumed) == 0 {
		return known
	}
	var forms []refinementsets.Refinement
	for _, f := range original {
		if f.Form == refinementsets.FormAtLeast && hasLo {
			forms = append(forms, refinementsets.AtLeast(*lo))
			continue
		}
		if f.Form == refinementsets.FormAtMost && hasHi {
			forms = append(forms, refinementsets.AtMost(*hi))
			continue
		}
		if f.Form != refinementsets.FormDifference {
			forms = append(forms, f)
			continue
		}
		if len(f.B.Forms) != 1 || f.B.Forms[0].Form != refinementsets.FormOneOf {
			forms = append(forms, f)
			continue
		}
		only := f.B.Forms[0]
		var kept []float64
		for _, w := range only.W {
			if _, isConsumed := consumed[w]; !isConsumed {
				kept = append(kept, w)
			}
		}
		if len(kept) == len(only.W) {
			forms = append(forms, f)
			continue
		}
		if len(kept) > 0 {
			rebuilt := f
			b := refinementsets.MakeRefinedSet(refinementsets.OneOf(kept))
			rebuilt.B = &b
			forms = append(forms, rebuilt)
			continue
		}
		if len(f.A_.Forms) != 0 {
			forms = append(forms, f)
		}
	}
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(forms...), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
}

func narrowKeyed(known abstractdomain.AbstractValue, path []string, n Narrowed) abstractdomain.AbstractValue {
	key, rest := path[0], path[1:]
	held, found := objectKeyValue(known, key)
	if !found {
		return known
	}
	narrowed := narrowAt(held, rest, n)
	// a narrowed object is no longer exactly the stated annotation;
	// the rebuild keeps the object's grade ceiling — and its bare
	// prototype, or a later missing-key read would falsely claim the
	// inherited function
	keys := make([]abstractdomain.ObjectKey, len(known.Keys))
	copy(keys, known.Keys)
	replaced := false
	for i, k := range keys {
		if k.Name == key {
			keys[i] = abstractdomain.ObjectKey{Name: key, Value: narrowed}
			replaced = true
			break
		}
	}
	if !replaced {
		keys = append(keys, abstractdomain.ObjectKey{Name: key, Value: narrowed})
	}
	return abstractdomain.KnownObject(keys, nil, known.Complete, abstractdomain.TrustLevelOf(known), known.BareProto)
}
