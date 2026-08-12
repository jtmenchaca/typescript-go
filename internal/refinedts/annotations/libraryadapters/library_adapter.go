// A library adapter is a third-party schema library the checker
// reads as the annotation language it is. Every adapter compiles
// into the same two targets — Annotation (a refined set) and
// ObjectAnnotation — and the core reader, the kernel questions, the
// registry, hover, and judgments never learn a new library exists.
// An adapter owns exactly its own folder, one file per concern:
//
//	recognition.ts — is this declaration file the adapter's package?
//	vocabulary.ts  — the adapter's own names → combinator table
//	quirks.ts      — sound loosenings and version residues, each
//	                 documented with the divergence it guards
//	<name>_library_adapter.test.ts — the drift detector: an upstream
//	                 API change surfaces as one failing adapter test
//
// Two policies are GLOBAL, never per-adapter: a libraryAdapter statement
// the checker cannot honor is NOT-AN-ANNOTATION (plain TypeScript,
// silent) — only the checker's own surface refuses loudly; and any
// quirk that could produce a false claim must compile as a sound
// loosening with a test pinning the divergent case.
//
// Ported 1:1 from annotations/library_adapters/library_adapter.ts.
package libraryadapters

import (
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/zod"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LibraryAdapter is the LibraryAdapter interface in the TS source.
type LibraryAdapter struct {
	// Name is the libraryAdapter id, carried on compiled annotations.
	Name string
	// Declares reports whether this declaration file belongs to the
	// libraryAdapter's package.
	Declares func(fileName string) bool
	// Roots are the libraryAdapter's OWN root constructors (beyond
	// the shared zod-shaped language): name -> the set it denotes.
	Roots map[string]func() refinementsets.RefinedSet
	// Chain are the libraryAdapter's own argument-less chain methods:
	// name -> the forms they add onto a base set.
	Chain map[string]func(base refinementsets.RefinedSet) refinementsets.RefinedSet
	// DefRoots is the COMPLETE def vocabulary: every schema def as a
	// constructor row, every check def as a chain row -- exact forms
	// or a sound claim marked unread, so nothing the libraryAdapter
	// spells is silent. A reader answers nil to hand the spelling
	// back to the shared language.
	DefRoots map[string]zod.DefRootReader
	DefChain map[string]zod.DefChainReader
	// RuntimeRoots / RuntimeMethods are the RUNTIME vocabulary --
	// calls that operate on values (parse, encode, registries) rather
	// than build schemas. A refused spelling OUTSIDE these sets
	// compiles as the unread unknown (total coverage); inside them it
	// stays a plain value.
	RuntimeRoots   map[string]bool
	RuntimeMethods map[string]bool
}

var zodAdapter = LibraryAdapter{
	Name:           "zod",
	Declares:       zod.DeclaresZod,
	Roots:          zod.ZodRoots,
	Chain:          zod.ZodChainVocabulary,
	DefRoots:       zod.ZodDefRoots,
	DefChain:       zod.ZodDefChain,
	RuntimeRoots:   zod.ZodRuntimeRoots,
	RuntimeMethods: zod.ZodRuntimeMethods,
}

// LibraryAdapters is LIBRARY_ADAPTERS in the TS source: the adapters,
// in recognition order.
var LibraryAdapters = []*LibraryAdapter{&zodAdapter}

// LibraryAdapterOfFile is libraryAdapterOfFile in the TS source: the
// libraryAdapter a declaration file belongs to, or nil.
func LibraryAdapterOfFile(fileName string) *LibraryAdapter {
	for _, adapter := range LibraryAdapters {
		if adapter.Declares(fileName) {
			return adapter
		}
	}
	return nil
}

// LibraryAdapterNamed is libraryAdapterNamed in the TS source: a
// libraryAdapter by its id, or nil.
func LibraryAdapterNamed(name string) *LibraryAdapter {
	for _, adapter := range LibraryAdapters {
		if adapter.Name == name {
			return adapter
		}
	}
	return nil
}
