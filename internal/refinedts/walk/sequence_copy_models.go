// from evaluation/sequence_copy_models.ts
//
// Sequence-copy fallbacks under the read-only gate: concat (exact and
// windowed), .at, and slice / toSorted / toReversed copies with
// surviving measures.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readSequenceCopyMethods is readSequenceCopyMethods in the TS
// source: concat / at / slice / toSorted / toReversed after the exact
// readers decline. Nil when the method is none of those.
func readSequenceCopyMethods(site MethodCallSite, argKnowns []abstractdomain.AbstractValue, receiverStringy bool) *abstractdomain.AbstractValue {
	ctx, receiverExpression, receiver, method := site.Ctx, site.ReceiverExpression, site.Receiver, site.Method
	if method == "concat" && receiver.Kind == abstractdomain.KindValues {
		allValues := true
		for _, k := range argKnowns {
			if k.Kind != abstractdomain.KindValues {
				allValues = false
				break
			}
		}
		if allValues {
			// a string receiver concatenates string words; an array
			// receiver appends numbers and flattens arrays — a sort mix
			// would smuggle one word into the other reading
			argsFit := true
			for _, k := range argKnowns {
				if receiverStringy {
					if k.KindTag != abstractdomain.PrimitiveString {
						argsFit = false
						break
					}
				} else if !(k.KindTag == abstractdomain.PrimitiveNumber || k.KindTag == abstractdomain.PrimitiveArray) {
					argsFit = false
					break
				}
			}
			if !argsFit {
				out := silence.Residue()
				return &out
			}
			joined := append([]float64{}, receiver.Values...)
			for _, k := range argKnowns {
				joined = append(joined, k.Values...)
			}
			var kindTag abstractdomain.PrimitiveKind
			if receiverStringy {
				kindTag = abstractdomain.PrimitiveString
			} else {
				kindTag = abstractdomain.PrimitiveArray
			}
			out := abstractdomain.KnownValues(joined, kindTag, abstractdomain.TrustProved)
			return &out
		}
	}
	// a WINDOWED array concat: the result's length is the sum of the
	// parts' windows and every element is some part's element
	// (sec-array.prototype.concat) — exact argument arrays count their
	// items, windowed arguments add their edges
	if method == "concat" && !receiverStringy && receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone {
		base, baseOk := refinementsets.AsRepetition(receiver.Set)
		if baseOk {
			lo := base.Lo
			var hi *int
			if base.Hi != nil {
				h := *base.Hi
				hi = &h
			}
			elements := []refinementsets.RefinedSet{base.Element}
			readable := true
			for _, k := range argKnowns {
				if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveArray {
					lo += len(k.Values)
					if hi != nil {
						h := *hi + len(k.Values)
						hi = &h
					}
					if len(k.Values) > 0 {
						elements = append(elements, refinementsets.MakeRefinedSet(refinementsets.OneOf(append([]float64{}, k.Values...))))
					}
					continue
				}
				var rep *refinementsets.Repeated
				if k.Kind == abstractdomain.KindSet && k.SetKindTag == abstractdomain.SetKindTagNone {
					if r, ok := refinementsets.AsRepetition(k.Set); ok {
						rep = &r
					}
				}
				if rep != nil {
					lo += rep.Lo
					if hi == nil || rep.Hi == nil {
						hi = nil
					} else {
						h := *hi + *rep.Hi
						hi = &h
					}
					elements = append(elements, rep.Element)
					continue
				}
				readable = false
				break
			}
			if readable {
				element, ok := unionAll(elements)
				if !ok {
					operands := append([]abstractdomain.AbstractValue{receiver}, argKnowns...)
					out := abstractdomain.UnknownOver(operands)
					return &out
				}
				out := abstractdomain.KnownSet(refinementsets.Repetition(element, lo, hi), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
				return &out
			}
		}
	}
	if method == "at" && receiver.Kind == abstractdomain.KindValues && len(argKnowns) == 1 &&
		argKnowns[0].Kind == abstractdomain.KindValues && len(argKnowns[0].Values) == 1 {
		// `.at` on a string is unit-indexed — exact only astral-free
		if receiverStringy && !refinementsets.AstralFree(receiver.Values) {
			out := silence.Residue()
			return &out
		}
		asked := argKnowns[0].Values[0]
		index := asked
		if asked < 0 {
			index = float64(len(receiver.Values)) + asked
		}
		if index == float64(int64(index)) && index >= 0 && int(index) < len(receiver.Values) {
			var kindTag abstractdomain.PrimitiveKind
			if receiverStringy {
				kindTag = abstractdomain.PrimitiveString
			} else {
				kindTag = abstractdomain.PrimitiveNumber
			}
			// a string's element is a 1-character STRING
			out := abstractdomain.KnownValues([]float64{receiver.Values[int(index)]}, kindTag, abstractdomain.TrustProved)
			return &out
		}
		out := silence.Residue()
		return &out
	}
	// `.at(i)` on a receiver the walk holds only as an ELEMENT claim —
	// a repetition-shaped set (`number[]` seeded from its type), an
	// object-star, a list. sec-array.prototype.at returns undefined
	// when the absolute index falls outside [0, length) and otherwise
	// `Get(obj, ToString(k))`, which for such a receiver is exactly the
	// element it states at every position. So the read is the element
	// beside absence, whatever the index is — no in-bounds proof is
	// needed to answer it, because the out-of-range branch is the
	// absence the wrapper already carries.
	//
	// Placed after the exact-tuple arm above, which pins the exact
	// element when the receiver's own values are known.
	if method == "at" && len(argKnowns) == 1 && !receiverStringy {
		element := ElementOf(receiver)
		if element.Kind != abstractdomain.KindUnknown {
			// sec-array.prototype.at step 4 returns exactly *undefined*
			// out of range — never null, so the wrapper's absent side is
			// UndefOnly, and it is POSITIVELY derived from that step
			// rather than standing in for an unread index.
			out := abstractdomain.PossiblyAbsent(
				element, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true,
			)
			return &out
		}
	}
	if method == "slice" || method == "toSorted" || method == "toReversed" {
		// a string slice is unit-indexed and can split a surrogate pair,
		// leaving the model's sets — sound only astral-free
		if method == "slice" && primitives.IsStringKind(ctx.P.Checker, receiverExpression) &&
			!(receiver.Kind == abstractdomain.KindValues && refinementsets.AstralFree(receiver.Values)) {
			out := silence.Residue()
			return &out
		}
		// members survive. The SORTED measure survives slice (a
		// subsequence of a non-decreasing sequence is non-decreasing);
		// toSorted does NOT establish it — its default comparator is
		// lexicographic — and toReversed reverses it away. A sum
		// survives none of them.
		element, elementOk := abstractdomain.SetOfKnown(ElementOf(receiver))
		if !elementOk {
			operands := append([]abstractdomain.AbstractValue{receiver}, argKnowns...)
			out := abstractdomain.UnknownOver(operands)
			return &out
		}
		sorted := method == "slice" && receiver.Kind == abstractdomain.KindSet &&
			receiver.Measures != nil && receiver.Measures.Sorted
		// LENGTHS: toSorted/toReversed copy whole, and a slice with
		// start 0 (or no arguments) and no end keeps every element
		// (sec-array.prototype.slice: k = 0, final = len), so those keep
		// the receiver's repetition window; a nonnegative exact start k
		// drops at most k elements, shifting the window's edges down by
		// k with a floor at 0. Anything else stars.
		var held *refinementsets.Repeated
		if receiver.Kind == abstractdomain.KindSet {
			if r, ok := refinementsets.AsRepetition(receiver.Set); ok {
				held = &r
			}
		}
		start := 0
		startOk := true
		if method == "slice" && len(argKnowns) > 0 {
			if argKnowns[0].Kind == abstractdomain.KindValues && argKnowns[0].KindTag == abstractdomain.PrimitiveNumber &&
				len(argKnowns[0].Values) == 1 && argKnowns[0].Values[0] == float64(int64(argKnowns[0].Values[0])) {
				start = int(argKnowns[0].Values[0])
			} else {
				startOk = false
			}
		}
		var window *refinementsets.Repeated
		if held != nil && len(argKnowns) <= 1 && startOk && start >= 0 {
			lo := held.Lo - start
			if lo < 0 {
				lo = 0
			}
			var hi *int
			if held.Hi != nil {
				h := *held.Hi - start
				if h < 0 {
					h = 0
				}
				hi = &h
			}
			window = &refinementsets.Repeated{Element: held.Element, Lo: lo, Hi: hi}
		}
		var built abstractdomain.AbstractValue
		if window == nil {
			built = abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Star(element)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
		} else {
			built = abstractdomain.KnownSet(refinementsets.Repetition(element, window.Lo, window.Hi), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
		}
		var measures *abstractdomain.Measures
		if sorted {
			measures = &abstractdomain.Measures{Sorted: true}
		}
		out := abstractdomain.KnownWithMeasures(built, measures)
		return &out
	}
	return nil
}

// unionAll folds MakeRefinedSet(Union(a, b)) left to right over a
// non-empty slice of sets, mirroring the TS source's reduce with a
// try/catch around Union's own construction guard — (RefinedSet{},
// false) on the empty input or a union that construction refuses.
func unionAll(sets []refinementsets.RefinedSet) (refinementsets.RefinedSet, bool) {
	if len(sets) == 0 {
		return refinementsets.RefinedSet{}, false
	}
	element := sets[0]
	for _, next := range sets[1:] {
		element = refinementsets.MakeRefinedSet(refinementsets.Union(element, next))
	}
	return element, true
}
