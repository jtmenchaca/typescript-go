// from assignability/declared_value.ts
//
// A stated DeclaredRefinement → AbstractValue: the value a position
// wears when it arrives already matching what is declared. Callers
// that never check assignability still ask here (entry env, worn
// annotation, builtin models).

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// AbstractValueOfDeclared is abstractValueOfDeclared in the TS
// source: a value arriving at a stated position wears exactly what
// is stated: a set, the object annotation itself (carried by
// identity, so a value handed straight on needs no re-checking), or
// the refinement variable (opaque T — identity plus its bound).
func AbstractValueOfDeclared(stated annotations.DeclaredRefinement) abstractdomain.AbstractValue {
	switch stated.Kind {
	case annotations.DeclaredSet:
		bare := abstractdomain.KnownSet(
			*stated.Set,
			stated.Temporal,
			abstractdomain.TrustProved,
			setKindTagOf(stated.KindTag),
		)
		return abstractdomain.KnownWithMeasures(bare, measuresOf(stated.Measures))
	case annotations.DeclaredVariable:
		return abstractdomain.KnownVariable(stated.Symbol, *stated.Bound, stated.StarDepth, objectAnnotationRefOf(stated.BoundObject))
	case annotations.DeclaredPossiblyUndefined:
		inner := AbstractValueOfDeclared(*stated.Inner)
		return abstractdomain.PossiblyUndefined(inner, "", false, false)
	case annotations.DeclaredObjectArray:
		// an ARRAY OF RECORDS holds no single tracked value here —
		// element reads consult the declared statement
		// (evaluate_property_access)
		return silence.Residue()
	case annotations.DeclaredObject:
		keys := make([]abstractdomain.ObjectKey, 0, len(stated.Object.Keys))
		for _, key := range stated.Object.Keys {
			switch key.Value.Kind {
			case annotations.KeyValueSet:
				bare := abstractdomain.KnownSet(*key.Value.Set, nil, abstractdomain.TrustProved, setKindTagOf(key.Value.KindTag))
				// the key's sequence MEASURES ride the read — the exact
				// reduce total, the order — exactly as they would on the
				// unnested statement
				worn := bare
				if key.Value.Measures != nil {
					worn = abstractdomain.KnownWithMeasures(bare, measuresOf(key.Value.Measures))
				}
				// an optional or nullable key's reads wear the maybe
				// wrapper: the value may be the absent value, and a guard
				// strips it
				var value abstractdomain.AbstractValue
				if key.Value.Absent || key.MayBeAbsent {
					value = abstractdomain.PossiblyUndefined(worn, "", false, false)
				} else {
					value = worn
				}
				keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: value})
			case annotations.KeyValueObject:
				value := AbstractValueOfDeclared(annotations.DeclaredRefinement{
					Kind:   annotations.DeclaredObject,
					Object: key.Value.Object,
				})
				keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: value})
			case annotations.KeyValueReference:
				// the graph carries it, not a set
				keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: silence.Residue()})
			case annotations.KeyValueCollection:
				// TS switch only spells "set" | "object" | "reference" arms;
				// a collection value has no case in the TS source's switch
				// and falls through with no `keys[key.name]` assignment —
				// the object build below then reads it as absent. Mirrored
				// here by skipping the key entirely (no entry appended).
			}
		}
		return abstractdomain.KnownObject(keys, objectAnnotationRefOf(stated.Object), false, abstractdomain.TrustProved, false)
	}
	return abstractdomain.Unknown
}

// DeclaredOfKey is declaredOfKey in the TS source: what a key
// states, where the checker judges it directly. A reference key
// states a relationship in the graph, not a set at this position —
// the specification's judgment covers it.
func DeclaredOfKey(key annotations.ObjectKeySpec) *annotations.DeclaredRefinement {
	switch key.Value.Kind {
	case annotations.KeyValueSet:
		stated := annotations.DeclaredRefinement{
			Kind:    annotations.DeclaredSet,
			Set:     key.Value.Set,
			KindTag: key.Value.KindTag,
			Word:    key.Value.Word,
			Unread:  key.Value.Unread,
		}
		// an optional or nullable key admits the absent value beside
		// its set (the model conflates the two on cardinality —
		// TERMS.md §6): the maybe target, so null and undefined pass
		// and present values judge against the set
		if key.Value.Absent || key.MayBeAbsent {
			return &annotations.DeclaredRefinement{Kind: annotations.DeclaredPossiblyUndefined, Inner: &stated}
		}
		return &stated
	case annotations.KeyValueObject:
		return &annotations.DeclaredRefinement{Kind: annotations.DeclaredObject, Object: key.Value.Object}
	case annotations.KeyValueCollection:
		// entries checkAssignability through the adapter's entry walk (a
		// parse returns its input, so the exact collection carries
		// through)
		return nil
	case annotations.KeyValueReference:
		return nil
	}
	return nil
}

// setKindTagOf converts DeclaredRefinement/ObjectKeyValue's
// "bigint" | "symbol" | "" KindTag string into
// abstractdomain.SetKindTag.
func setKindTagOf(kindTag string) abstractdomain.SetKindTag {
	switch kindTag {
	case "bigint":
		return abstractdomain.SetKindTagBigint
	case "symbol":
		return abstractdomain.SetKindTagSymbol
	default:
		return abstractdomain.SetKindTagNone
	}
}

// measuresOf converts annotations.Measures into abstractdomain.Measures
// (nil stays nil — the TS source's `measures: {...} | undefined`).
func measuresOf(m *annotations.Measures) *abstractdomain.Measures {
	if m == nil {
		return nil
	}
	return &abstractdomain.Measures{Sum: m.Sum, HasSum: m.HasSum, Sorted: m.Sorted}
}

// objectAnnotationRefOf converts an *annotations.ObjectAnnotation
// into abstractdomain's opaque ObjectAnnotationRef stand-in
// (abstractdomain/abstract_value.go: ObjectAnnotationRef = *struct{},
// compared only by identity). Since annotations IS ported (unlike
// when abstract_value.go's comment was written), this is where the
// two would properly connect — but abstractdomain's own field type
// is still the placeholder *struct{}, so a real *ObjectAnnotation
// cannot be stored there without abstractdomain importing
// annotations (which would invert the port order: annotations
// imports abstractdomain, not the reverse). Each ObjectAnnotation
// gets ONE identity token, memoized by pointer, so the identity
// comparisons SameKnown/JoinKnown rely on (`a.Stated == b.Stated`)
// still hold: the same *ObjectAnnotation always maps to the same
// token, and different ones to different tokens.
// Mutex-guarded: concurrent entry walks reach this through declared
// values, and an unguarded race could mint TWO tokens for one
// annotation — breaking the identity SameKnown/JoinKnown compare by.
var (
	objectAnnotationTokensMu sync.Mutex
	objectAnnotationTokens   = map[*annotations.ObjectAnnotation]abstractdomain.ObjectAnnotationRef{}
)

func objectAnnotationRefOf(o *annotations.ObjectAnnotation) abstractdomain.ObjectAnnotationRef {
	if o == nil {
		return nil
	}
	objectAnnotationTokensMu.Lock()
	defer objectAnnotationTokensMu.Unlock()
	if token, ok := objectAnnotationTokens[o]; ok {
		return token
	}
	token := &struct{}{}
	objectAnnotationTokens[o] = token
	return token
}
