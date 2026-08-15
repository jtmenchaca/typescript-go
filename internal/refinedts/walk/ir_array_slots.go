// Array locals flattened into TWO scalar slots for the flow IR.
//
// A body that keeps an array in a local — `const a = [1, 2, 3]` — reads
// its length, pushes onto it, indexes it, and walks it. The kernel's
// walk is over a vector of scalar slots, so such a local is carried as
// TWO: a LENGTH slot spelled "a.len", holding the count as an ordinary
// number, and an ELEMENT slot spelled "a.elem", holding the JOIN of
// everything the array can hold. Every proved transfer applies
// unchanged — the kernel never learns two slots came from one array.
//
// The element slot is a weak summary on purpose: a write at any index
// joins into it rather than replacing it, so the slot always over-
// approximates what an index read can produce. That is the whole
// soundness story — an index read answers the join, never a narrower
// per-position claim.
//
// The recognized uses, total-or-decline over EVERY occurrence of the
// name:
//
//   - `a.length` → the len slot's var.
//   - `a.push(v, …)` → len := len + (the argument count); elem :=
//     join(elem, every argument). push ANSWERS the new length, so
//     `const n = a.push(v)` also writes n := the len slot's var, read
//     after the step.
//   - `a[i]` under a dominating `i < a.length` → the elem slot's var.
//   - `a[i]` with nothing bounding i → orAbsent(elem): the element or
//     undefined, which is exactly what an out-of-range read yields.
//   - `a[i] = v` → elem := join(elem, v) (weak update; len unchanged),
//     and the compound `a[i] += v` the same with the arithmetic in
//     front: elem := join(elem, elem + v).
//   - `for (const x of a)` and `for (x of a)` over a name declared
//     outside → the ordinary loop lowering with x's per-pass effect the
//     elem slot's var.
//   - `const b = [...a]` over an already-flattened sibling array of the
//     same body → a COPY: b's two slots take a's, read var for var. The
//     two hold the same values, so they wear the same sorts, and every
//     reader treats the copy exactly as it treats a literal-built array.
//   - `a.map(cb)`, `a.filter(cb)`, `a.forEach(cb)`, `a.find(cb)`,
//     `a.flatMap(cb)`, `a.reduce(cb, seed)` → the callback routes in
//     ir_callback_summary.go, which read a's two slots and convert the
//     callback. The array's own occurrence as the receiver is consumed
//     here; the callback and any seed still scan.
//
// Any other use of the name — an alias, an argument, a return, a method
// not listed — declines the array, and its slots then resolve to
// nothing so the whole body declines.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// ArrayLocal is one flattened array local: the declaration it came
// from, the name it was spelled under, the two slot names, and the
// literal's own element expressions in source order.
//
// A BRIDGED array — `const a = [...m.values()]` over a flattened Map or
// Set — has no literal elements of its own; it carries the collection it
// was built from instead, and its two slots are written from that
// collection's size and value slots. Every OTHER field, and every
// recognizer answer below, is identical to a literal-initialized
// array's: downstream readers (`a.length`, `a[i]`, `a.push(v)`, a
// for-of, and a concurrent agent's `.map(cb)`) cannot tell the two
// apart, which is the whole point of the bridge.
type ArrayLocal struct {
	Declaration  *ast.Node // VariableDeclaration
	Name         string
	LenSlotName  string // "a.len"
	ElemSlotName string // "a.elem"
	Elements     []*ast.Node
	// BridgedFrom: the spelled name of the Map or Set this array was
	// built from, or "" for an ordinary array literal. Its slots are
	// "<BridgedFrom>.size" and "<BridgedFrom>.vals".
	BridgedFrom string
	// CopiedFrom: the spelled name of the already-flattened ARRAY this one
	// was spread from — `const b = [...a]` — or "" for every other
	// initializer. Its slots are "<CopiedFrom>.len" and "<CopiedFrom>.elem".
	//
	// The twin of MapLocal.CopiedFrom, and it carries the same story: the
	// copy has no literal elements of its own, so it inherits the
	// sibling's in the ordinary Elements field and ArrayElementSort /
	// ArrayElementTypeof answer for it exactly as they answer for the
	// sibling.
	CopiedFrom string
	// Parameter: this array arrived as a PARAMETER (`function f(ids:
	// number[])`) rather than as a declaration with an initializer. There
	// are no element expressions to read a sort from — the caller's values
	// are what the elements will be — so DeclaredElementSort below carries
	// the sort read from the declared TYPE instead, and Elements stays
	// empty.
	//
	// Everything else about the local is a literal-built array's: the same
	// two slot spellings, the same use scan, the same index/length/push
	// readings. What differs is only where the two slots' values come from
	// — the ENTRY state rather than a lowered initializer, since a
	// parameter is already bound when the body starts.
	Parameter bool
	// DeclaredElementSort: the element sort read from a parameter's
	// declared type. Meaningful only when Parameter is set;
	// ArrayElementSort reads it there instead of scanning Elements.
	DeclaredElementSort BindingKind
	// SplitReceiver: the receiver expression of `const parts =
	// s.split(sep)` — the string this array's pieces were cut out of — or
	// nil for every other initializer.
	//
	// A split array has no element expressions of its own (its pieces are
	// computed, not spelled), so it reads like a Parameter array for sort
	// purposes: the pieces are strings, which is what SplitReceiver being
	// set states.
	//
	// The two slots are written UNEVENLY, and that is the honest part of
	// the row. The ELEM slot takes the kernel's drawn-from claim over the
	// receiver — a piece is a contiguous stretch of the receiver, so its
	// scalars all occurred there and it is no longer. The LEN slot takes
	// unknown: how many pieces there are depends on how many times the
	// separator occurs, which the receiver's set does not state, and
	// inventing a count would be a claim with nothing behind it.
	SplitReceiver *ast.Node
}

// statesElementSort is whether this local carries its element sort
// DIRECTLY rather than reading it off spelled elements. Two locals do:
// a parameter array, whose values come from the caller, and a split
// array, whose pieces are computed. Both leave Elements empty, so the
// scan below them would answer for an empty literal instead of for
// what the slot will actually hold.
func (local ArrayLocal) statesElementSort() bool {
	return local.Parameter || local.SplitReceiver != nil
}

// arrayLenSuffix and arrayElemSuffix are the two slot spellings a
// flattened array wears below its name.
const (
	arrayLenSuffix  = ".len"
	arrayElemSuffix = ".elem"
)

// ArrayLocalOf is the recognizer: a declaration `const a = [e, …]`
// whose every use in the body is one of the recognized forms becomes
// the two slots "a.len" and "a.elem"; anything else declines.
//
// `sources` answers what a spelled name in a lone-spread initializer
// already is — a flattened Map or Set for the BRIDGE, a flattened array
// for the COPY. A lone call may pass the zero value, admitting neither.
func ArrayLocalOf(body *ast.Node, declaration *ast.Node, sources flattenedSources) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	// the COPY, tried first: `const b = [...a]` is a lone spread of a bare
	// name, which the bridge reader below would otherwise take for a Set
	// and the literal reader would decline outright
	if copied, ok := copiedArrayLocalOf(body, declaration, sources); ok {
		return copied, true
	}
	// the BRIDGE: its initializer IS an array literal (a lone spread),
	// which the literal reader below would otherwise decline
	if bridged, ok := bridgedArrayLocalOf(body, declaration, sources); ok {
		return bridged, true
	}
	// the SPLIT: `const parts = s.split(sep)` at an astral-safe separator.
	// Its initializer is a CALL, which every reader above and below
	// declines, so it stands on its own and needs no sibling table
	if split, ok := splitArrayLocalOf(body, declaration); ok {
		return split, true
	}
	literal := arrayLiteralOfDeclaration(declaration)
	if literal == nil {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	elements, ok := arrayElementsOf(literal)
	if !ok {
		return ArrayLocal{}, false
	}
	// an element's own initializer must not mention the array — it would
	// read slots the declaration has not written yet
	for _, element := range elements {
		if mentionsName(element, name) {
			return ArrayLocal{}, false
		}
	}
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
		Elements:     elements,
	}, true
}
