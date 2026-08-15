// split from ir_object_slots.go — the CONSTRUCTOR family source

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// constructedLeavesOf is recognizer (1): a `const x = new C()` local
// whose constructor SERVES flattens to the fields that constructor
// writes, each leaf wearing its DECLARED field sort.
//
// The field family comes from the same rows constructorFieldRets
// threads: a served constructor's summary carries one this-entry per
// field the body touches, and the WRITTEN ones are the fields the
// constructor actually left a value in. Those are the leaves worth
// slots — an entry the constructor only READS holds whatever the field
// entered with, which for a fresh instance is absent, so giving it a
// leaf would offer a slot no exit ever fills.
//
// The SORT comes from the class's own field declarations (ClassFieldsOf,
// the reading the receiver bundle already lays its slots out under), not
// from the exit row: a BundleEntry names where a slot sits and whether
// the body moved it, and nothing else. Reading the sort from the
// declaration is what makes this local's leaf and the same field's slot
// inside a method wear one sort. A written field the class does not
// declare — one assigned in the constructor with no property
// declaration — has no annotation to read and takes the unknown sort,
// which admits only the definedness test.
//
// A constructor that does not serve — no summary, no resolvable class,
// no written this-field — declines, and the local keeps its whole-name
// slot exactly as today.
func constructedLeavesOf(ctx *FlowContext, declaration *ast.Node) ([]ObjectLocalKey, bool) {
	if ctx == nil || !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return nil, false
	}
	construction := Unwrapped(decl.Initializer)
	if construction == nil || !ast.IsNewExpression(construction) {
		return nil, false
	}
	constructor := constructedClassConstructor(ctx, construction)
	if constructor == nil {
		return nil, false
	}
	shape, served := LowerSummaryBody(ctx, constructor)
	if !served {
		return nil, false
	}
	// the declared sorts of the class the constructor belongs to; a class
	// whose fields do not read leaves every leaf unknown-sorted
	sortOfField := map[string]BundleField{}
	if fields, readable := ClassFieldsOf(ctx, constructor.Parent); readable {
		for _, field := range fields {
			sortOfField[field.Name] = field
		}
	}
	holder := decl.Name().Text()
	var keys []ObjectLocalKey
	seen := map[string]struct{}{}
	for _, entry := range shape.BundleEntries {
		if !entry.Written {
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		if _, already := seen[field]; already {
			continue
		}
		seen[field] = struct{}{}
		sort, tag := BindingKindUnknown, TypeofTagNone
		if declared, found := sortOfField[field]; found {
			sort, tag = declared.Sort, declared.TypeofTag
		}
		keys = append(keys, ObjectLocalKey{
			Path:      []string{field},
			Key:       field,
			SlotName:  holder + "." + field,
			Declared:  true,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	if len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// constructedClassConstructor resolves a `new C()` to the CONSTRUCTOR
// declaration whose summary the leaves are read from — the SAME
// resolution the call lowering uses, so the leaves and the exit rows
// that fill them always name one declaration. A `new` whose callee does
// not resolve to a class with a constructor body answers nil.
func constructedClassConstructor(ctx *FlowContext, construction *ast.Node) *ast.Node {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil
	}
	callee := Unwrapped(construction.AsNewExpression().Expression)
	if callee == nil || !ast.IsIdentifier(callee) {
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, callee)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	classLike := symbol.ValueDeclaration
	if !ast.IsClassLike(classLike) || ast.GetSourceFileOfNode(classLike).IsDeclarationFile {
		return nil
	}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if ast.IsConstructorDeclaration(member) && member.Body() != nil {
			return member
		}
	}
	return nil
}
