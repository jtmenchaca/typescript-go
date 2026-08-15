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
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// HeldPlaceEntry reads the PLACE-VALUE memory for one access: the
// value a dotted key holds for the place the expression spells.
//
// The writers — assume_condition's applySide, whose narrowings on a
// path the root cannot absorb land under the dotted key, and
// loop_push_growth's place-keyed growth, whose grown sequence for
// `this.items` lands the same way — spell the key by joining the
// binding and every path segment with dots. This reads the same
// spelling back. Segments come from dataflowfacts.TrackedPlaceOf, so
// `o.total`, `this.items`, `o["k"]` and `xs[0]` all name the place
// their writer named; a receiver the place reading cannot resolve
// names none, and this answers nothing.
//
// The answer is a FLOW-CURRENT claim: it holds at this point of the
// walk and dies when the flow moves, which every write and havoc
// enforces by sweeping the root's dotted prefix
// (ForgetPlaceEntriesEnv, reached through HavocEnv, UpdateTrackedEnv,
// WriteBinding, and ForgetThisHeld for the `this` root).
func HeldPlaceEntry(c *checker.Checker, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	// the checker-carrying place reading resolves a const-bound index
	// (`const i = 0; xs[i]`) to the same slot the literal spells
	place := dataflowfacts.TrackedPlaceOfWith(c, e, func(name string) bool {
		_, held := env.Get(name)
		return held
	})
	if place == nil || len(place.Path) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	key := place.Binding
	for _, segment := range place.Path {
		key += "." + segment
	}
	return env.Get(key)
}

// meetHeldPlaceEntry combines a reader's own answer with the dotted
// entry the place-value memory holds for the same access. Both are
// claims about the ONE value the read names — the reader's is the
// receiver's own shape (a class field invariant, an object's key), the
// entry's is what the flow narrowed or grew in place — so they MEET
// rather than shadow each other. With no entry held, the reader's
// answer stands untouched.
func meetHeldPlaceEntry(c *checker.Checker, env Env, e *ast.Node, answer *abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	held, ok := HeldPlaceEntry(c, env, e)
	if !ok {
		return answer
	}
	if answer == nil {
		return &held
	}
	out := abstractdomain.MeetKnown(*answer, held)
	return &out
}

// ReadPropertyAccess is readPropertyAccess in the TS source.
func ReadPropertyAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if enumMember := ReadEnumMemberAccess(ctx, env, e); enumMember != nil {
		return enumMember
	}
	if globalConstant := ReadGlobalConstantAccess(ctx, e); globalConstant != nil {
		return globalConstant
	}
	// `this.key` and a known object's key each answer off the receiver,
	// and the dotted entry speaks about the same value — so the two
	// meet here rather than the earlier reader shadowing the memory.
	// An enum member and a global constant name no tracked place, so
	// they answer above this point untouched.
	if thisProperty := ReadThisPropertyAccess(ctx, env, e); thisProperty != nil {
		return meetHeldPlaceEntry(ctx.P.Checker, env, e, thisProperty)
	}
	if objectKey := ReadObjectKeyAccess(ctx, env, e); objectKey != nil {
		return meetHeldPlaceEntry(ctx.P.Checker, env, e, objectKey)
	}
	// reading a sequence's length: a tracked name reads its held value,
	// and ANY OTHER receiver expression evaluates once right here — a
	// literal, a call result, a nested read — so `"abc".length` and
	// `Object.keys(o).length` answer exactly like a named binding's
	if ast.IsPropertyAccessExpression(e) {
		pa := e.AsPropertyAccessExpression()
		if pa.Name().Text() == "length" || pa.Name().Text() == "size" {
			// the receiver's held value comes from the PLACE it spells —
			// a tracked name reads its own entry, and a longer path
			// (`this.items.length`, `o.rows.length`) reads the dotted
			// entry the place-value memory holds for that path. Any other
			// receiver expression evaluates.
			var receiver abstractdomain.AbstractValue
			if ast.IsIdentifier(pa.Expression) {
				if held, ok := env.Get(pa.Expression.Text()); ok {
					receiver = held
				} else {
					receiver = silence.Residue()
				}
			} else if held, ok := HeldPlaceEntry(ctx.P.Checker, env, pa.Expression); ok {
				receiver = held
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
			// an OBJECT-STAR states no count, so its length answers only
			// what the sort itself guarantees: an array's length is always
			// a non-negative integer below 2^32 (sec-array-exotic-objects'
			// ArraySetLength rejects anything else). That is a real claim —
			// it decides `length >= 0` and every negative comparison — and
			// it is the whole of what the form knows.
			if receiver.Kind == abstractdomain.KindObjectStar {
				out := abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(
						refinementsets.Integer,
						refinementsets.AtLeast(0),
						refinementsets.AtMost(4294967295),
					),
					nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
				)
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
			// branch's comparisons narrowed it in place. The place reading
			// spells the key, so a `this`-rooted receiver
			// (`this.items.length`) reads its entry the same way a plain
			// name's does.
			if held, ok := HeldPlaceEntry(ctx.P.Checker, env, e); ok {
				return &held
			}
			out := silence.Residue()
			return &out
		}
	}
	// no arm above answered: a dotted entry for this very access still
	// speaks — a guard's narrowing on `o.total` where the root holds no
	// object shape to absorb it, or the sequence a push loop grew under
	// `this.items`. Only a PROPERTY access answers here; an element
	// access falls through to readElementAccess, whose own readers run
	// before its dotted entry is asked for.
	if !ast.IsPropertyAccessExpression(e) {
		return nil
	}
	return meetHeldPlaceEntry(ctx.P.Checker, env, e, nil)
}

// ReadElementAccess is readElementAccess in the TS source.
func ReadElementAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	answer := ElementAccessOf(ctx, env, e)
	// an element read spells a place too when its index is a literal or
	// a const resolving to one (`xs[0]`, `o["k"]`, `xs[I]`): the same
	// dotted entry the property path reads, meeting whatever the
	// element readers answered off the receiver's own shape
	if !ast.IsElementAccessExpression(e) {
		return answer
	}
	return meetHeldPlaceEntry(ctx.P.Checker, env, e, answer)
}
