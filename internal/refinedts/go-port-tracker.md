# Go port tracker

One row per TS directory. Status moves pending → in-flight → ported;
`blocked-on` names imports a unit could not port past (from the
agent's report). Tests column is ported/passing from the report.
Update this file when a unit lands — it is the board the waves are
dispatched from.

| TS directory | Go package | status | tests | notes / blocked-on |
|---|---|---|---|---|
| primitives | primitives | **ported** | 6/6 | int64 dyadics; FlowContext bypassed → `*checker.Checker` directly |
| refinement_sets | refinementsets | **ported** | 30/30 | GOOS rename (`repetition_window_forms.go`); RE2 lookahead by hand; set_simplification against a 3-method kernel interface; temporal grammar + integration tests await kernel_bridge/service |
| kernel_bridge | kernelbridge | **ported** | 33/33 (3 seam + 30 remainder) | cgo seam PROVEN (dlopen + locked worker thread; native-only, no wasm path); `structural`/`checkAssignability`/`encodeSpecification` NOT ported — Specification (object_graphs, unported) — blocked-on object_graphs; canonicalKeyOf's WeakMap memo dropped (map[string]any is not comparable in Go, and every call site rebuilds its wire form fresh anyway — no correctness loss, only the memo-hit speed win); question store salted to a DIFFERENT file name (questions-go-v1.json); tracing seam wired (wire.encodeSet/encodeTuple spans, kernel.ask span, cacheHit/cacheLookup/canonicalKey counters) |
| abstract_domain | abstractdomain | **ported** | 7/7 (grades.test.ts blocked — needs kernel_bridge, control_flow, dataflow_facts, annotations, service, not ported yet) | ObjectAnnotation (annotations/) referenced only by identity — stands in as an opaque `*struct{}` pointer; STRICTNESS (service/analysis_limits.ts) inlined as its current "full" value in TrustLevelAdmitted |
| dataflow_facts | dataflowfacts | partial | 37/37 | alias_analysis (AliasClasses/ForgetPlaceEntries/UpdateTracked, against abstractdomain+silence) and linear_constraints (ConstraintsImply/MixedConstraintsImply/DecideComparison's kernel fallback, against kernelbridge.RefinedTSKernel.LinearImplies) now PORTED — unit tests exercise the real native kernel dylib (t.Skip if the artifact is absent, never faked). Still blocked, banners only, no files: difference_constraints.ts, entry_dependent_constraints.ts (needs annotations' DeclaredRefinement), length_guard_narrowings.ts, inverse_factor_narrowings.ts (all need narrowing/condition_tree.ts, in flight concurrently); path_conditions.go's gateKeyOf/assumedVerdict/gatesTestedBy/correlationGateOf and sum_constraints.go's sumConstraintsOf (same condition_tree dependency); syntactic_facts.go's writtenNamesOf (needs service's resolvesToDefaultLib) — see dataflowfacts/*.go banners |
| type_reading | typereading | **ported** | 11/11 | host reads direct on *checker.Checker; symbolAt inlined from service/program_resolution; exports.go gained GetConstraintOfType + SymbolInDefaultLib; jsnum.FromString for literal text |
| comparison | comparison | blocked | 0/0 | sole file compareKnown() is 100% AbstractValue/FlowContext/residue — none ported; blocked-on abstract_domain, kernel_bridge (via FlowContext.kernel), silence |
| silence | silence | **ported** | 7/7 | afterReaders/seededBinding ported against typereading.ArrivedUnchecked/ReadHostType and abstractdomain directly (the TS CheckerProgram/host param is `*checker.Checker`, per PORT.md); unknown.test.ts's whole-tree grep suite (no `return UNKNOWN` / no `UNKNOWN` import outside the allowlist) has no Go-shaped twin yet — ported the functional half (Residue/CutUnknown/DissolveUnknown each return the unknown atom) instead; see silence/unknown_test.go's file comment |
| narrowing | narrowing | pending (wave 2) | — | needs abstract_domain, dataflow_facts |
| evaluation | evaluation | pending (wave 2) | — | needs abstract_domain, narrowing dispatchers |
| bindings | bindings | pending (wave 2) | — | |
| interprocedural | interprocedural | pending (wave 2) | — | |
| control_flow | controlflow | pending (wave 3) | — | the analyzeStatement/answerFlowAt dispatchers |
| assignability | assignability | pending (wave 3) | — | |
| annotations | annotations | pending (wave 3) | — | includes library_adapters/zod |
| object_graphs | objectgraphs | **ported** | 5/5 | Specification/CardinalityPath/ObjectKey/ObjectNode/CountGroup ported 1:1 in graph_specification.go; derivedReaches ported 1:1 against kernelbridge.RefinedTSKernel.Transfer(TransferOpCountProduct) directly (no wire round-trip needed — the Go kernel interface already returns decoded TransferAnswer); unblocks kernelbridge's structural/checkAssignability/encodeSpecification (Specification now has a Go twin) — coordinator to wire those in kernelbridge/ next |
| surface | — | decide | — | z.* runtime surface — may stay TS-only (it is the user-facing library) |
| conformance | conformance | pending (last) | — | the parity gate: same fixtures, diff verdicts vs the TS checker |
| service | service | pending (last) | — | check entry, CLIs; the oracle/wire layer under service/tsgo does NOT port (deleted by design) |
| bench | — | decide | — | profilers are instrument code; port only what the gauge needs |

## Standing dispatch rules

- One agent, one directory, exclusive ownership; the only shared file
  is `internal/checker/exports.go` (adding wrappers) — collisions
  there are report items.
- Replace a misbehaving agent; never resume one alongside itself.
- Every report's insights fold into PORT.md before the next wave.
- Waves may run ahead of strict dependency order: agents port the
  independent parts and report blockages (no silent stubs).
