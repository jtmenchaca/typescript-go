// TS enum member VALUES as one AbstractValue. Numeric members join
// as exact number words; all-string enums as the union of their
// tuples; mixed or computed reads as nothing.

package typereading

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// EnumValuesState is enumValuesState in the TS source. Returns
// (value, true) on a determined enum, (zero, false) when the
// declaration mixes number and string members, or holds a computed
// initializer (the TS source's null result).
func EnumValuesState(declaration *ast.EnumDeclaration) (abstractdomain.AbstractValue, bool) {
	var numbersSeen []float64
	var stringsSeen []string
	next := 0.0
	for _, member := range declaration.Members.Nodes {
		enumMember := member.AsEnumMember()
		initializer := enumMember.Initializer
		if initializer == nil {
			numbersSeen = append(numbersSeen, next)
			next += 1
			continue
		}
		if ast.IsNumericLiteral(initializer) {
			v := float64(jsnum.FromString(initializer.AsNumericLiteral().Text))
			numbersSeen = append(numbersSeen, v)
			next = v + 1
			continue
		}
		if ast.IsStringLiteral(initializer) {
			stringsSeen = append(stringsSeen, initializer.AsStringLiteral().Text)
			continue
		}
		return abstractdomain.AbstractValue{}, false
	}
	if len(numbersSeen) > 0 && len(stringsSeen) > 0 {
		return abstractdomain.AbstractValue{}, false
	}
	if len(numbersSeen) > 0 {
		return abstractdomain.KnownValues(numbersSeen, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	if len(stringsSeen) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	set := refinementsets.StringTuple(stringsSeen[0])
	for _, s := range stringsSeen[1:] {
		set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(s)))
	}
	return abstractdomain.KnownSet(set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone), true
}
