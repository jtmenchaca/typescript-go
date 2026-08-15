// split from ir_field_bundles.go — the declared field set

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
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

// checkerOf is the census's nil-tolerant reach for the checker: the
// field readings run under ctx-less callers (a lowering with no program,
// the syntax-only tests), and those decline every symbol key rather than
// crashing.
func checkerOf(ctx *FlowContext) *checker.Checker {
	if ctx == nil || ctx.P == nil {
		return nil
	}
	return ctx.P.Checker
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
//     the slot, UNLESS the key is a STABLE SYMBOL const
//     (`private readonly [INSTANCE_ID_SYMBOL]: string`), which spells the
//     derived `#sym:INSTANCE_ID_SYMBOL` name and contributes like any
//     other field. Note this is the DECLARATION being computed; a
//     computed ACCESS in a body is FieldCensus.Computed's business.
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
		var text string
		switch {
		case name != nil && (ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)):
			text = name.Text()
		default:
			// A computed name spells a slot only through a stable symbol
			// const; every other computed key names nothing the vector holds.
			//
			// THE PRIVACY SPLIT, and why it does not gate the slot. A symbol
			// property is reachable only by code holding the symbol VALUE,
			// so an UNEXPORTED module-level const keeps the field inside the
			// module's own text — runtime privacy the `private` modifier
			// never had, since the modifier is erased. An EXPORTED const
			// (nest exports INSTANCE_ID_SYMBOL and INSTANCE_METADATA_SYMBOL)
			// hands the key to every importer, and any of them can write the
			// field. ExportedSymbolConst (class_field_invariants.go) is the
			// reading of that.
			//
			// Both sides get a slot here, because that is what the escape
			// rules already do for a PUBLIC field: a public field is
			// writable by every holder of the instance and still takes an
			// entry, with the census's own escape and havoc reports — not
			// the field's declared visibility — deciding when the slot stops
			// being believed. An exported symbol field is open in exactly
			// that way and takes exactly that treatment; an unexported one
			// is strictly tighter. Nothing here claims more for either.
			symbolName, isSymbolKey := symbolMemberFieldName(checkerOf(ctx), name)
			if !isSymbolKey {
				continue
			}
			text = symbolName
		}
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
//     spells nothing), PLUS everything it inherits (see below). TYPE
//     PARAMETERS still decline: a type parameter means the members'
//     annotations are not the ones the instance actually holds;
//   - a TYPE ALIAS of a type literal → the literal's members. An alias of
//     anything else (a union, another reference, a mapped type) declines
//     — the census reads syntax, and only a literal spells its members.
//
// HERITAGE EXPANDS. `interface Wrapper extends Base { … }` reads Base's
// members too: each heritage clause's parent references resolve through
// the same symbolAt this function already uses, each parent's own
// members read under the SAME field rules, recursively (a parent's own
// heritage walks too), and the CHILD's members SHADOW a parent's on a
// name collision, keeping the slot position the parent's list already
// gave — TypeScript's own rule for a redeclared inherited property, and
// the position rule is what keeps the slot vector from moving because a
// child restated a member.
//
// Every decline holds at EVERY LINK of the chain: type parameters on any
// declaration, a parent reference carrying type arguments or spelled by
// anything but a plain identifier, a parent that is neither a plain
// interface nor an alias of a type literal, and a symbol MORE THAN ONE
// of whose declarations contributes members — a merged interface splits
// its member list across declarations, so reading one of them builds a
// field set the other contradicts. A CYCLE in the chain (illegal TS, but
// not assumed pre-checked) declines rather than looping: the walk
// carries the declaration nodes already on its own path and refuses to
// re-enter one.
//
// A MEMBERLESS child with heritage (`interface W extends Base {}`) takes
// its parents' fields — it declares nothing of its own and is exactly
// what it inherits.
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
	// the CLASS arm keeps its own resolution: a class-typed annotation and
	// a method's own `this` read one field set, and classes carry their own
	// heritage semantics outside this walk
	for _, declaration := range symbol.Declarations {
		if ast.IsClassLike(declaration) {
			return ClassFieldsOf(ctx, declaration)
		}
	}
	return declaredBundleFieldsOf(ctx, symbol.Declarations, nil)
}
