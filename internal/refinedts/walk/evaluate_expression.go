// from evaluation/evaluate_expression.ts
//
// Reading an expression: what value does this expression have, given
// what is known here? Depth bookkeeping lives here; evaluateForm is
// the thin dispatcher that switches on syntax and calls the siblings.
// The imports run in a cycle — an expression contains a function
// body, a body contains statements, a statement contains expressions
// — which is the shape of the language. Every crossing is a hoisted
// function declaration, so the cycle closes at load.

package walk

import (
	"math"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// analysisDepth: how deep THIS check's expression walk has gone. A
// call read by walking its body nests the walk inside itself, so this
// is the honest measure of how far the checker follows code — the
// number `limits.ts` talks about when it discusses CALL_DEPTH. The
// unwind is in a `defer` because a refused kernel question panics
// past here, and a leaked frame would make every later reading look
// deeper than it was.
//
// The TS source's module-level `let` is a single synchronous call
// stack's own counter. Under goroutine-per-entry parallelism that
// counter must be PER-CHECK: one shared package var would let two
// concurrent checks push and pop the same number, so neither reading
// means anything. analysisDepths holds one counter per check, keyed
// on ctx.P (the per-check view pointer, per PORT.md's parallel-sweep
// audit), guarded by analysisDepthMu.
var (
	analysisDepthMu sync.Mutex
	analysisDepths  = map[*program.CheckerProgram]int{}
)

// deepestReached: the deepest expression nesting this PROCESS has
// walked, across every check — an intentional cross-check bench
// high-water mark (deepestWalk's own doc comment), not per-check
// state, so it stays one mutex-guarded global rather than joining
// analysisDepths.
var (
	depthMu        sync.Mutex
	deepestReached int
)

// DeepestWalk is deepestWalk in the TS source: the deepest expression
// nesting this process has walked.
func DeepestWalk() int {
	depthMu.Lock()
	defer depthMu.Unlock()
	return deepestReached
}

// ClearDeepestWalk is clearDeepestWalk in the TS source: bench seam —
// start the measurement over.
func ClearDeepestWalk() {
	depthMu.Lock()
	deepestReached = 0
	depthMu.Unlock()
}

// evaluateExpression is evaluateExpression in the TS source: every
// expression visit. The depth bookkeeping lives HERE rather than in a
// wrapper: this runs once per expression node in the program, and the
// extra call frame a wrapper adds was measured at a third of a whole
// real-codebase run.
func evaluateExpression(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	analysisDepthMu.Lock()
	analysisDepths[ctx.P]++
	depth := analysisDepths[ctx.P]
	analysisDepthMu.Unlock()
	depthMu.Lock()
	if depth > deepestReached {
		deepestReached = depth
	}
	depthMu.Unlock()
	defer func() {
		analysisDepthMu.Lock()
		analysisDepths[ctx.P]--
		if analysisDepths[ctx.P] == 0 {
			delete(analysisDepths, ctx.P)
		}
		analysisDepthMu.Unlock()
	}()
	known := evaluateForm(ctx, env, e)
	// a call `this` reaches — as method receiver, argument, or
	// inside a closure handed over — can run class code that writes
	// any field: what is held about `this` forgets, and the field
	// invariants (which every such write respects) reseed it
	if ast.IsCallExpression(e) || ast.IsNewExpression(e) || ast.IsTaggedTemplateExpression(e) {
		if _, hasThis := env["this"]; hasThis && MentionsThis(e) {
			ForgetThisHeld(ctx, env, e)
		}
	}
	// the RESOLVED type is the last reader for a short-circuit or
	// ternary whose join fell silent — those unknowns are join
	// failures, never fixpoint cuts. Calls and reads stay with the
	// fall-through fallback under the syntax table: an inlined
	// recursive call returns the cycle-cut unknown DELIBERATELY, and
	// seeding it type-wide poisons the certified induction join
	// (PV2-020/021 pin this). An opaque result keeps its provenance.
	if known.Kind == abstractdomain.KindUnknown && !known.Opaque && TypeReadsSilentResult(e) {
		return silence.AfterReaders(known, ctx.P.Checker, e, silence.RoleJoin)
	}
	return known
}

func evaluateForm(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	if ast.IsParenthesizedExpression(e) {
		return evaluateExpression(ctx, env, e.AsParenthesizedExpression().Expression)
	}
	if ast.IsAwaitExpression(e) {
		return EvaluateAwait(ctx, env, e)
	}
	if ast.IsSatisfiesExpression(e) {
		return EvaluateSatisfies(ctx, env, e)
	}
	if ast.IsAsExpression(e) || ast.IsNonNullExpression(e) || ast.IsTypeAssertion(e) {
		return EvaluateCast(ctx, env, e)
	}
	if literal, ok := EvaluateLiteral(ctx, env, e); ok {
		return literal
	}
	if ast.IsPrefixUnaryExpression(e) || ast.IsPostfixUnaryExpression(e) {
		unary, hasUnary := ReadUnary(ctx, env, e)
		// a `this` step writes.ts cannot place — an element step in the
		// chain — forgets the tracked `this`, invariants reseed; a plain
		// `this.#x++` chain is placed by writeProperty and survives
		var operator ast.Kind
		var operand *ast.Node
		if ast.IsPrefixUnaryExpression(e) {
			pre := e.AsPrefixUnaryExpression()
			operator, operand = pre.Operator, pre.Operand
		} else {
			post := e.AsPostfixUnaryExpression()
			operator, operand = post.Operator, post.Operand
		}
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			if _, hasThis := env["this"]; hasThis && MentionsThis(operand) && !PlacedThisChain(operand) {
				ForgetThisHeld(ctx, env, e)
			}
		}
		if hasUnary {
			return unary
		}
	}
	if ast.IsIdentifier(e) {
		if e.Text() == "Infinity" {
			return abstractdomain.KnownValues([]float64{math.Inf(1)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		}
		if e.Text() == "NaN" {
			return abstractdomain.NaNValue
		}
		// the global `undefined` is an INTRINSIC — no lib declaration —
		// so "not shadowed by any user binding" is the whole test
		if e.Text() == "undefined" {
			if _, tracked := env[e.Text()]; !tracked {
				symbol := ctx.P.Checker.GetSymbolAtLocation(e)
				shadowed := false
				if symbol != nil {
					for _, d := range symbol.Declarations {
						if !ast.GetSourceFileOfNode(d).IsDeclarationFile {
							shadowed = true
							break
						}
					}
				}
				if !shadowed {
					return abstractdomain.Undef
				}
			}
		}
		if held, ok := env[e.Text()]; ok {
			// an OPAQUE value at a position tsc NARROWS: the file's own
			// text determined more than the outside sent, and the walk did
			// not read it — the walk's own gap, never the outside's silence
			if held.Kind == abstractdomain.KindUnknown && held.Opaque && NarrowedSinceDeclaration(ctx, e) {
				return silence.Residue()
			}
			return held
		}
		return UntrackedIdentifier(ctx, e)
	}
	// `this` inside a class method body (arrows included — they keep
	// the surrounding `this`) is a tracked binding like a parameter:
	// initialized at body entry with the class's field invariants, narrowed
	// by guards, forgotten wherever a write could land. A `this` with
	// its own dynamic receiver — a function expression's, an object
	// method's — never reads the tracked one.
	if e.Kind == ast.KindThisKeyword {
		if dataflowfacts.EnclosingThisClass(e) != nil {
			if held, ok := env["this"]; ok {
				return held
			}
		}
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        "`this` is tracked only inside a class method body",
				Unsupported: true,
			})
		}
		return silence.Residue()
	}
	if e.Kind == ast.KindNullKeyword {
		return abstractdomain.Undef
	}
	if ast.IsVoidExpression(e) {
		evaluateExpression(ctx, env, e.AsVoidExpression().Expression)
		return abstractdomain.Undef
	}
	// `typeof x` is pinned by the STATIC type when tsc's verdict
	// admits exactly one word — null is "object", a callable is
	// "function", exactly as the spec tables say
	if ast.IsTypeOfExpression(e) {
		typeOf := e.AsTypeOfExpression()
		operand := evaluateExpression(ctx, env, typeOf.Expression)
		word := primitives.TypeofWordOf(ctx.P.Checker, ctx.P.Checker.GetTypeAtLocation(typeOf.Expression))
		// a WALKED value admitting several words (an enum reverse read:
		// value, name, or undefined; a maybe-unwritten `let x: string`)
		// contradicts any single static word — EXCEPT that pure absence
		// wears "undefined" or "object" (null rides the same marker),
		// so a static claim of those two stands over it
		if abstractdomain.TypeofPlural(operand) &&
			!(operand.Kind == abstractdomain.KindUndef && (word == "undefined" || word == "object")) {
			return silence.Residue()
		}
		if word != "" {
			return abstractdomain.KnownValues(refinementsets.CodepointsOf(word), abstractdomain.PrimitiveString, abstractdomain.TrustSpec)
		}
		// the static type says nothing (an `any` boundary, an inlined
		// parameter) — the WALKED value may still pin the one word, at
		// the grade its own claim carries
		held := abstractdomain.TypeofWordOfKnown(operand)
		if held != "" {
			return abstractdomain.KnownValues(
				refinementsets.CodepointsOf(held),
				abstractdomain.PrimitiveString,
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(operand), abstractdomain.TrustSpec),
			)
		}
		return silence.Residue()
	}
	if ast.IsDeleteExpression(e) {
		del := e.AsDeleteExpression()
		// a delete removes a key at runtime: whatever held the object
		// can no longer be vouched
		ForgetThrough(ctx, env, del.Expression)
		// a ThisKeyword receiver is no binding, so forgetThrough cannot
		// place it — the tracked `this` forgets here instead
		if _, hasThis := env["this"]; hasThis && MentionsThis(del.Expression) {
			ForgetThisHeld(ctx, env, e)
		}
		// ...and the DELETED KEY itself is now absent: on a plain named
		// receiver the own property is gone (sec-delete-operator), so the
		// key reads undefined from here on — what lets a delete-then-read
		// prove, and a key the delete list MISSED stay visibly unproven
		if ast.IsPropertyAccessExpression(del.Expression) {
			pa := del.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(pa.Expression) {
				if _, tracked := env[pa.Expression.Text()]; tracked {
					receiverName := pa.Expression.Text()
					held, ok := env[receiverName]
					if !ok {
						held = silence.Residue()
					}
					var keys []abstractdomain.ObjectKey
					var stated abstractdomain.ObjectAnnotationRef
					complete := false
					if held.Kind == abstractdomain.KindObject {
						keys = setObjectKey(held.Keys, pa.Name().Text(), abstractdomain.Undef)
						stated = held.Stated
						complete = held.Complete
					} else {
						keys = []abstractdomain.ObjectKey{{Name: pa.Name().Text(), Value: abstractdomain.Undef}}
					}
					dataflowfacts.UpdateTracked(ctx.Aliases, env, receiverName, abstractdomain.KnownObject(keys, stated, complete, abstractdomain.TrustSpec, false))
				}
			}
		}
		return silence.Residue()
	}
	if ast.IsArrayLiteralExpression(e) {
		return EvaluateArrayLiteral(ctx, env, e)
	}
	if ast.IsObjectLiteralExpression(e) {
		return EvaluateObjectLiteral(ctx, env, e)
	}
	if ast.IsConditionalExpression(e) {
		return ReadConditional(ctx, env, e)
	}
	if ast.IsBinaryExpression(e) {
		known := ReadBinary(ctx, env, e)
		// a write through `this` that writes.ts cannot PLACE — an element
		// step in the chain, a destructuring target — forgets the tracked
		// `this`, and the field invariants (which the write respects)
		// reseed it. A plain property chain rooted at `this` is placed by
		// writeProperty now, so the placed fact survives.
		bin := e.AsBinaryExpression()
		op := bin.OperatorToken.Kind
		if op >= ast.KindFirstAssignment && op <= ast.KindLastAssignment {
			if _, hasThis := env["this"]; hasThis && MentionsThis(bin.Left) && !PlacedThisChain(bin.Left) {
				ForgetThisHeld(ctx, env, e)
			}
		}
		return known
	}
	if ast.IsCallExpression(e) {
		return EvaluateCallExpression(ctx, env, e)
	}
	if read := ReadPropertyAccess(ctx, env, e); read != nil {
		return *read
	}
	if read := ReadElementAccess(ctx, env, e); read != nil {
		return *read
	}
	if ast.IsNewExpression(e) {
		if known := EvaluateNewExpression(ctx, env, e); known != nil {
			return *known
		}
	}
	// the RESOLVED type at the expression is the last reader before
	// the terminal unknown — a property read off a typed value, an
	// element read, an unmodeled construction or call, an await: the
	// host's claim at the position holds wherever no `any` boundary
	// intervened (an any receiver types the read `any`, which reads
	// as nothing), so the walk's silence becomes the type's statement
	if ast.IsPropertyAccessExpression(e) || ast.IsElementAccessExpression(e) ||
		ast.IsNewExpression(e) || ast.IsCallExpression(e) ||
		ast.IsAwaitExpression(e) || ast.IsTaggedTemplateExpression(e) ||
		e.Kind == ast.KindJsxElement || e.Kind == ast.KindJsxSelfClosingElement ||
		e.Kind == ast.KindJsxFragment {
		seeded := silence.AfterReaders(silence.Residue(), ctx.P.Checker, e, silence.RoleModel)
		if seeded.Kind != abstractdomain.KindUnknown || seeded.Opaque {
			return seeded
		}
	}
	// the terminal unknown — sound every way, but not the same fact:
	// the ONE syntax table says which. A modeled kind fell through an
	// existing case on an instance it could not resolve (the row's
	// default covers it); a declined kind says its own sentence; a
	// kind absent from the table has no case at all, and the note
	// says exactly that
	model, hasModel := SyntaxModels[e.Kind]
	if !hasModel {
		assignability.NoteReason(assignability.ReasonNote{
			Site:        "expression",
			Node:        e,
			Said:        "the walk has no case for this syntax (" + e.Kind.String() + ")",
			Unsupported: true,
		})
	} else if !model.Modeled {
		assignability.NoteReason(assignability.ReasonNote{
			Site:        "expression",
			Node:        e,
			Said:        model.Said,
			Unsupported: model.Unsupported,
		})
	}
	return silence.Residue()
}
