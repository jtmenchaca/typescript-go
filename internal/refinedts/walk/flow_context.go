// from evaluation/flow_context.ts
//
// The state a check runs under: the environment a walk carries, and
// the context every expression, statement, and call shares. Types
// only — no evaluation.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// Callback is a function value handed as a callback: an inline
// arrow, or a NAME resolving to a const-bound arrow / function
// expression / function declaration — `xs.map(double)` reads the
// same as the inline form. (TS: ArrowFunction | FunctionExpression |
// FunctionDeclaration; the Go node is any of those three kinds.)
//
// Originally a type-only hub in its own interprocedural package
// (interprocedural/callback_models.go, from
// interprocedural/callback_models.ts); that package held nothing but
// this alias once callback_models.ts's own functions landed in walk,
// so it moved here per PORT.md's walk-package split and the package
// was deleted (walk-integration-punchlist.md items 3 and 9).
type Callback = *ast.Node

// FunctionContract is the declared shape of a function: its
// parameters and result, each an optional DeclaredRefinement, plus
// whether any position is actually grounded by a stated annotation.
type FunctionContract struct {
	Declaration *ast.Node // FunctionDeclaration | ArrowFunction | FunctionExpression | MethodDeclaration
	Params      []*annotations.DeclaredRefinement
	Result      *annotations.DeclaredRefinement
	// Grounded is true when some position or bound came from a stated
	// annotation. An ungrounded signature (plain TS, pure generics)
	// still walks and still instantiates at calls, but its OWN
	// positions checkAssignability nothing — plain TypeScript stays
	// untouched.
	Grounded bool
}

// GateAssumption is what a correlation pass assumes: the canonical
// gate, and which way its ToBoolean fell in this pass.
//
// (TS home: dataflow_facts/path_conditions.ts. That file's own port
// left GateAssumption undefined — gateKeyOf, assumedVerdict,
// gatesTestedBy, and correlationGateOf are BLOCKED on
// narrowing/condition_tree.ts's conditionTreeOf, per
// dataflowfacts/path_conditions.go's banner, but the GateAssumption
// SHAPE itself carries no such dependency and FlowContext needs it
// now. Defined here rather than in dataflowfacts to avoid reaching
// into a directory outside this port unit; move it to dataflowfacts
// when that file's functions are unblocked.)
type GateAssumption struct {
	Base   *ast.Symbol
	Detail string
	Truthy bool
}

// FlowContext is the state a check runs under: the kernel handle,
// the diagnostic report sink, declared statements, program facts —
// the walk's context. Its TS `p: CheckerProgram` field's host
// question was `CheckerHost`; the port replaces it with
// `*program.CheckerProgram`, whose own Checker field is already a
// direct `*checker.Checker`.
type FlowContext struct {
	P      *program.CheckerProgram
	Kernel *kernelbridge.RefinedTSKernel
	// Registry is `z.*` chain statements, by symbol.
	Registry annotations.AnnotationRegistry
	// Contracts is FunctionContract by declared symbol.
	Contracts map[*ast.Symbol]*FunctionContract
	// Report is the diagnostic sink.
	Report  func(d assignability.RefinementDiagnostic)
	Aliases *dataflowfacts.AliasClasses
	// Objects: the object annotations, by symbol — `z.object`
	// statements.
	Objects annotations.ObjectRegistry
	// Declared: the declared invariants — an annotation-typed
	// binding's stated set. Every write judges against it, so it
	// holds at all times — which is exactly what lets a loop skip
	// widening for it.
	Declared map[string]*annotations.DeclaredRefinement
	// ReturnSink: when non-nil, a return statement's value is
	// COLLECTED here instead of ending a contract walk — the seam
	// callbacks and inlined closures read their block bodies through.
	ReturnSink *[]abstractdomain.AbstractValue
	// Inlining: the closures currently being inlined — re-entry is
	// recursion, and a recursive inline answers unknown rather than
	// diverging.
	Inlining map[*ast.Symbol]struct{}
	// ThrowSink: when non-nil, a walked `throw` records a snapshot of
	// its environment here — the states an exception can carry out to
	// a caller's catch.
	ThrowSink *[]Env
	// SnapshotOwner: the declaration whose DEDICATED walk this is
	// (pass 3's own), whose lexically-contained call sites record
	// their environments as read-once snapshots (call_site_snapshots.ts).
	// Inline and callback walks evaluate calls of OTHER functions, so
	// the owner gate keeps their states out without unsetting this.
	SnapshotOwner *ast.Node
	// CallableParams: function-literal arguments of the call being
	// inlined, by the callee's parameter name — a call through the
	// parameter runs the very callback the caller handed over.
	CallableParams map[string]Callback
	// LabelSinks: open label targets. A `break label` records its
	// environment here, and the labeled statement joins those states
	// back in at its end — the rejoin point.
	LabelSinks map[string]*[]Env
	// BreakSink: the switch a bare `break` leaves. A break nested
	// inside a clause — in a block, an `if`, a `try` — records its
	// environment here, so the switch can rejoin those states instead
	// of reading the break as control leaving the enclosing statement
	// list. A loop body clears it: a break there belongs to the loop.
	BreakSink *[]Env
	// ContinueSink: where a bare `continue` RECORDS its state: control
	// re-enters the next iteration carrying everything the branch
	// wrote, so the loop's body effect joins these into its exit —
	// without this, a push-then-continue's write never reached the
	// fixpoint (prisma's diagnostics accumulator froze at []). A loop
	// entry clears it: an inner loop's continue belongs to the inner.
	ContinueSink *[]Env
	// ThisWriteSink: when non-nil, every `this.key = value` write
	// records its value here — the field-invariant collection walk
	// (fields.ts). Reads of `this.key` answer nothing while it is
	// set, so no invariant rests on itself.
	ThisWriteSink map[string][]abstractdomain.AbstractValue
	// DifferenceConstraints: strict order rows the dominating guards
	// vouch: on every run reaching this code, minuend's value −
	// subtrahend's value ≥ bound (recorded only over bindings the
	// function never writes; relations.ts), consumed by the ordered
	// subtraction.
	DifferenceConstraints []dataflowfacts.DifferenceConstraint
	// SumConstraints: sum rows the dominating guards vouch: what a
	// comparison whose one side is a two-place sum said about the
	// COMPUTED sum — consumed by the element read, which supplies the
	// nonneg-integer exactness argument (relations.ts).
	SumConstraints []dataflowfacts.SumConstraint
	// GateAssumptions: the correlation passes this walk runs under:
	// every condition testing one of these stable gates is DECIDED
	// the assumed way (through its negation parity). analyzeStatements
	// pushes one assumption per split — at most two deep — and each
	// level's passes partition every run, so the joins stay exact and
	// the correlations between the branches survive.
	GateAssumptions []GateAssumption
	// CallSiteSeeded: this body's unstated parameters wear the join
	// of what the CURRENT call sites pass. A condition folding false
	// under that seeding is false for today's callers, not for every
	// admissible input — so the dead-guard report stays quiet here.
	CallSiteSeeded bool
}

// Env is the environment: a binding's name to its AbstractValue.
type Env = map[string]abstractdomain.AbstractValue
