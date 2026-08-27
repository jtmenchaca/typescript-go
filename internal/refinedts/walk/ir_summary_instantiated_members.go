// split from ir_summary_named_type_members.go — the INSTANTIATED
// reading a type reference carrying TYPE ARGUMENTS takes

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// instantiatedReferenceMembersOf resolves a type reference that CARRIES
// TYPE ARGUMENTS — `p: Box<number>` — to the members its INSTANTIATION
// stands for, or (false).
//
// THIS ARM IS CHECK-DEPENDENT, and says so where the rest of the
// named-type reading argues check-independence. The syntax route reads
// members off a declaration's own written annotation, which an
// instantiation does not have: `hi: T` under `Box<T>` means `number`
// for `Box<number>` only because the checker substituted the argument.
// So the members here are the checker's own answer
// (GetTypeFromTypeNode + GetPropertiesOfType + GetTypeOfSymbolAtLocation)
// — the same authority every GetTypeAtLocation ask in the walk already
// trusts. The two-seams agreement (layout vs call sites) never came
// from check-independence; it comes from recordParamMembersIn's memo,
// which pins this arm's first answer exactly as it pins the syntax
// route's.
//
// WHAT THE READING KEEPS from the syntax route's discipline, arm by arm:
//
//   - a CLASS instantiation declines — an instance carries methods,
//     accessors and private state the flattening cannot hold
//     (namedTypeMembersOf's own class refusal, reached here through the
//     type's symbol);
//   - an ARRAY-LIKE instantiation declines — `Array<number>`,
//     `ReadonlyArray<T>` and tuples belong to the array-parameter route,
//     and one annotation must reach exactly one slot family
//     (SummaryParameterEntriesIn's record-then-array exclusivity);
//   - a METHOD contributes no leaf and kills nothing — it is a call,
//     not a slot, and the uses discipline refuses any body that calls
//     one (scalarMemberListOf's own method rule);
//   - an OPTIONAL member contributes its leaf wearing MayBeAbsent, the
//     optionality's undefined stripped before the sort is read — the
//     absence rides the entry state, never the sort (recordParamMember's
//     MayBeAbsent doc);
//   - a member whose instantiated type the sort vocabulary cannot spell
//     — an object, a union across sorts, a still-abstract type
//     parameter — contributes its named leaf UNKNOWN-SORTED rather than
//     refusing (the accounting argument on scalarMemberListOf);
//   - EMPTY declines — a name expanding to no leaf leaves every read of
//     the holder unserved with no slot to point at, so the holder keeps
//     its whole-name slot (declaredTypeMembersOf's entry rule; this arm
//     is only reached from the entry position).
//
// ONE GUARD THE INSTANTIATED ARM ADDS, keeping an expansion from serving
// LESS than the whole-name slot it replaces:
//
//   - AN EXPANSION THE OWNING BODY'S OWN USES WOULD ONLY HAVOC OR
//     REFUSE declines (instantiatedExpansionServesOwnBody). The scan's
//     unreadable answer refuses the body outright, and its hand-over
//     answer (a called method member — `api.getState()` — or the whole
//     record as a call argument) marks EVERY leaf Written and joins the
//     havoc vector, porous at best — where the same body over the
//     un-expanded single slot has been observed to lower COMPLETE. Only
//     a body whose whole-name uses are reads (members-only, read-whole)
//     keeps the expansion. The body scanned is the parameter's own
//     enclosing declaration, the same one-body fact arrayParamSlotsIn
//     already reads, and the memo makes the scanned answer the one both
//     seams see.
func instantiatedReferenceMembersOf(ctx *FlowContext, holder string, typeNode *ast.Node) ([]recordParamMember, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	c := ctx.P.Checker
	tracing.CountBy("host.typeFromTypeNode", 1)
	t := c.GetTypeFromTypeNode(typeNode)
	if t == nil || (t.Flags()&checker.TypeFlagsObject) == 0 {
		return nil, false
	}
	if symbol := t.Symbol(); symbol != nil && (symbol.Flags&ast.SymbolFlagsClass) != 0 {
		return nil, false
	}
	tracing.CountBy("host.isArrayLikeType", 1)
	if c.IsArrayLikeType(t) {
		return nil, false
	}
	tracing.CountBy("host.propertiesOfType", 1)
	properties := c.GetPropertiesOfType(t)
	if len(properties) == 0 {
		return nil, false
	}
	seen := map[string]struct{}{}
	out := make([]recordParamMember, 0, len(properties))
	for _, property := range properties {
		// only a DATA PROPERTY names a slot: a method or an accessor
		// resolves as a call, and an internal `__`-prefixed name is not
		// one any property step spells
		if (property.Flags & ast.SymbolFlagsProperty) == 0 ||
			(property.Flags&(ast.SymbolFlagsMethod|ast.SymbolFlagsAccessor)) != 0 {
			continue
		}
		if strings.HasPrefix(property.Name, "__") {
			continue
		}
		if _, already := seen[property.Name]; already {
			return nil, false
		}
		seen[property.Name] = struct{}{}
		// the declaration site the member's type is asked at; a synthetic
		// member (a mapped-type property) may have none, and the reference
		// itself stands in
		site := property.ValueDeclaration
		if site == nil && len(property.Declarations) > 0 {
			site = property.Declarations[0]
		}
		if site == nil {
			site = typeNode
		}
		mayBeAbsent := (property.Flags & ast.SymbolFlagsOptional) != 0
		tracing.CountBy("host.typeOfSymbolAtLocation", 1)
		sort, tag := instantiatedMemberSort(c.GetTypeOfSymbolAtLocation(property, site), mayBeAbsent)
		out = append(out, recordParamMember{
			Key:         property.Name,
			Path:        []string{property.Name},
			SlotName:    holder + "." + property.Name,
			Sort:        sort,
			TypeofTag:   tag,
			MayBeAbsent: mayBeAbsent,
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	if !instantiatedExpansionServesOwnBody(typeNode, out) {
		return nil, false
	}
	return out, true
}

// instantiatedMemberSort reads one instantiated member type as the sort
// and typeof evidence its leaf carries — ResolvedExpressionSort's
// masking, applied per union part with agreement required: every part
// must wear the same sort or the leaf is unknown, and parts agreeing on
// the sort but not the typeof (number beside boolean) keep the sort and
// claim no tag. An OPTIONAL member's undefined part is stripped first —
// that undefined is the absence MayBeAbsent already carries on the
// entry state, not a fact about the present value — while a REQUIRED
// member's explicit `| undefined` stays and lands the leaf on unknown,
// since nothing else carries it.
func instantiatedMemberSort(memberType *checker.Type, optional bool) (BindingKind, TypeofTag) {
	if memberType == nil {
		return BindingKindUnknown, TypeofTagNone
	}
	var present []*checker.Type
	for _, part := range typePartsOf(memberType) {
		if optional && (part.Flags()&checker.TypeFlagsUndefined) != 0 {
			continue
		}
		present = append(present, part)
	}
	if len(present) == 0 {
		return BindingKindUnknown, TypeofTagNone
	}
	sort, tag := scalarSortOfTypeFlags(present[0].Flags())
	if sort == BindingKindUnknown {
		return BindingKindUnknown, TypeofTagNone
	}
	for _, part := range present[1:] {
		partSort, partTag := scalarSortOfTypeFlags(part.Flags())
		if partSort != sort {
			return BindingKindUnknown, TypeofTagNone
		}
		if partTag != tag {
			tag = TypeofTagNone
		}
	}
	return sort, tag
}

// scalarSortOfTypeFlags is the masking LocalSortResolved and
// ResolvedExpressionSort state, over one type's flags: only number/
// boolean flags is the number sort (booleans ride the number sort,
// their typeof differs), only string flags the string sort, anything
// else unknown.
func scalarSortOfTypeFlags(flags checker.TypeFlags) (BindingKind, TypeofTag) {
	num := checker.TypeFlagsNumber | checker.TypeFlagsNumberLiteral
	boolean := checker.TypeFlagsBoolean | checker.TypeFlagsBooleanLiteral
	switch {
	case (flags&num) != 0 && (flags & ^num) == 0:
		return BindingKindNumber, TypeofTagNumber
	case (flags&boolean) != 0 && (flags & ^boolean) == 0:
		return BindingKindNumber, TypeofTagBoolean
	}
	strOrLit := checker.TypeFlagsString | checker.TypeFlagsStringLiteral
	if (flags&strOrLit) != 0 && (flags & ^strOrLit) == 0 {
		return BindingKindString, TypeofTagString
	}
	return BindingKindUnknown, TypeofTagNone
}

// instantiatedExpansionServesOwnBody says whether expanding this
// annotation could serve the one body its parameter belongs to. False
// where expanding is KNOWN to serve no more than the whole-name slot:
// the owning body's use scan refuses the candidate expansion outright
// (unreadable — a stored interior path, a computed step), the scan
// answers HANDED-OVER (a called method member, the record as a call
// argument — the hand-over wiring havocs every leaf, porous at best,
// where the un-expanded body still lowers around the call), or the
// parameter's function has no body at all (an overload or
// declaration-file signature — no use to serve, and slots nobody reads
// only widen the entry vector, arrayParamSlotsIn's own rule).
//
// A NON-PARAMETER position (a declared local's annotation, an array
// element type) answers true: the record-parameter use scan is a fact
// about parameter bodies, and those routes carry their own use
// disciplines.
func instantiatedExpansionServesOwnBody(typeNode *ast.Node, members []recordParamMember) bool {
	parameter := typeNode.Parent
	if parameter == nil || !ast.IsParameterDeclaration(parameter) {
		return true
	}
	name := parameter.AsParameterDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		// a binding-pattern parameter's own name never appears in the body
		return true
	}
	owner := parameter.Parent
	if owner == nil {
		return true
	}
	body := owner.Body()
	if body == nil {
		return false
	}
	use, _ := recordParameterUseOf(body, name.Text(), members)
	return use == recordParameterMembersOnly || use == recordParameterReadWhole
}
