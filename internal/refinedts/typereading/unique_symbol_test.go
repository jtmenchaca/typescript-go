// Pins f-type-nodes.ts's uniqueSymbolAnnotation row: a `unique symbol`
// typed value (`declare const ageBrand: unique symbol`) now reads as
// UnknownSymbol rather than falling through readHostTypeUncached's
// every flag check unread. TypeFlagsESSymbolLike (types.go) is
// ESSymbol | UniqueESSymbol -- host_type.go used to test ESSymbol
// alone, so a unique-symbol-typed value answered nothing.

package typereading

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestReadHostType_AUniqueSymbolReadsAsUnknownSymbol(t *testing.T) {
	p := programFromSource(t, "declare const ageBrand: unique symbol;\nexport function f(): void { console.log(ageBrand); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "ageBrand", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSymbol {
		t.Errorf("Kind = %v, want symbol", worn.Kind)
	}
}

func TestReadHostType_APlainSymbolStillReadsAsUnknownSymbol(t *testing.T) {
	p := programFromSource(t, "export function f(s: symbol): void { console.log(s); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "s", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSymbol {
		t.Errorf("Kind = %v, want symbol", worn.Kind)
	}
}
