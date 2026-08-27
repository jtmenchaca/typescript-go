package typereading

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestProbeDiscriminantUnionParameter(t *testing.T) {
	p := programFromSource(t, `type Shape = { kind: "circle"; r: number } | { kind: "square"; side: number };
export function f(s: Shape): void { console.log(s); }
`)
	param := parameterNamed(t, p, "s")
	decl := param.AsParameterDeclaration()
	worn, ok := ReadDeclaredType(p.checker, decl.Type, decl.Name())
	if !ok {
		t.Fatalf("expected a value, got none")
	}
	spelling, spelledOk := abstractdomain.FormatAbstractValueInline(worn)
	t.Logf("Kind=%v spelledOk=%v spelling=%q", worn.Kind, spelledOk, spelling)
	if worn.Kind == abstractdomain.KindKindUnion {
		for i, arm := range worn.Arms {
			armSpelling, armOk := abstractdomain.FormatAbstractValueInline(arm)
			t.Logf("arm[%d] Kind=%v ok=%v spelling=%q", i, arm.Kind, armOk, armSpelling)
			for _, k := range arm.Keys {
				kSpelling, kOk := abstractdomain.FormatAbstractValueInline(k.Value)
				t.Logf("  key=%s valueKind=%v ok=%v spelling=%q", k.Name, k.Value.Kind, kOk, kSpelling)
			}
		}
	}
}
