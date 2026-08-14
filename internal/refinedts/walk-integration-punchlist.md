# walk integration punchlist (from stage reports)

Items 1–3, 5–10, 12–13, 15–16 resolved at the integration pass that
made `pnpm go:build`/`go:vet`/`go:test` green across the whole tree.
Deleted below; see go-port-tracker.md's rows (walk, narrowing,
conditiontree, dataflow_facts, annotations, comparison,
interprocedural) for what changed. Remaining items are genuinely
unresolved, each with its own reason.

4. date_models.go Date.parse approximation (time.Parse layout list,
   not full ECMA Date-parse) — acceptable for now; note in tracker
   as a fidelity gap to revisit at conformance. STATUS: unresolved,
   informational only — no action taken this pass (not a build/test
   blocker; a conformance-stage fidelity item).

6. abstractdomain.ObjectAnnotationRef remains *struct{} with
   walk/declared_value.go bidirectional memo (objectAnnotationsByToken +
   objectAnnotationOf) — closed. walk/object_key_access.go:84-115 now
   resolves the token to its annotation and answers stated keys without
   requiring an import inversion. The ref type stays opaque to
   abstractdomain; the memo keeps the annotation accessible in both
   directions. STATUS: RESOLVED — the inverse-memo approach fixed the
   bound-object key read without needing to invert the package order.

11. control_flow's .test.ts files were out of the porter's scope —
    port them at/after integration (loop_fixpoint.test,
    switch_statement.test, entry_env.test, call_site_bindings.test ✓
    with its file, kernel_delegation.test, walk_probe.test,
    flow_state_at_position.test, lowering_to_kernel_ir.test) — they
    are the component's behavioral coverage. STATUS: unresolved —
    out of scope for a build/test-green integration pass (these are
    NEW test ports, not fixes to existing code); a future unit's
    work.

13 (remainder). zod's parse_evaluator.ts (needs walk's
    js_value_conversion.go, which IS ported) → joins walk as
    `// from annotations/library_adapters/zod/parse_evaluator.ts`.
    STATUS: unresolved — WornOfAnnotation/WornOfObject call sites in
    schema_runtime_models.go and callback_pins.go are fixed and
    verified working (they were the mechanical half of this item).
    evaluateParseOutcome itself is a genuine port task (reads deep
    into the zod adapter's compiled-schema shapes across the
    two-way-cycle boundary already resolved for schema_chain_compiler
    et al.) — left for a dedicated porting unit, not attempted here
    per the instruction to restrict scope to what integration
    requires plus item 15's explicit design task.
