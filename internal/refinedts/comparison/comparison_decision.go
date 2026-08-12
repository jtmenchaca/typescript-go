// One comparison, decided. Every finite row is a MEMBERSHIP question
// to the kernel — `a < b` asks whether a is in the set below b — so
// the answer is proved rather than computed here. The rows the kernel
// cannot be asked are transcribed from the specification and marked
// as such: NaN fails every comparison and passes every disequality,
// absence compares only under `==`, and infinite words order by the
// extended reals rather than by float arithmetic.
//
// BLOCKED: the TS twin (comparison_decision.ts) is one function,
// compareKnown(ctx, op, strict, a, b), whose entire body is written
// against types this directory does not own and that are not yet
// ported to Go:
//
//   - AbstractValue, knownValues (abstract_domain/abstract_value.ts)
//   - trustLevelOf, minTrustLevel (abstract_domain/trust_grades.ts)
//   - setOfKnown (abstract_domain/lattice_operations.ts)
//   - FlowContext, specifically ctx.kernel (evaluation/flow_context.ts,
//     which itself pulls in kernel_bridge, service/program_host,
//     annotations, dataflow_facts, interprocedural, assignability)
//   - residue (silence/unknown.ts)
//
// Every branch of compareKnown reads or returns an AbstractValue and
// calls ctx.kernel.member — there is no fragment of the algorithm that
// stands independent of those types. Per PORT.md, the independent-parts
// rule does not apply when there are none: this file intentionally
// carries no Go declarations yet rather than inventing placeholder
// types for AbstractValue/FlowContext ahead of the abstract_domain and
// kernel_bridge ports landing (both in flight concurrently per
// go-port-tracker.md). Port compareKnown here once
// internal/refinedts/abstractdomain exports AbstractValue/knownValues/
// trustLevelOf/minTrustLevel/setOfKnown and a kernel-bearing context
// (or the checker-direct substitute PORT.md's FlowContext-bypass
// convention calls for) exists to type ctx against.
//
// The refinement-set side this file needs is already ported and
// ready to call once the above lands:
//   refinementsets.Above / .AtLeast / .AtMost / .Below / .OneOf /
//   .MakeRefinedSet (refinement_forms.go) stand in for the TS file's
//   above/atLeast/atMost/below/oneOf/refinedSet imports.
package comparison
