// Interface test for CompileAnnotationFileFacts: a plain (no-kernel)
// sweep of a file's top-level annotation and object statements —
// the reporting=false, kernel=nil path, which needs no live kernel
// artifact.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

func TestCompileAnnotationFileFacts_CollectsSetAndObjectAnnotations(t *testing.T) {
	p := newTestProgram(t,
		"const zPct = z.number().min(0).max(100);\n"+
			"const zUser = z.object({ id: z.number() });\n"+
			"const notAnAnnotation = 5;\n")
	merged := AnnotationFileFactsMerged{Registry: AnnotationRegistry{}, Objects: ObjectRegistry{}}
	var diagnostics []assignability.RefinementDiagnostic
	report := func(d assignability.RefinementDiagnostic) { diagnostics = append(diagnostics, d) }

	facts := CompileAnnotationFileFacts(p.program, p.program.Entry, merged, nil, false, report)

	if len(facts.Annotations) != 1 {
		t.Errorf("Annotations = %d entries, want 1 (zPct)", len(facts.Annotations))
	}
	if len(facts.Objects) != 1 {
		t.Errorf("Objects = %d entries, want 1 (zUser)", len(facts.Objects))
	}
	if len(diagnostics) != 0 {
		t.Errorf("diagnostics = %v, want none (reporting=false)", diagnostics)
	}
	// the merged registries are also populated -- later files in a
	// program compile against them
	if len(merged.Registry) != 1 || len(merged.Objects) != 1 {
		t.Errorf("merged registries not populated: registry=%d objects=%d", len(merged.Registry), len(merged.Objects))
	}
}

func TestCompileAnnotationFileFacts_UnhonorableStatementReportsWhenReporting(t *testing.T) {
	// z.record is unread-vocabulary in the zod adapter, but on the
	// checker's OWN surface an unrecognized root refuses loudly
	// (7004) when reporting is on — the surface never goes silent.
	p := newTestProgram(t, `const zBad = z.notARealConstructor();`+"\n")
	merged := AnnotationFileFactsMerged{Registry: AnnotationRegistry{}, Objects: ObjectRegistry{}}
	var diagnostics []assignability.RefinementDiagnostic
	report := func(d assignability.RefinementDiagnostic) { diagnostics = append(diagnostics, d) }

	CompileAnnotationFileFacts(p.program, p.program.Entry, merged, nil, true, report)

	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one 7004", diagnostics)
	}
	if diagnostics[0].Code != 7004 {
		t.Errorf("diagnostics[0].Code = %d, want 7004", diagnostics[0].Code)
	}
}
