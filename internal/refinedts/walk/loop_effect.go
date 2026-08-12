// from control_flow/loop_effect.ts
//
// Lowering a loop body for the kernel's loop solver: the checker
// READS the program — each tracked binding's body effect becomes a
// small expression over the bindings, known sets, the arithmetic
// operators and joins — and the KERNEL does the solving
// (set_functions/loop_solve.lean). The reading is honest at every
// point: an assignment the grammar cannot express is the unknown
// effect for that binding, and any construct that could write
// outside the grammar's sight — a call, a nested loop, a try, an
// early exit — bails the whole lowering, and the caller falls back
// to the walker-based path. A write the state does not record is
// never allowed to hide inside an expression: the expression walk
// bails on embedded assignments and increments.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var unknownEffect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}

// mathCallee is mathCallee in the TS source: a Math.* read the
// grammar speaks.
func mathCallee(e *ast.Node) (string, bool) {
	call := e.AsCallExpression()
	if ast.IsPropertyAccessExpression(call.Expression) {
		access := call.Expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Math" {
			return access.Name().Text(), true
		}
	}
	return "", false
}

// loopEffectSupported is supported in the TS source: nothing outside
// the grammar's sight may run in the body — any call (except the
// Math reads), nested loop, early exit, or function definition bails
// the lowering outright.
func loopEffectSupported(node *ast.Node) bool {
	ok := true
	var visit func(n *ast.Node)
	visit = func(n *ast.Node) {
		if !ok {
			return
		}
		if ast.IsCallExpression(n) {
			if _, isMath := mathCallee(n); !isMath {
				ok = false
				return
			}
		}
		if ast.IsNewExpression(n) || ast.IsAwaitExpression(n) ||
			ast.IsYieldExpression(n) || ast.IsTaggedTemplateExpression(n) ||
			ast.IsDeleteExpression(n) || ast.IsTryStatement(n) ||
			ast.IsSwitchStatement(n) || ast.IsBreakStatement(n) || ast.IsContinueStatement(n) ||
			ast.IsReturnStatement(n) || ast.IsThrowStatement(n) ||
			ast.IsLabeledStatement(n) || ast.IsFunctionLike(n) ||
			ast.IsClassLike(n) || ast.IsWhileStatement(n) ||
			ast.IsDoStatement(n) || ast.IsForStatement(n) ||
			ast.IsForOfStatement(n) || ast.IsForInStatement(n) {
			ok = false
			return
		}
		n.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(node)
	return ok
}

func sameLoopEffect(a, b kernelbridge.LoopEffect) bool {
	return kernelbridge.EffectWire(a) == kernelbridge.EffectWire(b)
}

// loopLoweringBail is the bail sentinel: the loop leaves the
// grammar. The TS source throws a unique Symbol and catches it by
// identity; Go has no exception value to smuggle through arbitrary
// call frames the same way, so this is a panic value recovered at
// LowerLoopEffect's one entry point (the TS source's own try/catch
// boundary) — any OTHER panic still propagates.
type loopLoweringBail struct{}

// LoweredLoop mirrors the TS LoweredLoop interface.
type LoweredLoop struct {
	Body []kernelbridge.LoopEffect
	Cond []*refinementsets.RefinedSet
}

// LowerLoopEffect is lowerLoopEffect in the TS source: read one loop
// body into the solver's effect grammar, or (nil, false) when the
// body leaves it. `vars` are the solved bindings, in answer order;
// `readEnv` supplies what everything else is known to be;
// `condition` is the loop test for the narrowing sets (nil for a
// do-while, whose first pass runs unguarded); the element binding of
// a for-of wears its element set fresh each pass.
func LowerLoopEffect(
	ctx *FlowContext,
	loop *ast.Node,
	incrementor *ast.Node,
	hasIncrementor bool,
	vars []string,
	readEnv Env,
	condition *ast.Node,
	elementName string,
	hasElementName bool,
	elementKnown abstractdomain.AbstractValue,
) (result *LoweredLoop, ok bool) {
	var statement *ast.Node
	switch {
	case ast.IsForStatement(loop):
		statement = loop.AsForStatement().Statement
	case ast.IsWhileStatement(loop):
		statement = loop.AsWhileStatement().Statement
	case ast.IsDoStatement(loop):
		statement = loop.AsDoStatement().Statement
	case ast.IsForOfStatement(loop):
		statement = loop.AsForInOrOfStatement().Statement
	case ast.IsForInStatement(loop):
		statement = loop.AsForInOrOfStatement().Statement
	}
	if !loopEffectSupported(statement) {
		return nil, false
	}
	if hasIncrementor && !loopEffectSupported(incrementor) {
		return nil, false
	}
	index := map[string]int{}
	for i, v := range vars {
		index[v] = i
	}
	state := map[string]kernelbridge.LoopEffect{}
	for i, v := range vars {
		state[v] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}
	}
	if hasElementName {
		if _, tracked := index[elementName]; !tracked {
			if set, ok := abstractdomain.SetOfKnown(elementKnown); ok {
				state[elementName] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: set}
			} else {
				state[elementName] = unknownEffect
			}
		}
	}

	readName := func(name string) kernelbridge.LoopEffect {
		if held, ok := state[name]; ok {
			return held
		}
		known, ok := readEnv[name]
		if !ok {
			return unknownEffect
		}
		set, ok := abstractdomain.SetOfKnown(known)
		if !ok {
			return unknownEffect
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: set}
	}

	unknownIfPure := func(e *ast.Node) kernelbridge.LoopEffect {
		if ContainsWrite(e) {
			panic(loopLoweringBail{})
		}
		return unknownEffect
	}

	// the expression grammar is the SHARED lowering's — this path
	// reads names through the sequential state (then the entry
	// environment), and answers unknown for write-free shapes the
	// grammar cannot speak
	var lowerExpr func(e *ast.Node) kernelbridge.LoopEffect
	lowerExpr = func(e *ast.Node) kernelbridge.LoopEffect {
		lowered, ok := LowerEffectExpression(e, EffectReader{
			ReadPlace: func(spelled string) (kernelbridge.LoopEffect, bool) {
				if held, ok := state[spelled]; ok {
					return held, true
				}
				known, ok := readEnv[spelled]
				if !ok {
					return kernelbridge.LoopEffect{}, false
				}
				set, ok := abstractdomain.SetOfKnown(known)
				if !ok {
					return kernelbridge.LoopEffect{}, false
				}
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: set}, true
			},
			Opaque: func(e *ast.Node) (kernelbridge.LoopEffect, bool) {
				return unknownIfPure(e), true
			},
		})
		if !ok {
			return unknownIfPure(e)
		}
		return lowered
	}

	writeTargetUnknown := func(target *ast.Node) {
		if ast.IsIdentifier(target) {
			state[target.Text()] = unknownEffect
			return
		}
		receiver := target
		for ast.IsPropertyAccessExpression(receiver) || ast.IsElementAccessExpression(receiver) {
			if ast.IsPropertyAccessExpression(receiver) {
				receiver = receiver.AsPropertyAccessExpression().Expression
			} else {
				receiver = receiver.AsElementAccessExpression().Expression
			}
		}
		if ast.IsIdentifier(receiver) {
			state[receiver.Text()] = unknownEffect
			return
		}
		// destructuring and stranger targets: everything named decays
		panic(loopLoweringBail{})
	}

	var lowerExpressionStatement func(e *ast.Node)
	lowerExpressionStatement = func(e *ast.Node) {
		if ast.IsParenthesizedExpression(e) {
			lowerExpressionStatement(e.AsParenthesizedExpression().Expression)
			return
		}
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if bin.OperatorToken.Kind == ast.KindEqualsToken {
					value := lowerExpr(bin.Right)
					if ast.IsIdentifier(bin.Left) {
						state[bin.Left.Text()] = value
						return
					}
					writeTargetUnknown(bin.Left)
					return
				}
				compound := map[ast.Kind]kernelbridge.LoopEffectOp{
					ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
					ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
					ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
					ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
					ast.KindPercentEqualsToken:  kernelbridge.LoopOpRem,
				}
				if op, ok := compound[bin.OperatorToken.Kind]; ok && ast.IsIdentifier(bin.Left) {
					a := readName(bin.Left.Text())
					b := lowerExpr(bin.Right)
					state[bin.Left.Text()] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}
					return
				}
				// an unmodeled compound write: the value is unknown, the
				// right side must still be write-free to lower at all
				if ContainsWrite(bin.Right) {
					panic(loopLoweringBail{})
				}
				writeTargetUnknown(bin.Left)
				return
			}
		}
		if ast.IsPrefixUnaryExpression(e) || ast.IsPostfixUnaryExpression(e) {
			var operator ast.Kind
			var operand *ast.Node
			if ast.IsPrefixUnaryExpression(e) {
				u := e.AsPrefixUnaryExpression()
				operator, operand = u.Operator, u.Operand
			} else {
				u := e.AsPostfixUnaryExpression()
				operator, operand = u.Operator, u.Operand
			}
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				op := kernelbridge.LoopOpAdd
				if operator == ast.KindMinusMinusToken {
					op = kernelbridge.LoopOpSub
				}
				if ast.IsIdentifier(operand) {
					a := readName(operand.Text())
					one := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}
					state[operand.Text()] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &one}
					return
				}
				writeTargetUnknown(operand)
				return
			}
		}
		// a pure expression statement moves nothing
		if ContainsWrite(e) {
			panic(loopLoweringBail{})
		}
	}

	var lowerStatement func(s *ast.Node)
	lowerStatement = func(s *ast.Node) {
		if ast.IsBlock(s) {
			for _, inner := range s.AsBlock().Statements.Nodes {
				lowerStatement(inner)
			}
			return
		}
		if ast.IsEmptyStatement(s) {
			return
		}
		if ast.IsExpressionStatement(s) {
			lowerExpressionStatement(s.AsExpressionStatement().Expression)
			return
		}
		if ast.IsVariableStatement(s) {
			for _, d := range s.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				decl := d.AsVariableDeclaration()
				if !ast.IsIdentifier(decl.Name()) {
					panic(loopLoweringBail{})
				}
				name := decl.Name().Text()
				// shadowing a solved binding would fork one name in two
				if _, tracked := index[name]; tracked {
					panic(loopLoweringBail{})
				}
				if decl.Initializer == nil {
					state[name] = unknownEffect
				} else {
					state[name] = lowerExpr(decl.Initializer)
				}
			}
			return
		}
		if ast.IsIfStatement(s) {
			ifStmt := s.AsIfStatement()
			if ContainsWrite(ifStmt.Expression) {
				panic(loopLoweringBail{})
			}
			before := map[string]kernelbridge.LoopEffect{}
			for k, v := range state {
				before[k] = v
			}
			lowerStatement(ifStmt.ThenStatement)
			thenState := map[string]kernelbridge.LoopEffect{}
			for k, v := range state {
				thenState[k] = v
			}
			for k := range state {
				delete(state, k)
			}
			for k, v := range before {
				state[k] = v
			}
			if ifStmt.ElseStatement != nil {
				lowerStatement(ifStmt.ElseStatement)
			}
			elseState := map[string]kernelbridge.LoopEffect{}
			for k, v := range state {
				elseState[k] = v
			}
			for k := range state {
				delete(state, k)
			}
			names := map[string]struct{}{}
			for k := range thenState {
				names[k] = struct{}{}
			}
			for k := range elseState {
				names[k] = struct{}{}
			}
			for name := range names {
				t, tOk := thenState[name]
				if !tOk {
					t = unknownEffect
				}
				e, eOk := elseState[name]
				if !eOk {
					e = unknownEffect
				}
				if sameLoopEffect(t, e) {
					state[name] = t
				} else {
					state[name] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &t, B: &e}
				}
			}
			return
		}
		panic(loopLoweringBail{})
	}

	bailed := func() (didBail bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, isBail := r.(loopLoweringBail); isBail {
					didBail = true
					return
				}
				panic(r)
			}
		}()
		lowerStatement(statement)
		if hasIncrementor {
			lowerExpressionStatement(incrementor)
		}
		return false
	}()
	if bailed {
		return nil, false
	}

	// the loop condition's narrowing sets — the body runs only when
	// the test held, so each binding is also inside its narrowing.
	// Refuting narrowings stay out (the NaN discipline: a refutation
	// proves nothing about an unknown), as do path- and definedness-
	// shaped ones.
	cond := make([]*refinementsets.RefinedSet, len(vars))
	if condition != nil {
		// a condition whose rows land WAS read — as a relation riding
		// the body entries and the exit channel — and this secondary
		// reading tells the coverage report so instead of counting it unread
		readAsRelation := len(dataflowfacts.DifferenceConstraintsOf(ctx.P.Checker, condition, condition, nil, nil, nil)) > 0
		readElsewhere := narrowing.GuardReadNowhere
		if readAsRelation {
			readElsewhere = narrowing.GuardReadRelation
		}
		narrowed := narrowing.Narrowings(ctx.P.Checker, condition, func(name string) bool {
			_, ok := index[name]
			return ok
		}, nil, readElsewhere)
		for _, n := range narrowed.WhenTrue {
			if len(n.Path) != 0 || n.Definedness != "" || n.Refuting {
				continue
			}
			i, ok := index[n.Binding]
			if !ok {
				continue
			}
			var set *refinementsets.RefinedSet
			if n.Exact != nil {
				sort := n.ExactSort
				if sort == "" {
					sort = abstractdomain.PrimitiveNumber
				}
				if len(n.Exact) == 1 && sort == abstractdomain.PrimitiveNumber {
					made := refinementsets.MakeRefinedSet(refinementsets.OneOf(n.Exact))
					set = &made
				}
			} else if len(n.Forms) > 0 {
				made := refinementsets.MakeRefinedSet(n.Forms...)
				set = &made
			}
			if set == nil {
				continue
			}
			if cond[i] == nil {
				cond[i] = set
			} else {
				merged := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, cond[i].Forms...), set.Forms...)...)
				cond[i] = &merged
			}
		}
	}

	body := make([]kernelbridge.LoopEffect, len(vars))
	for i, v := range vars {
		body[i] = readName(v)
	}
	return &LoweredLoop{Body: body, Cond: cond}, true
}
