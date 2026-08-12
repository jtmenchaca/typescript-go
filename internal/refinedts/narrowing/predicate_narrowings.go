// Predicate and assertion calls as guards: a call used as a guard
// narrows by its callee's inlined body, and a bare assertion call
// narrows by the guard its throwing helper proves. Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// PredicateCallNarrowings is predicateCallNarrowings in the TS source:
// a call used as a guard narrows by its callee's inlined body: the
// body's own narrowings, read with the parameters as the tracked
// places, carried out to the arguments. The function RETURNS its body
// expression, so the call's truthiness IS the body's — both branches
// carry over exactly. (BranchNarrowings{}, false) where the callee's
// body is not pinned or nothing carries out.
func PredicateCallNarrowings(c *checker.Checker, call *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	if PredicateReadDepth(c) >= 3 {
		return BranchNarrowings{}, false
	}
	callExpr := call.AsCallExpression()
	fn := PinnedFunctionOf(c, callExpr.Expression)
	if fn == nil {
		return BranchNarrowings{}, false
	}
	var parameters []string
	for _, parameter := range fn.Parameters() {
		if !ast.IsIdentifier(parameter.Name()) {
			return BranchNarrowings{}, false
		}
		parameters = append(parameters, parameter.Name().Text())
	}
	if len(parameters) == 0 {
		return BranchNarrowings{}, false
	}
	var argumentPlaces []*dataflowfacts.TrackedPlace
	anyPlace := false
	if callExpr.Arguments != nil {
		for _, a := range callExpr.Arguments.Nodes {
			place := dataflowfacts.TrackedPlaceOf(a, isTracked)
			argumentPlaces = append(argumentPlaces, place)
			if place != nil {
				anyPlace = true
			}
		}
	}
	if !anyPlace {
		return BranchNarrowings{}, false
	}
	OpenPredicateRead(c)
	defer ClosePredicateRead(c)
	inner, ok := PredicateBodyBranches(c, fn, func(name string) bool {
		for _, p := range parameters {
			if p == name {
				return true
			}
		}
		return false
	})
	if !ok {
		return BranchNarrowings{}, false
	}
	whenTrue := RemapPlaces(inner.WhenTrue, parameters, argumentPlaces)
	whenFalse := RemapPlaces(inner.WhenFalse, parameters, argumentPlaces)
	if len(whenTrue) == 0 && len(whenFalse) == 0 {
		return BranchNarrowings{}, false
	}
	return BranchNarrowings{WhenTrue: whenTrue, WhenFalse: whenFalse}, true
}

// PredicateBodyBranches is predicateBodyBranches in the TS source: what
// a predicate BODY proves per branch. A single expression (or a lone
// `return`) reads whole. A block in the early-return-false shape (any
// number of `if (c) return false;` peels, local consts passed over —
// claims on locals drop at the remap — one final `return expr`) proves,
// when the CALL answers true, that every peeled condition was FALSE and
// the final expression TRUE; its falsity proves nothing (which exit
// refused is unknown). The parameters must never be reassigned, so each
// condition read the same values the call was handed.
// (BranchNarrowings{}, false) stands in for the TS source's null.
func PredicateBodyBranches(c *checker.Checker, fn *ast.Node, isTrackedInner func(name string) bool) (BranchNarrowings, bool) {
	body := fn.Body()
	if body == nil {
		return BranchNarrowings{}, false
	}
	if !ast.IsBlock(body) {
		return Narrowings(c, body, isTrackedInner, nil, GuardReadNowhere), true
	}
	statements := body.AsBlock().Statements
	if statements != nil && len(statements.Nodes) == 1 {
		only := statements.Nodes[0]
		if ast.IsReturnStatement(only) && only.AsReturnStatement().Expression != nil {
			return Narrowings(c, only.AsReturnStatement().Expression, isTrackedInner, nil, GuardReadNowhere), true
		}
		return BranchNarrowings{}, false
	}
	// a reassigned parameter would break the reading — each peeled
	// condition must have seen the call's own values. The assigned
	// names come from the seam, computed once per body.
	assigned := dataflowfacts.AssignedIdentifierNames(body)
	reassigned := false
	for name := range assigned {
		if isTrackedInner(name) {
			reassigned = true
			break
		}
	}
	if reassigned {
		return BranchNarrowings{}, false
	}
	var returnsFalse func(s *ast.Node) bool
	returnsFalse = func(s *ast.Node) bool {
		if ast.IsReturnStatement(s) {
			expr := s.AsReturnStatement().Expression
			return expr != nil && expr.Kind == ast.KindFalseKeyword
		}
		if ast.IsBlock(s) {
			stmts := s.AsBlock().Statements
			if stmts != nil && len(stmts.Nodes) == 1 {
				return returnsFalse(stmts.Nodes[0])
			}
		}
		return false
	}
	var whenTrue []Narrowed
	nodes := statements.Nodes
	for i := 0; i < len(nodes); i++ {
		statement := nodes[i]
		if ast.IsVariableStatement(statement) {
			continue
		}
		if ast.IsIfStatement(statement) {
			ifStmt := statement.AsIfStatement()
			if ifStmt.ElseStatement == nil && returnsFalse(ifStmt.ThenStatement) {
				b := Narrowings(c, ifStmt.Expression, isTrackedInner, nil, GuardReadNowhere)
				whenTrue = append(whenTrue, b.WhenFalse...)
				continue
			}
		}
		if ast.IsReturnStatement(statement) && statement.AsReturnStatement().Expression != nil && i == len(nodes)-1 {
			b := Narrowings(c, statement.AsReturnStatement().Expression, isTrackedInner, nil, GuardReadNowhere)
			whenTrue = append(whenTrue, b.WhenTrue...)
			return BranchNarrowings{WhenTrue: whenTrue}, true
		}
		return BranchNarrowings{}, false
	}
	return BranchNarrowings{}, false
}

// AssertionCallNarrowings is assertionCallNarrowings in the TS source:
// a bare assertion call narrows by its callee's guard: a function whose
// signature ASSERTS (`asserts x is T` or `asserts cond`) and whose body
// is exactly one `if (cond) throw …` returns only where cond was FALSE
// — so the guard's whenFalse claims carry out to the arguments, the way
// a predicate call's body carries its truthiness. The SIGNATURE gates
// (a plain throwing helper states no contract); the BODY supplies the
// claims, so nothing rests on the annotation alone. (nil, false) where
// the shape declines.
func AssertionCallNarrowings(c *checker.Checker, call *ast.Node, isTracked func(name string) bool) ([]Narrowed, bool) {
	if PredicateReadDepth(c) >= 3 {
		return nil, false
	}
	callExpr := call.AsCallExpression()
	fn := PinnedFunctionOf(c, callExpr.Expression)
	if fn == nil {
		return nil, false
	}
	var returnType *ast.Node
	switch {
	case ast.IsArrowFunction(fn):
		returnType = fn.AsArrowFunction().Type
	case ast.IsFunctionExpression(fn):
		returnType = fn.AsFunctionExpression().Type
	case ast.IsFunctionDeclaration(fn):
		returnType = fn.AsFunctionDeclaration().Type
	}
	stated := returnType != nil && ast.IsTypePredicateNode(returnType) &&
		returnType.AsTypePredicateNode().AssertsModifier != nil
	// a SAME-FILE helper whose whole body is one `if (cond) throw`
	// states its contract by behavior — the asserts annotation is not
	// required there (JT's ruling); a helper from another file still
	// needs the signature to say so
	sameFile := ast.GetSourceFileOfNode(fn) == ast.GetSourceFileOfNode(call)
	if !stated && !sameFile {
		return nil, false
	}
	body := fn.Body()
	if body == nil || !ast.IsBlock(body) {
		return nil, false
	}
	statements := body.AsBlock().Statements
	if statements == nil || len(statements.Nodes) != 1 {
		return nil, false
	}
	only := statements.Nodes[0]
	if !ast.IsIfStatement(only) || only.AsIfStatement().ElseStatement != nil {
		return nil, false
	}
	thenStatement := only.AsIfStatement().ThenStatement
	throws := ast.IsThrowStatement(thenStatement)
	if !throws && ast.IsBlock(thenStatement) {
		stmts := thenStatement.AsBlock().Statements
		throws = stmts != nil && len(stmts.Nodes) == 1 && ast.IsThrowStatement(stmts.Nodes[0])
	}
	if !throws {
		return nil, false
	}
	var parameters []string
	for _, parameter := range fn.Parameters() {
		if !ast.IsIdentifier(parameter.Name()) {
			return nil, false
		}
		parameters = append(parameters, parameter.Name().Text())
	}
	if len(parameters) == 0 {
		return nil, false
	}
	var argumentPlaces []*dataflowfacts.TrackedPlace
	anyPlace := false
	if callExpr.Arguments != nil {
		for _, a := range callExpr.Arguments.Nodes {
			place := dataflowfacts.TrackedPlaceOf(a, isTracked)
			argumentPlaces = append(argumentPlaces, place)
			if place != nil {
				anyPlace = true
			}
		}
	}
	if !anyPlace {
		return nil, false
	}
	OpenPredicateRead(c)
	defer ClosePredicateRead(c)
	inner := NarrowingsOf(c, only.AsIfStatement().Expression, func(name string) bool {
		for _, p := range parameters {
			if p == name {
				return true
			}
		}
		return false
	}, nil, GuardReadNowhere)
	survived := RemapPlaces(inner.WhenFalse, parameters, argumentPlaces)
	if len(survived) == 0 {
		return nil, false
	}
	return survived, true
}
