// split from ir_field_bundles.go — the stable symbol key

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

/* ── the stable symbol key ───────────────────────────────────────── */

// symbolFieldPrefix opens the name a SYMBOL-KEYED field is spelled
// under. `#` is the same character the base layout's own "#done"/"#ret"
// and the accessor temps' "#get." wear, and the `:` after the tag is
// what keeps this apart from a PRIVATE IDENTIFIER: `this.#id` spells its
// field "#id", with no separator, and no JavaScript private name may
// hold a colon. So "#sym:INSTANCE_ID_SYMBOL" collides with neither a
// plain property name nor a `#`-named one.
const symbolFieldPrefix = "#sym:"

// StableSymbolKeyName is the field name behind a SYMBOL-KEYED member
// access — `this[INSTANCE_ID_SYMBOL]`, `originalRef[K_MODULE_ID]` — or
// (false) where the key is not one this reading calls stable.
//
// THE KEY IDENTITY. A key qualifies when it is a plain identifier whose
// binding is a MODULE-LEVEL `const` initialized by a `Symbol(...)` or
// `Symbol.for(...)` call on the default library's Symbol. Two things
// follow from that shape, and both are what a field name needs:
//
//   - the const cannot be rebound, and its declaration runs ONCE per
//     module, so every evaluation of the name in the program reads the
//     one symbol value the module built. `Symbol.for` is stable for a
//     second reason on top (the registry hands the same symbol back per
//     key, sec-symbol.for), but `Symbol()` needs no second reason: the
//     single evaluation is the whole argument.
//   - two accesses spelled with the SAME const are the same field, and
//     accesses spelled with different consts are different fields —
//     because the symbol is the property key at runtime, and distinct
//     symbols are distinct keys however they are described.
//
// The identity carried is the const's own SYMBOL (the checker's, through
// import aliases), so an imported `INSTANCE_ID_SYMBOL` and the exporting
// file's own spelling of it name one field. The NAME is derived from the
// const's declared identifier, which is unique within any one scope, so
// no two distinct consts a class body can both see spell one field name.
//
// WHAT STAYS OUT, each because the shape does not hold the value fixed:
// a `let`/`var`/parameter binding (rebindable), a const initialized by
// anything but a Symbol construction (an imported value the reading has
// not followed, a call whose result varies), a const declared inside a
// function or block (a fresh symbol per entry, so two accesses in two
// activations are two different keys), a qualified or computed key
// expression, and a WELL-KNOWN symbol (`Symbol.iterator`), which is a
// property access rather than an identifier and never reaches here.
//
// A nil checker declines everything — the census's ctx-less callers get
// exactly the behaviour they had before this reading existed.
func StableSymbolKeyName(c *checker.Checker, key *ast.Node) (string, bool) {
	if c == nil || key == nil {
		return "", false
	}
	key = Unwrapped(key)
	if key == nil || !ast.IsIdentifier(key) {
		return "", false
	}
	symbol := symbolAt(c, key)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return "", false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return "", false
	}
	name := declaration.AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		// a binding pattern spells no single const name to derive from
		return "", false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) {
		return "", false
	}
	if (list.Flags & ast.NodeFlagsConst) == 0 {
		return "", false
	}
	// MODULE LEVEL: the declaration statement's own parent is the source
	// file. A const inside a function or a block re-runs its initializer
	// per entry, so `Symbol('x')` there is a fresh key each time and two
	// accesses need not name one field.
	statement := list.Parent
	if statement == nil || !ast.IsVariableStatement(statement) {
		return "", false
	}
	if statement.Parent == nil || !ast.IsSourceFile(statement.Parent) {
		return "", false
	}
	initializer := Unwrapped(declaration.AsVariableDeclaration().Initializer)
	if initializer == nil || !symbolConstructionCall(c, initializer) {
		return "", false
	}
	return symbolFieldPrefix + name.Text(), true
}

// symbolConstructionCall is whether an expression BUILDS a symbol:
// `Symbol()`, `Symbol(description)`, or `Symbol.for(key)`, with the
// `Symbol` name resolving to the default library so a local shadow named
// Symbol is not mistaken for the builtin. It is readSymbolBuiltin's
// recognition (symbol_builtin_models.go) without the value reading — the
// key identity needs to know a symbol was constructed, not which one.
func symbolConstructionCall(c *checker.Checker, e *ast.Node) bool {
	if !ast.IsCallExpression(e) {
		return false
	}
	call := e.AsCallExpression()
	if call.QuestionDotToken != nil {
		return false
	}
	if ast.IsPropertyAccessExpression(call.Expression) {
		access := call.Expression.AsPropertyAccessExpression()
		return access.QuestionDotToken == nil &&
			ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Symbol" &&
			ast.IsIdentifier(access.Name()) && access.Name().Text() == "for" &&
			c.SymbolInDefaultLib(c.GetSymbolAtLocation(access.Expression))
	}
	return ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Symbol" &&
		c.SymbolInDefaultLib(c.GetSymbolAtLocation(call.Expression))
}

// SymbolKeyedFieldName reads the field name behind a whole ELEMENT
// ACCESS whose receiver is the given object — `this[S]`, `wrapper[S]` —
// answering the derived `#sym:` name. An optional step declines: `this?.[S]`
// admits an absent receiver, which no slot spells.
func SymbolKeyedFieldName(c *checker.Checker, access *ast.Node) (string, bool) {
	if access == nil || !ast.IsElementAccessExpression(access) {
		return "", false
	}
	element := access.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		return "", false
	}
	return StableSymbolKeyName(c, element.ArgumentExpression)
}

// symbolMemberFieldName reads the field name behind a class MEMBER's
// computed name — the `[INSTANCE_ID_SYMBOL]` of
// `private readonly [INSTANCE_ID_SYMBOL]: string`. Same key identity,
// read off a declaration rather than an access, so the declaration and
// every access spell one field.
func symbolMemberFieldName(c *checker.Checker, name *ast.Node) (string, bool) {
	if name == nil || !ast.IsComputedPropertyName(name) {
		return "", false
	}
	return StableSymbolKeyName(c, name.AsComputedPropertyName().Expression)
}

// symbolKeyedMethodOf is the class METHOD declared under a stable symbol
// key — the `[S]() { … }` of a class that also spells `this[S](…)` in one
// of its bodies — or nil.
//
// WHY A SYMBOL-KEYED METHOD IS A METHOD. The key identity
// (StableSymbolKeyName) is what makes `#sym:S` name ONE thing: the const
// cannot be rebound, its declaration runs once per module, and the symbol
// IS the property key at runtime. That argument says nothing about
// whether the member holding the key is a field or a method — it settles
// the KEY, and the member kind is then read off the declaration exactly
// as a dotted name's is. So a symbol-keyed method resolves to one body
// the write-set closure can walk, which is the whole of what the closure
// needs from a method name.
//
// Only a method WITH A BODY answers: an overload signature, an accessor,
// and an arrow-valued property declaration each hold writes this reading
// cannot enumerate, and the closure's own (nil, false) is what they get.
// A STATIC member never answers — it belongs to the constructor object,
// not to any instance, so no receiver's call reaches it.
func symbolKeyedMethodOf(c *checker.Checker, classLike *ast.Node, name string) *ast.Node {
	if c == nil || classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if !ast.IsMethodDeclaration(member) || member.Body() == nil {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		spelled, isSymbolKey := symbolMemberFieldName(c, member.Name())
		if isSymbolKey && spelled == name {
			return member
		}
	}
	return nil
}
