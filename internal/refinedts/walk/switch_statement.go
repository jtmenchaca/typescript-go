// from control_flow/switch_statement.ts
//
// What a switch does to the env: exact-scrutinee keys select one
// path; otherwise clause joins, a break sink, and fallthrough havoc.

package walk

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// switchNumberOf reads a numeric literal (through a leading minus)
// used only by this file's syntactic label readers.
func switchNumberOf(e *ast.Node) (float64, bool) {
	return NumberOf(e)
}

// SwitchLabelValues is switchLabelValues in the TS source: the exact
// values a run of case labels admits, as one AbstractValue — all
// numeric labels pin the number words, one string label pins the
// tuple, several pin their union set. (zero, false) on any label
// this reading cannot pin (the clause then narrows nothing).
func SwitchLabelValues(labels []*ast.Node) (abstractdomain.AbstractValue, bool) {
	return SwitchLabelValuesWith(nil, labels)
}

// SwitchLabelValuesWith is SwitchLabelValues with the checker that
// resolves a const-bound label to its literal, so `const A = 1;
// switch (x) { case A: }` reads. A nil checker reads literal tokens
// only.
func SwitchLabelValuesWith(c *checker.Checker, labels []*ast.Node) (abstractdomain.AbstractValue, bool) {
	var numbersSeen []float64
	var stringsSeen []string
	for _, label := range labels {
		// a label follows its const-to-const links first, so the
		// reading below sees the literal the label names
		resolved, resolvedOk := dataflowfacts.ConstChainLiteral(c, label)
		if !resolvedOk {
			return abstractdomain.AbstractValue{}, false
		}
		if ast.IsNumericLiteral(resolved) {
			numbersSeen = append(numbersSeen, switchNumberMust(resolved))
		} else if n, ok := switchNumberOf(resolved); ok && ast.IsPrefixUnaryExpression(resolved) {
			numbersSeen = append(numbersSeen, n)
		} else if ast.IsStringLiteral(resolved) {
			stringsSeen = append(stringsSeen, resolved.AsStringLiteral().Text)
		} else if ast.IsNoSubstitutionTemplateLiteral(resolved) {
			stringsSeen = append(stringsSeen, resolved.Text())
		} else if resolved.Kind == ast.KindTrueKeyword {
			// a boolean label rides the number sort: true is the exact
			// word 1 and false the word 0, the spec's own ToNumber — the
			// same encoding every boolean literal in this package wears
			numbersSeen = append(numbersSeen, 1)
		} else if resolved.Kind == ast.KindFalseKeyword {
			numbersSeen = append(numbersSeen, 0)
		} else {
			return abstractdomain.AbstractValue{}, false
		}
	}
	if len(numbersSeen) > 0 && len(stringsSeen) > 0 {
		return abstractdomain.AbstractValue{}, false
	}
	if len(numbersSeen) > 0 {
		return abstractdomain.KnownValues(numbersSeen, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	if len(stringsSeen) == 1 {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(stringsSeen[0]), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	if len(stringsSeen) > 1 {
		set := refinementsets.StringTuple(stringsSeen[0])
		for _, s := range stringsSeen[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(s)))
		}
		return abstractdomain.KnownSet(set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone), true
	}
	return abstractdomain.AbstractValue{}, false
}

func switchNumberMust(e *ast.Node) float64 {
	v, _ := NumberOf(e)
	return v
}

// SwitchKeyOfKnown is switchKeyOfKnown in the TS source: the
// strict-equality key an exact scrutinee carries into a switch —
// sort-tagged so `true` never matches `1`. NaN keys as its own
// never-matching marker (NaN === NaN is false). ("", false) = not
// exact.
func SwitchKeyOfKnown(k abstractdomain.AbstractValue) (string, bool) {
	if k.Kind == abstractdomain.KindNaN {
		return "!nan", true
	}
	if k.Kind != abstractdomain.KindValues {
		return "", false
	}
	if k.KindTag == abstractdomain.PrimitiveString {
		runes := make([]rune, len(k.Values))
		for i, v := range k.Values {
			runes[i] = rune(int32(v))
		}
		return "s:" + string(runes), true
	}
	if len(k.Values) != 1 {
		return "", false
	}
	if k.KindTag == abstractdomain.PrimitiveBoolean {
		return fmt.Sprintf("b:%v", k.Values[0] != 0), true
	}
	if k.KindTag == abstractdomain.PrimitiveNumber {
		return "n:" + jsnum.Number(k.Values[0]).String(), true
	}
	return "", false
}

// SwitchKeyOfLabel is switchKeyOfLabel in the TS source: a case
// label's key, read SYNTACTICALLY — literals only, so no label
// expression ever evaluates on the decided path.
func SwitchKeyOfLabel(e *ast.Node) (string, bool) {
	bare := e
	if ast.IsParenthesizedExpression(e) {
		bare = e.AsParenthesizedExpression().Expression
	}
	if ast.IsNumericLiteral(bare) {
		return "n:" + jsnum.Number(switchNumberMust(bare)).String(), true
	}
	if ast.IsPrefixUnaryExpression(bare) {
		unary := bare.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			v, _ := NumberOf(bare)
			return "n:" + jsnum.Number(v).String(), true
		}
	}
	if ast.IsStringLiteral(bare) {
		return "s:" + bare.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(bare) {
		return "s:" + bare.Text(), true
	}
	if bare.Kind == ast.KindTrueKeyword {
		return "b:true", true
	}
	if bare.Kind == ast.KindFalseKeyword {
		return "b:false", true
	}
	return "", false
}

// AnalyzeSwitchStatement is analyzeSwitchStatement in the TS source.
func AnalyzeSwitchStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	switchStmt := statement.AsSwitchStatement()
	scrutinee := evaluateExpression(ctx, env, switchStmt.Expression)
	clauses := switchStmt.CaseBlock.AsCaseBlock().Clauses.Nodes

	// walk the selected path from clause `start`, fallthrough
	// included, with no join and no havoc. A break anywhere in the
	// walked path leaves the SWITCH; the state it carries rejoins
	// after it. A return or a throw leaves the enclosing list, as
	// it does anywhere else.
	analyzeFrom := func(start int) bool {
		var broke []Env
		inner := *ctx
		inner.BreakSink = &broke
		exits := false
		fellOut := true
	walkLoop:
		for i := start; i < len(clauses); i++ {
			clauseStatements := caseOrDefaultStatements(clauses[i])
			for _, s := range clauseStatements {
				if ast.IsBreakStatement(s) && s.AsBreakStatement().Label == nil {
					broke = append(broke, env.Clone())
					fellOut = false
					break walkLoop
				}
				if AnalyzeStatement(&inner, env, s, result) {
					exits = true
					fellOut = false
					break walkLoop
				}
			}
		}
		if len(broke) == 0 {
			return exits
		}
		var joined Env
		if fellOut {
			joined = env
		}
		for _, snapshot := range broke {
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

	// a DECIDED switch: an exact scrutinee against all-literal case
	// labels selects its one path. Labels are read SYNTACTICALLY
	// (never evaluated), so no label effect runs.
	if scrutineeKey, ok := SwitchKeyOfKnown(scrutinee); ok {
		selected := -1
		defaultIndex := -1
		decided := true
		for i := 0; i < len(clauses) && decided; i++ {
			clause := clauses[i]
			if ast.IsDefaultClause(clause) {
				defaultIndex = i
				continue
			}
			label, labelOk := SwitchKeyOfLabel(clause.AsCaseOrDefaultClause().Expression)
			if !labelOk {
				decided = false
			} else if selected == -1 && label == scrutineeKey {
				selected = i
			}
		}
		// NaN matches nothing — strict equality's own row
		if decided && scrutineeKey == "!nan" {
			selected = -1
		}
		if decided {
			start := selected
			if start == -1 {
				start = defaultIndex
			}
			if start == -1 {
				return false // no clause runs
			}
			return analyzeFrom(start)
		}
	}

	// a correlation pass decides the switch when an assumed gate is
	// `scrutinee === <label>`: the truthy pass SELECTS that clause
	// (distinct literals cannot also match), the falsy pass KILLS it
	excluded := -1
	if len(ctx.GateAssumptions) > 0 {
		place := dataflowfacts.PlaceKeyOf(ctx.P.Checker, switchStmt.Expression)
		if place != nil {
			for _, assumption := range ctx.GateAssumptions {
				if assumption.Base != place.Base {
					continue
				}
				prefix := place.Path + "==="
				if len(assumption.Detail) < len(prefix) || assumption.Detail[:len(prefix)] != prefix {
					continue
				}
				literal := assumption.Detail[len(prefix):]
				index := -1
				readable := true
				for i := 0; i < len(clauses) && readable; i++ {
					clause := clauses[i]
					if ast.IsDefaultClause(clause) {
						continue
					}
					label, labelOk := SwitchKeyOfLabel(clause.AsCaseOrDefaultClause().Expression)
					if !labelOk {
						readable = false
					} else if index == -1 && label == literal {
						index = i
					}
				}
				if !readable || index == -1 {
					continue
				}
				if assumption.Truthy {
					return analyzeFrom(index)
				}
				excluded = index
				break
			}
		}
	}

	// clauses ENDING in a bare break contribute their state to the
	// join; a clause that exits (return/throw) contributes nothing;
	// an EMPTY clause shares the next one's path. Any possible
	// fallthrough COMPOSES clauses — then the write model is not the
	// per-clause join, and everything the switch may write is
	// forgotten (sound as before).
	var contributions []Env
	precise := true
	hasDefault := false
	for index, clause := range clauses {
		// a clause the correlation pass excluded never runs in this
		// pass (fallthrough INTO it still degrades to the havoc path)
		if excluded == index {
			continue
		}
		if ast.IsDefaultClause(clause) {
			hasDefault = true
		}
		clauseStatements := caseOrDefaultStatements(clause)
		if len(clauseStatements) == 0 {
			continue
		}
		clauseEnv := env.Clone()
		// inside a case's body the discriminant WEARS the label: the
		// runtime took `scrutinee === label` to get here (fallthrough
		// from a non-empty clause lands in the havoc path, never
		// here). Empty clauses share the next body, so the pin is the
		// union of the contiguous labels above it and its own.
		if ast.IsCaseClause(clause) && ast.IsIdentifier(switchStmt.Expression) {
			if _, has := clauseEnv.Get(switchStmt.Expression.Text()); has {
				labels := []*ast.Node{clause.AsCaseOrDefaultClause().Expression}
				for back := index - 1; back >= 0; back-- {
					previous := clauses[back]
					if !ast.IsCaseClause(previous) || len(previous.AsCaseOrDefaultClause().Statements.Nodes) > 0 {
						break
					}
					labels = append(labels, previous.AsCaseOrDefaultClause().Expression)
				}
				if pinned, ok := SwitchLabelValuesWith(ctx.P.Checker, labels); ok {
					clauseEnv.Set(switchStmt.Expression.Text(), pinned)
				}
			}
		}
		// the DEFAULT arm runs only when no case label matched: an
		// exact-values discriminant sheds every readable label, and a
		// set-known one sheds them as a difference form
		if ast.IsDefaultClause(clause) && ast.IsIdentifier(switchStmt.Expression) {
			held, hasHeld := clauseEnv.Get(switchStmt.Expression.Text())
			var caseLabels []*abstractdomain.AbstractValue
			for _, c := range clauses {
				if !ast.IsCaseClause(c) {
					continue
				}
				if v, ok := SwitchLabelValuesWith(ctx.P.Checker, []*ast.Node{c.AsCaseOrDefaultClause().Expression}); ok {
					caseLabels = append(caseLabels, &v)
				} else {
					caseLabels = append(caseLabels, nil)
				}
			}
			// the labels the reader COULD pin as numbers are shed; an
			// unreadable label leaves whatever it names in place. Shedding
			// a subset is sound on its own: reaching the default arm means
			// EVERY case failed, so failing each readable one is part of
			// what the arm proves, and the labels left unread only mean
			// the residual is wider than the truth — never narrower.
			var numericLabels []float64
			for _, l := range caseLabels {
				if l == nil || l.Kind != abstractdomain.KindValues || l.KindTag != abstractdomain.PrimitiveNumber {
					continue
				}
				numericLabels = append(numericLabels, l.Values...)
			}
			if hasHeld && held.Kind == abstractdomain.KindValues &&
				(held.KindTag == abstractdomain.PrimitiveNumber || held.KindTag == abstractdomain.PrimitiveBoolean) &&
				len(numericLabels) > 0 {
				excludedSet := map[float64]struct{}{}
				for _, v := range numericLabels {
					excludedSet[v] = struct{}{}
				}
				var remaining []float64
				for _, v := range held.Values {
					if _, ex := excludedSet[v]; !ex {
						remaining = append(remaining, v)
					}
				}
				if len(remaining) > 0 && len(remaining) < len(held.Values) {
					clauseEnv.Set(switchStmt.Expression.Text(), abstractdomain.KnownValues(remaining, held.KindTag, abstractdomain.TrustLevelOf(held)))
				}
			} else if hasHeld && held.Kind == abstractdomain.KindSet && held.SetKindTag == abstractdomain.SetKindTagNone &&
				len(numericLabels) > 0 {
				diff := refinementsets.MakeRefinedSet(refinementsets.Refinement{
					Form: refinementsets.FormDifference,
					A_:   &held.Set,
					B:    ptrRefinedSet(refinementsets.MakeRefinedSet(refinementsets.OneOf(numericLabels))),
				})
				clauseEnv.Set(switchStmt.Expression.Text(), abstractdomain.KnownSet(diff, nil, abstractdomain.TrustLevelOf(held), abstractdomain.SetKindTagNone))
			}
		}
		// `switch (true)` runs a case body exactly when its test held:
		// the case expression narrows like an if condition (a body
		// shared through empty clauses runs under a disjunction, so
		// only a stand-alone case narrows)
		clauseCtx := ctx
		if ast.IsCaseClause(clause) && switchStmt.Expression.Kind == ast.KindTrueKeyword &&
			(index == 0 || !ast.IsCaseClause(clauses[index-1]) || len(clauses[index-1].AsCaseOrDefaultClause().Statements.Nodes) > 0) {
			// the case test narrows exactly like an if condition — the
			// assume operator, so the clause body also consumes the
			// test's rows, copies, and length guards
			assumed := assumeCondition(ctx, clauseEnv, clause.AsCaseOrDefaultClause().Expression, AssumeConditionScope{
				WhenTrueScope:  clause,
				WhenFalseScope: clause,
				At:             clause,
			}, false, false)
			assumed.WhenTrue.Env.Range(func(name string, held abstractdomain.AbstractValue) bool {
				clauseEnv.Set(name, held)
				return true
			})
			clauseCtx = assumed.WhenTrue.Ctx
		}
		var broke []Env
		inner := *clauseCtx
		inner.BreakSink = &broke
		exited := false
		brokeOut := false
		for _, s := range clauseStatements {
			if ast.IsBreakStatement(s) && s.AsBreakStatement().Label == nil {
				brokeOut = true
				break
			}
			if AnalyzeStatement(&inner, clauseEnv, s, result) {
				exited = true
				break
			}
		}
		// a break nested inside the clause — in a block, an `if` — left
		// the switch too, carrying the state it held there
		contributions = append(contributions, broke...)
		if brokeOut {
			contributions = append(contributions, clauseEnv)
		} else if !exited {
			// falling off the end composes into the NEXT clause's
			// statements — unless there is no next clause (every later
			// one, excluded or empty, shares nothing to fall into), in
			// which case falling off the end is a normal switch exit,
			// exactly like ending in a break
			if lastRunnableClause(clauses, index, excluded) {
				contributions = append(contributions, clauseEnv)
			} else {
				precise = false
			}
		}
	}
	if !precise {
		havocAssigned(ctx, env, statement)
		return false
	}
	// no default: the operand may match no clause and fall past —
	// unless the case labels exhaust its literal union
	mayFallPast := !hasDefault
	if mayFallPast {
		operandType := typereading.TypeAtLocation(ctx.P.Checker, switchStmt.Expression)
		var members []*checker.Type
		if operandType.IsUnion() {
			members = operandType.Types()
		} else {
			members = []*checker.Type{operandType}
		}
		labels := map[string]struct{}{}
		for _, clause := range clauses {
			if !ast.IsCaseClause(clause) {
				continue
			}
			t := typereading.TypeAtLocation(ctx.P.Checker, clause.AsCaseOrDefaultClause().Expression)
			if t.IsStringLiteral() {
				if s, ok := t.AsLiteralType().Value().(string); ok {
					labels["s:"+s] = struct{}{}
				}
			} else if t.IsNumberLiteral() {
				// tsgo spells a number literal's value as jsnum.Number, a
				// NAMED float64 — a bare .(float64) assertion never matches
				if n, ok := numberLiteralValue(t.AsLiteralType().Value()); ok {
					labels["n:"+jsnum.Number(n).String()] = struct{}{}
				}
			}
		}
		covered := len(members) > 0
		for _, member := range members {
			matched := false
			if member.IsStringLiteral() {
				if s, ok := member.AsLiteralType().Value().(string); ok {
					_, matched = labels["s:"+s]
				}
			} else if member.IsNumberLiteral() {
				if n, ok := numberLiteralValue(member.AsLiteralType().Value()); ok {
					_, matched = labels["n:"+jsnum.Number(n).String()]
				}
			}
			if !matched {
				covered = false
				break
			}
		}
		mayFallPast = !covered
	}
	if mayFallPast {
		contributions = append(contributions, env.Clone())
	}
	if len(contributions) == 0 {
		return true // every path exits
	}
	joined := contributions[0]
	for _, contribution := range contributions[1:] {
		joined = JoinEnvs(joined, contribution)
	}
	ReplaceEnv(env, joined)
	return false
}

// lastRunnableClause reports whether `index` is the last clause a
// fallthrough could possibly reach: every clause after it is either
// excluded by the correlation pass or carries no statements of its
// own (an empty clause shares whatever comes after it, so it is
// nothing to fall INTO). Falling off the end of such a clause is a
// normal switch exit — there is no further body for the composed
// write model to lose track of — so it joins in exactly like a bare
// break, instead of degrading the whole switch to havoc.
func lastRunnableClause(clauses []*ast.Node, index int, excluded int) bool {
	for i := index + 1; i < len(clauses); i++ {
		if i == excluded {
			continue
		}
		if len(caseOrDefaultStatements(clauses[i])) > 0 {
			return false
		}
	}
	return true
}

// caseOrDefaultStatements reads a CaseClause's or DefaultClause's
// statement list — the two node kinds share one Go struct,
// CaseOrDefaultClause, so a single downcast reads either.
func caseOrDefaultStatements(clause *ast.Node) []*ast.Node {
	return clause.AsCaseOrDefaultClause().Statements.Nodes
}

func ptrRefinedSet(r refinementsets.RefinedSet) *refinementsets.RefinedSet {
	return &r
}
