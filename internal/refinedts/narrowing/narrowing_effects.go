// The two-sided narrowing result: what one recognized test states
// (Narrowed) and what a condition states per branch
// (BranchNarrowings, whenTrue/whenFalse — TypeScript's own names).
// Split from condition_analysis.ts per the v2 tree.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// Narrowed is Narrowed in the TS source.
type Narrowed struct {
	// Binding is the name the narrowing lands on.
	Binding string
	// Path is the keys walked from that name — `o.total` is ["total"]. A
	// guard on a property narrows exactly that key, leaving the rest
	// of the object alone.
	Path  []string
	Forms []refinementsets.Refinement
	// Refuting is true when the claim is WEAK — the kernel's strength bit,
	// inverted. A weak claim holds only for values already known
	// inside ℝ̄; applying it to an unknown binding would smuggle NaN
	// into a set claim. A strong claim's holding itself proves the
	// value real.
	Refuting bool

	// Definedness: an absence test (`=== undefined` / `=== null`):
	// "defined" strips the maybe wrapper, "undefined" pins the absent
	// value. A real runtime test either way — NaN plays no part in it.
	// "" means the TS source's field was absent.
	Definedness string // "" | "defined" | "undefined"

	// KeepAbsent: the claim narrows the PRESENT part only and proves
	// nothing about absence — the `P === undefined || <numeric tests
	// on P>` disjunction's whenTrue, where the held condition admits
	// the absent value OR a value the numeric side proves. Applied to
	// a maybe-wrapped value, Forms narrow the inner part and the
	// wrapper stays; the bare absent value passes whole.
	KeepAbsent bool

	// Truthiness: a bare-place condition read through ToBoolean: held
	// truth keeps only truthy values (and rules NaN out — ToBoolean of
	// NaN is false); held falsity keeps only the falsy ones, absence
	// included. Filters number- and boolean-sorted words; every other
	// shape passes untouched. "" means the TS source's field was absent.
	Truthiness string // "" | "truthy" | "falsy"

	// Exact: a held equality with a literal pins the EXACT tuple —
	// checked by membership, which is decidable for every shape. nil
	// means the TS source's optional field was absent.
	Exact []float64
	// ExactSort is the sort the pinned tuple was read under (a string
	// equality pins a STRING word). "" means the TS source's field was
	// absent — read as PrimitiveNumber, mirroring `n.exactSort ?? "number"`
	// at call sites.
	ExactSort abstractdomain.PrimitiveKind

	// Shape: the SHAPE a type test proves — the ground a typeof word
	// names, the object an instanceof holds for, the key an `in` finds.
	// Met with what is held: it fills an unknown, adds a key to an
	// object, and never displaces a sharper fact. Every such test
	// answers false for the absent value, so holding proves presence
	// too. HasShape takes the place of TS's optional field.
	Shape    abstractdomain.AbstractValue
	HasShape bool

	// ExcludesKind: the kind a REFUTED type test rules out: `typeof x
	// === "number"` false proves a present x is not a number, so a kind
	// union drops its number arms. Nothing else changes — the test's
	// falsity proves neither presence nor any set fact, and number
	// never conflates with boolean here (their typeof words differ).
	// "" means the TS source's field was absent.
	ExcludesKind string // "" | "string" | "number" | "boolean" | "object" | "symbol"

	// WordSet: the exact WORDS a held literal-equality DISJUNCTION
	// admits (`x === "a" || x === "b"`): the value is one of these
	// string tuples, so word-listable claims intersect and a kind union
	// drops arms sharing no word. Holding proves presence. HasWordSet
	// takes the place of TS's optional field.
	WordSet    [][]float64
	HasWordSet bool

	// ExcludesBooleanWord: the word a REFUTED boolean-literal equality
	// rules out — `b !== false` proves a BOOLEAN b is exactly true.
	//
	// Strict inequality against a boolean literal also holds for every
	// NON-boolean value (`"x" !== false` is true), so this narrowing
	// applies only where the held value is already known boolean-sorted:
	// a KindValues tagged PrimitiveBoolean. Every other shape passes
	// untouched, which is the answer that discards no runtime value.
	// HasExcludesBooleanWord takes the place of TS's optional field.
	ExcludesBooleanWord    float64
	HasExcludesBooleanWord bool

	// WordSetExcluded: the dual — the REFUTED disjunction proves the
	// value is NONE of these words — word-listable claims shed them,
	// and an arm whose every word is excluded drops. Falsity proves no
	// presence. HasWordSetExcluded takes the place of TS's optional
	// field.
	WordSetExcluded    [][]float64
	HasWordSetExcluded bool

	// SequenceBrand: a held `Array.isArray(x)` proves x IS an Array
	// exotic object (sec-array.isarray answers true for exactly those),
	// so a kind union keeps only the arms an Array could be and drops
	// the rest. It is a POSITIVE brand claim, unlike ExcludesKind's
	// refuted test, and it is deliberately separate from the typeof
	// channel: typeof an Array answers "object", the same word every
	// other graph value answers, so a typeof narrowing cannot express
	// what isArray proves.
	//
	// What survives is stated by what the tuple layer can and cannot
	// tell apart. A set-shaped arm whose forms speak SEQUENCE (a
	// repetition, a concatenation, a word) is kept: strings and arrays
	// share that layer, so such an arm may be the Array — and a plain
	// string arm is kept for the same reason, since nothing in the
	// claim distinguishes it. The graph kinds an array value wears —
	// KindList, KindObjectStar, KindArrayHoles, and the flat
	// PrimitiveArray tuple — are kept outright. Every SCALAR arm
	// (number, boolean, bigint, symbol) goes: none of those is an
	// Object at all, so isArray answers false for them
	// (sec-array.isarray step 1's IsArray on a non-Object).
	//
	// An arm the reading cannot classify keeps its place — the answer
	// that discards no runtime value.
	SequenceBrand bool

	// RefutedBrand: the default-library constructor a REFUTED
	// `instanceof` rules out — the symmetric half of the true arm's
	// Shape row. `x instanceof Map` answering FALSE proves the Map
	// prototype is nowhere on x's prototype chain
	// (sec-instanceofoperator step 5 hands a default-library
	// constructor to OrdinaryHasInstance, and sec-ordinaryhasinstance's
	// Repeat walks [[GetPrototypeOf]] until null, returning false only
	// when %Map.prototype% never appeared), so a kind union drops every
	// arm whose kind IS that brand's.
	//
	// Only a DEFAULT-LIBRARY constructor is ever written here — the same
	// gate the true arm keeps (ambientConstructor's own doc): a
	// third-party class may install %Symbol.hasInstance% and answer
	// anything at all, in which case step 3 returns before
	// OrdinaryHasInstance ever runs and the prototype-chain reading says
	// nothing. The brands the refutation can decide are the ones whose
	// value kind the domain spells: "Map", "Set", "Array", "Date",
	// "RegExp".
	//
	// A brand the refutation CANNOT decide about an arm leaves that arm
	// standing — a Map exotic object and a plain object are distinct
	// kinds here, but a set-shaped or unclassified arm says nothing
	// about its prototype chain, so it keeps its place. "" means no
	// refuted brand rides this narrowing.
	RefutedBrand string // "" | "Map" | "Set" | "Array" | "Date" | "RegExp"
}

// BranchNarrowings is BranchNarrowings in the TS source.
type BranchNarrowings struct {
	WhenTrue  []Narrowed
	WhenFalse []Narrowed
}

// None is NONE in the TS source.
var None = BranchNarrowings{}
