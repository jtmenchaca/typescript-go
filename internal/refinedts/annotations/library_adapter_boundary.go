// Conversions across the annotations <-> libraryadapters boundary.
// The two packages carry structurally identical Compiled/Annotation
// shapes (see libraryadapters/compiledshape/compiled.go's header for
// why they are two Go types rather than one) — this file is the one
// place values cross from one to the other, so a field added to
// Annotation and forgotten here fails at the call sites that convert,
// not silently.
package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
)

func toLibraryAdapterAnnotation(a *Annotation) *compiledshape.AnnotationValue {
	if a == nil {
		return nil
	}
	var measures *compiledshape.Measures
	if a.Measures != nil {
		measures = &compiledshape.Measures{HasSum: a.Measures.HasSum, Sum: a.Measures.Sum, Sorted: a.Measures.Sorted}
	}
	var word *compiledshape.WordSpelling
	if a.Word != nil {
		word = &compiledshape.WordSpelling{Text: a.Word.Text, Covers: a.Word.Covers}
	}
	return &compiledshape.AnnotationValue{
		Set:            derefSet(a.Set),
		Refine:         a.Refine,
		Temporal:       a.Temporal,
		LibraryAdapter: a.LibraryAdapter,
		Absent:         a.Absent,
		Promise:        a.Promise,
		Date:           a.Date,
		KindTag:        a.KindTag,
		Passthrough:    a.Passthrough,
		Measures:       measures,
		Unread:         a.Unread,
		Word:           word,
	}
}

func fromLibraryAdapterAnnotation(a *compiledshape.AnnotationValue) *Annotation {
	if a == nil {
		return nil
	}
	var measures *Measures
	if a.Measures != nil {
		measures = &Measures{HasSum: a.Measures.HasSum, Sum: a.Measures.Sum, Sorted: a.Measures.Sorted}
	}
	var word *WordSpelling
	if a.Word != nil {
		word = &WordSpelling{Text: a.Word.Text, Covers: a.Word.Covers}
	}
	return &Annotation{
		Set:            setPtr(a.Set),
		Refine:         a.Refine,
		Temporal:       a.Temporal,
		LibraryAdapter: a.LibraryAdapter,
		Absent:         a.Absent,
		Promise:        a.Promise,
		Date:           a.Date,
		KindTag:        a.KindTag,
		Passthrough:    a.Passthrough,
		Measures:       measures,
		Unread:         a.Unread,
		Word:           word,
	}
}

func toLibraryAdapterCompiled(c Compiled) compiledshape.Compiled {
	if c.Unsupported != nil {
		return compiledshape.Compiled{Unsupported: &compiledshape.UnsupportedValue{
			Unsupported: c.Unsupported.Unsupported,
			At:          c.Unsupported.At,
		}}
	}
	return compiledshape.Compiled{Annotation: toLibraryAdapterAnnotation(c.Annotation)}
}

func fromLibraryAdapterCompiled(c compiledshape.Compiled) *Compiled {
	if c.Unsupported != nil {
		return &Compiled{Unsupported: &Unsupported{Unsupported: c.Unsupported.Unsupported, At: c.Unsupported.At}}
	}
	return &Compiled{Annotation: fromLibraryAdapterAnnotation(c.Annotation)}
}

func toLibraryAdapterRegistry(registry AnnotationRegistry) compiledshape.AnnotationRegistry {
	out := make(compiledshape.AnnotationRegistry, len(registry))
	for symbol, annotation := range registry {
		out[symbol] = toLibraryAdapterAnnotation(annotation)
	}
	return out
}

// libraryAdapterCompile builds a libraryadapters-facing compile
// callback from a *program.CheckerProgram + registry pair, so
// chain_root_constructor.go and chain_method.go need not repeat the
// wrapping at each DefReadContext construction.
func libraryAdapterCompile(compile func(e *ast.Node) Compiled) func(e *ast.Node) compiledshape.Compiled {
	return func(e *ast.Node) compiledshape.Compiled {
		return toLibraryAdapterCompiled(compile(e))
	}
}
