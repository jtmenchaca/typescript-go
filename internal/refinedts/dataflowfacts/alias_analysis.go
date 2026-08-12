// Unmodeled mutation forgets — the v1 rule rebuilt over refined sets.
// Arrays and objects are reference values: two names can share one, so
// a write through either invalidates what was known through both. The
// checker keeps ALIAS CLASSES (joined when a reference-typed name is
// bound to another name), a read-only allowlist of the methods that
// provably keep the facts, and one rule for everything else: an
// unmodeled method on a reference, or a reference handed to any call,
// forgets the WHOLE alias class. No stale fact survives an unmodeled
// write; number-typed bindings copy by value and are never havocked.
//
// BLOCKED: AliasClasses, forgetPlaceEntries, and updateTracked all need
// abstract_domain (AbstractValue, knownObject, joinKnown, sameKnown) and
// silence (residue) — both out of this directory's allowed import set
// (they are being ported concurrently; see PORT.md). READ_ONLY_ARRAY_METHODS,
// STRING_READ_METHODS, referenceType, syntacticReferenceSort, and
// referenceTyped, which need only the checker, are ported below.
package dataflowfacts

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// ReadOnlyArrayMethods is the array methods that read without writing —
// receiver facts survive them. (Their RESULTS transfer separately where
// a transfer exists; an absent transfer is just an unknown result,
// never a havoc.)
var ReadOnlyArrayMethods = map[string]struct{}{
	"at":             {},
	"concat":         {},
	"entries":        {},
	"every":          {},
	"filter":         {},
	"find":           {},
	"findIndex":      {},
	"findLast":       {},
	"findLastIndex":  {},
	"flat":           {},
	"flatMap":        {},
	"forEach":        {},
	"includes":       {},
	"indexOf":        {},
	"join":           {},
	"keys":           {},
	"lastIndexOf":    {},
	"map":            {},
	"reduce":         {},
	"reduceRight":    {},
	"slice":          {},
	"some":           {},
	"toLocaleString": {},
	"toReversed":     {},
	"toSorted":       {},
	"toSpliced":      {},
	"toString":       {},
	"values":         {},
	"with":           {},
}

// StringReadMethods is the string reads the checker computes on exact
// tuples. Strings copy by value, so this set gates result TRANSFERS,
// never havoc.
var StringReadMethods = map[string]struct{}{
	"split":       {},
	"toUpperCase": {},
	"toLowerCase": {},
	"trim":        {},
	"trimStart":   {},
	"trimEnd":     {},
	"startsWith":  {},
	"endsWith":    {},
	"charAt":      {},
	"charCodeAt":  {},
	"codePointAt": {},
	"repeat":      {},
	"padStart":    {},
	"padEnd":      {},
	"replace":     {},
	"replaceAll":  {},
	"substring":   {},
	"match":       {},
}

// valueish is: does this expression have a reference type (array, tuple,
// object)? Value-typed bindings (numbers, strings, booleans) copy —
// mutation through another name cannot touch them.
const valueish = checker.TypeFlagsNumberLike | checker.TypeFlagsStringLike |
	checker.TypeFlagsBooleanLike | checker.TypeFlagsEnumLike |
	checker.TypeFlagsBigIntLike | checker.TypeFlagsESSymbolLike |
	checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid |
	checker.TypeFlagsNever

// ReferenceType reports whether a type is reference-shaped (an array,
// tuple, or object copies by reference; a union copies only when EVERY
// constituent copies — string|number is value-sorted, string|string[]
// is not).
func ReferenceType(t *checker.Type) bool {
	if (t.Flags() & valueish) != 0 {
		return false
	}
	if t.IsUnion() {
		for _, constituent := range t.Types() {
			if ReferenceType(constituent) {
				return true
			}
		}
		return false
	}
	return true
}

// syntacticReferenceSort answers value-vs-reference by syntax alone,
// where the tree decides it: literals and operators whose result is
// always a primitive copy, and expressions that always construct a
// fresh reference. Nil where only the type can say.
func syntacticReferenceSort(node *ast.Node) *bool {
	t := true
	f := false
	switch node.Kind {
	case ast.KindStringLiteral,
		ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTemplateExpression,
		ast.KindNumericLiteral,
		ast.KindBigIntLiteral,
		ast.KindTrueKeyword,
		ast.KindFalseKeyword,
		ast.KindNullKeyword,
		ast.KindTypeOfExpression:
		return &f
	case ast.KindObjectLiteralExpression,
		ast.KindArrayLiteralExpression,
		ast.KindArrowFunction,
		ast.KindFunctionExpression,
		ast.KindClassExpression,
		ast.KindNewExpression:
		return &t
	case ast.KindPrefixUnaryExpression:
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindExclamationToken ||
			operator == ast.KindMinusToken ||
			operator == ast.KindPlusToken ||
			operator == ast.KindTildeToken {
			return &f
		}
		return nil
	case ast.KindParenthesizedExpression:
		return syntacticReferenceSort(node.AsParenthesizedExpression().Expression)
	default:
		return nil
	}
}

// symbolReferenceSortMu guards symbolReferenceSortCache: one answer per
// SYMBOL, from the type at its own declaration. The declared type
// covers every value the binding can hold, so a mixed union answers
// "reference" even at an occurrence a guard narrowed to a value sort —
// forgetting more, never less.
//
// The TS source keys this memo with a WeakMap<ts.Symbol, boolean>; Go
// has no weak maps, so this substitutes a regular map guarded by a
// mutex. Functionally identical per program: entries live exactly as
// long as the program that produced the symbols is in use by this port.
var (
	symbolReferenceSortMu    sync.Mutex
	symbolReferenceSortCache = map[*ast.Symbol]bool{}
)

// ReferenceTyped reports whether an expression's type is reference-shaped.
func ReferenceTyped(c *checker.Checker, node *ast.Node) bool {
	if syntactic := syntacticReferenceSort(node); syntactic != nil {
		return *syntactic
	}
	if ast.IsIdentifier(node) {
		symbol := c.GetSymbolAtLocation(node)
		if symbol != nil {
			symbolReferenceSortMu.Lock()
			held, ok := symbolReferenceSortCache[symbol]
			symbolReferenceSortMu.Unlock()
			if ok {
				return held
			}
			var t *checker.Type
			if symbol.ValueDeclaration != nil {
				t = c.GetTypeOfSymbolAtLocation(symbol, symbol.ValueDeclaration)
			} else {
				t = c.GetTypeAtLocation(node)
			}
			answer := ReferenceType(t)
			symbolReferenceSortMu.Lock()
			symbolReferenceSortCache[symbol] = answer
			symbolReferenceSortMu.Unlock()
			return answer
		}
	}
	return ReferenceType(c.GetTypeAtLocation(node))
}
