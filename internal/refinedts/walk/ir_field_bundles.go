// The FIELD CENSUS: what an object with a declared shape is worth as a
// bundle of slots, and what one body does with it.
//
// Wave 4 makes every object with a declared shape a slot bundle — a
// method's `this`, a class/interface-typed parameter. Two seams read
// that bundle: the LAYOUT, which decides how many entry slots the body
// gets and what each one is sorted as, and the CALL SITES, which fill
// those entries from the caller's own knowledge and map written fields
// back out. THE CONTRACT OF THIS FILE IS THAT BOTH READ IT. A layout
// built from one reading of a class's fields and a call site built from
// another would disagree about the slot vector's length or order, and
// the apply side would fill entry k from a caller value belonging to
// entry j — a silent unsoundness with no syntax to point at. So the
// field set (ClassFieldsOf / BundleTypeFieldsOf) and the per-body use
// report (FieldCensusOf) live here, are deterministic, read only syntax
// plus the checker's symbol resolution, and answer in declaration order.
//
// The census REPORTS shape; it never decides policy. A field with an
// annotation nothing can sort still appears, wearing an unknown sort; a
// computed member access and an escaping mention are reported as flags,
// not as declines. Whether an unknown-sorted field can carry an entry,
// whether a computed access havocs the bundle or refuses the body — the
// consumer rules on that, from one report.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the declared field set ──────────────────────────────────────── */

// BundleField is one declared field of a bundled object: the name it is
// spelled under, the slot name a body reads it by ("this.container",
// "wrapper.metatype"), and the sort and typeof evidence its OWN
// annotation states.
//
// The sort reading is declaredParamSort's, member-wise: number and
// boolean ride the number sort (their typeof differs), string rides the
// string sort, anything else is unknown. A summary quantifies over every
// entry the field can hold, so only what the annotation itself spells is
// promised.
type BundleField struct {
	Name      string
	SlotName  string
	Sort      BindingKind
	TypeofTag TypeofTag
}

// annotationSort reads a field's sort and typeof evidence from its own
// type annotation, exactly as declaredParamSort/declaredParamTypeof read
// a parameter's. A missing annotation, or one this reading does not
// spell, is unknown — which admits only the definedness test.
func annotationSort(typeNode *ast.Node) (BindingKind, TypeofTag) {
	if typeNode == nil {
		return BindingKindUnknown, TypeofTagNone
	}
	switch typeNode.Kind {
	case ast.KindNumberKeyword:
		return BindingKindNumber, TypeofTagNumber
	case ast.KindBooleanKeyword:
		// booleans ride the number sort — declaredParamSort's own rule
		return BindingKindNumber, TypeofTagBoolean
	case ast.KindStringKeyword:
		return BindingKindString, TypeofTagString
	default:
		return BindingKindUnknown, TypeofTagNone
	}
}

// ClassFieldsOf is the declared INSTANCE FIELDS of a class declaration
// or class expression, in declaration order, spelled under the given
// receiver by the caller (the slot name is filled in by BundleFieldsAs;
// here SlotName carries the bare field name and the caller re-spells).
//
// What counts as a field: a property declaration whose name is a plain
// identifier or a private identifier. What does not:
//
//   - a STATIC member — it belongs to the constructor object, not to any
//     instance, so no instance's bundle holds it;
//   - a METHOD, a get/set ACCESSOR, a constructor, an index signature —
//     a method resolves as a CALL (through ContractBySymbol, carrying its
//     own summary), never as a slot, and an accessor with a body runs
//     code the slot could not stand for;
//   - a member whose name is COMPUTED (`[key]: number`) — nothing spells
//     the slot. Note this is the DECLARATION being computed; a computed
//     ACCESS in a body is FieldCensus.Computed's business.
//
// A field whose annotation this reading cannot sort still CONTRIBUTES,
// wearing an unknown sort — the census reports the shape it found, and
// the consumer decides whether an unknown-sorted slot is worth an entry.
// That is the whole difference from recordParamMembersOf, which declines
// the parameter outright on the first unreadable member: a record
// parameter is all-or-nothing because its holder name disappears, while a
// bundle's receiver stays and its readable fields are still worth slots.
//
// (false) only for a node that is not class-like at all.
func ClassFieldsOf(ctx *FlowContext, classLike *ast.Node) ([]BundleField, bool) {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, false
	}
	members := classLike.ClassLikeData().Members.Nodes
	fields := make([]BundleField, 0, len(members))
	seen := map[string]struct{}{}
	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		declaration := member.AsPropertyDeclaration()
		name := declaration.Name()
		if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
			continue
		}
		text := name.Text()
		// a name declared twice (a class body tsc would refuse, or two
		// spellings colliding) contributes once — the slot vector must not
		// hold the same spelling twice
		if _, already := seen[text]; already {
			continue
		}
		seen[text] = struct{}{}
		sort, tag := annotationSort(declaration.Type)
		fields = append(fields, BundleField{
			Name:      text,
			SlotName:  text,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return fields, true
}

// BundleFieldsAs re-spells a field set under a receiver name: the fields
// of a class read through `this` are spelled "this.container", the same
// fields read through a parameter named wrapper are "wrapper.metatype".
// The layout and the call sites both spell slots this way, so the
// re-spelling is one function rather than two string concatenations.
func BundleFieldsAs(receiverName string, fields []BundleField) []BundleField {
	out := make([]BundleField, len(fields))
	for index, field := range fields {
		out[index] = BundleField{
			Name:      field.Name,
			SlotName:  receiverName + "." + field.Name,
			Sort:      field.Sort,
			TypeofTag: field.TypeofTag,
		}
	}
	return out
}

// BundleTypeFieldsOf is the declared field set behind a class- or
// interface-typed ANNOTATION — the parameter side of the bundle
// (`wrapper: InstanceWrapper`, `module: Module`).
//
// A TYPE LITERAL annotation (`p: { lo: number }`) reads its members
// directly, no checker involved. Anything else is a TYPE REFERENCE whose
// identifier resolves through symbolAt — following import aliases, so a
// type imported from another file answers from its declaration there —
// to whichever declaration the name has:
//
//   - a CLASS → ClassFieldsOf, so a class-typed parameter and a method's
//     own `this` read one field set;
//   - an INTERFACE → its property signatures, subject to the same field
//     rules (method signatures are calls, not slots; a computed name
//     spells nothing). An interface with HERITAGE (`extends`) or TYPE
//     PARAMETERS declines: an inherited member is declared in a
//     declaration this reading never visits, so the field set would be
//     incomplete and a body reading the inherited member would read a
//     slot that is not there; a type parameter means the members'
//     annotations are not the ones the instance actually holds;
//   - a TYPE ALIAS of a type literal → the literal's members. An alias of
//     anything else (a union, another reference, a mapped type) declines
//     — the census reads syntax, and only a literal spells its members.
//
// (false) where nothing resolves: a nil context, a context with no
// program or no checker (the nil-tolerance the callers rely on — a
// lowering that runs without a checker must decline the bundle, never
// crash), an unresolved name, a name whose declarations are none of the
// three above.
//
// Fields come back spelled bare; the caller re-spells them under the
// parameter's name with BundleFieldsAs.
func BundleTypeFieldsOf(ctx *FlowContext, typeNode *ast.Node) ([]BundleField, bool) {
	if typeNode == nil {
		return nil, false
	}
	// a type literal spells its own members — no resolution needed
	if ast.IsTypeLiteralNode(typeNode) {
		return typeElementFieldsOf(typeNode.AsTypeLiteralNode().Members.Nodes), true
	}
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil, false
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	typeName := typeNode.AsTypeReferenceNode().TypeName
	if typeName == nil {
		return nil, false
	}
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil {
		return nil, false
	}
	for _, declaration := range symbol.Declarations {
		if ast.IsClassLike(declaration) {
			return ClassFieldsOf(ctx, declaration)
		}
	}
	for _, declaration := range symbol.Declarations {
		if !ast.IsInterfaceDeclaration(declaration) {
			continue
		}
		asInterface := declaration.AsInterfaceDeclaration()
		// heritage hides members this reading never sees; a type parameter
		// means the annotations are not the instance's own
		if asInterface.HeritageClauses != nil && len(asInterface.HeritageClauses.Nodes) > 0 {
			return nil, false
		}
		if asInterface.TypeParameters != nil && len(asInterface.TypeParameters.Nodes) > 0 {
			return nil, false
		}
		return typeElementFieldsOf(asInterface.Members.Nodes), true
	}
	for _, declaration := range symbol.Declarations {
		if !ast.IsTypeAliasDeclaration(declaration) {
			continue
		}
		asAlias := declaration.AsTypeAliasDeclaration()
		if asAlias.TypeParameters != nil && len(asAlias.TypeParameters.Nodes) > 0 {
			return nil, false
		}
		if asAlias.Type == nil || !ast.IsTypeLiteralNode(asAlias.Type) {
			return nil, false
		}
		return typeElementFieldsOf(asAlias.Type.AsTypeLiteralNode().Members.Nodes), true
	}
	return nil, false
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

/* ── the per-body use report ─────────────────────────────────────── */

// FieldCensus is what ONE body does with ONE bundle: which declared
// fields it reads, which it writes, and the two shapes that put the
// whole bundle out of the lowering's sight.
//
// Reads and Writes hold the fields themselves — spelled and sorted as
// the declaration gave them — in the declaration order of the field set
// the census was asked about, never in the order the body happens to
// mention them. That order is what makes two readings of one census
// produce the same slot vector.
//
// A written field appears in BOTH lists when the body also reads it; a
// write-only field appears only in Writes. The layout gives every field
// in either list a slot; the call site maps the Writes back out through
// rets, the way a return value rides.
//
// Computed is any `receiver[e]` element access on the receiver: the
// expression names no field, so nothing bounds WHICH slot moved — but
// the DECLARATION bounds the set, so a consumer can havoc every slot of
// this one object rather than declining.
//
// Escapes is any occurrence of the receiver the lowering cannot follow:
// a bare mention (`f(this)`, `return wrapper`, `xs.push(this)`), an
// alias (`const w = wrapper`), an optional-chained or non-declared
// member. A bundle that escapes may be written through a name the census
// never saw, so its slots cannot be trusted past that point.
type FieldCensus struct {
	Reads    []BundleField
	Writes   []BundleField
	Computed bool
	Escapes  bool
}

// FieldCensusOf scans a body for what it does with `receiverName` —
// "this" for a method's own receiver (ThisKeyword receivers are
// recognized under that spelling), or a parameter's identifier for a
// class/interface-typed parameter.
//
// The four dimensions, as implemented:
//
//   - a READ is `<receiver>.<field>` in any position that is not a write
//     target. `this.injector.load(…)` is the read of injector — the
//     receiver of a method call is a field value like any other — while
//     `load` is a METHOD name in callee position, which resolves through
//     its own summary rather than a slot, so an undeclared name there
//     contributes nothing and is NOT an escape (a declared field called
//     as a function still reads that field).
//   - a WRITE is `<receiver>.<field>` as an assignment target (plain or
//     compound), the operand of `++`/`--`, or the operand of `delete`. A
//     compound write and an update also READ, so both lists get the field.
//   - COMPUTED is `<receiver>[e]` — an element access whose own receiver
//     is this receiver.
//   - an ESCAPE is every other occurrence of the receiver: a bare
//     identifier or `this` that is not the receiver of one of the above,
//     an optional chain (`this?.x` — its receiver may be absent, which no
//     slot spells), and a member the field set never declared (no slot
//     holds it, and reading it would answer another slot's state).
//
// The scan never enters a NESTED function: a closure runs at a time this
// scan cannot place, so a field it touches is not this body's read or
// write. A receiver mention inside one is an ESCAPE — the closure carries
// the bundle out of the lowering's sight.
//
// Nothing here declines. A body that escapes its receiver and computes
// into it reports both flags with whatever reads and writes it also did;
// the consumer rules on what that is worth.
func FieldCensusOf(body *ast.Node, receiverName string, fields []BundleField) FieldCensus {
	census := FieldCensus{}
	if body == nil {
		return census
	}
	byName := map[string]BundleField{}
	for _, field := range fields {
		byName[field.Name] = field
	}
	readNames := map[string]struct{}{}
	writeNames := map[string]struct{}{}

	// isReceiver: does this node denote the object the census is about?
	// `this` answers under the name "this"; any other name answers as its
	// own identifier.
	isReceiver := func(node *ast.Node) bool {
		if node == nil {
			return false
		}
		if node.Kind == ast.KindThisKeyword {
			return receiverName == "this"
		}
		return ast.IsIdentifier(node) && node.Text() == receiverName
	}

	// consumed marks the nodes an outer form already accounted for, so a
	// `this` inside a recognized `this.x` is not counted a second time as
	// a bare mention.
	consumed := map[*ast.Node]struct{}{}

	// noteRead / noteWrite record a field once each, whatever the body's
	// order — the lists are rebuilt in declaration order at the end.
	noteRead := func(name string) bool {
		if _, declared := byName[name]; !declared {
			return false
		}
		readNames[name] = struct{}{}
		return true
	}
	noteWrite := func(name string) bool {
		if _, declared := byName[name]; !declared {
			return false
		}
		writeNames[name] = struct{}{}
		return true
	}

	// fieldAccessOf: is this a plain `<receiver>.<name>` property access?
	// An optional step is not — `this?.x` admits an absent receiver, which
	// no slot spells, so it falls through to the escape rule.
	fieldAccessOf := func(node *ast.Node) (string, bool) {
		if node == nil || !ast.IsPropertyAccessExpression(node) {
			return "", false
		}
		access := node.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil {
			return "", false
		}
		if !isReceiver(Unwrapped(access.Expression)) {
			return "", false
		}
		if !ast.IsIdentifier(access.Name()) {
			return "", false
		}
		return access.Name().Text(), true
	}

	// writeTargetOf: the `<receiver>.<field>` a write form writes through,
	// if this node is one of the write forms.
	writeTargetOf := func(node *ast.Node) (target *ast.Node, alsoReads bool, ok bool) {
		if ast.IsBinaryExpression(node) {
			binary := node.AsBinaryExpression()
			operator := binary.OperatorToken.Kind
			if operator == ast.KindEqualsToken {
				return Unwrapped(binary.Left), false, true
			}
			if operator >= ast.KindFirstCompoundAssignment && operator <= ast.KindLastCompoundAssignment {
				// a compound write reads the old value before storing the new
				return Unwrapped(binary.Left), true, true
			}
			return nil, false, false
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				return Unwrapped(unary.Operand), true, true
			}
			return nil, false, false
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				return Unwrapped(unary.Operand), true, true
			}
			return nil, false, false
		}
		if ast.IsDeleteExpression(node) {
			return Unwrapped(node.AsDeleteExpression().Expression), false, true
		}
		return nil, false, false
	}

	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == nil {
			return false
		}
		if _, already := consumed[node]; already {
			// the outer form accounted for this node's receiver spelling; its
			// remaining children (a computed step's index, an assignment's
			// right side) still walk
			node.ForEachChild(visit)
			return false
		}
		// a nested function's body runs at a time this scan cannot place —
		// a receiver mentioned inside carries the bundle out of sight.
		//
		// `this` is the one spelling that does not always cross: only an
		// ARROW keeps the enclosing `this`, while a function expression, a
		// function declaration, a method, and a class body all rebind it, so
		// a `this` inside one of those denotes some other object entirely
		// and is none of this bundle's business. A NAMED receiver crosses
		// into every nested form — a closure over `wrapper` is still that
		// wrapper.
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			rebindsThis := !ast.IsArrowFunction(node)
			if receiverName == "this" && rebindsThis {
				return false
			}
			if mentionsReceiver(node, receiverName) {
				census.Escapes = true
			}
			return false
		}
		// the WRITE forms, before the read rule: an assignment target is a
		// write, not a read of the slot it stores into
		if target, alsoReads, isWrite := writeTargetOf(node); isWrite && target != nil {
			if name, isField := fieldAccessOf(target); isField {
				if noteWrite(name) {
					if alsoReads {
						noteRead(name)
					}
				} else {
					// a member the field set never declared: no slot holds it, and
					// writing it moves state the census cannot name
					census.Escapes = true
				}
				consumed[target] = struct{}{}
				consumeReceiver(consumed, target)
			} else if ast.IsElementAccessExpression(target) &&
				isReceiver(Unwrapped(target.AsElementAccessExpression().Expression)) {
				// `this[k] = v`: the declaration bounds which slots could move,
				// but nothing names which one did
				census.Computed = true
				consumeReceiver(consumed, target)
			}
			node.ForEachChild(visit)
			return false
		}
		// a COMPUTED member read on the receiver
		if ast.IsElementAccessExpression(node) {
			access := node.AsElementAccessExpression()
			if isReceiver(Unwrapped(access.Expression)) {
				census.Computed = true
				consumeReceiver(consumed, node)
				// the index expression still walks — it may mention the receiver
				// itself (`this[this.key]`), which is its own occurrence
				visit(access.ArgumentExpression)
				return false
			}
		}
		// a CALL whose callee is `<receiver>.<name>`: the name is a METHOD,
		// which resolves through ContractBySymbol carrying its own summary,
		// not a slot. So an undeclared name in callee position is not a
		// missing slot and does not escape — but a DECLARED field called as
		// a function (`this.handler()`) is a genuine read of that field.
		// Only the callee step is accounted for here; the arguments walk on.
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			if call.QuestionDotToken == nil {
				if name, isField := fieldAccessOf(Unwrapped(call.Expression)); isField {
					noteRead(name)
					consumeReceiver(consumed, Unwrapped(call.Expression))
					consumed[call.Expression] = struct{}{}
					for _, argument := range call.Arguments.Nodes {
						visit(argument)
					}
					return false
				}
			}
		}
		// a plain `<receiver>.<field>` READ
		if name, isField := fieldAccessOf(node); isField {
			if !noteRead(name) {
				// the field set never declared this member — reading it would
				// answer some other slot's state
				census.Escapes = true
			}
			consumeReceiver(consumed, node)
			return false
		}
		// a bare mention of the receiver in any other position — an
		// argument, a return value, an alias, an optional chain, the object
		// of an undeclared step.
		//
		// A property NAME spelled like the receiver is not a mention: the
		// `wrapper` in `holder.wrapper` is a step, not the object.
		if isReceiver(node) && !isPropertyStepName(node) {
			census.Escapes = true
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)

	// the lists come back in the field set's own declaration order, never
	// in the body's mention order — that is what lets the layout and the
	// call sites build the same slot vector from the same census
	for _, field := range fields {
		if _, read := readNames[field.Name]; read {
			census.Reads = append(census.Reads, field)
		}
	}
	for _, field := range fields {
		if _, written := writeNames[field.Name]; written {
			census.Writes = append(census.Writes, field)
		}
	}
	return census
}

// consumeReceiver marks the receiver spelling inside a recognized access
// as accounted for, so the walk does not count it again as a bare
// mention. It marks the access's own receiver expression and everything
// the parens and casts around it wrap.
func consumeReceiver(consumed map[*ast.Node]struct{}, access *ast.Node) {
	var receiver *ast.Node
	if ast.IsPropertyAccessExpression(access) {
		receiver = access.AsPropertyAccessExpression().Expression
	} else if ast.IsElementAccessExpression(access) {
		receiver = access.AsElementAccessExpression().Expression
	}
	for receiver != nil {
		consumed[receiver] = struct{}{}
		unwrapped := Unwrapped(receiver)
		if unwrapped == receiver {
			return
		}
		receiver = unwrapped
	}
}

// isPropertyStepName answers whether an identifier is the NAME half of a
// property access — the `wrapper` in `holder.wrapper`. Such an identifier
// spells a step, never the object, so a receiver-shaped name in that
// position is nobody's mention of the receiver. Both walks in this file
// apply the rule, so it lives in one place.
func isPropertyStepName(node *ast.Node) bool {
	if node == nil || !ast.IsIdentifier(node) {
		return false
	}
	parent := node.Parent
	if parent == nil {
		return false
	}
	if ast.IsPropertyAccessExpression(parent) {
		return parent.AsPropertyAccessExpression().Name() == node
	}
	return false
}

// mentionsReceiver answers whether a subtree names the receiver at all —
// the test a nested function is judged by, where WHAT it does with the
// bundle is beyond this scan's reach and any mention is an escape.
func mentionsReceiver(node *ast.Node, receiverName string) bool {
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found || child == nil {
			return true
		}
		if child.Kind == ast.KindThisKeyword && receiverName == "this" {
			found = true
			return true
		}
		if ast.IsIdentifier(child) && child.Text() == receiverName && !isPropertyStepName(child) {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	node.ForEachChild(visit)
	return found
}
