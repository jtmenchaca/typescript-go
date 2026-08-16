// from annotations/file_facts_compiler.ts and annotations/interface_hash.ts
//
// Per-file compilation of annotations, objects, contracts, and the
// diagnostics those statements earn — orchestrates the pass-1/2
// readers and stamps the interface hash.
//
// interfaceHashOf's own contracts-folding remainder joins this file
// too: it needs FunctionContract, and per PORT.md's cycle rule that
// puts it here beside CompileFileFacts rather than back in
// annotations (walk-integration-punchlist.md items 5 and 10).
// PartsOfSets/FormatStated/HashOf/SortStrings are the exported pieces
// annotations/interface_hash.go left reusable for exactly this.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// InterfaceHashOf is interfaceHashOf in the TS source: the full
// interface hash, folding the imports and the annotations/objects
// registries (annotations.PartsOfSets) PLUS each function's contract
// — the `c:` line annotations.InterfaceHashOfSets leaves out because
// FunctionContract lives here, not there.
func InterfaceHashOf(
	annotationFacts map[*ast.Symbol]*annotations.Annotation,
	objectFacts map[*ast.Symbol]*annotations.ObjectAnnotation,
	contracts map[*ast.Symbol]*FunctionContract,
	importHashes map[string]string,
) string {
	parts := annotations.PartsOfSets(annotationFacts, objectFacts, importHashes)
	for symbol, contract := range contracts {
		paramSpellings := make([]string, len(contract.Params))
		for i, param := range contract.Params {
			if param == nil {
				paramSpellings[i] = "-"
			} else {
				paramSpellings[i] = annotations.FormatStated(param)
			}
		}
		resultSpelling := "-"
		if contract.Result != nil {
			resultSpelling = annotations.FormatStated(contract.Result)
		}
		line := "c:" + symbol.Name + "=" + strings.Join(paramSpellings, ",") + "->" + resultSpelling
		// a generator contract's yield position is part of what
		// dependents see — only generator contracts spell it, so
		// every other line's hash stays what it was
		if contract.Yield != nil {
			line += " yields " + annotations.FormatStated(contract.Yield)
		}
		parts = append(parts, line+":"+strconv.FormatBool(contract.Grounded))
	}
	annotations.SortStrings(parts)
	return annotations.HashOf(strings.Join(parts, "\n"))
}

// FileFacts is FileFacts in the TS source.
type FileFacts struct {
	Annotations map[*ast.Symbol]*annotations.Annotation
	Objects     map[*ast.Symbol]*annotations.ObjectAnnotation
	Contracts   map[*ast.Symbol]*FunctionContract
	// Diagnostics: the file's own statements' diagnostics — held only
	// when the file was compiled AS the entry (with a kernel for
	// emptiness); nil means "compiled silently", and an entry check
	// recompiles. (TS `readonly RefinementDiagnostic[] | null`; nil
	// slice reads the same as TS null here — always checked with a
	// separate Reporting flag, never by len().)
	Diagnostics    []assignability.RefinementDiagnostic
	HasDiagnostics bool
	// InterfaceHash: the file's own interface spelling — what
	// dependents see.
	InterfaceHash string
	// ImportHashes: the interface hash of each user file this one
	// imports, at the time these facts were compiled.
	ImportHashes map[string]string
}

// FileFactsMerged is the destructured `merged` parameter shape
// compileFileFacts shares with compileAnnotationFileFacts and
// CompileContractFileFacts.
type FileFactsMerged struct {
	Registry  annotations.AnnotationRegistry
	Objects   annotations.ObjectRegistry
	Contracts map[*ast.Symbol]*FunctionContract
}

// CompileFileFacts is compileFileFacts in the TS source.
func CompileFileFacts(
	p *program.CheckerProgram,
	file *ast.SourceFile,
	merged FileFactsMerged,
	kernel *kernelbridge.RefinedTSKernel,
	reporting bool,
	importHashes map[string]string,
) FileFacts {
	var diagnostics []assignability.RefinementDiagnostic
	report := func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}

	annotationFacts := annotations.CompileAnnotationFileFacts(
		p, file,
		annotations.AnnotationFileFactsMerged{Registry: merged.Registry, Objects: merged.Objects},
		kernel, reporting, report,
	)
	contracts := CompileContractFileFacts(
		p, file, merged.Registry, merged.Objects, merged.Contracts, reporting, report,
	)

	interfaceHash := tracing.Span("facts.compile.interfaceHash", func() string {
		return InterfaceHashOf(annotationFacts.Annotations, annotationFacts.Objects, contracts, importHashes)
	}, tracing.GrainStep)

	// complete diagnostics need the kernel (emptiness); a kernel-less
	// compile (the hover path) must not cache an incomplete set
	hasDiagnostics := reporting && kernel != nil

	return FileFacts{
		Annotations:    annotationFacts.Annotations,
		Objects:        annotationFacts.Objects,
		Contracts:      contracts,
		Diagnostics:    diagnostics,
		HasDiagnostics: hasDiagnostics,
		InterfaceHash:  interfaceHash,
		ImportHashes:   importHashes,
	}
}
