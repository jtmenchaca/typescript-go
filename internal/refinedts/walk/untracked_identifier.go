// from evaluation/untracked_identifier.ts
//
// An identifier the walk does not track, and whether a callee has
// no body in reach, lives in the default library, or sits on the
// annotation surface. A module-level const may still hand on an
// opaque or scalar fact; everything else wears the last-reader type.

package walk

import (
	"regexp"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
	"github.com/microsoft/typescript-go/internal/scanner"
)

var calleeWordsWhitespace = regexp.MustCompile(`\s+`)

// CalleeWords is calleeWords in the TS source: the callee as the
// source spells it, one line. Node.Text() only reads leaf kinds
// (identifiers, literals) and PANICS on a compound expression — a
// property access (`addon.compute`) or a nested call
// (`require("bindings")(...)`), both real unmodeled-callee shapes
// UnmodeledCallResult reaches — so this reads the callee's own SOURCE
// SPAN instead (scanner.GetSourceTextOfNodeFromSourceFile, the same
// reader every other "spell this node's own source text" site in the
// tree already uses), which handles every node kind uniformly. A
// callee whose enclosing source file cannot be found (a synthesized
// node, never real parsed source) falls back to the empty string
// rather than panic.
func CalleeWords(callee *ast.Node) string {
	sourceFile := ast.GetSourceFileOfNode(callee)
	if sourceFile == nil {
		return ""
	}
	text := scanner.GetSourceTextOfNodeFromSourceFile(sourceFile, callee, false)
	return calleeWordsWhitespace.ReplaceAllString(text, " ")
}

// NarrowedSinceDeclaration is narrowedSinceDeclaration in the TS
// source: whether tsc's type at this occurrence differs from the
// type at the name's declaration — a guard in this file proved
// something the declaration did not say. An OPAQUE value reaching
// such a position is NOT "everything this file determines": the file
// determined more, and a walk that did not read it owes the
// narrowing.
func NarrowedSinceDeclaration(ctx *FlowContext, e *ast.Node) bool {
	symbol := ctx.P.Checker.GetSymbolAtLocation(e)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return false
	}
	declaration := symbol.ValueDeclaration
	occurrence := typereading.TypeAtLocation(ctx.P.Checker, e)
	declared := typereading.TypeAtLocation(ctx.P.Checker, declaration)
	// the same type OBJECT prints the same — the common un-narrowed
	// case skips both prints
	if occurrence == declared {
		return false
	}
	return ctx.P.Checker.TypeToString(occurrence) != ctx.P.Checker.TypeToString(declared)
}

// following is FOLLOWING in the TS source: module consts currently
// being followed — the guard that keeps a cyclic pair of module
// bindings from recursing.
//
// The TS source keys this guard with a WeakSet<ts.Declaration>; Go has
// no weak sets, so this substitutes a regular map guarded by a mutex
// (same pattern as dataflowfacts.NestedFunctions's memo). Functionally
// identical per program: entries live exactly as long as the program
// that produced the declarations is in use by this port.
var (
	followingMu sync.Mutex
	following   = map[*ast.Node]bool{}
)

// UntrackedIdentifier is untrackedIdentifier in the TS source: an
// identifier the walk does not track. Two facts are still safe to
// state: a name resolving entirely outside the program's reach (an
// unresolved module, ambient declarations only) holds a value from
// outside the file's determination; and a MODULE-LEVEL const whose
// initializer evaluates to such a value (a client handle, `const db =
// new PrismaClient()`) hands that standing on. Any other result of
// the follow is discarded — a module const's OBJECT may be mutated
// between calls, so only the opaque verdict (which nothing in the
// file can improve) survives.
func UntrackedIdentifier(ctx *FlowContext, e *ast.Node) abstractdomain.AbstractValue {
	symbol := ctx.P.Checker.GetSymbolAtLocation(e)
	if symbol == nil {
		return silence.Residue()
	}
	if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
		aliased := ctx.P.Checker.GetAliasedSymbol(symbol)
		if aliased == nil {
			return abstractdomain.Opaque // an alias into nothing — an unresolved module
		}
		symbol = aliased
	}
	declarations := symbol.Declarations
	if len(declarations) == 0 {
		return abstractdomain.Opaque
	}
	allDeclarationFiles := true
	for _, d := range declarations {
		if !ast.GetSourceFileOfNode(d).IsDeclarationFile {
			allDeclarationFiles = false
			break
		}
	}
	if allDeclarationFiles {
		return abstractdomain.Opaque
	}
	d := symbol.ValueDeclaration
	// a refinement-less PARAMETER of an EXPORTED function: its value is
	// sent by a caller outside this file, so the type is everything the
	// file determines. A private function's parameter stays plain — its
	// callers are in this file, and their arguments are determinable.
	if d != nil && ExportedFunctionParameter(ctx.P.Checker, d) {
		return abstractdomain.Opaque
	}
	if d != nil && ast.IsVariableDeclaration(d) {
		vd := d.AsVariableDeclaration()
		if vd.Initializer != nil &&
			d.Parent != nil && ast.IsVariableDeclarationList(d.Parent) &&
			(d.Parent.Flags&ast.NodeFlagsConst) != 0 &&
			d.Parent.Parent != nil && ast.IsVariableStatement(d.Parent.Parent) &&
			d.Parent.Parent.Parent != nil && ast.IsSourceFile(d.Parent.Parent.Parent) {
			followingMu.Lock()
			alreadyFollowing := following[d]
			if !alreadyFollowing {
				following[d] = true
			}
			followingMu.Unlock()
			if !alreadyFollowing {
				followed := func() abstractdomain.AbstractValue {
					defer func() {
						followingMu.Lock()
						delete(following, d)
						followingMu.Unlock()
					}()
					return evaluateExpression(ctx, NewEnv(), vd.Initializer)
				}()
				if followed.Kind == abstractdomain.KindUnknown && followed.Opaque {
					return abstractdomain.Opaque
				}
				// a module CONST holding a SCALAR fact: the binding can
				// never be rebound, and a scalar cannot be mutated
				// through an alias — so `MIN_DATE_MS` reads as its
				// number wherever a guard tests against it. A held
				// OBJECT stays unclaimed: its content may move between
				// calls.
				//
				// An ARRAY literal is the one KindValues shape the
				// alias-freedom argument above does NOT cover on its
				// own — samples.push(...), samples[i] = …, and
				// Object.assign(samples, …) all mutate the array in
				// place with no rebinding at all (RULING, JT
				// 2026-08-21). Serve the array only when a file-level
				// scan finds no such mutation site for this name;
				// finding one falls through to the last-reader path
				// below, naming nothing new itself — the consumer's
				// existing decline sentence owns the wording.
				if followed.Kind == abstractdomain.KindValues && followed.KindTag == abstractdomain.PrimitiveArray {
					if !ConstArrayMutated(d, symbol.Name) {
						return followed
					}
				} else {
					switch followed.Kind {
					case abstractdomain.KindValues, abstractdomain.KindSet,
						abstractdomain.KindNaN, abstractdomain.KindPossiblyNaN,
						abstractdomain.KindBigints:
						return followed
					}
				}
			}
		}
	}
	// the last reader: an in-program name the walk does not track (a
	// closed-over binding, an enclosing function's parameter outside
	// this walk's env) still wears its resolved type — every runtime
	// value a binding holds inhabits its static type, writes included
	return silence.AfterReaders(silence.Residue(), ctx.P.Checker, e, silence.RoleModel)
}

// narrowsPlace answers, for ONE leaf of a condition tree, whether the
// leaf tests `name` in a position the narrowers actually read a claim
// from. The positions are exactly the ones the narrowing recognizers
// take a place from — read off their own readers, not invented here:
//
//   - `typeof x === "s"`: typeof_ground.go's TypeofLeaf takes the place
//     from the operand of the typeof side.
//   - `x === lit`, `x !== lit`, `x == null`, `x < k`: the equality and
//     comparison readers (structural_narrowing.go's equality block,
//     comparison_leaf.go) take the place from EITHER side.
//   - `x % k === 0`: comparison_leaf.go's ModuloSide takes the place
//     from the left of the `%`.
//   - `x.includes(s)`, `x.startsWith(s)`: string_test_leaf.go and
//     comparison_leaf.go's index-of reading take the place from the
//     METHOD RECEIVER.
//   - `f(x)`: predicate_narrowings.go's PredicateCallNarrowings and
//     array_shape_narrowing.go's ArrayShapeLeaf take the place from the
//     FIRST argument.
//   - `"k" in x`: structural_narrowing.go's `in` reading takes the
//     place from the right side.
//   - `x instanceof C`: instanceof_narrowing.go takes the place from
//     the left side.
//   - a bare `x`: structural_narrowing.go's last row reads truthiness
//     of the place itself.
//
// Anything else the name appears in — a further call argument, an
// arithmetic operand, a template piece, an argument of a call that is
// only a SIDE of some other test — narrows nothing, and so must not
// veto. This mirrors condition_tree_lowering.go's CollectPlaces (the
// narrowing machinery's own "which places does this condition test"),
// restricted to a single name. It reads places WITH the checker, the
// way CollectPlaces does, so `if (xs[i] === "a")` under `const i = 0`
// vetoes here exactly where the narrowers read it — every reading this
// resolution adds ADDS a veto, and a veto only ever turns an opaque
// parameter into a plainly-read one. The one checker-dependent row in
// CollectPlaces this reading still takes by NAME is the default-library
// check on `Boolean(x)`: recognizing a shadowed local `Boolean` as a
// narrowing position also only widens the veto, the same safe
// direction.
func narrowsPlace(c *checker.Checker, test *ast.Node, name string) bool {
	isTracked := func(candidate string) bool { return candidate == name }
	// place reads a candidate expression as a place on `name`: `x`
	// itself, or a key path rooted at it (`x.a`, `x["k"].b`, and
	// `x[i]` where a const pins i) — the same reading every narrowing
	// leaf performs.
	place := func(candidate *ast.Node) bool {
		return candidate != nil && dataflowfacts.TrackedPlaceOfWith(c, candidate, isTracked) != nil
	}
	// narrows reads one leaf. `!` and the peelable wrappers recurse,
	// since a refuted leaf narrows the same place its held form does.
	var narrows func(e *ast.Node) bool
	narrows = func(e *ast.Node) bool {
		e = narrowing.Peeled(e)
		if ast.IsPrefixUnaryExpression(e) &&
			e.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
			return narrows(e.AsPrefixUnaryExpression().Operand)
		}
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			op := bin.OperatorToken.Kind
			// `x ?? d` in test position reads as the bare test of x
			if op == ast.KindQuestionQuestionToken {
				return narrows(bin.Left)
			}
			// `"k" in x` states on x and on x.k
			if op == ast.KindInKeyword {
				return place(bin.Right)
			}
			if op == ast.KindInstanceOfKeyword {
				return place(bin.Left)
			}
			if narrowing.IsComparisonOperator(op) ||
				op == ast.KindEqualsEqualsToken || op == ast.KindExclamationEqualsToken {
				for _, side := range []*ast.Node{bin.Left, bin.Right} {
					side = narrowing.Peeled(side)
					if place(side) {
						return true
					}
					// `typeof x === "number"` — the typeof side's operand
					if ast.IsTypeOfExpression(side) && place(side.AsTypeOfExpression().Expression) {
						return true
					}
					// `x % k === 0` — the remainder's left side
					if ast.IsBinaryExpression(side) &&
						side.AsBinaryExpression().OperatorToken.Kind == ast.KindPercentToken &&
						place(side.AsBinaryExpression().Left) {
						return true
					}
					// `x.indexOf(s) === -1` — the method receiver
					if ast.IsCallExpression(side) &&
						ast.IsPropertyAccessExpression(side.AsCallExpression().Expression) &&
						place(side.AsCallExpression().Expression.AsPropertyAccessExpression().Expression) {
						return true
					}
				}
			}
			return false
		}
		if ast.IsCallExpression(e) {
			call := e.AsCallExpression()
			// `Boolean(x)` IS the test of x — the argument re-enters whole
			if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Boolean" &&
				call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
				return narrows(call.Arguments.Nodes[0])
			}
			// a predicate call states on its FIRST argument only
			if call.Arguments != nil && len(call.Arguments.Nodes) > 0 &&
				place(call.Arguments.Nodes[0]) {
				return true
			}
			// a method receiver is the tested place: `x.includes("s")`
			if ast.IsPropertyAccessExpression(call.Expression) &&
				place(call.Expression.AsPropertyAccessExpression().Expression) {
				return true
			}
			return false
		}
		// a bare place as the whole leaf is a truthiness test
		return place(e)
	}
	return narrows(test)
}

// narrowedByCondition is testedByCondition in the TS source, narrowed
// to the mentions that MATTER. The TS source vetoed on any textual
// mention of the name anywhere inside any condition; `if (x.length >
// unrelated)` and `if (config.debug) { log(x) }` and `if (send(x))`
// all vetoed, though the first two state nothing about x's admitted
// shape that the walk reads, and the third states only what its own
// predicate body says. Here a condition vetoes only when the name
// stands in a position one of the narrowing recognizers reads a claim
// from (narrowsPlace above), folded over the SHARED condition tree so
// that `&&`, `||` and `!` decompose exactly as the narrowers decompose
// them.
//
// The condition SITES are unchanged from the TS source: an if, a
// ternary, a while, a do, a for test, and the left side of a
// short-circuit.
func narrowedByCondition(c *checker.Checker, body *ast.Node, name string) bool {
	narrowsName := func(condition *ast.Node) bool {
		for _, leaf := range conditiontree.AllLeaves(conditiontree.ConditionTreeOf(condition, false)) {
			if narrowsPlace(c, leaf.Test, name) {
				return true
			}
		}
		return false
	}
	found := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if found {
			return
		}
		var condition *ast.Node
		switch {
		case ast.IsIfStatement(node):
			condition = node.AsIfStatement().Expression
		case ast.IsConditionalExpression(node):
			condition = node.AsConditionalExpression().Condition
		case ast.IsWhileStatement(node):
			condition = node.AsWhileStatement().Expression
		case ast.IsDoStatement(node):
			condition = node.AsDoStatement().Expression
		case ast.IsForStatement(node):
			condition = node.AsForStatement().Condition
		case ast.IsBinaryExpression(node):
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken ||
				bin.OperatorToken.Kind == ast.KindBarBarToken ||
				bin.OperatorToken.Kind == ast.KindQuestionQuestionToken {
				condition = bin.Left
			}
		}
		if condition != nil && narrowsName(condition) {
			found = true
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(body)
	return found
}

// ExportedFunctionParameter is exportedFunctionParameter in the TS
// source: whether a parameter's value is determined entirely OUTSIDE
// this file. Two things must hold. Its function is EXPORTED, so no
// call site in this file sends the argument — a function declaration
// with the export modifier, an arrow or function expression bound by
// an exported const, or a method of an exported class. And no
// condition in that function's body NARROWS the name: a guard would
// prove something the declaration did not say, and until the walk
// reads it that is the walk's gap to close, never the outside's
// silence.
//
// "Narrows" is the narrowing recognizers' own reading, not a textual
// mention (narrowedByCondition below). A parameter handed to a call,
// added to something, or interpolated into a template inside a
// condition states nothing about its own admitted shape, so it leaves
// the opaque standing intact.
func ExportedFunctionParameter(c *checker.Checker, declaration *ast.Node) bool {
	var bound *ast.Node
	if ast.IsParameterDeclaration(declaration) {
		bound = declaration.AsParameterDeclaration().Name()
	} else if ast.IsBindingElement(declaration) {
		bound = declaration.AsBindingElement().Name()
	}
	var name string
	hasName := false
	if bound != nil && ast.IsIdentifier(bound) {
		name, hasName = bound.Text(), true
	}
	node := declaration
	for ast.IsBindingElement(node) || ast.IsArrayBindingPattern(node) || ast.IsObjectBindingPattern(node) {
		node = node.Parent
	}
	if !ast.IsParameterDeclaration(node) {
		return false
	}
	fn := node.Parent
	exported := func(node *ast.Node) bool {
		return ast.HasSyntacticModifier(node, ast.ModifierFlagsExport)
	}
	var sent bool
	switch {
	case ast.IsFunctionDeclaration(fn):
		sent = exported(fn)
	case ast.IsArrowFunction(fn) || ast.IsFunctionExpression(fn):
		sent = fn.Parent != nil && ast.IsVariableDeclaration(fn.Parent) &&
			fn.Parent.Parent != nil && ast.IsVariableDeclarationList(fn.Parent.Parent) &&
			fn.Parent.Parent.Parent != nil && ast.IsVariableStatement(fn.Parent.Parent.Parent) &&
			exported(fn.Parent.Parent.Parent)
	case ast.IsMethodDeclaration(fn) || ast.IsConstructorDeclaration(fn):
		sent = fn.Parent != nil && ast.IsClassDeclaration(fn.Parent) && exported(fn.Parent)
	}
	if !sent {
		return false
	}
	body := fn.Body()
	if body == nil || !hasName {
		return sent
	}
	return !narrowedByCondition(c, body, name)
}

// BodilessCallee is bodilessCallee in the TS source: whether a named
// callee has NO BODY anywhere in reach: every declaration is bodiless
// (an ambient signature), or the name resolves to a module the
// program cannot see at all. Either way the value it produces enters
// from outside the file's determination. A callee with no symbol (a
// computed expression) answers false — the function may live in this
// very file.
func BodilessCallee(ctx *FlowContext, callee *ast.Node) bool {
	symbol := ctx.P.Checker.GetSymbolAtLocation(callee)
	if symbol == nil {
		return false
	}
	if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
		aliased := ctx.P.Checker.GetAliasedSymbol(symbol)
		if aliased == nil {
			return true // an alias into nothing — an unresolved module
		}
		symbol = aliased
	}
	if symbol == nil {
		return false
	}
	for _, d := range symbol.Declarations {
		if d.Body() != nil {
			return false
		}
		// a class carries no `body` node — its source file decides:
		// ambient stays bodiless, a source class is readable work
		if ast.IsClassDeclaration(d) || ast.IsClassExpression(d) {
			if !ast.GetSourceFileOfNode(d).IsDeclarationFile {
				return false
			}
			continue
		}
		// a const holding a function IS a body in reach; only an
		// ambient `declare const` has none
		if ast.IsVariableDeclaration(d) {
			if d.AsVariableDeclaration().Initializer != nil {
				return false
			}
			continue
		}
		if ast.IsBindingElement(d) {
			return false
		}
	}
	return true
}

// CalleeInDefaultLib is calleeInDefaultLib in the TS source: whether
// the callee's own declaration lives in the default library — what
// separates an unmodeled BUILT-IN (the checker's work) from a user's
// ambient declaration (whose type is everything the file determines).
func CalleeInDefaultLib(ctx *FlowContext, callee *ast.Node) bool {
	return ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(callee))
}

// CalleeInSurface is calleeInSurface in the TS source: whether the
// callee is declared in the annotation surface — those calls are the
// annotation reader's to answer, never this walk's.
func CalleeInSurface(ctx *FlowContext, callee *ast.Node) bool {
	symbol := ctx.P.Checker.GetSymbolAtLocation(callee)
	if symbol == nil {
		return false
	}
	for _, d := range symbol.Declarations {
		if ctx.P.SurfacePaths[ast.GetSourceFileOfNode(d).FileName()] {
			return true
		}
	}
	return false
}
