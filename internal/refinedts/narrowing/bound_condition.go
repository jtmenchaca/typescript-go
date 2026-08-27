// Conditions bound to names: resolve a tested expression to the
// condition a const binding holds, and the value-copy bindings a
// function's `const x = <place>` pairs make. Split from
// condition_analysis.ts per the v2 tree.
package narrowing

import (
	"math"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ConstCopy is one `const x = <tracked place>` pair a function
// declares — and, with a non-zero Shift, one `const x = <place> ± k`
// pair, which is the same fact displaced along the line.
type ConstCopy struct {
	Name  string
	Place dataflowfacts.TrackedPlace
	// Shift is what the binding adds to the place: x === place + Shift
	// on every run. Zero for a plain copy, so every existing reader that
	// ignores this field keeps reading exactly the pairs it always did.
	Shift float64
	// ShiftExpression is the constant side of a shift this file could not
	// read as a number itself — `const REFERENCE = new Date(…).getTime()`
	// names one number on every run, but not one this syntactic reader
	// can fold. The WALK can: it evaluates the expression and supplies
	// the value, which is why the field carries the node rather than a
	// number. Nil when Shift above is already the whole answer.
	//
	// ShiftNegated says the expression is SUBTRACTED from the place, so
	// the walk's value is negated before it becomes the shift.
	ShiftExpression *ast.Node
	ShiftNegated    bool
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
				} else if shifted, ok := shiftedPlaceOf(c, varDecl.Initializer); ok {
					shifted.Name = node.Name().Text()
					copies = append(copies, shifted)
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

// shiftedPlaceOf reads `<place> - k` and `<place> + k` — an initializer
// that is one place displaced by a compile-time constant — and answers
// the place with the SHIFT the binding adds to it: `d.getTime() - REF`
// answers (d.getTime(), -REF), because the binding equals the place
// plus that amount on every run.
//
// The shift has to be EXACT to be worth anything, and it is: a guard's
// window is carried across the shift by adding the same constant to
// each endpoint, so the endpoints have to land where the runtime
// subtraction lands. The caller (ShiftedForms) refuses any shift that
// is not finite, and the endpoint arithmetic it does is the same double
// arithmetic the program itself runs.
//
// Only `k + place` and `place ± k` are read. `k - place` is a
// REFLECTION, not a shift — it flips the order of the endpoints — and
// this reader states nothing about it rather than getting it backwards.
func shiftedPlaceOf(c *checker.Checker, e *ast.Node) (ConstCopy, bool) {
	bare := Peeled(e)
	if !ast.IsBinaryExpression(bare) {
		return ConstCopy{}, false
	}
	bin := bare.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	if op != ast.KindMinusToken && op != ast.KindPlusToken {
		return ConstCopy{}, false
	}
	anyName := func(string) bool { return true }
	// the constant side: a number this reader folds itself, or — for a
	// const whose initializer is computed (`new Date(…).getTime()`) — the
	// EXPRESSION, handed to the walk, which evaluates what syntax cannot
	constantSide := func(side *ast.Node, negated bool) (ConstCopy, bool) {
		if value, ok := LiteralOf(side); ok && !math.IsInf(value, 0) && !math.IsNaN(value) {
			if negated {
				value = -value
			}
			return ConstCopy{Shift: value}, true
		}
		if ast.IsIdentifier(side) {
			if value, ok := dataflowfacts.ConstChainNumber(c, side); ok && !math.IsInf(value, 0) && !math.IsNaN(value) {
				if negated {
					value = -value
				}
				return ConstCopy{Shift: value}, true
			}
			// a const the syntactic resolver cannot fold: the walk gets the
			// node. Only a CONST binding qualifies — a let or a parameter
			// can move between the binding and the guard, and then the two
			// reads are not the same number at all.
			if _, isConst := dataflowfacts.ConstInitializerOf(c, side); isConst {
				return ConstCopy{ShiftExpression: side, ShiftNegated: negated}, true
			}
		}
		return ConstCopy{}, false
	}
	// `place - k` and `place + k`
	if place := dataflowfacts.TrackedPlaceOfWith(c, bin.Left, anyName); place != nil {
		copy, ok := constantSide(bin.Right, op == ast.KindMinusToken)
		if !ok {
			return ConstCopy{}, false
		}
		copy.Place = *place
		return copy, true
	}
	// `k + place` — addition only; `k - place` reflects and is refused
	if op == ast.KindPlusToken {
		if place := dataflowfacts.TrackedPlaceOfWith(c, bin.Right, anyName); place != nil {
			copy, ok := constantSide(bin.Left, false)
			if !ok {
				return ConstCopy{}, false
			}
			copy.Place = *place
			return copy, true
		}
	}
	return ConstCopy{}, false
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
	tracing.CountBy("host.symbolAtLocation", 1)
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
	var names []string
	for _, copy := range CopyBindingsWithShiftOf(c, site, source) {
		// a PLAIN copy only: a shift, whether already a number or still an
		// expression for the walk to evaluate, is a different fact
		if copy.Shift == 0 && copy.ShiftExpression == nil {
			names = append(names, copy.Name)
		}
	}
	return names
}

// CopyBindingsWithShiftOf is CopyBindingsOf keeping each copy's SHIFT:
// `const off = d.getTime() - REF` rides the place's narrowings too, with
// every endpoint moved by -REF. A plain copy comes back with shift 0,
// which is what CopyBindingsOf above filters for.
func CopyBindingsWithShiftOf(c *checker.Checker, site *ast.Node, source dataflowfacts.TrackedPlace) []ConstCopy {
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
	var found []ConstCopy
	for _, copy := range ConstCopiesOf(c, fn) {
		if _, isWritten := written[copy.Name]; isWritten {
			continue
		}
		if dataflowfacts.SameTrackedPlace(copy.Place, source) {
			found = append(found, copy)
		}
	}
	return found
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
