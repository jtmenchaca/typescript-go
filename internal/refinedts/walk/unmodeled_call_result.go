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
		if held, ok := env[calleeRoot.Text()]; ok && held.Kind == abstractdomain.KindUnknown && held.Opaque {
			return abstractdomain.Opaque
		}
	}
	// a callee with NO BODY anywhere in reach — a .d.ts signature, or
	// an import from an unresolved module: its result ENTERS from
	// outside the file's determination, so reads through it stay
	// opaque (join.ts OPAQUE) instead of counting as the walk's gap
	if BodilessCallee(ctx, call.Expression) && !CalleeInDefaultLib(ctx, call.Expression) {
		return abstractdomain.Opaque
	}
	return silence.Residue()
}
