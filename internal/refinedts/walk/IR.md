# IR lowering — one deep module inside `package walk`

IR lowering is the adapter half of the engine division: recognize
TypeScript syntax, lower it to kernel flow IR / effect expressions /
summary bodies, and decline honestly where the reading does not hold.
It lives inside `package walk` because evaluation, control flow,
interprocedural, and bindings form one SCC that Go cannot split into
packages. Do not extract a new Go package for IR until a second adapter
appears.

Twin doctrine for walk-route `()` / tagged-template evaluation (not IR):
`CALLS.md`. That rewrite does not change this module’s Interface.

## External interface

Callers outside the IR cluster reach it through a small surface:

| Caller | Entry points |
|---|---|
| `kernel_delegation.go` | `LowerStatements` |
| `kernel_summaries.go` | `LowerStatements`, summary-body lowering |
| `loop_effect.go` | `LowerEffectExpression` |
| `summary_fixpoint.go` | summary-body lowering |

Everything else in the IR cluster is implementation behind that surface:
slot recognition (array / object / map / field bundles), statement
lowering, effect expression lowering, opaque decline/havoc, and summary
compile (body / call / callback).

## Internal seams (capability clusters)

These are the natural seams for file splits. Exact file names after a
split wave are listed under **Post-split file map** (filled by the
parent after agents report).

1. **Slot recognition** — flattened locals and their sorts/slots for
   arrays, objects, maps, and class/record field bundles.
2. **Statement lowering** — `LowerStatements` / statement-kind routes,
   switch chains, return shapes, throw/try coverage, condition hoist.
3. **Effects** — `LowerEffectExpression` and the pure/sequence/string/
   closure helpers it calls.
4. **Opaque decline** — run bookkeeping, statement havoc, call havoc,
   construct naming.
5. **Summary compile** — declaration-keyed body lowering, call/closure
   lowering, callback method summaries.

## Non-goals

- No new Go package or subdirectory under `walk/`.
- No export renames, no behavior changes, no “while I’m here” cleanups.
- No package-boundary reorganization of evaluation/control_flow back
  out of `walk` (see `PORT.md` — the SCC merge stays).

## Post-split file map

Two waves complete (mechanical splits + giant-func helper extracts;
no build/test gate). The IR / effect / lowering cluster production
files are all under 400 lines.

### Slot recognition

- **array** — `ir_array_slots.go` (recognizer + type) plus
  `ir_array_use_scan.go`, `ir_array_bridge.go`, `ir_array_copies.go`,
  `ir_array_split.go`, `ir_array_tables.go`, `ir_array_declarations.go`,
  `ir_array_parameters.go`, `ir_array_element_sorts.go`,
  `ir_array_slot_effects.go`, `ir_array_push.go`, `ir_array_index.go`,
  `ir_array_for_of.go`
- **object** — `ir_object_slots.go` (leaf vocabulary) plus
  `ir_object_slots_use_scan.go`, `ir_object_slots_recognizer.go`,
  `ir_object_slots_declared_types.go`, `ir_object_slots_constructed.go`,
  `ir_object_slots_join_arms.go`, `ir_object_slots_arm_type_nodes.go`,
  `ir_object_slots_slot_index.go`, `ir_object_slots_record_assignment.go`,
  `ir_object_slots_destructuring.go`,
  `ir_object_slots_pattern_assignments.go`
- **map** — `ir_map_slots.go` (recognizer + doctrine) plus
  `ir_map_syntax.go`, `ir_map_use_scan.go`, `ir_map_slot_sorts.go`,
  `ir_map_slot_reads.go`, `ir_map_declaration_assignments.go`,
  `ir_map_mutation_assignments.go`, `ir_map_iteration_slots.go`
- **field bundles** — `ir_field_bundles.go` (census entry) plus
  `ir_field_bundles_declared_fields.go`,
  `ir_field_bundles_heritage.go`, `ir_field_bundles_symbol_keys.go`,
  `ir_field_bundles_capture_writes.go`,
  `ir_field_bundles_capture_mentions.go`,
  `ir_field_bundles_receiver_forms.go`, and the scan extract:
  `ir_field_bundles_scan.go`, `ir_field_bundles_store_targets.go`,
  `ir_field_bundles_visit.go`, `ir_field_bundles_read_arms.go`,
  `ir_field_bundles_write_arms.go`, `ir_field_bundles_call_arms.go`

### Statement lowering

- `lowering_to_kernel_ir.go` (dispatcher + `lowerStatementList`) plus
  `lowering_to_kernel_ir_return.go`,
  `lowering_to_kernel_ir_return_branch.go`,
  `lowering_to_kernel_ir_return_arms.go` (plural: a `_arm.go`
  suffix is a GOARCH build constraint and the file silently drops
  out of every non-ARM build),
  `lowering_to_kernel_ir_return_members.go`,
  `lowering_to_kernel_ir_throw.go`,
  `lowering_to_kernel_ir_condition.go`,
  `lowering_to_kernel_ir_loops.go`,
  `lowering_to_kernel_ir_flattening.go`,
  `lowering_to_kernel_ir_switch.go`,
  `lowering_to_kernel_ir_switch_labels.go`

### Effects

- `effect_expression.go` (dispatcher + op tables + `EffectReader`) plus
  `effect_pure_builtins.go`, `effect_write_freedom.go`,
  `effect_closure_writes.go`, `effect_capture_census.go` plus
  `effect_capture_census_objects.go`, `effect_capture_census_visit.go`,
  `effect_capture_census_calls.go`,
  `effect_capture_census_member_write.go`,
  `effect_capture_census_writes.go`, `effect_capture_names.go`,
  `effect_sequence.go`, `effect_exact_string_methods.go`,
  `effect_sequence_call_gates.go`, `effect_replace_union.go`

### Opaque decline

- `ir_opaque_havoc.go` (floor entry) plus `ir_opaque_havoc_run.go`,
  `ir_opaque_havoc_impossibilities.go`,
  `ir_opaque_havoc_decline_names.go`,
  `ir_opaque_havoc_statement_effects.go`,
  `ir_opaque_havoc_enumeration.go`, `ir_opaque_havoc_call.go`,
  `ir_opaque_havoc_construct_names.go`,
  `ir_opaque_havoc_shape_names.go`

### Assignment / accessors / guards / await / hoist / loops

- **assignment** — `ir_assignment.go` plus `ir_assignment_effect_read.go`,
  `ir_assignment_const_reads.go`, `ir_assignment_chained.go`,
  `ir_assignment_declarators.go`, `ir_assignment_closure_values.go`
- **accessors** — `ir_accessor_calls.go` plus
  `ir_accessor_calls_read_write.go`, `ir_accessor_calls_statement.go`,
  `ir_accessor_calls_receiver.go`, `ir_accessor_calls_doors.go`,
  `ir_accessor_calls_read_modify_write.go`
- **guards** — `ir_guard.go` plus `ir_guard_single_head.go`,
  `ir_guard_shapes.go`, `ir_guard_typeof_read.go`,
  `ir_guard_hoisted_temp.go`, `ir_guard_arg_sorts.go`
- **await** — `ir_await.go` plus `ir_await_promise_local.go`,
  `ir_await_return.go`, `ir_await_promise_all.go`
- **call hoist** — `ir_call_hoist.go` plus
  `ir_call_hoist_order_gate.go`, `ir_call_hoist_flush.go`
- **loop stmts** — `ir_loop_stmts.go` plus `ir_loop_stmts_for.go`,
  `ir_loop_stmts_for_in_of.go`, `ir_loop_stmts_head.go`,
  `ir_loop_stmts_transfers.go`
- **loop fold** — `ir_loop.go` plus `ir_loop_fold.go`,
  `ir_loop_map_for_of.go`

### Summary compile

- **body** — `ir_summary_body.go` (doors) plus
  `ir_summary_record_member_reading.go`,
  `ir_summary_named_type_members.go`,
  `ir_summary_composite_type_members.go`,
  `ir_summary_interface_heritage_members.go`,
  `ir_summary_parameter_entries.go`,
  `ir_summary_record_parameter_uses.go`,
  `ir_summary_this_bundle_layout.go`,
  `ir_summary_returned_shape.go`,
  `ir_summary_local_slot_layout.go`,
  `ir_summary_capture_slots.go`,
  `ir_summary_body_lowering.go` plus
  `ir_summary_body_lowering_layout.go`,
  `ir_summary_body_lowering_parameters.go`,
  `ir_summary_body_lowering_record_parameters.go`,
  `ir_summary_body_lowering_captures.go`,
  `ir_summary_body_lowering_slots.go`,
  `ir_summary_body_lowering_havoc_vector.go`,
  `ir_summary_body_lowering_constructor_prelude.go`,
  `ir_summary_body_lowering_default_prelude.go`
- **call** — `ir_summary_call.go` (door/havoc) plus
  `ir_summary_call_closure_serving.go`,
  `ir_summary_call_closure_blob.go`,
  `ir_summary_call_statement.go`,
  `ir_summary_call_ret_members.go`,
  `ir_summary_call_receiver.go`,
  `ir_summary_call_param_bundles.go`,
  `ir_summary_call_surface.go`
- **callback** — `ir_callback_summary.go` (statement entry) plus
  `ir_callback_recognition.go`, `ir_callback_entries.go`,
  `ir_callback_convert.go`, `ir_callback_shapes.go`,
  `ir_callback_array_statements.go`,
  `ir_callback_scalar_statements.go`,
  `ir_callback_collection_then.go`, `ir_callback_return.go`

### Still over 400 outside the IR cluster

Walk production files still over 400 are mostly evaluation / control /
interprocedural / models (e.g. `kernel_summaries`, `call_site_bindings`,
`loop_fixpoint`, `inliner`, `*_models`, `assume_condition`) — not IR
lowering. Next split wave if wanted.
