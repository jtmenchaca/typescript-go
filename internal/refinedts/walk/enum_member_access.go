// from evaluation/enum_member_access.ts
//
// Enum member reads and Enum[x] reverse reads. A member tsc cannot
// pin stays residue; the reverse read images the whole enum so an
// `=== undefined` guard can prove the survivors.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// numberLiteralValueOf reads a number-literal type's own value. tsgo
// stores it as jsnum.Number (checker/types.go's `value any // string |
// jsnum.Number | bool | PseudoBigInt`), a NAMED float64 type -- a
// plain `.(float64)` assertion never matches it. False where the
// checker pinned no value (a computed enum member).
func numberLiteralValueOf(t *checker.Type) (float64, bool) {
	switch value := t.AsLiteralType().Value().(type) {
	case jsnum.Number:
		return float64(value), true
	case float64:
		return value, true
	}
	return 0, false
}

// ReadEnumMemberAccess is readEnumMemberAccess in the TS source.
func ReadEnumMemberAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	// an enum member read: tsc itself holds the member's literal —
	// the same source `typeofWordOf` reads, at the same spec grade.
	// A member tsc cannot pin (a computed member over an opaque
	// expression) stays unknown.
	if ast.IsPropertyAccessExpression(e) {
		pa := e.AsPropertyAccessExpression()
		// followed through the import alias: an enum used where it is
		// imported (nest's RequestMethod at every decorator) is the same
		// enum, and its members pin the same literals. Receiver need not
		// be a bare identifier — `ns.RequestMethod.GET` is the same enum.
		receiverSymbol := symbolAt(ctx.P.Checker, pa.Expression)
		isEnum := false
		if receiverSymbol != nil {
			for _, d := range receiverSymbol.Declarations {
				if ast.IsEnumDeclaration(d) {
					isEnum = true
					break
				}
			}
		}
		if isEnum {
			memberType := ctx.P.Checker.GetTypeAtLocation(e)
			if memberType.IsNumberLiteral() {
				// tsgo stores a number literal's value as jsnum.Number, a
				// NAMED float64 -- a plain float64 assertion never matched
				// it, so every numeric enum member read 0 instead of its
				// own value. A member the checker never pinned (a computed
				// one) has no value at all and stays residue below.
				v, ok := numberLiteralValueOf(memberType)
				if !ok {
					out := silence.Residue()
					return &out
				}
				out := abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
				return &out
			}
			if memberType.IsStringLiteral() {
				v, _ := memberType.AsLiteralType().Value().(string)
				out := abstractdomain.KnownValues(refinementsets.CodepointsOf(v), abstractdomain.PrimitiveString, abstractdomain.TrustSpec)
				return &out
			}
			out := silence.Residue()
			return &out
		}
	}
	// Enum[x] — the reverse read on the enum OBJECT: a numeric enum
	// compiles two-way, so the read is a member VALUE (x named a
	// member), a member NAME (x spelled a value), or undefined; a
	// string enum compiles one-way, so it is a value or undefined.
	// The whole image, exactly — what lets an `=== undefined` guard
	// downstream prove the survivors sit in the enum.
	if ast.IsElementAccessExpression(e) {
		elem := e.AsElementAccessExpression()
		receiverSymbol := symbolAt(ctx.P.Checker, elem.Expression)
		var enumDeclaration *ast.Node
		if receiverSymbol != nil {
			for _, d := range receiverSymbol.Declarations {
				if ast.IsEnumDeclaration(d) {
					enumDeclaration = d
					break
				}
			}
		}
		if enumDeclaration != nil {
			evaluateExpression(ctx, env, elem.ArgumentExpression)
			values, ok := typereading.EnumValuesState(enumDeclaration.AsEnumDeclaration())
			if !ok {
				out := silence.Residue()
				return &out
			}
			if values.Kind == abstractdomain.KindSet {
				out := abstractdomain.PossiblyUndefined(values, "", false, false)
				return &out
			}
			members := enumDeclaration.AsEnumDeclaration().Members.Nodes
			var names []string
			allNamed := true
			for _, member := range members {
				name := member.AsEnumMember().Name()
				if ast.IsIdentifier(name) {
					names = append(names, name.Text())
				} else {
					allNamed = false
					break
				}
			}
			if !allNamed {
				out := silence.Residue()
				return &out
			}
			nameSet := refinementsets.StringTuple(names[0])
			for _, name := range names[1:] {
				nameSet = refinementsets.MakeRefinedSet(refinementsets.Union(nameSet, refinementsets.StringTuple(name)))
			}
			out := abstractdomain.PossiblyUndefined(
				abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
					values,
					abstractdomain.KnownSet(nameSet, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
				}),
				"", false, false,
			)
			return &out
		}
	}
	return nil
}
