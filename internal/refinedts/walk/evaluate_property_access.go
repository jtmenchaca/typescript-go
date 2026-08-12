// from evaluation/evaluate_property_access.ts
//
// Property and element reads off the EVALUATED receiver value:
// enum members, the Number and Math constants, `this` reads
// through class field invariants, key reads on known objects, and
// element reads with in-bounds proofs. Split from
// evaluate_expression.ts per the v2 tree — each reader answers, or
// returns null and evaluateForm's chain moves on.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ReadPropertyAccess is readPropertyAccess in the TS source.
func ReadPropertyAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if enumMember := ReadEnumMemberAccess(ctx, env, e); enumMember != nil {
		return enumMember
	}
	if globalConstant := ReadGlobalConstantAccess(ctx, e); globalConstant != nil {
		return globalConstant
	}
	if thisProperty := ReadThisPropertyAccess(ctx, env, e); thisProperty != nil {
		return thisProperty
	}
	if objectKey := ReadObjectKeyAccess(ctx, env, e); objectKey != nil {
		return objectKey
	}
	// reading a sequence's length: a tracked name reads its held value,
	// and ANY OTHER receiver expression evaluates once right here — a
	// literal, a call result, a nested read — so `"abc".length` and
	// `Object.keys(o).length` answer exactly like a named binding's
	if ast.IsPropertyAccessExpression(e) {
		pa := e.AsPropertyAccessExpression()
		if pa.Name().Text() == "length" || pa.Name().Text() == "size" {
			var receiver abstractdomain.AbstractValue
			if ast.IsIdentifier(pa.Expression) {
				if held, ok := env[pa.Expression.Text()]; ok {
					receiver = held
				} else {
					receiver = silence.Residue()
				}
			} else {
				receiver = evaluateExpression(ctx, env, pa.Expression)
			}
			// `.size` belongs to collections alone; `.length` to the rest
			if pa.Name().Text() == "size" {
				if receiver.Kind == abstractdomain.KindCollection {
					if receiver.Complete {
						out := abstractdomain.KnownValues([]float64{float64(len(receiver.Entries))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(receiver))
						return &out
					}
					out := abstractdomain.KnownSet(
						refinementsets.MakeRefinedSet(refinementsets.AtLeast(float64(len(receiver.Entries))), refinementsets.Integer),
						nil, abstractdomain.TrustLevelOf(receiver), abstractdomain.SetKindTagNone,
					)
					return &out
				}
				out := silence.Residue()
				return &out
			}
			if receiver.Kind == abstractdomain.KindValues {
				// a string's `.length` counts UTF-16 code units, not scalar
				// values — exact for a known tuple: each astral scalar is two
				var length int
				if primitives.IsStringKind(ctx.P.Checker, pa.Expression) || receiver.KindTag == abstractdomain.PrimitiveString {
					length = refinementsets.Utf16LengthOf(receiver.Values)
				} else {
					length = len(receiver.Values)
				}
				out := abstractdomain.KnownValues([]float64{float64(length)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
				return &out
			}
			if receiver.Kind == abstractdomain.KindList {
				out := abstractdomain.KnownValues([]float64{float64(len(receiver.Items))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
				return &out
			}
			if receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone {
				// a difference's members are all members of its minuend, so the
				// minuend's length window bounds every one of them — a pattern-
				// narrowed string (`!/[%+]/.test(key)`) keeps its length read
				carrier := receiver.Set
				for {
					var only *refinementsets.Refinement
					if len(carrier.Forms) == 1 {
						only = &carrier.Forms[0]
					}
					if only == nil || only.Form != refinementsets.FormDifference {
						break
					}
					carrier = *only.A_
				}
				rep, ok := refinementsets.AsRepetition(carrier)
				if ok {
					// a string's length counts UTF-16 units — at least one per
					// scalar, two for astrals — so only the floor survives there
					stringy := primitives.IsStringKind(ctx.P.Checker, pa.Expression)
					var set refinementsets.RefinedSet
					if stringy || rep.Hi == nil {
						set = refinementsets.MakeRefinedSet(refinementsets.AtLeast(float64(rep.Lo)), refinementsets.Integer)
					} else {
						set = refinementsets.MakeRefinedSet(refinementsets.AtLeast(float64(rep.Lo)), refinementsets.AtMost(float64(*rep.Hi)), refinementsets.Integer)
					}
					out := abstractdomain.KnownSet(set, nil, abstractdomain.TrustLevelOf(receiver), abstractdomain.SetKindTagNone)
					return &out
				}
			}
			// an unknown receiver keeps the general path's own answers: the
			// web platform getters, and opacity carrying through the read
			if receiver.Kind == abstractdomain.KindUnknown {
				if web := WebPropertyRead(ctx.P, e); web != nil {
					return web
				}
				if receiver.Opaque {
					out := abstractdomain.Opaque
					return &out
				}
			}
			// the PLACE-VALUE memory: a dotted entry a guard recorded
			// (assume_condition) answers where the receiver's own shape
			// could not — `Array.isArray(u)` grounded u.length, and the
			// branch's comparisons narrowed it in place
			if ast.IsIdentifier(pa.Expression) {
				if held, ok := env[pa.Expression.Text()+"."+pa.Name().Text()]; ok {
					return &held
				}
			}
			out := silence.Residue()
			return &out
		}
	}
	return nil
}

// ReadElementAccess is readElementAccess in the TS source.
func ReadElementAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	return ElementAccessOf(ctx, env, e)
}
