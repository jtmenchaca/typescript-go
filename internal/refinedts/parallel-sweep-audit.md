# Parallel-sweep audit — package-level mutable state

The port's payoff lever is per-file parallelism (tsgo checks recharts
in 0.7s using every core; the TS checker was single-threaded by
nature). This is the audit of what blocks a goroutine-per-entry
sweep, from a `^var ` survey of internal/refinedts (2026-08-12,
tree at the walk-integration milestone).

## Two designs, in order of arrival

1. **Process-per-shard** (the speed-ladder's row 5a): N processes,
   each one program + one kernel + its file partition. Sidesteps this
   entire audit — every global is per-process. Costs one program
   build + kernel init per shard. This ships FIRST because it needs
   zero code audited.
2. **Goroutine-per-entry, one process** (the tsgo-native shape):
   shared program + shared kernel, all cores, no per-shard boot. Needs
   every row below resolved. This is the end state.

## The kernel under concurrency

`kernelbridge.NativeKernel` already serializes every ask onto ONE
locked OS thread (Lean runtime discipline) — goroutines can ask
concurrently and correctness holds, but kernel asks contend on that
one thread. Kernel self-time on the TS sweeps was ~1s warm; if the
profile shows contention, per-goroutine kernels (one dylib handle
each) or a small kernel pool is the follow-up. The question cache
(`questionCache`, `dirtyAnswers`, `storeLoaded`) must become
mutex-guarded (verify current state) — it is shared across all asks.

## Immutable — safe as-is (no action)

Constant tables and compiled regexes: the temporal grammars
(refinementsets/temporal_string_grammars.go), regex_compiler's
classes, codepoint sets, Integer/EmptyTuple/Numbers forms,
zod vocabularies/patterns/roots, walk's op-wire maps, syntax models,
web_api tables, dateGetterWindows, ArrayCallbackMethods,
comparisonOps, trustLevelOrder, GrainLevel, ownerNote, iso patterns,
oneArg/twoArgSymbols, LibraryAdapters (verify no post-init writes),
sentinel values (Unknown/Opaque/Undef/NaNValue/HostFunction,
absentMarker/notExactMarker, None, Other, Bottom, UnreadUnknown).

## Mutable, WALK-SCOPED state that must move into the check's context

These are per-check working state living in package vars — correct
single-threaded, racy under goroutines. The fix shape is the same for
all: hang them off FlowContext / a per-check struct, or key by entry.

- assignability/decline_reasons.go: `collectors` (a stack; whole
  design is per-check nesting) — becomes a context-carried collector.
- narrowing/predicate_read.go: `predicateDepth` int.
- walk/constructed_instance.go: `constructing` map (recursion guard).
- walk/evaluate_expression.go, call_site_snapshots.go,
  arithmetic_transfer.go var blocks; narrowing/reassigned_names.go,
  bound_condition.go, pinned_function.go; dataflowfacts/
  written_paths.go, syntactic_facts.go, exit_constraints.go,
  sum_constraints.go, alias_analysis.go var blocks; annotations/
  annotation_of_type.go, program_graph.go var blocks — EACH needs a
  read to classify memo-vs-working-state (some are caches keyed by
  node — see next section; some are per-walk state — context).

## Mutable CACHES — need a mutex or sync.Map (cheap, keep global)

Keyed by stable pointers, content immutable once computed — safe to
share once guarded:

- primitives/primitive_kinds.go `stringLikeMemo` (no lock today).
- dataflowfacts/access_paths.go `nestedFunctionsCache` (HAS mutex ✓ —
  the pattern to copy).
- walk's per-program memos (call_site_bindings' remembered map is
  mutex-guarded ✓ per its port report).
- kernelbridge/question_costs.go `costTotals`/`worstQuestions`
  (instrument-only; guard or accept skew).

## Singletons — init-once, fine with sync.Once

- kernelbridge/ask_kernel.go `kernelInstance`/`kernelNative`.
- narrowing/type_guard_recognizers.go `NarrowKernel` (set at load).
- kernelbridge/kernel_bridge.go `kernelArtifactPath`.

## Tracing — documented single-writer

tracing/trace_state.go's records say so in their header. Under
goroutines: either per-goroutine records merged at stop, or tracing
stays off during parallel sweeps (the gauge's wall clock and
per-file times don't need spans). Decide when the gauge exists.

## Order of work when the gate opens

1. Gauge single-goroutine first (the honest baseline).
2. Shard-by-process for the immediate multiplier (no audit needed).
3. Then the in-process audit above, mechanical rows first
   (mutexes on caches, context-carried walk state), gauge after each.
