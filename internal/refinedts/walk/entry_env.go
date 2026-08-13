// from control_flow/entry_env.ts
//
// What a function body knows at entry: stated annotations, then
// call-site joins, then the plain type (or opaque / residue). Judgment
// and hover both bind through this one function, so the two walks
// cannot disagree about a parameter.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// InitialStateOfPlainParameter is initialStateOfPlainParameter in the
// TS source: the set a PLAIN type states at a parameter — syntax
// then host. Exported parameters whose type states nothing are
// opaque: callers live outside this file.
func InitialStateOfPlainParameter(p *program.CheckerProgram, parameter *ast.Node) abstractdomain.AbstractValue {
	unread := func() abstractdomain.AbstractValue {
		if ExportedFunctionParameter(parameter) {
			return abstractdomain.Opaque
		}
		return silence.Residue()
	}
	decl := parameter.AsParameterDeclaration()
	if !ast.IsIdentifier(decl.Name()) {
		return unread()
	}
	if decl.Type != nil {
		if held, ok := typereading.ReadDeclaredType(p.Checker, decl.Type, decl.Name()); ok {
			return held
		}
		return unread()
	}
	if held, ok := typereading.ReadHostType(p.Checker, p.Checker.GetTypeAtLocation(decl.Name()), decl.Name(), 0); ok {
		return held
	}
	return unread()
}

// BindEntryEnvInput mirrors the destructured parameter of
// bindEntryEnv in the TS source.
type BindEntryEnvInput struct {
	P                     *program.CheckerProgram
	Env                   Env
	Parameters            []*ast.Node                             // ParameterDeclaration
	StatedParams          []*annotations.DeclaredRefinement       // nil entries allowed; nil slice means "no stated params"
	CallSiteInitialStates map[string]abstractdomain.AbstractValue // nil means "no call-site join"
	OnStated              func(name string, stated *annotations.DeclaredRefinement)
}

// BindEntryEnv is bindEntryEnv in the TS source: bind every
// parameter name into env. Stated outranks a call-site join; a join
// outranks a value already on the env (an enclosing callback pin);
// silence fills from the plain type. Destructuring reads stated or
// plain — the same source the body walk always used.
//
// CROSS-DIRECTORY: readDestructuring is bindings/destructuring.ts's
// readDestructuring — not yet ported (bindings/ is a concurrent
// wave-2 agent's file, joining this same package). Called here as a
// plain package-level function; the walk will not build until that
// agent lands it. abstractValueOfDeclared is
// assignability/declared_value.ts's function, likewise concurrent
// (assignability's FlowContext-reading files join this package per
// PORT.md) — called the same way.
func BindEntryEnv(input BindEntryEnvInput) {
	for i, parameter := range input.Parameters {
		var stated *annotations.DeclaredRefinement
		if input.StatedParams != nil && i < len(input.StatedParams) {
			stated = input.StatedParams[i]
		}
		decl := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(decl.Name()) {
			var source abstractdomain.AbstractValue
			if stated != nil {
				source = AbstractValueOfDeclared(*stated)
			} else {
				source = InitialStateOfPlainParameter(input.P, parameter)
			}
			ReadDestructuring(decl.Name(), source, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
				input.Env.Set(name, silence.SeededBinding(input.P.Checker, held, at))
			})
			continue
		}
		name := decl.Name().Text()
		if stated != nil {
			input.Env.Set(name, AbstractValueOfDeclared(*stated))
			if input.OnStated != nil {
				input.OnStated(name, stated)
			}
			continue
		}
		if input.CallSiteInitialStates != nil {
			if fromCall, ok := input.CallSiteInitialStates[name]; ok {
				input.Env.Set(name, fromCall)
				continue
			}
		}
		if held, ok := input.Env.Get(name); ok && held.Kind != abstractdomain.KindUnknown {
			continue
		}
		input.Env.Set(name, InitialStateOfPlainParameter(input.P, parameter))
	}
}
