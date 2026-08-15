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

/* ── the stable symbol key ───────────────────────────────────────── */

// symbolFieldPrefix opens the name a SYMBOL-KEYED field is spelled
// under. `#` is the same character the base layout's own "#done"/"#ret"
// and the accessor temps' "#get." wear, and the `:` after the tag is
// what keeps this apart from a PRIVATE IDENTIFIER: `this.#id` spells its
// field "#id", with no separator, and no JavaScript private name may
// hold a colon. So "#sym:INSTANCE_ID_SYMBOL" collides with neither a
// plain property name nor a `#`-named one.
const symbolFieldPrefix = "#sym:"

// StableSymbolKeyName is the field name behind a SYMBOL-KEYED member
// access — `this[INSTANCE_ID_SYMBOL]`, `originalRef[K_MODULE_ID]` — or
// (false) where the key is not one this reading calls stable.
//
// THE KEY IDENTITY. A key qualifies when it is a plain identifier whose
// binding is a MODULE-LEVEL `const` initialized by a `Symbol(...)` or
// `Symbol.for(...)` call on the default library's Symbol. Two things
// follow from that shape, and both are what a field name needs:
//
//   - the const cannot be rebound, and its declaration runs ONCE per
//     module, so every evaluation of the name in the program reads the
//     one symbol value the module built. `Symbol.for` is stable for a
//     second reason on top (the registry hands the same symbol back per
//     key, sec-symbol.for), but `Symbol()` needs no second reason: the
//     single evaluation is the whole argument.
//   - two accesses spelled with the SAME const are the same field, and
//     accesses spelled with different consts are different fields —
//     because the symbol is the property key at runtime, and distinct
//     symbols are distinct keys however they are described.
//
// The identity carried is the const's own SYMBOL (the checker's, through
// import aliases), so an imported `INSTANCE_ID_SYMBOL` and the exporting
// file's own spelling of it name one field. The NAME is derived from the
// const's declared identifier, which is unique within any one scope, so
// no two distinct consts a class body can both see spell one field name.
//
// WHAT STAYS OUT, each because the shape does not hold the value fixed:
// a `let`/`var`/parameter binding (rebindable), a const initialized by
// anything but a Symbol construction (an imported value the reading has
// not followed, a call whose result varies), a const declared inside a
// function or block (a fresh symbol per entry, so two accesses in two
// activations are two different keys), a qualified or computed key
// expression, and a WELL-KNOWN symbol (`Symbol.iterator`), which is a
// property access rather than an identifier and never reaches here.
//
// A nil checker declines everything — the census's ctx-less callers get
// exactly the behaviour they had before this reading existed.
func StableSymbolKeyName(c *checker.Checker, key *ast.Node) (string, bool) {
	if c == nil || key == nil {
		return "", false
	}
	key = Unwrapped(key)
	if key == nil || !ast.IsIdentifier(key) {
		return "", false
	}
	symbol := symbolAt(c, key)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return "", false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return "", false
	}
	name := declaration.AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		// a binding pattern spells no single const name to derive from
		return "", false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) {
		return "", false
	}
	if (list.Flags & ast.NodeFlagsConst) == 0 {
		return "", false
	}
	// MODULE LEVEL: the declaration statement's own parent is the source
	// file. A const inside a function or a block re-runs its initializer
	// per entry, so `Symbol('x')` there is a fresh key each time and two
	// accesses need not name one field.
	statement := list.Parent
	if statement == nil || !ast.IsVariableStatement(statement) {
		return "", false
	}
	if statement.Parent == nil || !ast.IsSourceFile(statement.Parent) {
		return "", false
	}
	initializer := Unwrapped(declaration.AsVariableDeclaration().Initializer)
	if initializer == nil || !symbolConstructionCall(c, initializer) {
		return "", false
	}
	return symbolFieldPrefix + name.Text(), true
}

// symbolConstructionCall is whether an expression BUILDS a symbol:
// `Symbol()`, `Symbol(description)`, or `Symbol.for(key)`, with the
// `Symbol` name resolving to the default library so a local shadow named
// Symbol is not mistaken for the builtin. It is readSymbolBuiltin's
// recognition (symbol_builtin_models.go) without the value reading — the
// key identity needs to know a symbol was constructed, not which one.
func symbolConstructionCall(c *checker.Checker, e *ast.Node) bool {
	if !ast.IsCallExpression(e) {
		return false
	}
	call := e.AsCallExpression()
	if call.QuestionDotToken != nil {
		return false
	}
	if ast.IsPropertyAccessExpression(call.Expression) {
		access := call.Expression.AsPropertyAccessExpression()
		return access.QuestionDotToken == nil &&
			ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Symbol" &&
			ast.IsIdentifier(access.Name()) && access.Name().Text() == "for" &&
			c.SymbolInDefaultLib(c.GetSymbolAtLocation(access.Expression))
	}
	return ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Symbol" &&
		c.SymbolInDefaultLib(c.GetSymbolAtLocation(call.Expression))
}

// SymbolKeyedFieldName reads the field name behind a whole ELEMENT
// ACCESS whose receiver is the given object — `this[S]`, `wrapper[S]` —
// answering the derived `#sym:` name. An optional step declines: `this?.[S]`
// admits an absent receiver, which no slot spells.
func SymbolKeyedFieldName(c *checker.Checker, access *ast.Node) (string, bool) {
	if access == nil || !ast.IsElementAccessExpression(access) {
		return "", false
	}
	element := access.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		return "", false
	}
	return StableSymbolKeyName(c, element.ArgumentExpression)
}

// symbolMemberFieldName reads the field name behind a class MEMBER's
// computed name — the `[INSTANCE_ID_SYMBOL]` of
// `private readonly [INSTANCE_ID_SYMBOL]: string`. Same key identity,
// read off a declaration rather than an access, so the declaration and
// every access spell one field.
func symbolMemberFieldName(c *checker.Checker, name *ast.Node) (string, bool) {
	if name == nil || !ast.IsComputedPropertyName(name) {
		return "", false
	}
	return StableSymbolKeyName(c, name.AsComputedPropertyName().Expression)
}

// symbolKeyedMethodOf is the class METHOD declared under a stable symbol
// key — the `[S]() { … }` of a class that also spells `this[S](…)` in one
// of its bodies — or nil.
//
// WHY A SYMBOL-KEYED METHOD IS A METHOD. The key identity
// (StableSymbolKeyName) is what makes `#sym:S` name ONE thing: the const
// cannot be rebound, its declaration runs once per module, and the symbol
// IS the property key at runtime. That argument says nothing about
// whether the member holding the key is a field or a method — it settles
// the KEY, and the member kind is then read off the declaration exactly
// as a dotted name's is. So a symbol-keyed method resolves to one body
// the write-set closure can walk, which is the whole of what the closure
// needs from a method name.
//
// Only a method WITH A BODY answers: an overload signature, an accessor,
// and an arrow-valued property declaration each hold writes this reading
// cannot enumerate, and the closure's own (nil, false) is what they get.
// A STATIC member never answers — it belongs to the constructor object,
// not to any instance, so no receiver's call reaches it.
func symbolKeyedMethodOf(c *checker.Checker, classLike *ast.Node, name string) *ast.Node {
	if c == nil || classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if !ast.IsMethodDeclaration(member) || member.Body() == nil {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		spelled, isSymbolKey := symbolMemberFieldName(c, member.Name())
		if isSymbolKey && spelled == name {
			return member
		}
	}
	return nil
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
// Computed is any `receiver[e]` element access on the receiver, read or
// written: the expression names no field.
//
// ComputedWrite is the half of that which MOVES state — `this[k] = v`,
// `this[k]++`, `delete this[k]`. The two are separate because they cost
// different things. A computed READ names no field but changes nothing,
// so every slot keeps its value and the bundle is worth exactly what it
// was. A computed STORE moves a slot nothing names, so no slot of this
// receiver can be believed past it; the DECLARATION still bounds the
// set, so a consumer can havoc every slot of this one object rather than
// declining outright.
//
// Escapes is any occurrence of the receiver the lowering cannot follow:
// a bare mention (`f(this)`, `return wrapper`, `xs.push(this)`), an
// alias (`const w = wrapper`), an optional-chained or non-declared
// member. A bundle that escapes may be written through a name the census
// never saw, so its slots cannot be trusted past that point.
type FieldCensus struct {
	Reads         []BundleField
	Writes        []BundleField
	Computed      bool
	ComputedWrite bool
	Escapes       bool
	// CapturedMethodCalls: the receiver METHODS a nested closure calls
	// (`xs.forEach(x => this.insert(x))`). Such a capture is not an
	// escape by itself — the closure may run at any later time, so the
	// consumer must treat every field those methods can write
	// (transitively) as movable at EVERY call statement in the body,
	// and refuse the bundle when that write set cannot be computed.
	// Deduplicated, in first-mention order.
	CapturedMethodCalls []string
	// DirectMethodCalls: the receiver methods the body calls in plain
	// statement position (`this.register(x)`, and `this[S](…)` where the
	// class declares S as a method, under its `#sym:` spelling). These
	// lower as real call statements and need nothing from the census —
	// the field exists for CaptureWriteSet's closure, where a captured
	// method's own direct calls carry its transitive writes.
	DirectMethodCalls []string
	// ReturnsSelf: the body ends `return this` (this-receivers only —
	// the fluent-builder shape). Not an escape: nothing moves during
	// the body. The CALLER gains an alias it may write through later,
	// so the serving seams must forget the caller's receiver knowledge
	// (the direct route) or decline the composed call (the statement
	// route) — LoweredSummary.ReturnsReceiver carries the requirement.
	ReturnsSelf bool
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
//     is this receiver AND whose key is not a stable symbol const. When
//     that access is a STORE position rather than a read, ComputedWrite
//     is set too: a read names no field and moves nothing, while a store
//     moves a slot nothing names.
//   - a SYMBOL-KEYED access (`this[INSTANCE_ID_SYMBOL]`) is NOT computed:
//     the key identity names one field, so the access reads or writes the
//     `#sym:` field exactly as a dotted step reads or writes its own. It
//     needs a checker to resolve the const, so it is FieldCensusWith's
//     arm and the checker-less FieldCensusOf keeps reporting Computed.
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
	return FieldCensusWith(nil, body, receiverName, fields)
}

// FieldCensusWith is FieldCensusOf carrying the checker the STABLE
// SYMBOL KEY needs. With a checker, `<receiver>[S]` where S resolves to
// a module-level Symbol const reads and writes the `#sym:S` field rather
// than reporting a computed move; with a nil checker every element
// access on the receiver is computed, which is the reading every caller
// had before the symbol key existed.
func FieldCensusWith(c *checker.Checker, body *ast.Node, receiverName string, fields []BundleField) FieldCensus {
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

	// noteDirectMethodCall records a receiver method the body calls in
	// plain statement position, once, in first-mention order. The name is
	// a dotted step's own identifier or a symbol-keyed member's `#sym:`
	// spelling — the write-set closure resolves both back to one
	// declaration, so one list holds them.
	noteDirectMethodCall := func(name string) {
		for _, held := range census.DirectMethodCalls {
			if held == name {
				return
			}
		}
		census.DirectMethodCalls = append(census.DirectMethodCalls, name)
	}

	// fieldAccessOf: is this a plain `<receiver>.<name>` property access,
	// or a `<receiver>[S]` access under a STABLE SYMBOL const? Both name
	// exactly one field, so both answer here and every read, write, and
	// callee rule below treats them alike.
	//
	// An optional step is neither — `this?.x` admits an absent receiver,
	// which no slot spells, so it falls through to the escape rule.
	fieldAccessOf := func(node *ast.Node) (string, bool) {
		if node == nil {
			return "", false
		}
		if ast.IsElementAccessExpression(node) {
			if !isReceiver(Unwrapped(node.AsElementAccessExpression().Expression)) {
				return "", false
			}
			// the symbol const IS the property key at runtime, so the access
			// names one field the way a dotted step does
			return SymbolKeyedFieldName(c, node)
		}
		if !ast.IsPropertyAccessExpression(node) {
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

	// noteStoreTarget records ONE store position. A store into
	// `<receiver>.<field>` is a write; a store into `<receiver>[e]` moves a
	// field nothing names; a store through any other shape that MENTIONS
	// the receiver puts it somewhere the census cannot follow.
	//
	// It answers whether the target was accounted for, so the caller knows
	// not to walk it again as an ordinary expression — a store position
	// visited as an expression reads as a plain READ, which is the exact
	// wrong answer: the slot would keep its entry value past the store.
	noteStoreTarget := func(target *ast.Node, alsoReads bool) bool {
		target = Unwrapped(target)
		if target == nil {
			return false
		}
		if name, isField := fieldAccessOf(target); isField {
			switch {
			case noteWrite(name):
				if alsoReads {
					noteRead(name)
				}
			case ast.IsElementAccessExpression(target):
				// a SYMBOL-KEYED store whose field the set never declared —
				// the class stores under a symbol it declares no member for.
				// The key names ONE field, so nothing outside this receiver
				// moves, and the declaration still bounds the slots: the
				// bounded havoc ComputedWrite already stands for is the honest
				// report, not the whole-body escape an unnamed member gets.
				census.Computed = true
				census.ComputedWrite = true
			default:
				// a member the field set never declared: no slot holds it, and
				// writing it moves state the census cannot name
				census.Escapes = true
			}
			consumed[target] = struct{}{}
			consumeReceiver(consumed, target)
			return true
		}
		if ast.IsElementAccessExpression(target) &&
			isReceiver(Unwrapped(target.AsElementAccessExpression().Expression)) {
			// `this[k] = v`: the declaration bounds which slots could move,
			// but nothing names which one did — so no slot of this receiver
			// can be believed past this point
			census.Computed = true
			census.ComputedWrite = true
			consumeReceiver(consumed, target)
			return true
		}
		return false
	}

	// storePattern walks a DESTRUCTURING target — the `{ x: this.count }`
	// of `({ x: this.count } = source)`, the `[this.count]` of
	// `[this.count] = pair`, and the same shapes nested inside each other.
	//
	// Every leaf of such a pattern is a STORE position, not a read. The
	// walk descends through the pattern's own structure and hands each
	// leaf to noteStoreTarget; a leaf that is not a receiver access at all
	// (a plain local, another object's field) is nobody's business here,
	// and its own subexpressions — a computed key, a default's right side
	// — are ordinary expressions and walk normally.
	//
	// Declared here and assigned below because it and `visit` call each
	// other: a default value inside a pattern is an ordinary expression.
	var visit func(node *ast.Node) bool
	var storePattern func(target *ast.Node)
	storePattern = func(target *ast.Node) {
		target = Unwrapped(target)
		if target == nil {
			return
		}
		switch {
		case ast.IsObjectLiteralExpression(target):
			for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
				switch {
				case ast.IsPropertyAssignment(property):
					assignment := property.AsPropertyAssignment()
					// a computed key is an ordinary expression evaluated in place
					if name := assignment.Name(); name != nil && ast.IsComputedPropertyName(name) {
						visit(name.AsComputedPropertyName().Expression)
					}
					storePattern(assignment.Initializer)
				case ast.IsShorthandPropertyAssignment(property):
					// `({ count } = source)` stores into a LOCAL named count, never
					// into the receiver — but its default value is an expression
					if initializer := property.AsShorthandPropertyAssignment().ObjectAssignmentInitializer; initializer != nil {
						visit(initializer)
					}
				case ast.IsSpreadAssignment(property):
					storePattern(property.AsSpreadAssignment().Expression)
				default:
					visit(property)
				}
			}
		case ast.IsArrayLiteralExpression(target):
			for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
				if ast.IsSpreadElement(element) {
					storePattern(element.AsSpreadElement().Expression)
					continue
				}
				storePattern(element)
			}
		case ast.IsBinaryExpression(target) &&
			target.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken:
			// a DEFAULTED element: `{ x: this.count = 1 }` stores into
			// this.count when the source has no x, and evaluates 1 as an
			// ordinary expression
			binary := target.AsBinaryExpression()
			storePattern(binary.Left)
			visit(binary.Right)
		default:
			if noteStoreTarget(target, false) {
				return
			}
			// not a receiver access: a plain local, another object's field.
			// It still walks as an expression so a receiver mentioned inside
			// it (`other[this.key] = v`) is counted.
			visit(target)
		}
	}

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
		// a receiver mentioned inside carries the bundle out of sight,
		// UNLESS every mention is a plain READ of a declared field: a read
		// moves nothing, so it may interleave at any later time without
		// invalidating any belief the body holds. Those reads are recorded
		// as the body's own; anything else inside — a write, a method
		// call (whose body may write), a bare mention, a computed access —
		// keeps the escape.
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
			if !mentionsReceiver(node, receiverName) {
				return false
			}
			if reads, called, admissible := captureMentions(c, node, isReceiver, byName); admissible {
				for _, name := range reads {
					noteRead(name)
				}
				for _, method := range called {
					already := false
					for _, held := range census.CapturedMethodCalls {
						if held == method {
							already = true
							break
						}
					}
					if !already {
						census.CapturedMethodCalls = append(census.CapturedMethodCalls, method)
					}
				}
				return false
			}
			census.Escapes = true
			return false
		}
		// `const { a, b } = this` — a destructuring READ of declared
		// fields, wearing a pattern. Each plain element is the read of the
		// field it names; a default, a rest, a computed key, or a nested
		// pattern keeps the escape (the pattern reads shapes no slot
		// spells).
		if ast.IsVariableDeclaration(node) {
			d := node.AsVariableDeclaration()
			if d.Initializer != nil && isReceiver(Unwrapped(d.Initializer)) &&
				d.Name() != nil && ast.IsObjectBindingPattern(d.Name()) {
				for _, element := range d.Name().AsBindingPattern().Elements.Nodes {
					binding := element.AsBindingElement()
					if binding.DotDotDotToken != nil || binding.Initializer != nil {
						census.Escapes = true
						return false
					}
					if !ast.IsIdentifier(binding.Name()) {
						census.Escapes = true
						return false
					}
					read := binding.Name().Text()
					if binding.PropertyName != nil {
						if !ast.IsIdentifier(binding.PropertyName) {
							census.Escapes = true
							return false
						}
						read = binding.PropertyName.Text()
					}
					if !noteRead(read) {
						// the pattern reads a member the field set never
						// declared — no slot answers it
						census.Escapes = true
						return false
					}
				}
				consumed[Unwrapped(d.Initializer)] = struct{}{}
				consumed[d.Initializer] = struct{}{}
				return false
			}
		}
		// a LOOP BINDING through an expression: `for (this.count of xs)` and
		// `for (this.count in o)` store into their target once per
		// iteration. The target is not an assignment expression, so the
		// write forms below never see it — without this the store reads as
		// a plain access and the slot survives the loop unchanged.
		//
		// A for-of/for-in whose initializer is a DECLARATION binds fresh
		// names and stores into nothing the bundle holds; only the
		// expression form can name a field.
		if node.Kind == ast.KindForOfStatement || node.Kind == ast.KindForInStatement {
			loop := node.AsForInOrOfStatement()
			if loop.Initializer != nil && !ast.IsVariableDeclarationList(loop.Initializer) {
				storePattern(loop.Initializer)
			} else {
				visit(loop.Initializer)
			}
			visit(loop.Expression)
			visit(loop.Statement)
			return false
		}
		// the WRITE forms, before the read rule: an assignment target is a
		// write, not a read of the slot it stores into
		if target, alsoReads, isWrite := writeTargetOf(node); isWrite && target != nil {
			// a DESTRUCTURING target is a pattern of store positions, each of
			// which may name a field; a simple target is one store position
			if ast.IsObjectLiteralExpression(target) || ast.IsArrayLiteralExpression(target) {
				storePattern(target)
				// the source side is an ordinary expression
				if ast.IsBinaryExpression(node) {
					visit(node.AsBinaryExpression().Right)
				}
				return false
			}
			noteStoreTarget(target, alsoReads)
			node.ForEachChild(visit)
			return false
		}
		// a member read on the receiver spelled with brackets. A STABLE
		// SYMBOL const names one field, so it is the read of that field;
		// every other key names none, and the access is computed.
		if ast.IsElementAccessExpression(node) {
			access := node.AsElementAccessExpression()
			if isReceiver(Unwrapped(access.Expression)) {
				if name, isField := fieldAccessOf(node); isField {
					if !noteRead(name) {
						// the class stores under this symbol without declaring a
						// member for it — the key still names ONE field, so the
						// bounded computed reading is what the access is worth
						census.Computed = true
					}
				} else {
					census.Computed = true
				}
				consumeReceiver(consumed, node)
				// the index expression still walks — it may mention the receiver
				// itself (`this[this.key]`), which is its own occurrence
				visit(access.ArgumentExpression)
				return false
			}
		}
		// `return this` — the fluent-builder tail. Nothing moves during
		// the body; the serving seams carry the caller-alias requirement
		// (ReturnsSelf's doc). This-receivers only: a named receiver
		// returned would need the parameter-bundle seams taught the same
		// forgetting, which they are not.
		if ast.IsReturnStatement(node) {
			returned := node.AsReturnStatement().Expression
			if returned != nil && Unwrapped(returned).Kind == ast.KindThisKeyword && receiverName == "this" {
				census.ReturnsSelf = true
				consumed[returned] = struct{}{}
				consumed[Unwrapped(returned)] = struct{}{}
				return false
			}
		}
		// `Object.assign(this, source)` — a store into fields nothing
		// names: the computed store's own shape. Every field may have
		// moved, the declaration bounds the set, and ComputedWrite's
		// all-field havoc bracketing stands for it — not an escape. The
		// source arguments walk on as ordinary expressions.
		if ast.IsCallExpression(node) {
			assignCall := node.AsCallExpression()
			if assignCall.QuestionDotToken == nil && ast.IsPropertyAccessExpression(assignCall.Expression) {
				assignAccess := assignCall.Expression.AsPropertyAccessExpression()
				if assignAccess.QuestionDotToken == nil &&
					ast.IsIdentifier(assignAccess.Expression) && assignAccess.Expression.Text() == "Object" &&
					ast.IsIdentifier(assignAccess.Name()) && assignAccess.Name().Text() == "assign" &&
					assignCall.Arguments != nil && len(assignCall.Arguments.Nodes) > 0 &&
					isReceiver(Unwrapped(assignCall.Arguments.Nodes[0])) {
					census.Computed = true
					census.ComputedWrite = true
					consumed[assignCall.Arguments.Nodes[0]] = struct{}{}
					consumed[Unwrapped(assignCall.Arguments.Nodes[0])] = struct{}{}
					for _, argument := range assignCall.Arguments.Nodes[1:] {
						visit(argument)
					}
					return false
				}
			}
		}
		// `this.onData.bind(this)` — a DEFERRED method call: the bound
		// function may run at any later time, exactly like a closure
		// calling the method, and is collected the same way. The shape
		// is exact: callee `<receiver>.<m>.bind`, first argument the
		// receiver itself; anything looser falls through to the rules
		// below.
		if method, isBind := receiverMethodBindOf(node, isReceiver); isBind {
			already := false
			for _, held := range census.CapturedMethodCalls {
				if held == method {
					already = true
					break
				}
			}
			if !already {
				census.CapturedMethodCalls = append(census.CapturedMethodCalls, method)
			}
			consumeBindMentions(consumed, node)
			// partial-application arguments past the bound receiver are
			// ordinary expressions and may mention the receiver themselves
			for _, argument := range node.AsCallExpression().Arguments.Nodes[1:] {
				visit(argument)
			}
			return false
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
				callee := Unwrapped(call.Expression)
				if name, isField := fieldAccessOf(callee); isField {
					// the callee names a FIELD or a METHOD, and BOTH join the
					// call list. A method resolves through its declaration; a
					// declared field called as a function is a read of that
					// field AND a call through whatever the class stored in
					// it, whose body may write fields of its own — the
					// write-set closure resolves either from the class's own
					// text (fieldValuedFunctionBodies).
					//
					// A `#sym:` spelling from the element-access arm lands
					// here on the same terms a dotted name does: the stable
					// key names one member, and nothing about the bracket
					// makes it less placeable than a dot.
					noteRead(name)
					noteDirectMethodCall(name)
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

// Believable answers whether a body's slots for this bundle may be
// trusted at all. Two reports kill it, for one reason: the body moved
// the object through something no slot names.
//
//   - ESCAPES — the receiver reached code the scan cannot follow, which
//     may have stored through it under a name never seen.
//   - COMPUTED WRITE — `this[k] = v` stored into a field the expression
//     does not name, so every slot of this receiver is suspect.
//
// A computed READ is not here: naming no field costs precision on that
// one access and moves nothing.
//
// Every consumer that decides whether to expand a bundle reads THIS,
// not the flags directly — the layout and the two call sites have to
// agree about which bodies expand, and three spellings of one condition
// is three chances to drift.
func (census FieldCensus) Believable() bool {
	// a method-calling capture is not believable HERE: the consumer that
	// can compute the captured methods' transitive write set (and havoc
	// those fields at every call statement) admits it through its own
	// gate — every consumer without that machinery refuses, exactly as
	// it refused when the shape was an escape.
	return !census.Escapes && !census.ComputedWrite && len(census.CapturedMethodCalls) == 0
}

// CaptureWriteSet answers every field the collected capture-called
// methods can WRITE, transitively through the class's own methods — the
// havoc set a consumer applies at every call statement when it admits a
// method-calling capture.
//
// The closure is over the class's own declared members. Each visited
// method's census must itself be tame: no escape, no computed write;
// its Writes accumulate, and its own captured and direct method calls
// join the worklist. Any name the class does not declare at all (an
// inherited method, an accessor, an overload signature) makes the whole
// set incomputable and the answer is (nil, false) — the caller then
// keeps the escape.
//
// A name that declares a FIELD rather than a method is a call through a
// stored function value, and it answers here too. The class's own
// assignments to that field are the only sources of what it holds
// (a field the census let out would have escaped already), so:
//
//   - every assignment a function LITERAL this walk can read — the
//     initializer arrow, a constructor-assigned arrow — contributes its
//     body's write set, and the field's call moves their UNION;
//   - any assignment it cannot read (an imported handler, a parameter, a
//     call's result) leaves the stored body unknown, and the answer is
//     EVERY field of the class with ok true — the declaration bounds
//     which fields exist, so whole-bundle havoc says exactly what is
//     true. It is strictly stronger than refusing: refusing costs the
//     class every field READING as well, and no reading is wrong here.
//
// A worklist name may be a plain identifier or a `#sym:` spelling. The
// second resolves through symbolKeyedMethodOf: a computed name is not
// unreadable when its key is a STABLE SYMBOL const, because that key
// names one declaration, and one declaration is all this closure needs
// to walk a body. Only a computed name whose key is NOT stable is
// unreadable, and no such name ever reaches this worklist.
func CaptureWriteSet(classLike *ast.Node, fields []BundleField, methods []string) ([]BundleField, bool) {
	return CaptureWriteSetWith(nil, classLike, fields, methods)
}

// CaptureWriteSetWith is CaptureWriteSet carrying the checker the stable
// symbol key needs, so a captured method writing `this[S]` contributes
// that field to the havoc set instead of making the whole set
// incomputable through a computed write.
func CaptureWriteSetWith(c *checker.Checker, classLike *ast.Node, fields []BundleField, methods []string) ([]BundleField, bool) {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, false
	}
	spelled := BundleFieldsAs("this", fields)
	written := map[string]struct{}{}
	seen := map[string]struct{}{}
	worklist := append([]string{}, methods...)
	for len(worklist) > 0 {
		name := worklist[0]
		worklist = worklist[1:]
		if _, visited := seen[name]; visited {
			continue
		}
		seen[name] = struct{}{}
		var body *ast.Node
		for _, member := range classLike.ClassLikeData().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			memberName := member.Name()
			if memberName == nil || !ast.IsIdentifier(memberName) || memberName.Text() != name {
				continue
			}
			body = member.Body()
			break
		}
		if body == nil {
			// a SYMBOL-KEYED method (`[S]() { … }`), named on the worklist
			// under its `#sym:` spelling. The key identity resolves it to
			// one declaration the same way it resolves an access, so the
			// closure walks its body exactly as it walks a dotted method's.
			if method := symbolKeyedMethodOf(c, classLike, name); method != nil {
				body = method.Body()
			}
		}
		if body == nil {
			// the name declares no METHOD — but a class field can HOLD a
			// function (`private readonly handler = (x: number) => { … }`,
			// or a constructor-assigned one), and `this.handler()` calls
			// through that stored value. The class's own assignments to the
			// field are the only sources of what it holds, so where every one
			// of them is a function literal this walk can read, the union of
			// their bodies' write sets is what the call can move.
			literals, everyAssignmentWalkable, isField := fieldValuedFunctionBodies(c, classLike, name)
			switch {
			case !isField:
				// an inherited method, an accessor, a bodyless overload — the
				// name resolves to no declaration of this class at all, so
				// nothing bounds its writes
				return nil, false
			case !everyAssignmentWalkable:
				// the field holds something this walk cannot read — an
				// imported handler, a constructor parameter, a value returned
				// by a call. The DECLARATION still bounds which fields exist,
				// so every field of the class joins the havoc set: the call
				// through the stored closure may write any of them, and none
				// of them may be believed past it. That is strictly stronger
				// than refusing the bundle, which costs the class every
				// READING too — a field this body reads before the call keeps
				// its answer either way, and refusing throws it away for
				// nothing.
				return append([]BundleField{}, fields...), true
			default:
				for _, literal := range literals {
					literalCensus := FieldCensusWith(c, literal, "this", spelled)
					if literalCensus.Escapes || literalCensus.ComputedWrite {
						return nil, false
					}
					for _, field := range literalCensus.Writes {
						written[field.Name] = struct{}{}
					}
					worklist = append(worklist, literalCensus.CapturedMethodCalls...)
					worklist = append(worklist, literalCensus.DirectMethodCalls...)
				}
				continue
			}
		}
		census := FieldCensusWith(c, body, "this", spelled)
		if census.Escapes || census.ComputedWrite {
			return nil, false
		}
		for _, field := range census.Writes {
			written[field.Name] = struct{}{}
		}
		worklist = append(worklist, census.CapturedMethodCalls...)
		worklist = append(worklist, census.DirectMethodCalls...)
	}
	out := make([]BundleField, 0, len(written))
	for _, field := range fields {
		if _, wrote := written[field.Name]; wrote {
			out = append(out, field)
		}
	}
	return out, true
}

// fieldValuedFunctionBodies answers what a call through a class FIELD
// holding a function can move: the BODIES of every function literal the
// class assigns to that field, and whether the class's assignments were
// ALL such literals.
//
// THE CLOSED-SET ARGUMENT, which is what makes the union sound. A field
// holds whatever was last stored into it, so the write set of a call
// through it is the union over the possible stored values. The class's
// OWN TEXT bounds that set: the field's initializer and every
// `this.<field> = …` in its members are the only stores, because a store
// from anywhere else needs the instance, and an instance the class let
// out is an ESCAPE the census already reported (Escapes kills the bundle
// before this walk runs). So enumerating the class's assignments
// enumerates the sources.
//
// WHAT COUNTS AS WALKABLE: an arrow function or a function expression
// with a body. Its body is then read by the caller with the SAME census
// machinery a method's body is read by — a stored closure writing
// `this.count` writes the field a method writing it writes, and the
// arrow keeps the enclosing `this`, which is the instance.
//
// A function-expression assignment is walkable on the same terms with
// one difference the caller does not have to know: `function () { … }`
// rebinds `this`, so a `this.count` inside it denotes some other
// receiver and the census reads nothing of this bundle from it. That is
// a body contributing no writes, not a body whose writes are unknown.
//
// Answers (bodies, everyAssignmentWalkable, isField). isField false means
// the name declares no field of this class either, so the caller keeps
// its refusal.
func fieldValuedFunctionBodies(
	c *checker.Checker,
	classLike *ast.Node,
	name string,
) (bodies []*ast.Node, everyAssignmentWalkable bool, isField bool) {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, false, false
	}
	members := classLike.ClassLikeData().Members.Nodes
	// the DECLARATION: a non-static property whose name spells this field,
	// dotted or under a stable symbol key
	var declared *ast.Node
	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		if fieldMemberName(c, member.Name()) != name {
			continue
		}
		declared = member
		break
	}
	if declared == nil {
		return nil, false, false
	}
	everyAssignmentWalkable = true
	note := func(value *ast.Node) {
		value = Unwrapped(value)
		if value == nil {
			everyAssignmentWalkable = false
			return
		}
		if !ast.IsArrowFunction(value) && !ast.IsFunctionExpression(value) {
			// an imported handler, a parameter, a call's result, another
			// field's value — this walk cannot say what body it holds
			everyAssignmentWalkable = false
			return
		}
		body := value.Body()
		if body == nil {
			everyAssignmentWalkable = false
			return
		}
		bodies = append(bodies, body)
	}
	if initializer := declared.AsPropertyDeclaration().Initializer; initializer != nil {
		note(initializer)
	}
	// every `this.<field> = …` the class's own members spell — the
	// constructor's assignment is the common one, a method re-assigning
	// the handler is the same kind of store, and a store inside another
	// field's initializer is one too. A member with neither a body nor an
	// initializer spells no assignment.
	for _, member := range members {
		scanned := member.Body()
		if scanned == nil && ast.IsPropertyDeclaration(member) {
			scanned = member.AsPropertyDeclaration().Initializer
		}
		if scanned == nil {
			continue
		}
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node == nil {
				return false
			}
			if ast.IsBinaryExpression(node) {
				binary := node.AsBinaryExpression()
				operator := binary.OperatorToken.Kind
				if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
					if target := Unwrapped(binary.Left); thisFieldStoreNameOf(c, target) == name {
						if operator == ast.KindEqualsToken {
							note(binary.Right)
						} else {
							// a COMPOUND store into a function-valued field
							// (`this.handler ||= f`) leaves a value this reading
							// cannot name
							everyAssignmentWalkable = false
						}
						visit(binary.Right)
						return false
					}
				}
			}
			node.ForEachChild(visit)
			return false
		}
		visit(scanned)
	}
	return bodies, everyAssignmentWalkable, true
}

// fieldMemberName spells a class member's name the way the field set
// spells it — a plain or private identifier under its own text, a
// computed name under its `#sym:` name when the key is a stable symbol
// const — or "" when the name spells no field.
func fieldMemberName(c *checker.Checker, name *ast.Node) string {
	if name == nil {
		return ""
	}
	if ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name) {
		return name.Text()
	}
	if spelled, isSymbolKey := symbolMemberFieldName(c, name); isSymbolKey {
		return spelled
	}
	return ""
}

// thisFieldStoreNameOf spells the field a `this.<name>` or `this[S]`
// STORE TARGET names — the two spellings the census reads a field under
// — or "" for anything else. Both spellings answer here so the
// assignment scan reads `this.handler = …` and `this[S] = …` as stores
// into one field. (thisFieldNameOf, kernel_summaries.go, is the same
// question over a slot's PATH STRING rather than over a target node.)
func thisFieldStoreNameOf(c *checker.Checker, node *ast.Node) string {
	if node == nil {
		return ""
	}
	if ast.IsElementAccessExpression(node) {
		element := node.AsElementAccessExpression()
		if Unwrapped(element.Expression) == nil ||
			Unwrapped(element.Expression).Kind != ast.KindThisKeyword {
			return ""
		}
		if spelled, isSymbolKey := SymbolKeyedFieldName(c, node); isSymbolKey {
			return spelled
		}
		return ""
	}
	if !ast.IsPropertyAccessExpression(node) {
		return ""
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return ""
	}
	receiver := Unwrapped(access.Expression)
	if receiver == nil || receiver.Kind != ast.KindThisKeyword {
		return ""
	}
	if !ast.IsIdentifier(access.Name()) && !ast.IsPrivateIdentifier(access.Name()) {
		return ""
	}
	return access.Name().Text()
}

// receiverMethodBindOf recognizes `<receiver>.<m>.bind(<receiver>)` —
// the deferred method call. The callee must be a plain two-step access
// ending in `bind`, no optional steps, and the FIRST argument must be
// the receiver itself; extra arguments (partial application) are
// allowed and walk as ordinary expressions.
func receiverMethodBindOf(node *ast.Node, isReceiver func(*ast.Node) bool) (string, bool) {
	if node == nil || !ast.IsCallExpression(node) {
		return "", false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil || call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return "", false
	}
	if !isReceiver(Unwrapped(call.Arguments.Nodes[0])) {
		return "", false
	}
	outer := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(outer) {
		return "", false
	}
	outerAccess := outer.AsPropertyAccessExpression()
	if outerAccess.QuestionDotToken != nil || !ast.IsIdentifier(outerAccess.Name()) ||
		outerAccess.Name().Text() != "bind" {
		return "", false
	}
	inner := Unwrapped(outerAccess.Expression)
	if !ast.IsPropertyAccessExpression(inner) {
		return "", false
	}
	innerAccess := inner.AsPropertyAccessExpression()
	if innerAccess.QuestionDotToken != nil || !ast.IsIdentifier(innerAccess.Name()) ||
		!isReceiver(Unwrapped(innerAccess.Expression)) {
		return "", false
	}
	return innerAccess.Name().Text(), true
}

// consumeBindMentions marks a recognized bind's receiver spellings as
// accounted for: the method access chain and the first argument.
func consumeBindMentions(consumed map[*ast.Node]struct{}, node *ast.Node) {
	call := node.AsCallExpression()
	consumed[call.Arguments.Nodes[0]] = struct{}{}
	consumed[Unwrapped(call.Arguments.Nodes[0])] = struct{}{}
	outer := Unwrapped(call.Expression)
	consumed[call.Expression] = struct{}{}
	consumed[outer] = struct{}{}
	inner := Unwrapped(outer.AsPropertyAccessExpression().Expression)
	consumed[outer.AsPropertyAccessExpression().Expression] = struct{}{}
	consumed[inner] = struct{}{}
	consumeReceiver(consumed, inner)
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

// captureMentions walks a NESTED FUNCTION's subtree and answers the
// declared fields it READS through the receiver and the receiver
// METHODS it CALLS — provided every receiver mention is one of exactly
// those two shapes. Anything else — a write target, an optional or
// computed step, an undeclared member read, a bare mention — answers
// (nil, nil, false) and the caller keeps the escape.
//
// A read is admissible from inside a closure that runs at ANY later
// time because reading moves nothing. A method CALL is admissible only
// conditionally: the consumer must compute the transitive write set of
// every collected method and treat those fields as movable at every
// call statement — captureMentions only collects the names.
func captureMentions(
	c *checker.Checker,
	node *ast.Node,
	isReceiver func(*ast.Node) bool,
	byName map[string]BundleField,
) ([]string, []string, bool) {
	var reads []string
	var calledMethods []string
	readOnly := true
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if !readOnly || child == nil {
			return true
		}
		// any write form whose subtree mentions the receiver fails the
		// admission — the target may be a field, and a field written at an
		// unplaceable time is exactly what the escape guards
		if ast.IsBinaryExpression(child) {
			operator := child.AsBinaryExpression().OperatorToken.Kind
			if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
				if mentionsReceiverNode(child.AsBinaryExpression().Left, isReceiver) {
					readOnly = false
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			operator := child.AsPrefixUnaryExpression().Operator
			if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
				mentionsReceiverNode(child.AsPrefixUnaryExpression().Operand, isReceiver) {
				readOnly = false
				return true
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			operator := child.AsPostfixUnaryExpression().Operator
			if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
				mentionsReceiverNode(child.AsPostfixUnaryExpression().Operand, isReceiver) {
				readOnly = false
				return true
			}
		}
		if ast.IsDeleteExpression(child) && mentionsReceiverNode(child, isReceiver) {
			readOnly = false
			return true
		}
		// `Object.assign(this, source)` — a store into fields nothing
		// names, exactly the computed store's shape: every field may have
		// moved, and the declaration still bounds the set. ComputedWrite
		// admits it under the all-field havoc bracketing instead of the
		// escape. The SOURCE arguments walk on as ordinary expressions.
		if ast.IsCallExpression(child) {
			assignCall := child.AsCallExpression()
			if assignCall.QuestionDotToken == nil && ast.IsPropertyAccessExpression(assignCall.Expression) {
				assignAccess := assignCall.Expression.AsPropertyAccessExpression()
				if assignAccess.QuestionDotToken == nil &&
					ast.IsIdentifier(assignAccess.Expression) && assignAccess.Expression.Text() == "Object" &&
					ast.IsIdentifier(assignAccess.Name()) && assignAccess.Name().Text() == "assign" &&
					assignCall.Arguments != nil && len(assignCall.Arguments.Nodes) > 0 &&
					isReceiver(Unwrapped(assignCall.Arguments.Nodes[0])) {
					readOnly = false // reached only inside captures — a capture that
					// reshapes the receiver is beyond the capture rules
					return true
				}
			}
		}
		// a CALL whose callee is a receiver access is a METHOD call. The
		// method's body may write fields, so the call is not a read — but
		// it is not blind either: the method NAME is collected, and the
		// consumer decides whether the transitive write set of every
		// collected method is computable. A non-identifier or optional
		// callee step fails outright. The ARGUMENTS walk on under the
		// same rules.
		//
		// A SYMBOL-KEYED callee (`this[S](…)`) is the same call under a
		// different spelling and takes the same arm, collected under its
		// `#sym:` name. The stable key names ONE member, which is all this
		// collection needs; whether that member is a METHOD whose body the
		// closure can walk or a FIELD holding a function value is
		// CaptureWriteSetWith's question, and it answers it identically for
		// both spellings — a name resolving to a method-with-body
		// contributes that body's writes, and a name resolving to anything
		// else makes the whole set incomputable and the caller keeps the
		// escape. Deciding it here instead would refuse the symbol-keyed
		// field call one stage earlier than the dotted one spells the same
		// refusal, for no reason the two shapes justify.
		if ast.IsCallExpression(child) {
			call := child.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsPropertyAccessExpression(callee) &&
				isReceiver(Unwrapped(callee.AsPropertyAccessExpression().Expression)) {
				access := callee.AsPropertyAccessExpression()
				if call.QuestionDotToken != nil || access.QuestionDotToken != nil ||
					!ast.IsIdentifier(access.Name()) {
					readOnly = false
					return true
				}
				calledMethods = append(calledMethods, access.Name().Text())
				for _, argument := range call.Arguments.Nodes {
					visit(argument)
				}
				return false
			}
			if ast.IsElementAccessExpression(callee) &&
				isReceiver(Unwrapped(callee.AsElementAccessExpression().Expression)) {
				name, isSymbolKey := SymbolKeyedFieldName(c, callee)
				if call.QuestionDotToken != nil || !isSymbolKey {
					readOnly = false
					return true
				}
				calledMethods = append(calledMethods, name)
				for _, argument := range call.Arguments.Nodes {
					visit(argument)
				}
				return false
			}
		}
		// a plain declared-field READ: record it and step over the
		// receiver spelling it consumed
		if ast.IsPropertyAccessExpression(child) {
			access := child.AsPropertyAccessExpression()
			if isReceiver(Unwrapped(access.Expression)) {
				if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
					readOnly = false
					return true
				}
				name := access.Name().Text()
				if _, declared := byName[name]; !declared {
					readOnly = false
					return true
				}
				reads = append(reads, name)
				return false
			}
		}
		// a SYMBOL-KEYED read: `this[S]` under a stable symbol const names
		// one declared field, so it is admissible on the same ground the
		// dotted read is — reading moves nothing. Every other bracketed
		// key names no field and fails below.
		//
		// A `this[S](…)` CALL never reaches here: the call arm above takes
		// it, admitting it where the class declares a method for S and
		// refusing it where nothing does. What is left in this position is
		// a read of the slot's own value, which is what the field rules
		// answer.
		if ast.IsElementAccessExpression(child) {
			element := child.AsElementAccessExpression()
			if isReceiver(Unwrapped(element.Expression)) {
				name, isSymbolKey := SymbolKeyedFieldName(c, child)
				if !isSymbolKey {
					readOnly = false
					return true
				}
				if _, declared := byName[name]; !declared {
					readOnly = false
					return true
				}
				reads = append(reads, name)
				return false
			}
		}
		// any OTHER receiver occurrence — bare, computed, spread — fails
		if isReceiver(child) && !isPropertyStepName(child) {
			readOnly = false
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	node.ForEachChild(visit)
	if !readOnly {
		return nil, nil, false
	}
	return reads, calledMethods, true
}

// mentionsReceiverNode is mentionsReceiver over a receiver PREDICATE
// rather than a name — the nested-function admission tests subtrees
// with the census's own isReceiver.
func mentionsReceiverNode(node *ast.Node, isReceiver func(*ast.Node) bool) bool {
	if node == nil {
		return false
	}
	if isReceiver(node) && !isPropertyStepName(node) {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if found {
			return true
		}
		if mentionsReceiverNode(child, isReceiver) {
			found = true
			return true
		}
		return false
	})
	return found
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
