// from evaluation/unmodeled_call_result.ts
//
// What an unmodeled call still wears after every modeled path has
// declined: the resolved return annotation, the default-library sort
// ground, the opaque/bodiless outside verdict, and the reason note.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// UnmodeledCallResult is unmodeledCallResult in the TS source.
func UnmodeledCallResult(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	// a call whose RESULT is a generator or an iterable that reached
	// HERE — the contract lookup did not place its declaration, so the
	// generator route in evaluate_call_expression.go never saw it, but
	// the call's own resolved type still says `Generator<T>` /
	// `IterableIterator<T>`. The value is the iterator object, which
	// nothing in the tuple layer or the object graph spells, so the
	// reading is the opaque one: it entered from outside this walk's
	// determination. Without this row the ground reader below would
	// spell the iterator as a record of its `next`/`return`/`throw`
	// members — true, and useless, and it would stand in the way of the
	// element readings the drain routes take through the same type.
	if _, isIterator := generatorDeclaredElement(ctx, e); isIterator {
		return abstractdomain.Opaque
	}
	// an unmodeled call whose RESOLVED return type reads as a stated
	// annotation wears the annotation: `ports.get(k)` typed
	// `Port | undefined` with Port = z.infer<typeof zPort> answers
	// the stated set, or absent. The precedent is the parametric
	// rule above — the claim is the declaration's, so it carries
	// LIBRARY grade, and a refutation through it still refutes.
	if worn := AnnotationOfReturnType(ctx, e); worn != nil {
		return *worn
	}
	// an unmodeled DEFAULT-LIBRARY call still answers its return
	// type's sort ground where tsc states one cleanly — setTimeout
	// is a number, whatever the host does with it. The claim rests
	// on tsc's checking, the sort layer's own trust.
	if CalleeInDefaultLib(ctx, call.Expression) {
		if ground := ReturnTypeGround(ctx, e); ground != nil {
			return *ground
		}
	}
	// the reason, said: a default-library callee with no model is
	// work the checker owes; a user's ambient declaration has no body
	// anywhere in the file, so the type is everything the file
	// determines — the checker is done there. Annotation-surface
	// calls are the annotation reader's, not this walk's — no note.
	if assignability.CollectingReasons() && !CalleeInSurface(ctx, call.Expression) {
		callee := CalleeWords(call.Expression)
		// `f.bind(...)` with f's body in reach: the bound function is
		// modeled at its consumption sites (map callbacks, direct
		// calls), so this declaration itself owes nothing more
		if BoundFunctionOf(ctx, e) != nil {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        callee + "() stores a bound function — its body reads at each call",
				Unsupported: false,
			})
		} else if CalleeInDefaultLib(ctx, call.Expression) {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        callee + "() is not modeled",
				Unsupported: true,
			})
		} else {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        callee + "() has no body to read — the type is everything this file determines",
				Unsupported: false,
			})
		}
	}
	// the opaque readings below still wear the sort ground the call's
	// RESOLVED return type states cleanly, at LIBRARY grade — the
	// claim is the declaration's, the same standing the
	// stated-annotation reading above and the parametric rule both
	// rest on, and a refutation through it still refutes. An imported
	// `useAppSelector` typed to hand back its selector's own literal
	// union wears that union whether the binding entered the
	// environment opaque or the callee spells no body. A return type
	// the ground reader cannot spell keeps the opaque reading: the
	// value entered from outside the file's determination, so reads
	// through it stay opaque (join.ts OPAQUE) instead of counting as
	// the walk's gap.
	opaqueWorn := func() abstractdomain.AbstractValue {
		if ground := ReturnTypeGround(ctx, e); ground != nil {
			return abstractdomain.AtTrustLevel(*ground, abstractdomain.TrustLibrary)
		}
		return abstractdomain.Opaque
	}
	// a call through an OPAQUE value — a function held in a binding
	// that entered from outside, or a member of one (`date[key](…)`):
	// the function itself is outside the file's determination, so its
	// result is too
	calleeRoot := call.Expression
	if ast.IsPropertyAccessExpression(call.Expression) {
		calleeRoot = call.Expression.AsPropertyAccessExpression().Expression
	} else if ast.IsElementAccessExpression(call.Expression) {
		calleeRoot = call.Expression.AsElementAccessExpression().Expression
	}
	if ast.IsIdentifier(calleeRoot) {
		if held, ok := env.Get(calleeRoot.Text()); ok && held.Kind == abstractdomain.KindUnknown && held.Opaque {
			return opaqueWorn()
		}
	}
	// a call rooted at `super` — `super.m(…)` or a derived
	// constructor's `super(…)` — that reached HERE: the base body was
	// not read, so what comes back enters from outside this walk's
	// determination exactly as a call through an opaque value does.
	//
	// A super call whose base declaration super_binding.go resolves to a
	// held contract no longer reaches here at all: evaluate_call_expression
	// hands it the inline route, which reads the caller's `this` as the
	// receiver (SummaryCallReceiver) and forgets through it with
	// ForgetThisHeld. The calls still landing here are the ones that
	// route declines — a base with no extends clause this walk can
	// follow, a base outside the checked files, a computed `super[k]`
	// member, an overridden base method, a bare `super(…)` whose
	// constructor spells no name to key an inline on. The
	// stated-annotation and default-library readings above still run
	// first, so a super call whose return type spells an annotation
	// wears it rather than reaching here.
	if calleeRoot.Kind == ast.KindSuperKeyword {
		return opaqueWorn()
	}
	// a callee with NO BODY anywhere in reach — a .d.ts signature, or
	// an import from an unresolved module: the same worn-or-opaque
	// reading (opaqueWorn's own doc)
	if BodilessCallee(ctx, call.Expression) && !CalleeInDefaultLib(ctx, call.Expression) {
		return opaqueWorn()
	}
	// the call that reached HERE has a BODY in reach — no model read it,
	// no contract held it, no inline replayed it — so its declared
	// return type is a claim tsc itself checked that body against. That
	// is the same standing the default-library branch above rests on
	// ("the claim rests on tsc's checking, the sort layer's own trust"),
	// and it holds for a constructed sort as much as a scalar one: a
	// body returning a record is checked to return that record's shape.
	//
	// ReturnTypeGround reads the constructed sorts through the resolved-
	// type reader, so an object return answers an INCOMPLETE object —
	// the shape is present and no key beyond what that reader read is
	// claimed. A return type it cannot spell still answers nil here and
	// the residue below stands.
	if ground := ReturnTypeGround(ctx, e); ground != nil {
		return *ground
	}
	return silence.Residue()
}
