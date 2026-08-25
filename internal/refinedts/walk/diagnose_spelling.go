// Shared spelling helpers for the diagnose channel: every walk-layer
// log line that carries a DeclaredRefinement or an AbstractValue wants
// a short human string, not the struct itself. Both helpers are cheap
// wrappers around formatters the package already has (declared_value.go's
// AbstractValueOfDeclared, abstractdomain's FormatAbstractValueInline)
// and are only ever called from inside a diagnose.EventOn(event) guard
// for that call site's own event, so their cost never lands on a run
// that has diagnosis off, or a filtered run that is not asking for
// this event.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// spellDeclared renders a *DeclaredRefinement the way a hover would —
// "absent" for a nil pointer (no statement at this position at all),
// otherwise the inline AbstractValue spelling, or "unspellable" where
// the formatter itself declines.
func spellDeclared(stated *annotations.DeclaredRefinement) string {
	if stated == nil {
		return "absent"
	}
	spelling, ok := abstractdomain.FormatAbstractValueInline(AbstractValueOfDeclared(*stated))
	if !ok {
		return "unspellable"
	}
	return spelling
}

// spellValue renders an AbstractValue the same inline way, for a
// flowing value rather than a declared statement.
func spellValue(known abstractdomain.AbstractValue) string {
	spelling, ok := abstractdomain.FormatAbstractValueInline(known)
	if !ok {
		return "unspellable"
	}
	return spelling
}

// functionLabel names a function-like declaration for a log line: its
// own written name where the syntax carries one (a declaration, a
// method), the enclosing binding's name for an anonymous arrow/
// function expression assigned to a name, or a position label
// otherwise — never empty, since an empty function name in a diverging
// run's log is indistinguishable from a missing field.
func functionLabel(declaration *ast.Node) string {
	if declaration == nil {
		return "<nil>"
	}
	if name := declaration.Name(); name != nil {
		if ast.IsIdentifier(name) {
			return name.Text()
		}
	}
	if parent := declaration.Parent; parent != nil {
		if ast.IsVariableDeclaration(parent) {
			if name := parent.AsVariableDeclaration().Name(); name != nil && ast.IsIdentifier(name) {
				return name.Text()
			}
		}
		if ast.IsPropertyAssignment(parent) {
			if name := parent.AsPropertyAssignment().Name(); name != nil && ast.IsIdentifier(name) {
				return name.Text()
			}
		}
		if ast.IsPropertyDeclaration(parent) {
			if name := parent.AsPropertyDeclaration().Name(); name != nil && ast.IsIdentifier(name) {
				return name.Text()
			}
		}
	}
	return "<anonymous@" + declaration.Kind.String() + ">"
}
