// Conditions bound to names: resolve a tested expression to the
// condition a const binding holds, and the value-copy bindings a
// function's `const x = <place>` pairs make. Split from
// condition_analysis.ts per the v2 tree.
package narrowing

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// ConstCopy is one `const x = <tracked place>` pair a function
// declares.
type ConstCopy struct {
	Name  string
	Place dataflowfacts.TrackedPlace
}

// constCopiesMu guards constCopiesCache: every `const x = <tracked
// place>` pair a function declares — collected ONCE per function (the
// per-application visits of copyBindingsOf and copySourcePlaceOf each
// rescanned the whole function). Pure per node: the place reading is
// syntactic.
//
// The TS source keys this memo with a WeakMap<ts.Node, …>; Go has no
// weak maps, so this substitutes a regular map guarded by a mutex.
// Functionally identical per program: entries live exactly as long as
// the program that produced the function node is in use by this port.
var (
	constCopiesMu    sync.Mutex
	constCopiesCache = map[*ast.Node][]ConstCopy{}
)

// ConstCopiesOf is constCopiesOf in the TS source, reading places WITH
// the checker where the TS source read them without one: the
// checker-carrying place reading resolves a const-bound index in a
// copied initializer (`const i = 0; const c = xs[i]`) to the slot the
// literal spells, so that copy rides the place's narrowings too. The TS
// source's trackedPlaceOf takes no checker and reads no such copy —
// this widening is deliberate; every copy read here still passes the
// same FunctionWrites stability gates its callers apply.
//
// The memo keys on the function node alone and needs no checker in the
// key: dataflowfacts.ConstChainNumber follows const-to-const symbol
// links to a literal token, and for a fixed program those links and
// that token are fixed, so one function node has one answer.
func ConstCopiesOf(c *checker.Checker, fn *ast.Node) []ConstCopy {
	constCopiesMu.Lock()
	held, ok := constCopiesCache[fn]
	constCopiesMu.Unlock()
	if ok {
		return held
	}
	var copies []ConstCopy
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) && ast.IsIdentifier(node.Name()) {
			varDecl := node.AsVariableDeclaration()
			if varDecl.Initializer != nil && ast.IsVariableDeclarationList(node.Parent) &&
				(node.Parent.Flags&ast.NodeFlagsConst) != 0 {
				place := dataflowfacts.TrackedPlaceOfWith(c, varDecl.Initializer, func(string) bool { return true })
				if place != nil {
					copies = append(copies, ConstCopy{Name: node.Name().Text(), Place: *place})
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	fn.ForEachChild(visit)
	constCopiesMu.Lock()
	constCopiesCache[fn] = copies
	constCopiesMu.Unlock()
	return copies
}

// BoundConditionInitializer is boundConditionInitializer in the TS
// source: the initializer behind `const ok = cond`, when testing `ok`
// reads as testing `cond` — the binding is const in the same function,
// and nothing the condition mentions can have moved (no mentioned name
// is assigned in the function, no call can reach a mentioned reference
// — a value-sorted argument travels by copy — and a default-library
// method's receiver is never written by calling it). Answers nil when
// the reading finds no such initializer.
func BoundConditionInitializer(c *checker.Checker, e *ast.Node) *ast.Node {
	if !ast.IsIdentifier(e) {
		return nil
	}
	symbol := c.GetSymbolAtLocation(e)
	if symbol == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil || !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	varDecl := declaration.AsVariableDeclaration()
	if varDecl.Initializer == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return nil
	}
	fn := dataflowfacts.EnclosingFunctionOf(e)
	if fn == nil || fn != dataflowfacts.EnclosingFunctionOf(declaration) {
		return nil
	}
	written := FunctionWrites(c, fn)
	stable := true
	var mentions func(node *ast.Node) bool
	mentions = func(node *ast.Node) bool {
		if !stable {
			return true
		}
		if ast.IsIdentifier(node) {
			if _, isWritten := written[node.Text()]; isWritten {
				stable = false
			}
		}
		node.ForEachChild(mentions)
		return false
	}
	mentions(varDecl.Initializer)
	if !stable {
		return nil
	}
	return varDecl.Initializer
}

// ResolvedCondition is the { condition, flipped } pair
// resolveBoundCondition answers.
type ResolvedCondition struct {
	Condition *ast.Node
	Flipped   bool
}

// ResolveBoundCondition is resolveBoundCondition in the TS source:
// resolve a tested expression to the CONDITION a const binding holds —
// a bare name, or the name compared against a boolean literal
// (`ok === true`, `ok !== false` and mirrors). Flipped says the
// resolved condition's parity is inverted. Answers
// (ResolvedCondition{}, false) when the expression resolves to no bound
// condition; narrowingsOf's caller then reads the condition as itself.
func ResolveBoundCondition(c *checker.Checker, e *ast.Node) (ResolvedCondition, bool) {
	cond := Peeled(e)
	if ast.IsIdentifier(cond) {
		initializer := BoundConditionInitializer(c, cond)
		if initializer == nil {
			return ResolvedCondition{}, false
		}
		return ResolvedCondition{Condition: initializer, Flipped: false}, true
	}
	if !ast.IsBinaryExpression(cond) {
		return ResolvedCondition{}, false
	}
	bin := cond.AsBinaryExpression()
	eq := bin.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken
	ne := bin.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
	if !eq && !ne {
		return ResolvedCondition{}, false
	}
	// boolOf reads a side that is spelled `true` or `false`; the second
	// answer says the side is a boolean literal at all.
	boolOf := func(side *ast.Node) (bool, bool) {
		if side.Kind == ast.KindTrueKeyword {
			return true, true
		}
		if side.Kind == ast.KindFalseKeyword {
			return false, true
		}
		return false, false
	}
	leftBool, leftIsBool := boolOf(bin.Left)
	rightBool, rightIsBool := boolOf(bin.Right)
	// exactly ONE side has to be the literal: neither, or both, reads as
	// no bound condition
	if leftIsBool == rightIsBool {
		return ResolvedCondition{}, false
	}
	// the NAMED side is whichever side is not the literal; the literal
	// value is the one side that spelled a boolean
	named := bin.Left
	literal := rightBool
	if leftIsBool {
		named = bin.Right
		literal = leftBool
	}
	named = Peeled(named)
	if !ast.IsIdentifier(named) {
		return ResolvedCondition{}, false
	}
	initializer := BoundConditionInitializer(c, named)
	if initializer == nil {
		return ResolvedCondition{}, false
	}
	return ResolvedCondition{Condition: initializer, Flipped: ne != (literal == false)}, true
}

// CopyBindingsOf is copyBindingsOf in the TS source: names
// `const x = <source place>` binds in the site's function with
// neither the copy nor the source's root ever written there — x
// still equals the place, so it rides the place's narrowings. Answers
// nil when nothing is proven — a bare source place, no enclosing
// function, or the source's root written in it — and the caller in
// walk/assume_condition.go then applies no additional copy narrowings.
func CopyBindingsOf(c *checker.Checker, site *ast.Node, source dataflowfacts.TrackedPlace) []string {
	if len(source.Path) == 0 {
		return nil
	}
	fn := dataflowfacts.EnclosingFunctionOf(site)
	if fn == nil {
		return nil
	}
	written := FunctionWrites(c, fn)
	if _, isWritten := written[source.Binding]; isWritten {
		return nil
	}
	var names []string
	for _, copy := range ConstCopiesOf(c, fn) {
		if _, isWritten := written[copy.Name]; isWritten {
			continue
		}
		if dataflowfacts.SameTrackedPlace(copy.Place, source) {
			names = append(names, copy.Name)
		}
	}
	return names
}

// CopySourcePlaceOf is copySourcePlaceOf in the TS source: the PLACE
// `const x = <source place>` copied from, when neither x nor the
// place's root is ever written in the site's function — x still
// equals the place, so a narrowing on x holds of the place too. The
// REVERSE of CopyBindingsOf: guard the copy, and the source place
// narrows with it. Answers (TrackedPlace{}, false) when no source place
// is proven.
func CopySourcePlaceOf(c *checker.Checker, site *ast.Node, binding string) (dataflowfacts.TrackedPlace, bool) {
	fn := dataflowfacts.EnclosingFunctionOf(site)
	if fn == nil {
		return dataflowfacts.TrackedPlace{}, false
	}
	written := FunctionWrites(c, fn)
	if _, isWritten := written[binding]; isWritten {
		return dataflowfacts.TrackedPlace{}, false
	}
	for _, copy := range ConstCopiesOf(c, fn) {
		if copy.Name != binding {
			continue
		}
		if len(copy.Place.Path) > 0 {
			if _, rootWritten := written[copy.Place.Binding]; !rootWritten {
				return dataflowfacts.TrackedPlace{Binding: copy.Place.Binding, Path: copy.Place.Path}, true
			}
		}
	}
	return dataflowfacts.TrackedPlace{}, false
}
