// split from ir_loop.go — the for-of over a flattened Map or Set,
// lowered as an ordinary loop

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MapForOfLowering is a for-of over a flattened Map or Set, lowered as
// the ordinary loop exactly the way ArrayForOfLowering lowers an array's
// — the per-pass binding effects are slot vars, the rest of the body
// folds through FoldBody, and NO numeric head bounds the trip count (the
// collection's size is not related to any binding by the flattening), so
// every cond and after entry stays nil and the solver certifies whatever
// the body's effects support.
//
// The recognized heads:
//
//   - `for (const v of s)` and `for (const v of m.values())` — the
//     binding takes the value slot's var each pass.
//   - `for (const k of m.keys())` — the key slot's var.
//   - `for (const [k, v] of m)` and `of m.entries()` — an array pattern
//     of EXACTLY two plain identifiers, k taking the key slot and v the
//     value slot.
//
// Declines where the binding is not one of those shapes, where the
// collection is not flattened, or where the body leaves the fold's
// grammar.
func MapForOfLowering(context *LoweringContext, statement *ast.Node) (kernelbridge.IrStatement, bool) {
	if !ast.IsForOfStatement(statement) {
		return kernelbridge.IrStatement{}, false
	}
	forOf := statement.AsForInOrOfStatement()
	// `for await (… of …)` awaits each element — the value the binding
	// takes is the awaited one, which the value slot does not hold
	if forOf.AwaitModifier != nil {
		return kernelbridge.IrStatement{}, false
	}
	if forOf.Initializer == nil || !ast.IsVariableDeclarationList(forOf.Initializer) {
		return kernelbridge.IrStatement{}, false
	}
	declarations := forOf.Initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return kernelbridge.IrStatement{}, false
	}
	bindingName := declarations[0].AsVariableDeclaration().Name()
	// the fold starts every binding at its own var; the loop's own
	// bindings then take their per-pass slot reads
	current := make([]kernelbridge.LoopEffect, len(context.Bindings))
	for index := range current {
		current[index] = varEffect(index)
	}
	// per-pass writes, collected first so the two shapes share one path
	type perPass struct{ target, source int }
	var passes []perPass
	if ast.IsIdentifier(bindingName) {
		slot, pairIterated, ok := MapIterationSlotOf(context, forOf.Expression)
		if !ok || pairIterated {
			return kernelbridge.IrStatement{}, false
		}
		target, found := slotIndexOfName(context, bindingName.Text())
		if !found {
			return kernelbridge.IrStatement{}, false
		}
		passes = append(passes, perPass{target: target, source: slot})
	} else if ast.IsArrayBindingPattern(bindingName) {
		keysSlot, valsSlot, ok := MapEntrySlotsOf(context, forOf.Expression)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		elements := bindingName.AsBindingPattern().Elements.Nodes
		if len(elements) != 2 {
			return kernelbridge.IrStatement{}, false
		}
		sources := []int{keysSlot, valsSlot}
		for index, element := range elements {
			binding := element.AsBindingElement()
			if binding.DotDotDotToken != nil || binding.Initializer != nil ||
				binding.PropertyName != nil || !ast.IsIdentifier(binding.Name()) {
				return kernelbridge.IrStatement{}, false
			}
			target, found := slotIndexOfName(context, binding.Name().Text())
			if !found {
				return kernelbridge.IrStatement{}, false
			}
			passes = append(passes, perPass{target: target, source: sources[index]})
		}
	} else {
		return kernelbridge.IrStatement{}, false
	}
	for _, pass := range passes {
		if pass.target >= len(current) || pass.source >= len(current) {
			return kernelbridge.IrStatement{}, false
		}
		current[pass.target] = varEffect(pass.source)
	}
	if !FoldBody(context, StatementsOf(forOf.Statement), current) {
		return kernelbridge.IrStatement{}, false
	}
	written := make([]bool, len(current))
	for index, effect := range current {
		written[index] = !(effect.Kind == kernelbridge.LoopEffectVar && effect.Index == index)
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: written,
		Cond:    make([]*refinementsets.RefinedSet, len(current)),
		After:   make([]*refinementsets.RefinedSet, len(current)),
		Body:    current,
	}, true
}
