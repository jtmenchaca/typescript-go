// A STATIC get/set accessor pair over a static backing field —
// `static get ceiling() { return C.#held; }` / `static set
// ceiling(value) { C.#held = value; }` — read and written through the
// class's own name (e-class-and-function.ts's staticMethodAndAccessors
// row).
//
// The flow story rides the place-value memory: a write through the
// setter lands the evaluated value under the dotted key
// "<ClassName>.<#backing>", and a read through the getter answers that
// entry first, the backing field's own invariant second. The key's
// root is the class name, so the existing sweeps (HavocEnv on a name a
// call may write, ForgetThrough) invalidate the entry on the same
// terms every dotted entry dies.
//
// ONLY the trivial backing shapes are recognized — a getter whose body
// is exactly `return C.#x;` (or `this.#x` — a static member's `this`
// is the constructor object) and a setter whose body is exactly
// `C.#x = value;` storing its own parameter. A body that transforms,
// guards, or does anything else declines to today's answer: the
// invariant collection already scans accessor bodies, so the backing
// field's invariant never overclaims either way.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// staticClassDeclarationOf resolves an identifier receiver to the
// class DECLARATION it names — the same resolution
// ReadStaticFieldAccess makes — or nil.
func staticClassDeclarationOf(ctx *FlowContext, receiver *ast.Node) *ast.Node {
	if receiver == nil || !ast.IsIdentifier(receiver) {
		return nil
	}
	c := checkerOf(ctx)
	if c == nil {
		return nil
	}
	symbol := symbolAt(c, receiver)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsClassDeclaration(declaration) || ast.GetSourceFileOfNode(declaration).IsDeclarationFile {
		return nil
	}
	return declaration
}

// staticAccessorBackingName is the backing FIELD a static accessor's
// body trivially fronts: the getter's `return <recv>.<#x>;`, the
// setter's `<recv>.<#x> = <its own parameter>;`, receiver naming the
// class or `this`. ("", false) for every other body.
func staticAccessorBackingName(classDeclaration *ast.Node, name string, wantSetter bool) (string, bool) {
	className := ""
	if n := classDeclaration.Name(); n != nil && ast.IsIdentifier(n) {
		className = n.Text()
	}
	for _, member := range classDeclaration.ClassLikeData().Members.Nodes {
		if wantSetter && !ast.IsSetAccessorDeclaration(member) {
			continue
		}
		if !wantSetter && !ast.IsGetAccessorDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic == 0 {
			continue
		}
		memberName := member.Name()
		if memberName == nil || !(ast.IsIdentifier(memberName) || ast.IsPrivateIdentifier(memberName)) ||
			memberName.Text() != name {
			continue
		}
		body := member.Body()
		if body == nil || !ast.IsBlock(body) {
			return "", false
		}
		statements := body.AsBlock().Statements.Nodes
		if len(statements) != 1 {
			return "", false
		}
		classField := func(e *ast.Node) (string, bool) {
			e = Unwrapped(e)
			if e == nil || !ast.IsPropertyAccessExpression(e) {
				return "", false
			}
			access := e.AsPropertyAccessExpression()
			receiver := Unwrapped(access.Expression)
			named := receiver != nil && ast.IsIdentifier(receiver) && receiver.Text() == className
			if !named && (receiver == nil || receiver.Kind != ast.KindThisKeyword) {
				return "", false
			}
			field := access.Name()
			if field == nil || !(ast.IsIdentifier(field) || ast.IsPrivateIdentifier(field)) {
				return "", false
			}
			return field.Text(), true
		}
		if !wantSetter {
			if !ast.IsReturnStatement(statements[0]) {
				return "", false
			}
			return classField(statements[0].AsReturnStatement().Expression)
		}
		parameters := member.Parameters()
		if len(parameters) != 1 {
			return "", false
		}
		parameterName := parameters[0].AsParameterDeclaration().Name()
		if parameterName == nil || !ast.IsIdentifier(parameterName) {
			return "", false
		}
		if !ast.IsExpressionStatement(statements[0]) {
			return "", false
		}
		assignment := Unwrapped(statements[0].AsExpressionStatement().Expression)
		if assignment == nil || !ast.IsBinaryExpression(assignment) {
			return "", false
		}
		binary := assignment.AsBinaryExpression()
		if binary.OperatorToken.Kind != ast.KindEqualsToken {
			return "", false
		}
		stored := Unwrapped(binary.Right)
		if stored == nil || !ast.IsIdentifier(stored) || stored.Text() != parameterName.Text() {
			return "", false
		}
		return classField(binary.Left)
	}
	return "", false
}

// WriteStaticAccessorBacking lands `C.prop = v` where prop is a static
// SET accessor trivially fronting a backing field: the evaluated value
// goes under the place key "<C>.<#backing>". Answers whether it wrote.
func WriteStaticAccessorBacking(ctx *FlowContext, env Env, target *ast.Node, value abstractdomain.AbstractValue) bool {
	if target == nil || !ast.IsPropertyAccessExpression(target) {
		return false
	}
	access := target.AsPropertyAccessExpression()
	receiver := Unwrapped(access.Expression)
	declaration := staticClassDeclarationOf(ctx, receiver)
	if declaration == nil {
		return false
	}
	nameNode := access.Name()
	if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
		return false
	}
	backing, ok := staticAccessorBackingName(declaration, nameNode.Text(), true)
	if !ok {
		// a PLAIN static FIELD written through the class name — legal
		// module text for a public field — lands under its own key, and
		// the read side answers it ahead of any invariant (the write is
		// the flow's newer fact)
		if !staticFieldDeclared(declaration, nameNode.Text()) {
			return false
		}
		backing = nameNode.Text()
	}
	env.Set(receiver.Text()+"."+backing, value)
	return true
}

// staticFieldDeclared: the class declares a static PROPERTY of this
// name (plain or private identifier).
func staticFieldDeclared(classDeclaration *ast.Node, name string) bool {
	for _, member := range classDeclaration.ClassLikeData().Members.Nodes {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic == 0 {
			continue
		}
		memberName := member.Name()
		if memberName != nil && (ast.IsIdentifier(memberName) || ast.IsPrivateIdentifier(memberName)) &&
			memberName.Text() == name {
			return true
		}
	}
	return false
}

// readStaticAccessorBacking answers `C.prop` where prop is a static
// GET accessor trivially fronting a backing field: the flow-current
// place entry first, the backing field's invariant second, nil where
// neither speaks.
func readStaticAccessorBacking(ctx *FlowContext, env Env, declaration *ast.Node, receiver *ast.Node, name string) *abstractdomain.AbstractValue {
	backing, ok := staticAccessorBackingName(declaration, name, false)
	if !ok {
		return nil
	}
	if held, has := env.Get(receiver.Text() + "." + backing); has {
		return &held
	}
	invariants := StaticFieldInvariantsOf(ctx, declaration)
	if v, has := invariants[backing]; has {
		return &v
	}
	return nil
}
