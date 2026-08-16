// split from ir_object_slots.go — the recognizer entries, one local and the batch

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// ObjectLocalOf is the recognizer: a declaration `const p = { k: e, … }`
// — nested literals included — whose every use in the body is a path on
// a declared leaf or a recognized whole-record form becomes the slot
// family "p.a.b"; anything else declines. Total-or-decline — the caller
// reads the flat leaves or keeps its existing route.
//
// `sameShapeName` answers whether another spelled local is a flattened
// record of THIS record's leaf shape, which is what `p = q` needs; the
// batch entry ObjectLocalsOf supplies it, and a lone call may pass nil
// (no record-to-record assignment is then admitted).
func ObjectLocalOf(body *ast.Node, declaration *ast.Node, sameShapeName func(other string) bool) (ObjectLocal, bool) {
	return ObjectLocalIn(nil, body, declaration, sameShapeName, nil)
}

// ObjectLocalIn is ObjectLocalOf with the check's own context, so the
// declarations whose leaves come from a DECLARATION rather than a
// literal are recognized too: a `new C()` whose constructor serves, a
// declared record type over an opaque initializer, and a `??` or ternary
// both of whose arms name one family.
//
// The four routes are tried in that order and are EXCLUSIVE — the first
// that answers a family is the local's, and one name has one slot
// family. The USE SCAN is then the same scan for every family: whatever
// produced the leaves, every occurrence of the name in the body must
// still be a path on a declared leaf or one of the recognized
// whole-record forms. Widening which declarations produce families never
// widens which uses are admitted.
//
// `familyOfName` answers the leaves another spelled local flattens to,
// which the join arms read; the batch entry ObjectLocalsIn supplies it,
// and nil admits no name-armed join.
func ObjectLocalIn(
	ctx *FlowContext,
	body *ast.Node,
	declaration *ast.Node,
	sameShapeName func(other string) bool,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) (ObjectLocal, bool) {
	if !ast.IsVariableDeclaration(declaration) ||
		!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ObjectLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := declarationLeavesOf(ctx, declaration, name, familyOfName)
	if !ok {
		return ObjectLocal{}, false
	}
	// a leaf's own initializer must not mention the record — `{ lo: 0, hi:
	// p.lo }` reads a slot that does not exist yet. A DECLARED leaf has no
	// initializer and no such row to check.
	for _, key := range keys {
		if key.Initializer != nil && mentionsName(key.Initializer, name) {
			return ObjectLocal{}, false
		}
	}
	if sameShapeName == nil {
		sameShapeName = func(string) bool { return false }
	}
	// the literal's method rows, by dotted path — the use scan admits
	// exactly these spellings in callee position; a declaration-sourced
	// family (a constructor's exits, a declared type) carries none
	methods := literalMethodPaths(objectLiteralOfDeclaration(declaration), nil)
	if !usesAreAllDeclaredKeySteps(checkerOf(ctx), body, declaration, name, keys, methods, sameShapeName) {
		return ObjectLocal{}, false
	}
	return ObjectLocal{Declaration: declaration, Name: name, Keys: keys, Methods: methods}, true
}

// declarationLeavesOf is the one place the four family sources are
// ordered: the object literal first (the reading that needs no context
// and has always served), then the constructor's exit rows, then the
// declared type, then the two-armed join. The first answer wins.
func declarationLeavesOf(
	ctx *FlowContext,
	declaration *ast.Node,
	name string,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if literal := objectLiteralOfDeclaration(declaration); literal != nil {
		return flatKeysOfLiteralWith(checkerOf(ctx), literal, name, nil)
	}
	if ctx == nil {
		return nil, false
	}
	if keys, ok := constructedLeavesOf(ctx, declaration); ok {
		return keys, true
	}
	if keys, ok := declaredTypeLeavesOf(ctx, declaration); ok {
		return keys, true
	}
	return joinedArmLeavesOf(ctx, declaration, familyOfName)
}

// ObjectLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration. A local that
// declines is simply absent — the caller keeps treating it as a whole
// binding, which is today's behaviour.
//
// Two passes: the first reads each local's leaf shape from its literal
// alone, which is what `p = q` compares; the second runs the full
// use scan with that shape table in hand. A record whose shape table
// entry vanished in the second pass (because its own uses declined)
// leaves any partner that assigned from it declining too, on the next
// pass — so the loop repeats until the admitted set stops shrinking.
func ObjectLocalsOf(body *ast.Node, locals []*ast.Node) map[*ast.Node]ObjectLocal {
	return ObjectLocalsIn(nil, body, locals)
}

// ObjectLocalsIn is ObjectLocalsOf with the check's own context, so the
// non-literal families are recognized. The context is threaded, not
// consulted for anything but resolution: a nil one answers exactly what
// ObjectLocalsOf always answered, which is what the seams holding no
// checker keep.
func ObjectLocalsIn(ctx *FlowContext, body *ast.Node, locals []*ast.Node) map[*ast.Node]ObjectLocal {
	// candidate shapes, from each declaration's own family source. The
	// LITERAL candidates are collected first and become the family table
	// the join arms read, so `const x = a ?? b` sees whatever `a` and `b`
	// already flatten to. A join whose arm is itself a join is not
	// resolved — one level only, so the collection cannot depend on its
	// own order.
	shapeOfName := map[string]string{}
	candidates := map[*ast.Node]string{}
	literalFamilies := map[string][]ObjectLocalKey{}
	for _, declaration := range locals {
		if !ast.IsVariableDeclaration(declaration) ||
			!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
			continue
		}
		literal := objectLiteralOfDeclaration(declaration)
		if literal == nil {
			continue
		}
		name := declaration.AsVariableDeclaration().Name().Text()
		if keys, ok := flatKeysOfLiteral(literal, name, nil); ok {
			literalFamilies[name] = keys
		}
	}
	// a name's family for the join arms: its literal leaves where it has
	// them, its DECLARED-TYPE leaves otherwise. The constructor and join
	// routes are deliberately not offered here — a constructor's leaves
	// depend on a summary the layout may still be building, and a join of
	// joins would make the answer depend on collection order.
	declaredFamilies := map[string][]ObjectLocalKey{}
	if ctx != nil {
		for _, declaration := range locals {
			if !ast.IsVariableDeclaration(declaration) ||
				!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
				continue
			}
			name := declaration.AsVariableDeclaration().Name().Text()
			if _, hasLiteral := literalFamilies[name]; hasLiteral {
				continue
			}
			if keys, ok := declaredTypeLeavesOf(ctx, declaration); ok {
				declaredFamilies[name] = keys
			}
		}
	}
	familyOfName := func(name string) ([]ObjectLocalKey, bool) {
		if keys, found := literalFamilies[name]; found {
			return keys, true
		}
		keys, found := declaredFamilies[name]
		return keys, found
	}
	for _, declaration := range locals {
		if !ast.IsVariableDeclaration(declaration) ||
			!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
			continue
		}
		name := declaration.AsVariableDeclaration().Name().Text()
		keys, ok := declarationLeavesOf(ctx, declaration, name, familyOfName)
		if !ok {
			continue
		}
		shape := recordShapeOf(keys)
		// a name declared twice with disagreeing shapes has no one shape
		if held, seen := shapeOfName[name]; seen && held != shape {
			shapeOfName[name] = ""
			continue
		}
		shapeOfName[name] = shape
		candidates[declaration] = shape
	}
	admitted := map[*ast.Node]ObjectLocal{}
	for {
		next := map[*ast.Node]ObjectLocal{}
		for declaration, shape := range candidates {
			sameShape := func(other string) bool {
				held, seen := shapeOfName[other]
				return seen && held != "" && held == shape
			}
			if local, ok := ObjectLocalIn(ctx, body, declaration, sameShape, familyOfName); ok {
				next[declaration] = local
			}
		}
		if len(next) == len(candidates) {
			admitted = next
			break
		}
		// a declined candidate withdraws its shape, which may decline a
		// partner that assigned from it — repeat until stable
		for declaration := range candidates {
			if _, kept := next[declaration]; !kept {
				name := declaration.AsVariableDeclaration().Name().Text()
				delete(shapeOfName, name)
				delete(candidates, declaration)
			}
		}
		admitted = next
		if len(candidates) == 0 {
			break
		}
	}
	return admitted
}
