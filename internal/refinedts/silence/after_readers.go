// Unknown after every reader that could have answered was asked.
// Walk knowledge outranks the type; the seed fills silence only.
// Cuts and opaque values do not seed (PV2-020/021, PV2-010..012).
//
// BLOCKED: the TS twin (after_readers.ts) is two functions —
// afterReaders(held, p, at, role) and seededBinding(p, held, at) —
// whose bodies are written against types this directory does not own
// and that are not yet ported to Go:
//
//   - AbstractValue, OPAQUE (abstract_domain/abstract_value.ts)
//   - CheckerProgram, specifically p.host.getTypeAtLocation
//     (service/program_host.ts) — PORT.md retires the CheckerProgram/
//     CheckerHost adapter layer itself (ported modules type directly
//     against *checker.Checker), but the replacement surface for
//     "the checker plus the program's entry node" that this file's
//     ts.Node walk needs is not yet established by any landed port
//   - arrivedUnchecked (type_reading/gates.ts)
//   - readHostType (type_reading/read_type.ts)
//
// Every branch of afterReaders reads or returns an AbstractValue and
// calls into type_reading; seededBinding is a thin call to
// afterReaders. There is no fragment of either function that stands
// independent of those types. Per PORT.md, the independent-parts rule
// does not apply when there are none: this file intentionally carries
// no Go declarations yet rather than inventing placeholder types for
// AbstractValue or the checker-program surface ahead of the
// abstract_domain and type_reading ports landing (neither is in
// flight yet per go-port-tracker.md, which lists silence itself as
// depending on nothing further down that list — the tracker's own
// dependency order has type_reading landing before silence).
//
// Port afterReaders and seededBinding here once
// internal/refinedts/abstractdomain exports AbstractValue/OPAQUE and
// a type_reading package exports arrivedUnchecked/readHostType typed
// against *checker.Checker per PORT.md's FlowContext-bypass
// convention.
package silence
