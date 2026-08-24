// from control_flow/analyze_statement.ts
//
// Walking statements in flow order, and joining what the branches
// leave behind. Each form answers one question — does control leave
// this list? — and leaves the environment holding what is true after
// it ran.
//
// The joins are where the honesty lives. A branch whose narrowing
// contradicts a known value is dead and contributes nothing. A branch
// that returns or throws contributes nothing either. Everything else
// joins exactly (join.ts), so a name that differs across the arms
// comes out holding both possibilities rather than one of them.
//
// CROSS-DIRECTORY: assertionCallNarrowings, applyNarrowed are
// narrowing package exports (already ported, called through their
// Go names below). checkAssignability is
// assignability/check_assignability.ts's FlowContext-reading
// function, now landed in this package (check_assignability.go).
// CorrelationGateOf is dataflow_facts/path_conditions.ts's function
// — it landed IN this package (correlation_gate.go) rather than in
// dataflowfacts, per go-port-tracker.md's control_flow row (its own
// condition_tree dependency made it walk-owned, same as
// GateAssumption itself, which flow_context.go defines).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// AnalyzeStatements is analyzeStatements in the TS source: walk
// statements in flow order; true when the list definitely returned
// or threw. `result` is the enclosing stated return type.
//
// A list that tests one immutable gate in TWO OR MORE `if`
// statements walks twice — once with the gate assumed truthy, once
// falsy — and joins the passes. ToBoolean of a never-written binding
// is fixed per run, so the passes partition every run: the join is
// exact, and the correlation the per-if joins would discard
// (then-arm here implies then-arm there) survives. One gate at a
// time — a pass never re-splits.
func AnalyzeStatements(ctx *FlowContext, env Env, statements []*ast.Node, result *annotations.DeclaredRefinement) bool {
	held := ctx.GateAssumptions
	if len(held) < 2 {
		gate, ok := CorrelationGateOf(ctx, statements, held)
		if ok {
			envTrue := env.Clone()
			envFalse := env.Clone()
			trueCtx := *ctx
			trueCtx.GateAssumptions = append(append([]GateAssumption{}, held...), GateAssumption{Base: gate.Base, Detail: gate.Detail, Truthy: true})
			exitsTrue := AnalyzeStatements(&trueCtx, envTrue, statements, result)
			falseCtx := *ctx
			falseCtx.GateAssumptions = append(append([]GateAssumption{}, held...), GateAssumption{Base: gate.Base, Detail: gate.Detail, Truthy: false})
			exitsFalse := AnalyzeStatements(&falseCtx, envFalse, statements, result)
			if exitsTrue && exitsFalse {
				return true
			}
			if exitsTrue {
				ReplaceEnv(env, envFalse)
			} else if exitsFalse {
				ReplaceEnv(env, envTrue)
			} else {
				ReplaceEnv(env, JoinEnvs(envTrue, envFalse))
			}
			return false
		}
	}
	return listWalk(ctx, env, statements, result)
}

// ContextWithExitRows is contextWithExitRows in the TS source: the
// context a statement hands the REST of its list — a
// condition-tested loop or an exiting branch leaves its negated
// condition's rows on the exit channel, and every later statement
// reads under them.
func ContextWithExitRows(ctx *FlowContext, statement *ast.Node) *FlowContext {
	running := ctx
	if exitConstraints, ok := dataflowfacts.ExitConstraintsOf(statement); ok && len(exitConstraints) > 0 {
		next := *running
		next.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, running.DifferenceConstraints...), exitConstraints...)
		running = &next
	}
	if sumRows, ok := dataflowfacts.SumExitConstraintsOf(statement); ok && len(sumRows) > 0 {
		next := *running
		next.SumConstraints = append(append([]dataflowfacts.SumConstraint{}, running.SumConstraints...), sumRows...)
		running = &next
	}
	return running
}

// ContextAfterStatements is contextAfterStatements in the TS source:
// the context after a WALKED prefix — the exit rows every statement
// in it left behind, folded in order. The rows live on a side
// channel keyed by statement, so a second walker (the flow-state
// seam) reads the same rows the list walk ran under.
func ContextAfterStatements(ctx *FlowContext, statements []*ast.Node) *FlowContext {
	running := ctx
	for _, statement := range statements {
		running = ContextWithExitRows(running, statement)
	}
	return running
}

// mergedForeignOverrides combines two override maps for the SAME
// statement index — existing may be nil (the ordinary case, nothing
// pending yet at that key) or may already hold one edge's entry (a
// diamond, or a call's own CallOverride landing on the same statement
// as another edge's parse Override). Distinct node keys simply union;
// this never arises for the SAME key today (a call node and its own
// later parse node are never equal), so there is no overwrite rule to
// pick between two claims about one node — only two maps to fold into
// one.
func mergedForeignOverrides(
	existing map[*ast.Node]abstractdomain.AbstractValue,
	added map[*ast.Node]abstractdomain.AbstractValue,
) map[*ast.Node]abstractdomain.AbstractValue {
	if len(existing) == 0 {
		return added
	}
	merged := make(map[*ast.Node]abstractdomain.AbstractValue, len(existing)+len(added))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range added {
		merged[k] = v
	}
	return merged
}

func listWalk(ctx *FlowContext, env Env, statements []*ast.Node, result *annotations.DeclaredRefinement) bool {
	running := ctx
	// walked: the index this list has already reached. The relational
	// accumulation consumes the statement AFTER the loop as well, so the
	// list resumes past it rather than walking it a second time.
	walked := 0
	// pendingForeignOverrides: every CROSS-LANGUAGE edge's pinned parse
	// result recognized so far but not yet consumed, keyed by the index
	// of the statement it belongs to (foreign_edge.go). The edge's call
	// and the JSON.parse that reads its stdout are two statements, and
	// the fact on the parse comes from another language's checker —
	// nothing this walk does to that node can reach it. So the value
	// rides ctx.NodeOverrides for exactly the one statement that contains
	// the parse, set below and restored by leaving the copy.
	//
	// THE RULE: an override is keyed PER EDGE (its own OverrideStatement
	// index), never held in a single scalar slot. A DIAMOND shape — two
	// execFileSync calls recognized back to back, each naming a LATER
	// statement as its own parse's home — must not let the second
	// recognition overwrite the first's still-pending entry: each is
	// consumed independently, exactly once, at its own index, and an
	// entry never consumed by the time this list finishes walking simply
	// expires when the map falls out of scope with the function return —
	// the same expiry a single scalar slot already had, now per-key
	// instead of per-call.
	pendingForeignOverrides := map[int]map[*ast.Node]abstractdomain.AbstractValue{}
	for index, statement := range statements {
		if index < walked {
			continue
		}
		// the spawn (async) return leg: recognized separately from every
		// OTHER cross-language shape, because serving it means WALKING two
		// callback bodies (spawn_callback_serve.go) rather than pinning one
		// override on a statement the ordinary walk already reaches. Tried
		// BEFORE ForeignEdgeAt at the same statement so a served pair
		// reports once, through this route's own sentence, rather than
		// twice (ForeignEdgeAt's own spawn recognizer still declines
		// internally — see foreign_edge.go's spawnAsyncEdgeOf — so running
		// both would double-report the same construct).
		if spawnOutcome, isSpawn := ServeSpawnReturnLeg(running, env, statements, index); isSpawn {
			if spawnOutcome.Decline != "" && spawnOutcome.DeclineNode != nil {
				running.Report(assignability.At(spawnOutcome.DeclineNode, 7002, spawnOutcome.Decline))
			}
			if running.ConsumedForeignSink != nil && spawnOutcome.TargetPath != "" {
				*running.ConsumedForeignSink = append(*running.ConsumedForeignSink, spawnOutcome.TargetPath)
			}
		} else if outcome, isEdge := ForeignEdgeAt(running, env, statements, index); isEdge {
			// a recognized cross-language call: its premises are discharged
			// here, where the environment still holds what crosses out
			if outcome.Decline != "" && outcome.DeclineNode != nil {
				running.Report(assignability.At(outcome.DeclineNode, 7002, outcome.Decline))
			}
			if outcome.Override != nil {
				// keyed by ITS OWN statement index — a second recognized
				// edge earlier in the list (a diamond's own second call)
				// never touches this entry, and this one never touches
				// another edge's still-pending entry either
				pendingForeignOverrides[outcome.OverrideStatement] = mergedForeignOverrides(
					pendingForeignOverrides[outcome.OverrideStatement], outcome.Override)
			}
			if outcome.CallOverride != nil {
				// the intermediate captured-stdout binding's own pin
				// (foreign_edge.go's file banner, step 5): keyed at
				// edge.Call's own statement — CallOverrideStatement is
				// always THIS index, the statement listWalk is about to
				// walk right below, never a later one the way the parse's
				// Override is. Without this merge, evaluateExpression never
				// sees the pin (ctx.NodeOverrides reads it, but nothing
				// upstream of this loop ever sets it from CallOverride),
				// so the bound name's initializer keeps evaluating
				// ORDINARILY — exactly the gap that let a declared
				// `string | Buffer` return flow through as a kindUnion
				// instead of the plain-string-ground/serialized-set claim
				// this route exists to attach. Merged (not overwritten)
				// with whatever the SAME statement's Override may already
				// carry, the same discipline the Override branch above
				// keeps for a diamond's second edge.
				pendingForeignOverrides[outcome.CallOverrideStatement] = mergedForeignOverrides(
					pendingForeignOverrides[outcome.CallOverrideStatement], outcome.CallOverride)
			}
			// consumer-index prerequisite (docs/one-checker/lsp-coordinator.md
			// build plan item 3): every edge this walk recognized — fired,
			// declined, or served — names a foreign target this check
			// consumed, read back through service.CheckResult.ConsumedForeignTargets.
			if running.ConsumedForeignSink != nil && outcome.TargetPath != "" {
				*running.ConsumedForeignSink = append(*running.ConsumedForeignSink, outcome.TargetPath)
			}
		}
		if override, isPending := pendingForeignOverrides[index]; isPending {
			pinning := *running
			pinning.NodeOverrides = override
			exits := AnalyzeStatement(&pinning, env, statement, result)
			delete(pendingForeignOverrides, index)
			if exits {
				return true
			}
			running = ContextWithExitRows(running, statement)
			walked = index + 1
			continue
		}
		// accumulate-then-divide-by-count spans TWO statements, and the
		// relation tying the sum to the count dies at a kernel program
		// boundary — so the pair is harvested here, from the states as
		// they stand before either runs, and lowered into one program
		// (relational_accumulation.go). Both statements still walk
		// ordinarily below; what the kernel proved MEETS what they left.
		accumulation, hasAccumulation := RelationalAccumulationOf(running, env, statements, index)
		exitsHere := AnalyzeStatement(running, env, statement, result)
		if running.StatementObserver != nil {
			running.StatementObserver(env, exitsHere)
		}
		if exitsHere {
			return true
		}
		// a condition-tested loop hands its NEGATED condition to the
		// rest of this list — the continuation the loop cannot reach
		running = ContextWithExitRows(running, statement)
		walked = index + 1
		if !hasAccumulation {
			continue
		}
		// the kernel is asked BEFORE the second statement walks: a return
		// shape needs the proved quotient in hand to pin it on the
		// division node the walk is about to reach. A refusal leaves both
		// answers unset and the statement walks exactly as it would have.
		answer, answered := AskRelationalAccumulation(accumulation)
		if answered {
			MeetRelationalTotalInto(env, accumulation, answer)
		}
		division := statements[index+1]
		exits := func() bool {
			// the override is set around THIS ONE statement and restored
			// after — flow_context.go's NodeOverrides states the obligation,
			// and it is ReturnSink's own set-then-restore discipline
			if answered {
				if override, pinned := RelationalQuotientOverride(running, env, accumulation, answer); pinned {
					pinning := *running
					pinning.NodeOverrides = override
					return AnalyzeStatement(&pinning, env, division, result)
				}
			}
			return AnalyzeStatement(running, env, division, result)
		}()
		// the declaration shape's quotient lands on its bound name AFTER
		// the declaration walked it; the return shape bound nothing and
		// already consumed its answer through the override above
		if answered {
			MeetRelationalQuotientInto(env, accumulation, answer)
		}
		if running.StatementObserver != nil {
			running.StatementObserver(env, exits)
		}
		if exits {
			return true
		}
		running = ContextWithExitRows(running, division)
		walked = index + 2
	}
	return false
}

// AnalyzeStatement is analyzeStatement in the TS source: one
// statement, named by its syntactic form — the same grain as an
// expression visit. A branch or loop that lowers to the kernel's
// flow IR is ALSO walked engine-side from its pre-statement states,
// and the engine's proved exit claims meet the walked environment —
// what flows onward carries both.
func AnalyzeStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	entry, hasEntry := EngineEntryOf(env, statement, func(node *ast.Node) BindingKind {
		// the host's own type at the occurrence — the sort layer
		t := typereading.TypeAtLocation(ctx.P.Checker, node)
		flags := t.Flags()
		numOrBool := checker.TypeFlagsNumber | checker.TypeFlagsNumberLiteral |
			checker.TypeFlagsBoolean | checker.TypeFlagsBooleanLiteral
		if (flags&numOrBool) != 0 && (flags & ^numOrBool) == 0 {
			return BindingKindNumber
		}
		strOrLit := checker.TypeFlagsString | checker.TypeFlagsStringLiteral
		if (flags&strOrLit) != 0 && (flags & ^strOrLit) == 0 {
			return BindingKindString
		}
		return BindingKindUnknown
	})
	exits := walkStatementForm(ctx, env, statement, result)
	if hasEntry && !exits {
		EngineMeetInto(env, entry)
	}
	return exits
}

func walkStatementForm(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	if ast.IsVariableStatement(statement) {
		return AnalyzeVariableStatement(ctx, env, statement)
	}
	if ast.IsExpressionStatement(statement) {
		expr := statement.AsExpressionStatement().Expression
		evaluateExpression(ctx, env, expr)
		// a bare assertion call: control continues only where the
		// callee's guard did not throw, so its whenFalse claims land
		if ast.IsCallExpression(expr) {
			survived, ok := narrowing.AssertionCallNarrowings(ctx.P.Checker, expr, func(name string) bool {
				_, has := env.Get(name)
				return has
			})
			if ok {
				for _, n := range survived {
					env.Set(n.Binding, narrowing.ApplyNarrowed(envOrResidue(env, n.Binding), n))
				}
			}
		}
		return false
	}
	if ast.IsReturnStatement(statement) {
		return AnalyzeReturnStatement(ctx, env, statement, result)
	}
	if ast.IsThrowStatement(statement) {
		evaluateExpression(ctx, env, statement.AsThrowStatement().Expression)
		if ctx.ThrowSink != nil {
			*ctx.ThrowSink = append(*ctx.ThrowSink, env.Clone())
		}
		return true
	}
	if ast.IsBlock(statement) {
		return AnalyzeBlockStatement(ctx, env, statement, result)
	}
	if ast.IsIfStatement(statement) {
		return AnalyzeIfStatement(ctx, env, statement, result)
	}
	if ast.IsWhileStatement(statement) || ast.IsDoStatement(statement) ||
		ast.IsForStatement(statement) || ast.IsForOfStatement(statement) || ast.IsForInStatement(statement) {
		return AnalyzeLoopStatement(ctx, env, statement, result)
	}
	if ast.IsFunctionDeclaration(statement) {
		return false // walked by contract
	}
	if ast.IsBreakStatement(statement) || ast.IsContinueStatement(statement) {
		// control leaves this statement list; the loop solver and the
		// switch join already account for where it goes — a LABELED
		// break records its state for the label's rejoin point, and a
		// bare continue records its state for the next iteration's entry
		if ast.IsContinueStatement(statement) && statement.AsContinueStatement().Label == nil && ctx.ContinueSink != nil {
			*ctx.ContinueSink = append(*ctx.ContinueSink, env.Clone())
		}
		if ast.IsBreakStatement(statement) && statement.AsBreakStatement().Label != nil {
			if sink, ok := ctx.LabelSinks[statement.AsBreakStatement().Label.Text()]; ok {
				*sink = append(*sink, env.Clone())
			}
		} else if ast.IsBreakStatement(statement) && ctx.BreakSink != nil {
			// a bare break inside a switch clause: control resumes AFTER the
			// switch, carrying this state, not out of the enclosing list
			*ctx.BreakSink = append(*ctx.BreakSink, env.Clone())
		}
		return true
	}
	if ast.IsLabeledStatement(statement) {
		// breaks to this label REJOIN here: each recorded its state, and
		// the fall-through state (when the body did not exit) joins them
		labeled := statement.AsLabeledStatement()
		rejoined := []Env{}
		sinks := map[string]*[]Env{}
		for k, v := range ctx.LabelSinks {
			sinks[k] = v
		}
		sinks[labeled.Label.Text()] = &rejoined
		inner := *ctx
		inner.LabelSinks = sinks
		exits := AnalyzeStatement(&inner, env, labeled.Statement, result)
		if len(rejoined) == 0 {
			return exits
		}
		var joined Env
		if !exits {
			joined = env
		}
		for _, snapshot := range rejoined {
			if joined == nil {
				joined = snapshot
			} else {
				joined = JoinEnvs(joined, snapshot)
			}
		}
		if joined != nil {
			ReplaceEnv(env, joined)
		}
		return false
	}
	if ast.IsClassDeclaration(statement) {
		// methods and function-valued fields walk as contracts; a PLAIN
		// field with a stated type owes its initializer here, where the
		// value is written
		for _, member := range statement.AsClassDeclaration().Members.Nodes {
			if !ast.IsPropertyDeclaration(member) {
				continue
			}
			pd := member.AsPropertyDeclaration()
			if pd.Initializer == nil || pd.Type == nil || ast.IsArrowFunction(pd.Initializer) || ast.IsFunctionExpression(pd.Initializer) {
				continue
			}
			read := annotations.AnnotationOfType(ctx.P, pd.Type, ctx.Registry, ctx.Objects)
			if read.Unsupported == "" && read.Stated != nil {
				CheckAssignability(ctx, evaluateExpression(ctx, env, pd.Initializer), *read.Stated, pd.Initializer, "a field initializer", nil)
			}
		}
		// a constructor never registers as a FunctionContract, so no
		// other pass ever judges its own `this.key = value` writes
		// against the written field's declared type (constructor_field_writes.go)
		checkConstructorFieldWrites(ctx, statement)
		// a static block or an initializer can still write outer names
		havocAssigned(ctx, env, statement)
		return false
	}
	if ast.IsSwitchStatement(statement) {
		return AnalyzeSwitchStatement(ctx, env, statement, result)
	}
	if ast.IsTryStatement(statement) {
		return AnalyzeTryStatement(ctx, env, statement, result)
	}
	if ast.IsWithStatement(statement) {
		return AnalyzeWithStatement(ctx, env, statement, result)
	}
	// any statement kind the walker does not model: whatever it may
	// write is forgotten — unmodeled control flow can HIDE nothing
	havocAssigned(ctx, env, statement)
	return false
}

// havocAssigned is havocAssigned in the TS source: forget every name
// a subtree may write — the catch-all that keeps unmodeled
// statements from preserving stale facts. A value-sorted word handed
// to a call travels by copy, so it survives.
func havocAssigned(ctx *FlowContext, env Env, node *ast.Node) {
	written := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, node, written)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, node, written, nil)
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
}
