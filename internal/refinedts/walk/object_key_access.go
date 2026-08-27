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
					return memberValueGraded(inner, inner.Keys[idx].Value)
				}
				return silence.Residue()
			}
			return silence.Residue()
		})
		if threaded != nil {
			return threaded
		}
	}
	// a PLAIN (non-optional) key read off a MAYBE receiver reads the
	// PRESENT side, with no wrapper on the result.
	//
	// sec-property-accessors: `MemberExpression . IdentifierName`
	// evaluates through EvaluatePropertyAccessWithIdentifierKey, whose
	// step 3 is "? RequireObjectCoercible(baseValue)" ahead of the
	// [[Get]]. sec-requireobjectcoercible's table throws a TypeError for
	// undefined and for null. So the absent side never yields a value at
	// this read AT ALL — it leaves through the throw — and the only side
	// that reaches the property is the present one. Wrapping the result
	// in a maybe would state that the read can answer undefined, which
	// this access can never do; and answering nothing (the behaviour this
	// arm replaces) left `o!.a` and `(o as {a: number}).a` undetermined
	// even where the present side is exactly known.
	//
	// The optional-chain arm above keeps its precedence: `o?.a` DOES
	// short-circuit to undefined rather than throwing, so it wears the
	// wrapper and this arm is not reached for it.
	//
	// The peel is deliberately NARROW — a present side that is a known
	// object ALREADY CARRYING the named key. That is the whole case this
	// arm exists for, and stopping there keeps every other maybe-wrapped
	// receiver falling through to the readers that already own it: an
	// element/hole read whose wrapper is a claim about the RESULT rather
	// than the receiver (element_in_bounds.go's proved-in-bounds arm,
	// where the length can count holes) must keep its wrapper, and a
	// broader peel silently took it.
	if receiver.Kind == abstractdomain.KindPossiblyUndefined && receiver.Inner != nil {
		if present := *receiver.Inner; present.Kind == abstractdomain.KindObject {
			if _, carriesKey := objectKeyIndex(present, pa.Name().Text()); carriesKey {
				receiver = present
			}
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
	// a LIST's own non-index properties: an array object carries named
	// properties beside its indexed slots, and a match array's `groups`
	// is the one the walk builds (regex_exec_capture.go's
	// withNamedGroups). Only a name the list actually carries answers —
	// the list states no complete key set, so any other name falls
	// through to the readers below unchanged.
	if receiver.Kind == abstractdomain.KindList && len(receiver.Keys) > 0 {
		if idx, ok := objectKeyIndex(receiver, pa.Name().Text()); ok {
			out := memberValueGraded(receiver, receiver.Keys[idx].Value)
			return &out
		}
	}
	if receiver.Kind == abstractdomain.KindObject {
		if idx, ok := objectKeyIndex(receiver, pa.Name().Text()); ok {
			out := memberValueGraded(receiver, receiver.Keys[idx].Value)
			return &out
		}
		// the OPEN-MAP gate the element read carries (element_access.go's
		// OpenMapAt): a receiver whose declared type has an index signature
		// names no fixed key set, so a missing key is not a definite
		// absence there. The parameter binding now strips the completeness
		// an open-map-typed parameter never earned, which makes this
		// defense in depth — an evaluation-path object can still carry one
		// call site's completeness through a route that crosses no
		// parameter binding, and this is where it meets its own type.
		if receiver.Complete && !openMapReceiver(ctx, pa.Expression) {
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
		out := silence.ResidueOf("the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent")
		return &out
	}
	// a SORT UNION receiver whose own residue already names why it is a
	// union of arms rather than one shape (JSON.parse's own grammar
	// claim on unpinned text, coercion_models_json.go's anyJSONValue) —
	// a key read off it inherits that same reason: the read cannot pick
	// a key set until one arm is picked, and the receiver already states
	// why none was.
	if receiver.Kind == abstractdomain.KindKindUnion && receiver.ResidueReason != "" {
		out := silence.ResidueOf(receiver.ResidueReason)
		return &out
	}
	// a SORT UNION receiver with no residue of its own (a discriminated
	// object type after `"r" in s` or its negation): the guard's absence
	// proof for this key lands as a dotted place-value entry, not a
	// rebuild of the union itself — apply_narrowing.go's narrowAt only
	// turns a Definedness("undefined") test into Undef when the place
	// already held the PossiblyUndefined or Undef wrapper; a bare unknown
	// dotted entry (the ordinary starting state for a path no root
	// absorbed) is handed back unchanged. So the guard is real but
	// nothing here converts it into a set at this key, and the union
	// receiver itself carries no key to fall back on either.
	if receiver.Kind == abstractdomain.KindKindUnion {
		out := silence.ResidueOf("the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent")
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

// memberValueGraded is the property-access twin of
// return_type_ground.go's typeGroundOf grade stamp: a member type read
// off a DECLARATION-BACKED receiver is exactly as declaration-backed as
// the receiver itself, so the member's own claim is capped at the
// receiver's floor — AtTrustLevel only ever LOWERS (MinTrustLevel), so
// a member the walk separately proved to a WEAKER grade than the
// receiver (crossed its own boundary on the way here) keeps that
// weaker floor rather than being raised back up to the receiver's.
//
// The rule reads off the RECEIVER's own Grade field, not off a fresh
// declaration lookup at the member: receiver.Grade != "" is precisely
// the signal a checked declaration's own type ground left behind
// (typeGroundOf's AtTrustLevel(united, TrustLibrary) stamp on a call
// like `device()`'s return, or any other reader that already graded
// the whole object). An ORDINARY object the walk built itself — an
// object literal, a plain narrowed value — carries no such stamp
// (KnownObject's own floor computation only sets Grade when a member
// or the constructor's own grade argument already reads below
// TrustProved), so its members pass through untouched: nothing invents
// a grade for a receiver that is itself an ungraded residue or a
// plainly-proved value. This is the same split nan_wrapper.go's
// CheckPossiblyNaN already keys on (`known.Grade != ""`) — a graded
// wrapper is a served claim about a checked declaration; an ungraded
// one is silence.AfterReaders' own fallback seed for something the
// walk never examined, and must not be dressed up as more than that.
func memberValueGraded(receiver, member abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if receiver.Grade == "" {
		return member
	}
	return abstractdomain.AtTrustLevel(member, receiver.Grade)
}
