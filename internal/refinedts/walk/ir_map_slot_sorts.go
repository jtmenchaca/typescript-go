// split from ir_map_slots.go — the seed-read sorts and typeof evidence
//
// What sort a flattened collection's value and key slots wear, and what
// typeof answers for them, read from the seed rows' syntax alone.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// MapValueSort is a flattened collection's VALUE sort, read from its
// seed rows the way ArrayElementSort reads an array literal's elements:
// every seeded value string-shaped by syntax makes a string slot;
// anything else the lowering reads numerically. An EMPTY collection has
// no value to read, so it takes the number sort the sets and the gets
// speak.
//
// A PRODUCER (`const u = a.union(b)`) has no seed rows of its own; its
// value sort was already resolved from its two operands at recognition
// time (setProducerMapLocalOf) and rides in ProducerValsSort.
func MapValueSort(local MapLocal) BindingKind {
	if local.ProducerMethod != "" {
		return local.ProducerValsSort
	}
	return sortOfSeedRow(local.SeedVals)
}

// MapKeySort is the same reading for a Map's KEY slot. A Set has no key
// slot and answers the number sort, which nothing consults.
func MapKeySort(local MapLocal) BindingKind {
	return sortOfSeedRow(local.SeedKeys)
}

func sortOfSeedRow(entries []*ast.Node) BindingKind {
	if len(entries) == 0 {
		return BindingKindNumber
	}
	for _, entry := range entries {
		if !SpelledSequenceShape(entry) {
			return BindingKindNumber
		}
	}
	return BindingKindString
}

// MapValueTypeof is a flattened collection's value typeof evidence,
// from the seed's syntax alone — only an all-same reading claims
// anything, exactly as ArrayElementTypeof does.
//
// A PRODUCER reads ProducerValsTypeof, resolved at recognition time —
// see MapValueSort.
func MapValueTypeof(local MapLocal) TypeofTag {
	if local.ProducerMethod != "" {
		return local.ProducerValsTypeof
	}
	return typeofOfSeedRow(local.SeedVals)
}

// MapKeyTypeof is the same reading for a Map's key slot.
func MapKeyTypeof(local MapLocal) TypeofTag {
	return typeofOfSeedRow(local.SeedKeys)
}

func typeofOfSeedRow(entries []*ast.Node) TypeofTag {
	if len(entries) == 0 {
		return TypeofTagNone
	}
	var held TypeofTag
	for index, entry := range entries {
		e := Unwrapped(entry)
		var tag TypeofTag
		switch {
		case SpelledSequenceShape(e):
			tag = TypeofTagString
		case e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword:
			tag = TypeofTagBoolean
		default:
			if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
				tag = TypeofTagNumber
			} else {
				return TypeofTagNone
			}
		}
		if index == 0 {
			held = tag
			continue
		}
		if tag != held {
			return TypeofTagNone
		}
	}
	return held
}
