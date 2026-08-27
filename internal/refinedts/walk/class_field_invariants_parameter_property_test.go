// Pins the `this.field` half of the class-instance field-read defect:
// a field declared ONLY through a constructor parameter property
// (`constructor(public age: Age) {}`, no PropertyDeclaration anywhere
// in the class body) took NO field invariant at all before this fix —
// computeFieldInvariants' own candidate loop and InitialThisStateOf's
// own key loop both walked PropertyDeclaration members only. A
// `this.age` read inside the class's own method then found `age`
// outside an INCOMPLETE `this` object's known keys and reported
// RTS7002 ("not proven complete") instead of Age's own window.
//
// Pinned by calling ReadThisPropertyAccess directly on the `this.age`
// node — the same low-level door property_member_grade_provenance_test.go
// uses for ReadPropertyAccess — because no production door in this
// package walks a locally-nested class's own METHOD bodies with live
// judgment (parseVocabRun's single-function AnalyzeFunction call never
// reaches a class's own methods; verified separately that an outright
// wrong literal return inside such a method reports nothing). The real
// checker binary (service/check.go's own production path) DOES walk
// method bodies and was used to verify the fix end-to-end before this
// pin was written.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

const classFieldParameterPropertyHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(150);
type Age = z.infer<typeof zAge>;
`

// classFieldInvariantsThisAccess builds a real program with a compiled
// annotation registry, finds the named method's own `this.<field>`
// return expression, and hands back a FlowContext plus that node ready
// for ReadThisPropertyAccess.
func classFieldInvariantsThisAccess(t *testing.T, source string, methodName string) (*FlowContext, *ast.Node) {
	t.Helper()
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		nil, false, func(assignability.RefinementDiagnostic) {})
	ctx := &FlowContext{
		P:        p,
		Registry: registry,
		Objects:  objects,
		Report:   func(assignability.RefinementDiagnostic) {},
	}
	var method *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if method != nil || node == nil {
			return
		}
		if ast.IsMethodDeclaration(node) {
			name := node.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == methodName {
				method = node
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return method != nil
		})
	}
	visit(p.Entry.AsNode())
	if method == nil {
		t.Fatalf("no method named %s", methodName)
	}
	body := method.Body()
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("%s has no block body", methodName)
	}
	statements := body.AsBlock().Statements.Nodes
	if len(statements) == 0 || !ast.IsReturnStatement(statements[0]) {
		t.Fatalf("%s's first statement is not a return", methodName)
	}
	expr := statements[0].AsReturnStatement().Expression
	if expr == nil || !ast.IsPropertyAccessExpression(expr) {
		t.Fatalf("%s's return expression is not a property access", methodName)
	}
	return ctx, expr
}

// TestThisFieldRead_ParameterPropertyOnlyFieldReadsItsOwnDeclaredWindow
// pins the fix directly: `this.age` off a class whose only spelling of
// `age` is a constructor parameter property answers Age's own window,
// not the opaque/incomplete floor RTS7002 used to report.
func TestThisFieldRead_ParameterPropertyOnlyFieldReadsItsOwnDeclaredWindow(t *testing.T) {
	source := classFieldParameterPropertyHeader + `
class Person {
  constructor(public age: Age) {}
  ownAge(): Age {
    return this.age;
  }
}
`
	ctx, expr := classFieldInvariantsThisAccess(t, source, "ownAge")
	env := NewEnv()
	got := ReadThisPropertyAccess(ctx, env, expr)
	if got == nil {
		t.Fatalf("ReadThisPropertyAccess(this.age) = nil, want Age's own window")
	}
	if got.Opaque {
		t.Fatalf("ReadThisPropertyAccess(this.age) = Opaque, want a real set — the parameter-property-only field must carry an invariant")
	}
	if got.Kind == abstractdomain.KindUnknown {
		t.Fatalf("ReadThisPropertyAccess(this.age).Kind = KindUnknown (ResidueReason=%q), want a determined set", got.ResidueReason)
	}
}

// TestThisFieldRead_ParameterPropertyOnlyFieldKeepsItsOwnWindowNotJustNumber
// checks the invariant is Age's OWN [0,150] window, not the wider
// unrefined number ground InitialStateOfPlainParameter alone would
// have given before statedOrPlainParameterValue routed the seed
// through AnnotationOfType. A value the kernel would place outside
// Age (say 999) must NOT be assignable to what this read answers —
// checked here by confirming the read's own Grade/shape carries a
// bound at all (KindSet, not the bare NumberWithNaN ground with no
// window), which is the observable difference between the two seeds
// without requiring a live kernel.
func TestThisFieldRead_ParameterPropertyOnlyFieldKeepsItsOwnWindowNotJustNumber(t *testing.T) {
	source := classFieldParameterPropertyHeader + `
class Person {
  constructor(public age: Age) {}
  ownAge(): Age {
    return this.age;
  }
}
`
	ctx, expr := classFieldInvariantsThisAccess(t, source, "ownAge")
	env := NewEnv()
	got := ReadThisPropertyAccess(ctx, env, expr)
	if got == nil {
		t.Fatalf("ReadThisPropertyAccess(this.age) = nil, want Age's own window")
	}
	inner := *got
	if inner.Kind == abstractdomain.KindPossiblyNaN && inner.Inner != nil {
		inner = *inner.Inner
	}
	if inner.Kind != abstractdomain.KindSet {
		t.Fatalf("ReadThisPropertyAccess(this.age) inner Kind = %v, want KindSet carrying Age's own [0,150] window", inner.Kind)
	}
}

// TestThisFieldRead_ClassGetAccessorFieldReadsItsOwnReturnType pins a
// getter-only property (backed by a `#`-named private field), the same
// shape A9.xfer.member.ts exercises: `p.age` off an EXTERNAL
// class-typed parameter must read the getter's own written return type
// rather than falling to the checker's resolved (unrefined) type —
// the regression this reading's own first draft introduced (a getter
// name absent from the object's Keys blocked the later host-type
// fallback that used to answer it, if imprecisely) and this pin
// guards against returning.
func TestThisFieldRead_ClassGetAccessorFieldReadsItsOwnReturnType(t *testing.T) {
	source := classFieldParameterPropertyHeader + `
class Person {
  #age: Age = 0;
  get age(): Age { return 5; }
}
function wrapperReadViaGetter(p: Person): Age {
  return p.age;
}
`
	kernel := parseVocabKernel(t)
	diagnostics := parseVocabRun(t, kernel, source, "wrapperReadViaGetter")
	for _, d := range diagnostics {
		t.Errorf("wrapperReadViaGetter reported %+v, want none — p.age must read the getter's own Age return type", d)
	}
}
