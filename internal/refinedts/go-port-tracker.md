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
| kernel_bridge | kernelbridge | **ported** | 33/33 (3 seam + 30 remainder) | cgo seam PROVEN (dlopen + locked worker thread; native-only, no wasm path); `structural`/`checkAssignability`/`encodeSpecification` NOT ported — Specification (object_graphs, unported) — blocked-on object_graphs; canonicalKeyOf's WeakMap memo dropped (map[string]any is not comparable in Go, and every call site rebuilds its wire form fresh anyway — no correctness loss, only the memo-hit speed win); question store salted to a DIFFERENT file name (questions-go-v1.json) |
| abstract_domain | abstractdomain | **ported** | 7/7 (grades.test.ts blocked — needs kernel_bridge, control_flow, dataflow_facts, annotations, service, not ported yet) | ObjectAnnotation (annotations/) referenced only by identity — stands in as an opaque `*struct{}` pointer; STRICTNESS (service/analysis_limits.ts) inlined as its current "full" value in TrustLevelAdmitted |
| dataflow_facts | dataflowfacts | partial | 27/27 | 4 of 10 files fully blocked (difference_constraints, entry_dependent_constraints, length_guard_narrowings, inverse_factor_narrowings — all need kernel_bridge/kernel_interface.ts's RefinedTSKernel and/or narrowing/condition_tree.ts, neither ported yet); alias_analysis and linear_constraints partially blocked (AliasClasses/updateTracked need abstractdomain+silence, out of allowed import set even though landed; decideComparison's kernel-composed fallback needs RefinedTSKernel); path_conditions mostly blocked (gateKeyOf and dependents need condition_tree) — see dataflowfacts/*.go banners |
| type_reading | typereading | **ported** | 11/11 | host reads direct on *checker.Checker; symbolAt inlined from service/program_resolution; exports.go gained GetConstraintOfType + SymbolInDefaultLib; jsnum.FromString for literal text |
| comparison | comparison | blocked | 0/0 | sole file compareKnown() is 100% AbstractValue/FlowContext/residue — none ported; blocked-on abstract_domain, kernel_bridge (via FlowContext.kernel), silence |
| silence | silence | in-flight (wave 1) | — | |
| narrowing | narrowing | pending (wave 2) | — | needs abstract_domain, dataflow_facts |
| evaluation | evaluation | pending (wave 2) | — | needs abstract_domain, narrowing dispatchers |
| bindings | bindings | pending (wave 2) | — | |
| interprocedural | interprocedural | pending (wave 2) | — | |
| control_flow | controlflow | pending (wave 3) | — | the analyzeStatement/answerFlowAt dispatchers |
| assignability | assignability | pending (wave 3) | — | |
| annotations | annotations | pending (wave 3) | — | includes library_adapters/zod |
| object_graphs | objectgraphs | pending (wave 3) | — | |
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
