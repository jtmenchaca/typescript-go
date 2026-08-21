// UntrackedIdentifier's module-const follow: the array-mutation gate
// (RULING, JT 2026-08-21). A clean module still serves the array
// literal exactly as before; any of the three named mutation shapes
// blocks the serve; a DIFFERENT array's mutation does not block this
// one; a scalar const stays on the original alias-freedom argument
// regardless of what methods get called near it.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// untrackedIdentifierBareRead builds a program whose module declares
// `samples` (plus whatever else `extraModuleSource` adds) and reads it
// as a bare expression statement inside function `f` — the same shape
// the foreign-edge fixtures use to reach UntrackedIdentifier's
// module-const follow (samples is read inside a function that never
// itself declares it).
func untrackedIdentifierBareRead(t *testing.T, samplesDecl string, extraModuleSource string) abstractdomain.AbstractValue {
	t.Helper()
	source := samplesDecl + "\n" + extraModuleSource + "function f() {\n\tsamples;\n}\n"
	p := entryEnvTestProgram(t, source)
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	ctx := relationalAccumulationContext(p)
	return UntrackedIdentifier(ctx, payload)
}

func isServedPrimitiveArray(v abstractdomain.AbstractValue) bool {
	return v.Kind == abstractdomain.KindValues && v.KindTag == abstractdomain.PrimitiveArray
}

func TestUntrackedIdentifier_ACleanModuleConstArrayStillServesTheLiteral(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];", "")
	if !isServedPrimitiveArray(got) {
		t.Fatalf("a clean module const array did not serve its literal: %+v", got)
	}
	if len(got.Values) != 3 || got.Values[0] != 0.5 || got.Values[1] != -0.3 || got.Values[2] != 0.2 {
		t.Errorf("served values = %+v, want [0.5, -0.3, 0.2]", got.Values)
	}
}

func TestUntrackedIdentifier_APushAnywhereInTheModuleBlocksTheServe(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];",
		"function mutateElsewhere() {\n\tsamples.push(0.1);\n}\n")
	if isServedPrimitiveArray(got) {
		t.Fatalf("a module with samples.push(...) still served the stale literal: %+v", got)
	}
}

func TestUntrackedIdentifier_ATopLevelPushBlocksTheServe(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];",
		"samples.push(0.1);\n")
	if isServedPrimitiveArray(got) {
		t.Fatalf("a top-level samples.push(...) still served the stale literal: %+v", got)
	}
}

func TestUntrackedIdentifier_AnElementWriteBlocksTheServe(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];",
		"function mutateElsewhere() {\n\tsamples[0] = 0.9;\n}\n")
	if isServedPrimitiveArray(got) {
		t.Fatalf("a module with samples[0] = … still served the stale literal: %+v", got)
	}
}

func TestUntrackedIdentifier_ACompoundElementWriteBlocksTheServe(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];",
		"function mutateElsewhere() {\n\tsamples[0] += 0.1;\n}\n")
	if isServedPrimitiveArray(got) {
		t.Fatalf("a module with samples[0] += … still served the stale literal: %+v", got)
	}
}

func TestUntrackedIdentifier_AnObjectAssignFirstArgumentBlocksTheServe(t *testing.T) {
	got := untrackedIdentifierBareRead(t, "const samples = [0.5, -0.3, 0.2];",
		"function mutateElsewhere() {\n\tObject.assign(samples, [1, 2, 3]);\n}\n")
	if isServedPrimitiveArray(got) {
		t.Fatalf("a module with Object.assign(samples, …) still served the stale literal: %+v", got)
	}
}

func TestUntrackedIdentifier_ADifferentArraysMutationDoesNotBlockThisOne(t *testing.T) {
	got := untrackedIdentifierBareRead(t,
		"const samples = [0.5, -0.3, 0.2];\nconst other = [1, 2, 3];",
		"function mutateElsewhere() {\n\tother.push(4);\n}\n")
	if !isServedPrimitiveArray(got) {
		t.Fatalf("other's mutation wrongly blocked samples's serve: %+v", got)
	}
}

func TestUntrackedIdentifier_AScalarConstIsUnaffectedByANumericMethodLookingCall(t *testing.T) {
	// the helper's read is always the name `samples`, so the scalar
	// under test carries that name too
	got := untrackedIdentifierBareRead(t, "const samples = 40;",
		"function callSomething() {\n\tsamples.toFixed(2);\n}\n")
	if got.Kind != abstractdomain.KindValues || got.KindTag == abstractdomain.PrimitiveArray {
		t.Fatalf("a scalar const near a method-looking call did not serve as a scalar: %+v", got)
	}
	if len(got.Values) != 1 || got.Values[0] != 40 {
		t.Errorf("served values = %+v, want [40]", got.Values)
	}
}

// TestConstArrayMutated exercises the scan directly against a
// VariableDeclaration node, independent of UntrackedIdentifier's own
// gating — the three site shapes and the false-on-clean / false-on-
// different-name cases.
func TestConstArrayMutated(t *testing.T) {
	declarationOf := func(t *testing.T, source string) *ast.Node {
		t.Helper()
		p := entryEnvTestProgram(t, source)
		for _, statement := range p.Entry.Statements.Nodes {
			if !ast.IsVariableStatement(statement) {
				continue
			}
			for _, d := range statement.AsVariableStatement().DeclarationList.
				AsVariableDeclarationList().Declarations.Nodes {
				if d.AsVariableDeclaration().Name().Text() == "samples" {
					return d
				}
			}
		}
		t.Fatalf("no `samples` declaration found")
		return nil
	}

	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "clean module",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction f() { samples; }\n",
			want:   false,
		},
		{
			name:   "push inside another function",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction other() { samples.push(1); }\n",
			want:   true,
		},
		{
			name:   "top-level splice",
			source: "const samples = [0.5, -0.3, 0.2];\nsamples.splice(0, 1);\n",
			want:   true,
		},
		{
			name:   "element write",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction other() { samples[1] = 9; }\n",
			want:   true,
		},
		{
			name:   "compound element write",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction other() { samples[1] += 9; }\n",
			want:   true,
		},
		{
			name:   "Object.assign first argument",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction other() { Object.assign(samples, {}); }\n",
			want:   true,
		},
		{
			name:   "a read-only method call is not a mutation",
			source: "const samples = [0.5, -0.3, 0.2];\nfunction other() { samples.map(x => x); }\n",
			want:   false,
		},
		{
			name:   "a different array's mutation does not count",
			source: "const samples = [0.5, -0.3, 0.2];\nconst other = [1, 2, 3];\nfunction f() { other.push(4); }\n",
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := declarationOf(t, c.source)
			got := ConstArrayMutated(d, "samples")
			if got != c.want {
				t.Errorf("ConstArrayMutated = %v, want %v", got, c.want)
			}
		})
	}
}
