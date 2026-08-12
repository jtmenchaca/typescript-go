// The library-adapter boundary's own copy of what a compiled z.*
// chain states — a LEAF package both libraryadapters and
// libraryadapters/zod import, so neither imports annotations
// directly. Go bans the import cycle annotations <-> libraryadapters
// <-> libraryadapters/zod would close if this shape lived in
// annotations.Annotation/Compiled instead (zod_def_chain.ts and
// zod_def_roots.ts call annotations' isUnsupported and read/build
// Annotation values, while annotations/chain_root_constructor.go and
// chain_method.go need libraryadapters.LibraryAdapter — a genuine
// two-way runtime dependency in the TS source, not a type-only one).
//
// AnnotationValue and Compiled here are STRUCTURALLY the TS
// Annotation/Compiled/Unsupported shapes (schema_chain_compiler.ts),
// duplicated at this lower layer the way objectgraphs duplicates the
// kernel wire encoder rather than importing back into kernelbridge
// (graph_specification.go's own comment on the same Go constraint).
// annotations/chain_root_constructor.go and chain_method.go convert
// at the call boundary (annotationOf / toLibraryAdapterCompiled).
package compiledshape

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// Measures mirrors annotations.Measures.
type Measures struct {
	HasSum bool
	Sum    float64
	Sorted bool
}

// WordSpelling mirrors annotations.WordSpelling.
type WordSpelling struct {
	Text   string
	Covers int
}

// AnnotationValue mirrors annotations.Annotation field for field.
type AnnotationValue struct {
	Set            refinementsets.RefinedSet
	Refine         *ast.Node
	Temporal       *refinementsets.TemporalAnnotation
	LibraryAdapter string
	Absent         bool
	Promise        bool
	Date           bool
	KindTag        string
	Passthrough    bool
	Measures       *Measures
	Unread         bool
	Word           *WordSpelling
}

// UnsupportedValue mirrors annotations.Unsupported.
type UnsupportedValue struct {
	Unsupported string
	At          *ast.Node
}

// Compiled mirrors annotations.Compiled: exactly one of Annotation /
// Unsupported is non-nil.
type Compiled struct {
	Annotation  *AnnotationValue
	Unsupported *UnsupportedValue
}

// AnnotationRegistry mirrors annotations.AnnotationRegistry.
type AnnotationRegistry = map[*ast.Symbol]*AnnotationValue

// IsUnsupported is isUnsupported in the TS source, mirrored here so
// the zod def readers need not import annotations.IsUnsupported.
func IsUnsupported(c Compiled) bool {
	return c.Unsupported != nil
}
