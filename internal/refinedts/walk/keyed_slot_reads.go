// Values stored under NON-PLAIN KEYS, read back by the evaluation walk.
//
// Two vocabularies live here, shared by the object-literal write side
// (object_literal.go) and the element-access read side
// (element_access.go):
//
//   - the STABLE SYMBOL KEY: a computed member `{ [K]: v }` and a read
//     `o[K]` spelled with the same symbol const name ONE slot, so the
//     value reads back. Module-level consts reuse the field census's own
//     `#sym:` identity (StableSymbolKeyName); a FUNCTION-LOCAL const
//     additionally qualifies here, because the literal that writes the
//     slot and the read that consumes it both sit inside the one walked
//     activation where the const was bound once.
//
//   - the EXACT-WORD ENUMERATION of a string set: a computed key that is
//     one of several exact strings (`flag ? "age" : "years"`) names one
//     of several slots, and the read is the JOIN of those slots.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the stable symbol slot ──────────────────────────────────────── */

// symbolKeyConstruction is what a stable symbol const's initializer
// BUILT: a fresh `Symbol(...)` (Registry false), or a registry
// `Symbol.for(key)` — with the registry key's exact spelling where the
// argument is a string literal. Distinctness proofs read off this:
// a fresh symbol equals nothing built elsewhere, and two registry
// symbols are the same exactly when their keys are (sec-symbol.for).
type symbolKeyConstruction struct {
	Registry         bool
	RegistryKeyKnown bool
	RegistryKey      string
}

// provablyDistinctSymbolKeys is whether two stable symbol consts denote
// provably DISTINCT runtime symbols. At least one fresh `Symbol()`
// proves it — a fresh symbol is a new value every construction, so it
// equals neither another const's fresh symbol nor any registry symbol.
// Two registry constructions prove it only under different spelled
// keys; a registry key the walk cannot spell proves nothing.
func provablyDistinctSymbolKeys(a, b symbolKeyConstruction) bool {
	if !a.Registry || !b.Registry {
		return true
	}
	return a.RegistryKeyKnown && b.RegistryKeyKnown && a.RegistryKey != b.RegistryKey
}

// symbolKeyConstructionOf reads an initializer expression as a symbol
// construction with its details — symbolConstructionCall's recognition
// (ir_field_bundles_symbol_keys.go), plus WHICH construction it was.
func symbolKeyConstructionOf(c *checker.Checker, e *ast.Node) (symbolKeyConstruction, bool) {
	if c == nil || e == nil || !ast.IsCallExpression(e) {
		return symbolKeyConstruction{}, false
	}
	call := e.AsCallExpression()
	if call.QuestionDotToken != nil {
		return symbolKeyConstruction{}, false
	}
	if ast.IsPropertyAccessExpression(call.Expression) {
		access := call.Expression.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil ||
			!ast.IsIdentifier(access.Expression) || access.Expression.Text() != "Symbol" ||
			!ast.IsIdentifier(access.Name()) || access.Name().Text() != "for" ||
			!c.SymbolInDefaultLib(c.GetSymbolAtLocation(access.Expression)) {
			return symbolKeyConstruction{}, false
		}
		out := symbolKeyConstruction{Registry: true}
		if call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
			if text, isText := stringOrNoSubstitutionLiteralText(Unwrapped(call.Arguments.Nodes[0])); isText {
				out.RegistryKeyKnown, out.RegistryKey = true, text
			}
		}
		return out, true
	}
	if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Symbol" &&
		c.SymbolInDefaultLib(c.GetSymbolAtLocation(call.Expression)) {
		return symbolKeyConstruction{}, true
	}
	return symbolKeyConstruction{}, false
}

// enclosingFunctionLikeOf is the nearest function-like ancestor, or nil
// for a module-level node.
func enclosingFunctionLikeOf(node *ast.Node) *ast.Node {
	for cursor := node.Parent; cursor != nil && !ast.IsSourceFile(cursor); cursor = cursor.Parent {
		if ast.IsFunctionLike(cursor) {
			return cursor
		}
	}
	return nil
}

// declarationReRuns is whether a declaration's initializer can run more
// than once inside its enclosing function — any loop between the
// declaration and the function boundary re-enters the declaration, so
// `Symbol(...)` there is a fresh key per iteration and one spelled slot
// would alias several runtime keys.
func declarationReRuns(declaration *ast.Node) bool {
	for cursor := declaration.Parent; cursor != nil && !ast.IsFunctionLike(cursor) && !ast.IsSourceFile(cursor); cursor = cursor.Parent {
		switch cursor.Kind {
		case ast.KindForStatement, ast.KindForInStatement, ast.KindForOfStatement,
			ast.KindWhileStatement, ast.KindDoStatement:
			return true
		}
	}
	return false
}

// stableSymbolSlotOf is the slot name and construction behind a symbol
// key expression, or (false) where the key is not one this reading
// calls stable.
//
// A MODULE-LEVEL const answers the field census's own `#sym:` name
// (StableSymbolKeyName, ir_field_bundles_symbol_keys.go) — one identity
// for the class census, the flattened-record leaves, and this read.
//
// A FUNCTION-LOCAL const answers a name suffixed with the declaration's
// own file and position. Why a local qualifies at all: the literal that
// writes the slot and the element read that consumes it are both inside
// the one walked activation, where the const bound once — a value that
// crosses activations arrives through an entry state, which is never a
// complete tracked literal, so no stale slot survives the crossing. Two
// guards keep that argument honest: the access and the declaration must
// share their enclosing function (a closure reading an outer function's
// const is a different activation story), and the declaration must not
// sit inside a loop (a fresh symbol per iteration would alias one
// spelled slot). The file-and-position suffix keeps a local from ever
// colliding with a module-level `#sym:` name it shadows, or with a
// same-named local in another file.
func stableSymbolSlotOf(c *checker.Checker, key *ast.Node) (string, symbolKeyConstruction, bool) {
	if c == nil || key == nil {
		return "", symbolKeyConstruction{}, false
	}
	use := Unwrapped(key)
	if use == nil || !ast.IsIdentifier(use) {
		return "", symbolKeyConstruction{}, false
	}
	symbol := symbolAt(c, use)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return "", symbolKeyConstruction{}, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return "", symbolKeyConstruction{}, false
	}
	name := declaration.AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return "", symbolKeyConstruction{}, false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || (list.Flags&ast.NodeFlagsConst) == 0 {
		return "", symbolKeyConstruction{}, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return "", symbolKeyConstruction{}, false
	}
	construction, isConstruction := symbolKeyConstructionOf(c, Unwrapped(initializer))
	if !isConstruction {
		return "", symbolKeyConstruction{}, false
	}
	// module level: the one shared identity
	if spelled, isModuleLevel := StableSymbolKeyName(c, use); isModuleLevel {
		return spelled, construction, true
	}
	// function-local: one activation, bound once
	declarationFunction := enclosingFunctionLikeOf(declaration)
	if declarationFunction == nil || declarationFunction != enclosingFunctionLikeOf(use) {
		return "", symbolKeyConstruction{}, false
	}
	if declarationReRuns(declaration) {
		return "", symbolKeyConstruction{}, false
	}
	file := ast.GetSourceFileOfNode(declaration)
	if file == nil {
		return "", symbolKeyConstruction{}, false
	}
	return symbolFieldPrefix + name.Text() + "@" + file.FileName() + ":" + strconv.Itoa(declaration.Pos()),
		construction, true
}

// symbolSlotKey is whether an object key name is a derived symbol slot
// (`#sym:…`) rather than a string key. The string-keyed enumerations —
// Object.keys / values / entries and JSON.stringify — skip these: the
// runtime key is a SYMBOL, which none of those surface (sec-object.keys
// reads EnumerableOwnProperties over String-valued keys only, and
// SerializeJSONObject the same). The write side keeps the prefix
// honest: a SOURCE-SPELLED string key wearing it costs the literal its
// completeness instead of entering the key list (object_literal.go).
func symbolSlotKey(name string) bool {
	return strings.HasPrefix(name, symbolFieldPrefix)
}

/* ── the exact-word enumeration ──────────────────────────────────── */

// exactWordsOfSet enumerates the exact words a set spells, where the
// set is a word or a union of words (the StringTuple encodings a join
// of exact strings builds — JoinKnown's string-sorted arm). (nil,
// false) for any other form: a ray, a difference, a star, or a
// multi-form conjunction enumerates no finite word list here.
func exactWordsOfSet(set refinementsets.RefinedSet) ([][]float64, bool) {
	if word, isWord := refinementsets.WordOf(set); isWord {
		return [][]float64{word}, true
	}
	if len(set.Forms) == 1 && set.Forms[0].Form == refinementsets.FormUnion {
		left, leftOk := exactWordsOfSet(*set.Forms[0].A_)
		if !leftOk {
			return nil, false
		}
		right, rightOk := exactWordsOfSet(*set.Forms[0].B)
		if !rightOk {
			return nil, false
		}
		return append(left, right...), true
	}
	return nil, false
}
