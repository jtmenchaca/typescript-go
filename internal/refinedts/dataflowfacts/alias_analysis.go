// Unmodeled mutation forgets — the v1 rule rebuilt over refined sets.
// Arrays and objects are reference values: two names can share one, so
// a write through either invalidates what was known through both. The
// checker keeps ALIAS CLASSES (joined when a reference-typed name is
// bound to another name), a read-only allowlist of the methods that
// provably keep the facts, and one rule for everything else: an
// unmodeled method on a reference, or a reference handed to any call,
// forgets the WHOLE alias class. No stale fact survives an unmodeled
// write; number-typed bindings copy by value and are never havocked.
package dataflowfacts

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
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
	// the Annex B aliases: "The initial value of the *trimLeft* property
	// is %String.prototype.trimStart%" (String.prototype.trimleft,
	// String.prototype.trimright) — the SAME function objects
	"trimLeft":  {},
	"trimRight": {},
	// String.prototype.toString on a string is the string itself
	// (sec-string.prototype.tostring)
	"toString": {},
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

// InvalidatableFact is the TS source's InvalidatableFact interface: a
// difference row as the invalidation machinery sees it — two places rooted
// at base names, and a mutable staleness flag. Implemented by
// *DifferenceConstraint (its Minuend/Subtrahend fields carry BaseName).
// The full row shape lives in relations.ts in the TS source; this
// structural view keeps the import direction one-way.
type InvalidatableFact interface {
	MinuendBaseName() string
	SubtrahendBaseName() string
	SetDead(bool)
}

// MinuendBaseName implements InvalidatableFact.
func (d *DifferenceConstraint) MinuendBaseName() string { return d.Minuend.BaseName }

// SubtrahendBaseName implements InvalidatableFact.
func (d *DifferenceConstraint) SubtrahendBaseName() string { return d.Subtrahend.BaseName }

// SetDead implements InvalidatableFact.
func (d *DifferenceConstraint) SetDead(dead bool) { d.Dead = dead }

// rootedFact is one entry of AliasClasses' liveFacts ledger: a fact of any
// arity (registerRooted), registered with every base name it roots in.
// The TS source keys this ledger with a `Map<{dead?}, readonly string[]>`,
// comparing entries by object identity; Go's map equivalent needs a
// comparable key, so this carries the fact by an interface value (pointer
// underneath — DifferenceConstraint/SumConstraint entries are addressed
// from their owning slice) alongside its roots.
type rootedFact struct {
	fact  deadMarker
	roots []string
}

// deadMarker is the minimal shape registerRooted needs: something whose
// staleness flag this ledger can set. *DifferenceConstraint and
// *SumConstraint both satisfy it via their own SetDead.
type deadMarker interface {
	SetDead(bool)
}

// SetDead implements deadMarker for *SumConstraint.
func (s *SumConstraint) SetDead(dead bool) { s.Dead = dead }

// AliasClasses: which names may share one reference. Joining is monotone
// and conservative — an over-wide class only forgets more, never less.
//
// The class also holds the LIVE difference rows: every recorded row
// registers here, and every write invalidates the rows its name (or any
// alias of it) could have made stale — the flow-sensitive half of row
// validity, in walk order. A loop's condition rows revalidate at each
// body entry, where the condition has just re-passed.
//
// Not safe for concurrent use — one instance threads through one walk,
// mirroring the TS class's plain (unsynchronized) Map/Set fields.
type AliasClasses struct {
	// groups maps every member name to the SAME shared set for its
	// class — the TS source's `Map<string, Set<string>>` sharing one
	// Set object across every member. Go has no shared-mutable-set
	// aliasing through separate map entries the way TS's Map<string,
	// Set> does implicitly (mutating one entry's Set value mutates it
	// for every key pointing at the same object) without an explicit
	// pointer, so this carries *map[string]struct{} — a pointer to the
	// class's member set — as the shared value.
	groups map[string]*map[string]struct{}
	// liveFacts: every live fact maps to the base names it roots in —
	// ANY arity, so a two-place difference row and a three-place sum
	// row invalidate through the one walk. Keyed by the fact's identity
	// (its pointer, carried inside rootedFact); a Go map cannot key on
	// an interface holding a pointer to a growing slice element
	// directly the way a WeakMap would use object identity, so this is
	// a slice of (fact, roots) pairs walked linearly — the same walk
	// invalidate() does in the TS source regardless (it iterates the
	// whole Map on every call).
	liveFacts []rootedFact
}

// NewAliasClasses builds an empty AliasClasses.
func NewAliasClasses() *AliasClasses {
	return &AliasClasses{groups: map[string]*map[string]struct{}{}}
}

// Link is link in the TS source.
func (a *AliasClasses) Link(x, y string) {
	group := a.groups[x]
	if group == nil {
		group = a.groups[y]
	}
	if group == nil {
		fresh := map[string]struct{}{}
		group = &fresh
	}
	if existing := a.groups[x]; existing != nil {
		for member := range *existing {
			(*group)[member] = struct{}{}
		}
	} else {
		(*group)[x] = struct{}{}
	}
	if existing := a.groups[y]; existing != nil {
		for member := range *existing {
			(*group)[member] = struct{}{}
		}
	} else {
		(*group)[y] = struct{}{}
	}
	(*group)[x] = struct{}{}
	(*group)[y] = struct{}{}
	for member := range *group {
		a.groups[member] = group
	}
}

// ClassOf is classOf in the TS source.
func (a *AliasClasses) ClassOf(name string) map[string]struct{} {
	if group := a.groups[name]; group != nil {
		out := make(map[string]struct{}, len(*group))
		for member := range *group {
			out[member] = struct{}{}
		}
		return out
	}
	return map[string]struct{}{name: {}}
}

// Register is register in the TS source.
func (a *AliasClasses) Register(rows []InvalidatableFact) {
	for _, row := range rows {
		a.liveFacts = append(a.liveFacts, rootedFact{
			fact:  row,
			roots: []string{row.MinuendBaseName(), row.SubtrahendBaseName()},
		})
	}
}

// RegisterRooted is registerRooted in the TS source: a fact of any arity,
// registered with every base name it roots in, invalidated when any of
// them (or an alias) is written.
func (a *AliasClasses) RegisterRooted(fact deadMarker, roots []string) {
	a.liveFacts = append(a.liveFacts, rootedFact{fact: fact, roots: roots})
}

// Invalidate is invalidate in the TS source: a write to name (or any alias
// of it) invalidates every live fact any of whose places roots there.
func (a *AliasClasses) Invalidate(name string) {
	if len(a.liveFacts) == 0 {
		return
	}
	mates := a.ClassOf(name)
	for _, entry := range a.liveFacts {
		for _, root := range entry.roots {
			if _, ok := mates[root]; ok {
				entry.fact.SetDead(true)
				break
			}
		}
	}
}

// Havoc is havoc in the TS source: forget the whole class wherever it is
// bound.
func (a *AliasClasses) Havoc(env map[string]abstractdomain.AbstractValue, name string) {
	for member := range a.ClassOf(name) {
		if _, ok := env[member]; ok {
			env[member] = silence.Residue()
		}
		ForgetPlaceEntries(env, member)
	}
	a.Invalidate(name)
}

// ForgetPlaceEntries is forgetPlaceEntries in the TS source: drop every
// dotted place entry rooted at a name — the sweep every write and havoc of
// that name owes the place-value memory (assume_condition records the
// dotted keys; dots never appear in identifiers).
func ForgetPlaceEntries(env map[string]abstractdomain.AbstractValue, root string) {
	prefix := root + "."
	for key := range env {
		if strings.HasPrefix(key, prefix) {
			delete(env, key)
		}
	}
}

// UpdateTracked is updateTracked in the TS source: replace a tracked
// name's knowledge, CLASS-AWARE. A same-shaped alias (it held the very
// value) takes the new one. An EMBEDDER — its knowledge holds the written
// reference under a key — sees that key either as the very object (the
// new value) or as another one (the old): their join covers both. Any
// other object-shaped class member is a CANDIDATE sharer (a
// branch-selected alias), and the join of its own state with the new
// value covers both cases too. Everything else forgets.
func UpdateTracked(
	aliases *AliasClasses,
	env map[string]abstractdomain.AbstractValue,
	name string,
	next abstractdomain.AbstractValue,
) {
	aliases.Invalidate(name)
	ForgetPlaceEntries(env, name)
	held, ok := env[name]
	if !ok {
		held = silence.Residue()
	}
	for member := range aliases.ClassOf(name) {
		memberKnown, ok := env[member]
		if !ok {
			continue
		}
		if member == name || abstractdomain.SameKnown(memberKnown, held) {
			env[member] = next
			continue
		}
		if memberKnown.Kind == abstractdomain.KindObject {
			keys := make([]abstractdomain.ObjectKey, len(memberKnown.Keys))
			copy(keys, memberKnown.Keys)
			embeds := false
			for i, key := range keys {
				if abstractdomain.SameKnown(key.Value, held) {
					keys[i].Value = abstractdomain.JoinKnown(held, next)
					embeds = true
				}
			}
			if embeds {
				env[member] = abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false)
				continue
			}
			env[member] = abstractdomain.JoinKnown(memberKnown, next)
			continue
		}
		env[member] = silence.Residue()
	}
}
