// Pins the e2e sink.assign rows (A1/A2/A3/A5.sink.assign): the
// designation sits directly above `const a: Age = x;` — the assignment
// the row's own claim is about — and the reported 7001 lands at that
// declaration. AnalyzeVariableStatement (variable_statement.go) routes
// the declared local's initializer through WriteBinding, which calls
// CheckAssignability against the declared type (assignments.go), and
// the diagnostic's span is the declaration's own. After the refused
// write the binding keeps the DECLARED set (the refused-write law), so
// the following `return a;` is silent rather than a second fire.
package walk

import (
	"strings"
	"testing"
)

const declaredLocalSinkHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(150);
type Age = z.infer<typeof zAge>;
const zWide = z.number().int().min(0).max(200);
type Wide = z.infer<typeof zWide>;
`

// TestDeclaredLocalSink_FiresAtTheDeclaration pins the mechanism:
// assigning an out-of-range Wide value to a binding declared Age
// reports 7001 with the diagnostic's Start inside
// `const a: Age = x;`, and no second 7001 lands on `return a;`.
func TestDeclaredLocalSink_FiresAtTheDeclaration(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := declaredLocalSinkHeader + `
function assignOutside(x: Wide): Age {
  const a: Age = x;
  return a;
}
`
	diagnostics := parseVocabRun(t, kernel, source, "assignOutside")
	declStart := strings.Index(source, "const a: Age = x;")
	returnStart := strings.Index(source, "return a;")
	if declStart < 0 || returnStart < 0 {
		t.Fatalf("fixture source changed shape — const/return offsets not found")
	}
	atDeclaration := 0
	atReturn := 0
	for _, d := range diagnostics {
		if d.Code != 7001 {
			continue
		}
		if d.Start >= declStart && d.Start < declStart+len("const a: Age = x;") {
			atDeclaration++
		}
		if d.Start >= returnStart && d.Start < returnStart+len("return a;") {
			atReturn++
		}
	}
	if atDeclaration != 1 {
		t.Errorf("want exactly one 7001 inside `const a: Age = x;`, got %d (all: %+v)", atDeclaration, diagnostics)
	}
	if atReturn != 0 {
		t.Errorf("want no 7001 on `return a;` (the refused write keeps the declared set), got %d (all: %+v)", atReturn, diagnostics)
	}
}
