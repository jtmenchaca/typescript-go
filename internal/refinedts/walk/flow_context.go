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
	// Yield: the stated yield position of a GENERATOR declaration —
	// the Y of a written `Generator<Y, R, N>`, what every `yield e`
	// in the body hands the caller (Result holds R, the return
	// statement's own position). Nil for every non-generator and
	// wherever Y states nothing.
	Yield *annotations.DeclaredRefinement
	// YieldResume: the stated resume position of a GENERATOR
	// declaration — the N of a written `Generator<Y, R, N>`, what the
	// yield EXPRESSION ITSELF reads as (what the caller's next(v)
	// sends back). Nil for every non-generator and wherever N states
	// nothing.
	YieldResume *annotations.DeclaredRefinement
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
	// YieldStated: the enclosing generator body's stated yield
	// position — what every `yield e` judges its operand against
	// (yield_contract.go). analyzeFunctionBody sets it per body, so
	// it is nil outside a grounded generator's own walk.
	YieldStated *annotations.DeclaredRefinement
	// YieldResumeStated: the enclosing generator body's stated resume
	// position — what a `yield e` EXPRESSION reads as its own value
	// (yield_contract.go). Set per body alongside YieldStated, nil
	// outside a grounded generator's own walk or wherever N states
	// nothing.
	YieldResumeStated *annotations.DeclaredRefinement
	// Inlining: the closures currently being inlined — re-entry is
	// recursion, and a recursive inline answers unknown rather than
	// diverging.
	Inlining map[*ast.Symbol]struct{}
	// ResolverTargets: a `Promise.withResolvers()` destructured
	// resolve/reject binding's declared symbol maps to the NAME its
	// paired promise binding is tracked under (promise_with_resolvers.go).
	// A later `resolve(arg)`/`reject()` call on the SAME symbol writes
	// the settled value into that name's env slot through
	// UpdateTrackedEnv — the two bindings come from one destructuring
	// statement, so the pairing is fixed at bind time, not discovered
	// by scanning ahead.
	ResolverTargets map[*ast.Symbol]string
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
	// ThisOwnerDeclaration: the declaration node this walk bound "this"
	// FOR, when that binding cannot be proven from `site`'s own static
	// position alone (method_this_writes.go's property-alias route: a
	// plain FunctionDeclaration/FunctionExpression reached through an
	// object-literal property alias, `{ bump: helperFn }` — the SAME
	// declaration also serves a bare `helperFn()` call elsewhere, so no
	// syntactic climb can tell the two apart the way
	// EnclosingThisObjectLiteralMethod's parent-is-a-literal check does
	// for a directly-written method). evaluate_expression.go's
	// KindThisKeyword arm trusts env's own "this" binding only when
	// dataflowfacts.EnclosingThisOwner(site) is IDENTICAL to this field
	// — the nearest function/method that owns `site`'s `this` must be
	// the exact declaration this walk bound, not a more deeply nested
	// sibling function with its own unrelated dynamic receiver. Nil
	// everywhere else, including the class and direct-object-literal-
	// method routes, which stay on their existing syntactic recognizers
	// (EnclosingThisClass, EnclosingThisObjectLiteralMethod) and never
	// set this field.
	ThisOwnerDeclaration *ast.Node
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
