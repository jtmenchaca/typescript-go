// PLACES and WRITE SCOPES: the identities relational facts attach
// to, and the filters that say how long a fact recorded about a
// place stays true.
//
// A place is a base binding plus a property path — `x` or `o.lo`.
// A fact recorded about a place (an order row, a gate verdict)
// binds every later read of the SAME place only while nothing can
// have rewritten it. The filters here scope that check to where the
// fact is actually consumed — the guarded branch, the statement
// list, the function body — instead of refusing any binding the
// function ever writes:
//
//   - a BARE binding goes stale only through a direct write (an
//     assignment, ++/--, a for-head) in the consumption scope, or
//     through any closure of the enclosing function (a closure can
//     run at any time), or through `arguments` aliasing;
//   - a PROPERTY place additionally goes stale through calls that
//     could reach its base — a method on the base, the base handed
//     to a callee — because an object travels by reference.
//
// Reading a property place as stable DATA is the checker's standing
// model (stated object keys are read everywhere); a stateful getter
// is outside that model here exactly as it is in every other read.
package dataflowfacts

import (
	"strconv"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// PlaceKey identifies a place: a base binding plus a property path.
type PlaceKey struct {
	Base *ast.Symbol
	// "" for the bare binding; ".k.l" for a property chain.
	Path string
	// The base identifier's text — what the write filters match.
	BaseName string
}

// EnclosingThisClass is the class whose instance `this` names at `site` —
// read through the containers that keep the surrounding `this` (arrow
// functions) and stopped by the ones that bind their own (function
// declarations and expressions, static members, static blocks).
// Nil wherever `this` is not a class instance.
func EnclosingThisClass(site *ast.Node) *ast.Node {
	cursor := site
	for cursor != nil {
		if ast.IsFunctionDeclaration(cursor) || ast.IsFunctionExpression(cursor) ||
			ast.IsClassStaticBlockDeclaration(cursor) {
			return nil
		}
		if ast.IsMethodDeclaration(cursor) || ast.IsConstructorDeclaration(cursor) ||
			ast.IsGetAccessorDeclaration(cursor) ||
			ast.IsSetAccessorDeclaration(cursor) ||
			ast.IsPropertyDeclaration(cursor) {
			if (ast.GetCombinedModifierFlags(cursor) & ast.ModifierFlagsStatic) != 0 {
				return nil
			}
			parent := cursor.Parent
			if parent != nil && (ast.IsClassDeclaration(parent) || ast.IsClassExpression(parent)) {
				return parent
			}
			return nil
		}
		cursor = cursor.Parent
	}
	return nil
}

// EnclosingThisObjectLiteralMethod is the OBJECT-LITERAL method,
// getter, or setter whose own call/read/write the walk route binds
// `this` for — `{ age: 40, bump() { this.age = this.age + 1 } }`,
// `{ _age: 40, get age() { return this._age } }` — read through the
// containers that keep the surrounding `this` (arrow functions), the
// same climb EnclosingThisClass makes; stopped the first time it
// reaches a method/getter/setter declaration (returning it only when
// its own parent is an object literal, never a class), a function
// declaration or expression, or a static block — none of those share
// the object literal's `this`.
//
// Nil wherever `site` names no such member: a class member's `this`
// is EnclosingThisClass's own instance, and a plain function's `this`
// is its own dynamic receiver. The method-call walk route
// (ObjectLiteralMethodWalkCall, method_this_writes.go) and the
// object-literal getter's own body walk (object_literal.go's direct
// and spread-source routes) are the only callers that ever bind
// "this" in env for a member matching this climb, so a positive
// answer here is exactly when that binding is live to read.
func EnclosingThisObjectLiteralMethod(site *ast.Node) *ast.Node {
	cursor := site
	for cursor != nil {
		if ast.IsFunctionDeclaration(cursor) || ast.IsFunctionExpression(cursor) ||
			ast.IsClassStaticBlockDeclaration(cursor) {
			return nil
		}
		if ast.IsMethodDeclaration(cursor) || ast.IsGetAccessorDeclaration(cursor) ||
			ast.IsSetAccessorDeclaration(cursor) {
			if (ast.GetCombinedModifierFlags(cursor) & ast.ModifierFlagsStatic) != 0 {
				return nil
			}
			parent := cursor.Parent
			if parent != nil && ast.IsObjectLiteralExpression(parent) {
				return cursor
			}
			return nil
		}
		if ast.IsConstructorDeclaration(cursor) ||
			ast.IsPropertyDeclaration(cursor) {
			return nil
		}
		cursor = cursor.Parent
	}
	return nil
}

// EnclosingThisOwner is the nearest node — through arrow functions,
// the same climb EnclosingThisClass and EnclosingThisObjectLiteralMethod
// make — that OWNS `site`'s `this`: the FunctionDeclaration,
// FunctionExpression, MethodDeclaration, ConstructorDeclaration,
// GetAccessorDeclaration, or SetAccessorDeclaration `this` dynamically
// binds to at a call, or nil at a class static block or module top
// level (neither owns a callable `this`). Unlike the two climbs above
// — which answer nil unless the owner's OWN static position (an
// object-literal method, a this-parameter function) already proves
// what `this` is — this one answers the owner REGARDLESS of position,
// so a caller can compare it by IDENTITY against a specific
// declaration node it already knows binds `this` some other way (a
// property-alias walk route binding "this" to a receiver for exactly
// ONE declaration, at exactly one call).
func EnclosingThisOwner(site *ast.Node) *ast.Node {
	cursor := site
	for cursor != nil {
		if ast.IsFunctionDeclaration(cursor) || ast.IsFunctionExpression(cursor) ||
			ast.IsMethodDeclaration(cursor) || ast.IsConstructorDeclaration(cursor) ||
			ast.IsGetAccessorDeclaration(cursor) || ast.IsSetAccessorDeclaration(cursor) {
			return cursor
		}
		if ast.IsClassStaticBlockDeclaration(cursor) || ast.IsPropertyDeclaration(cursor) {
			return nil
		}
		cursor = cursor.Parent
	}
	return nil
}

// EnclosingThisParameterFunction is the function-declaration or
// function-expression whose OWN written `this` parameter `site`
// reads — `function withThis(this: { age: number }) { return
// this.age; }`. Read through the containers that keep the
// surrounding `this` (arrow functions), the same way
// EnclosingThisClass climbs; stopped the first time it reaches a
// function declaration or expression, whether or not THAT function
// itself declares a `this` parameter — a `this` inside a plain,
// this-less function is its own dynamic receiver, not this site's.
//
// Nil wherever `site` names no such function: a class method's
// `this` is EnclosingThisClass's own instance, never this reading's
// (a class member's `this` parameter is not TypeScript's own
// grammar), and a static block or top-level `this` has no enclosing
// function at all.
func EnclosingThisParameterFunction(site *ast.Node) *ast.Node {
	cursor := site
	for cursor != nil {
		if ast.IsFunctionDeclaration(cursor) || ast.IsFunctionExpression(cursor) {
			if declaredThisParameter(cursor) != nil {
				return cursor
			}
			return nil
		}
		if ast.IsMethodDeclaration(cursor) || ast.IsConstructorDeclaration(cursor) ||
			ast.IsGetAccessorDeclaration(cursor) || ast.IsSetAccessorDeclaration(cursor) ||
			ast.IsClassStaticBlockDeclaration(cursor) {
			return nil
		}
		cursor = cursor.Parent
	}
	return nil
}

// declaredThisParameter is a function-like declaration's own written
// `this` parameter node — TypeScript's own syntax
// (function f(this: T, …)) marks it as the first parameter whose
// name is literally the identifier "this" (sec 3.6.3 of the
// TypeScript Handbook's this-parameters section; tsgo's own parser
// recognizes no separate AST node kind for it). Nil where the
// function declares no such parameter.
func declaredThisParameter(declaration *ast.Node) *ast.Node {
	parameters := declaration.Parameters()
	if len(parameters) == 0 {
		return nil
	}
	first := parameters[0]
	name := first.AsParameterDeclaration().Name()
	if name != nil && ast.IsIdentifier(name) && name.Text() == "this" {
		return first
	}
	return nil
}

// PlaceKeyOf is the place an expression names: an identifier, or a
// property chain of identifiers. Nil for anything else.
func PlaceKeyOf(c *checker.Checker, e *ast.Node) *PlaceKey {
	path := ""
	cursor := e
	for {
		if ast.IsParenthesizedExpression(cursor) {
			cursor = cursor.AsParenthesizedExpression().Expression
			continue
		}
		if ast.IsPropertyAccessExpression(cursor) {
			path = "." + cursor.AsPropertyAccessExpression().Name().Text() + path
			cursor = cursor.AsPropertyAccessExpression().Expression
			continue
		}
		// a literal element read is a place too: `xs[0]` names the slot
		if ast.IsElementAccessExpression(cursor) &&
			ast.IsNumericLiteral(cursor.AsElementAccessExpression().ArgumentExpression) {
			n, err := strconv.ParseFloat(cursor.AsElementAccessExpression().ArgumentExpression.Text(), 64)
			if err != nil {
				break
			}
			path = "[" + strconv.FormatFloat(n, 'f', -1, 64) + "]" + path
			cursor = cursor.AsElementAccessExpression().Expression
			continue
		}
		break
	}
	// `this` names the enclosing class's instance — one fixed receiver
	// per walked body, so the class's own symbol is its identity
	if cursor.Kind == ast.KindThisKeyword {
		declaration := EnclosingThisClass(cursor)
		if declaration == nil {
			return nil
		}
		className := declaration.Name()
		if className == nil {
			return nil
		}
		base := c.GetSymbolAtLocation(className)
		if base == nil {
			return nil
		}
		return &PlaceKey{Base: base, Path: path, BaseName: "this"}
	}
	if !ast.IsIdentifier(cursor) {
		return nil
	}
	base := c.GetSymbolAtLocation(cursor)
	if base == nil {
		return nil
	}
	return &PlaceKey{Base: base, Path: path, BaseName: cursor.Text()}
}

// SamePlace reports whether two places are the same base symbol and path.
func SamePlace(a, b PlaceKey) bool {
	return a.Base == b.Base && a.Path == b.Path
}

// OffsetPlace is a place plus an integer literal offset: `i`, `i - 1`, `i + 2`.
type OffsetPlace struct {
	Place  PlaceKey
	Offset int
}

// OffsetPlaceOf reads a place plus an integer literal offset off `e`.
// The offset arithmetic is treated as exact by CONSUMERS that can vouch
// for it — an array index sits below 2^32 (ArrayLength is a uint32), so
// its integer offsets stay representable.
func OffsetPlaceOf(c *checker.Checker, e *ast.Node) *OffsetPlace {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	if ast.IsBinaryExpression(cursor) {
		bin := cursor.AsBinaryExpression()
		if (bin.OperatorToken.Kind == ast.KindPlusToken ||
			bin.OperatorToken.Kind == ast.KindMinusToken) &&
			ast.IsNumericLiteral(bin.Right) {
			k, err := strconv.ParseFloat(bin.Right.Text(), 64)
			if err != nil || k != float64(int(k)) {
				return nil
			}
			place := PlaceKeyOf(c, bin.Left)
			if place == nil {
				return nil
			}
			offset := int(k)
			if bin.OperatorToken.Kind == ast.KindMinusToken {
				offset = -offset
			}
			return &OffsetPlace{Place: *place, Offset: offset}
		}
	}
	place := PlaceKeyOf(c, cursor)
	if place == nil {
		return nil
	}
	return &OffsetPlace{Place: *place, Offset: 0}
}

/* ── scopes ──────────────────────────────────────────────────────── */

// EnclosingFunctionOf finds the nearest enclosing function-like node, or
// the source file when `site` is at the top level.
func EnclosingFunctionOf(site *ast.Node) *ast.Node {
	cursor := site
	for cursor != nil && !ast.IsFunctionLike(cursor) {
		cursor = cursor.Parent
	}
	if cursor != nil {
		return cursor
	}
	return ast.GetSourceFileOfNode(site).AsNode()
}

var nestedFunctionsMu sync.Mutex
var nestedFunctionsCache = map[*ast.Node][]*ast.Node{}

// NestedFunctions is every function nested inside `fn` (not `fn` itself) —
// the code that can run at times the walk cannot place.
//
// The TS source keys this memo with a WeakMap<ts.Node, …>; Go has no weak
// maps, so this substitutes a regular map guarded by a mutex. Functionally
// identical per program: entries live exactly as long as the program that
// produced their nodes is in use by this port.
func NestedFunctions(fn *ast.Node) []*ast.Node {
	nestedFunctionsMu.Lock()
	held, ok := nestedFunctionsCache[fn]
	nestedFunctionsMu.Unlock()
	if ok {
		return held
	}
	var found []*ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node != fn && ast.IsFunctionLike(node) {
			found = append(found, node)
			return false // its own nested functions are inside its subtree
		}
		node.ForEachChild(visit)
		return false
	}
	fn.ForEachChild(visit)
	nestedFunctionsMu.Lock()
	nestedFunctionsCache[fn] = found
	nestedFunctionsMu.Unlock()
	return found
}

/* ── the tracked place: the narrowing channel's identity ─────────── */
//
// TWO identities, ONE vocabulary (finding 20): PlaceKey above is
// SYMBOL-identified — the fact store outlives scopes, so its rows
// key on what a name resolves to. TrackedPlace below is
// NAME-identified — the narrowing channel applies its effects to a
// name-keyed environment, so its identity is the binding's name
// plus the keys walked from it. A third spelling must not appear;
// anything needing a path speaks one of these two.
//
// BOTH spell an element slot the same way — in brackets. PlaceKey's
// Path is one string, `.lo` and `[0]` concatenated (PlaceKeyOf);
// TrackedPlace's Path is the segments as a list, `lo` and `[0]`
// (TrackedPlaceOf). IndexSegmentOf reads a TrackedPlace segment back
// as a slot.

// TrackedPlace is a tracked place: a name, or a name followed by keys.
//
// A path segment is either a KEY — the plain text of a property name or
// a string-literal element key, `total` or `line` — or an INDEX — a
// non-negative integer element slot, spelled in brackets: `[0]`, `[1]`.
// The bracket spelling cannot collide with a key, because a key segment
// carries the property's own text and a JavaScript property named `[0]`
// is unwritable in either access form: `o.[0]` does not parse, and
// `o["[0]"]` produces the segment `[0]` only through the string-literal
// arm — which reads its text, so it spells the brackets literally and
// names the same slot the index segment does only if the object really
// carries the key `[0]`, an object no index segment is ever built for
// (the index arm requires a NUMERIC literal). The two arms therefore
// never write the same segment for two different slots.
//
// A consumer that steps a path INTO a value reads a key segment as an
// object key and an index segment as a list item — IndexSegmentOf tells
// them apart. A consumer that only carries paths around (the narrowing
// channels appending and copying segments) needs no distinction.
type TrackedPlace struct {
	Binding string
	Path    []string
}

// IndexSegmentOf reads a path segment spelled as an index — `[0]` — and
// answers the slot it names. Not-an-index for every key segment. The
// spelling has to round-trip exactly: `[00]` and `[0x1]` read as keys,
// not as slot 0 and slot 1, so no key ever borrows a slot's identity.
func IndexSegmentOf(segment string) (int, bool) {
	if len(segment) < 3 || segment[0] != '[' || segment[len(segment)-1] != ']' {
		return 0, false
	}
	digits := segment[1 : len(segment)-1]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	slot, err := strconv.Atoi(digits)
	if err != nil || IndexSegment(slot) != segment {
		return 0, false
	}
	return slot, true
}

// IndexSegment spells a slot as an index segment.
func IndexSegment(slot int) string {
	return "[" + strconv.Itoa(slot) + "]"
}

// sourceSpellingOf is a token's own text as the FILE spells it, from
// the post-trivia token start to the node's end. `.Text()` is not
// this: the scanner normalizes a numeric literal to its value text.
// Falls back to `.Text()` where the node reaches no source file (a
// synthesized node).
func sourceSpellingOf(node *ast.Node) string {
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return node.Text()
	}
	start := scanner.GetTokenPosOfNode(node, file, false)
	text := file.Text()
	if start < 0 || node.End() > len(text) || start > node.End() {
		return node.Text()
	}
	return text[start:node.End()]
}

// SameTrackedPlace reports whether two tracked places name the same
// binding and path.
func SameTrackedPlace(a, b TrackedPlace) bool {
	if a.Binding != b.Binding || len(a.Path) != len(b.Path) {
		return false
	}
	for i, key := range a.Path {
		if key != b.Path[i] {
			return false
		}
	}
	return true
}

// TrackedPlaceOf reads `x`, `o.total`, `o.line.qty` as a place — and
// `o["k"]` with a literal key, which names the same key a property
// access would, and `xs[0]` with a non-negative integer literal, which
// names one slot. Anything else (a call, an index that is not a literal)
// is not a place: the checker cannot say which slot it reads, so it
// names none.
//
// Without a checker an index that is a NAME reads as no place, even
// when the name is const-bound to a literal. TrackedPlaceOfWith takes
// the checker that resolves it.
func TrackedPlaceOf(e *ast.Node, isTracked func(name string) bool) *TrackedPlace {
	return TrackedPlaceOfWith(nil, e, isTracked)
}

// TrackedPlaceOfWith is TrackedPlaceOf with the checker that resolves a
// const-bound index to its literal, so `const i = 0; xs[i]` names the
// same slot `xs[0]` does. A nil checker reads literal indices only.
func TrackedPlaceOfWith(c *checker.Checker, e *ast.Node, isTracked func(name string) bool) *TrackedPlace {
	// a cast changes no runtime place — `(source as any).hooks` tests
	// source.hooks
	for ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) {
		switch {
		case ast.IsParenthesizedExpression(e):
			e = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			e = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			e = e.AsNonNullExpression().Expression
		}
	}
	if ast.IsIdentifier(e) {
		if isTracked(e.Text()) {
			return &TrackedPlace{Binding: e.Text(), Path: nil}
		}
		return nil
	}
	// `this` is a tracked place inside a walked method body — its
	// fields (`this.#options.retry`) narrow like any binding's keys
	if e.Kind == ast.KindThisKeyword {
		if isTracked("this") {
			return &TrackedPlace{Binding: "this", Path: nil}
		}
		return nil
	}
	if ast.IsPropertyAccessExpression(e) {
		propAccess := e.AsPropertyAccessExpression()
		inner := TrackedPlaceOfWith(c, propAccess.Expression, isTracked)
		if inner == nil {
			return nil
		}
		return &TrackedPlace{
			Binding: inner.Binding,
			Path:    append(append([]string{}, inner.Path...), propAccess.Name().Text()),
		}
	}
	if ast.IsElementAccessExpression(e) {
		elemAccess := e.AsElementAccessExpression()
		segment, spelled := elementSegmentOf(c, elemAccess.ArgumentExpression)
		if !spelled {
			return nil
		}
		inner := TrackedPlaceOfWith(c, elemAccess.Expression, isTracked)
		if inner == nil {
			return nil
		}
		return &TrackedPlace{
			Binding: inner.Binding,
			Path:    append(append([]string{}, inner.Path...), segment),
		}
	}
	return nil
}

// elementSegmentOf spells the path segment an element-access argument
// names: a string literal names a KEY by its own text, a non-negative
// integer literal names an INDEX in brackets. Anything else — a call, an
// expression, a negative or fractional literal — names no segment,
// because the checker cannot say which slot the read lands on.
//
// A NAME names a segment only through the const-chain resolver, and
// only with a checker in hand: `const i = 0; xs[i]` names slot 0,
// because a const bound to a literal holds that literal at every
// reachable point. A let, a var, a parameter, or any name the resolver
// cannot pin names no segment.
func elementSegmentOf(c *checker.Checker, argument *ast.Node) (string, bool) {
	if key := StringLiteralOf(argument); key != nil {
		return *key, true
	}
	if ast.IsNumericLiteral(argument) {
		slot, err := strconv.Atoi(argument.Text())
		if err != nil || slot < 0 {
			return "", false
		}
		// the SOURCE has to spell the slot exactly: `.Text()` answers the
		// scanner's normalized value text ("1" for `01` and `1.0` alike),
		// so the round-trip reads the file's own spelling — a spelling
		// this walk cannot reproduce names no segment
		if strconv.Itoa(slot) != sourceSpellingOf(argument) {
			return "", false
		}
		return IndexSegment(slot), true
	}
	// a const-bound name resolves to the number it holds. The resolver
	// answers a float64, so the value has to BE a non-negative integer
	// the index spelling can carry — a fractional, negative, or
	// out-of-range value names no slot. The segment spelled here is the
	// one the literal arm would spell for the same slot, so a resolved
	// index and a written one name the same place.
	if c != nil && ast.IsIdentifier(argument) {
		value, resolved := ConstChainNumber(c, argument)
		if !resolved {
			return "", false
		}
		slot := int(value)
		if value != float64(slot) || slot < 0 {
			return "", false
		}
		segment := IndexSegment(slot)
		if readBack, isIndex := IndexSegmentOf(segment); !isIndex || readBack != slot {
			return "", false
		}
		return segment, true
	}
	return "", false
}

// StringLiteralOf is a string literal side of an equality.
func StringLiteralOf(e *ast.Node) *string {
	if ast.IsStringLiteral(e) {
		text := e.Text()
		return &text
	}
	if ast.IsNoSubstitutionTemplateLiteral(e) {
		text := e.Text()
		return &text
	}
	return nil
}
