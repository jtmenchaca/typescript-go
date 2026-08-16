// The public-field seal: when a class's NON-private fields keep the
// standing invariants the private ones already carry
// (class_field_invariants.go).
//
// A private field's invariant is complete because the language keeps
// outside code from writing it. A public field has no such fence — any
// holder of the instance writes it freely — so its invariant is honest
// only when the FILE ITSELF is the fence: the class value never leaves
// the file, no constructed instance leaves the file, and no text in the
// file writes the field through anything but `this`. Each rule below is
// one leg of that fence, and any reading this walk cannot make answers
// unsealed — the shape that would wrongly claim privacy.
//
// The boundary is tsc-typed code: a cast that manufactures a
// constructor or a foreign receiver is the cast's own vouch
// (CastAllowedByComment), not this seal's to police — the same line the
// `private`-modifier invariants already stand on.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// PublicFieldSeal is what the invariant collection asks: whether the
// class is sealed inside its file, and — only meaningful when it is —
// which field names some non-`this` text in the file writes anyway.
type PublicFieldSeal struct {
	Sealed bool
	// OutsideWritten: field names written through any receiver that is
	// not `this`, anywhere in the file. A `this.key` write belongs to
	// its own enclosing class or object literal and is collected by the
	// invariant walk's own sink; every other spelling writes through a
	// reference the sink never sees.
	OutsideWritten map[string]struct{}
}

// PublicFieldSealOf reads the seal for one class-like declaration.
func PublicFieldSealOf(ctx *FlowContext, declaration *ast.Node) PublicFieldSeal {
	c := checkerOf(ctx)
	if c == nil || declaration == nil {
		return PublicFieldSeal{}
	}
	sourceFile := ast.GetSourceFileOfNode(declaration)
	if sourceFile == nil || sourceFile.IsDeclarationFile {
		return PublicFieldSeal{}
	}
	// a DECORATED class hands its constructor to the decorator, which
	// may wrap or subclass it — holders this walk never sees
	if ast.HasDecorators(declaration) {
		return PublicFieldSeal{}
	}
	nameNode := sealBindingNameOf(declaration)
	if nameNode == nil {
		return PublicFieldSeal{}
	}
	if sealBindingExported(declaration, nameNode, sourceFile) {
		return PublicFieldSeal{}
	}
	symbol := symbolAt(c, nameNode)
	if symbol == nil {
		return PublicFieldSeal{}
	}
	if !classReferencesSealed(c, declaration, sourceFile.AsNode(), nameNode, symbol) {
		return PublicFieldSeal{}
	}
	return PublicFieldSeal{
		Sealed:         true,
		OutsideWritten: nonThisWrittenFieldNames(c, sourceFile.AsNode(), symbol),
	}
}

// sealBindingNameOf is the ONE name the file constructs the class
// through: a class declaration's own name, or the `const` a class
// expression is bound to. Nil — no seal — for a nameless declaration
// (`export default class`), a NAMED class expression (its inner name
// is a second binding this reading does not follow), and a class
// expression bound any other way (handed to a call, a `let` that may
// be reassigned).
func sealBindingNameOf(declaration *ast.Node) *ast.Node {
	if ast.IsClassDeclaration(declaration) {
		return declaration.Name()
	}
	if !ast.IsClassExpression(declaration) {
		return nil
	}
	if declaration.Name() != nil {
		return nil
	}
	holder := declaration
	for holder.Parent != nil && ast.IsParenthesizedExpression(holder.Parent) {
		holder = holder.Parent
	}
	parent := holder.Parent
	if parent == nil || !ast.IsVariableDeclaration(parent) {
		return nil
	}
	vd := parent.AsVariableDeclaration()
	if vd.Initializer == nil || Unwrapped(vd.Initializer) != declaration {
		return nil
	}
	if parent.Parent == nil || (parent.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return nil
	}
	name := vd.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	return name
}

// sealBindingExported: whether the binding leaves the file by
// declaration — an `export` modifier on the class or on the binding
// const's statement, or the spelled name in any of the file's own
// export clauses (the ExportedSymbolConst reading, generalized). An
// unreadable clause answers exported.
func sealBindingExported(declaration *ast.Node, nameNode *ast.Node, sourceFile *ast.SourceFile) bool {
	if ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsExport != 0 {
		return true
	}
	if ast.IsClassExpression(declaration) {
		// the const's VariableStatement carries the modifier
		if ast.GetCombinedModifierFlags(nameNode.Parent)&ast.ModifierFlagsExport != 0 {
			return true
		}
	}
	spelled := nameNode.Text()
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsExportDeclaration(statement) {
			continue
		}
		exportDeclaration := statement.AsExportDeclaration()
		if exportDeclaration.ModuleSpecifier != nil {
			continue
		}
		clause := exportDeclaration.ExportClause
		if clause == nil || !ast.IsNamedExports(clause) {
			return true
		}
		for _, element := range clause.AsNamedExports().Elements.Nodes {
			specifier := element.AsExportSpecifier()
			local := specifier.PropertyName
			if local == nil {
				local = specifier.Name()
			}
			if local != nil && ast.IsIdentifier(local) && local.Text() == spelled {
				return true
			}
		}
	}
	return false
}

// wrapperChainTop climbs the value-preserving wrappers ABOVE a use —
// parentheses, `as`, `!` — to the node whose parent consumes the
// value. The mirror of Unwrapped, which climbs DOWN.
func wrapperChainTop(node *ast.Node) *ast.Node {
	cursor := node
	for cursor.Parent != nil {
		parent := cursor.Parent
		if ast.IsParenthesizedExpression(parent) && parent.AsParenthesizedExpression().Expression == cursor {
			cursor = parent
			continue
		}
		if ast.IsAsExpression(parent) && parent.AsAsExpression().Expression == cursor {
			cursor = parent
			continue
		}
		if ast.IsNonNullExpression(parent) && parent.AsNonNullExpression().Expression == cursor {
			cursor = parent
			continue
		}
		break
	}
	return cursor
}

// classReferencesSealed: every reference to the class name in the file
// is one the seal can account for — a `new C(...)` whose instance
// stays behind member reads, an `x instanceof C` test, or a spelling
// inside a type node (types read, never write). Anything else — an
// alias, an argument, a heritage clause, a bare mention — hands the
// class value to text this seal does not read.
func classReferencesSealed(
	c *checker.Checker,
	declaration *ast.Node,
	fileNode *ast.Node,
	nameNode *ast.Node,
	symbol *ast.Symbol,
) bool {
	spelled := nameNode.Text()
	sealed := true
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if !sealed {
			return
		}
		if ast.IsIdentifier(node) && node.Text() == spelled && node != nameNode &&
			symbolAt(c, node) == symbol && !ast.IsPartOfTypeNode(node) {
			if !classReferenceSealed(c, declaration, node) {
				sealed = false
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return !sealed
		})
	}
	visit(fileNode)
	return sealed
}

// classReferenceSealed reads ONE value reference of the class name.
func classReferenceSealed(c *checker.Checker, declaration *ast.Node, reference *ast.Node) bool {
	use := wrapperChainTop(reference)
	parent := use.Parent
	if parent == nil {
		return false
	}
	if ast.IsNewExpression(parent) && parent.AsNewExpression().Expression == use {
		return instanceUseSealed(c, declaration, parent)
	}
	if ast.IsBinaryExpression(parent) {
		be := parent.AsBinaryExpression()
		if be.OperatorToken.Kind == ast.KindInstanceOfKeyword && be.Right == use {
			return true
		}
	}
	return false
}

// instanceUseSealed: the instance a `new C(...)` builds stays behind
// member reads — consumed on the spot by a member access, discarded,
// or bound to a local whose every reference is itself a member access
// or a whole-name reassignment. Anything else (a return, an argument,
// a property value, an export) carries the instance to text whose
// writes the invariant walk cannot read.
func instanceUseSealed(c *checker.Checker, declaration *ast.Node, newExpr *ast.Node) bool {
	use := wrapperChainTop(newExpr)
	parent := use.Parent
	if parent == nil {
		return false
	}
	if ast.IsExpressionStatement(parent) || ast.IsVoidExpression(parent) {
		return true
	}
	if (ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Expression == use) ||
		(ast.IsElementAccessExpression(parent) && parent.AsElementAccessExpression().Expression == use) {
		return memberUseSealed(declaration, parent)
	}
	if ast.IsVariableDeclaration(parent) && parent.AsVariableDeclaration().Initializer != nil &&
		Unwrapped(parent.AsVariableDeclaration().Initializer) == newExpr {
		if ast.GetCombinedModifierFlags(parent)&ast.ModifierFlagsExport != 0 {
			return false
		}
		name := parent.AsVariableDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) {
			return false
		}
		bound := symbolAt(c, name)
		if bound == nil {
			return false
		}
		return instanceBindingSealed(c, declaration, name, bound)
	}
	return false
}

// instanceBindingSealed: every reference to a local the instance is
// bound to is a member access, a `void` discard, or a whole-name
// reassignment (which DROPS the instance rather than moving it).
func instanceBindingSealed(
	c *checker.Checker,
	declaration *ast.Node,
	bindingName *ast.Node,
	bound *ast.Symbol,
) bool {
	fileNode := ast.GetSourceFileOfNode(bindingName).AsNode()
	spelled := bindingName.Text()
	sealed := true
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if !sealed {
			return
		}
		if ast.IsIdentifier(node) && node.Text() == spelled && node != bindingName &&
			symbolAt(c, node) == bound && !ast.IsPartOfTypeNode(node) {
			use := wrapperChainTop(node)
			parent := use.Parent
			allowed := false
			if parent != nil {
				switch {
				case ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Expression == use:
					allowed = memberUseSealed(declaration, parent)
				case ast.IsElementAccessExpression(parent) && parent.AsElementAccessExpression().Expression == use:
					allowed = memberUseSealed(declaration, parent)
				case ast.IsVoidExpression(parent):
					allowed = true
				case ast.IsBinaryExpression(parent) &&
					parent.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken &&
					parent.AsBinaryExpression().Left == use:
					allowed = true
				}
			}
			if !allowed {
				sealed = false
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return !sealed
		})
	}
	visit(fileNode)
	return sealed
}

// memberUseSealed reads one member access on a sealed instance. A
// FIELD access is a read or a write — the write scan speaks for the
// writes. A METHOD (or accessor-getter) access must be the callee of a
// call, spelled right there: extracted, the method may later run with
// a receiver the class's own text never met, and its `this.<field>`
// reads would wear an invariant that receiver never satisfied. A name
// the class does not declare — `constructor` above all — reaches
// machinery this seal does not read.
func memberUseSealed(declaration *ast.Node, access *ast.Node) bool {
	var name string
	if ast.IsPropertyAccessExpression(access) {
		nameNode := access.AsPropertyAccessExpression().Name()
		if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
			return false
		}
		name = nameNode.Text()
	} else {
		// a bracketed step names no member this reading can place
		return false
	}
	isField, isMethod := declaredMemberKind(declaration, name)
	if isField {
		return true
	}
	if !isMethod {
		return false
	}
	return access.Parent != nil && ast.IsCallExpression(access.Parent) &&
		access.Parent.AsCallExpression().Expression == access
}

// declaredMemberKind places a name among the class's own members:
// (field) for a property declaration or a constructor parameter
// property, (method) for a method or accessor. Neither for anything
// else.
func declaredMemberKind(declaration *ast.Node, name string) (isField bool, isMethod bool) {
	if declaration == nil || !ast.IsClassLike(declaration) {
		return false, false
	}
	for _, member := range declaration.ClassLikeData().Members.Nodes {
		memberName := member.Name()
		if memberName == nil || !(ast.IsIdentifier(memberName) || ast.IsPrivateIdentifier(memberName)) {
			if ast.IsConstructorDeclaration(member) {
				for _, parameter := range member.Parameters() {
					if !isParameterPropertyDeclaration(parameter) {
						continue
					}
					pd := parameter.AsParameterDeclaration()
					if pd.Name() != nil && ast.IsIdentifier(pd.Name()) && pd.Name().Text() == name {
						return true, false
					}
				}
			}
			continue
		}
		if memberName.Text() != name {
			continue
		}
		if ast.IsPropertyDeclaration(member) {
			return true, false
		}
		if ast.IsMethodDeclaration(member) || ast.IsGetAccessorDeclaration(member) ||
			ast.IsSetAccessorDeclaration(member) {
			return false, true
		}
	}
	return false, false
}

// nonThisWrittenFieldNames: every property name the FILE writes through
// a receiver that is not `this` — `x.age = v`, `x["age"] = v`,
// `x.age++`, `delete x.age`, a destructuring target — AND whose
// receiver's own type could actually hold an instance of `classSymbol`.
// The invariant walk's sink collects the `this.<key>` writes; these are
// the writes it cannot, whichever object they land on, so a name in
// this set vetoes the matching public field of the sealed class.
//
// A bare NAME match alone over-vetoes: `delete person.age` where
// `person` is a const bound to its own object literal in an unrelated
// function can never touch a `ThisPerson` instance — the literal is a
// fresh ordinary object and the const never rebinds. That PROVENANCE
// is the narrowing this reading uses; the receiver's stated TYPE
// proves nothing, because TypeScript is structural — `{ age?: number }`
// and `any` both admit a class instance behind them. Any receiver
// whose provenance is not that one provable shape still counts — the
// conservative, sound default.
func nonThisWrittenFieldNames(c *checker.Checker, fileNode *ast.Node, classSymbol *ast.Symbol) map[string]struct{} {
	written := map[string]struct{}{}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		var name string
		var receiver *ast.Node
		named := false
		if ast.IsPropertyAccessExpression(node) {
			access := node.AsPropertyAccessExpression()
			if Unwrapped(access.Expression).Kind != ast.KindThisKeyword {
				nameNode := access.Name()
				if nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
					name, named = nameNode.Text(), true
					receiver = access.Expression
				}
			}
		} else if ast.IsElementAccessExpression(node) {
			access := node.AsElementAccessExpression()
			if Unwrapped(access.Expression).Kind != ast.KindThisKeyword {
				argument := Unwrapped(access.ArgumentExpression)
				if argument != nil && ast.IsStringLiteralLike(argument) {
					name, named = argument.Text(), true
					receiver = access.Expression
				}
			}
		}
		if named && writtenAt(node) != writtenAtNone && receiverCouldHoldAnInstance(c, receiver) {
			written[name] = struct{}{}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(fileNode)
	return written
}

// receiverCouldHoldAnInstance: could `receiver` reference a class
// instance at runtime? The stated type cannot answer this — structural
// assignability lets `{ age?: number }` and `any` alike carry one. The
// ONE provably-clear shape is a CONST binding born from an object
// LITERAL: the literal is a fresh ordinary object and the const never
// rebinds, so no instance can ever sit behind the name. Everything
// else answers true — the conservative, sound default.
func receiverCouldHoldAnInstance(c *checker.Checker, receiver *ast.Node) bool {
	if c == nil || receiver == nil {
		return true
	}
	root := Unwrapped(receiver)
	if root == nil || !ast.IsIdentifier(root) {
		return true
	}
	symbol := symbolAt(c, root)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return true
	}
	declaration := symbol.Declarations[0]
	if !ast.IsVariableDeclaration(declaration) {
		return true
	}
	if declaration.Parent == nil || declaration.Parent.Flags&ast.NodeFlagsConst == 0 {
		return true
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return true
	}
	return !ast.IsObjectLiteralExpression(Unwrapped(initializer))
}
