// split from ir_summary_record_parameter_uses.go — the interior-read
// position classifier

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// interiorPathReadAt classifies a READ of a path the leaves do not
// spell — `p.inner` stopping short of a nested leaf, `p.mid` on an
// undeclared member, `p.ticks.map` reaching past one — by the position
// that consumes its VALUE. The read resolves to no slot, so nothing can
// be mis-answered at the read site itself; what remains is the value
// possibly being a REFERENCE into the caller's own object:
//
//   - a TEST (truthiness, equality, `typeof`, `in`-right) reads it and
//     answers a fresh boolean or tag — read-whole, the same spec
//     arguments the bare-name arms cite;
//   - a RETURN — the statement or a concise arrow body — or a SPREAD
//     hands out nothing the caller could not already reach through the
//     record it passed — read-whole, the `return p` argument one step
//     in;
//   - a call or new ARGUMENT, or the path standing as a CALLEE
//     (`p.ticks.map(…)` runs code holding the interior reference) —
//     handed-over: every leaf goes out Written and joins the havoc
//     vector, exactly as `f(p)`;
//   - an ELEMENT ACCESS RECEIVER (`p.axes.byId[id]`) — the element read
//     hands out no more than the interior read did, so the walk
//     continues to the element access's own consumer; `&&`/`||`/`??`
//     chains and parens/casts pass the value through the same way;
//   - a STORE (`const q = p.inner`) reads whole where interiorStoreAliasAdmissible
//     proves the new name is never written through in the enclosing
//     body (axisSelectors.ts's `const axis =
//     state.cartesianAxis.zAxis[axisId]`) — see that function's own
//     doc for why a read-only alias is sound though the interior value
//     resolves to no slot;
//   - everything else — an assignment right side, a property value, or
//     a store whose alias IS written through — refuses: a write
//     through the alias would move the caller's own leaf with no
//     statement mentioning `p`, which no havoc wiring covers.
func interiorPathReadAt(node *ast.Node) recordParameterUse {
	current := node
	for {
		parent := current.Parent
		if parent == nil {
			return recordParameterUnreadable
		}
		switch {
		case ast.IsParenthesizedExpression(parent) || ast.IsAsExpression(parent) ||
			ast.IsNonNullExpression(parent) || ast.IsTypeAssertion(parent) ||
			ast.IsSatisfiesExpression(parent):
			current = parent
			continue
		case ast.IsElementAccessExpression(parent):
			if parent.AsElementAccessExpression().Expression != current {
				// the path as the KEY expression: its value becomes a
				// property key — ToPropertyKey reads it, but the position
				// vocabulary does not follow keys; refuse
				return recordParameterUnreadable
			}
			current = parent
			continue
		case ast.IsReturnStatement(parent):
			return recordParameterReadWhole
		case ast.IsArrowFunction(parent) && parent.AsArrowFunction().Body == current:
			// a CONCISE arrow body (`(state) => state.options.kind`) is the
			// return position spelled without the keyword
			return recordParameterReadWhole
		case ast.IsSpreadAssignment(parent):
			return recordParameterReadWhole
		case ast.IsIfStatement(parent):
			if parent.AsIfStatement().Expression == current {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case ast.IsWhileStatement(parent):
			if parent.AsWhileStatement().Expression == current {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case ast.IsDoStatement(parent):
			if parent.AsDoStatement().Expression == current {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case ast.IsForStatement(parent):
			if parent.AsForStatement().Condition == current {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case ast.IsConditionalExpression(parent):
			if parent.AsConditionalExpression().Condition == current {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case ast.IsPrefixUnaryExpression(parent):
			if parent.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		case isTypeofOperand(current, parent):
			return recordParameterReadWhole
		case ast.IsBinaryExpression(parent):
			switch parent.AsBinaryExpression().OperatorToken.Kind {
			case ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken,
				ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken:
				return recordParameterReadWhole
			case ast.KindInKeyword:
				if isInOperatorRightOperand(current, parent) {
					return recordParameterReadWhole
				}
				return recordParameterUnreadable
			case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken, ast.KindQuestionQuestionToken:
				// the chain may answer the value itself — the chain's own
				// consumer decides, aliasBareUseAdmissible's reading
				current = parent
				continue
			}
			return recordParameterUnreadable
		case ast.IsCallExpression(parent):
			call := parent.AsCallExpression()
			if call.Expression == current {
				return recordParameterHandedOver
			}
			if call.Arguments != nil {
				for _, argument := range call.Arguments.Nodes {
					if argument == current {
						return recordParameterHandedOver
					}
				}
			}
			return recordParameterUnreadable
		case ast.IsNewExpression(parent):
			if arguments := argumentsOf(parent); arguments != nil {
				for _, argument := range arguments {
					if argument == current {
						return recordParameterHandedOver
					}
				}
			}
			return recordParameterUnreadable
		case ast.IsVariableDeclaration(parent):
			declaration := parent.AsVariableDeclaration()
			if declaration.Initializer == current && interiorStoreAliasAdmissible(parent) {
				return recordParameterReadWhole
			}
			return recordParameterUnreadable
		default:
			return recordParameterUnreadable
		}
	}
}

// interiorStoreAliasAdmissible answers whether `const q = <interior
// read>;` is safe to read as though the interior value had been used
// directly, without the store — axisSelectors.ts's `const axis =
// state.cartesianAxis.zAxis[axisId]; if (axis == null) { … } return
// axis;` (selectZAxisSettings).
//
// WHY A STORE CAN BE SOUND HERE THOUGH `const q = p` NEVER IS. The
// interior value resolves to NO SLOT at all (interiorPathReadAt's own
// doc: "the read resolves to no slot, so nothing can be mis-answered
// at the read site itself") — unlike the array-element alias
// (elementAliasSlotIndexOf, ir_object_slots_slot_index.go), which binds
// a real slot the layout already carved out and can therefore honor a
// WRITE through the alias by joining into it. There is no such slot
// here for a write to land in, so a write through `q` (`q.deep = 5`)
// can never be expressed soundly — but a READ through `q` writes
// nothing; it answers Unknown/opaque exactly as reading the interior
// path directly would have. So the alias is sound exactly where every
// use of `q` in the body is itself a READ-ONLY consuming position —
// the same vocabulary interiorPathReadAt already applies to the
// interior path itself, walked one more hop from `q`'s own
// declaration.
//
// The gate: a `const` declarator (never `let` — a reassigned alias
// could point `q` at a second interior read this admission never
// checked) naming a plain identifier, where EVERY occurrence of that
// name in the declaration's enclosing function body is a read-only
// consumer (interiorAliasUseAdmissible) — a test, a return, a spread,
// a call/new argument, or the receiver of a further property/element
// step whose OWN consumer is in turn read-only. A write of any kind
// through `q`, at any depth, refuses the whole admission — the same
// conservative, total-or-decline shape recordParameterUseOf's own
// write scan already takes for the record parameter itself.
func interiorStoreAliasAdmissible(declaration *ast.Node) bool {
	decl := declaration.AsVariableDeclaration()
	if decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return false
	}
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return false
	}
	body := enclosingFunctionBodyOf(declaration)
	if body == nil {
		return false
	}
	name := decl.Name().Text()
	declarationName := decl.Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName && !isPropertyStepName(node) {
			if !interiorAliasUseAdmissible(node) {
				ok = false
				return true
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

// interiorAliasUseAdmissible classifies ONE bare occurrence of an
// interior-read alias (interiorStoreAliasAdmissible's own `q`). It first
// climbs through every property/element step taken off `q` — `q.deep`,
// `q.deep.x`, `q[k]` — the same climb propertyPathAdmittingRootOptionalStep
// performs for a plain path read, since interiorPathReadAt itself is
// built to classify only the OUTERMOST node of such a chain (it has no
// PropertyAccessExpression arm of its own — a further named step past
// its argument is not a shape it expects to see, by construction: every
// existing caller hands it a path recordParameterUseOf's own scan
// already spelled all the way out). A WRITE found anywhere along that
// climb — an assignment target, a `++`/`--` step, a `delete` operand —
// refuses outright, since the aliased value has no slot for a write to
// land in. Once the climb reaches a node no further step extends,
// interiorPathReadAt classifies ITS consumer exactly as it would have
// classified the same chain reached directly off `p` — a test, a
// return, a spread, a call/new argument, a callee, an element-access
// receiver climbing on. A hand-over is admissible too here, since the
// alias carries no more risk handed to running code than the interior
// read itself already does.
func interiorAliasUseAdmissible(node *ast.Node) bool {
	outer, wrote := climbAliasSteps(node)
	if wrote {
		return false
	}
	use := interiorPathReadAt(outer)
	return use == recordParameterReadWhole || use == recordParameterHandedOver
}

// climbAliasSteps climbs from NODE through every paren/cast/property/
// element step built directly off it — `q` to `q.deep`, `q.deep` to
// `q.deep.x`, and so on — answering the OUTERMOST node reached and
// whether a WRITE (an assignment target, a `++`/`--` step, a `delete`
// operand) was found anywhere along the way. The outermost node is what
// interiorPathReadAt then classifies by ITS OWN consumer, mirroring how
// propertyPathAdmittingRootOptionalStep spells a whole chain before
// recordParameterUseOf ever asks what consumes it.
func climbAliasSteps(node *ast.Node) (outer *ast.Node, wrote bool) {
	current := node
	for {
		parent := current.Parent
		if parent == nil {
			return current, false
		}
		switch {
		case ast.IsParenthesizedExpression(parent) || ast.IsAsExpression(parent) ||
			ast.IsNonNullExpression(parent) || ast.IsTypeAssertion(parent) ||
			ast.IsSatisfiesExpression(parent):
			current = parent
			continue
		case ast.IsPropertyAccessExpression(parent):
			if parent.AsPropertyAccessExpression().Expression != current {
				return current, false
			}
			current = parent
			continue
		case ast.IsElementAccessExpression(parent):
			if parent.AsElementAccessExpression().Expression != current {
				return current, false
			}
			current = parent
			continue
		case ast.IsBinaryExpression(parent):
			bin := parent.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment && bin.Left == current {
				return current, true
			}
			return current, false
		case ast.IsPrefixUnaryExpression(parent):
			unary := parent.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				return current, true
			}
			return current, false
		case ast.IsPostfixUnaryExpression(parent):
			return current, true
		case ast.IsDeleteExpression(parent):
			return current, true
		default:
			return current, false
		}
	}
}
