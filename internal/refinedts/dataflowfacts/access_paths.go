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

// TrackedPlace is a tracked place: a name, or a name followed by keys.
type TrackedPlace struct {
	Binding string
	Path    []string
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
// access would; anything else (a call, a computed index) is not one.
func TrackedPlaceOf(e *ast.Node, isTracked func(name string) bool) *TrackedPlace {
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
		inner := TrackedPlaceOf(propAccess.Expression, isTracked)
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
		key := StringLiteralOf(elemAccess.ArgumentExpression)
		if key == nil {
			return nil
		}
		inner := TrackedPlaceOf(elemAccess.Expression, isTracked)
		if inner == nil {
			return nil
		}
		return &TrackedPlace{
			Binding: inner.Binding,
			Path:    append(append([]string{}, inner.Path...), *key),
		}
	}
	return nil
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
