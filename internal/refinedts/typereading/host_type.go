// Resolved-type adapter: *checker.Type → AbstractValue. Callers use
// ReadHostType / ReadDeclaredType, not this file.
//
// The TS source reads through a CheckerProgram (p.host) -- the
// CheckerHost/tsgo-oracle adapter (spans, handles, wire guards). Per
// the port's convention, that adapter does not port: this file reads
// *checker.Checker and *checker.Type directly, since the host IS the
// checker, in-process.

package typereading

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

const absentFlags = checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid

// readHostTypeUncached is readHostType in the TS source. Callers go
// through ReadHostType (host_type_memo.go), which remembers each
// (type, depth) answer per checker — the recursive calls below go
// through the memo too, so a big union's arms remember individually.
//
// usedAt reports whether the answer depended on the `at` fallback (a
// member symbol with no declaration of its own): such an answer is a
// function of the call SITE, not the type alone, so the memo must not
// remember it — under the parallel sweep, which entry reads a type
// first varies per run, and a site-dependent first write would make
// the cached answer nondeterministic across runs.
func readHostTypeUncached(c *checker.Checker, t *checker.Type, at *ast.Node, depth int, usedAt *bool) (abstractdomain.AbstractValue, bool) {
	flags := t.Flags()
	if (flags & checker.TypeFlagsStringLiteral) != 0 {
		value, ok := t.AsLiteralType().Value().(string)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(value), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	if (flags & checker.TypeFlagsNumberLiteral) != 0 {
		value, ok := numberLiteralValueOf(t)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownValues([]float64{value}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	if (flags & checker.TypeFlagsBooleanLiteral) != 0 {
		// The TS source reads `(type as unknown as
		// {intrinsicName?}).intrinsicName` -- tsc's own boolean-literal
		// representation. tsgo represents a boolean literal type as a
		// *checker.LiteralType carrying a Go bool (see checker.go's
		// newLiteralType(TypeFlagsBooleanLiteral, ...) construction and
		// getBooleanLiteralValue), not an *IntrinsicType with a name
		// string -- AsIntrinsicType() panics on that shape. AsLiteralType
		// is the correct downcast here, matching the StringLiteral/
		// NumberLiteral branches just above.
		value, ok := t.AsLiteralType().Value().(bool)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		bit := 0.0
		if value {
			bit = 1
		}
		return abstractdomain.KnownValues([]float64{bit}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), true
	}
	if (flags & checker.TypeFlagsBoolean) != 0 {
		return BooleanCodes(), true
	}
	if (flags & checker.TypeFlagsString) != 0 {
		return StringGround(), true
	}
	if (flags & checker.TypeFlagsNumber) != 0 {
		return NumberWithNaN(), true
	}
	if (flags & checker.TypeFlagsESSymbol) != 0 {
		return UnknownSymbol(), true
	}
	if (flags & checker.TypeFlagsTemplateLiteral) != 0 {
		spans := t.AsTemplateLiteralType()
		texts := spans.Texts()
		types := spans.Types()
		var parts []refinementsets.RefinedSet
		for i := range texts {
			if len(texts[i]) > 0 {
				parts = append(parts, refinementsets.StringTuple(texts[i]))
			}
			if i < len(types) {
				parts = append(parts, refinementsets.Strings)
			}
		}
		if len(parts) == 0 {
			return abstractdomain.AbstractValue{}, false
		}
		set := parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(parts[i], set))
		}
		return abstractdomain.KnownSet(set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone), true
	}
	if (flags&(checker.TypeFlagsTypeParameter|checker.TypeFlagsConditional|checker.TypeFlagsIndexedAccess|checker.TypeFlagsSubstitution)) != 0 && depth < 3 {
		constrained := c.GetConstraintOfType(t)
		if constrained == nil || constrained == t {
			return abstractdomain.AbstractValue{}, false
		}
		return readHostTypeMemoized(c, constrained, at, depth+1, usedAt)
	}
	// A TS enum type IS the union of its member literal types, wearing
	// EnumLiteral beside Union (getDeclaredTypeOfEnum). Its members
	// carry the same literal values the enum DECLARATION spells, so the
	// declaration reader answers the whole set in one step — and it
	// answers a mixed or computed enum with nothing, exactly as the
	// per-member walk below would. Read ahead of the union branch so an
	// enum at any depth answers: the members are literals, and reading
	// them costs no recursion the depth gate exists to bound.
	if (flags & checker.TypeFlagsEnumLiteral) != 0 {
		if declaration := enumDeclarationOf(t); declaration != nil {
			if values, ok := EnumValuesState(declaration); ok {
				return values, true
			}
		}
	}
	if (flags&checker.TypeFlagsUnion) != 0 && depth < 3 {
		var arms []abstractdomain.AbstractValue
		sawAbsent := false
		for _, part := range t.Types() {
			if (part.Flags() & absentFlags) != 0 {
				sawAbsent = true
				continue
			}
			inner, ok := readHostTypeMemoized(c, part, at, depth+1, usedAt)
			if !ok {
				// an arm the reader cannot spell still names a SORT
				// where tsc states one, and that sort's whole ground is
				// a true claim about every value the arm admits — the
				// union joins it with the arms already read. An arm
				// naming no sort at all has no widest reading to give:
				// the union of a stated set with the unknown IS the
				// unknown, so the whole read refuses, as it did before.
				widest, hasWidest := sortGroundOf(part)
				if !hasWidest {
					return abstractdomain.AbstractValue{}, false
				}
				inner = widest
			}
			arms = append(arms, inner)
		}
		return PresentUnion(arms, sawAbsent)
	}
	// depth 3, the union branch's own limit: a record inside a union
	// inside a record used to be cut by the tighter of the two, so the
	// same value read nothing depending on which branch reached it
	// first. One limit, one answer.
	if (flags&(checker.TypeFlagsObject|checker.TypeFlagsIntersection)) != 0 && depth < 3 {
		if len(c.GetCallSignatures(t)) > 0 || len(c.GetConstructSignatures(t)) > 0 {
			return abstractdomain.HostFunction, true
		}
		// GetTypeArguments assumes a Reference type underneath (tsgo's
		// getTypeArguments downcasts unconditionally via
		// t.AsTypeReference(), unlike tsc's own defensive
		// `(type as TypeReference).resolvedTypeArguments || emptyArray`)
		// -- IsArrayLikeType admits array-like INTERSECTIONS too (an
		// array type-reference intersected with a stated `{ length }`,
		// z.array(...).min(n)'s host shape), which carries no
		// TypeReference of its own and segfaults the downcast. The
		// ObjectFlagsReference check is the guard tsc's isArrayLikeType
		// + getTypeArguments pairing provides implicitly; without it
		// this reads as "not determined", the same answer an
		// unrecognized array-like shape already falls through to below.
		if c.IsArrayLikeType(t) && (t.ObjectFlags()&checker.ObjectFlagsReference) != 0 {
			slots := c.GetTypeArguments(t)
			if len(slots) == 0 {
				return abstractdomain.AbstractValue{}, false
			}
			var element abstractdomain.AbstractValue
			haveElement := false
			for _, slot := range slots {
				inner, ok := readHostTypeMemoized(c, slot, at, depth+1, usedAt)
				if !ok {
					return abstractdomain.AbstractValue{}, false
				}
				if !haveElement {
					element = inner
					haveElement = true
				} else {
					element = abstractdomain.JoinKnown(element, inner)
				}
			}
			if !haveElement {
				return abstractdomain.AbstractValue{}, false
			}
			return StarOfElement(element)
		}
		// A lib-declared record (Buffer, IncomingMessage, Server) reads
		// the same way any other record does: each member the reader can
		// spell is as true of a host object as of a user one, and the
		// object built below is incomplete (Complete false), so no key it
		// does NOT name carries a claim. What the lib shapes really cost
		// is their SIZE, and the member budget below is what bounds that
		// — the declaring file is not the thing that made them expensive.
		members := c.GetPropertiesOfType(t)
		// no member at all leaves nothing to seed — the record answers
		// nothing because it states nothing, not because a gate cut it.
		if len(members) == 0 {
			return abstractdomain.AbstractValue{}, false
		}
		// A wide record answers its FIRST members rather than refusing
		// whole: the object is incomplete either way, so reading 64 of
		// 300 keys states 64 true things instead of none. The budget is
		// what keeps a props record's member walk off the sweep's clock,
		// and the checker's own property order is what picks them.
		budget := members
		if len(budget) > 64 {
			budget = budget[:64]
		}
		var keys []abstractdomain.ObjectKey
		seededAny := false
		for _, member := range budget {
			if strings.HasPrefix(member.Name, "__") {
				continue
			}
			site := member.ValueDeclaration
			if site == nil && len(member.Declarations) > 0 {
				site = member.Declarations[0]
			}
			if site == nil {
				site = at
				*usedAt = true
			}
			memberType := c.GetTypeOfSymbolAtLocation(member, site)
			if memberType == nil {
				continue
			}
			inner, ok := readHostTypeMemoized(c, memberType, at, depth+1, usedAt)
			if !ok {
				continue
			}
			if (member.Flags & ast.SymbolFlagsOptional) != 0 {
				inner = abstractdomain.PossiblyUndefined(inner, "", false, false)
			}
			keys = append(keys, abstractdomain.ObjectKey{Name: member.Name, Value: inner})
			seededAny = true
		}
		// no member read leaves no key to state — the record answers
		// nothing, which is exactly what it holds.
		if !seededAny {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false), true
	}
	return abstractdomain.AbstractValue{}, false
}

// numberLiteralValueOf reads a number-literal type's own value. tsgo
// stores it as jsnum.Number (checker/types.go's `value any // string |
// jsnum.Number | bool | PseudoBigInt`), which is a NAMED float64 type
// -- a plain `.(float64)` assertion never matches it, so the literal
// branch answered nothing for every number literal, enum members
// included. The nil case is a computed enum member, whose value the
// checker never pinned.
func numberLiteralValueOf(t *checker.Type) (float64, bool) {
	switch value := t.AsLiteralType().Value().(type) {
	case jsnum.Number:
		return float64(value), true
	case float64:
		return value, true
	}
	return 0, false
}

// enumDeclarationOf is the enum DECLARATION behind an enum type. The
// checker hangs the enum's own symbol on the union it builds for the
// members (getDeclaredTypeOfEnum), and a member literal carries the
// member symbol, whose parent declaration is the same enum -- so both
// the whole enum and one member reach the declaration the values
// reader reads.
func enumDeclarationOf(t *checker.Type) *ast.EnumDeclaration {
	symbol := t.Symbol()
	if symbol == nil {
		return nil
	}
	for _, d := range symbol.Declarations {
		if ast.IsEnumDeclaration(d) {
			return d.AsEnumDeclaration()
		}
		if ast.IsEnumMember(d) && d.Parent != nil && ast.IsEnumDeclaration(d.Parent) {
			return d.Parent.AsEnumDeclaration()
		}
	}
	return nil
}

// sortGroundOf is the widest reading of a type the reader could not
// spell: where tsc states the type's SORT, that sort's whole ground is
// true of every value the type admits. A type naming no scalar sort
// answers nothing -- there is no set wider than the unknown, and the
// union of a stated set with the unknown IS the unknown.
func sortGroundOf(t *checker.Type) (abstractdomain.AbstractValue, bool) {
	flags := t.Flags()
	switch {
	case (flags & checker.TypeFlagsStringLike) != 0:
		return StringGround(), true
	case (flags & checker.TypeFlagsNumberLike) != 0:
		return NumberWithNaN(), true
	case (flags & checker.TypeFlagsBooleanLike) != 0:
		return BooleanCodes(), true
	}
	return abstractdomain.AbstractValue{}, false
}
