// from interprocedural/callback_walk.ts
//
// Shared state for one callbackOutcome walk: the bound parameters,
// body evaluators, and forget/finish seams the method cases share.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// CallbackWalk is the shared state for one callbackOutcome walk: the
// bound parameters, body evaluators, and forget/finish seams the
// method cases share.
type CallbackWalk struct {
	Ctx                 *FlowContext
	Env                 Env
	Receiver            abstractdomain.AbstractValue
	Method              string
	Call                *ast.Node // CallExpression
	Arrow               Callback
	TrackedName         string // "" means null (TS `string | null`)
	HasTrackedName      bool
	Analyzers           LoopAnalyzers
	Body                *ast.Node // ConciseBody
	Silent              *FlowContext
	PreboundBindings    map[string]abstractdomain.AbstractValue
	Element             abstractdomain.AbstractValue
	ElementParameter    string // "" means null
	HasElementParameter bool
	OwnerParameter      string // "" means null
	HasOwnerParameter   bool
	ParameterAt         func(index int) *ast.Node // ParameterDeclaration, nil when absent
	NameAt              func(index int) (string, bool)
	EvalBody            func(reporting *FlowContext, bindings map[string]abstractdomain.AbstractValue) abstractdomain.AbstractValue
	ReportPins          func(accumulator *abstractdomain.AbstractValue) map[string]abstractdomain.AbstractValue
	ForgetWrites        func()
	Finish              func(result abstractdomain.AbstractValue) abstractdomain.AbstractValue
}
