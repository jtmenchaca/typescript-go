// from assignability/declared_value.test.ts
//
// Interface tests for AbstractValueOfDeclared / DeclaredOfKey: a
// stated set, object keys (required, optional, absent, measures),
// maybe wrap, objectArray silence, collection/reference keys.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// identifierNode mirrors ts.factory.createIdentifier("k"): any node
// with a stable identity stands in — DeclaredOfKey/AbstractValueOfDeclared
// never read its contents, only ObjectKeySpec.At's presence as a
// struct field. A parsed identifier from a throwaway source keeps the
// same *ast.Node shape every other call site in this package expects.
func identifierNode(t *testing.T) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/k.ts", Path: "/k.ts"}
	file := parser.ParseSourceFile(opts, "k;", core.ScriptKindTS)
	return file.AsNode()
}

func keySpec(t *testing.T, name string, value annotations.ObjectKeyValue, mayBeAbsent bool) annotations.ObjectKeySpec {
	t.Helper()
	countSet := refinementsets.MakeRefinedSet(refinementsets.Integer)
	return annotations.ObjectKeySpec{
		Name:        name,
		Count:       &countSet,
		MayBeAbsent: mayBeAbsent,
		At:          identifierNode(t),
		Value:       value,
	}
}

func TestAbstractValueOfDeclared_ASetIsTheDeclaredWindow(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	worn := AbstractValueOfDeclared(annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set})
	formatted, ok := abstractdomain.FormatAbstractValue(worn)
	if !ok || formatted != "{𝑥 ≥ 0}" {
		t.Errorf("FormatAbstractValue = %q, %v, want %q, true", formatted, ok, "{𝑥 ≥ 0}")
	}
	if abstractdomain.TrustLevelOf(worn) != abstractdomain.TrustProved {
		t.Errorf("TrustLevelOf = %v, want proved", abstractdomain.TrustLevelOf(worn))
	}
}

func TestAbstractValueOfDeclared_MeasuresRideTheSet(t *testing.T) {
	numbers := refinementsets.Numbers
	set := refinementsets.MakeRefinedSet(refinementsets.Star(numbers))
	worn := AbstractValueOfDeclared(annotations.DeclaredRefinement{
		Kind:     annotations.DeclaredSet,
		Set:      &set,
		Measures: &annotations.Measures{HasSum: true, Sum: 10, Sorted: true},
	})
	if worn.Kind != abstractdomain.KindSet {
		t.Fatalf("Kind = %v, want set", worn.Kind)
	}
	if worn.Measures == nil || !worn.Measures.HasSum || worn.Measures.Sum != 10 || !worn.Measures.Sorted {
		t.Errorf("Measures = %+v, want {Sum:10 HasSum:true Sorted:true}", worn.Measures)
	}
}

func TestAbstractValueOfDeclared_AMaybeTargetWrapsTheInner(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	inner := annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
	worn := AbstractValueOfDeclared(annotations.DeclaredRefinement{Kind: annotations.DeclaredPossiblyUndefined, Inner: &inner})
	if worn.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("Kind = %v, want possiblyUndefined", worn.Kind)
	}
	formatted, ok := abstractdomain.FormatAbstractValue(worn)
	if !ok || formatted != "{𝑥 ≥ 0, or absent}" {
		t.Errorf("FormatAbstractValue = %q, %v, want %q, true", formatted, ok, "{𝑥 ≥ 0, or absent}")
	}
}

func TestAbstractValueOfDeclared_AnObjectArrayIsResidue(t *testing.T) {
	worn := AbstractValueOfDeclared(annotations.DeclaredRefinement{
		Kind:        annotations.DeclaredObjectArray,
		Object:      &annotations.ObjectAnnotation{},
		Lo:          0,
		HiUnbounded: true,
	})
	if worn.Kind != abstractdomain.KindUnknown {
		t.Errorf("Kind = %v, want unknown", worn.Kind)
	}
}

func TestAbstractValueOfDeclared_ObjectKeysWearTheirStatements(t *testing.T) {
	atLeast0 := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	starOfNumbers := refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Numbers))
	innerObject := &annotations.ObjectAnnotation{}
	object := &annotations.ObjectAnnotation{
		Keys: []annotations.ObjectKeySpec{
			keySpec(t, "n", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0}, false),
			keySpec(t, "opt", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0}, true),
			keySpec(t, "nil", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0, Absent: true}, false),
			keySpec(t, "xs", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &starOfNumbers, Measures: &annotations.Measures{HasSum: true, Sum: 3}}, false),
			keySpec(t, "inner", annotations.ObjectKeyValue{Kind: annotations.KeyValueObject, Object: innerObject}, false),
			keySpec(t, "ref", annotations.ObjectKeyValue{Kind: annotations.KeyValueReference, Target: nil}, false),
		},
	}
	worn := AbstractValueOfDeclared(annotations.DeclaredRefinement{Kind: annotations.DeclaredObject, Object: object})
	if worn.Kind != abstractdomain.KindObject {
		t.Fatalf("Kind = %v, want object", worn.Kind)
	}
	if abstractdomain.TrustLevelOf(worn) != abstractdomain.TrustProved {
		t.Errorf("TrustLevelOf = %v, want proved", abstractdomain.TrustLevelOf(worn))
	}
	if worn.Stated != objectAnnotationRefOf(object) {
		t.Errorf("Stated does not match objectAnnotationRefOf(object)")
	}
	keys := make(map[string]abstractdomain.AbstractValue, len(worn.Keys))
	for _, k := range worn.Keys {
		keys[k.Name] = k.Value
	}
	if keys["n"].Kind != abstractdomain.KindSet {
		t.Errorf("keys[n].Kind = %v, want set", keys["n"].Kind)
	}
	if keys["opt"].Kind != abstractdomain.KindPossiblyUndefined {
		t.Errorf("keys[opt].Kind = %v, want possiblyUndefined", keys["opt"].Kind)
	}
	if keys["nil"].Kind != abstractdomain.KindPossiblyUndefined {
		t.Errorf("keys[nil].Kind = %v, want possiblyUndefined", keys["nil"].Kind)
	}
	if keys["xs"].Kind != abstractdomain.KindSet {
		t.Errorf("keys[xs].Kind = %v, want set", keys["xs"].Kind)
	}
	if xs := keys["xs"]; xs.Kind == abstractdomain.KindSet {
		if xs.Measures == nil || !xs.Measures.HasSum || xs.Measures.Sum != 3 {
			t.Errorf("keys[xs].Measures = %+v, want {Sum:3 HasSum:true}", xs.Measures)
		}
	}
	if keys["inner"].Kind != abstractdomain.KindObject {
		t.Errorf("keys[inner].Kind = %v, want object", keys["inner"].Kind)
	}
	if keys["ref"].Kind != abstractdomain.KindUnknown {
		t.Errorf("keys[ref].Kind = %v, want unknown", keys["ref"].Kind)
	}
}

func TestDeclaredOfKey_ARequiredSetIsTheSetOptionalAbsentWrap(t *testing.T) {
	atLeast0 := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	required := DeclaredOfKey(keySpec(t, "n", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0}, false))
	if required == nil || required.Kind != annotations.DeclaredSet {
		t.Fatalf("DeclaredOfKey(required) = %+v, want kind set", required)
	}
	if required.KindTag != "" || required.Unread {
		t.Errorf("DeclaredOfKey(required) = %+v, want zero KindTag/Unread", required)
	}
	optional := DeclaredOfKey(keySpec(t, "n", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0}, true))
	if optional == nil || optional.Kind != annotations.DeclaredPossiblyUndefined {
		t.Errorf("DeclaredOfKey(optional).Kind = %+v, want possiblyUndefined", optional)
	}
	absent := DeclaredOfKey(keySpec(t, "n", annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &atLeast0, Absent: true}, false))
	if absent == nil || absent.Kind != annotations.DeclaredPossiblyUndefined {
		t.Errorf("DeclaredOfKey(absent).Kind = %+v, want possiblyUndefined", absent)
	}
}

func TestDeclaredOfKey_ObjectKeysStateTheObjectCollectionReferenceAreNil(t *testing.T) {
	nested := &annotations.ObjectAnnotation{}
	objectKey := DeclaredOfKey(keySpec(t, "o", annotations.ObjectKeyValue{Kind: annotations.KeyValueObject, Object: nested}, false))
	if objectKey == nil || objectKey.Kind != annotations.DeclaredObject || objectKey.Object != nested {
		t.Errorf("DeclaredOfKey(object) = %+v, want kind object wrapping nested", objectKey)
	}
	mapKey := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	sizeSet := refinementsets.MakeRefinedSet(refinementsets.Integer)
	collection := DeclaredOfKey(keySpec(t, "m", annotations.ObjectKeyValue{
		Kind:   annotations.KeyValueCollection,
		Flavor: "map",
		Key:    &mapKey,
		Value:  &mapKey,
		Size:   &sizeSet,
	}, false))
	if collection != nil {
		t.Errorf("DeclaredOfKey(collection) = %+v, want nil", collection)
	}
	reference := DeclaredOfKey(keySpec(t, "r", annotations.ObjectKeyValue{Kind: annotations.KeyValueReference, Target: nil}, false))
	if reference != nil {
		t.Errorf("DeclaredOfKey(reference) = %+v, want nil", reference)
	}
}
