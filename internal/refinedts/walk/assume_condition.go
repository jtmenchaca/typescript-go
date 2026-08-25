// from control_flow/assume_condition.ts
//
// The assume operator: everything a condition proves, applied at a
// branch split — ONE routine for every site that splits on a
// condition (if, ternary, &&/||, switch(true)), so no site gets a
// hand-picked subset of the narrowing families. Each side comes back
// as an environment and a context: the set narrowings, the
// structural narrowings, value-copy narrowings, inverse-factor and
// length-guard narrowings on the environment; the condition's
// difference rows and sum rows on the context — the TRUE side's held
// rows, the FALSE side's refuted rows (¬(a < b) proves a ≥ b of real
// pairs). Dead sides are decided here too: a computed verdict, a
// path-condition assumption, or a narrowing a known value
// contradicts.
//
// CROSS-DIRECTORY: dataflowfacts.DifferenceConstraintsOf,
// NegatedDifferenceConstraintsOf (dataflow_facts/difference_constraints.ts),
// LengthGuardNarrowings (dataflow_facts/length_guard_narrowings.ts), and
// SumConstraintsOf/NoteSumExitConstraints (dataflow_facts/sum_constraints.ts)
// are now landed in package dataflowfacts, but each answers NO ROWS —
// they need narrowing/condition_tree.ts's conditionTreeOf/conjunctiveLeaves,
// and dataflowfacts cannot import narrowing (narrowing already imports
// dataflowfacts in several files — a true cross-package cycle; see
// dataflowfacts/difference_constraints.go's banner). The sound "nothing
// vouched" fallback these answer is exactly what this file's own
// heldConstraints/refutedConstraints/etc. already tolerate when a guard
// proves nothing. InverseFactorNarrowings (dataflow_facts/
// inverse_factor_narrowings.ts) landed in THIS package instead (walk/
// inverse_factor_narrowings.go) — it needs narrowing.SideBounds/Narrowed,
// which would close the same cycle from dataflowfacts, but walk already
// imports narrowing one-way, so it fits here and is called unqualified.
// AssumedVerdict is this package's own (correlation_gate.go /
// path_conditions.go).

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// registerSumConstraints is registerSumConstraints in the TS source:
// sum rows carry the same flow-sensitive invalidation order rows do
// — each registers with EVERY name it roots in — both terms and the
// anchor — so a write through any alias of any of them kills it.
func registerSumConstraints(ctx *FlowContext, rows []dataflowfacts.SumConstraint) {
	for i := range rows {
		row := &rows[i]
		ctx.Aliases.RegisterRooted(row, []string{
			row.Terms[0].BaseName,
			row.Terms[1].BaseName,
			row.Anchor.BaseName,
		})
	}
}

// InfeasibleBranch is infeasibleBranch in the TS source: a branch is
// DEAD when a narrowing contradicts a singleton value — the runtime
// cannot take it, so its writes never happen and its value never
// joins. Decided only on facts a known real decides — everything
// else stays feasible.
func InfeasibleBranch(env Env, ns []narrowing.Narrowed) bool {
	for _, n := range ns {
		held, ok := env.Get(n.Binding)
		if !ok {
			continue
		}
		// a PATH narrowing reads the key the path names, one step per
		// segment through the object's own keys — the same exact-key
		// reading the object rebuild does. A step that is not an object,
		// or a key the object does not carry, says nothing about the
		// leaf, so the narrowing keeps today's skip; only a leaf the
		// walk holds EXACTLY can contradict anything.
		if len(n.Path) != 0 {
			leaf, reached := exactKeyAtPath(held, n.Path)
			if !reached {
				continue
			}
			held = leaf
		}
		if held.Kind != abstractdomain.KindValues {
			continue
		}
		if len(held.Values) != 1 || held.KindTag != abstractdomain.PrimitiveNumber {
			continue
		}
		v := held.Values[0]
		for _, form := range n.Forms {
			if form.Form == refinementsets.FormOneOf && !floatsInclude(form.W, v) {
				return true
			}
			if form.Form == refinementsets.FormAtLeast && v < form.A {
				return true
			}
			if form.Form == refinementsets.FormAbove && v <= form.A {
				return true
			}
			if form.Form == refinementsets.FormAtMost && v > form.A {
				return true
			}
			if form.Form == refinementsets.FormBelow && v >= form.A {
				return true
			}
			if form.Form == refinementsets.FormDifference {
				// membership in A∖B needs v ∉ B: a v inside B kills the
				// branch whatever A holds
				excluded := len(form.B.Forms) == 1 &&
					form.B.Forms[0].Form == refinementsets.FormOneOf &&
					floatsInclude(form.B.Forms[0].W, v)
				if excluded {
					return true
				}
			}
		}
	}
	return false
}

// exactKeyAtPath walks a narrowing's path into what a binding holds and
// answers the value at the leaf, when every step reaches the value the
// segment names: an object carrying that key, or a list carrying that
// item. A maybe wrapper is stepped through: the path narrowing only
// speaks about runs where the value was there, and the dead-branch
// reading below only fires on an exact contradiction, which the absent
// case cannot produce. Anything else answers not-reached, and the caller
// leaves the narrowing alone.
func exactKeyAtPath(held abstractdomain.AbstractValue, path []string) (abstractdomain.AbstractValue, bool) {
	for _, key := range path {
		if held.Kind == abstractdomain.KindPossiblyUndefined && held.Inner != nil {
			held = *held.Inner
		}
		// an INDEX segment reads a list item by position; it names no
		// object key, so an object under one is not reached. The bare
		// Items read needs no absence: a KindList is hole-free by
		// construction (element_access.go states the argument), and an
		// elision already holds Undef in its own slot, which the exact
		// reading below treats as the value it is.
		if slot, isIndex := dataflowfacts.IndexSegmentOf(key); isIndex {
			if held.Kind != abstractdomain.KindList || slot >= len(held.Items) {
				return abstractdomain.AbstractValue{}, false
			}
			held = held.Items[slot]
			continue
		}
		if held.Kind != abstractdomain.KindObject {
			return abstractdomain.AbstractValue{}, false
		}
		index, hasKey := objectKeyIndex(held, key)
		if !hasKey {
			return abstractdomain.AbstractValue{}, false
		}
		held = held.Keys[index].Value
	}
	if held.Kind == abstractdomain.KindPossiblyUndefined && held.Inner != nil {
		held = *held.Inner
	}
	return held, true
}

func floatsInclude(list []float64, v float64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ConditionEnvTransfersSite mirrors conditionEnvTransfers' `site`
// parameter.
type ConditionEnvTransfersSite struct {
	// At: the node value-copy collection scans from.
	At *ast.Node
	// SideWindow: comparison-side windows — a loop passes an
	// entry-state reader gated to names it never writes (an ungated
	// window would go stale by the second iteration); a branch site
	// leaves this nil and the current environment answers.
	SideWindow    narrowing.SideBounds
	ReadElsewhere narrowing.GuardReadElsewhere
}

// ConditionEnvTransfers is the ENVIRONMENT transfers one condition
// proves, packaged for any site that applies them repeatedly — a
// branch applies each side once; a loop applies the held side at
// every body entry and the refuted side at the exit. Rows are NOT
// here: each site owns its own row discipline (branches carry rows
// on their contexts; loops invalidate and revalidate theirs per
// entry).
type ConditionEnvTransfers struct {
	WhenTrue  []narrowing.Narrowed
	WhenFalse []narrowing.Narrowed
	// ApplyWhenTrue applies everything the HELD condition proves to
	// `into`: the narrowings with their value copies, the inverse
	// product factors, the length-guard floors.
	ApplyWhenTrue func(into Env)
	// ApplyWhenFalse applies everything the REFUTED condition proves
	// to `into`.
	ApplyWhenFalse func(into Env)
}

// ConditionEnvTransfersOf is conditionEnvTransfers in the TS source.
func ConditionEnvTransfersOf(ctx *FlowContext, env Env, expression *ast.Node, site ConditionEnvTransfersSite) ConditionEnvTransfers {
	// a condition BOUND TO A NAME keeps every fact it encodes;
	// narrowings() resolves bound names internally
	condition := narrowing.BoundConditionInitializer(ctx.P.Checker, expression)
	if condition == nil {
		condition = expression
	}
	windows := site.SideWindow
	if windows == nil {
		windows = SideBoundsIn(ctx, env)
	}
	branches := narrowing.Narrowings(ctx.P.Checker, expression, func(name string) bool {
		_, ok := env.Get(name)
		return ok
	}, windows, site.ReadElsewhere)
	// a held product guard (`x * y > k`) inverted: each factor narrows
	// by the quotient of k and the other factor's window
	inverse := InverseFactorNarrowings(ctx.P.Checker, ctx.Kernel, condition, func(name string) bool {
		_, ok := env.Get(name)
		return ok
	}, windows)
	// a VALUE COPY rides its source's narrowing: `const n = o.n` with
	// neither n nor o written in this function still equals o.n when
	// the guard tests it — applied on BOTH sides, and in BOTH
	// directions: a guard on the place narrows its copies, a guard on
	// the copy narrows the source place
	applySide := func(into Env, ns []narrowing.Narrowed) {
		for _, n := range ns {
			into.Set(n.Binding, narrowing.ApplyNarrowed(envOrResidue(into, n.Binding), n))
			if len(n.Path) > 0 {
				// the PLACE-VALUE memory: a root whose own shape cannot
				// absorb the path — an unknown behind Array.isArray, a
				// sequence's length — remembers the narrowed value under the
				// DOTTED key (dots never appear in identifiers), so the
				// entry rides the environment's own forking and joining;
				// writes through the root sweep it (assignments.ts)
				//
				// A LIST root absorbs the write directly when the path names
				// one of its items — a single INDEX segment inside the items
				// it carries. ApplyNarrowed already rebuilt the root with
				// that item narrowed (apply_narrowing.go's list arm), so the
				// env.Set above IS the write, and the fallback would only
				// record a second copy of the same fact under a dotted key
				// no reader would prefer. Everything else — an index past the
				// items, a deeper path, a non-list root — keeps the memory.
				root := envOrResidue(into, n.Binding)
				rootAbsorbs := root.Kind == abstractdomain.KindObject ||
					(root.Kind == abstractdomain.KindPossiblyUndefined && root.Inner != nil && root.Inner.Kind == abstractdomain.KindObject)
				if !rootAbsorbs && root.Kind == abstractdomain.KindList && len(n.Path) == 1 {
					if slot, isIndex := dataflowfacts.IndexSegmentOf(n.Path[0]); isIndex && slot < len(root.Items) {
						rootAbsorbs = true
					}
				}
				if !rootAbsorbs {
					pathKey := n.Binding
					for _, p := range n.Path {
						pathKey += "." + p
					}
					pathless := n
					pathless.Path = nil
					into.Set(pathKey, narrowing.ApplyNarrowed(envOrResidue(into, pathKey), pathless))
				}
				for _, copy := range narrowing.CopyBindingsOf(ctx.P.Checker, site.At, dataflowfacts.TrackedPlace{Binding: n.Binding, Path: n.Path}) {
					if _, ok := into.Get(copy); !ok {
						continue
					}
					renamed := n
					renamed.Binding = copy
					renamed.Path = nil
					into.Set(copy, narrowing.ApplyNarrowed(envOrResidue(into, copy), renamed))
				}
			}
			if len(n.Path) == 0 {
				source, ok := narrowing.CopySourcePlaceOf(ctx.P.Checker, site.At, n.Binding)
				if ok {
					if _, has := into.Get(source.Binding); has {
						renamed := n
						renamed.Binding = source.Binding
						renamed.Path = source.Path
						into.Set(source.Binding, narrowing.ApplyNarrowed(envOrResidue(into, source.Binding), renamed))
					}
				}
			}
		}
		// SECOND PASS: a conjunction's narrowings apply in list order,
		// and a refutation is honestly a no-op on a binding still
		// unknown — so `[0.5, 1.5].includes(x) && x !== 1.5` needs the
		// subtraction to run again once the pin has landed (and the
		// mirrored spelling needs the pin met against the subtraction).
		// Every application is intersective (apply_narrowing.go's
		// Exact-meet and scatter-membership arms), so reapplying
		// tightens or holds, never widens.
		for _, n := range ns {
			into.Set(n.Binding, narrowing.ApplyNarrowed(envOrResidue(into, n.Binding), n))
		}
	}
	isStringKindAt := func(e *ast.Node) bool {
		return primitives.IsStringKind(ctx.P.Checker, e)
	}
	return ConditionEnvTransfers{
		WhenTrue:  branches.WhenTrue,
		WhenFalse: branches.WhenFalse,
		ApplyWhenTrue: func(into Env) {
			applySide(into, branches.WhenTrue)
			for _, n := range inverse {
				into.Set(n.Binding, narrowing.ApplyNarrowed(envOrResidue(into, n.Binding), n))
			}
			// a held length guard (`xs.length >= k`) raises a repetition's
			// counting floor — read from the target state, which the test
			// just passed
			for _, n := range dataflowfacts.LengthGuardNarrowings(into.Get, condition, isStringKindAt, false) {
				into.Set(n.Binding, n.Known)
			}
			// a held Map presence guard (`m.has("k")`) proves the key an
			// entry — read from the target state, which the test just
			// passed
			for _, n := range dataflowfacts.MapPresenceNarrowings(into.Get, condition, false) {
				into.Set(n.Binding, n.Known)
			}
			// a held zero-exclusion guard (`x !== 0`) retreats the tested
			// place's own window/list endpoint at zero
			for _, n := range dataflowfacts.ZeroExclusionNarrowings(into.Get, condition, false) {
				into.Set(n.Binding, n.Known)
			}
		},
		ApplyWhenFalse: func(into Env) {
			applySide(into, branches.WhenFalse)
			// refuted, the length guard caps the floor instead
			for _, n := range dataflowfacts.LengthGuardNarrowings(into.Get, condition, isStringKindAt, true) {
				into.Set(n.Binding, n.Known)
			}
			// kept symmetric with the held side above — a refuted `.has`
			// proves absence, not presence, so this reads zero rows for a
			// bare `.has` leaf today; a future OR-composed leaf can still
			// reach it through the same shared conjunctive-leaf channel
			for _, n := range dataflowfacts.MapPresenceNarrowings(into.Get, condition, true) {
				into.Set(n.Binding, n.Known)
			}
			// refuted, a zero-exclusion guard reads the leaf's OWN negated
			// polarity: `!(x !== 0)` proves x could be 0, and excludes
			// nothing — the exit-guard shape below is where the refuted
			// `=== 0` form actually excludes
			for _, n := range dataflowfacts.ZeroExclusionNarrowings(into.Get, condition, true) {
				into.Set(n.Binding, n.Known)
			}
		},
	}
}

func envOrResidue(env Env, name string) abstractdomain.AbstractValue {
	if v, ok := env.Get(name); ok {
		return v
	}
	return silence.Residue()
}

// AssumedBranch mirrors the TS AssumedBranch interface.
type AssumedBranch struct {
	// Env: the entry environment with this side's narrowings applied.
	Env Env
	// Ctx: the context with this side's condition rows riding — the
	// TRUE side carries the held rows, the FALSE side the refuted
	// ones.
	Ctx *FlowContext
	// Dead: the runtime cannot take this side.
	Dead bool
}

// AssumeConditionScope mirrors assumeCondition's `site` parameter.
type AssumeConditionScope struct {
	// WhenTrueScope: where the TRUE side's rows must stay stable (the
	// branch).
	WhenTrueScope *ast.Node
	// WhenFalseScope: where the FALSE side's rows must stay stable.
	WhenFalseScope *ast.Node
	// At: the node value-copy collection scans from.
	At *ast.Node
}

// AssumedCondition mirrors the TS AssumedCondition interface.
type AssumedCondition struct {
	WhenTrue  AssumedBranch
	WhenFalse AssumedBranch
	// HeldConstraints: the held (true-side) rows — the continuation
	// after an else-exit carries them (NoteExitConstraints is the
	// caller's, at its statement).
	HeldConstraints []dataflowfacts.DifferenceConstraint
	// RefuteIntoContinuation: the refuted condition carried into the
	// CONTINUATION after a then-exit — records the negated rows and
	// sum rows at `statement` for the statement list to pick up, and
	// applies the negated length guards to the continuing
	// environment.
	RefuteIntoContinuation func(statement *ast.Node, scope *ast.Node, continuation Env)
}

// assumeCondition is assumeCondition in the TS source: everything
// one condition proves, both sides — see the header. The caller
// evaluates the condition expression first (for its effects and its
// verdict) and passes ToBoolean's verdict when it pinned one (via
// computedVerdict/hasComputedVerdict); the sides this routine
// evaluates itself are the comparison operands the row machinery
// windows, exactly as the if-arm always did.
func assumeCondition(
	ctx *FlowContext,
	env Env,
	expression *ast.Node,
	site AssumeConditionScope,
	computedVerdict bool,
	hasComputedVerdict bool,
) AssumedCondition {
	// a condition tested INSIDE a with body resolves its subject names
	// against the scope object, which a getter can answer differently
	// at every read — no narrowing, difference/sum row, or length
	// guard this condition would otherwise prove is trustworthy, so
	// nothing is recorded and both sides carry the entry env unchanged
	if expression.Flags&ast.NodeFlagsInWithStatement != 0 {
		return AssumedCondition{
			WhenTrue:  AssumedBranch{Env: env.Clone(), Ctx: ctx, Dead: hasComputedVerdict && !computedVerdict},
			WhenFalse: AssumedBranch{Env: env.Clone(), Ctx: ctx, Dead: hasComputedVerdict && computedVerdict},
			RefuteIntoContinuation: func(statement *ast.Node, scope *ast.Node, continuation Env) {
			},
		}
	}
	// a condition BOUND TO A NAME keeps every fact it encodes for the
	// ROW machinery; narrowings() resolves bound names internally
	condition := narrowing.BoundConditionInitializer(ctx.P.Checker, expression)
	if condition == nil {
		condition = expression
	}
	exactWindowOf := func(side *ast.Node) (dataflowfacts.ExactWindow, bool) {
		r := RangeOfKnown(evaluateExpression(ctx, env, side))
		if r == nil {
			return dataflowfacts.ExactWindow{}, false
		}
		return dataflowfacts.ExactWindow{Lo: r.Lo, Hi: r.Hi, Int: r.Int}, true
	}
	exactValueOf := func(side *ast.Node) (float64, bool) {
		if !ast.IsIdentifier(side) {
			return 0, false
		}
		held := evaluateExpression(ctx, env, side)
		if held.Kind == abstractdomain.KindValues && held.KindTag == abstractdomain.PrimitiveNumber && len(held.Values) == 1 {
			return held.Values[0], true
		}
		// a const bound to a literal outside the walked scope (a
		// module-level gap) — const, so the value never moves. The
		// initializer follows its const-to-const links, so
		// `const M = 10; const N = M;` reads N as 10 (const_chain_literal.go).
		return dataflowfacts.ConstChainNumber(ctx.P.Checker, side)
	}
	// an offset side whose arithmetic leaves the safe range: the
	// kernel's proved envelope on |fl(x + k) − (x + k)| widens the
	// row's bound instead of dropping the row
	flSlackOf := func(window dataflowfacts.ExactWindow, offset float64) (float64, bool) {
		if !window.Int || math.IsInf(window.Lo, 0) || math.IsNaN(window.Lo) || math.IsInf(window.Hi, 0) || math.IsNaN(window.Hi) {
			return 0, false
		}
		return ctx.Kernel.Envelope(
			"add",
			refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(window.Lo), refinementsets.AtMost(window.Hi)),
			refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(offset), refinementsets.AtMost(offset)),
		)
	}
	// ¬(a < b) proves a ≥ b only of a real pair: a set-known side is
	// NaN-free by construction (no set holds NaN)
	realSide := func(side *ast.Node) bool {
		held := evaluateExpression(ctx, env, side)
		if held.Kind == abstractdomain.KindSet {
			return true
		}
		if held.Kind == abstractdomain.KindValues && held.KindTag == abstractdomain.PrimitiveNumber {
			for _, v := range held.Values {
				if math.IsNaN(v) {
					return false
				}
			}
			return true
		}
		return false
	}

	heldConstraints := dataflowfacts.DifferenceConstraintsOf(ctx.P.Checker, condition, site.WhenTrueScope, exactWindowOf, exactValueOf, flSlackOf)
	readElsewhere := narrowing.GuardReadNowhere
	if hasComputedVerdict {
		readElsewhere = narrowing.GuardReadVerdict
	} else if len(heldConstraints) > 0 {
		readElsewhere = narrowing.GuardReadRelation
	}
	transfers := ConditionEnvTransfersOf(ctx, env, expression, ConditionEnvTransfersSite{
		At:            site.At,
		ReadElsewhere: readElsewhere,
	})
	// under a path-condition split, a test of the assumed condition is
	// DECIDED: the pass models exactly the runs where it held (failed)
	assumedVerdictValue, hasAssumedVerdict := AssumedVerdict(ctx, ctx.GateAssumptions, expression)
	whenTrueDead := (hasAssumedVerdict && !assumedVerdictValue) ||
		(hasComputedVerdict && !computedVerdict) ||
		InfeasibleBranch(env, transfers.WhenTrue)
	whenFalseDead := (hasAssumedVerdict && assumedVerdictValue) ||
		(hasComputedVerdict && computedVerdict) ||
		InfeasibleBranch(env, transfers.WhenFalse)

	whenTrueEnv := env.Clone()
	transfers.ApplyWhenTrue(whenTrueEnv)
	whenFalseEnv := env.Clone()
	transfers.ApplyWhenFalse(whenFalseEnv)

	// the rows ride the branch contexts — held on the true side,
	// refuted on the false side — recorded only for places the branch
	// (and every closure) leaves unwritten
	registerDifferenceConstraints(ctx, heldConstraints)
	heldSumConstraints := dataflowfacts.SumConstraintsOf(ctx.P.Checker, condition, site.WhenTrueScope, false, nil)
	registerSumConstraints(ctx, heldSumConstraints)
	whenTrueCtx := ctx
	if len(heldConstraints) > 0 {
		next := *ctx
		next.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, ctx.DifferenceConstraints...), heldConstraints...)
		whenTrueCtx = &next
	}
	if len(heldSumConstraints) > 0 {
		next := *whenTrueCtx
		next.SumConstraints = append(append([]dataflowfacts.SumConstraint{}, whenTrueCtx.SumConstraints...), heldSumConstraints...)
		whenTrueCtx = &next
	}
	refutedConstraints := dataflowfacts.NegatedDifferenceConstraintsOf(ctx.P.Checker, condition, site.WhenFalseScope, exactWindowOf, exactValueOf, realSide, flSlackOf)
	registerDifferenceConstraints(ctx, refutedConstraints)
	refutedSumConstraints := dataflowfacts.SumConstraintsOf(ctx.P.Checker, condition, site.WhenFalseScope, true, realSide)
	registerSumConstraints(ctx, refutedSumConstraints)
	whenFalseCtx := ctx
	if len(refutedConstraints) > 0 {
		next := *ctx
		next.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, ctx.DifferenceConstraints...), refutedConstraints...)
		whenFalseCtx = &next
	}
	if len(refutedSumConstraints) > 0 {
		next := *whenFalseCtx
		next.SumConstraints = append(append([]dataflowfacts.SumConstraint{}, whenFalseCtx.SumConstraints...), refutedSumConstraints...)
		whenFalseCtx = &next
	}

	return AssumedCondition{
		WhenTrue:        AssumedBranch{Env: whenTrueEnv, Ctx: whenTrueCtx, Dead: whenTrueDead},
		WhenFalse:       AssumedBranch{Env: whenFalseEnv, Ctx: whenFalseCtx, Dead: whenFalseDead},
		HeldConstraints: heldConstraints,
		RefuteIntoContinuation: func(statement *ast.Node, scope *ast.Node, continuation Env) {
			// the continuation runs with the condition REFUTED: the false
			// side's rows ride the exit channel (the same one loops use),
			// recomputed at the continuation's own scope so stability
			// covers everything that follows
			exitConstraints := dataflowfacts.NegatedDifferenceConstraintsOf(ctx.P.Checker, condition, scope, exactWindowOf, exactValueOf, realSide, flSlackOf)
			registerDifferenceConstraints(ctx, exitConstraints)
			if len(exitConstraints) > 0 {
				dataflowfacts.NoteExitConstraints(statement, exitConstraints)
			}
			exitSumConstraints := dataflowfacts.SumConstraintsOf(ctx.P.Checker, condition, scope, true, realSide)
			registerSumConstraints(ctx, exitSumConstraints)
			if len(exitSumConstraints) > 0 {
				dataflowfacts.NoteSumExitConstraints(statement, exitSumConstraints)
			}
			for _, n := range dataflowfacts.LengthGuardNarrowings(env.Get, condition, func(e *ast.Node) bool { return primitives.IsStringKind(ctx.P.Checker, e) }, true) {
				continuation.Set(n.Binding, n.Known)
			}
			// refuted, a Map presence guard proves the SAME key present
			// past the exit — the `if (!m.has(k)) return;` shape
			for _, n := range dataflowfacts.MapPresenceNarrowings(env.Get, condition, true) {
				continuation.Set(n.Binding, n.Known)
			}
			// refuted, a zero-exclusion guard retreats the SAME endpoint
			// past the exit — `if (x === 0) return; …after: x !== 0`
			for _, n := range dataflowfacts.ZeroExclusionNarrowings(env.Get, condition, true) {
				continuation.Set(n.Binding, n.Known)
			}
		},
	}
}

func registerDifferenceConstraints(ctx *FlowContext, rows []dataflowfacts.DifferenceConstraint) {
	facts := make([]dataflowfacts.InvalidatableFact, len(rows))
	for i := range rows {
		facts[i] = &rows[i]
	}
	ctx.Aliases.Register(facts)
}
