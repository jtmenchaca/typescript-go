// from evaluation/object_key_access.ts
//
// Known-object key reads, host-function data properties, T-bound
// object keys, and web/opaque fallbacks for property access.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
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
	// The bound object arrives as an opaque ObjectAnnotationRef —
	// abstractdomain holds the token and compares it by identity only.
	// objectAnnotationOf (declared_value.go) walks the token back to
	// the *annotations.ObjectAnnotation it was minted from; this file
	// imports both packages, so the read lives here.
	if receiver.Kind == abstractdomain.KindVariable && receiver.StarDepth == 0 && receiver.BoundObject != nil {
		if bound := objectAnnotationOf(receiver.BoundObject); bound != nil {
			for _, key := range bound.Keys {
				if key.Name != pa.Name().Text() {
					continue
				}
				// only a SET key states a value the read can wear; an
				// object, collection, or reference key states a shape
				// this arm holds nothing about
				inner := silence.Residue()
				if key.Value.Kind == annotations.KeyValueSet {
					inner = abstractdomain.KnownSet(*key.Value.Set, nil, abstractdomain.TrustProved, setKindTagOf(key.Value.KindTag))
				}
				if inner.Kind != abstractdomain.KindUnknown {
					// a key the statement lets be absent reads as the
					// maybe wrapper, exactly as declared_value.go builds it
					if key.MayBeAbsent {
						out := abstractdomain.PossiblyUndefined(inner, "", false, false)
						return &out
					}
					return &inner
				}
				break
			}
		}
	}
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
