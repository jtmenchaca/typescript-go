// A KNOWN object's AbstractValue → the wire's value assignment, keyed
// via the specification's own object markings (ObjectNode.Keys). The
// encoder never narrows the claim: a shape it cannot carry faithfully
// declines with a named reason and the caller keeps its local walk.
//
// What crosses: exact scalar/sequence values as tuples, candidate
// SETS as set leaves (the common case — a key known in 0..120 crosses
// as its window; the kernel judges the subset question), scalar
// candidate lists as one_of sets, and nested objects as instances.
//
// THE SORT SPLIT, and which way each miss falls:
//   - a genuine sort mismatch between an EXACT value and the stated
//     set (a string at a scalar-stated key, a number at a
//     sequence-stated key) is a REFUTATION the local walk already
//     fires (checkSetMembership's admitted-language arms) — the
//     encoder declines those so that refutation keeps firing, never
//     silencing it behind a kernel acceptance the tuple layer's one
//     root would wrongly give.
//   - a SET-valued key needs no local sort gate at all: the kernel's
//     own shape dispatch (both-scalar or both-recognized-sequence)
//     answers, and its undecided arm comes back as the 3014 fault the
//     call site reads as a decline.
//
// WORK QUEUE — each remaining decline names the missing construct and
// where it lands; none of these is a category to keep, every one is a
// construct to build:
//   - maybe/NaN-wrapped key values (KindPossiblyUndefined /
//     KindPossiblyNaN): the wrapper's absent side interacts with the
//     presence counts (an absent-admitting VALUE at a {1}-counted
//     key is neither plainly present nor plainly absent) — a real
//     design piece for the judgment's presence clause, queued.
//   - variant-carrying objects (Variants) → item (c), the
//     arm-quantified union judgment over CountGroup's skeleton.
//   - maybe-array objects (MaybeArray) → the same union staging.
//   - collection-valued keys (Map/Set) → the entry-walk boundary
//     until the collection vocabulary lands kernel-side (the pipeline
//     already omits them from Specifications).
//   - NaN-beside-set (KindSet.NaNElements) → the NaN-wrapper
//     discipline (NaN is a member of no refined set; a subset answer
//     would overclaim).
//   - temporal-charted sets (KindSet.Temporal) → the calendar lens.
//   - bigint/symbol-sorted sets (SetKindTag) → the non-double sort
//     vocabulary at the wire.
//   - nested exact sequences (KindList) and the remaining value kinds
//     (dates, promises, regexes, …) → their own lowering rules.
package objectgraphs

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// The whole-object decline reasons.
const (
	DeclineValueNotObject  = "the value is not an object"
	DeclineValueVariants   = "the object carries variant alternatives"
	DeclineValueMaybeArray = "the object may be an array"
	DeclineRootUnmarked    = "the node carries no object marking"
)

// The per-key decline reasons, each naming its key and construct.

func declineKeyCollection(name string) string {
	return "the key '" + name + "' holds a Map or Set — entries ride the entry walk"
}

func declineKeyWrapper(name string) string {
	return "the key '" + name + "' wears a maybe or NaN wrapper — the absent side meets the presence counts in its own unit"
}

func declineKeyNaNBeside(name string) string {
	return "the key '" + name + "' may be NaN beside its set — the NaN wrapper rides the local walk"
}

func declineKeyTemporal(name string) string {
	return "the key '" + name + "' wears a temporal chart — the calendar lens rides the local walk"
}

func declineKeySetSort(name string) string {
	return "the key '" + name + "' wears a bigint or symbol sort — the sort stays with the local walk"
}

func declineKeyNotCarried(name string) string {
	return "the key '" + name + "' holds a value the wire does not carry yet"
}

// declineKeySort marks the exact-value sort mismatches the LOCAL walk
// refutes today — the decline keeps that refutation firing.
func declineKeySort(name string) string {
	return "the key '" + name + "' and its stated set do not wear one sort"
}

func declineKeyObjectAtSet(name string) string {
	return "the key '" + name + "' holds an object where a plain set is stated"
}

func declineKeyValueAtObject(name string) string {
	return "the key '" + name + "' holds a plain value where an object is stated"
}

func declineKeyPath(name string) string {
	return "the key '" + name + "' names a path outside the specification"
}

// MarkingAt is the object marking at a node index, if any.
func MarkingAt(s Specification, node int) (ObjectNode, bool) {
	for _, o := range s.Objects {
		if o.Node == node {
			return o, true
		}
	}
	return ObjectNode{}, false
}

// ValueAssignmentOf encodes a KNOWN object against its specification:
// the marking's keys drive the walk — a key the instance holds
// crosses as its value (a nested instance at a marked head, a tuple
// or candidate set at a set head), a key it does not hold is omitted
// (absence is the judgment's to count), and instance keys the
// specification does not name are outside the claim and never cross.
// Returns the assignment and "" on success, or the named decline
// reason.
func ValueAssignmentOf(s Specification, root int, known abstractdomain.AbstractValue) (ValueAssignment, string) {
	tree, reason := valueNodeOf(s, root, known)
	if reason != "" {
		return ValueAssignment{}, reason
	}
	return ValueAssignment{Root: root, Value: tree}, ""
}

func valueNodeOf(s Specification, node int, known abstractdomain.AbstractValue) (ValueTree, string) {
	if known.Kind != abstractdomain.KindObject {
		return ValueTree{}, DeclineValueNotObject
	}
	if len(known.Variants) > 0 {
		return ValueTree{}, DeclineValueVariants
	}
	if known.MaybeArray {
		return ValueTree{}, DeclineValueMaybeArray
	}
	marking, marked := MarkingAt(s, node)
	if !marked {
		return ValueTree{}, DeclineRootUnmarked
	}
	// last occurrence wins — the same reading CheckObjectTarget's
	// knownKeys map takes
	byName := make(map[string]abstractdomain.AbstractValue, len(known.Keys))
	for _, k := range known.Keys {
		byName[k.Name] = k.Value
	}
	var keys []ValueTreeKey
	for _, mk := range marking.Keys {
		held, has := byName[mk.Name]
		if !has {
			continue // absence is the judgment's to count
		}
		if mk.Path < 0 || mk.Path >= len(s.Paths) {
			return ValueTree{}, declineKeyPath(mk.Name)
		}
		if held.Kind == abstractdomain.KindCollection {
			return ValueTree{}, declineKeyCollection(mk.Name)
		}
		head := s.Paths[mk.Path].Head
		if _, headMarked := MarkingAt(s, head); headMarked {
			if held.Kind != abstractdomain.KindObject {
				return ValueTree{}, declineKeyValueAtObject(mk.Name)
			}
			sub, reason := valueNodeOf(s, head, held)
			if reason != "" {
				return ValueTree{}, reason
			}
			keys = append(keys, ValueTreeKey{Name: mk.Name, Value: sub})
			continue
		}
		if head < 0 || head >= len(s.Nodes) {
			return ValueTree{}, declineKeyPath(mk.Name)
		}
		leaf, reason := keyLeafOf(mk.Name, held, s.Nodes[head])
		if reason != "" {
			return ValueTree{}, reason
		}
		keys = append(keys, ValueTreeKey{Name: mk.Name, Value: leaf})
	}
	return ValueTree{Keys: keys}, ""
}

// keyLeafOf converts one held value at a set-headed key into a leaf:
// an exact value as the tuple the membership decider walks, a known
// SET (or a scalar candidate list) as the candidate set the subset
// decider judges. Sort handling per the package comment: exact-value
// sort mismatches decline so the local walk's refutation keeps
// firing; set leaves carry no local sort gate — the kernel's shape
// dispatch answers, undecided pairs coming back as the decline fault.
func keyLeafOf(name string, held abstractdomain.AbstractValue, headSet refinementsets.RefinedSet) (ValueTree, string) {
	switch held.Kind {
	case abstractdomain.KindObject:
		return ValueTree{}, declineKeyObjectAtSet(name)
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		return ValueTree{}, declineKeyWrapper(name)
	case abstractdomain.KindSet:
		if held.NaNElements {
			return ValueTree{}, declineKeyNaNBeside(name)
		}
		if held.Temporal != nil {
			return ValueTree{}, declineKeyTemporal(name)
		}
		if held.SetKindTag != abstractdomain.SetKindTagNone {
			return ValueTree{}, declineKeySetSort(name)
		}
		return ValueTree{IsSet: true, Set: held.Set}, ""
	case abstractdomain.KindValues:
		for _, x := range held.Values {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return ValueTree{}, declineKeyNotCarried(name)
			}
		}
		switch held.KindTag {
		case abstractdomain.PrimitiveNumber, abstractdomain.PrimitiveBoolean:
			if !refinementsets.OnOneTupleLayer(headSet) {
				return ValueTree{}, declineKeySort(name)
			}
			// one candidate is one instance; several are the finite
			// candidate set, judged by the same subset question
			if len(held.Values) == 1 {
				return ValueTree{IsLeaf: true, Tuple: []float64{held.Values[0]}}, ""
			}
			return ValueTree{IsSet: true,
				Set: refinementsets.MakeRefinedSet(refinementsets.OneOf(held.Values))}, ""
		case abstractdomain.PrimitiveString, abstractdomain.PrimitiveArray:
			// a sequence-sorted KindValues IS the whole tuple
			if !refinementsets.StatesSequence(headSet) {
				return ValueTree{}, declineKeySort(name)
			}
			return ValueTree{IsLeaf: true, Tuple: held.Values}, ""
		}
		return ValueTree{}, declineKeyNotCarried(name)
	}
	return ValueTree{}, declineKeyNotCarried(name)
}
