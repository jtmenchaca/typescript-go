// What a TYPE NODE states: a refined set, object, or refinement
// variable; a refusal; or nil-nil for plain TypeScript. Not a schema-
// expression compiler — that question lives in object_schema_compiler.go.
//
// Ported 1:1 from annotations/annotation_of_type.ts.

package annotations

import (
	"bytes"
	"runtime"
	"strconv"
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
// A cycle is a property of ONE CALL STACK, so the guard keys by
// (goroutine, node) — never by the node alone. AST nodes are shared
// across every per-entry checker, and a node-only key read a
// CONCURRENT reader on another entry's goroutine as a cycle: that
// read returned "plain TypeScript" for a written annotation,
// intermittently, with nothing reported — the A1 sweep
// nondeterminism (2026-08-24), where a parameter written `Wide`
// seeded as plain number whenever two entries' reads of the support
// file's nodes overlapped. The facts-divergence self-check
// (service/check.go) is what caught it; this key is the fix at the
// true layer.
type readingTypeNodeKey struct {
	gid  uint64
	node *ast.Node
}

var (
	readingTypeNodesMu sync.Mutex
	readingTypeNodes   = map[readingTypeNodeKey]bool{}
)

// annotationOfType is annotationOfType in the TS source.
func annotationOfType(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) AnnotationOfTypeResult {
	key := readingTypeNodeKey{gid: goroutineID(), node: typeNode}
	readingTypeNodesMu.Lock()
	if readingTypeNodes[key] {
		readingTypeNodesMu.Unlock()
		return AnnotationOfTypeResult{}
	}
	readingTypeNodes[key] = true
	readingTypeNodesMu.Unlock()
	defer func() {
		readingTypeNodesMu.Lock()
		delete(readingTypeNodes, key)
		readingTypeNodesMu.Unlock()
	}()
	return annotationOfTypeUnguarded(p, typeNode, registry, objects, bindings)
}

// goroutineID parses the id out of runtime.Stack's first line — the
// same helper tracing and diagnose carry (each package keeps its own
// copy; the parse is the runtime's documented first-line shape). The
// reentrancy key above needs it because a cycle only exists within
// one goroutine's call stack.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:\n"
	line := buf[:n]
	const prefix = "goroutine "
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0
	}
	line = line[len(prefix):]
	end := bytes.IndexByte(line, ' ')
	if end < 0 {
		return 0
	}
	id, err := strconv.ParseUint(string(line[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return id
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
