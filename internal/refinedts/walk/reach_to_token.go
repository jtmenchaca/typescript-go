// from control_flow/reach_to_token.ts
//
// Walk to a token: the statement list that holds it, and the path
// down through branches, loops, and short-circuits that vouches for
// the state THERE. False where no state can be vouched for.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/scanner"
)

var shortCircuit = map[ast.Kind]struct{}{
	ast.KindAmpersandAmpersandToken: {},
	ast.KindBarBarToken:             {},
	ast.KindQuestionQuestionToken:   {},
}

// WalkSite is the TS WalkSite interface: the statement list a
// position lives in, and the function whose body it is. Contract is
// nil at the file's top level, and nil again inside a function the
// contract collector never saw.
type WalkSite struct {
	Statements []*ast.Node
	Statement  *ast.Node
	Index      int
	Parameters []*ast.Node
	Contract   *FunctionContract
	Fn         *ast.Node // WalkedFunction: FunctionDeclaration | ArrowFunction | FunctionExpression | MethodDeclaration
}

func nodeStart(node *ast.Node) int {
	return scanner.GetTokenPosOfNode(node, ast.GetSourceFileOfNode(node), false)
}

// EnclosingBlockBody is enclosingBlockBody in the TS source: the
// innermost function whose BODY IS A BLOCK containing `token`. An
// expression-bodied arrow has no statement list to walk to, so it is
// not a site.
func EnclosingBlockBody(token *ast.Node) *ast.Node {
	for node := token.Parent; node != nil; node = node.Parent {
		if (ast.IsFunctionDeclaration(node) || ast.IsArrowFunction(node) ||
			ast.IsFunctionExpression(node) || ast.IsMethodDeclaration(node)) &&
			node.Body() != nil && ast.IsBlock(node.Body()) {
			return node
		}
		if ast.IsSourceFile(node) {
			return nil
		}
	}
	return nil
}

func contractFor(contracts map[*ast.Symbol]*FunctionContract, declaration *ast.Node) *FunctionContract {
	for _, contract := range contracts {
		if contract.Declaration == declaration {
			return contract
		}
	}
	return nil
}

// SiteOf is siteOf in the TS source: where the walk starts for this
// position, and which statement it stops at.
func SiteOf(p *program.CheckerProgram, contracts map[*ast.Symbol]*FunctionContract, token *ast.Node) (WalkSite, bool) {
	fn := EnclosingBlockBody(token)
	var statements []*ast.Node
	if fn == nil {
		statements = p.Entry.Statements.Nodes
	} else {
		statements = fn.Body().AsBlock().Statements.Nodes
	}
	statement := token
	found := func() bool {
		for _, s := range statements {
			if s == statement {
				return true
			}
		}
		return false
	}
	for statement.Parent != nil && !found() {
		statement = statement.Parent
		if ast.IsSourceFile(statement) {
			return WalkSite{}, false
		}
	}
	index := -1
	for i, s := range statements {
		if s == statement {
			index = i
			break
		}
	}
	if index == -1 || !ast.IsStatement(statement) {
		return WalkSite{}, false
	}
	var parameters []*ast.Node
	var contract *FunctionContract
	if fn != nil {
		parameters = fn.Parameters()
		contract = contractFor(contracts, fn)
	}
	return WalkSite{
		Statements: statements,
		Statement:  statement,
		Index:      index,
		Parameters: parameters,
		Contract:   contract,
		Fn:         fn,
	}, true
}

// StatementOf is statementOf in the TS source: the nearest statement
// the token sits in — the one that runs it.
func StatementOf(token *ast.Node) *ast.Node {
	for node := token.Parent; node != nil; node = node.Parent {
		if ast.IsStatement(node) {
			return node
		}
		if ast.IsSourceFile(node) {
			return nil
		}
	}
	return nil
}

// childHolding is childHolding in the TS source: the direct child of
// `node` that contains `token`.
func childHolding(node *ast.Node, token *ast.Node) *ast.Node {
	var found *ast.Node
	tokenStart := nodeStart(token)
	tokenEnd := token.End()
	node.ForEachChild(func(child *ast.Node) bool {
		if found == nil && nodeStart(child) <= tokenStart && tokenEnd <= child.End() {
			found = child
		}
		return false
	})
	return found
}

// AnalyzeToToken is analyzeToToken in the TS source: run the
// statements before the one holding the token, then enter that one.
// A WRITE position runs its OWN statement too.
func AnalyzeToToken(ctx *FlowContext, env Env, statements []*ast.Node, token *ast.Node, writes bool) bool {
	tokenStart := nodeStart(token)
	tokenEnd := token.End()
	index := -1
	for i, s := range statements {
		if nodeStart(s) <= tokenStart && tokenEnd <= s.End() {
			index = i
			break
		}
	}
	if index == -1 {
		return false
	}
	var prefix []*ast.Node
	for _, s := range statements[:index] {
		if !ast.IsFunctionDeclaration(s) {
			prefix = append(prefix, s)
		}
	}
	holder := statements[index]
	if writes && holder == StatementOf(token) {
		// a WRITE position runs its own statement too — walked in the
		// SAME AnalyzeStatements call as the prefix, not a second one.
		// A cross-language edge recognized in the prefix (foreign_edge.go's
		// pendingForeignOverrides) pins its published fact on a LATER
		// statement in the same list; splitting the walk in two would
		// start a fresh, empty override map for holder's own walk, and
		// the fact the prefix just recognized could never reach it — the
		// exact gap a declaration-name hover (`const level = JSON.parse(stdout)`)
		// sat behind while `return level` two statements later, walked
		// under the one shared list, read the fact correctly.
		//
		// A name this SAME list still writes AFTER holder (an
		// accumulator a later loop rewrites, e.g. `let total = 0` ahead
		// of `for (...) { total += ... }`) makes holder's own value the
		// wrong answer: the declaration position must serve what a read
		// placed right after the name's LAST write would serve, not the
		// initializer alone — the same claim `total`'s post-loop comment
		// states. So the walk runs the REST of the list too whenever a
		// later statement still writes this name, and the caller reads
		// the name's FINAL value back off the resulting env.
		rest := statements[index+1:]
		if writesNameLater(rest, token.Text()) {
			AnalyzeStatements(ctx, env, append(append([]*ast.Node{}, prefix...), statements[index:]...), nil)
			return true
		}
		AnalyzeStatements(ctx, env, append(append([]*ast.Node{}, prefix...), holder), nil)
		return true
	}
	// a READ inside `holder` still needs the SAME loop-and-division
	// pairing a write position gets: RelationalAccumulationOf
	// (relational_accumulation.go) only fires when the loop and its
	// division sit together in the SAME statements slice a list walk
	// sees (listWalk's own call, analyze_statement.go), because the
	// relation the kernel carries between them dies at the boundary
	// between two separate AnalyzeStatements calls. `prefix` above ends
	// at the loop and excludes holder, so a plain
	// `AnalyzeStatements(ctx, env, prefix, nil)` walks the loop without
	// its pairing partner ever joining the same slice — the pairing's
	// own index+1 bound then never fires, and `total` inside
	// `holder` (a read of the accumulator sitting in the division
	// statement itself) is served the loop's OWN, unrelated,
	// interval-arithmetic answer instead of the kernel's tighter one.
	//
	// The fix asks the SAME recognizer listWalk asks, over the SAME
	// prefix-relative slice, to learn whether holder is the pairing
	// partner of the statement right before it — never a special-cased
	// recomputation of what pairs. A yes routes holder into the list
	// walk (one AnalyzeStatements call, exactly the write branch's own
	// shape above) so the pairing fires and the token's read comes back
	// off the resulting env; a no leaves the existing node-level descent
	// (enterToToken) untouched for every other read.
	if index > 0 && pairsWithPrecedingLoop(ctx, env, prefix, holder) {
		AnalyzeStatements(ctx, env, append(append([]*ast.Node{}, prefix...), holder), nil)
		return true
	}
	AnalyzeStatements(ctx, env, prefix, nil)
	ctxHere := ContextAfterStatements(ctx, prefix)
	return enterToToken(ctxHere, env, holder, token, writes)
}

// pairsWithPrecedingLoop is whether `prefix`'s LAST statement and
// `holder` are the two halves of a recognized accumulate-then-divide
// pair — the exact test listWalk's own RelationalAccumulationOf call
// runs at the loop's index, over the SAME entry state: a CLONE of
// `env` walked through everything in `prefix` before the loop, since
// RelationalAccumulationOf reads the accumulator's value as it stands
// right before the loop runs (its EXACT-start gate), not the site's
// raw entry state. The probe changes nothing the caller has not
// already decided to walk for real via the routed AnalyzeStatements
// call above — it runs on the clone alone.
func pairsWithPrecedingLoop(ctx *FlowContext, env Env, prefix []*ast.Node, holder *ast.Node) bool {
	if len(prefix) == 0 {
		return false
	}
	probeEnv := env.Clone()
	loopIndex := len(prefix) - 1
	AnalyzeStatements(ctx, probeEnv, prefix[:loopIndex], nil)
	pair := append(append([]*ast.Node{}, prefix...), holder)
	_, ok := RelationalAccumulationOf(ctx, probeEnv, pair, loopIndex)
	return ok
}

// writesNameLater is whether any statement in the list writes `name`
// directly — a plain text scan, the same one enterCatchClause uses to
// decide what a try's own text could still have been mid-write on.
// Over-inclusion (a name a callee writes through a reference this
// scan cannot see) only costs an extra walk of statements already
// being walked for other reasons; it never serves a wrong answer.
func writesNameLater(statements []*ast.Node, name string) bool {
	written := map[string]struct{}{}
	for _, statement := range statements {
		AssignedNamesDirect(statement, written)
		if _, ok := written[name]; ok {
			return true
		}
	}
	return false
}

func isLoop(node *ast.Node) bool {
	return ast.IsForStatement(node) || ast.IsForOfStatement(node) ||
		ast.IsForInStatement(node) || ast.IsWhileStatement(node) || ast.IsDoStatement(node)
}

// enterCaseClause puts `env` into the state a case body runs under:
// the discriminant pinned to the labels that reach this body — its
// own, plus the contiguous EMPTY clauses above it, whose labels share
// this body. A default arm and a discriminant that is not a plain
// name are left as they stand; the sibling analysis's shedding of the
// case labels from a default arm reads the whole clause list, which
// this single-path descent does not walk.
func enterCaseClause(ctx *FlowContext, env Env, clause *ast.Node) {
	if !ast.IsCaseClause(clause) {
		return
	}
	block := clause.Parent
	if block == nil || !ast.IsCaseBlock(block) || block.Parent == nil {
		return
	}
	discriminant := block.Parent.AsSwitchStatement().Expression
	if !ast.IsIdentifier(discriminant) {
		return
	}
	if _, has := env.Get(discriminant.Text()); !has {
		return
	}
	clauses := block.AsCaseBlock().Clauses.Nodes
	index := -1
	for i, c := range clauses {
		if c == clause {
			index = i
			break
		}
	}
	if index == -1 {
		return
	}
	labels := []*ast.Node{clause.AsCaseOrDefaultClause().Expression}
	for back := index - 1; back >= 0; back-- {
		previous := clauses[back]
		if !ast.IsCaseClause(previous) || len(previous.AsCaseOrDefaultClause().Statements.Nodes) > 0 {
			break
		}
		labels = append(labels, previous.AsCaseOrDefaultClause().Expression)
	}
	if pinned, ok := SwitchLabelValuesWith(ctx.P.Checker, labels); ok {
		env.Set(discriminant.Text(), pinned)
	}
}

// enterCatchClause puts `env` into the state a catch body runs under:
// the try's own text may have been part-way through its writes when
// the exception came, so every name it writes from the first
// statement that can raise onward is forgotten, and the caught
// binding takes its seeded value.
func enterCatchClause(ctx *FlowContext, env Env, catchClause *ast.Node) {
	tryStatement := catchClause.Parent
	if tryStatement == nil || !ast.IsTryStatement(tryStatement) {
		return
	}
	tryBlock := tryStatement.AsTryStatement().TryBlock
	written := map[string]struct{}{}
	for _, s := range statementsFromFirstThrowing(tryBlock.AsBlock().Statements.Nodes) {
		AssignedNamesDirect(s, written)
	}
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
	binding := catchClause.AsCatchClause().VariableDeclaration
	if binding != nil {
		bindingName := binding.AsVariableDeclaration().Name()
		if bindingName != nil && ast.IsIdentifier(bindingName) {
			env.Set(bindingName.Text(), silence.SeededBinding(ctx.P.Checker, silence.Residue(), bindingName))
		}
	}
}

func enterToToken(ctx *FlowContext, env Env, from *ast.Node, token *ast.Node, writes bool) bool {
	node := from
	for {
		if node == token {
			return true
		}
		child := childHolding(node, token)
		if child == nil {
			return nodeStart(node) <= nodeStart(token)
		}

		if ast.IsBlock(node) {
			return AnalyzeToToken(ctx, env, node.AsBlock().Statements.Nodes, token, writes)
		}

		if ast.IsIfStatement(node) {
			ifStmt := node.AsIfStatement()
			if child != ifStmt.Expression {
				transfers := ConditionEnvTransfersOf(ctx, env, ifStmt.Expression, ConditionEnvTransfersSite{At: node})
				if child == ifStmt.ThenStatement {
					transfers.ApplyWhenTrue(env)
				} else {
					transfers.ApplyWhenFalse(env)
				}
			}
			node = child
			continue
		}

		if ast.IsConditionalExpression(node) {
			cond := node.AsConditionalExpression()
			if child != cond.Condition {
				transfers := ConditionEnvTransfersOf(ctx, env, cond.Condition, ConditionEnvTransfersSite{At: node})
				if child == cond.WhenTrue {
					transfers.ApplyWhenTrue(env)
				} else {
					transfers.ApplyWhenFalse(env)
				}
			}
			node = child
			continue
		}

		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if child == bin.Right {
				if _, isShortCircuit := shortCircuit[bin.OperatorToken.Kind]; isShortCircuit {
					transfers := ConditionEnvTransfersOf(ctx, env, bin.Left, ConditionEnvTransfersSite{At: node})
					if bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken {
						transfers.ApplyWhenTrue(env)
					} else {
						transfers.ApplyWhenFalse(env)
					}
					node = child
					continue
				}
			}
		}

		if isLoop(node) {
			var loopBody *ast.Node
			switch {
			case ast.IsForStatement(node):
				loopBody = node.AsForStatement().Statement
			case ast.IsWhileStatement(node):
				loopBody = node.AsWhileStatement().Statement
			case ast.IsDoStatement(node):
				loopBody = node.AsDoStatement().Statement
			case ast.IsForOfStatement(node), ast.IsForInStatement(node):
				loopBody = node.AsForInOrOfStatement().Statement
			}
			// a for-initializer and a for-of/for-in ITERABLE each run
			// ONCE, before anything else the loop does — so the state
			// they run under is the one already in hand here, and
			// nothing needs solving to reach it
			runsBeforeTheLoop := false
			if ast.IsForStatement(node) {
				runsBeforeTheLoop = child == node.AsForStatement().Initializer
			} else if ast.IsForOfStatement(node) || ast.IsForInStatement(node) {
				runsBeforeTheLoop = child == node.AsForInOrOfStatement().Expression
			}
			if runsBeforeTheLoop {
				node = child
				continue
			}
			// the CONDITION and the INCREMENTOR run on every trip, under
			// the loop's settled facts — the same state the body enters
			// with, which is what SolveLoop fills bodyEntry with. (The
			// condition's own narrowing is inside that state; it is what
			// the previous trip's test left, which is what a token in the
			// condition sits under from the second trip on.)
			inHead := false
			if ast.IsForStatement(node) {
				forStmt := node.AsForStatement()
				inHead = child == forStmt.Condition || child == forStmt.Incrementor
			} else if ast.IsWhileStatement(node) {
				inHead = child == node.AsWhileStatement().Expression
			} else if ast.IsDoStatement(node) {
				inHead = child == node.AsDoStatement().Expression
			}
			if child != loopBody && !inHead {
				return false
			}
			bodyEntry := NewEnv()
			SolveLoop(ctx, env, node, nil, LoopAnalyzers{
				AnalyzeStatement:   AnalyzeStatement,
				EvaluateExpression: evaluateExpression,
				IterationElement:   IterationElementOf,
			}, bodyEntry)
			ReplaceEnv(env, bodyEntry)
			node = child
			continue
		}

		// a switch's scrutinee runs before any clause does, under the
		// state in hand; the CaseBlock is just the clause container
		if ast.IsSwitchStatement(node) {
			if child == node.AsSwitchStatement().Expression {
				node = child
				continue
			}
			evaluateExpression(ctx, env, node.AsSwitchStatement().Expression)
			node = child
			continue
		}
		if ast.IsCaseBlock(node) {
			node = child
			continue
		}

		// a case body runs with the discriminant WEARING its label: the
		// runtime took `scrutinee === label` to get here. A case LABEL
		// itself is tested before any body runs, so it wears nothing.
		if ast.IsCaseClause(node) || ast.IsDefaultClause(node) {
			clause := node.AsCaseOrDefaultClause()
			if ast.IsCaseClause(node) && child == clause.Expression {
				node = child
				continue
			}
			enterCaseClause(ctx, env, node)
			return AnalyzeToToken(ctx, env, clause.Statements.Nodes, token, writes)
		}

		// a catch body runs on the state the try left, with everything
		// the try's own text could still have been writing forgotten
		// and the caught binding seeded
		if ast.IsCatchClause(node) {
			catchClause := node.AsCatchClause()
			if child == catchClause.VariableDeclaration {
				return false // the binding declares the name; it holds no walked state
			}
			enterCatchClause(ctx, env, node)
			return AnalyzeToToken(ctx, env, catchClause.Block.AsBlock().Statements.Nodes, token, writes)
		}

		node = child
	}
}
