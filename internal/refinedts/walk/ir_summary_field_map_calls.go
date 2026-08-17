// A METHOD CALL on a once-assigned `this.<field>` Map or Set —
// `this.cache.get(k)`, `.has(k)`, `.delete(k)`, `.set(k, v)`,
// `.clear()`. The class never reassigns the field past its own
// declaration initializer, so the field's IDENTITY is fixed for the
// life of every instance; what changes is the Map/Set's CONTENTS, and
// this lowering tracks no slot for those. Every answer below claims
// only what the spec fixes regardless of contents:
//
//	.get(k)    → unknown (the held value, or undefined — not claimed)
//	.has(k)    → boolean (sec-map.prototype.has / sec-set.prototype.has)
//	.delete(k) → boolean (sec-map.prototype.delete / sec-set.prototype.delete)
//	.set(k, v) → no readable result (statement position only)
//	.clear()   → no readable result
//
// Havoc-free: none of these writes any slot the lowering tracks. The
// field's own bundle entry (its `this.m`-spelled slot, unknown-sorted
// since a bare `new Map()` field carries no annotation) is never
// written here — a content mutation invalidates nothing a summary
// claims, because nothing here claims anything about the contents.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// fieldMapMethods is the set of Map/Set methods this lowering answers
// on a once-assigned `this.<field>` receiver, and what each is worth:
// "value" reads unknown, "boolean" reads the two-value set, "none" is
// a statement-position-only write with no readable result.
var fieldMapMethods = map[string]string{
	"get":    "value",
	"has":    "boolean",
	"delete": "boolean",
	"set":    "none",
	"clear":  "none",
}

// thisFieldMapCallOf recognizes `this.<field>.<method>(args)` where
// `field`'s class declares it initialized to a bare `new Map(…)` /
// `new Set(…)` (the default lib's own constructor) and never
// reassigns it anywhere in the class body. Answers the field's own
// property-access node (`this.<field>`, whose slot IndexOf resolves),
// the method's spelled kind ("value"/"boolean"/"none"), and the call's
// arguments.
func thisFieldMapCallOf(call *ast.Node) (receiver *ast.Node, kind string, arguments []*ast.Node, ok bool) {
	if !ast.IsCallExpression(call) {
		return nil, "", nil, false
	}
	callExpr := call.AsCallExpression()
	if !ast.IsPropertyAccessExpression(callExpr.Expression) {
		return nil, "", nil, false
	}
	outer := callExpr.Expression.AsPropertyAccessExpression()
	if outer.QuestionDotToken != nil || !ast.IsIdentifier(outer.Name()) {
		return nil, "", nil, false
	}
	kind, isFieldMapMethod := fieldMapMethods[outer.Name().Text()]
	if !isFieldMapMethod {
		return nil, "", nil, false
	}
	fieldAccess := Unwrapped(outer.Expression)
	if !ast.IsPropertyAccessExpression(fieldAccess) {
		return nil, "", nil, false
	}
	inner := fieldAccess.AsPropertyAccessExpression()
	if inner.QuestionDotToken != nil || inner.Expression.Kind != ast.KindThisKeyword || !ast.IsIdentifier(inner.Name()) {
		return nil, "", nil, false
	}
	if callExpr.Arguments != nil {
		arguments = callExpr.Arguments.Nodes
	}
	return fieldAccess, kind, arguments, true
}

// onceAssignedMapField answers whether the property declaration for
// `fieldName` in `classLike` initializes to a bare `new Map(…)` /
// `new Set(…)` and is never reassigned anywhere else in the class —
// no plain or compound assignment, no `++`/`--`, no `delete`, on
// `this.<fieldName>`, in ANY member's body including nested closures
// (a closure runs at a time this lowering cannot place, so a
// reassignment inside one is exactly as disqualifying as one in plain
// statement position).
func onceAssignedMapField(ctx *FlowContext, classLike *ast.Node, fieldName string) bool {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return false
	}
	members := classLike.ClassLikeData().Members.Nodes
	declared := false
	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		property := member.AsPropertyDeclaration()
		name := property.Name()
		if name == nil || !ast.IsIdentifier(name) || name.Text() != fieldName {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		if property.Initializer == nil {
			return false
		}
		if _, _, isConstruction := constructionOfNewExpression(Unwrapped(property.Initializer)); !isConstruction {
			return false
		}
		if !resolvesToDefaultLib(ctx, Unwrapped(property.Initializer).AsNewExpression().Expression) {
			return false
		}
		declared = true
	}
	if !declared {
		return false
	}
	reassigned := false
	var inspect func(node *ast.Node)
	inspect = func(node *ast.Node) {
		if reassigned {
			return
		}
		if node.Kind == ast.KindThisKeyword {
			parent := node.Parent
			if parent != nil && ast.IsPropertyAccessExpression(parent) &&
				parent.AsPropertyAccessExpression().Expression == node &&
				ast.IsIdentifier(parent.AsPropertyAccessExpression().Name()) &&
				parent.AsPropertyAccessExpression().Name().Text() == fieldName &&
				writtenAt(parent) != writtenAtNone {
				reassigned = true
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			inspect(child)
			return reassigned
		})
	}
	for _, member := range members {
		if ast.IsPropertyDeclaration(member) {
			// the declaration's own initializer is the one write this
			// check must NOT count — inspect every OTHER member instead
			continue
		}
		body := member.Body()
		if body == nil {
			continue
		}
		inspect(body)
		if reassigned {
			return false
		}
	}
	return true
}

// thisFieldMapCallStatement lowers a recognized `this.<field>.<method>`
// call to its statement, or declines. Tried ahead of the summary and
// opaque-havoc tiers (SummaryCallOrHavocNamed, ir_summary_call.go) —
// the same seam a resolved-callee call takes — so a body that used to
// go porous at "call this.m.get" now completes instead.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads (`.set`/`.clear`'s own case
// always, `.get`/`.has`/`.delete` wherever the source discards the
// result — LRUCache.set's own `this.cache.delete(firstKey);`). Where
// -1, the answer is an IDENTITY write of the receiver's own bundle
// slot back onto itself (varStateEffect) — a sound no-op statement:
// nothing here ever claims to know the field's slot moved, so writing
// it back verbatim costs nothing and needs no new IR shape for "ran,
// wrote nothing."
func thisFieldMapCallStatement(context *LoweringContext, call *ast.Node, target int) (kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.IrStatement{}, false
	}
	fieldAccess, kind, arguments, isFieldMapCall := thisFieldMapCallOf(call)
	if !isFieldMapCall {
		return kernelbridge.IrStatement{}, false
	}
	classLike := dataflowfacts.EnclosingThisClass(call)
	if classLike == nil {
		return kernelbridge.IrStatement{}, false
	}
	fieldName := fieldAccess.AsPropertyAccessExpression().Name().Text()
	if !onceAssignedMapField(context.Flow, classLike, fieldName) {
		return kernelbridge.IrStatement{}, false
	}
	// the receiver's own bundle slot must resolve — the field is a READ
	// here (thisBundleOf's "the receiver of a method call is a field
	// value like any other"), so its entry already exists wherever the
	// method's body reads any this-field at all
	receiverSlot, resolved := IndexOf(context, fieldAccess)
	if !resolved {
		return kernelbridge.IrStatement{}, false
	}
	// every argument must move nothing this lowering could miss: a
	// call or a write inside `k`/`v` runs code no slot here accounts
	// for, and this call's own answer claims nothing about the map's
	// contents to catch it with
	for _, argument := range arguments {
		if !writeAndCallFree(argument) {
			return kernelbridge.IrStatement{}, false
		}
	}
	// `.set(k, v)` / `.clear()` claim no readable result at all — Map's
	// own `.set` returns the receiver (sec-map.prototype.set), which
	// this reading does not carry, so a caller that reads the VALUE
	// (`x = m.set(k, v)`) is not this shape and declines outright,
	// unlike `.get`/`.has`/`.delete`'s target<0 case below, which is
	// merely a caller that discards a value this reading DOES have.
	if kind == "none" {
		if target >= 0 {
			return kernelbridge.IrStatement{}, false
		}
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: receiverSlot, Effect: varStateEffect(receiverSlot)}, true
	}
	if target < 0 {
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: receiverSlot, Effect: varStateEffect(receiverSlot)}, true
	}
	if kind == "boolean" {
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: target, Effect: booleanPairEffect()}, true
	}
	// kind == "value": `.get(k)` — the held value, or undefined; not
	// claimed either way
	return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: target, Effect: unknownEffect}, true
}
