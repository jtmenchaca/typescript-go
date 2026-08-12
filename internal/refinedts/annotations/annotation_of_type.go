// What a TYPE NODE states: a refined set, object, or refinement
// variable; a refusal; or nil-nil for plain TypeScript. Not a schema-
// expression compiler — that question lives in object_schema_compiler.go.
//
// Ported 1:1 from annotations/annotation_of_type.ts.

package annotations

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// readingTypeNodes is the TS source's `Set<ts.TypeNode>` reentrancy
// guard: a node re-entered before its own read finished is a CYCLE --
// a recursive alias, a self-referential constraint -- and a cycle
// states nothing the reader can close, so it reads as plain
// TypeScript. One guard for every arm: tailwindcss's first recursive
// alias took a worker down through the alias arm, and the constraint
// arm was next.
//
// The TS source's module-level Set becomes a mutex-guarded map here
// (this port's substitute for shared mutable cross-call state; see
// narrowing/pinned_function.go's resolvingFactoryBindings for the
// same pattern) -- the checker walk is not guaranteed single-threaded
// the way a TS worker's synchronous call stack is.
var (
	readingTypeNodesMu sync.Mutex
	readingTypeNodes   = map[*ast.Node]bool{}
)

// annotationOfType is annotationOfType in the TS source.
func annotationOfType(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) AnnotationOfTypeResult {
	readingTypeNodesMu.Lock()
	if readingTypeNodes[typeNode] {
		readingTypeNodesMu.Unlock()
		return AnnotationOfTypeResult{}
	}
	readingTypeNodes[typeNode] = true
	readingTypeNodesMu.Unlock()
	defer func() {
		readingTypeNodesMu.Lock()
		delete(readingTypeNodes, typeNode)
		readingTypeNodesMu.Unlock()
	}()
	return annotationOfTypeUnguarded(p, typeNode, registry, objects, bindings)
}

func annotationOfTypeUnguarded(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) AnnotationOfTypeResult {
	if sets, matched := annotationOfTypeSets(p, typeNode, registry, objects, bindings); matched {
		return sets
	}
	if utilities, matched := annotationOfTypeUtilities(p, typeNode, registry, objects, bindings); matched {
		return utilities
	}
	if aliases, matched := annotationOfTypeAliases(p, typeNode, registry, objects, bindings); matched {
		return aliases
	}
	return AnnotationOfTypeResult{}
}

// AnnotationOfType is the exported entry point (annotationOfType in
// the TS source is exported directly there; this wrapper starts a
// call with no instantiation bindings, matching every external call
// site's `annotationOfType(p, typeNode, registry, objects)` — the
// bindings parameter is optional in TS).
func AnnotationOfType(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) AnnotationOfTypeResult {
	return annotationOfType(p, typeNode, registry, objects, nil)
}
