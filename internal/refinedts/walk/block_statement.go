// from control_flow/block_statement.ts
//
// Walk a block: restore block-scoped shadows at exit, restore those
// names on break/throw snapshots, and havoc writes captured by a
// `using` disposer.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// blockScopedNames is blockScopedNames in the TS source: the names a
// block DECLARES with block scope — its direct let/const
// declarations (patterns included), classes, and function
// declarations. `var` is excluded: it hoists to the function, so a
// block-exit restore would erase a real write.
func blockScopedNames(block *ast.Node) map[string]struct{} {
	names := map[string]struct{}{}
	var bindingNames func(name *ast.Node)
	bindingNames = func(name *ast.Node) {
		// The TS source's parameter is typed ts.BindingName, never
		// absent; tsgo's *BindingElement.name field CAN be nil on a
		// parser-error-recovered node — see dataflowfacts/
		// syntactic_facts.go's bindingNames, the same duplicated
		// helper's other copy, for the full note.
		if name == nil {
			return
		}
		if ast.IsIdentifier(name) {
			names[name.Text()] = struct{}{}
			return
		}
		if ast.IsArrayBindingPattern(name) || ast.IsObjectBindingPattern(name) {
			for _, element := range name.AsBindingPattern().Elements.Nodes {
				if ast.IsOmittedExpression(element) {
					continue
				}
				bindingNames(element.Name())
			}
		}
	}
	for _, s := range block.AsBlock().Statements.Nodes {
		if ast.IsVariableStatement(s) {
			declList := s.AsVariableStatement().DeclarationList
			flags := declList.Flags
			if (flags & (ast.NodeFlagsLet | ast.NodeFlagsConst)) == 0 {
				continue
			}
			for _, d := range declList.AsVariableDeclarationList().Declarations.Nodes {
				bindingNames(d.AsVariableDeclaration().Name())
			}
		}
		if (ast.IsFunctionDeclaration(s) || ast.IsClassDeclaration(s)) && s.Name() != nil {
			names[s.Name().Text()] = struct{}{}
		}
	}
	return names
}

// AnalyzeBlockStatement is analyzeBlockStatement in the TS source.
func AnalyzeBlockStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	block := statement.AsBlock()
	// the block's own let/const/class/function names are
	// BLOCK-scoped: whatever they shadow comes back when the block
	// ends. Only the block's DIRECT declarations restore — nested
	// blocks handle their own, and `var` hoists to the function, so
	// its writes survive.
	scoped := blockScopedNames(statement)
	shadowSaved := map[string]abstractdomain.AbstractValue{}
	shadowSavedHas := map[string]bool{}
	for name := range scoped {
		if held, ok := env.Get(name); ok {
			shadowSaved[name] = held
			shadowSavedHas[name] = true
		} else {
			shadowSavedHas[name] = false
		}
	}
	breakMark := 0
	if ctx.BreakSink != nil {
		breakMark = len(*ctx.BreakSink)
	}
	throwMark := 0
	if ctx.ThrowSink != nil {
		throwMark = len(*ctx.ThrowSink)
	}
	exits := AnalyzeStatements(ctx, env, block.Statements.Nodes, result)
	// only a name that SHADOWS an outer binding restores — a block
	// local with no outer namesake keeps its exit knowledge (there
	// is nothing to corrupt, a post-block read is tsc's own error,
	// and the hover ruling shows a counter's exit value at its head)
	restoreInto := func(target Env) {
		for name, held := range shadowSaved {
			if shadowSavedHas[name] {
				target.Set(name, held)
			}
		}
	}
	restoreInto(env)
	// a break or throw SNAPSHOT taken inside the block carries the
	// block's own names — the join that consumes it (after the
	// switch, in the catch) must see the OUTER bindings, so the
	// restore reaches those snapshots too
	if len(shadowSaved) > 0 {
		if ctx.BreakSink != nil {
			for i := breakMark; i < len(*ctx.BreakSink); i++ {
				restoreInto((*ctx.BreakSink)[i])
			}
		}
		if ctx.ThrowSink != nil {
			for i := throwMark; i < len(*ctx.ThrowSink); i++ {
				restoreInto((*ctx.ThrowSink)[i])
			}
		}
	}
	// a `using` declaration runs [Symbol.dispose]() at this block's
	// exit — a call the source never spells. A disposer built in view
	// can capture and write the names its initializer's subtree
	// assigns, so those names forget here; one built outside the file
	// cannot capture this file's names at all.
	for _, inner := range block.Statements.Nodes {
		if !ast.IsVariableStatement(inner) {
			continue
		}
		declList := inner.AsVariableStatement().DeclarationList
		if (declList.Flags & ast.NodeFlagsUsing) == 0 {
			continue
		}
		written := map[string]struct{}{}
		for _, declaration := range declList.AsVariableDeclarationList().Declarations.Nodes {
			decl := declaration.AsVariableDeclaration()
			if decl.Initializer != nil {
				AssignedNames(nil, decl.Initializer, written)
			}
		}
		for name := range written {
			if _, ok := env.Get(name); ok {
				HavocEnv(ctx.Aliases, env, name)
			}
		}
	}
	return exits
}
