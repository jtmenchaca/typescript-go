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
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

const absentFlags = checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid

// ReadHostType is readHostType in the TS source.
func ReadHostType(c *checker.Checker, t *checker.Type, at *ast.Node, depth int) (abstractdomain.AbstractValue, bool) {
	flags := t.Flags()
	if (flags & checker.TypeFlagsStringLiteral) != 0 {
		value, ok := t.AsLiteralType().Value().(string)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(value), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	if (flags & checker.TypeFlagsNumberLiteral) != 0 {
		value, ok := t.AsLiteralType().Value().(float64)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownValues([]float64{value}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	if (flags & checker.TypeFlagsBooleanLiteral) != 0 {
		name := t.AsIntrinsicType().IntrinsicName()
		if name != "true" && name != "false" {
			return abstractdomain.AbstractValue{}, false
		}
		bit := 0.0
		if name == "true" {
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
		return ReadHostType(c, constrained, at, depth+1)
	}
	if (flags&checker.TypeFlagsUnion) != 0 && depth < 3 {
		var arms []abstractdomain.AbstractValue
		sawAbsent := false
		for _, part := range t.Types() {
			if (part.Flags() & absentFlags) != 0 {
				sawAbsent = true
				continue
			}
			inner, ok := ReadHostType(c, part, at, depth+1)
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			arms = append(arms, inner)
		}
		return PresentUnion(arms, sawAbsent)
	}
	if (flags&(checker.TypeFlagsObject|checker.TypeFlagsIntersection)) != 0 && depth < 2 {
		if len(c.GetCallSignatures(t)) > 0 || len(c.GetConstructSignatures(t)) > 0 {
			return abstractdomain.HostFunction, true
		}
		if c.IsArrayLikeType(t) {
			slots := c.GetTypeArguments(t)
			if len(slots) == 0 {
				return abstractdomain.AbstractValue{}, false
			}
			var element abstractdomain.AbstractValue
			haveElement := false
			for _, slot := range slots {
				inner, ok := ReadHostType(c, slot, at, depth+1)
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
		if c.SymbolInDefaultLib(t.Symbol()) {
			return abstractdomain.AbstractValue{}, false
		}
		members := c.GetPropertiesOfType(t)
		if len(members) == 0 || len(members) > 64 {
			return abstractdomain.AbstractValue{}, false
		}
		var keys []abstractdomain.ObjectKey
		seededAny := false
		for _, member := range members {
			if strings.HasPrefix(member.Name, "__") {
				continue
			}
			site := member.ValueDeclaration
			if site == nil && len(member.Declarations) > 0 {
				site = member.Declarations[0]
			}
			if site == nil {
				site = at
			}
			memberType := c.GetTypeOfSymbolAtLocation(member, site)
			if memberType == nil {
				continue
			}
			inner, ok := ReadHostType(c, memberType, at, depth+1)
			if !ok {
				continue
			}
			if (member.Flags & ast.SymbolFlagsOptional) != 0 {
				inner = abstractdomain.PossiblyUndefined(inner, "", false, false)
			}
			keys = append(keys, abstractdomain.ObjectKey{Name: member.Name, Value: inner})
			seededAny = true
		}
		if !seededAny {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false), true
	}
	return abstractdomain.AbstractValue{}, false
}
