// Predicate and assertion calls as guards: a call used as a guard
// narrows by its callee's inlined body, and a bare assertion call
// narrows by the guard its throwing helper proves. Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// PredicateCallNarrowings is predicateCallNarrowings in the TS source:
// a call used as a guard narrows by its callee's inlined body: the
// body's own narrowings, read with the parameters as the tracked
// places, carried out to the arguments. The function RETURNS its body
// expression, so the call's truthiness IS the body's — both branches
// carry over exactly. (BranchNarrowings{}, false) where the callee's
// body is not pinned or nothing carries out.
//
// Where the body reading recovers nothing, the callee's DECLARED type
// predicate speaks instead — see StatedPredicateNarrowings.
func PredicateCallNarrowings(c *checker.Checker, call *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	if PredicateReadDepth(c) >= 3 {
		return BranchNarrowings{}, false
	}
	callExpr := call.AsCallExpression()
	fn := PinnedFunctionOf(c, callExpr.Expression)
	if fn == nil {
		return StatedPredicateNarrowings(c, call, isTracked)
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
			place := dataflowfacts.TrackedPlaceOfWith(c, a, isTracked)
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
	inner, ok := PredicateBodyBranches(c, fn, func(name string) bool {
		for _, p := range parameters {
			if p == name {
				return true
			}
		}
		return false
	})
	ClosePredicateRead(c)
	if !ok {
		return StatedPredicateNarrowings(c, call, isTracked)
	}
	whenTrue := RemapPlaces(inner.WhenTrue, parameters, argumentPlaces)
	whenFalse := RemapPlaces(inner.WhenFalse, parameters, argumentPlaces)
	if len(whenTrue) == 0 && len(whenFalse) == 0 {
		// the body recovered nothing that carries out — a predicate whose
		// parameter is `any`/`unknown` has nothing to shed, so every leaf
		// the body reads lands on a place with no held sort. The declared
		// type predicate is the fact that pins it.
		return StatedPredicateNarrowings(c, call, isTracked)
	}
	// the body spoke on at least one side; the declared predicate fills
	// the side it left empty. `isNumber = (v: unknown): v is number =>
	// (typeof v === 'number' || v instanceof Number) && !isNan(v)` reads
	// nothing on the TRUE side (a `||` composes only its false side), so
	// without this the held branch of `isNumber(interval)` learned
	// nothing the guard proves.
	if len(whenTrue) == 0 || len(whenFalse) == 0 {
		if stated, statedOk := StatedPredicateNarrowings(c, call, isTracked); statedOk {
			if len(whenTrue) == 0 {
				whenTrue = stated.WhenTrue
			}
			if len(whenFalse) == 0 {
				whenFalse = stated.WhenFalse
			}
		}
	}
	return BranchNarrowings{WhenTrue: whenTrue, WhenFalse: whenFalse}, true
}

// StatedPredicateNarrowings reads the callee's DECLARED type predicate
// — `(val: any): val is string` — as a guard in its own right: the
// held side narrows the tested place to T's reading, the refuted side
// excludes T's sort. It is a first-class channel beside the body
// reading, and the one that answers where the body cannot: a predicate
// over an `any`/`unknown` parameter has nothing to shed, so every leaf
// its body reads lands on a place holding no sort and carries nothing
// out.
//
// HONESTY GATE: the declared claim is believed only to the extent the
// predicate's OWN BODY proves it. The body is read the same way a call
// guard reads it (PredicateBodyBranches, the condition-tree walk every
// ordinary guard goes through), folded onto the unknown parameter to
// the PROVEN abstract value; the claimed T's set and the proven set
// both reduce to the kernel's 1-tuple layer (abstractdomain.SetOfKnown)
// and the kernel's ScalarSubset asks proven ⊆ claimed. `true` believes
// the claim (the ordinary leaf below runs); `false` — a positive
// theorem that the body proves LESS than it claims — does not believe
// it, and this function declines (ok=false), so the call carries
// nothing beyond what the tested place already held. The SAME decline
// covers every case with no proof to check: the body is UNREADABLE
// (PredicateBodyBranches ok=false — a multi-statement body outside its
// early-return-false shape, a reassigned parameter, the predicate-read
// depth bound), the proof or the claim does not reduce to a 1-tuple-
// layer set (an object shape, a sequence), or the kernel declines the
// ScalarSubset question outright (no kernel seated, or the ask panics).
// A decline proves nothing either way, and believing on no evidence is
// exactly the unsoundness this gate exists to close — never believe a
// claim the walk could not check.
//
// TRUST: past the gate, the annotation is taken at the same grade the
// checker gives any declared type. `val is T` is tsc-checked SYNTAX
// whose body tsc does NOT verify — a predicate may lie about its own
// body and tsc will not say so — which is exactly the standing of
// every parameter and return annotation this checker already reads and
// trusts. Trusting a PROVEN predicate is therefore not a new
// concession; the gate above is what makes "proven" the operative word
// rather than "declared."
//
// The result is the ordinary leaf shape — a Shape on the true side, an
// ExcludesKind on the false side, both landing on a TrackedPlace — so
// &&/||/! and ternary conditions fold it through the same condition
// tree every other narrowing goes through, with no new plumbing.
func StatedPredicateNarrowings(c *checker.Checker, call *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	callExpr := call.AsCallExpression()
	predicate := DeclaredTypePredicateOf(c, callExpr.Expression)
	if predicate == nil {
		return BranchNarrowings{}, false
	}
	predicateNode := predicate.AsTypePredicateNode()
	// an ASSERTING signature says nothing about the call's truthiness —
	// it returns void and narrows by returning at all, which is
	// AssertionCallNarrowings's reading, not this one
	if predicateNode.AssertsModifier != nil || predicateNode.Type == nil {
		return BranchNarrowings{}, false
	}
	if predicateNode.ParameterName == nil || !ast.IsIdentifier(predicateNode.ParameterName) {
		return BranchNarrowings{}, false
	}
	// which ARGUMENT the predicate names: `val is T` on the signature's
	// first parameter narrows the first argument, and so on down. A
	// predicate naming `this` narrows no argument.
	fn := PinnedFunctionOf(c, callExpr.Expression)
	position := declaredPredicatePosition(c, callExpr.Expression, fn, predicateNode.ParameterName.Text())
	if position < 0 || callExpr.Arguments == nil || position >= len(callExpr.Arguments.Nodes) {
		return BranchNarrowings{}, false
	}
	place := dataflowfacts.TrackedPlaceOfWith(c, callExpr.Arguments.Nodes[position], isTracked)
	if place == nil {
		return BranchNarrowings{}, false
	}
	held, ok := typereading.ReadTypeNode(c, predicateNode.Type, predicateNode.Type, 0)
	if !ok || held.Kind == abstractdomain.KindUnknown {
		return BranchNarrowings{}, false
	}
	if !predicateClaimProven(c, fn, predicateNode.ParameterName.Text(), held) {
		return BranchNarrowings{}, false
	}
	wears := Narrowed{Binding: place.Binding, Path: place.Path, Shape: held, HasShape: true}
	whenFalse := []Narrowed{}
	// the REFUTED side sheds the sort only where T pins ONE word — the
	// same rule typeof's refuted side follows. A T spanning two sorts
	// (`string | number`) excludes neither on its own.
	if word := abstractdomain.TypeofWordOfKnown(held); word != "" {
		whenFalse = append(whenFalse, Narrowed{Binding: place.Binding, Path: place.Path, ExcludesKind: word})
	}
	return BranchNarrowings{WhenTrue: []Narrowed{wears}, WhenFalse: whenFalse}, true
}

// predicateClaimProven is the honesty gate: does the predicate's OWN
// BODY prove the claimed type `held`, on the parameter the predicate
// names? fn is the pinned function the predicate's body lives in
// (nil where the callee is not pinned — no body to check, so the
// claim is never proven). Neither an unreadable body, an unreducible
// shape, nor a kernel decline proves anything — every one of those
// answers false, the same "not believed" verdict a positive disproof
// gets. Only a kernel THEOREM (ScalarSubset answering true) proves the
// claim.
func predicateClaimProven(c *checker.Checker, fn *ast.Node, parameterName string, held abstractdomain.AbstractValue) bool {
	if fn == nil {
		return false
	}
	branches, ok := PredicateBodyBranches(c, fn, func(name string) bool { return name == parameterName })
	if !ok || len(branches.WhenTrue) == 0 {
		return false
	}
	proven := abstractdomain.Unknown
	for _, n := range branches.WhenTrue {
		if n.Binding != parameterName || len(n.Path) != 0 {
			// a claim about anything other than the bound parameter
			// itself proves nothing about IT — the body read something,
			// but not the thing the predicate claims
			return false
		}
		proven = ApplyNarrowed(proven, n)
	}
	// a numeric TYPEOF ground wraps the real half as POSSIBLY NaN — the
	// same wrapper checkPossiblyNaNSubset reads through (walk/
	// nan_wrapper.go's known.Inner). A body proving `v >= 0 && v <= 120`
	// on TOP of that ground already excludes NaN (NaN fails every
	// comparison), so the wrapper is unwrapped here rather than
	// SetOfKnown declining on it outright — the honest single-expression
	// twin (`typeof v === "number" && ...`) must still reduce to a
	// comparable set.
	if proven.Kind == abstractdomain.KindPossiblyNaN {
		proven = *proven.Inner
	}
	claimedForSet := held
	if claimedForSet.Kind == abstractdomain.KindPossiblyNaN {
		claimedForSet = *claimedForSet.Inner
	}
	provenSet, provenOk := abstractdomain.SetOfKnown(proven)
	claimedSet, claimedOk := abstractdomain.SetOfKnown(claimedForSet)
	if !provenOk || !claimedOk {
		return false
	}
	kernel := NarrowKernel()
	if kernel == nil || kernel.ScalarSubset == nil {
		return false
	}
	subset, refused := scalarSubsetRefusable(kernel, provenSet, claimedSet)
	return !refused && subset
}

// scalarSubsetRefusable asks the kernel's ScalarSubset question,
// turning a refusal (a panic through the bridge, exactly as every
// other kernel ask does) into an (answer, refused) pair — the same
// recover() shape apply_narrowing.go's seqSubsetRefusable uses for
// SeqSubset.
func scalarSubsetRefusable(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) (subset bool, refused bool) {
	defer func() {
		if recover() != nil {
			subset, refused = false, true
		}
	}()
	return kernel.ScalarSubset(a, b), false
}

// DeclaredTypePredicateOf is the `x is T` return annotation the callee
// of a call expression declares, or nil. It reads the SIGNATURE, not a
// body: a pinned local function's own return type node first, and
// otherwise the declaration the checker resolves the callee name to —
// so an imported `isString` states its contract the same way a local
// one does.
func DeclaredTypePredicateOf(c *checker.Checker, callee *ast.Node) *ast.Node {
	if fn := PinnedFunctionOf(c, callee); fn != nil {
		if node := returnTypeNodeOf(fn); node != nil && ast.IsTypePredicateNode(node) {
			return node
		}
	}
	cursor := callee
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(cursor)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		aliased := func() (result *ast.Symbol) {
			defer func() {
				if recover() != nil {
					result = nil
				}
			}()
			return c.GetAliasedSymbol(symbol)
		}()
		if aliased != nil {
			symbol = aliased
		}
	}
	if symbol == nil {
		return nil
	}
	for _, declaration := range symbol.Declarations {
		if node := signatureReturnTypeNodeOf(declaration); node != nil && ast.IsTypePredicateNode(node) {
			return node
		}
	}
	return nil
}

// returnTypeNodeOf is the return type node a function-like node spells,
// or nil where it spells none.
func returnTypeNodeOf(fn *ast.Node) *ast.Node {
	switch {
	case ast.IsArrowFunction(fn):
		return fn.AsArrowFunction().Type
	case ast.IsFunctionExpression(fn):
		return fn.AsFunctionExpression().Type
	case ast.IsFunctionDeclaration(fn):
		return fn.AsFunctionDeclaration().Type
	case ast.IsMethodDeclaration(fn):
		return fn.AsMethodDeclaration().Type
	}
	return nil
}

// signatureReturnTypeNodeOf is the return type node a DECLARATION
// spells — the function-like forms above, plus a `const f: (x) => x is
// T` variable whose initializer carries the annotation and a method
// signature in an interface.
func signatureReturnTypeNodeOf(declaration *ast.Node) *ast.Node {
	if node := returnTypeNodeOf(declaration); node != nil {
		return node
	}
	switch {
	case ast.IsMethodSignatureDeclaration(declaration):
		return declaration.AsMethodSignatureDeclaration().Type
	case ast.IsFunctionTypeNode(declaration):
		return declaration.AsFunctionTypeNode().Type
	case ast.IsVariableDeclaration(declaration):
		varDecl := declaration.AsVariableDeclaration()
		if varDecl.Type != nil && ast.IsFunctionTypeNode(varDecl.Type) {
			return varDecl.Type.AsFunctionTypeNode().Type
		}
		if varDecl.Initializer != nil {
			initializer := varDecl.Initializer
			for ast.IsParenthesizedExpression(initializer) {
				initializer = initializer.AsParenthesizedExpression().Expression
			}
			return returnTypeNodeOf(initializer)
		}
	}
	return nil
}

// declaredPredicatePosition is the ARGUMENT position the predicate's
// named parameter sits at, or -1 where the name matches no parameter
// (a `this is T` predicate, or a signature this walk cannot read).
func declaredPredicatePosition(c *checker.Checker, callee *ast.Node, fn *ast.Node, predicateParameter string) int {
	parametersOf := func(node *ast.Node) []*ast.Node {
		// a callee symbol's declaration can be ANY shape — a const
		// binding, an import specifier — and Parameters() dereferences
		// function-like data that such a node does not carry; a
		// non-function-like declaration names no parameter positions
		if node == nil || !ast.IsFunctionLike(node) {
			return nil
		}
		return node.Parameters()
	}
	candidates := [][]*ast.Node{parametersOf(fn)}
	cursor := callee
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	if symbol := c.GetSymbolAtLocation(cursor); symbol != nil {
		for _, declaration := range symbol.Declarations {
			candidates = append(candidates, parametersOf(declaration))
		}
	}
	for _, parameters := range candidates {
		for i, parameter := range parameters {
			name := parameter.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == predicateParameter {
				return i
			}
		}
	}
	return -1
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
			place := dataflowfacts.TrackedPlaceOfWith(c, a, isTracked)
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
