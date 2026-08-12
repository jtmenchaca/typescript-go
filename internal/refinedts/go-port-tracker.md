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
| kernel_bridge | kernelbridge | in-flight (main session — the cgo/C-ABI seam) | — | dlopen→cgo; wire format; question cache; satisfies refinementsets.SimplificationKernel |
| abstract_domain | abstractdomain | in-flight (wave 1) | — | |
| dataflow_facts | dataflowfacts | in-flight (wave 1) | — | |
| type_reading | typereading | in-flight (wave 1) | — | |
| comparison | comparison | in-flight (wave 1) | — | |
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
