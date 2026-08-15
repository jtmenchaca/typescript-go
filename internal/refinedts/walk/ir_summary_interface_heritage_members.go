// split from ir_summary_body.go — record parameters: interface heritage and shadowing

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// interfaceOwnMembersOf reads an interface's OWN member list under the
// scalar rules, allowing the EMPTY list an entry reading refuses:
// `interface Bounds extends Base {}` declares nothing itself and takes
// every member from its parent. The caller applies the entry rule to the
// MERGED list, so an interface with no members and no heritage still
// declines at an entry exactly as before.
//
// `parameterNames` are the interface's own type parameters, carried in
// so each member is tested for mentioning one — the invariance rule
// scalarMemberListOfIn states.
//
// CHECKER-LESS: kept for inferredCalleeReturnDeclaredLeaves
// (ir_object_slots_arm_type_nodes.go), the one remaining caller with no
// checker to thread and no cycle guard to carry — a call arm's resolved
// return type is read fresh with no `visiting` path of its own. Every
// caller that HOLDS a checker and a visiting path (declaredTypeMembersOf)
// calls interfaceOwnMembersWithCheckerIn directly instead, so an
// interface's own members get the SAME nested-family recursion and
// stable-symbol reading a type literal or alias already has.
func interfaceOwnMembersOf(
	holder string,
	asInterface *ast.InterfaceDeclaration,
	parameterNames map[string]struct{},
) ([]recordParamMember, bool) {
	return interfaceOwnMembersWithCheckerIn(nil, holder, asInterface, parameterNames, nil)
}

// interfaceOwnMembersWithCheckerIn is interfaceOwnMembersOf's widened
// reading: threaded with a CHECKER to resolve a computed member name's
// stable symbol and a nested member's own named-type reference
// (scalarMemberListWithCheckerIn's doc), and a `visiting` cycle guard so
// a nested member's type reference that resolves back onto THIS
// interface's own declaration terminates rather than recursing forever —
// the same guard heritageMembersOf's own link already carries.
func interfaceOwnMembersWithCheckerIn(
	c *checker.Checker,
	holder string,
	asInterface *ast.InterfaceDeclaration,
	parameterNames map[string]struct{},
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	if asInterface.Members == nil || len(asInterface.Members.Nodes) == 0 {
		return nil, true
	}
	return scalarMemberListWithCheckerIn(c, holder, asInterface.Members.Nodes, parameterNames, false, visiting)
}

// heritageMembersOf reads everything an interface INHERITS: each
// heritage clause's parent references resolved to their own members by
// the same reading, in clause and reference order, later parents
// shadowing earlier ones the way TypeScript's own resolution does.
//
// A parent reference carrying TYPE ARGUMENTS (`extends Box<number>`)
// declines — the same shape the entry reference itself refuses, for the
// same reason.
//
// A QUALIFIED parent (`extends NodeJS.EventEmitter`) resolves like a
// plain one. In a heritage clause the qualifier is spelled as a PROPERTY
// ACCESS rather than a QualifiedName — the parser reads a heritage
// parent as an expression — so both spellings are admitted here, and
// symbolAt answers on either.
func heritageMembersOf(
	ctx *FlowContext,
	holder string,
	asInterface *ast.InterfaceDeclaration,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	if asInterface.HeritageClauses == nil {
		return nil, true
	}
	var inherited []recordParamMember
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
			// a plain parent name, or a QUALIFIED one — spelled as a
			// property access in heritage position
			if parent.Expression == nil ||
				!(ast.IsIdentifier(parent.Expression) || ast.IsPropertyAccessExpression(parent.Expression)) {
				return nil, false
			}
			// a parent is a LINK, not the entry: one that reads to no data
			// member contributes nothing rather than declining this
			// interface
			members, ok := declaredTypeMembersOf(ctx, holder, parent.Expression, visiting, false)
			if !ok {
				return nil, false
			}
			inherited = mergeShadowedMembers(inherited, members)
		}
	}
	return inherited, true
}

// mergeShadowedMembers lays the `shadowing` list over the `base` one:
// a member both spell is the SHADOWING one's, kept at the position the
// base already gave it, and a member only the shadowing list spells is
// appended after. Keeping the base's position is what makes the layout
// deterministic — the slot order a parent's members took does not move
// because a child redeclared one of them.
//
// Identity is the FULL PATH (strings.Join(Path, ".")), never the bare
// Key: two members at different depths can share a Key ("deep" under
// two different parents), and only the joined path tells them apart.
func mergeShadowedMembers(base, shadowing []recordParamMember) []recordParamMember {
	if len(base) == 0 {
		return shadowing
	}
	at := map[string]int{}
	merged := make([]recordParamMember, 0, len(base)+len(shadowing))
	for _, member := range base {
		at[strings.Join(member.Path, ".")] = len(merged)
		merged = append(merged, member)
	}
	for _, member := range shadowing {
		path := strings.Join(member.Path, ".")
		if index, already := at[path]; already {
			merged[index] = member
			continue
		}
		at[path] = len(merged)
		merged = append(merged, member)
	}
	return merged
}
