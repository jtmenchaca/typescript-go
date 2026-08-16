// Exact finite stepping for literal-bounded loops.
//
// A `for (let i = 0; i < 2; i++)` whose trip count the syntax pins,
// and a `for (const x of [10, 20, 30])` over an exactly known
// sequence, run an exactly known number of times over exactly known
// values. The fixpoint's widened invariant is the wrong tool there:
// its checked pass seeds a DECLARED binding at its full stated range,
// so `age = age + 1` under a guard on `i` reads the whole set and
// steps out of it — a fire on a loop that concretely ends at 2. This
// module steps such a loop the exact number of times instead, the
// same discipline reduceOutcome's exact fold already runs per
// element: each abstract step is a sound abstraction of the concrete
// step, run in ORDER against one carried environment, so the final
// state is exact.
//
// Reporting follows the fixpoint's own one-checked-pass law: the
// per-step walks run silently, and ONE reporting pass walks the body
// under the JOIN of every step's entry state. The join admits each
// step's values and every transfer is monotone, so anything a step
// would have reported fires under the join too — no missed fire, and
// no duplicate report per iteration.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// LoopUnrollBudget is the step count past which exact stepping stops
// paying for itself and the fixpoint (still sound) takes the loop —
// the same shape as EffectNodeBudget on the composed-effect side.
const LoopUnrollBudget = 128

// loopBodyEscapes is whether the body can leave the loop's own
// stepping — a break, a continue, or a return anywhere inside it
// makes the trip count the syntax pinned no longer the count that
// runs.
func loopBodyEscapes(body *ast.Node) bool {
	escapes := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if escapes {
			return
		}
		if ast.IsBreakStatement(node) || ast.IsContinueStatement(node) || ast.IsReturnStatement(node) {
			escapes = true
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(body)
	return escapes
}

// joinCarriedInto joins the carried environment's current values into
// the running per-name join — the union of every step's entry state,
// which the one reporting pass walks under. Only names the join
// already holds participate: the commit reads the same name set.
func joinCarriedInto(joined Env, carried Env) {
	joined.Range(func(name string, held abstractdomain.AbstractValue) bool {
		if v, ok := carried.Get(name); ok {
			joined.Set(name, abstractdomain.JoinKnown(held, v))
		}
		return true
	})
}

// fillBodyEntry mirrors bodyEffect's bodyEntry handling: empty the
// held image and copy the state the body is about to run under.
func fillBodyEntry(bodyEntry Env, from Env) {
	if bodyEntry == nil {
		return
	}
	bodyEntry.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		bodyEntry.Delete(name)
		return true
	})
	from.Range(func(name string, known abstractdomain.AbstractValue) bool {
		bodyEntry.Set(name, known)
		return true
	})
}

// UnrollLiteralBoundedLoop steps a literal-bounded loop exactly and
// leaves `env` holding the exact after-loop state. True when the loop
// was fully handled; false declines to the fixpoint untouched. Called
// by SolveLoop after the for-initializer has run and the condition
// has had its one checked evaluation.
func UnrollLiteralBoundedLoop(ctx *FlowContext, env Env, loop *ast.Node, result *annotations.DeclaredRefinement, analyzers LoopAnalyzers, bodyEntry Env) bool {
	switch {
	case ast.IsForStatement(loop):
		return unrollLiteralCountedFor(ctx, env, loop, result, analyzers, bodyEntry)
	case ast.IsForOfStatement(loop):
		return unrollExactSequenceForOf(ctx, env, loop, result, analyzers, bodyEntry)
	}
	return false
}

// unrollLiteralCountedFor steps `for (let i = A; i < B; i++)` — unit
// step, index unwritten in the body, no break/continue/return, A and
// B literal through const-to-const links (LiteralTripCountWith's own
// gates) — exactly count times. The index binding is SET to its
// exact per-trip value, which is what the gates guarantee it holds.
func unrollLiteralCountedFor(ctx *FlowContext, env Env, loop *ast.Node, result *annotations.DeclaredRefinement, analyzers LoopAnalyzers, bodyEntry Env) bool {
	forStmt := loop.AsForStatement()
	statement := forStmt.Statement
	if statement == nil {
		return false
	}
	count, counted := LiteralTripCountWith(ctx.P.Checker, loop)
	if !counted || count > LoopUnrollBudget {
		return false
	}
	// the index name and its literal start — re-read along the same
	// path the count itself came from
	declaration := forStmt.Initializer.AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	index := declaration.Name().Text()
	start, hasStart := dataflowfacts.ConstChainNumber(ctx.P.Checker, declaration.Initializer)
	if !hasStart {
		return false
	}

	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}

	carried := env.Clone()
	joined := env.Clone()
	// the index participates in the reporting join whether or not the
	// caller's environment already held it (SolveLoop's initializer
	// normally has)
	joined.Set(index, abstractdomain.KnownValues([]float64{start}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	for k := 0; k < count; k++ {
		carried.Set(index, abstractdomain.KnownValues([]float64{start + float64(k)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
		joinCarriedInto(joined, carried)
		analyzers.AnalyzeStatement(&silent, carried, statement, result)
	}
	// the loop exits with the index one step past the last trip — the
	// step that made the condition false
	carried.Set(index, abstractdomain.KnownValues([]float64{start + float64(count)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	// one reporting pass under the join of every trip's entry; a
	// zero-trip body never runs, so nothing in it reports
	if count > 0 {
		report := joined.Clone()
		fillBodyEntry(bodyEntry, report)
		analyzers.AnalyzeStatement(ctx, report, statement, result)
		if forStmt.Incrementor != nil {
			analyzers.EvaluateExpression(ctx, report, forStmt.Incrementor)
		}
	}

	// the exact finals replace the walk's own names
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		if v, ok := carried.Get(name); ok {
			env.Set(name, v)
		}
		return true
	})
	return true
}

// unrollExactSequenceForOf steps `for (const x of xs)` over an
// exactly known sequence — an exact tuple or list the walk holds —
// one element per trip, in order. Declines when the body can escape,
// when the binding is not a single let/const declaration, or when
// the body may write the iterable (a write at or above the cursor
// changes an element not yet read; the fixpoint's decayed reading
// takes those).
func unrollExactSequenceForOf(ctx *FlowContext, env Env, loop *ast.Node, result *annotations.DeclaredRefinement, analyzers LoopAnalyzers, bodyEntry Env) bool {
	forOf := loop.AsForInOrOfStatement()
	if forOf.AwaitModifier != nil {
		return false
	}
	statement := forOf.Statement
	if statement == nil || loopBodyEscapes(statement) {
		return false
	}
	initializer := forOf.Initializer
	if initializer == nil || !ast.IsVariableDeclarationList(initializer) ||
		(initializer.Flags&(ast.NodeFlagsLet|ast.NodeFlagsConst)) == 0 {
		return false
	}
	declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return false
	}
	binding := declarations[0].AsVariableDeclaration().Name()

	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}

	// an iterable expression that writes anything steps outside the
	// once-evaluated reading (`[i++, 2]`) — the fixpoint's decay of
	// condition-written names takes those
	iterableWrites := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, forOf.Expression, iterableWrites)
	if len(iterableWrites) > 0 {
		return false
	}

	// probe the iterable silently; its one reporting evaluation runs
	// only once the unroll is committed, so a decline leaves the main
	// path's own evaluation as the single reporting one
	iterable := analyzers.EvaluateExpression(&silent, env.Clone(), forOf.Expression)
	items := ItemsOf(iterable)
	if items == nil || len(items) > LoopUnrollBudget {
		return false
	}
	// a body that may write the iterable (any name the iterable
	// expression roots in, or an alias of one) declines
	written := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, loop, written)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, loop, written, nil)
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
	collect(forOf.Expression)
	for id := range iterableNames {
		for member := range ctx.Aliases.ClassOf(id) {
			if _, isWritten := written[member]; isWritten {
				return false
			}
		}
	}

	// the one reporting evaluation of the iterable expression itself
	analyzers.EvaluateExpression(ctx, env.Clone(), forOf.Expression)

	boundNames := map[string]struct{}{}
	seedBinding := func(target Env, item abstractdomain.AbstractValue) {
		if ast.IsIdentifier(binding) {
			boundNames[binding.Text()] = struct{}{}
			target.Set(binding.Text(), silence.SeededBinding(ctx.P.Checker, item, binding))
			return
		}
		ReadDestructuring(binding, item, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
			boundNames[name] = struct{}{}
			target.Set(name, silence.SeededBinding(ctx.P.Checker, held, at))
		})
	}

	carried := env.Clone()
	joined := env.Clone()
	for _, item := range items {
		joinCarriedInto(joined, carried)
		seedBinding(carried, item)
		analyzers.AnalyzeStatement(&silent, carried, statement, result)
	}

	// one reporting pass: the element binding wears the sequence's
	// element join, every outer name the join of its trip entries
	if len(items) > 0 {
		report := joined.Clone()
		seedBinding(report, ElementOf(iterable))
		fillBodyEntry(bodyEntry, report)
		analyzers.AnalyzeStatement(ctx, report, statement, result)
	}

	// the exact finals replace the walk's own names; the loop-scoped
	// element binding does not commit — any same-named outer binding
	// was shadowed, so its entry value stands
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		if _, isBound := boundNames[name]; isBound {
			return true
		}
		if v, ok := carried.Get(name); ok {
			env.Set(name, v)
		}
		return true
	})
	return true
}
