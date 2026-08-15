// split from ir_assignment.go — declaration-resolved constant reads

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// FreeConstEffect reads a free identifier that resolves — through
// import aliases — to a CONST declaration, and answers the effect of
// the const's own initializer where that initializer carries no
// binding of its own: a numeric, string, or boolean literal, null or
// undefined, or a write-and-call-free object/array/template literal
// (whose value rides unknown). A `let`/`var` declaration answers
// nothing — module state can move — and so does any initializer that
// reads names or runs code: those belong to the exporting file's own
// bindings, which this context does not hold.
func FreeConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsIdentifier(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, node)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return kernelbridge.LoopEffect{}, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return kernelbridge.LoopEffect{}, false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || list.Flags&ast.NodeFlagsConst == 0 {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
	if ast.IsNumericLiteral(head) || head.Kind == ast.KindTrueKeyword ||
		head.Kind == ast.KindFalseKeyword || IsAbsentKeyword(head) {
		return LowerEffectExpression(head, EffectReader{
			ReadPlace: func(string) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
			Opaque: func(e *ast.Node) (kernelbridge.LoopEffect, bool) {
				if IsAbsentKeyword(e) {
					return kernelbridge.AbsentConst(), true
				}
				return kernelbridge.LoopEffect{}, false
			},
		})
	}
	// inertValue, not writeAndCallFree: a const in the EXPORTING file may
	// hold a literal carrying arrows (`export const HOOKS = { on: () => …
	// }`), and building those arrows runs nothing. Their later writes land
	// on the exporting file's own bindings, which this context holds no
	// slot for, so there is no belief here for the closure to falsify.
	if (ast.IsObjectLiteralExpression(head) || ast.IsArrayLiteralExpression(head) ||
		ast.IsStringLiteral(head) || ast.IsTemplateExpression(head)) && inertValue(head) {
		// the value has no scalar spelling in a number-sorted read (a
		// string const's tuple belongs to the sequence route) — unknown
		// is what this reader can hold of it, and it is exact about that
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
	}
	return kernelbridge.LoopEffect{}, false
}

// freeStringConstEffect reads a free identifier that resolves — through
// import aliases — to a CONST whose initializer is a STRING literal, and
// answers that exact tuple. The sequence world's half of
// FreeConstEffect: that one reads a const numerically and hands a string
// const's tuple to this route, which is where a sequence read belongs.
//
// A `let`/`var` answers nothing (module state can move), and so does any
// other initializer shape — a template with substitutions reads the
// exporting file's own bindings, which this context does not hold.
func freeStringConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsIdentifier(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, node)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return kernelbridge.LoopEffect{}, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return kernelbridge.LoopEffect{}, false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || list.Flags&ast.NodeFlagsConst == 0 {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
	if ast.IsStringLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsStringLiteral().Text),
		}, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsNoSubstitutionTemplateLiteral().Text),
		}, true
	}
	return kernelbridge.LoopEffect{}, false
}

// EnumMemberConstEffect reads `Scope.TRANSIENT` — a property access
// whose NAME resolves to an ENUM MEMBER. An enum member is immutable
// by the language, so its explicit literal initializer IS its value: a
// numeric literal answers the exact constant. A string-membered or
// auto-numbered member answers nothing here — the numeric reader has
// no spelling for a word, and an auto value would be derived, never
// read.
func EnumMemberConstEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if node == nil || !ast.IsPropertyAccessExpression(node) {
		return kernelbridge.LoopEffect{}, false
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return kernelbridge.LoopEffect{}, false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	symbol := symbolAt(context.Flow.P.Checker, access.Name())
	if symbol == nil || symbol.ValueDeclaration == nil || !ast.IsEnumMember(symbol.ValueDeclaration) {
		return kernelbridge.LoopEffect{}, false
	}
	initializer := symbol.ValueDeclaration.AsEnumMember().Initializer
	if initializer == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(initializer)
	if !ast.IsNumericLiteral(head) {
		return kernelbridge.LoopEffect{}, false
	}
	return LowerEffectExpression(head, EffectReader{
		ReadPlace: func(string) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
		Opaque:    func(*ast.Node) (kernelbridge.LoopEffect, bool) { return kernelbridge.LoopEffect{}, false },
	})
}

// IsAbsentKeyword is whether an expression spells the ABSENT value:
// `null` or `undefined`. JavaScript's two absent spellings are one
// outcome kernel-side (KnownState's absent flag conflates them), so
// both lower to the same state constant.
//
// `undefined` is an ordinary identifier in the grammar, not a keyword
// token — a local named `undefined` would shadow it, so the tracked
// slots are consulted first by the caller (a tracked name COPIES) and
// only a free spelling reaches here.
func IsAbsentKeyword(e *ast.Node) bool {
	head := Unwrapped(e)
	if head.Kind == ast.KindNullKeyword {
		return true
	}
	return ast.IsIdentifier(head) && head.Text() == "undefined"
}
