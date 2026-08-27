package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestProbePlainNumberParameterGrade(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number): void { console.log(x); }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	xVal, _ := env.Get("x")
	t.Logf("x.Kind=%v x.Grade=%q", xVal.Kind, xVal.Grade)
	if xVal.Inner != nil {
		t.Logf("x.Inner.Kind=%v", xVal.Inner.Kind)
	}
}

func TestProbeObjectMemberNumberGrade(t *testing.T) {
	p := entryEnvTestProgram(t, `type Shape = { kind: "circle"; r: number } | { kind: "square"; side: number };
function f(s: Shape): void { console.log(s); }
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	sVal, _ := env.Get("s")
	t.Logf("s.Grade=%q", sVal.Grade)
	if sVal.Kind == abstractdomain.KindKindUnion {
		for i, arm := range sVal.Arms {
			t.Logf("arm[%d].Grade=%q", i, arm.Grade)
			for _, k := range arm.Keys {
				t.Logf("  key=%s Kind=%v Grade=%q", k.Name, k.Value.Kind, k.Value.Grade)
			}
		}
	}
}
