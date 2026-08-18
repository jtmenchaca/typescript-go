// split from ir_object_slots.go — the leaf-exact destructuring lowerings

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// hasNestedFamilySlot answers whether some OTHER slot is spelled with
// `prefix+"."` at its front — the tell that `prefix` names a NESTED
// ROOT (a flattened family's own leaves sit below it, but `prefix`
// itself never got a depth-1 slot: nestedMemberLeavesOf's own array/
// type-literal/type-reference readers replace a member with its
// children rather than keeping a slot at the member's own key). Used
// only where slotIndexOfName has already answered false for `prefix`
// itself — a key with BOTH a direct slot and nested children never
// reaches this check.
func hasNestedFamilySlot(context *LoweringContext, prefix string) bool {
	dotted := prefix + "."
	if context.Names != nil {
		for spelled := range context.Names {
			if strings.HasPrefix(spelled, dotted) {
				return true
			}
		}
		return false
	}
	for _, binding := range context.Bindings {
		if strings.HasPrefix(binding, dotted) {
			return true
		}
	}
	return false
}

// DestructuringWithDefaultsOf is the leaf-exact destructuring with
// per-element DEFAULTS: `const { x = 1, y } = p` from a flattened
// holder. Each element assigns its leaf's slot, and a defaulted element
// follows with the definedness branch the defaulted parameters ride —
// only an undefined leaf takes the default. Rests and computed keys
// decline to the routes after.
//
// A NESTED-ROOT element (`const { parentViewBox: alias } = options;`
// where `options.parentViewBox` was never a slot on its own — only
// `options.parentViewBox.x` etc. are, hasNestedFamilySlot's own tell)
// takes an explicit UNKNOWN assign instead of declining the whole
// statement: destructuresOnlyDeclaredMembers (ir_summary_record_parameter_uses.go)
// only ever admits such an element bare, no default — this route still
// declines a defaulted one (no source slot exists to test eqUndef
// against), matching that gate. The caller (appendRecordParameterEntries)
// havocs every leaf under the root separately; this statement's own job
// is only to give the bound local an honest value, the same explicit
// `assign target unknown` FunctionValuedDeclarationOf and
// havocAssignments already use for "no statement here believes this
// slot's old value."
func DestructuringWithDefaultsOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	decl := declarations[0].AsVariableDeclaration()
	if decl.Initializer == nil || !ast.IsObjectBindingPattern(decl.Name()) {
		return nil, false
	}
	initializer := Unwrapped(decl.Initializer)
	var holder string
	switch {
	case ast.IsIdentifier(initializer):
		holder = initializer.Text()
	case initializer.Kind == ast.KindThisKeyword:
		holder = "this"
	default:
		return nil, false
	}
	var out []kernelbridge.IrStatement
	sawDefault := false
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			read = binding.PropertyName.Text()
		}
		target, targetOk := slotIndexOfName(context, binding.Name().Text())
		if !targetOk {
			return nil, false
		}
		source, sourceOk := slotIndexOfName(context, holder+"."+read)
		if !sourceOk {
			// a NESTED-ROOT key: no depth-1 slot, but its own children
			// exist below it — a defaulted element has no source to test
			// eqUndef against, so it still declines the whole statement
			if binding.Initializer != nil || !hasNestedFamilySlot(context, holder+"."+read) {
				return nil, false
			}
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: unknownEffect,
			})
			continue
		}
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: target,
			Effect: varStateEffect(source),
		})
		if binding.Initializer == nil {
			continue
		}
		sawDefault = true
		if !writeAndCallFree(binding.Initializer) {
			return nil, false
		}
		defaultEffect, lowered := RhsEffect(context, context.Sorts[target], binding.Initializer)
		if !lowered {
			return nil, false
		}
		// a destructuring default fires on exactly UNDEFINED, never on
		// null (sec-destructuring-binding-patterns: KeyedBindingInitialization
		// applies the Initializer only "if v is undefined") — so the
		// branch tests eqUndef with the default on the true arm, not
		// definedness with the default on the absent arm, which would
		// wrongly default a null value away
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   target,
			Test: kernelbridge.IrTestEqUndef,
			Then: []kernelbridge.IrStatement{{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: asVarStateEffect(defaultEffect),
			}},
		})
	}
	// with no default present the plain route already served — this one
	// only exists for the defaulted shape
	if !sawDefault || len(out) == 0 {
		return nil, false
	}
	return out, true
}

// DestructuringAssignmentsOf is the destructuring lowering: `const { x,
// y } = p` where p is a flattened record becomes one assignment per
// bound name, each reading its leaf's slot. A NESTED-ROOT element (no
// slot at its own key, only below it — hasNestedFamilySlot) takes an
// explicit unknown assign instead, the same DestructuringWithDefaultsOf
// takes for its own bare nested-root arm — this route never sees a
// defaulted element at all (refused outright below), so every
// nested-root element reaching here is already the bare shape that
// arm's own gate admits. Declines unless every OTHER bound name has a
// slot AND names a one-step leaf — a nested pattern or a default reads
// shapes the flattening does not spell.
func DestructuringAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	decl := declarations[0].AsVariableDeclaration()
	if decl.Initializer == nil || !ast.IsObjectBindingPattern(decl.Name()) {
		return nil, false
	}
	initializer := Unwrapped(decl.Initializer)
	var holder string
	switch {
	case ast.IsIdentifier(initializer):
		holder = initializer.Text()
	case initializer.Kind == ast.KindThisKeyword:
		// `const { count } = this` — the method's own bundle spells its
		// fields "this.<name>", so the same leaf read serves
		holder = "this"
	default:
		return nil, false
	}
	var out []AssignmentTarget
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			return nil, false
		}
		if !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			read = binding.PropertyName.Text()
		}
		target, targetOk := slotIndexOfName(context, binding.Name().Text())
		if !targetOk {
			return nil, false
		}
		source, sourceOk := slotIndexOfName(context, holder+"."+read)
		if !sourceOk {
			if !hasNestedFamilySlot(context, holder+"."+read) {
				return nil, false
			}
			out = append(out, AssignmentTarget{Target: target, Effect: unknownEffect})
			continue
		}
		out = append(out, AssignmentTarget{Target: target, Effect: varStateEffect(source)})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
