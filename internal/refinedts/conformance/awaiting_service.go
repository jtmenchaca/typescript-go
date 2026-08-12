// Package conformance holds the Go twin of
// refined-ts-typescript/conformance/ — the parity gate that holds the
// checker's own mirrors (its join, its narrowing, its derivation
// chains) to the Lean kernel's proved answers.
//
// Three files needed only the kernel + abstract_domain + refinement_sets
// layers and are ported here, 1:1, kernel-gated (skip when the native
// dylib is absent, never a faked pass):
//
//   - lattice_conformance_test.go  (lattice_conformance.test.ts)
//   - narrowing_conformance_test.go (narrowing_conformance.test.ts)
//   - replay_test.go               (replay.test.ts)
//
// AWAITING SERVICE — every other .test.ts in that directory drives a
// full end-to-end file check (`service/check.ts`'s check/checkFile, or
// `service/program_host.ts`'s programFromSource, or
// `service/coverage_report.ts`'s coverageOf/`service/hover_provider.ts`'s
// formatRefinementAt) and has no Go twin yet, since
// internal/refinedts/service/ does not exist in this tree (a concurrent
// agent owns that port). Listed here so the gap is visible in one place
// rather than silently missing:
//
//   - assume_conformance.test.ts    — control_flow's analyzeStatements
//     run against kernel_delegation's engine route, both from
//     service/program_host.ts's programFromSource. The two engines it
//     compares (control_flow, dataflow_facts) DO have Go homes (package
//     walk, package dataflowfacts), but this file's own bothEngines
//     helper builds its FlowContext through programFromSource, which is
//     service-tier — the comparison cannot be separated from it.
//   - boundary_exhibit.test.ts      — service/check.ts's check()
//   - cycle_guards.test.ts          — service/check.ts's check()
//   - language_conformance.test.ts  — service/coverage_report.ts's
//     coverageOf, over tsc-vscode/fixtures/language/*.ts
//   - nested.test.ts                — service/hover_provider.ts's
//     formatRefinementAt + service/program_host.ts's programFromSource
//   - pipeline_v2_status.test.ts    — service/check.ts's checkFile, over
//     tsc-vscode/fixtures/examples/pipeline-v2.ts
//   - pipeline_v3_status.test.ts    — same, pipeline-v3.ts
//   - pipeline_v4_status.test.ts    — same, pipeline-v4.ts
//   - soundness.test.ts             — service/check.ts's check(); the
//     mustFlag battery, 65 cases, each asserting at least one
//     refinement diagnostic fires on a concrete soundness-violating
//     program
//   - soundness_requirement.test.ts — service/check.ts's checkFile, over
//     tsc-vscode/fixtures/examples/30-additional-gaps.ts (150+ cases)
//
// Per this unit's instructions: do NOT import or reference the service
// package even where files of that name appear elsewhere in the Go
// tree — this package must build standalone. Once service/ lands, port
// the nine files above the same way: check()/checkFile() calls become
// direct calls into the ported service package, and programFromSource
// callers gain a Go host builder the way walk's own tests already use
// program.CheckerProgram for the non-service-tier program shape.
package conformance
