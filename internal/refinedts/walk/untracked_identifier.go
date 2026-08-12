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
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

var calleeWordsWhitespace = regexp.MustCompile(`\s+`)

// CalleeWords is calleeWords in the TS source: the callee as the
// source spells it, one line.
func CalleeWords(callee *ast.Node) string {
	return calleeWordsWhitespace.ReplaceAllString(callee.Text(), " ")
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
	occurrence := ctx.P.Checker.GetTypeAtLocation(e)
	declared := ctx.P.Checker.GetTypeAtLocation(declaration)
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
	if d != nil && ExportedFunctionParameter(d) {
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
					return evaluateExpression(ctx, Env{}, vd.Initializer)
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
				switch followed.Kind {
				case abstractdomain.KindValues, abstractdomain.KindSet,
					abstractdomain.KindNaN, abstractdomain.KindPossiblyNaN,
					abstractdomain.KindBigints:
					return followed
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

// testedByCondition is testedByCondition in the TS source: whether any
// CONDITION inside a body tests a name — an if, a ternary, a loop
// test, or the left side of a short-circuit. A guard over the name
// means the file's own text determines more than the declaration
// said, whether or not the walk reads that form yet.
func testedByCondition(body *ast.Node, name string) bool {
	var mentions func(node *ast.Node) bool
	mentions = func(node *ast.Node) bool {
		if ast.IsIdentifier(node) {
			return node.Text() == name
		}
		hit := false
		node.ForEachChild(func(child *ast.Node) bool {
			if !hit {
				hit = mentions(child)
			}
			return false
		})
		return hit
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
		if condition != nil && mentions(condition) {
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
// condition in that function's body tests the name: a guard would
// prove something the declaration did not say, and until the walk
// reads it that is the walk's gap to close, never the outside's
// silence.
func ExportedFunctionParameter(declaration *ast.Node) bool {
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
	return !testedByCondition(body, name)
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
