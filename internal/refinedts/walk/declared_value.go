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
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
		// a set that ADDS NOTHING beyond a bare keyword's own ground
		// (annotation_of_type.go's KindNumberKeyword/StringKeyword/
		// BooleanKeyword arms, which ground a plain `number`/`string`/
		// `boolean` position into a DeclaredSet purely so downstream
		// assignability has something to ask) is a READ of the
		// position's own syntax, not a checked claim about it —
		// InitialStateOfPlainParameter's own doc draws exactly this
		// line for a parameter's plain type ("closer to a cited
		// language clause... than a checked LIBRARY signature") and
		// stamps TrustSpec, never TrustProved. A GENUINE zod-derived
		// bound (Age's [0,120]) still proves outright.
		grade := abstractdomain.TrustProved
		if AddsNothingSet(*stated.Set) {
			grade = abstractdomain.TrustSpec
		}
		// a BOOLEAN position wears the tagged two-value form, not a set.
		// The annotation side states a plain `boolean` as OneOf{0,1} with
		// KindTag "boolean" (annotations/type_node_sets.go's
		// KindBooleanKeyword arm), but SetKindTag holds only "bigint" and
		// "symbol" — setKindTagOf below drops "boolean" to
		// SetKindTagNone — so the boolean identity was lost right here and
		// the value entered the body as an untagged numeric-looking set.
		// Downstream that set read as a NUMBER (KindOfClaim sends an
		// untagged set through setSortOfForms, which calls a scalar leaf
		// "number") and every narrowing channel missed it (keepTruthy,
		// keepFalsy, and consistentAtLeaf all read the words off a
		// KindValues tagged number-or-boolean), so `if (b)`, `b !== false`
		// and `case true:` left the {0, 1} unnarrowed or fell to the
		// numeric transfer, which answers the open real interval (0, 1)
		// for "nonzero" — a real-line answer to a two-point question.
		//
		// KindValues{0, 1} tagged boolean is the form every other boolean
		// producer already writes (walk/comparison_decision.go's undecided
		// comparison, narrowing/typeof_ground.go, walk/foreign_edge_cases.go,
		// typereading/recipes.go's BooleanCodes), so the tag survives to
		// the sink and the narrowings read it.
		if stated.KindTag == "boolean" {
			forms := stated.Set.Forms
			if len(forms) == 1 && forms[0].Form == refinementsets.FormOneOf {
				return abstractdomain.KnownValues(append([]float64{}, forms[0].W...), abstractdomain.PrimitiveBoolean, grade)
			}
		}
		bare := abstractdomain.KnownSet(
			*stated.Set,
			stated.Temporal,
			grade,
			setKindTagOf(stated.KindTag),
		)
		worn := abstractdomain.KnownWithMeasures(bare, measuresOf(stated.Measures))
		// a bare `number` keyword grounds to the whole number ground
		// (R-bar, annotations/type_node_sets.go's KindNumberKeyword
		// arm) -- but a bare TypeScript `number` is not a refinement
		// that excludes NaN, since NaN IS a number at runtime. The
		// ground set itself carries no NaN member (refinementsets'
		// ray forms never mention it), so this arm must wear the same
		// PossiblyNaN wrapper typereading's NumberWithNaN already
		// wears around the identical set for the ungrounded read path
		// -- otherwise the grounding silently drops the NaN
		// possibility a plain `number` position has always carried.
		if refinementsets.IsNumberGround(*stated.Set) {
			// PossiblyNaN reads its own grade off worn.Grade (TrustLevelOf),
			// so the TrustSpec stamp above already carries through here —
			// no separate downgrade needed.
			return abstractdomain.PossiblyNaN(worn)
		}
		return worn
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
				// DIVERGES FROM TS: the TS switch spells only "set" |
				// "object" | "reference", so a collection key falls through
				// with no `keys[key.name]` assignment and the object build
				// reads the key as ABSENT — a key the statement says is
				// always present. Here the key is entered as the collection
				// it states: an INCOMPLETE Map or Set (no entry is named by
				// a statement, only the members and size are), which is what
				// the collection reads already expect — a `has` miss and a
				// `get` miss both stay unanswered on an incomplete record
				// (collection_models), while `typeof`, truthiness, and the
				// sort answer off the kind. The member and size statements
				// have nowhere to ride: the collection kind carries only
				// flavor, entries, and completeness.
				flavor := abstractdomain.FlavorSet
				if key.Value.Flavor == "map" {
					flavor = abstractdomain.FlavorMap
				}
				value := abstractdomain.AbstractValue{
					Kind:             abstractdomain.KindCollection,
					CollectionFlavor: flavor,
					Entries:          nil,
					Complete:         false,
				}
				// an optional or nullable collection key's reads wear the
				// maybe wrapper, exactly as a set key's do
				var worn abstractdomain.AbstractValue
				if key.MayBeAbsent {
					worn = abstractdomain.PossiblyUndefined(value, "", false, false)
				} else {
					worn = value
				}
				keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: worn})
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
//
// The map is kept in BOTH directions. A token is minted from exactly
// one annotation, so the annotation a token stands for is recoverable:
// objectAnnotationOf walks back, which is how a reader in walk (which
// imports both packages) opens a ref abstractdomain must keep opaque.
var (
	objectAnnotationTokensMu sync.Mutex
	objectAnnotationTokens   = map[*annotations.ObjectAnnotation]abstractdomain.ObjectAnnotationRef{}
	objectAnnotationsByToken = map[abstractdomain.ObjectAnnotationRef]*annotations.ObjectAnnotation{}
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
	objectAnnotationsByToken[token] = o
	return token
}

// objectAnnotationOf is objectAnnotationRefOf's inverse: the
// annotation a token was minted from, or nil for a token this walk
// never minted. Readers that need the stated SHAPE behind an
// AbstractValue's Stated/BoundObject ask here.
func objectAnnotationOf(ref abstractdomain.ObjectAnnotationRef) *annotations.ObjectAnnotation {
	if ref == nil {
		return nil
	}
	objectAnnotationTokensMu.Lock()
	defer objectAnnotationTokensMu.Unlock()
	return objectAnnotationsByToken[ref]
}
