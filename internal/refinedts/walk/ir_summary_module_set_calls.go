// A METHOD CALL on a MODULE-LEVEL `const` bound to `new Set(…)` / `new
// Map(…)` — the free-identifier twin of ir_summary_field_map_calls.go's
// `this.<field>.<method>` recognizer. `const S = new Set([...]); … S.has(x)`
// is a common vocabulary-membership pattern (svgPropertiesNoEvents.ts's
// SVGElementPropKeySet is the fixture this file mirrors): the const's
// identity is fixed for the module's whole lifetime, and — same as the
// field case — nothing here tracks the collection's CONTENTS, only that
// calling `.has`/`.delete` answers the two-value boolean set regardless.
//
// Havoc-free for the identical reason the field case is: nothing here
// writes any slot the lowering tracks — a bare identifier read never
// carries a slot of its own the way `this.<field>` does (thisFieldMapCallOf
// resolves the field's OWN bundle slot as the receiver to write an identity
// state back into; a free module const has no such slot to begin with, so
// the -1 target case here answers a genuine no-op statement with nothing to
// write at all).
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// moduleSetMethods is the set of Set/Map methods this lowering answers on
// a module-const receiver, and what each is worth. Narrower than
// fieldMapMethods (ir_summary_field_map_calls.go): a module const has no
// receiver slot to write an identity state back into, so ".set"/".clear"
// have no sound statement-position answer here at all (unlike the field
// case's varStateEffect no-op) and are left out — a body calling either
// through a module-level Set/Map declines to this recognizer and falls to
// the existing havoc tiers, which is unaffected by this file's absence
// from those two methods' behavior.
var moduleSetMethods = map[string]string{
	"has":    "boolean",
	"delete": "boolean",
}

// moduleSetCallOf recognizes `<name>.<method>(args)` where `name` resolves
// — through import aliases, symbolAt — to a top-level `const` declaration
// initialized to a bare `new Map(…)` / `new Set(…)` and never reassigned
// anywhere in the file (module bindings have no `this`-scoped body to
// reassign FROM the way a class field does — ReassignedNames' file-wide
// scan is exactly the right coarseness here, not merely sufficient for
// it). Answers the identifier node itself (there is no owning receiver
// slot the way a field access has one), the method's spelled kind, and
// the call's arguments.
func moduleSetCallOf(ctx *FlowContext, call *ast.Node) (identifier *ast.Node, kind string, arguments []*ast.Node, ok bool) {
	if !ast.IsCallExpression(call) {
		return nil, "", nil, false
	}
	callExpr := call.AsCallExpression()
	if !ast.IsPropertyAccessExpression(callExpr.Expression) {
		return nil, "", nil, false
	}
	access := callExpr.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return nil, "", nil, false
	}
	kind, isModuleSetMethod := moduleSetMethods[access.Name().Text()]
	if !isModuleSetMethod {
		return nil, "", nil, false
	}
	receiver := Unwrapped(access.Expression)
	if !ast.IsIdentifier(receiver) {
		return nil, "", nil, false
	}
	if !onceAssignedModuleSetConst(ctx, receiver) {
		return nil, "", nil, false
	}
	if callExpr.Arguments != nil {
		arguments = callExpr.Arguments.Nodes
	}
	return receiver, kind, arguments, true
}

// onceAssignedModuleSetConst answers whether `identifier` resolves to a
// top-level `const` declaration initializing to a bare `new Map(…)` /
// `new Set(…)` (the default lib's own constructor — resolvesToDefaultLib,
// shared with the field-map recognizer) and never reassigned anywhere in
// its declaring file — mirroring onceAssignedMapField's own three checks
// (const, bare-constructor initializer, no later write) at module scope
// instead of class scope.
func onceAssignedModuleSetConst(ctx *FlowContext, identifier *ast.Node) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return false
	}
	symbol := symbolAt(ctx.P.Checker, identifier)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) || list.Flags&ast.NodeFlagsConst == 0 {
		return false
	}
	// a top-level const: its declaring VariableStatement's parent is the
	// source file itself, not a block/function/class body — the same
	// "free" reading FreeConstEffect's own doc describes for a plain
	// literal const, applied here to a constructed one
	statement := list.Parent
	if statement == nil || statement.Parent == nil || !ast.IsSourceFile(statement.Parent) {
		return false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return false
	}
	if _, _, isConstruction := constructionOfNewExpression(Unwrapped(initializer)); !isConstruction {
		return false
	}
	if !resolvesToDefaultLib(ctx, Unwrapped(initializer).AsNewExpression().Expression) {
		return false
	}
	if _, reassigned := narrowing.ReassignedNames(ast.GetSourceFileOfNode(identifier))[identifier.Text()]; reassigned {
		return false
	}
	return true
}

// moduleSetCallStatement lowers a recognized `<moduleConst>.<method>` call
// to its statement, or declines. Tried in the same seam
// thisFieldMapCallStatement is (SummaryCallOrHavocNamed, ir_summary_call.go)
// — a module-const receiver resolves no FunctionContract either, so trying
// this ahead of the blob tier costs nothing on every call this shape does
// not match.
//
// `target` is the caller slot the call's value lands in, or -1 for a bare
// call whose value nothing reads. Unlike the field-map statement, there is
// no receiver slot to write an identity state back into on the -1 case — a
// module const carries no bundle entry at all — so a discarded boolean
// read answers a genuine NO-OP statement (Kind default / zero value would
// be wrong; IrStatementCall with no callee is not a shape this route
// builds, so the -1 case answers a self-assign of the DONE-less sentinel
// nothing else reads: an assign into a scratch write-only marker is not
// available here, so -1 simply declines — there is nothing sound and
// non-trivial to build for a call whose only claim is "answers boolean"
// and whose value nothing reads).
func moduleSetCallStatement(context *LoweringContext, call *ast.Node, target int) (kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.IrStatement{}, false
	}
	_, kind, arguments, isModuleSetCall := moduleSetCallOf(context.Flow, call)
	if !isModuleSetCall {
		return kernelbridge.IrStatement{}, false
	}
	// every argument must move nothing this lowering could miss — same
	// gate thisFieldMapCallStatement wears over its own arguments
	for _, argument := range arguments {
		if !writeAndCallFree(argument) {
			return kernelbridge.IrStatement{}, false
		}
	}
	if target < 0 {
		// nothing reads the value and there is no receiver slot to write
		// an identity no-op into — this call's only effect claim
		// (nothing moves) is already true of NOT lowering it as a
		// statement at all, so it declines rather than invent a target-
		// less IrStatementAssign shape no other route needs
		return kernelbridge.IrStatement{}, false
	}
	// kind is always "boolean" today (moduleSetMethods has no "none" or
	// "value" row), but the switch mirrors thisFieldMapCallStatement's
	// shape so a future kind added to the table costs one arm, not a
	// rewrite
	if kind == "boolean" {
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: target, Effect: booleanPairEffect()}, true
	}
	return kernelbridge.IrStatement{}, false
}
