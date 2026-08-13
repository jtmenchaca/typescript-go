// from control_flow/reach_to_token.ts
//
// Walk to a token: the statement list that holds it, and the path
// down through branches, loops, and short-circuits that vouches for
// the state THERE. False where no state can be vouched for.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
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
	AnalyzeStatements(ctx, env, prefix, nil)
	ctxHere := ContextAfterStatements(ctx, prefix)
	holder := statements[index]
	if writes && holder == StatementOf(token) {
		AnalyzeStatements(ctxHere, env, []*ast.Node{holder}, nil)
		return true
	}
	return enterToToken(ctxHere, env, holder, token, writes)
}

func isLoop(node *ast.Node) bool {
	return ast.IsForStatement(node) || ast.IsForOfStatement(node) ||
		ast.IsForInStatement(node) || ast.IsWhileStatement(node) || ast.IsDoStatement(node)
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
			if child != loopBody {
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

		if ast.IsSwitchStatement(node) || ast.IsCaseBlock(node) ||
			ast.IsCaseClause(node) || ast.IsDefaultClause(node) || ast.IsCatchClause(node) {
			return false
		}

		node = child
	}
}
