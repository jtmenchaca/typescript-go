// RefinementToDiagnostic is the one place a RefinementDiagnostic
// (assignability.CollectingReasons' own output — what refinementDiagnostics
// records for "errors as you type", editor.1) becomes a host diagnostic
// the LSP renderer converts into a live squiggle. These pins hold the
// conversion itself steady: the code/message/span land unchanged, and
// a related step becomes a navigable relatedInformation entry.

package ls

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/locale"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// refinedtsDiagnosticsTestSourceFile builds a bare *ast.SourceFile from
// literal text — the same construction foreignStepFile itself uses for
// a step outside the compiled program, reused here so this test needs
// no full compiler.Program.
func refinedtsDiagnosticsTestSourceFile(t *testing.T, path string, text string) *ast.SourceFile {
	t.Helper()
	normalized := tspath.NormalizePath(path)
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	node := factory.NewSourceFile(
		ast.SourceFileParseOptions{FileName: normalized, Path: tspath.Path(normalized)},
		text,
		factory.NewNodeList(nil),
		factory.NewToken(ast.KindEndOfFile),
	)
	return node.AsSourceFile()
}

// TestRefinementToDiagnostic_CarriesTheCodeMessageAndSpanUnchanged pins
// the base conversion: a RefinementDiagnostic with no Steps becomes an
// external diagnostic at the same code, message, and span, source
// "refinedts", category Error (every refinement code 7001-7005 is an
// Error — the file's own banner).
func TestRefinementToDiagnostic_CarriesTheCodeMessageAndSpanUnchanged(t *testing.T) {
	file := refinedtsDiagnosticsTestSourceFile(t, "/main.ts", "let age: Age = 200;\n")
	d := assignability.RefinementDiagnostic{
		Code:        7001,
		MessageText: "the value 200 is not assignable to type 'Age'",
		Start:       15,
		Length:      3,
	}
	got := RefinementToDiagnostic(file, d, nil)
	if got.Code() != 7001 {
		t.Errorf("Code() = %d, want 7001", got.Code())
	}
	if got.Localize(locale.Default) != d.MessageText {
		t.Errorf("Localize() = %q, want %q", got.Localize(locale.Default), d.MessageText)
	}
	if got.Loc().Pos() != d.Start || got.Loc().End() != d.Start+d.Length {
		t.Errorf("Loc() = [%d,%d), want [%d,%d)", got.Loc().Pos(), got.Loc().End(), d.Start, d.Start+d.Length)
	}
	if len(got.RelatedInformation()) != 0 {
		t.Errorf("RelatedInformation() has %d entries, want 0 for a Steps-less diagnostic", len(got.RelatedInformation()))
	}
}

// TestRefinementToDiagnostic_AStepWithAFileBecomesRelatedInformation
// pins the Steps arm: a step naming a real *ast.SourceFile (the
// declaration a bound was instantiated from, or the guard that
// established a fact) becomes one relatedInformation entry the
// renderer can turn into a navigable link.
func TestRefinementToDiagnostic_AStepWithAFileBecomesRelatedInformation(t *testing.T) {
	file := refinedtsDiagnosticsTestSourceFile(t, "/main.ts", "let age: Age = readAge();\n")
	stepFile := refinedtsDiagnosticsTestSourceFile(t, "/declare.ts", "type Age = number;\n")
	d := assignability.RefinementDiagnostic{
		Code:        7002,
		MessageText: "Type not yet determined.",
		Start:       4,
		Length:      3,
		Steps: []assignability.RelatedStep{
			{File: stepFile, Start: 5, Length: 3, Sentence: "Age is declared here"},
		},
	}
	got := RefinementToDiagnostic(file, d, nil)
	related := got.RelatedInformation()
	if len(related) != 1 {
		t.Fatalf("RelatedInformation() has %d entries, want 1", len(related))
	}
	if related[0].Localize(locale.Default) != "Age is declared here" {
		t.Errorf("related step message = %q, want %q", related[0].Localize(locale.Default), "Age is declared here")
	}
}

// TestRefinementToDiagnostic_AForeignStepOffDiskIsDropped pins the
// onDisk gate directly: a step naming a foreign file the editor's
// filesystem cannot read is dropped rather than crashing the pull —
// relatedStepDiagnostic's own documented contract.
func TestRefinementToDiagnostic_AForeignStepOffDiskIsDropped(t *testing.T) {
	file := refinedtsDiagnosticsTestSourceFile(t, "/main.ts", "const x = 1;\n")
	d := assignability.RefinementDiagnostic{
		Code:        7001,
		MessageText: "crossing fires",
		Start:       0,
		Length:      1,
		Steps: []assignability.RelatedStep{
			{ForeignFile: "/nonexistent/target.py", ForeignText: "x = 1\n", Start: 0, Length: 1, Sentence: "the Python target states this"},
		},
	}
	onDisk := func(path string) bool { return false }
	got := RefinementToDiagnostic(file, d, onDisk)
	if len(got.RelatedInformation()) != 0 {
		t.Errorf("RelatedInformation() has %d entries, want 0 — the foreign step's file does not exist on disk", len(got.RelatedInformation()))
	}
}
