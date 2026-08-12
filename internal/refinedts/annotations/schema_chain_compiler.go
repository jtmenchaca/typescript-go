// Compiling z.* chains to the refined sets they denote — TYPES ONLY
// so far: Annotation, Unsupported, Compiled, AnnotationRegistry are
// the hub types FlowContext and the wave-2 directories read. The
// compiler functions (compileAnnotation and the chain readers) land
// with the annotations directory's own port; this file is completed
// then, 1:1 against schema_chain_compiler.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

type Unsupported struct {
	Unsupported string
	At          *ast.Node
}

// Annotation is what one z.* chain states.
type Annotation struct {
	Set *refinementsets.RefinedSet
	// Refine is the `.refine` predicate riding an UNREAD statement —
	// carried so a judged position holding an EXACT value can run the
	// predicate itself where the compiled set could not speak for it.
	Refine   *ast.Node
	Temporal *refinementsets.TemporalAnnotation
	// LibraryAdapter is the adapter id when the chain roots in a
	// third-party schema package rather than the checker's own
	// surface — carried so the chain reader can apply the adapter's
	// vocabulary and quirks, and so a statement the checker cannot
	// honor stays silent rather than refusing.
	LibraryAdapter string
	// Absent is true when the statement ALSO admits the absent value
	// — a union with a null or undefined member. Absence is not a set
	// member (it leaves ℝ̄), so it rides beside the set the way the
	// maybe wrapper rides beside an AbstractValue.
	Absent bool
	// Promise is true for `z.promise(inner)`: the set is what the
	// RESOLVED value wears, and the parse result itself is a promise
	// OBJECT, which never wears the set. Only an await reads through.
	Promise bool
	// Date is true for `z.date()` chains: the set describes the TIME
	// VALUE (epoch milliseconds) of the Date the parse hands back,
	// not the Date object itself.
	Date bool
	// KindTag is the sort the statement's values wear when it is not
	// the double: "bigint" | "symbol" | "".
	KindTag string
	// Passthrough is true for validation-only schemas whose output
	// VALUES equal the input's exactly (`z.json()`). An exact
	// argument keeps its exact value knowledge; reference identity is
	// never claimed.
	Passthrough bool
	// Measures are sequence MEASURES the statement carries beside the
	// element sets: facts BETWEEN elements, which no per-position
	// form can state.
	Measures *Measures
	// Unread is true when a `.refine` body the reader could not parse
	// rides the chain: the parse checks MORE than the set says, so a
	// position checked against it alerts instead of accepting.
	Unread bool
	// Word is the surface's OWN WORD for the statement — "uuid",
	// "e164" — carried so hovers speak it instead of the compiled
	// grammar's algebra.
	Word *WordSpelling
}

// Compiled is an Annotation or an Unsupported; exactly one side is
// non-nil (the TS union, spread as a pair).
type Compiled struct {
	Annotation  *Annotation
	Unsupported *Unsupported
}

func IsUnsupported(c Compiled) bool {
	return c.Unsupported != nil
}

type AnnotationRegistry = map[*ast.Symbol]*Annotation
