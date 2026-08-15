// split from ir_await.go — the promise-held local: its "p.inner" slot
// registry, the declaration recognition, and the total-or-decline use scan.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// promiseInnerSuffix is the one slot spelling a promise-held local wears
// below its name.
const promiseInnerSuffix = ".inner"

// promiseLocalSlots holds the "p.inner" slot each recognized
// promise-held local was allocated, per lowering context. Keyed by the
// context POINTER: one lowering walk, one set of flattened promise
// locals, and the map dies with the walk.
//
// It lives here rather than on LoweringContext because the slot is
// allocated on demand, at the declaration statement, rather than laid
// out with the body's other locals — a promise local is recognized by
// how its name is USED downstream, which the slot layout does not scan.
var (
	promiseLocalSlotsMu sync.Mutex
	promiseLocalSlots   = map[*LoweringContext]map[string]int{}
)

// promiseInnerSlotOf answers the "p.inner" slot held for a name in this
// lowering, if the declaration statement recognized one.
func promiseInnerSlotOf(context *LoweringContext, name string) (int, bool) {
	promiseLocalSlotsMu.Lock()
	defer promiseLocalSlotsMu.Unlock()
	held, has := promiseLocalSlots[context]
	if !has {
		return 0, false
	}
	slot, found := held[name]
	return slot, found
}

// holdPromiseInnerSlot remembers the "p.inner" slot for a name.
func holdPromiseInnerSlot(context *LoweringContext, name string, slot int) {
	promiseLocalSlotsMu.Lock()
	defer promiseLocalSlotsMu.Unlock()
	held, has := promiseLocalSlots[context]
	if !has {
		held = map[string]int{}
		promiseLocalSlots[context] = held
	}
	held[name] = slot
}

// promiseLocalDeclarationOf is `const p = f(…)` where f resolves with a
// blob, OR `const p = Promise.resolve(e)`, and EVERY later use of p is
// `await p`. Such a local holds a promise that is only ever settled, so
// it flattens to ONE slot spelled "p.inner": the initializer's write
// lands it here, and each `await p` downstream reads its var
// (awaitIdentityEffect above).
//
// Total-or-decline over p's uses, the same law every other flattening
// here obeys: a p that is passed to something, returned, `.then`-ed, or
// read in any position other than the operand of an await declines the
// whole recognition, and the declaration then takes its former route.
func promiseLocalDeclarationOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context.Allocate == nil {
		return nil, false
	}
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(declaration.Name()) || declaration.Initializer == nil {
		return nil, false
	}
	initializer := Unwrapped(declaration.Initializer)
	name := declaration.Name().Text()
	// a name already flattened is a second declaration of the same
	// spelling — one name, one slot, so the second declines rather than
	// silently reusing the first's slot
	if _, already := promiseInnerSlotOf(context, name); already {
		return nil, false
	}
	body := enclosingBodyOf(statement)
	if body == nil {
		return nil, false
	}
	if !usesAreAllAwaits(body, declarations[0], name) {
		return nil, false
	}
	// `const p = Promise.resolve(e)` — e's own reading, gated exactly as
	// the await-position recognizer gates it (RhsEffect admits only
	// shapes CannotBeThenable would clear)
	if inner, isResolve := promiseResolveArgumentOf(initializer); isResolve {
		slot, allocated := context.Allocate(name+promiseInnerSuffix, BindingKindUnknown, TypeofTagNone)
		if !allocated {
			return nil, false
		}
		effect, ok := RhsEffect(context, BindingKindUnknown, inner)
		if !ok {
			return nil, false
		}
		holdPromiseInnerSlot(context, name, slot)
		return []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect}}, true
	}
	if !ast.IsCallExpression(initializer) {
		return nil, false
	}
	// only a callee the registry answers for: a promise from anything
	// else has no ret out-state to land in the flattened slot
	callee := summaryCalleeOf(context, initializer)
	if callee == nil || context.Flow == nil {
		return nil, false
	}
	if _, hasBlob := SummaryBlobFor(context.Flow, callee); !hasBlob {
		return nil, false
	}
	// the inner slot's sort is UNKNOWN: nothing in the declaration
	// spells what the callee settles to, and an unknown-sorted slot
	// admits only the definedness test — which loses coverage, never
	// soundness
	slot, allocated := context.Allocate(name+promiseInnerSuffix, BindingKindUnknown, TypeofTagNone)
	if !allocated {
		return nil, false
	}
	lowered, ok := SummaryCallOrHavoc(context, initializer, slot)
	if !ok {
		return nil, false
	}
	holdPromiseInnerSlot(context, name, slot)
	return lowered, true
}

// enclosingBodyOf is the block a statement sits in, walked up to the
// nearest function body or source file — the scan region a use analysis
// covers.
func enclosingBodyOf(statement *ast.Node) *ast.Node {
	for parent := statement.Parent; parent != nil; parent = parent.Parent {
		if ast.IsSourceFile(parent) {
			return parent
		}
		switch parent.Kind {
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindArrowFunction, ast.KindMethodDeclaration,
			ast.KindGetAccessor, ast.KindSetAccessor, ast.KindConstructor:
			return parent.Body()
		}
	}
	return nil
}

// usesAreAllAwaits scans a body for every occurrence of the name and
// answers whether each one is the operand of an `await` or the
// receiver of a `.then(…)` call — the two positions the flattened
// "p.inner" slot can serve (thenStatements lowers the then). The
// declaration's own name position is not a use.
func usesAreAllAwaits(body *ast.Node, declaration *ast.Node, name string) bool {
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `await p` — consumed whole; the operand's occurrence is admitted
		if ast.IsAwaitExpression(node) {
			operand := Unwrapped(node.AsAwaitExpression().Expression)
			if ast.IsIdentifier(operand) && operand.Text() == name {
				return false
			}
		}
		// `p.then(cb)` — the receiver reads through the settled slot the
		// then-lowering serves; the CALLBACK still scans on its own
		if ast.IsCallExpression(node) {
			access := Unwrapped(node.AsCallExpression().Expression)
			if ast.IsPropertyAccessExpression(access) {
				pa := access.AsPropertyAccessExpression()
				if pa.QuestionDotToken == nil && ast.IsIdentifier(pa.Expression) &&
					pa.Expression.Text() == name && ast.IsIdentifier(pa.Name()) &&
					pa.Name().Text() == "then" {
					if node.AsCallExpression().Arguments != nil {
						for _, argument := range node.AsCallExpression().Arguments.Nodes {
							argument.ForEachChild(visit)
						}
					}
					return false
				}
			}
		}
		// every other occurrence of the bare name — an argument, a return,
		// an alias, `.catch` — is the PROMISE itself in a position one
		// settled-value slot cannot spell
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}
