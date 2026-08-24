// from evaluation/syntax_models.ts
//
// ONE table over syntax kinds (finding 9): every kind either has a
// case in evaluateForm ("modeled" — a terminal fall-through under
// it means the case exists and the instance did not resolve) or
// carries its own decline sentence. A kind cannot sit in two
// parallel sets and drift; writing a real case for a declined kind
// means changing its row to "modeled". Any kind absent from the
// table gets the generic no-case sentence.

package walk

import "github.com/microsoft/typescript-go/internal/ast"

// SyntaxModel is the TS source's `"modeled" | { said: string;
// unsupported: boolean }` union: Modeled true for the bare "modeled"
// row, false with Said/Unsupported set for a declined row.
type SyntaxModel struct {
	Modeled     bool
	Said        string
	Unsupported bool
}

// TypeReadsSilentResult is typeReadsSilentResult in the TS source: the
// expression forms whose silent result the resolved type may answer
// for: join-shaped composites (a short-circuit, a ternary) where
// tsc's type at the whole expression covers every runtime outcome and
// no handler uses the unknown as a fixpoint cut. Calls and reads are
// excluded — their direct unknown returns carry solver meaning; only
// their FALL-THROUGH silence seeds, at the fallback under the syntax
// table. Identifiers stay out — their reporting seam owns the silence
// story.
func TypeReadsSilentResult(e *ast.Node) bool {
	if ast.IsConditionalExpression(e) {
		return true
	}
	if ast.IsBinaryExpression(e) {
		op := e.AsBinaryExpression().OperatorToken.Kind
		return op == ast.KindAmpersandAmpersandToken ||
			op == ast.KindBarBarToken ||
			op == ast.KindQuestionQuestionToken
	}
	return false
}

// SyntaxModels is SYNTAX_MODELS in the TS source.
var SyntaxModels = map[ast.Kind]SyntaxModel{
	ast.KindParenthesizedExpression:       {Modeled: true},
	ast.KindAwaitExpression:               {Modeled: true},
	ast.KindSatisfiesExpression:           {Modeled: true},
	ast.KindAsExpression:                  {Modeled: true},
	ast.KindNonNullExpression:             {Modeled: true},
	ast.KindTypeAssertionExpression:       {Modeled: true},
	ast.KindNumericLiteral:                {Modeled: true},
	ast.KindTrueKeyword:                   {Modeled: true},
	ast.KindFalseKeyword:                  {Modeled: true},
	ast.KindStringLiteral:                 {Modeled: true},
	ast.KindNoSubstitutionTemplateLiteral: {Modeled: true},
	ast.KindTemplateExpression:            {Modeled: true},
	ast.KindPrefixUnaryExpression:         {Modeled: true},
	ast.KindPostfixUnaryExpression:        {Modeled: true},
	ast.KindIdentifier:                    {Modeled: true},
	ast.KindNullKeyword:                   {Modeled: true},
	ast.KindVoidExpression:                {Modeled: true},
	ast.KindTypeOfExpression:              {Modeled: true},
	ast.KindDeleteExpression:              {Modeled: true},
	ast.KindArrayLiteralExpression:        {Modeled: true},
	ast.KindObjectLiteralExpression:       {Modeled: true},
	ast.KindConditionalExpression:         {Modeled: true},
	ast.KindBinaryExpression:              {Modeled: true},
	ast.KindCallExpression:                {Modeled: true},
	ast.KindPropertyAccessExpression:      {Modeled: true},
	ast.KindElementAccessExpression:       {Modeled: true},
	ast.KindNewExpression: {
		Said:        "new builds a value the walk does not model",
		Unsupported: true,
	},
	ast.KindTaggedTemplateExpression: {Modeled: true},
	// `super` reads as the base entered from outside this walk, and the
	// call it sits under forgets what the base could have written
	ast.KindSuperKeyword: {Modeled: true},
	ast.KindClassExpression: {
		// `new C()` on a class expression constructs from the class's own
		// members (evaluate_new_expression.go reads any class-LIKE
		// declaration); the class VALUE itself — the constructor
		// function a bare `C` reference holds — is what stays unread
		Said:        "a class value is not modeled",
		Unsupported: true,
	},
	// the kind covers `import.meta` and `new.target`; new.target reads as
	// the constructor-or-absent it is, so only import.meta declines here
	ast.KindMetaProperty: {
		Said:        "import.meta is the host's value, not the program's",
		Unsupported: true,
	},
	ast.KindYieldExpression: {
		Said:        "what a yield resumes with comes from the caller — not read",
		Unsupported: true,
	},
	ast.KindArrowFunction: {
		Said:        "a function — read at its calls",
		Unsupported: false,
	},
	ast.KindFunctionExpression: {
		Said:        "a function — read at its calls",
		Unsupported: false,
	},
	// jsx_expression.go's arms read all four — never a scalar sort, an
	// object with unstated keys, attributes and children walked for
	// their own sinks
	ast.KindJsxElement:            {Modeled: true},
	ast.KindJsxSelfClosingElement: {Modeled: true},
	ast.KindJsxFragment:           {Modeled: true},
	ast.KindJsxExpression:         {Modeled: true},
}
