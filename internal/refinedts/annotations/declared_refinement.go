// What a signature position or object schema states: the import
// magnets every reader and compiler shares. Sets, objects, arrays
// of records, and refinement variables — not how they are compiled.
//
// Ported 1:1 from annotations/declared_refinement.ts. This file is
// the annotations package's FIRST Go file, landed ahead of its
// directory because FlowContext and half of wave 2 read these types;
// the annotations directory's own porter builds beside it. The TS
// unions become Kind-tagged structs per PORT.md; ts.Node/ts.Symbol
// become *ast.Node/*ast.Symbol; the `.refine` arrow-function field
// is an *ast.Node (the function literal).

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// DependentBound is a dependent bound against a named sibling:
// `z.Gte<"lo">` or a `.refine((r) => r.hi >= r.lo)` row.
type DependentBound struct {
	Op    string // "ge" | "gt" | "le" | "lt"
	Param string
}

// WordSpelling is the surface's own word for a statement — hovers
// speak it instead of the algebra.
type WordSpelling struct {
	Text   string
	Covers int
}

// Measures are sequence MEASURES a statement carries: parse-checked
// facts BETWEEN elements — the exact reduce total, the non-decreasing
// order. HasSum marks Sum as stated (the TS optional number).
type Measures struct {
	HasSum bool
	Sum    float64
	Sorted bool
}

// ObjectKeyValueKind tags ObjectKeySpec's value union.
type ObjectKeyValueKind string

const (
	KeyValueSet        ObjectKeyValueKind = "set"
	KeyValueObject     ObjectKeyValueKind = "object"
	KeyValueCollection ObjectKeyValueKind = "collection"
	KeyValueReference  ObjectKeyValueKind = "reference"
)

// ObjectKeyValue is the value union of ObjectKeySpec, Kind-tagged.
type ObjectKeyValue struct {
	Kind ObjectKeyValueKind

	// set
	Set *refinementsets.RefinedSet
	// Absent is true when the key's statement ALSO admits the absent
	// value (a `.nullable()` chain) — the key is PRESENT, its value
	// may be null.
	Absent bool
	// Measures the key's statement carries; reads through the key
	// wear them.
	Measures *Measures
	// KindTag is the non-double sort the key's values wear:
	// "bigint" | "symbol" | "boolean" | "".
	KindTag string
	// Depends are DEPENDENT bounds against sibling keys, read from a
	// `.refine((r) => r.hi >= r.lo)` body — parse-checked by the
	// refine itself, checked at object positions, and initialized
	// into the order ledger for reads.
	Depends []DependentBound
	Word    *WordSpelling
	// Unread is true when the key's parse checks more than its set
	// says — checked keys alert.
	Unread bool

	// object
	Object *ObjectAnnotation

	// collection — a Map or Set statement: entries check member-by-
	// member through the tuple layer's existing questions, the size
	// through its own window — no new kernel form (a set of members
	// IS a repetition; the deciders are already proved).
	Flavor string // "map" | "set"
	// Key is the key statement — nil for a Set.
	Key *refinementsets.RefinedSet
	// Value is the collection's value statement.
	Value *refinementsets.RefinedSet
	// Size is the admitted sizes (zod's min/max/size are SIZE checks
	// — vendored schemas.ts ZodSet.min = minSize).
	Size *refinementsets.RefinedSet

	// reference
	Target *ast.Symbol
}

type ObjectKeySpec struct {
	Name string
	// Count is the key's cardinality node: how many values occur per
	// object.
	Count *refinementsets.RefinedSet
	// MayBeAbsent is true when the count admits zero — the key may be
	// absent.
	MayBeAbsent bool
	At          *ast.Node
	Value       ObjectKeyValue
}

type ObjectAnnotation struct {
	Keys []ObjectKeySpec
	// LibraryAdapter is the library adapter the statement roots in —
	// see DeclaredRefinement.
	LibraryAdapter string
	// Unread is true when a `.refine` body the reader could not parse
	// rides the statement: the parse checks MORE than the keys say,
	// so an otherwise-proved position stays undetermined, honestly.
	Unread bool
	// WholeKeySet is true when Keys is EVERY key a value carrying this
	// statement has — not merely every key the statement names.
	//
	// The two differ. `Keys` is what the compiler read; a value's own
	// key set is what the runtime produced. They coincide only when
	// the statement's producer is one that strips: zod's default
	// `z.object()` builds its output as a fresh `{}` and writes only
	// the shape's own keys into it (vendored core/schemas.ts:1955-1974
	// — `payload.value = {}`, the loop over `value.keys`, and the
	// no-catchall return before handleCatchall runs), and the installed
	// surface states the same default as `$strip`, whose `out` is `{}`
	// with no index signature (v4/core/schemas.d.cts:604).
	//
	// It is FALSE by default, and every route that loses a key must
	// leave it false: a key the compiler dropped rather than compiled
	// (CompileObject's library-adapter continue), and a statement read
	// from a TYPE annotation, where extra properties are assignable and
	// the runtime value may carry keys no reader ever saw.
	//
	// The reason this is a separate bit rather than "Keys is
	// authoritative": the key list is used for what each NAMED key
	// states, which is sound whether or not other keys exist. Only a
	// COUNT of the keys — Object.keys(x).length — needs the stronger
	// fact, and that is the one this bit licenses.
	WholeKeySet bool
}

type ObjectRegistry = map[*ast.Symbol]*ObjectAnnotation

// DeclaredRefinementKind tags the DeclaredRefinement union.
type DeclaredRefinementKind string

const (
	DeclaredSet               DeclaredRefinementKind = "set"
	DeclaredObject            DeclaredRefinementKind = "object"
	DeclaredObjectArray       DeclaredRefinementKind = "objectArray"
	DeclaredVariable          DeclaredRefinementKind = "variable"
	DeclaredPossiblyUndefined DeclaredRefinementKind = "possiblyUndefined"
	DeclaredTuple             DeclaredRefinementKind = "tuple"
)

// DeclaredRefinement is what a signature position states: a refined
// set, an object annotation, an array of records, a REFINEMENT
// VARIABLE — a TypeScript type parameter read as a variable ranging
// over refined subsets of its bound (`T extends B` states T ⊆ B;
// unconstrained is bounded by the root) — or `X | undefined`.
type DeclaredRefinement struct {
	Kind DeclaredRefinementKind

	// set
	Set *refinementsets.RefinedSet
	// Refine is the `.refine` predicate riding an UNREAD statement —
	// an exact value at a judged position runs it (an arrow function
	// or function expression node).
	Refine *ast.Node
	// Temporal is the chart the set spells, when the annotation is a
	// z.plainDate()-family statement — carries the chart bounds the
	// assignability check resolves through the kernel's calendar lens.
	Temporal *refinementsets.TemporalAnnotation
	// LibraryAdapter: a claim resting on the library's verified
	// semantics carries the library boundary in the ledger.
	LibraryAdapter string
	// KindTag is the non-double sort the statement's values wear:
	// "bigint" | "symbol" | "boolean" | "".
	KindTag string
	// Depends are the DEPENDENT bounds (z.Gte<"name"> and family):
	// the position's values relate to the named sibling parameters'
	// values — every bound of an intersection rides (`Gte<"lo"> &
	// Lte<"hi">` carries both). The set holds the base statement;
	// each CALL SITE instantiates each bound from its named
	// argument's knowledge — exact values make a constant set,
	// windows make subset questions.
	Depends []DependentBound
	// ElementDepends are dependent bounds on EVERY ELEMENT of an
	// array statement (`Array<number & z.Lt<"a">>`): the element's
	// own depends, carried up through the star so the return judge
	// can read them — the set component alone cannot spell a
	// relation.
	ElementDepends []DependentBound
	Measures       *Measures
	// Unread is true when an unread `.refine` rides the chain —
	// checked positions alert instead of accepting.
	Unread bool
	Word   *WordSpelling

	// object / objectArray
	Object *ObjectAnnotation
	// Lo/Hi bound an objectArray's count; HiUnbounded marks the
	// absent hi (the TS `hi: number | null`).
	Lo          float64
	Hi          float64
	HiUnbounded bool

	// variable
	Symbol *ast.Symbol
	Bound  *refinementsets.RefinedSet
	// BoundGrounded records whether the bound came from a stated
	// annotation — only a grounded bound is checked against.
	BoundGrounded bool
	// BoundObject: `T extends <object annotation>` — arguments check
	// against it, and key reads through the variable wear its keys'
	// statements.
	BoundObject *ObjectAnnotation
	// StarDepth is how many array layers wrap the variable: 0 for
	// `T`, 1 for `T[]`, 2 for `T[][]`, …
	StarDepth int

	// possiblyUndefined — the inner statement, or the absent value.
	Inner *DeclaredRefinement

	// tuple — one statement PER SLOT, at exactly this length. A tuple
	// type node (`[Age, Wide]`, `[number, number, number]`) states two
	// facts an array's star cannot: the length is exactly len(Slots),
	// and slot i's own set may differ from slot j's. The star form
	// throws both away — `[Age, Wide]` starred is the union's
	// repetition at any length — so the tuple carries its slots
	// instead.
	//
	// The mirror of the py adapter's `declared.positions`. Only the
	// ALL-REQUIRED form ever fills this: an optional (`T?`), rest
	// (`...T[]`), or named slot costs the exact position-to-length
	// pairing the reading depends on, and such a tuple states nothing
	// here rather than a length it does not have — the same gate
	// typereading/type_node.go's own tuple arm keeps.
	//
	// A slot compiles exactly as an array ELEMENT does, through the
	// same annotationOfType road, so a refined alias in a slot resolves
	// to its set the way it resolves anywhere else.
	Slots []*DeclaredRefinement
}

// AnnotationOfTypeResult is what reading a type node returns: a
// stated refinement, a loud refusal, or nil-nil for plain TypeScript.
type AnnotationOfTypeResult struct {
	Stated      *DeclaredRefinement
	Unsupported string
}
