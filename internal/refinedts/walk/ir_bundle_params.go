// A CLASS-TYPED PARAMETER as a slot bundle: the fields the class
// declares, respelled under the parameter's own name, and what one body
// does with them.
//
// Wave 4 makes every object with a declared shape a slot bundle. A
// method's `this` reads its class's fields through ClassFieldsOf
// (ir_field_bundles.go); this file is the OTHER receiver — a parameter
// annotated with a class name (`wrapper: InstanceWrapper`, `module:
// Module`), whose fields become entries spelled "wrapper.<field>".
//
// Two readings of one class have to agree, so both go through
// ClassFieldsOf: a `this` bundle and a parameter bundle of the same
// class produce the same field list in the same order, differing only in
// the receiver name the slots are spelled under. What this file ADDS is
// the CONSTRUCTOR PARAMETER PROPERTIES — `constructor(private readonly
// container: NestContainer)` declares a field that no property
// declaration spells, so ClassFieldsOf's member walk never sees it, and
// a body reading `this.container` or `wrapper.container` would find no
// slot. The union is this file's, and the weave note says where the
// `this` census should take it too.
//
// ONE OWNER PER SHAPE. An INTERFACE- or ALIAS-typed parameter is
// BundleTypeFieldsOf's route (ir_field_bundles.go); this file answers
// false for them so no shape is read twice by two rules that could
// drift.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── constructor parameter properties ────────────────────────────── */

// ConstructorParameterFields is the fields a class declares through its
// CONSTRUCTOR PARAMETERS — TypeScript's parameter properties:
//
//	constructor(private readonly container: NestContainer, public x: number)
//
// declares `container` and `x` as instance fields, with no property
// declaration anywhere in the class body. ClassFieldsOf walks MEMBERS
// and sees only the constructor, so without this reading those fields
// have no slot and every read of them falls to the opaque floor.
//
// What counts: a constructor parameter carrying an ACCESSIBILITY
// modifier (`public`/`private`/`protected`) or `readonly`. That is
// exactly TypeScript's own rule for when a parameter also declares a
// field — a bare `constructor(x: number)` declares a parameter and
// nothing else, so it contributes no field here.
//
// The Name is the parameter's identifier, and the SlotName comes back
// BARE — exactly as ClassFieldsOf spells its own answer, so the two
// halves of a class's field set are one list the caller re-spells in one
// place (BundleFieldsAs, under `this` for a method's receiver and under
// the parameter's name for a class-typed parameter). The sort and typeof
// read from the annotation by annotationSort, the SAME member-wise rule
// ClassFieldsOf applies to a property declaration: number and boolean
// ride the number sort, string rides the string sort, anything else is
// unknown and admits only the definedness test.
//
// A BINDING PATTERN parameter (`constructor(private {a}: T)` — which
// TypeScript itself refuses) and a parameter with no identifier name
// contribute nothing: no name spells the slot.
//
// Deterministic order: PARAMETER ORDER, so two readings of one class
// build the same slot vector.
//
// (nil) for anything that is not class-like, and for a class with no
// constructor or a constructor with no parameter properties.
func ConstructorParameterFields(classLike *ast.Node) []BundleField {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	var fields []BundleField
	seen := map[string]struct{}{}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if !ast.IsConstructorDeclaration(member) {
			continue
		}
		for _, parameter := range member.Parameters() {
			if !isParameterPropertyDeclaration(parameter) {
				continue
			}
			declaration := parameter.AsParameterDeclaration()
			name := declaration.Name()
			if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
				continue
			}
			text := name.Text()
			// a name declared twice contributes once — the slot vector must
			// not hold the same spelling twice (ClassFieldsOf's own rule)
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
		// a class body holds at most ONE constructor with a body; an
		// overload signature has no parameter properties (TypeScript refuses
		// them there), so the first constructor carrying any is the answer
		// and later ones only repeat names the `seen` set already holds
	}
	return fields
}

// isParameterPropertyDeclaration answers whether a constructor
// parameter also DECLARES a field: it carries an accessibility modifier
// or `readonly`.
//
// ast.IsParameterPropertyDeclaration exists and reads the same bit, but
// its flag set also admits `override` alone, which declares no field on
// its own. This spells the two modifier families the rule is actually
// about, so what counts as a field is stated here rather than inherited
// from a flag name that means something slightly wider.
func isParameterPropertyDeclaration(parameter *ast.Node) bool {
	if parameter == nil || !ast.IsParameterDeclaration(parameter) {
		return false
	}
	const declaresField = ast.ModifierFlagsAccessibilityModifier | ast.ModifierFlagsReadonly
	return parameter.ModifierFlags()&declaresField != 0
}

/* ── the class-typed parameter bundle ────────────────────────────── */

// ClassBundleFields is the WHOLE field set of one class as a bundle:
// its declared property members (ClassFieldsOf) followed by its
// constructor parameter properties (ConstructorParameterFields), each
// name appearing once, in declaration order — members first, then
// constructor parameters in parameter order.
//
// Fields come back spelled BARE, as ClassFieldsOf spells them; the
// caller re-spells them under whatever receiver the bundle is read
// through (BundleFieldsAs) — "this.<field>" for a method's own receiver,
// "wrapper.<field>" for a class-typed parameter.
//
// The union is what makes a Nest-shaped class readable at all: nearly
// every injected dependency is a constructor parameter property, so a
// class whose fields are read only through ClassFieldsOf answers an
// empty list and its bundle never expands.
//
// (false) only for a node that is not class-like.
func ClassBundleFields(ctx *FlowContext, classLike *ast.Node) ([]BundleField, bool) {
	declared, isClass := ClassFieldsOf(ctx, classLike)
	if !isClass {
		return nil, false
	}
	fields := make([]BundleField, 0, len(declared))
	seen := map[string]struct{}{}
	for _, field := range declared {
		if _, already := seen[field.Name]; already {
			continue
		}
		seen[field.Name] = struct{}{}
		fields = append(fields, field)
	}
	for _, field := range ConstructorParameterFields(classLike) {
		if _, already := seen[field.Name]; already {
			// a property declaration and a parameter property of the same
			// name is a class TypeScript refuses; the declared member wins so
			// the two readings cannot disagree about the slot's sort
			continue
		}
		seen[field.Name] = struct{}{}
		fields = append(fields, field)
	}
	return fields, true
}

// BundleParamFieldsOf is the field set behind a parameter annotated with
// a CLASS NAME — `wrapper: InstanceWrapper` — respelled under the
// parameter's own name ("wrapper.metatype", "wrapper.instance").
//
// The annotation must be a TYPE REFERENCE whose name resolves through
// symbolAt (following import aliases, so a class imported from another
// file answers from its declaration there) to a CLASS declaration. The
// fields are that class's ClassFieldsOf UNION its constructor parameter
// properties — ClassBundleFields above.
//
// THE STABILITY GATES, which are namedTypeMembersOf's (ir_summary_body
// .go) applied to the class shape. That comment states why the answer is
// check-independent: the identity of the answer is the resolved
// DECLARATION NODE, and the fields are then read off that node's own
// SYNTAX, so two checks can only differ by resolving the name to a
// different declaration. Each shape where that is possible declines:
//
//   - a QUALIFIED name (`ns.Wrapper`) or a name carrying TYPE ARGUMENTS
//     (`Box<number>`) — the fields would depend on what was applied,
//     which no entry vector spells;
//   - a symbol with NO declarations, or with MORE THAN ONE — a name
//     declared twice (a class merged with an interface, a declaration
//     merged with a namespace) has members spread across declarations
//     this reading does not visit, and reading only the first would build
//     a field list the other contradicts;
//   - a class carrying TYPE PARAMETERS — the fields' annotations are not
//     the ones any instance actually holds;
//   - a nil context, or one with no program or no checker — the
//     nil-tolerance the ctx-less callers rely on: a lowering that runs
//     without a checker declines the bundle rather than crashing.
//
// HERITAGE IS NOT A DECLINE, and this is the one place the class rule
// parts from the interface rule. An interface with `extends` declines
// (BundleTypeFieldsOf) because an interface is nothing but its members,
// so an incomplete member list is an incomplete answer. Classes have
// heritage COMMONLY, and a class with an `extends` clause still declares
// its OWN fields here: the list holds exactly the names this declaration
// spells, and the sorts are read from this declaration's own
// annotations, so every slot in it is sound. What the base class adds is
// simply NOT IN THE LIST — a body reading an inherited field finds no
// slot for it, and that read falls to the opaque floor exactly as it
// does today for a class with no bundle at all. Nothing claims the base
// fields are absent; they are unspelled, which costs precision and never
// soundness.
//
// INTERFACES AND ALIASES ANSWER FALSE. They are BundleTypeFieldsOf's
// route, and one shape read by two rules is two chances to disagree
// about the slot vector.
//
// The name answered is the parameter's own identifier — the receiver the
// census scans a body for and the prefix the call site fills slots under.
func BundleParamFieldsOf(ctx *FlowContext, parameter *ast.Node) (name string, fields []BundleField, ok bool) {
	if parameter == nil || !ast.IsParameterDeclaration(parameter) {
		return "", nil, false
	}
	declaration := parameter.AsParameterDeclaration()
	if declaration.Name() == nil || !ast.IsIdentifier(declaration.Name()) {
		return "", nil, false
	}
	typeNode := declaration.Type
	if typeNode == nil || !ast.IsTypeReferenceNode(typeNode) {
		return "", nil, false
	}
	reference := typeNode.AsTypeReferenceNode()
	// type ARGUMENTS make the fields depend on what was applied
	if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
		return "", nil, false
	}
	typeName := reference.TypeName
	// only a PLAIN identifier: a qualified name reaches into a namespace
	// whose resolution this reading does not claim
	if typeName == nil || !ast.IsIdentifier(typeName) {
		return "", nil, false
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return "", nil, false
	}
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return "", nil, false
	}
	classLike := symbol.Declarations[0]
	if classLike == nil || !ast.IsClassLike(classLike) {
		// an interface, an alias, an enum, a type parameter — none are this
		// file's shape (BundleTypeFieldsOf owns the first two)
		return "", nil, false
	}
	if classLike.ClassLikeData().TypeParameters != nil &&
		len(classLike.ClassLikeData().TypeParameters.Nodes) > 0 {
		return "", nil, false
	}
	declaredFields, isClass := ClassBundleFields(ctx, classLike)
	if !isClass || len(declaredFields) == 0 {
		return "", nil, false
	}
	holder := declaration.Name().Text()
	return holder, BundleFieldsAs(holder, declaredFields), true
}

/* ── the per-body census ─────────────────────────────────────────── */

// BundleParamCensus is the class-typed parameter's field set AND what
// one body does with it — the composition of BundleParamFieldsOf and
// FieldCensusOf, so the reads, the writes, and the two out-of-sight
// flags come from the SAME single scan the `this` bundle uses.
//
// No new scan and no second field reading: the census is the one report
// both the layout and the call sites read (ir_field_bundles.go's
// contract), and a parameter bundle joins that contract by reusing it
// rather than by growing a parallel one.
//
// The fields answered are the WHOLE set, respelled under the parameter's
// name — not just the read ones — because a consumer laying out entries
// needs the read list (census.Reads) while a consumer havocking the
// bundle needs every slot it could have.
//
// (false) exactly where BundleParamFieldsOf declines, plus a nil body:
// with nothing to scan there is no report to make.
func BundleParamCensus(
	ctx *FlowContext,
	body *ast.Node,
	parameter *ast.Node,
) (name string, census FieldCensus, fields []BundleField, ok bool) {
	holder, bundleFields, isBundle := BundleParamFieldsOf(ctx, parameter)
	if !isBundle {
		return "", FieldCensus{}, nil, false
	}
	if body == nil {
		return "", FieldCensus{}, nil, false
	}
	return holder, FieldCensusOf(body, holder, bundleFields), bundleFields, true
}

/* ── the apply side ──────────────────────────────────────────────── */

// BundleParamEntryStates is what a CLASS-TYPED PARAMETER's entries enter
// holding at a call: one state per READ field, in the read list's own
// order — which is the field set's declaration order, the same order the
// layout appended the entries in.
//
// Each field's state is read off the argument's own object knowledge by
// the SAME route the record-parameter apply takes (summaryEntryStates,
// kernel_summaries.go): objectKeyIndex to find the field, StateOfKnown
// to spell its value on the wire. thisEntryState is that route with the
// TOP fallbacks already attached, so this reuses it directly rather than
// opening a second reader that could answer differently.
//
// TOP, NOT DECLINE — and this is where a class bundle parts from a
// RECORD parameter. A record parameter's apply DECLINES the call on a
// non-object argument or a missing member: those entries were laid out
// for a `{ lo, hi }` the caller was supposed to hand over, and a scalar
// where the object should be means the site and the layout disagree
// about the shape entirely. A CLASS INSTANCE argument is different in
// kind: the caller routinely holds no object knowledge about it at all —
// it came from a constructor, a container lookup, another call — and the
// class bundle exists precisely to let a body read fields the CALLER
// knows nothing about. Declining there would kill every call whose
// argument is an ordinary instance, which is nearly all of them. So a
// non-object argument fills EVERY entry TOP, and so does a field the
// argument's knowledge does not name or whose value the wire cannot
// spell.
//
// TOP and never ABSENT, for thisEntryState's reason: absent would claim
// the field IS undefined — a claim no caller made, and one the body's
// reads would then narrow on. TOP is what the summary's entry quantifier
// already covers, so filling it costs precision and never soundness.
//
// The bool is (true) whenever the entries were built; it is false only
// for an empty read list, where there is nothing to fill and the caller
// has no entries to place.
func BundleParamEntryStates(
	argKnown abstractdomain.AbstractValue,
	read []BundleField,
) ([]kernelbridge.KnownStateWire, bool) {
	if len(read) == 0 {
		return nil, false
	}
	states := make([]kernelbridge.KnownStateWire, 0, len(read))
	for _, field := range read {
		// thisEntryState is the record-parameter apply's own reader with the
		// TOP fallbacks attached: a non-object receiver, an unnamed field,
		// and an unspellable value all answer {Top:true}
		states = append(states, thisEntryState(argKnown, field.Name))
	}
	return states, true
}

// BundleParamFieldNameOf is the field behind a parameter bundle's slot
// spelling: under the holder "wrapper", "wrapper.metatype" is metatype.
// Anything not spelled under that holder is some other bundle's row (a
// `this` field, a record-parameter leaf) and answers false.
//
// The call site's fill needs this the way thisFieldNameOf serves the
// `this` rows: a BundleEntry carries only its Path, and the row has to be
// read back apart to find which field of which receiver it names.
func BundleParamFieldNameOf(path string, holder string) (string, bool) {
	under := holder + "."
	if len(path) <= len(under) || path[:len(under)] != under {
		return "", false
	}
	return path[len(under):], true
}
