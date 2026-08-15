// split from ir_field_bundles.go — the heritage chain and its members

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// declaredBundleFieldsOf is the reading BundleTypeFieldsOf performs at
// every link of a heritage chain: take ONE name's declarations and
// answer the fields they stand for, including whatever they inherit.
//
// The declaration list is SCANNED rather than required to be of length
// one — this file's own resolution policy, kept — but a symbol MORE THAN
// ONE of whose declarations contributes members declines: a merged
// interface splits its member list across declarations, and answering
// from one of them would build a field set the other contradicts. A
// declaration that contributes nothing (an enum, a module, a function
// declaration sharing the name) does not count against that.
//
// `visiting` holds the declaration nodes already on this walk's path. A
// name resolving back onto one of them is a cycle, which answers false
// rather than recursing forever.
func declaredBundleFieldsOf(
	ctx *FlowContext,
	declarations []*ast.Node,
	visiting []*ast.Node,
) ([]BundleField, bool) {
	var contributor *ast.Node
	for _, declaration := range declarations {
		if declaration == nil {
			continue
		}
		if !ast.IsInterfaceDeclaration(declaration) && !ast.IsTypeAliasDeclaration(declaration) {
			continue
		}
		if contributor != nil {
			// two declarations both spell members: the merged interface's
			// split member list, which no single reading answers
			return nil, false
		}
		contributor = declaration
	}
	if contributor == nil {
		return nil, false
	}
	for _, seen := range visiting {
		if seen == contributor {
			return nil, false
		}
	}
	if ast.IsInterfaceDeclaration(contributor) {
		asInterface := contributor.AsInterfaceDeclaration()
		// a type parameter means the annotations are not the instance's own
		if asInterface.TypeParameters != nil && len(asInterface.TypeParameters.Nodes) > 0 {
			return nil, false
		}
		var own []BundleField
		if asInterface.Members != nil {
			own = typeElementFieldsOf(asInterface.Members.Nodes)
		}
		// a fresh path slice per link: sibling parents each recurse with
		// their own copy, so one branch's appends never land in another's
		path := make([]*ast.Node, len(visiting), len(visiting)+1)
		copy(path, visiting)
		inherited, inheritedOk := heritageBundleFieldsOf(ctx, asInterface, append(path, contributor))
		if !inheritedOk {
			return nil, false
		}
		return mergeShadowedFields(inherited, own), true
	}
	asAlias := contributor.AsTypeAliasDeclaration()
	if asAlias.TypeParameters != nil && len(asAlias.TypeParameters.Nodes) > 0 {
		return nil, false
	}
	if asAlias.Type == nil || !ast.IsTypeLiteralNode(asAlias.Type) {
		return nil, false
	}
	return typeElementFieldsOf(asAlias.Type.AsTypeLiteralNode().Members.Nodes), true
}

// heritageBundleFieldsOf reads everything an interface INHERITS: each
// heritage clause's parent references resolved to their own fields by
// the same reading, in clause and reference order, later parents
// shadowing earlier ones the way TypeScript's own resolution does.
//
// A parent reference carrying TYPE ARGUMENTS (`extends Box<number>`) or
// spelled by anything but a plain identifier (`extends ns.Base`)
// declines — the members would depend on what was applied, which no slot
// vector spells, and a qualified name reaches into a namespace this
// reading does not claim to resolve.
func heritageBundleFieldsOf(
	ctx *FlowContext,
	asInterface *ast.InterfaceDeclaration,
	visiting []*ast.Node,
) ([]BundleField, bool) {
	if asInterface.HeritageClauses == nil {
		return nil, true
	}
	var inherited []BundleField
	for _, clause := range asInterface.HeritageClauses.Nodes {
		heritage := clause.AsHeritageClause()
		if heritage.Types == nil {
			continue
		}
		for _, reference := range heritage.Types.Nodes {
			if !ast.IsExpressionWithTypeArguments(reference) {
				return nil, false
			}
			parent := reference.AsExpressionWithTypeArguments()
			if parent.TypeArguments != nil && len(parent.TypeArguments.Nodes) > 0 {
				return nil, false
			}
			if parent.Expression == nil || !ast.IsIdentifier(parent.Expression) {
				return nil, false
			}
			symbol := symbolAt(ctx.P.Checker, parent.Expression)
			if symbol == nil {
				return nil, false
			}
			fields, ok := declaredBundleFieldsOf(ctx, symbol.Declarations, visiting)
			if !ok {
				return nil, false
			}
			inherited = mergeShadowedFields(inherited, fields)
		}
	}
	return inherited, true
}

// mergeShadowedFields lays the `shadowing` list over the `base` one: a
// field both spell is the SHADOWING one's, kept at the position the base
// already gave it, and a field only the shadowing list spells is
// appended after. Keeping the base's position is what makes the layout
// deterministic — the slot order a parent's fields took does not move
// because a child redeclared one of them.
func mergeShadowedFields(base, shadowing []BundleField) []BundleField {
	if len(base) == 0 {
		return shadowing
	}
	at := map[string]int{}
	merged := make([]BundleField, 0, len(base)+len(shadowing))
	for _, field := range base {
		at[field.Name] = len(merged)
		merged = append(merged, field)
	}
	for _, field := range shadowing {
		if index, already := at[field.Name]; already {
			merged[index] = field
			continue
		}
		at[field.Name] = len(merged)
		merged = append(merged, field)
	}
	return merged
}

// typeElementFieldsOf is ClassFieldsOf's rule over TYPE ELEMENTS —
// interface members and type-literal members. A property signature with
// an identifier name is a field; a method signature, a call or construct
// signature, an index signature, and a computed name are not.
//
// An OPTIONAL member (`lo?: number`) contributes wearing an unknown
// sort: absence is a value the annotation's own sort does not cover, and
// the census reports the field rather than dropping it — a dropped field
// would leave a body's read of it spelling a slot the layout never made.
func typeElementFieldsOf(members []*ast.Node) []BundleField {
	fields := make([]BundleField, 0, len(members))
	seen := map[string]struct{}{}
	for _, member := range members {
		if !ast.IsPropertySignatureDeclaration(member) {
			continue
		}
		signature := member.AsPropertySignatureDeclaration()
		name := signature.Name()
		if name == nil || !ast.IsIdentifier(name) {
			continue
		}
		text := name.Text()
		if _, already := seen[text]; already {
			continue
		}
		seen[text] = struct{}{}
		sort, tag := annotationSort(signature.Type)
		if signature.PostfixToken != nil {
			sort, tag = BindingKindUnknown, TypeofTagNone
		}
		fields = append(fields, BundleField{
			Name:      text,
			SlotName:  text,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return fields
}
