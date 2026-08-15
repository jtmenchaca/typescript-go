// split from ir_object_slots.go — the unspelled-source pattern lowering

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// PatternAssignmentsOf is the destructuring lowering for every SOURCE
// the leaf-exact route above does not read: `const { a } = call()`,
// `const { b } = holder.path`, `const [x, y] = xs`. The bound values
// have no spelling — so every bound name that has a slot takes UNKNOWN,
// which is exactly what is true of it — and the source's own effects
// are carried: a call source lowers through the call machinery (a
// compiled callee splices, an opaque one havocs and names itself), and
// any other source must move nothing. The statement is then READ: the
// names were never knowable, and knowing that is not a hole.
//
// The pattern's own defaults and computed keys must also move nothing —
// a default expression with a call inside would run code the unknown
// assignment does not spell.
func PatternAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	decl := declarations[0].AsVariableDeclaration()
	name := decl.Name()
	if decl.Initializer == nil || name == nil ||
		(!ast.IsObjectBindingPattern(name) && !ast.IsArrayBindingPattern(name)) {
		return nil, false
	}
	if !patternMovesNothing(name) {
		return nil, false
	}
	source := Unwrapped(decl.Initializer)
	var out []kernelbridge.IrStatement
	if ast.IsCallExpression(source) {
		called, ok := SummaryCallOrHavoc(context, source, -1)
		if !ok {
			return nil, false
		}
		out = append(out, called...)
	} else if !writeAndCallFree(source) {
		return nil, false
	}
	// an ARRAY pattern from a FLATTENED array local reads each element
	// as element-or-undefined — the elem slot's join wrapped orAbsent —
	// instead of unknown: nothing bounds WHICH element each name took,
	// but every element is inside the elem join, and a short array
	// leaves undefined, which orAbsent spells exactly.
	if ast.IsArrayBindingPattern(name) && ast.IsIdentifier(source) {
		if _, elemSlot, slotsOk := arraySlotsOf(context, source.Text()); slotsOk {
			handled := true
			var precise []kernelbridge.IrStatement
			for _, element := range name.AsBindingPattern().Elements.Nodes {
				if element == nil || !ast.IsBindingElement(element) {
					continue
				}
				binding := element.AsBindingElement()
				bound := binding.Name()
				if binding.DotDotDotToken != nil || binding.Initializer != nil ||
					bound == nil || !ast.IsIdentifier(bound) {
					handled = false
					break
				}
				slot, has := slotIndexOfName(context, bound.Text())
				if !has {
					continue
				}
				elemVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: elemSlot}
				precise = append(precise, kernelbridge.IrStatement{
					Kind:   kernelbridge.IrStatementAssign,
					Target: slot,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elemVar},
				})
			}
			if handled {
				return append(out, precise...), true
			}
		}
	}
	for _, bound := range boundPatternNames(name) {
		slot, has := slotIndexOfName(context, bound)
		if !has {
			// an untracked name holds no slot and no belief — nothing to
			// assign
			continue
		}
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
	}
	return out, true
}

// patternMovesNothing answers whether binding the pattern can run any
// code: every default value and every computed key must be write- and
// call-free, at every depth.
func patternMovesNothing(pattern *ast.Node) bool {
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element == nil || !ast.IsBindingElement(element) {
			continue
		}
		binding := element.AsBindingElement()
		if binding.Initializer != nil && !writeAndCallFree(binding.Initializer) {
			return false
		}
		if binding.PropertyName != nil && ast.IsComputedPropertyName(binding.PropertyName) {
			if !writeAndCallFree(binding.PropertyName.AsComputedPropertyName().Expression) {
				return false
			}
		}
		if name := binding.Name(); name != nil &&
			(ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name)) {
			if !patternMovesNothing(name) {
				return false
			}
		}
	}
	return true
}

// boundPatternNames collects every identifier a pattern binds, at every
// depth — the names whose slots the unknown assignments cover.
func boundPatternNames(pattern *ast.Node) []string {
	var names []string
	var collect func(p *ast.Node)
	collect = func(p *ast.Node) {
		for _, element := range p.AsBindingPattern().Elements.Nodes {
			if element == nil || !ast.IsBindingElement(element) {
				continue
			}
			name := element.AsBindingElement().Name()
			if name == nil {
				continue
			}
			if ast.IsIdentifier(name) {
				names = append(names, name.Text())
				continue
			}
			if ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name) {
				collect(name)
			}
		}
	}
	collect(pattern)
	return names
}
