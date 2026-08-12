// Pass 1 for one file: object and set annotation statements, plus
// emptiness diagnostics when reporting with a kernel.
//
// Ported 1:1 from annotations/annotation_file_facts.ts.

package annotations

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// emptiness is emptiness in the TS source. A panic from the kernel
// ask (the TS source's try/catch) reads as nil (ok=false):
// emptiness is a courtesy, never a blocker.
func emptiness(kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet) (empty bool, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return kernel.SeqEmpty(set), true
}

// AnnotationFileFactsMerged is the destructured `merged` parameter
// shape shared by compileAnnotationFileFacts and
// compileContractFileFacts (contracts omitted here -- pass 1 does not
// read it).
type AnnotationFileFactsMerged struct {
	Registry AnnotationRegistry
	Objects  ObjectRegistry
}

// AnnotationFileFacts is the {annotations, objects} pair
// compileAnnotationFileFacts returns.
type AnnotationFileFacts struct {
	Annotations map[*ast.Symbol]*Annotation
	Objects     map[*ast.Symbol]*ObjectAnnotation
}

// CompileAnnotationFileFacts is compileAnnotationFileFacts in the TS
// source.
func CompileAnnotationFileFacts(
	p *program.CheckerProgram,
	file *ast.SourceFile,
	merged AnnotationFileFactsMerged,
	kernel *kernelbridge.RefinedTSKernel,
	reporting bool,
	report func(d assignability.RefinementDiagnostic),
) AnnotationFileFacts {
	annotations := map[*ast.Symbol]*Annotation{}
	objects := map[*ast.Symbol]*ObjectAnnotation{}

	tracing.Span("facts.compile.annotations", func() any {
		for _, statement := range file.Statements.Nodes {
			if !ast.IsVariableStatement(statement) {
				continue
			}
			for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				varDecl := declaration.AsVariableDeclaration()
				initializer := varDecl.Initializer
				if initializer == nil || !ast.IsIdentifier(varDecl.Name()) {
					continue
				}
				symbol := p.Checker.GetSymbolAtLocation(varDecl.Name())

				if rootsInObject(p, initializer) || derivedObjectChain(p, initializer, merged.Registry, merged.Objects) {
					compiled := CompileObject(p, initializer, merged.Registry, merged.Objects)
					if compiled.Unsupported != "" {
						// a DIALECT statement the checker cannot
						// honor is plain TypeScript -- untouched,
						// never a loud refusal; the checker's OWN
						// surface still refuses loudly
						if reporting && !rootsInLibraryAdapter(p, initializer) {
							report(assignability.At(initializer, 7004, compiled.Unsupported))
						}
						continue
					}
					if symbol != nil {
						// a libraryAdapter-rooted statement's claims
						// rest on the library's verified semantics --
						// the ledger reads the boundary here
						libraryAdapter := libraryAdapterNameOfChain(p, initializer)
						object := compiled.Object
						if libraryAdapter != "" {
							withAdapter := *compiled.Object
							withAdapter.LibraryAdapter = libraryAdapter
							object = &withAdapter
						}
						objects[symbol] = object
						merged.Objects[symbol] = object
					}
					continue
				}

				if !rootsInSurfaceOrDerived(p, initializer) {
					continue
				}
				// a RECORDS statement -- z.array(z.object(...)) or
				// z.array(z.ref(X)), bounds and all. Not a set
				// annotation: the set route would refuse at
				// z.object. arrayOfRecords reads it at each judged
				// position.
				if readsAsRecordsStatement(p, initializer, merged.Registry, merged.Objects) {
					continue
				}
				compiled := CompileAnnotation(p, initializer, merged.Registry)
				if IsUnsupported(compiled) {
					if reporting && !rootsInLibraryAdapter(p, initializer) {
						report(assignability.At(initializer, 7004, compiled.Unsupported.Unsupported))
					}
					continue
				}
				if symbol != nil {
					annotations[symbol] = compiled.Annotation
					merged.Registry[symbol] = compiled.Annotation
				}
				if !reporting || kernel == nil {
					continue
				}
				// the DELIBERATE empty set -- z.never and the
				// absence-only schemas formatAt bottom as the ray
				// past +infinity -- is the statement's meaning, not
				// an accident worth a lint
				set := derefSet(compiled.Annotation.Set)
				bottomSpelling := len(set.Forms) == 1 &&
					set.Forms[0].Form == refinementsets.FormAbove &&
					math.IsInf(set.Forms[0].A, 1)
				if bottomSpelling {
					continue
				}
				empty, ok := emptiness(kernel, set)
				if ok && empty {
					report(assignability.At(
						initializer, 7003,
						"this annotation denotes the empty set: '"+refinementsets.FormatForDiagnostics(set)+"'",
					))
				}
			}
		}
		return nil
	}, tracing.GrainStep)

	return AnnotationFileFacts{Annotations: annotations, Objects: objects}
}

// rootsInSurfaceOrDerived is the TS source's
// `!rootsInSurface(p, initializer) && !readsAsDerivedAnnotation(p, initializer)`
// guard, inverted to a single positive test (continue-on-false at
// the call site reads the same as the TS early-continue).
func rootsInSurfaceOrDerived(p *program.CheckerProgram, initializer *ast.Node) bool {
	return RootsInSurface(p, initializer) || readsAsDerivedAnnotation(p, initializer)
}
