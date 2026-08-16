// FormatObjectAnnotation renders an object STATEMENT for hover — the
// pure-formatting half of the hover stack, testable without a program.

package service

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestFormatObjectAnnotationEmpty(t *testing.T) {
	if _, ok := FormatObjectAnnotation(&annotations.ObjectAnnotation{}); ok {
		t.Fatal("an object with no keys states nothing")
	}
	if _, ok := FormatObjectAnnotation(nil); ok {
		t.Fatal("a nil object states nothing")
	}
}

func TestFormatObjectAnnotationKeys(t *testing.T) {
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(100))
	object := &annotations.ObjectAnnotation{
		Keys: []annotations.ObjectKeySpec{
			{
				Name:  "pct",
				Value: annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, Set: &window},
			},
			{
				Name:        "note",
				MayBeAbsent: true,
				Value:       annotations.ObjectKeyValue{Kind: annotations.KeyValueSet},
			},
		},
	}
	shown, ok := FormatObjectAnnotation(object)
	if !ok {
		t.Fatal("expected a rendering")
	}
	if !containsAll(shown, "pct:", "note?:", "unconstrained") {
		t.Fatalf("unexpected rendering %q", shown)
	}
}

func TestFormatObjectAnnotationSymbolAndReferenceKeys(t *testing.T) {
	object := &annotations.ObjectAnnotation{
		Keys: []annotations.ObjectKeySpec{
			{Name: "tag", Value: annotations.ObjectKeyValue{Kind: annotations.KeyValueSet, KindTag: "symbol"}},
		},
	}
	shown, ok := FormatObjectAnnotation(object)
	if !ok || !containsAll(shown, "tag: symbol") {
		t.Fatalf("unexpected rendering %q (ok=%v)", shown, ok)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(s); i++ {
			if s[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
