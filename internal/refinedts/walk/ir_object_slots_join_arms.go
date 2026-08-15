// split from ir_object_slots.go — the TWO-ARMED JOIN family source

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// joinedArmLeavesOf is recognizer (3): `const x = a ?? b` and
// `const x = c ? a : b` where BOTH arms flatten to the SAME member
// family, which the local then takes as its own.
//
// The join is per leaf: a member both arms spell keeps its sort where
// the two agree and takes the unknown sort where they disagree — the
// weaker reading, which claims nothing either arm contradicts. Arms
// naming DIFFERENT member sets have no one family, so the local keeps
// its whole-name slot: one name has one slot family, the exclusivity
// rule the whole flattening rests on.
//
// Each arm is read by armLeavesOf: a spelled object literal, a name
// whose own declaration flattens, or — for any arm at all — the members
// its own DECLARED TYPE spells. So a member read (`request.socket`) is
// an admissible arm whenever the member's annotation reads as a record;
// what refuses it is the annotation, never the arm's syntax. An arm
// whose type reads as a class, a union, or a generic still names no
// member family, and the whole shape declines.
func joinedArmLeavesOf(
	ctx *FlowContext,
	declaration *ast.Node,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return nil, false
	}
	holder := decl.Name().Text()
	left, right, isJoin := joinArmsOf(Unwrapped(decl.Initializer))
	if !isJoin {
		return nil, false
	}
	leftKeys, leftOk := armLeavesOf(ctx, holder, left, familyOfName)
	if !leftOk {
		return nil, false
	}
	rightKeys, rightOk := armLeavesOf(ctx, holder, right, familyOfName)
	if !rightOk {
		return nil, false
	}
	if recordShapeOf(leftKeys) != recordShapeOf(rightKeys) {
		// the arms disagree about the family — no one slot layout serves
		// both, so the name stays whole
		return nil, false
	}
	sortOfPath := map[string]BindingKind{}
	tagOfPath := map[string]TypeofTag{}
	for _, key := range rightKeys {
		sortOfPath[strings.Join(key.Path, ".")] = ObjectLocalKeySort(key)
		tagOfPath[strings.Join(key.Path, ".")] = ObjectLocalKeyTypeof(key)
	}
	out := make([]ObjectLocalKey, 0, len(leftKeys))
	for _, key := range leftKeys {
		path := strings.Join(key.Path, ".")
		sort := ObjectLocalKeySort(key)
		tag := ObjectLocalKeyTypeof(key)
		if other := sortOfPath[path]; other != sort {
			sort = BindingKindUnknown
		}
		if other := tagOfPath[path]; other != tag {
			tag = TypeofTagNone
		}
		out = append(out, ObjectLocalKey{
			Path:      key.Path,
			Key:       key.Key,
			SlotName:  key.SlotName,
			Declared:  true,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return out, true
}

// joinArmsOf is the two arms a joining initializer holds: `a ?? b`'s
// sides, and a ternary's two branches. Its CONDITION is not an arm —
// nothing about the record's shape comes from it.
func joinArmsOf(initializer *ast.Node) (left *ast.Node, right *ast.Node, ok bool) {
	if initializer == nil {
		return nil, nil, false
	}
	if ast.IsBinaryExpression(initializer) {
		bin := initializer.AsBinaryExpression()
		if bin.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			return Unwrapped(bin.Left), Unwrapped(bin.Right), true
		}
		return nil, nil, false
	}
	if initializer.Kind == ast.KindConditionalExpression {
		conditional := initializer.AsConditionalExpression()
		if conditional.WhenTrue == nil || conditional.WhenFalse == nil {
			return nil, nil, false
		}
		return Unwrapped(conditional.WhenTrue), Unwrapped(conditional.WhenFalse), true
	}
	return nil, nil, false
}

// armLeavesOf is the member family ONE arm of a join names, spelled
// under the joined local's holder: an object literal's own leaves, the
// family a spelled name's declaration already flattens to, or — for any
// arm at all — the members the arm's own RESOLVED TYPE spells.
//
// WHY THE TYPE READING IS THE RIGHT THIRD ARM. The first two arms read
// the arm's VALUE: a literal names its rows, a flattening name names the
// family its declaration already carries. Neither reaches
// `request.socket` or `pick()`, and the reason is not that those arms
// promise less — it is that the reading was looking in the wrong place.
// What a join arm has to supply is a MEMBER FAMILY, and a declared type
// is exactly a promise of member names over every value the expression
// could produce. That is the same argument declaredTypeLeavesOf already
// makes for an opaque initializer, applied one level out: the annotation
// promises the names, the leaves claim nothing about the values, and the
// slots then answer whatever the whole-name slot answered.
//
// So the fallback is by DECLARED TYPE and works for ANY arm expression
// whose resolution spells a record — a name, a member read, a call — and
// every existing rule stands on top of it unchanged: the two arms must
// still agree leaf for leaf (recordShapeOf), and the per-leaf sort join
// still weakens a disagreement to unknown.
//
// A leaf here is one step by construction (declaredLeavesOf spells a
// member list, never a path), which is what keeps a type-read arm and a
// literal arm comparable at all.
func armLeavesOf(
	ctx *FlowContext,
	holder string,
	arm *ast.Node,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if arm == nil {
		return nil, false
	}
	if ast.IsObjectLiteralExpression(arm) {
		return flatKeysOfLiteral(arm, holder, nil)
	}
	if ast.IsIdentifier(arm) && familyOfName != nil {
		if keys, found := familyOfName(arm.Text()); found {
			// respell the family under THIS local's holder — the arm's own
			// holder names the arm's slots, not the joined local's
			out := make([]ObjectLocalKey, 0, len(keys))
			for _, key := range keys {
				out = append(out, ObjectLocalKey{
					Path:        key.Path,
					Key:         key.Key,
					SlotName:    holder + "." + strings.Join(key.Path, "."),
					Initializer: key.Initializer,
					Declared:    key.Declared,
					Sort:        key.Sort,
					TypeofTag:   key.TypeofTag,
				})
			}
			return out, true
		}
	}
	return resolvedTypeArmLeaves(ctx, holder, arm)
}

// resolvedTypeArmLeaves is the member family ONE join arm's own
// DECLARED TYPE spells, whatever the arm's syntax is.
//
// The arm resolves through the checker to the declaration it names, and
// that declaration's SPELLED type node is read by declaredTypeNodeMembers
// — the same reader a declared record local and a record parameter take,
// so an arm annotated `Bounds` and a local declared `Bounds` expand to
// byte-identical member lists. The checker is asked only which
// declaration an expression names; the members come off that
// declaration's own syntax, which is what keeps the answer
// check-independent the way the one-level reading is.
//
// The reachable arms, all through one resolution: a NAME resolves to its
// variable or parameter declaration, a MEMBER READ (`h.window`) resolves
// to the property declaration or signature it steps to, and a CALL
// (`make()`) resolves through its CALLEE to that declaration's spelled
// RETURN annotation (calleeReturnTypeNodeOf) — a call places no symbol
// of its own, but what it promises is exactly what its callee declares
// it returns. A call whose callee spells NO return annotation falls
// through to inferredCalleeReturnDeclaredLeaves: the checker's own
// RESOLVED return type, read only for which declaration its symbol
// names, fed through the same interface/alias member rules the spelled
// path uses (that function's own doc states the refusals). One shape
// resolves to no declaration and declines here:
//
//   - a member read whose RECEIVER is `any`. The checker resolves the
//     whole access to `any` and places no symbol, which is the correct
//     answer: an `any` receiver promises no member to anybody, so there
//     is no annotation naming a family and none can be invented. This is
//     what nest's `request.socket` is — `TRequest extends
//     IncomingMessage = any` makes the receiver's resolved type `any`,
//     and the arm names no family for that reason rather than for the
//     class rule.
//
// Everything declaredTypeNodeMembers refuses refuses here too: a CLASS
// type (an instance carries methods, private state and identity no
// flattening holds), a union, a generic with type arguments, a merged
// interface, a non-record annotation, and a declaration with no spelled
// type at all (an inferred field). In each case the arm names no family
// and the joined local keeps its whole-name slot.
func resolvedTypeArmLeaves(ctx *FlowContext, holder string, arm *ast.Node) ([]ObjectLocalKey, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || arm == nil {
		return nil, false
	}
	typeNode := declaredTypeNodeOfExpression(ctx, arm)
	if typeNode != nil {
		members, isRecord := declaredTypeNodeMembers(ctx, holder, typeNode)
		if !isRecord {
			return nil, false
		}
		return declaredLeavesOf(holder, members), true
	}
	// no SPELLED return annotation — the one shape left admitted is a
	// CALL whose callee's INFERRED return type resolves to a single
	// interface or type-alias-of-literal declaration
	if !ast.IsCallExpression(arm) {
		return nil, false
	}
	members, isRecord := inferredCalleeReturnDeclaredLeaves(ctx, holder, arm)
	if !isRecord {
		return nil, false
	}
	return declaredLeavesOf(holder, members), true
}
