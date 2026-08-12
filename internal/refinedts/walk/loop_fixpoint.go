// from control_flow/loop_fixpoint.ts
//
// The loop fixpoint — havoc invalidated. Iterate the body's effect;
// widen what refuses to stabilize; the KERNEL certifies the candidate
// invariant (set_functions/invariant.lean `invariant_certifies`). A
// failed certificate falls back to unknown — never a guess. One
// checked pass; what follows sees the certified facts, with the
// refuted condition narrowing in unless a break leaves early.
//
// do-while: first pass unguarded; re-entries wear the held condition.
// for-of: the element binding wears the iterable's element set.
//
// CROSS-DIRECTORY: readDestructuring is bindings/destructuring.ts's
// ReadDestructuring — already ported (walk/destructuring.go).
// differenceConstraintsOf is dataflow_facts/difference_constraints.ts
// — blocked in dataflowfacts today (see assume_condition.go's banner).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// LoopAnalyzers mirrors the TS LoopAnalyzers interface — the three
// callbacks solveLoop needs from its caller (analyze_statement.go)
// so this file does not import upward into the dispatcher.
type LoopAnalyzers struct {
	AnalyzeStatement   func(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool
	EvaluateExpression func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue
	// IterationElement: what one element of a for-of is where the
	// ITERABLE expression says so and the walked value does not — a
	// web collection's iterator, an Object.entries call. Nil where
	// nothing speaks.
	IterationElement func(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue
}

func loopStatementBody(loop *ast.Node) *ast.Node {
	switch {
	case ast.IsForStatement(loop):
		return loop.AsForStatement().Statement
	case ast.IsWhileStatement(loop):
		return loop.AsWhileStatement().Statement
	case ast.IsDoStatement(loop):
		return loop.AsDoStatement().Statement
	case ast.IsForOfStatement(loop), ast.IsForInStatement(loop):
		return loop.AsForInOrOfStatement().Statement
	}
	return nil
}

// SolveLoop is solveLoop in the TS source: solve one loop in place —
// `env` leaves holding the after-loop facts.
//
// bodyEntry: when non-nil, the environment the BODY runs under is
// copied here: the certified invariant, the condition's narrowing,
// and the element binding. `env` itself comes out holding what
// follows the loop, which is a different state and the one a check
// wants. A hover inside the body wants this one. Filled on every
// pass, so it ends holding the checked pass's.
func SolveLoop(ctx *FlowContext, env Env, loop *ast.Node, result *annotations.DeclaredRefinement, analyzers LoopAnalyzers, bodyEntry Env) {
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}

	// a for-initializer runs once, before everything, checked
	if ast.IsForStatement(loop) && loop.AsForStatement().Initializer != nil {
		initializer := loop.AsForStatement().Initializer
		if ast.IsVariableDeclarationList(initializer) {
			for _, declaration := range initializer.AsVariableDeclarationList().Declarations.Nodes {
				decl := declaration.AsVariableDeclaration()
				if !ast.IsIdentifier(decl.Name()) {
					continue
				}
				if decl.Initializer == nil {
					env[decl.Name().Text()] = silence.Residue()
				} else {
					env[decl.Name().Text()] = analyzers.EvaluateExpression(ctx, env, decl.Initializer)
				}
			}
		} else {
			analyzers.EvaluateExpression(ctx, env, initializer)
		}
	}

	var condition *ast.Node
	switch {
	case ast.IsWhileStatement(loop):
		condition = loop.AsWhileStatement().Expression
	case ast.IsDoStatement(loop):
		condition = loop.AsDoStatement().Expression
	case ast.IsForStatement(loop):
		condition = loop.AsForStatement().Condition
	}
	// checkAssignability whatever the condition itself calls, once
	if condition != nil {
		analyzers.EvaluateExpression(ctx, cloneEnv(env), condition)
	}
	// names the LOOP writes anywhere — a comparison side rooted in one
	// is not loop-invariant, and its entry window would go stale
	loopWrites := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, loop, loopWrites)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, loop, loopWrites, nil)
	invariantSide := func(e *ast.Node) bool {
		root := e
		for ast.IsPropertyAccessExpression(root) {
			root = root.AsPropertyAccessExpression().Expression
		}
		if !ast.IsIdentifier(root) {
			return false
		}
		_, written := loopWrites[root.Text()]
		return !written
	}
	// the condition's cross-place rows hold at every body ENTRY (the
	// test just passed). Their scope is the CONDITION alone — a body
	// write RETIRES the row in walk order (writeBinding and havoc call
	// AliasClasses.invalidate), and each entry revalidates it, the condition
	// having just re-passed. So `while (i < xs.length) { use(xs[i]);
	// i++ }` keeps the row through the read and drops it at the
	// increment. A do-while body runs once unguarded, so it gets none.
	var conditionConstraints []dataflowfacts.DifferenceConstraint
	if condition != nil && !ast.IsDoStatement(loop) {
		conditionConstraints = dataflowfacts.DifferenceConstraintsOf(ctx.P.Checker, condition, condition, nil, nil, nil)
	}
	registerDifferenceConstraints(ctx, conditionConstraints)

	// the FULL transfer vocabulary — narrowings with value copies,
	// inverse factors, length guards — with comparison-side windows
	// read from the loop-entry state and gated to loop-invariant
	// names (an ungated window would go stale by the second iteration).
	// A condition whose rows landed WAS read — as a relation riding
	// the body entries and the exit channel — and the coverage report is told
	// so instead of counting the guard unread.
	var transfers *ConditionEnvTransfers
	if condition != nil {
		readElsewhere := narrowing.GuardReadNowhere
		if len(conditionConstraints) > 0 {
			readElsewhere = narrowing.GuardReadRelation
		}
		t := ConditionEnvTransfersOf(ctx, env, condition, ConditionEnvTransfersSite{
			At:            loop,
			ReadElsewhere: readElsewhere,
			SideWindow: func(e *ast.Node) (narrowing.Window, bool) {
				if !invariantSide(e) {
					return narrowing.Window{}, false
				}
				return narrowing.BoundsOfKnown(analyzers.EvaluateExpression(ctx, cloneEnv(env), e))
			},
		})
		transfers = &t
	}
	// a do-while body runs once before the condition is ever tested
	var bodyTransfers *ConditionEnvTransfers
	if !ast.IsDoStatement(loop) {
		bodyTransfers = transfers
	}

	// a condition (or a for-of iterable) with side effects steps
	// OUTSIDE the modeled discipline: the fixpoint iterates the BODY's
	// effect, so a name the condition writes would converge to its
	// stale entry value — `while (n-- > 0)` leaving n at 3. Every such
	// name decays to unknown before the solve, inside the body and
	// after the exit — the honest alert downstream, never a stale
	// fact. (The exit narrowing may still recover the final test's
	// refutation: it describes the tested place at test time.)
	conditionWritten := map[string]struct{}{}
	if condition != nil {
		AssignedNames(ctx.P.Checker, condition, conditionWritten)
	}
	if ast.IsForOfStatement(loop) || ast.IsForInStatement(loop) {
		AssignedNames(ctx.P.Checker, loop.AsForInOrOfStatement().Expression, conditionWritten)
	}
	for name := range conditionWritten {
		if _, ok := env[name]; ok {
			env[name] = silence.Residue()
		}
	}

	// for-of / for-in: the element binding — one name, or a
	// destructuring pattern over the element — and what one element is
	var elementName string
	hasElementName := false
	var elementBinding *ast.Node
	var elementPattern *ast.Node
	elementKnown := silence.Residue()
	if ast.IsForOfStatement(loop) || ast.IsForInStatement(loop) {
		forInOf := loop.AsForInOrOfStatement()
		iterable := analyzers.EvaluateExpression(ctx, cloneEnv(env), forInOf.Expression)
		if forInOf.Initializer != nil && ast.IsVariableDeclarationList(forInOf.Initializer) {
			declarations := forInOf.Initializer.AsVariableDeclarationList().Declarations.Nodes
			if len(declarations) == 1 {
				binding := declarations[0].AsVariableDeclaration().Name()
				if ast.IsIdentifier(binding) {
					elementName, hasElementName = binding.Text(), true
					elementBinding = binding
				} else {
					elementPattern = binding
				}
			}
		}
		if ast.IsForOfStatement(loop) {
			elementKnown = ElementOf(iterable)
		}
		// a for-in binding holds a property KEY, and the language yields
		// only strings there (ECMA-262 EnumerateObjectProperties: "all
		// the String-valued keys"; Symbol keys are never returned) — so
		// the binding wears the whole string sort
		if ast.IsForInStatement(loop) {
			elementKnown = abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		}
		if ast.IsForOfStatement(loop) && elementKnown.Kind == abstractdomain.KindUnknown {
			// an iterable the walk holds no sequence for may still SAY its
			// element: a web collection's iterator, an Object.entries call
			if said := analyzers.IterationElement(ctx, cloneEnv(env), forInOf.Expression); said != nil {
				elementKnown = *said
			}
		}
		if ast.IsForOfStatement(loop) && elementKnown.Kind == abstractdomain.KindUnknown {
			// the sort ground, extended to elements: iterating a
			// `number[]` the walk knows nothing more about still provably
			// yields doubles — any double, or NaN
			t := ctx.P.Checker.GetTypeAtLocation(forInOf.Expression)
			if ctx.P.Checker.TypeToString(t) == "number[]" {
				elementKnown = abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.RefinedSet{}, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
			}
		}
	}

	statement := loopStatementBody(loop)

	bodyEffect := func(fromEnv Env, reporting *FlowContext) Env {
		body := cloneEnv(fromEnv)
		if bodyTransfers != nil {
			bodyTransfers.ApplyWhenTrue(body)
		}
		if hasElementName && elementBinding != nil {
			body[elementName] = silence.SeededBinding(ctx.P.Checker, elementKnown, elementBinding)
		}
		// a destructured element binds through the shared reader
		// (destructure.ts): an exact pair's items land on their names,
		// an opaque element's slots stay opaque, a rest collects what
		// remains, and a defaulted slot admits the default honestly
		if elementPattern != nil {
			ReadDestructuring(elementPattern, elementKnown, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
				body[name] = silence.SeededBinding(ctx.P.Checker, held, at)
			})
		}
		// before the body runs, because the walk mutates it
		if bodyEntry != nil {
			for k := range bodyEntry {
				delete(bodyEntry, k)
			}
			for name, known := range body {
				bodyEntry[name] = known
			}
		}
		for i := range conditionConstraints {
			conditionConstraints[i].Dead = false
		}
		// a bare `continue` re-enters the NEXT iteration carrying its
		// branch's writes — recorded here and joined into the body's
		// exit, so a push-then-continue reaches the fixpoint
		var continued []Env
		inBody := *reporting
		inBody.ContinueSink = &continued
		if len(conditionConstraints) > 0 {
			inBody.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, reporting.DifferenceConstraints...), conditionConstraints...)
		}
		analyzers.AnalyzeStatement(&inBody, body, statement, result)
		for _, snapshot := range continued {
			for name, held := range body {
				body[name] = abstractdomain.JoinKnown(held, envOrResidue(snapshot, name))
			}
		}
		if ast.IsForStatement(loop) && loop.AsForStatement().Incrementor != nil {
			analyzers.EvaluateExpression(reporting, body, loop.AsForStatement().Incrementor)
		}
		return body
	}

	// a do-while RE-enters its body only through the test: every state
	// a later iteration starts from is the previous step's image WITH
	// the condition held. The first pass alone runs unguarded, and the
	// raw entry already sits in every join. So the solve's step images
	// wear the held condition, which makes the fixpoint's candidate the
	// true body window — entry ∪ (condition-held step) — instead of the
	// unbounded raw iterate. (The held comparison also proves its sides
	// real, so NaN never rides in through the narrowing.)
	stepImage := func(fromEnv Env, reporting *FlowContext) Env {
		stepped := bodyEffect(fromEnv, reporting)
		if ast.IsDoStatement(loop) && transfers != nil {
			transfers.ApplyWhenTrue(stepped)
		}
		return stepped
	}

	assigned := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, loop, assigned)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, loop, assigned, nil)
	var touched []string
	for name := range assigned {
		if _, ok := env[name]; ok {
			touched = append(touched, name)
		}
	}

	// an iterable the BODY may mutate cannot vouch its entry
	// elements — later iterations see what the writes made, so the
	// element decays to unknown (any name in the iterable expression,
	// or an alias of one, that the loop writes)
	if (hasElementName || elementPattern != nil) && (ast.IsForOfStatement(loop) || ast.IsForInStatement(loop)) {
		iterableNames := map[string]struct{}{}
		var collect func(node *ast.Node)
		collect = func(node *ast.Node) {
			if ast.IsIdentifier(node) {
				iterableNames[node.Text()] = struct{}{}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				collect(child)
				return false
			})
		}
		collect(loop.AsForInOrOfStatement().Expression)
		mutated := false
		for id := range iterableNames {
			for member := range ctx.Aliases.ClassOf(id) {
				if _, isAssigned := assigned[member]; isAssigned {
					mutated = true
					break
				}
			}
			if mutated {
				break
			}
		}
		// a mutated iterable's entry ELEMENTS cannot be vouched — but a
		// for-in binding never held elements, only property keys, and a
		// key is a string however the object changes mid-iteration
		if mutated && !ast.IsForInStatement(loop) {
			elementKnown = silence.Residue()
		}
	}
	// a declared binding's stated set IS its invariant — every write is
	// checked against it, so no widening is needed
	var fixpointed []string
	for _, name := range touched {
		if _, declared := ctx.Declared[name]; !declared {
			fixpointed = append(fixpointed, name)
		}
	}

	// a do-while runs its body once from the RAW entry before any
	// test. Unrolling that first pass into the premise lets the kernel
	// solve the REMAINING iterations under the condition — iterations
	// two onward genuinely enter with it true — instead of widening
	// unbounded to MAX_VALUE, whose dyadic bounds send every later
	// question over the set divergent (the do-then-anything hang).
	premiseEnv := env
	if ast.IsDoStatement(loop) && len(fixpointed) > 0 {
		first := bodyEffect(cloneEnv(env), &silent)
		joined := cloneEnv(env)
		for _, name := range fixpointed {
			joined[name] = abstractdomain.JoinKnown(envOrResidue(env, name), envOrResidue(first, name))
		}
		premiseEnv = joined
	}

	// ── settle the candidate (exact join → kernel / widen → certify) ──
	candidate := SettleLoopCandidate(SettleLoopCandidateInput{
		Ctx: ctx, Env: env, Loop: loop, Silent: &silent, StepImage: stepImage,
		Fixpointed: fixpointed, Touched: touched, ConditionWritten: conditionWritten,
		PremiseEnv: premiseEnv, Condition: condition,
		ElementName: elementName, HasElementName: hasElementName, ElementKnown: elementKnown,
	})

	// ── one checked pass against the certified facts ──────────────────
	checkedStep := bodyEffect(candidate, ctx)

	// ── what follows the loop ────────────────────────────────────────
	// zero iterations leave the entry state; any number leave the
	// invariant; the refuted condition narrows unless a break escapes.
	// A do-while tests AFTER the body, so its exit states are the
	// body's step image — which the body-window candidate no longer
	// contains — and that image joins in before the refutation narrows.
	after := Env{}
	for name, known := range env {
		carried := abstractdomain.JoinKnown(known, envOrResidue(candidate, name))
		if ast.IsDoStatement(loop) {
			after[name] = abstractdomain.JoinKnown(carried, envOrResidue(checkedStep, name))
		} else {
			after[name] = carried
		}
	}
	GrowPushedArrays(GrowPushedArraysInput{
		Ctx: ctx, Env: env, Loop: loop, Candidate: candidate, After: after,
		Fixpointed: fixpointed, EvaluateExpression: analyzers.EvaluateExpression,
	})
	if transfers != nil && !ContainsBreak(statement) {
		transfers.ApplyWhenFalse(after)
	}
	if condition != nil {
		NoteNegatedLoopExit(NoteNegatedLoopExitInput{
			Ctx: ctx, Loop: loop, Condition: condition,
			ConditionConstraints: conditionConstraints, After: after,
		})
	}
	for k := range env {
		delete(env, k)
	}
	for name, known := range after {
		env[name] = known
	}
}
