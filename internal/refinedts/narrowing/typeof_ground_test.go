package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestGroundOfTypeofWordMapsEachWordToItsSort ports typeof_ground.test.ts's
// "groundOfTypeofWord maps each word to its sort".
func TestGroundOfTypeofWordMapsEachWordToItsSort(t *testing.T) {
	cases := []struct {
		word string
		kind abstractdomain.Kind
	}{
		{"number", abstractdomain.KindPossiblyNaN},
		{"string", abstractdomain.KindSet},
		{"boolean", abstractdomain.KindValues},
		{"object", abstractdomain.KindPossiblyUndefined},
		{"symbol", abstractdomain.KindSymbol},
		{"function", abstractdomain.KindHostFunction},
	}
	for _, c := range cases {
		ground, ok := GroundOfTypeofWord(c.word)
		if !ok {
			t.Fatalf("%q: expected a ground", c.word)
		}
		if ground.Kind != c.kind {
			t.Errorf("%q: kind = %v, want %v", c.word, ground.Kind, c.kind)
		}
	}
}

// TestGroundOfTypeofWordHasNoGroundForUnknownWords ports
// typeof_ground.test.ts's "groundOfTypeofWord has no ground for unknown
// words".
func TestGroundOfTypeofWordHasNoGroundForUnknownWords(t *testing.T) {
	for _, word := range []string{"undefined", "bigint", ""} {
		if _, ok := GroundOfTypeofWord(word); ok {
			t.Errorf("%q: expected no ground", word)
		}
	}
}
