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
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
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
	// a value a caller PROVED for this exact node answers instead of the
	// walk's own reading — the relational accumulation's folded division,
	// whose quotient comes from a kernel relation no re-walk can reach
	// (flow_context.go's NodeOverrides carries the whole rule). The map
	// is nil for nearly every walk, so the ordinary path pays this
	// predictable branch and never a lookup; reads do not consume, so a
	// speculative probe walking the node first leaves it for the real one.
	if ctx.NodeOverrides != nil {
		if pinned, isPinned := ctx.NodeOverrides[e]; isPinned {
			return pinned
		}
	}
	known := evaluateForm(ctx, env, e)
	// a call `this` reaches — as method receiver, argument, or
	// inside a closure handed over — can run class code that writes
	// any field: what is held about `this` forgets, and the field
	// invariants (which every such write respects) reseed it, UNLESS
	// every such call carries a summary proving the receiver untouched
	// (thisSurvivesCalls). A call through `super` reaches the same
	// instance without spelling `this` at all: `super.init()` and a
	// derived constructor's `super(…)` both run base-class code on this
	// object, so they forget on the same terms — and the base body they
	// run is named by super_binding.go's resolver, so a resolved base
	// member proves the receiver untouched exactly as a `this` call
	// does (superSurvivesCalls). A super call that resolves to nothing
	// forgets.
	if ast.IsCallExpression(e) || ast.IsNewExpression(e) || ast.IsTaggedTemplateExpression(e) {
		if _, hasThis := env.Get("this"); hasThis {
			mentionsSuper := MentionsSuper(e)
			if (mentionsSuper && !superSurvivesCalls(ctx, e)) ||
				(MentionsThis(e) && !thisSurvivesCalls(ctx, e)) {
				ForgetThisHeld(ctx, env, e)
			}
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

// thisSurvivesCalls answers whether what is held about `this` still
// holds after the expression ran: whether EVERY call-like node inside
// it that mentions `this` carries a proof that the body it runs leaves
// the receiver alone.
//
// What makes keeping the facts sound: a lowered summary's receiver row
// is a statement about EVERY path through the body — the kernel walked
// the whole body to build it, so "no this-entry is written, and the
// receiver is not returned" rules out a write on any path, not just the
// one the call happened to take. That covers what the callee itself
// does. What it does not cover is what the callee's CALLERS-onward can
// do with a reference: a body handed `this` as an argument may store it
// in a field, a map, a closure, and write it after this call returns,
// and no receiver row speaks about that object afterwards. So the
// escape check (HandsThisOut) is a separate, syntactic requirement:
// `this` may reach the callee only as the receiver of a plain property
// chain, never through an argument or a template substitution.
//
// One expression can hold several calls (`f(this.a) + this.b()`); the
// answer is the AND over all of them, and every doubt — an unresolved
// callee, a virtual method, a declined lowering, a body whose own
// summary build is in flight — answers false and forgets.
func thisSurvivesCalls(ctx *FlowContext, e *ast.Node) bool {
	for _, call := range ThisMentioningCalls(e, nil) {
		// `this` reaching the callee anywhere but as a plain receiver
		// chain hands out a reference the summary says nothing about
		if HandsThisOut(call) {
			return false
		}
		callee := CalleeOfCallLike(call)
		if callee == nil {
			return false
		}
		contract := ContractOf(ctx, callee)
		// no contract: an unresolvable callee, a reassigned function name,
		// a method overridden in view (whose base body stands for no
		// instance) — nothing to read a receiver row off
		if contract == nil {
			return false
		}
		if !receiverProvedUntouched(ctx, contract.Declaration) {
			return false
		}
	}
	return true
}

// superSurvivesCalls answers the same question thisSurvivesCalls
// answers, for the calls that reach the instance through `super`
// instead of through `this`: whether EVERY super-rooted call inside
// the expression runs a base body whose summary proves the receiver
// untouched.
//
// The proof is the same one — the resolved declaration's lowered
// summary, plus the syntactic escape check — and only the resolution
// differs: a super callee names no symbol, so super_binding.go walks
// the enclosing class's heritage to the base member the call runs. A
// call that resolves to nothing (no extends clause, a mixin or ambient
// base, a computed member name, a member no class in the chain
// declares) answers false and forgets, which is what every super call
// answered before the resolver existed.
//
// A derived constructor's `super(…)` needs no special case: a base
// constructor initializes fields by design, so its summary says the
// receiver is touched and the row forgets on its own.
//
// Every `super` in the expression must be the ROOT of one of the calls
// proved here. A `super` reached any other way — read as a value,
// handed to a callee, stepped through `super[k]` — has no row, so it
// forgets: exactly the answer the blanket forget gave before.
func superSurvivesCalls(ctx *FlowContext, e *ast.Node) bool {
	calls := SuperRootedCalls(e, nil)
	provedRoots := map[*ast.Node]struct{}{}
	for _, call := range calls {
		callee := CalleeOfCallLike(call)
		// the instance reaching the base body anywhere but as the
		// receiver `super` already names hands out a reference the
		// summary says nothing about
		if HandsThisOutOfSuperCall(call) {
			return false
		}
		declaration := SuperCallDeclaration(ctx, callee)
		if declaration == nil {
			return false
		}
		if !receiverProvedUntouched(ctx, declaration) {
			return false
		}
		provedRoots[SuperCalleeRoot(callee)] = struct{}{}
	}
	for _, mention := range SuperMentions(e, nil) {
		if _, proved := provedRoots[mention]; !proved {
			return false
		}
	}
	return true
}

// receiverProvedUntouched: whether a callee declaration's summary
// proves the body leaves the receiver alone — the half of the
// this-survives proof that reads the callee rather than the call site,
// shared by the `this` route and the `super` route so the two never
// disagree about what a summary says.
func receiverProvedUntouched(ctx *FlowContext, declaration *ast.Node) bool {
	// a body whose own summary build is RUNNING right now cannot be
	// summarized from underneath itself: the memo stores only when the
	// build finishes, so asking here would lower it a second time
	// inside itself. The recursive case forgets.
	if SummaryCycleInFlight(checkerOf(ctx), declaration) {
		return false
	}
	// SummaryReceiverEffects answers a bare false BOTH when the summary
	// says the receiver is untouched AND when the body did not lower at
	// all, so the lowering is asked first — and only a COMPLETE body
	// gets to say "not written" (callee_effects.go's receiverWritten
	// reads the same answers in the same order, with the same porous
	// doubt direction: a porous body has statements the lowering did not
	// read, so its empty written-entry list proves nothing). A declined
	// or porous lowering forgets.
	if _, lowered := LowerSummaryBody(ctx, declaration); !lowered {
		return false
	}
	if outcome, _, recorded := SummaryOutcomeOf(checkerOf(ctx), declaration); !recorded || outcome != SummaryComplete {
		return false
	}
	// receiverTouched folds both rows the proof needs: a written
	// this-entry, and a returned receiver (which moves the caller's
	// object knowledge through the alias just as a write would)
	if receiverTouched, _ := SummaryReceiverEffects(ctx, declaration); receiverTouched {
		return false
	}
	return true
}

func evaluateForm(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	if ast.IsParenthesizedExpression(e) {
		return evaluateExpression(ctx, env, e.AsParenthesizedExpression().Expression)
	}
	if ast.IsAwaitExpression(e) {
		return EvaluateAwait(ctx, env, e)
	}
	// a yield's OPERAND is this body's own claim: it runs here and
	// judges against the enclosing generator's stated yield position
	// (yield_contract.go). No return — what the yield RESUMES with
	// comes from the caller's next(v), and the one syntax table at
	// the end speaks that decline.
	if ast.IsYieldExpression(e) {
		CheckYieldedValue(ctx, env, e)
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
			if _, hasThis := env.Get("this"); hasThis && MentionsThis(operand) && !PlacedThisChain(operand) {
				ForgetThisHeld(ctx, env, e)
			}
		}
		if hasUnary {
			return unary
		}
	}
	if ast.IsIdentifier(e) {
		// a with body resolves each read against the SCOPE OBJECT first
		// — a getter on it can answer differently at every call, so no
		// claim about a with-scoped identifier (not even Infinity/NaN/
		// undefined, all shadowable properties) survives from here
		if e.Flags&ast.NodeFlagsInWithStatement != 0 {
			return silence.ResidueOf("a with-scoped read resolves against the scope object at runtime, not the name written here")
		}
		if e.Text() == "Infinity" {
			return abstractdomain.KnownValues([]float64{math.Inf(1)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		}
		if e.Text() == "NaN" {
			return abstractdomain.NaNValue
		}
		// the global `undefined` is an INTRINSIC — no lib declaration —
		// so "not shadowed by any user binding" is the whole test
		if e.Text() == "undefined" {
			if _, tracked := env.Get(e.Text()); !tracked {
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
		if held, ok := env.Get(e.Text()); ok {
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
	// its own dynamic receiver — a function expression's — never reads
	// the tracked one.
	//
	// A function or function EXPRESSION declaring its own written
	// `this` PARAMETER (`function f(this: { age: number }) { … }`) is
	// the second tracked shape: EnclosingThisParameterFunction finds
	// the SAME function this `this` belongs to (through arrows, the
	// same climb EnclosingThisClass makes), and env.Get("this") reads
	// whatever the call site bound there — a contracted callee inlined
	// through Function.prototype.call binds it from the call's own
	// first argument (evaluate_call_expression.go's ThisParameterCall
	// route); every other caller of a this-parameter function leaves
	// it unset, which falls through to the same decline below.
	//
	// An OBJECT-LITERAL method's own `this` is the THIRD tracked shape:
	// ObjectLiteralMethodWalkCall (method_this_writes.go) binds "this"
	// in env to the receiver before walking the method's body on the
	// walk route, and EnclosingThisObjectLiteralMethod finds the SAME
	// method this `this` belongs to (through arrows, the same climb).
	// Without this arm, `this.age` inside `bump()`'s own body read
	// silence.Residue() regardless of the binding
	// ObjectLiteralMethodWalkCall had just set, because this was the
	// dynamic-receiver case the comment above used to lump an object
	// method into.
	//
	// A FOURTH shape rides ctx.ThisOwnerDeclaration rather than a purely
	// static syntactic climb: `{ bump: helperFn }`, a property pointing
	// at a SEPARATELY DECLARED FunctionDeclaration/FunctionExpression
	// (contract_file_facts.go's property-alias pass registers the
	// contract; objectLiteralMethodWalkTarget's calleePropertyInLiteral
	// check recognizes the call). helperFn's own body is an ordinary
	// FunctionDeclaration, called both through the alias (this = the
	// receiver) and potentially bare elsewhere (this = its own dynamic
	// receiver) — the SAME declaration node serves both shapes, so no
	// purely syntactic recognizer can tell them apart the way the class
	// and object-literal-method climbs do for THEIR shapes (an
	// object-literal method's own PARENT already proves what its `this`
	// is; helperFn's parent proves nothing about any one call).
	// ObjectLiteralMethodWalkCall sets ThisOwnerDeclaration to the exact
	// declaration it bound "this" for, in its own fresh, isolated
	// callEnv — dataflowfacts.EnclosingThisOwner(e) climbs from `e` the
	// same way the other three climbs do (through arrows, stopped at
	// the nearest function/method) and must land on that SAME node: a
	// more deeply nested sibling function within the same walked body
	// (its own dynamic `this`, never the bound receiver) climbs to
	// ITSELF instead, so the identity check keeps that shape correctly
	// unrecognized exactly as the other climbs already do for it.
	if e.Kind == ast.KindThisKeyword {
		recognizedThisSite := dataflowfacts.EnclosingThisClass(e) != nil ||
			dataflowfacts.EnclosingThisParameterFunction(e) != nil ||
			dataflowfacts.EnclosingThisObjectLiteralMethod(e) != nil ||
			(ctx.ThisOwnerDeclaration != nil && dataflowfacts.EnclosingThisOwner(e) == ctx.ThisOwnerDeclaration)
		if recognizedThisSite {
			if held, ok := env.Get("this"); ok {
				return held
			}
		}
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        "`this` is tracked only inside a class method body or a this-parameter function",
				Unsupported: true,
			})
		}
		// a RECOGNIZED this-shape (a class method, a this-parameter
		// function, an object-literal method) with no caller binding in
		// env is a real gap in what THIS WALK carries, not "nothing
		// stated about this site" — answering Opaque (still KindUnknown,
		// so every existing decline check still reads it as undetermined)
		// stops evaluateForm's own AfterReaders fallback
		// (silence.RoleModel, this file's tail) from seeding `this.key`'s
		// PROPERTY read from the declared this-parameter's or the
		// object-literal method's static host type — a plain `number`
		// member seeds NumberWithNaN (typereading/recipes.go), which
		// answered a real-looking possibly-NaN claim for a value this
		// walk in fact knows nothing about at this call. The class case
		// already reaches the same opaque floor one layer down
		// (this_property_access.go's readThisFieldInvariant, "a field
		// with NO standing invariant" — Opaque there too) for a
		// RECOGNIZED-class field with no invariant; this puts the
		// this-parameter and object-literal-method shapes on the same
		// footing at the keyword read itself, since neither has an
		// invariant table to fall back through. An UNRECOGNIZED this site
		// (no enclosing class, this-parameter function, or object-literal
		// method at all — a function expression's own dynamic receiver)
		// keeps plain silence.Residue(): nothing here claims to know
		// anything about that shape, seeded or not.
		if recognizedThisSite {
			return abstractdomain.Opaque
		}
		return silence.Residue()
	}
	if e.Kind == ast.KindNullKeyword {
		return abstractdomain.Null
	}
	// `new.target` is the constructor when the enclosing function ran
	// under `new`, and undefined when it ran as a plain call
	// (sec-built-in-function-objects). Which of the two it is depends
	// on the call site, so the walk holds BOTH: a function of unknown
	// body, possibly absent. `import.meta` shares this syntax kind and
	// stays with the table's own sentence — its shape is the host's.
	if e.Kind == ast.KindMetaProperty && e.AsMetaProperty().KeywordToken == ast.KindNewKeyword {
		return abstractdomain.PossiblyUndefined(abstractdomain.HostFunction, abstractdomain.TrustSpec, true, false)
	}
	// `import.meta` — the host's own value (ImportMeta: `{ url: string,
	// … }`), never the program's. Its RESOLVED type is a claim tsc
	// already states, the same standing a property access or call
	// result reads through AfterReaders; seeding it here lets a bare
	// `import.meta` used as a plain value refute at a scalar sink (a
	// constructed object is never a number) instead of sitting
	// undetermined for lack of any seed at all.
	if e.Kind == ast.KindMetaProperty && e.AsMetaProperty().KeywordToken == ast.KindImportKeyword {
		seeded := silence.AfterReaders(abstractdomain.Unknown, ctx.P.Checker, e, silence.RoleModel)
		if seeded.Kind != abstractdomain.KindUnknown {
			return seeded
		}
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
		word := primitives.TypeofWordOf(ctx.P.Checker, typereading.TypeAtLocation(ctx.P.Checker, typeOf.Expression))
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
		if _, hasThis := env.Get("this"); hasThis && MentionsThis(del.Expression) {
			ForgetThisHeld(ctx, env, e)
		}
		// ...and the DELETED KEY itself is now absent: on a plain named
		// receiver the own property is gone (sec-delete-operator), so the
		// key reads undefined from here on — what lets a delete-then-read
		// prove, and a key the delete list MISSED stay visibly unproven
		if ast.IsPropertyAccessExpression(del.Expression) {
			pa := del.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(pa.Expression) {
				if _, tracked := env.Get(pa.Expression.Text()); tracked {
					receiverName := pa.Expression.Text()
					held, ok := env.Get(receiverName)
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
					UpdateTrackedEnv(ctx.Aliases, env, receiverName, abstractdomain.KnownObject(keys, stated, complete, abstractdomain.TrustSpec, false))
				}
			}
		}
		// the delete itself answers a boolean (sec-delete-operator,
		// specifications/javascript/spec.html:20569-20590): every completing path
		// returns true or deleteStatus, and in strict code a false
		// deleteStatus THROWS instead of returning — so in a module
		// (always strict) the completed value is exactly true; in a
		// non-module file a sloppy delete can also complete false
		if file := ast.GetSourceFileOfNode(e); file != nil && ast.IsExternalModule(file) {
			return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
		}
		return abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
	}
	if ast.IsArrayLiteralExpression(e) {
		return EvaluateArrayLiteral(ctx, env, e)
	}
	if ast.IsObjectLiteralExpression(e) {
		return EvaluateObjectLiteral(ctx, env, e)
	}
	// a JSX element / self-closing element / fragment is never a scalar
	// (SYNTAX-COVERAGE.md §C) — jsx_expression.go states the one proved
	// fact (an object, unstated keys) and walks every attribute and
	// child so their own sinks still fire
	if e.Kind == ast.KindJsxElement {
		return EvaluateJsxElement(ctx, env, e)
	}
	if e.Kind == ast.KindJsxSelfClosingElement {
		return EvaluateJsxSelfClosingElement(ctx, env, e)
	}
	if e.Kind == ast.KindJsxFragment {
		return EvaluateJsxFragment(ctx, env, e)
	}
	if e.Kind == ast.KindJsxExpression {
		return EvaluateJsxExpressionContainer(ctx, env, e)
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
			if _, hasThis := env.Get("this"); hasThis && MentionsThis(bin.Left) && !PlacedThisChain(bin.Left) {
				ForgetThisHeld(ctx, env, e)
			}
		}
		return known
	}
	if ast.IsCallExpression(e) {
		return EvaluateCallExpression(ctx, env, e)
	}
	// a tagged template is a CALL of its tag: the substitutions run
	// for their effects and the tag's own return contract reads, the
	// way an unmodeled call's does
	if ast.IsTaggedTemplateExpression(e) {
		return EvaluateTaggedTemplate(ctx, env, e)
	}
	// `super` in expression position — the receiver of `super.m()`, the
	// callee of `super(...)`, or (rarer) a BARE value read: `super.years
	// as unknown as Age` names the base method itself, never a call.
	// The base class it names is entered from outside this walk's own
	// reading, so a CALLED super still holds what any value from
	// outside holds — Opaque, so the call's own effects fire at the
	// call site rather than being skipped. A bare read is not a call:
	// the base instance's own RESOLVED type at this position is a claim
	// tsc already checked (the extends clause fixes it exactly),
	// exactly the same "the type is everything this file determines"
	// standing AfterReaders already gives a property access or a call
	// result. Reading it through here (rather than Opaque outright)
	// lets a bare `super` used as a plain value — never a function, an
	// object, a number — refute at a scalar sink the way ThisBare's
	// bare `this` already does through env's held object shape.
	if e.Kind == ast.KindSuperKeyword {
		seeded := silence.AfterReaders(abstractdomain.Unknown, ctx.P.Checker, e, silence.RoleModel)
		if seeded.Kind != abstractdomain.KindUnknown {
			return seeded
		}
		return abstractdomain.Opaque
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
		ast.IsAwaitExpression(e) || ast.IsTaggedTemplateExpression(e) {
		seeded := silence.AfterReaders(silence.Residue(), ctx.P.Checker, e, silence.RoleModel)
		if seeded.Kind != abstractdomain.KindUnknown || seeded.Opaque {
			return seeded
		}
	}
	// a YIELD EXPRESSION read as its own VALUE — `const v = yield e` —
	// is what the caller's next(v) sends back: the N of the enclosing
	// generator's stated `Generator<Y, R, N>` (yield_contract.go).
	// YieldResumeStated seeds it the same way BindEntryEnv seeds a
	// parameter from its own declared type (AbstractValueOfDeclared).
	// Nil — an ungrounded generator, an unstated N, or no enclosing
	// generator walk at all — falls through to the syntax table's
	// decline exactly as before.
	if e.Kind == ast.KindYieldExpression && ctx.YieldResumeStated != nil {
		return AbstractValueOfDeclared(*ctx.YieldResumeStated)
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
