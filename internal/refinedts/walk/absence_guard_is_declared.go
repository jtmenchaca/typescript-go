// from control_flow/if_statement.ts (the dead-guard report's gate)
//
// Whether a condition that folded false is an ABSENCE TEST the tested
// name's own declared type demands — `value === undefined` on a name
// the host types `Age | undefined`.
//
// The dead-guard report exists for the vacuous-guard bug class: a test
// that cannot pass because the author wrote it against the wrong thing
// (`isFunction(key)` on a for-in string key, `nodeEnv === "development"`
// under a nodeEnv fixed to "production"). In every one of those the
// condition is dead AGAINST THE DECLARED TYPE, and the author has a
// real defect to fix.
//
// A `Map.get` guarded by `=== undefined` is the opposite case. The host
// signature is `V | undefined` (sec-map.prototype.get), so the guard is
// the spelling the host REQUIRES — without it the read does not compile.
// The walk may separately know the key was just set and answer the held
// value with no absence, and then the condition folds false; reporting
// that as a dead branch refuses the author for writing the only code the
// type system accepts. The knowledge is the walk's own, past what the
// declaration states, exactly the situation CallSiteSeeded already
// silences for parameters seeded from today's call sites.
//
// So: an absence test on a name whose DECLARED type admits absence is
// never a dead guard. The declared type is read at the declaration
// (GetTypeOfSymbolAtLocation with the symbol's ValueDeclaration, the
// alias_analysis.go precedent) rather than at the condition, because at
// the condition the checker's own narrowing has already removed the
// absence and would answer the question in the wrong direction.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// absenceTestOperand: the expression an absence test is testing, and
// whether the condition IS an absence test. Recognized spellings, all
// of which fold to "is this value absent":
//
//	x === undefined   x !== undefined   x == null   x != null
//	undefined === x   null == x         !x
//
// `!x` counts because on a `T | undefined` name it is the idiomatic
// absence guard; on a name whose declared type cannot be absent it is
// a truthiness test and this function's caller reads the declared type
// anyway, so nothing is silenced that the declaration does not already
// admit.
func absenceTestOperand(e *ast.Node) (*ast.Node, bool) {
	bare := e
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if ast.IsPrefixUnaryExpression(bare) {
		unary := bare.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			return absenceTestSubject(unary.Operand), true
		}
	}
	if !ast.IsBinaryExpression(bare) {
		return nil, false
	}
	binary := bare.AsBinaryExpression()
	switch binary.OperatorToken.Kind {
	case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken,
		ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
	default:
		return nil, false
	}
	if isAbsenceLiteral(binary.Right) {
		return absenceTestSubject(binary.Left), true
	}
	if isAbsenceLiteral(binary.Left) {
		return absenceTestSubject(binary.Right), true
	}
	return nil, false
}

// isAbsenceLiteral: the `undefined` identifier or the `null` keyword —
// the two right-hand sides an absence test compares against.
func isAbsenceLiteral(e *ast.Node) bool {
	bare := e
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if bare.Kind == ast.KindNullKeyword {
		return true
	}
	return ast.IsIdentifier(bare) && bare.AsIdentifier().Text == "undefined"
}

// absenceTestSubject strips parentheses off the tested expression.
func absenceTestSubject(e *ast.Node) *ast.Node {
	bare := e
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	return bare
}

// DeclaredTypeAdmitsAbsence: does the tested expression's own
// declaration admit undefined, null, or void? True also for an OPTIONAL
// property (`hidden?: number`), whose optionality is a symbol flag
// rather than a union arm.
//
// False whenever the answer cannot be established — no checker, no
// symbol, no declaration — so an unreadable case leaves the dead-guard
// report exactly as it was.
func DeclaredTypeAdmitsAbsence(ctx *FlowContext, tested *ast.Node) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || tested == nil {
		return false
	}
	c := ctx.P.Checker
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(tested)
	if symbol == nil {
		return false
	}
	// an optional property or an optional parameter states its absence
	// through the flag, not through a union arm
	if (symbol.Flags & ast.SymbolFlagsOptional) != 0 {
		return true
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil {
		return false
	}
	// the type AT THE DECLARATION, never at the condition: by the
	// condition the checker's narrowing has already removed the absence
	// this function is asking about
	tracing.CountBy("host.typeOfSymbolAtLocation", 1)
	declared := c.GetTypeOfSymbolAtLocation(symbol, declaration)
	if declared == nil {
		return false
	}
	return typeAdmitsAbsence(declared)
}

// typeAdmitsAbsence: does this type have undefined, null, or void in
// it — either outright or as a union arm?
func typeAdmitsAbsence(t *checker.Type) bool {
	const absent = checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid
	if (t.Flags() & absent) != 0 {
		return true
	}
	if (t.Flags() & checker.TypeFlagsUnion) == 0 {
		return false
	}
	for _, arm := range t.Types() {
		if (arm.Flags() & absent) != 0 {
			return true
		}
	}
	return false
}

// AbsenceGuardTheDeclarationDemands: the whole gate in one ask — is
// this folded-false condition an absence test on a name whose
// declaration admits absence? Such a guard is the host type's own
// requirement, so it is never reported as a dead branch.
//
// UNLESS the declared absence is refuted — by the callee's OWN BODY on
// every input (CalleeBodyRefutesItsDeclaredAbsence), or by the walk's
// OWN EXACT KNOWLEDGE of the tested expression's current flow value
// (WalkKnowsTestedExpressionIsNeverAbsent). That is the discriminator
// between the shapes that reach here with identical syntax:
//
//   - `Map.get(k)` guarded by `=== undefined`, where the walk answers
//     the read as "unknown, possibly absent" (no exact knowledge): the
//     declared `V | undefined` is HONEST — the signature is a host one
//     with no body to read, and the walk's contrary knowledge, if any,
//     is CALL-SPECIFIC. The guard is the only spelling that compiles,
//     and the report stays quiet.
//   - a local `keepPresent(x): number | undefined` whose every return
//     path yields a number: the declared absence is refuted by the
//     function's own body, for EVERY input, with no call-site knowledge
//     involved. The declaration is simply wrong, the guard under it can
//     never pass, and that is the vacuous-guard defect this report
//     exists for.
//   - `new URL(exactString).searchParams.get("code")` on a query the
//     walk parsed exactly (url_models.go's exactUrlObject): the
//     DECLARED signature is the same `string | null` as Map.get's, but
//     the walk's read is not "unknown, possibly absent" — it is the
//     exact word "AB", because the receiver was a COMPLETE object this
//     specific call site built from a COMPLETE literal. That is exactly
//     as call-specific as `keepPresent`'s body-refutation, carried
//     through a builtin model's exact reader instead of a local
//     function's return statements — the same vacuous-guard defect,
//     read a different way.
func AbsenceGuardTheDeclarationDemands(ctx *FlowContext, env Env, condition *ast.Node) bool {
	tested, isAbsenceTest := absenceTestOperand(condition)
	if !isAbsenceTest || tested == nil {
		return false
	}
	if !DeclaredTypeAdmitsAbsence(ctx, tested) {
		return false
	}
	if CalleeBodyRefutesItsDeclaredAbsence(ctx, tested) {
		return false
	}
	return !WalkKnowsTestedExpressionIsNeverAbsent(env, tested)
}

// WalkKnowsTestedExpressionIsNeverAbsent: does the walk's OWN current
// flow-value for the tested expression already exclude undefined and
// null outright — an exact answer past what the declared type states,
// the same standing CalleeBodyRefutesItsDeclaredAbsence reads through
// a callee's body instead of a value?
//
// Read from the live env when the tested expression is a plain
// identifier bound there (the common case: `const code = url...get(...)`
// then `code === null` on the same, unreassigned binding) — never by
// re-evaluating an arbitrary expression, which could re-run an effect
// or read a name the env does not track. Anything else — no env, not a
// tracked identifier, or a flow-value the walk did not pin exactly —
// answers false, leaving the gate exactly as it was.
func WalkKnowsTestedExpressionIsNeverAbsent(env Env, tested *ast.Node) bool {
	if env == nil || !ast.IsIdentifier(tested) {
		return false
	}
	current, found := env.Get(tested.AsIdentifier().Text)
	if !found {
		return false
	}
	switch current.Kind {
	case abstractdomain.KindUndef, abstractdomain.KindNull,
		abstractdomain.KindPossiblyUndefined, abstractdomain.KindUnknown:
		return false
	default:
		return true
	}
}

// CalleeBodyRefutesItsDeclaredAbsence: was the tested name initialized
// from a call to a function whose OWN BODY never yields undefined or
// null on any input, even though its declared return type admits one?
//
// The reading is entirely syntactic and entirely about the CALLEE, so
// it says nothing about this call site: every `return` in the body is
// examined, and the answer is yes only when each one returns an
// expression that is provably not absent. That makes the claim hold for
// every input, which is exactly what separates a wrong declaration from
// an honest one narrowed by local knowledge.
//
// False on anything not read with certainty — no initializer, a callee
// with no resolvable body (a host signature like Map.get, an overload,
// an ambient declaration), a body with no return statements, or a
// return this reader cannot judge. A false answer leaves the gate
// exactly as it was, so an unreadable case never gains a report.
func CalleeBodyRefutesItsDeclaredAbsence(ctx *FlowContext, tested *ast.Node) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return false
	}
	c := ctx.P.Checker
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(tested)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil || !ast.IsCallExpression(initializer) {
		return false
	}
	callee := initializer.AsCallExpression().Expression
	tracing.CountBy("host.symbolAtLocation", 1)
	calleeSymbol := c.GetSymbolAtLocation(callee)
	if calleeSymbol == nil || calleeSymbol.ValueDeclaration == nil {
		return false
	}
	body := calleeSymbol.ValueDeclaration.Body()
	if body == nil {
		return false // a host signature (Map.get) — nothing to read
	}
	sawReturn := false
	refutes := true
	forEachReturnInBody(body, func(returned *ast.Node) {
		sawReturn = true
		if !expressionIsNeverAbsent(returned) {
			refutes = false
		}
	})
	return sawReturn && refutes
}

// forEachReturnInBody visits every `return` in a function body, without
// descending into a NESTED function — an inner function's returns are
// its own, never this body's.
func forEachReturnInBody(node *ast.Node, visit func(returned *ast.Node)) {
	node.ForEachChild(func(child *ast.Node) bool {
		if ast.IsFunctionLike(child) {
			return false // a nested function's returns belong to it
		}
		if ast.IsReturnStatement(child) {
			visit(child.AsReturnStatement().Expression)
			return false
		}
		forEachReturnInBody(child, visit)
		return false
	})
}

// expressionIsNeverAbsent: is this returned expression provably neither
// undefined nor null, read syntactically?
//
// A bare `return;` (no expression) yields undefined outright, so it
// answers false. A numeric or string literal never is. An IDENTIFIER
// answers false — its value is exactly what this reader cannot settle
// syntactically — which keeps the whole judgment conservative except
// where the body's returns are literals.
//
// Deliberately narrow: the gate's default is to stay quiet, and only a
// body this reader can fully settle moves it. `keepPresent` reaches the
// affirmative through its `return x` under an `x !== undefined` guard,
// which narrowedIdentifierIsPresent below reads.
func expressionIsNeverAbsent(returned *ast.Node) bool {
	if returned == nil {
		return false // `return;` is undefined
	}
	bare := returned
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	switch bare.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindBigIntLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindObjectLiteralExpression, ast.KindArrayLiteralExpression:
		return true
	}
	if ast.IsIdentifier(bare) {
		return narrowedIdentifierIsPresent(bare)
	}
	return false
}

// narrowedIdentifierIsPresent: is this returned identifier sitting
// inside a branch that already proved it present?
//
// It walks outward from the return to the enclosing function, and
// answers yes when some enclosing `if` tests exactly this name for
// presence (`name !== undefined`, `name != null`) and the return is in
// its THEN branch — the shape `keepPresent` is written in. Any other
// arrangement answers false.
func narrowedIdentifierIsPresent(name *ast.Node) bool {
	wanted := name.AsIdentifier().Text
	child := name
	for node := name.Parent; node != nil; node = node.Parent {
		if ast.IsFunctionLike(node) {
			return false
		}
		if ast.IsIfStatement(node) {
			ifStmt := node.AsIfStatement()
			// only the THEN branch carries the presence the test proved
			if ifStmt.ThenStatement == child {
				if tested, isTest := absenceTestOperand(ifStmt.Expression); isTest &&
					tested != nil && ast.IsIdentifier(tested) &&
					tested.AsIdentifier().Text == wanted &&
					absenceTestIsPresenceForm(ifStmt.Expression) {
					return true
				}
			}
		}
		child = node
	}
	return false
}

// absenceTestIsPresenceForm: does this absence test prove PRESENCE in
// its true branch? `x !== undefined` and `x != null` do; `x === undefined`
// and `!x` prove the opposite there.
func absenceTestIsPresenceForm(e *ast.Node) bool {
	bare := e
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if !ast.IsBinaryExpression(bare) {
		return false
	}
	switch bare.AsBinaryExpression().OperatorToken.Kind {
	case ast.KindExclamationEqualsEqualsToken, ast.KindExclamationEqualsToken:
		return true
	}
	return false
}
