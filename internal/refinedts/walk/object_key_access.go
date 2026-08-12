// from evaluation/object_key_access.ts
//
// Known-object key reads, host-function data properties, T-bound
// object keys, and web/opaque fallbacks for property access.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ReadObjectKeyAccess is readObjectKeyAccess in the TS source.
func ReadObjectKeyAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	// reading a key off a known object — a COMPLETE key set makes the
	// missing-key read exactly undefined (except the prototype's own
	// members, which every object HAS); an optional chain on the
	// absent value short-circuits to undefined
	if !ast.IsPropertyAccessExpression(e) {
		return nil
	}
	pa := e.AsPropertyAccessExpression()
	receiver := evaluateExpression(ctx, env, pa.Expression)
	if pa.QuestionDotToken != nil {
		threaded := ReadThroughMaybeReceiver(receiver, func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue {
			if inner.Kind == abstractdomain.KindObject {
				if idx, ok := objectKeyIndex(inner, pa.Name().Text()); ok {
					return inner.Keys[idx].Value
				}
				return silence.Residue()
			}
			return silence.Residue()
		})
		if threaded != nil {
			return threaded
		}
	}
	// a FUNCTION's own data properties: length is its arity (a
	// nonnegative integer) and name a string
	// (sec-function-instances) — everything else stays unclaimed
	if receiver.Kind == abstractdomain.KindHostFunction {
		if pa.Name().Text() == "length" {
			out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			return &out
		}
		if pa.Name().Text() == "name" {
			out := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			return &out
		}
		out := silence.Residue()
		return &out
	}
	if receiver.Kind == abstractdomain.KindObject {
		if idx, ok := objectKeyIndex(receiver, pa.Name().Text()); ok {
			out := receiver.Keys[idx].Value
			return &out
		}
		if receiver.Complete {
			// a prototype member every plain object inherits IS a
			// function, never undefined — the collision class the zod
			// survey catalogued (its #5266/#5098); a bare-prototype
			// object (Object.create(null)) inherits nothing
			if receiver.BareProto {
				out := abstractdomain.Undef
				return &out
			}
			if abstractdomain.ObjectPrototypeFunctionKeys[pa.Name().Text()] {
				out := abstractdomain.HostFunction
				return &out
			}
			out := abstractdomain.Undef
			return &out
		}
		// an incomplete object of a WEB class still answers its
		// getters' spec shapes (web.ts)
		if web := WebPropertyRead(ctx.P, e); web != nil {
			return web
		}
		out := silence.Residue()
		return &out
	}
	// a key read THROUGH an object-bounded T wears the key's own
	// statement: every admissible T is a subset of the bound.
	//
	// BLOCKED: reading receiver.boundObject.keys needs the real
	// *annotations.ObjectAnnotation shape. abstractdomain.AbstractValue's
	// BoundObject field is typed ObjectAnnotationRef = *struct{} — an
	// OPAQUE stand-in (abstract_value.go's own comment: "Comparisons in
	// this package only ever check identity... When annotations ports,
	// this becomes *annotations.ObjectAnnotation"). annotations HAS
	// ported ObjectAnnotation now, but abstractdomain's field type is
	// abstractdomain's own file to change, outside this port unit —
	// so this arm cannot read the bound object's keys yet and falls
	// through to the next reader, exactly like a receiver this branch
	// never matched. Every other branch of this function is unaffected.
	// a receiver the walk holds nothing about, whose STATIC type is
	// a web platform class: the getter's spec shape (web.ts)
	if receiver.Kind == abstractdomain.KindUnknown {
		if web := WebPropertyRead(ctx.P, e); web != nil {
			return web
		}
		// a read through an OPAQUE value stays opaque: the whole
		// structure entered from outside the file's determination
		if receiver.Opaque {
			out := abstractdomain.Opaque
			return &out
		}
	}
	return nil
}
