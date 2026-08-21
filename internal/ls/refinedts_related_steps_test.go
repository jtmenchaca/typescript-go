// The related-step substrate, at the shape the host renders: a
// refinement finding carrying steps must land those steps in the
// ast.Diagnostic's own relatedInformation, each with its own file and
// span — which is the whole reason the LSP renderer needs no change
// (ls/lsconv/converters.go:454-472 walks exactly that list).

package ls

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/tspath"
)

func parseForSteps(t *testing.T, fileName string, text string) *ast.SourceFile {
	t.Helper()
	return parser.ParseSourceFile(
		ast.SourceFileParseOptions{FileName: fileName, Path: tspath.Path(fileName)},
		text,
		core.ScriptKindTS,
	)
}

// TestRelatedStepsPopulateRelatedInformation is the shape assertion the
// substrate exists for: two steps in, two relatedInformation entries
// out, each keeping its own file, span, and sentence.
func TestRelatedStepsPopulateRelatedInformation(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const total = widen(3, 7);\n")
	other := parseForSteps(t, "/repo/lib.ts", "export const ceiling = 7;\n")

	finding := assignability.RefinementDiagnostic{
		Code:        7001,
		MessageText: "argument of type '3' is not assignable to type '{≥ 7}'",
		Start:       20,
		Length:      1,
	}.WithSteps(
		assignability.RelatedStep{
			Sentence: "'ceiling' is 7 here",
			File:     other,
			Start:    23,
			Length:   1,
		},
		assignability.RelatedStep{
			Sentence: "the bound was instantiated at this call",
			File:     checked,
			Start:    14,
			Length:   5,
		},
	)

	built := RefinementToDiagnostic(checked, finding, nil)

	related := built.RelatedInformation()
	if len(related) != 2 {
		t.Fatalf("expected 2 related entries, got %d", len(related))
	}
	if built.Code() != 7001 || built.Loc() != core.NewTextRange(20, 21) {
		t.Fatalf("the finding itself changed: code %d at %v", built.Code(), built.Loc())
	}
	if built.File() != checked {
		t.Fatalf("the finding must stay on the checked file")
	}

	if related[0].File() != other {
		t.Fatalf("the first step must keep its own file, got %v", related[0].File())
	}
	if related[0].Loc() != core.NewTextRange(23, 24) {
		t.Fatalf("the first step's span is %v", related[0].Loc())
	}
	if related[0].String() != "'ceiling' is 7 here" {
		t.Fatalf("the first step's sentence is %q", related[0].String())
	}
	if related[1].File() != checked {
		t.Fatalf("the second step must sit on the checked file")
	}
	if related[1].Loc() != core.NewTextRange(14, 19) {
		t.Fatalf("the second step's span is %v", related[1].Loc())
	}
	if related[1].String() != "the bound was instantiated at this call" {
		t.Fatalf("the second step's sentence is %q", related[1].String())
	}
}

// TestNoStepsLeavesRelatedInformationEmpty pins the additive claim: a
// finding that carries no steps is exactly the diagnostic it has always
// been.
func TestNoStepsLeavesRelatedInformationEmpty(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const total = 3;\n")
	built := RefinementToDiagnostic(checked, assignability.RefinementDiagnostic{
		Code:        7002,
		MessageText: assignability.AlertText,
		Start:       14,
		Length:      1,
	}, nil)
	if len(built.RelatedInformation()) != 0 {
		t.Fatalf("a step-free finding must carry no related entries")
	}
	if built.String() != assignability.AlertText {
		t.Fatalf("the message changed: %q", built.String())
	}
}

// TestForeignStepCarriesItsOwnFile is the cross-language half: a step
// in a file the TypeScript program never compiled renders on a
// file-shaped carrier built from the path and text the adapter read, so
// the converter's File()/Text()/SpanMap() reads all answer.
func TestForeignStepCarriesItsOwnFile(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const q = quantile(0.5);\n")
	pythonText := "def quantile(p: float) -> float:\n    return p\n"

	finding := assignability.RefinementDiagnostic{
		Code:        7001,
		MessageText: "the crossed value is not assignable to what Python states",
		Start:       10,
		Length:      13,
	}.WithSteps(assignability.StepInForeignFile(
		"/repo/stats.py", pythonText, 37, 8,
		"Python states '0 ≤ p ≤ 1' here",
	))

	built := RefinementToDiagnostic(checked, finding, func(string) bool { return true })

	related := built.RelatedInformation()
	if len(related) != 1 {
		t.Fatalf("expected the foreign step to render, got %d entries", len(related))
	}
	stepFile := related[0].File()
	if stepFile == nil {
		t.Fatalf("a step with no file panics the renderer (converters.go:467)")
	}
	if stepFile.FileName() != "/repo/stats.py" {
		t.Fatalf("the carrier must name the foreign file, got %q", stepFile.FileName())
	}
	if stepFile.OriginalFileName() != "/repo/stats.py" {
		t.Fatalf("the URI reads OriginalFileName, got %q", stepFile.OriginalFileName())
	}
	if stepFile.Text() != pythonText {
		t.Fatalf("the carrier must hold the foreign text")
	}
	if stepFile.SpanMap() != nil {
		t.Fatalf("the carrier must not read as content-mapped")
	}
	if related[0].Loc() != core.NewTextRange(37, 45) {
		t.Fatalf("the foreign step's span is %v", related[0].Loc())
	}
	if related[0].String() != "Python states '0 ≤ p ≤ 1' here" {
		t.Fatalf("the foreign step's sentence is %q", related[0].String())
	}
}

// TestForeignStepOffDiskIsDropped is the wall, asserted rather than
// described: the renderer converts an offset with a line map the
// session reads off the file system, and dereferences it without a nil
// check (converters.go:378). A step naming a path the file system
// cannot read is therefore dropped here instead of crashing the pull.
func TestForeignStepOffDiskIsDropped(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const q = quantile(0.5);\n")
	finding := assignability.RefinementDiagnostic{
		Code:        7001,
		MessageText: "the crossed value is not assignable to what Python states",
		Start:       10,
		Length:      13,
	}.WithSteps(assignability.StepInForeignFile(
		"/nowhere/stats.py", "def quantile(p): ...\n", 4, 8,
		"Python states '0 ≤ p ≤ 1' here",
	))

	built := RefinementToDiagnostic(checked, finding, func(string) bool { return false })
	if len(built.RelatedInformation()) != 0 {
		t.Fatalf("an unreadable foreign path must not reach the renderer")
	}
	if built.String() != "the crossed value is not assignable to what Python states" {
		t.Fatalf("dropping a step must not touch the finding: %q", built.String())
	}
}

// TestStepWithoutAnyFileIsDropped guards the same nil-File hazard for a
// step that names no file at all.
func TestStepWithoutAnyFileIsDropped(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const total = 3;\n")
	finding := assignability.RefinementDiagnostic{
		Code: 7001, MessageText: "refuted", Start: 14, Length: 1,
	}.WithSteps(assignability.RelatedStep{Sentence: "a place with no file"})
	if got := len(RefinementToDiagnostic(checked, finding, nil).RelatedInformation()); got != 0 {
		t.Fatalf("expected the fileless step dropped, got %d entries", got)
	}
}

// TestStepSpanOutsideItsTextFallsToFileHead pins the other honest
// degradation: an offset the text does not contain places the step at
// the head of its file rather than claiming a span that is not there.
func TestStepSpanOutsideItsTextFallsToFileHead(t *testing.T) {
	checked := parseForSteps(t, "/repo/main.ts", "const total = 3;\n")
	other := parseForSteps(t, "/repo/lib.ts", "export const c = 1;\n")
	finding := assignability.RefinementDiagnostic{
		Code: 7001, MessageText: "refuted", Start: 14, Length: 1,
	}.WithSteps(assignability.RelatedStep{
		Sentence: "past the end", File: other, Start: 9000, Length: 4,
	})
	related := RefinementToDiagnostic(checked, finding, nil).RelatedInformation()
	if len(related) != 1 {
		t.Fatalf("expected the step kept, got %d", len(related))
	}
	if related[0].Loc() != core.NewTextRange(0, 0) {
		t.Fatalf("expected the file head, got %v", related[0].Loc())
	}
}
