# Walk-route calls — six questions, two products, one-shot rewrite

**Doctrine + migration map** for walk-route `()` / tagged-template
evaluation inside `package walk`. Twin of `IR.md` (IR lowering stays a
separate Module).

Org chart: plain English questions. Not “kinds of knowing,” not a plugin
cascade.

Pressure-tested 2026-08-16 (Gemini / Grok). Feedback on World order,
Prepare/Admit double-eval, obligation doors, and the test gate is
folded into §§2–3 and §7 below.

---

## 1. Org chart

At every call-shaped **job** (`CallExpression`, `TaggedTemplateExpression`)
the checker answers six questions:

| # | Question |
|---|---|
| **Q1** | Who is actually being called, and what is `this`? |
| **Q2** | What values occupy which argument positions? |
| **Q3** | Are those arguments allowed? *(static/builtin diagnostics — never the return)* |
| **Q4** | Which in-reach JavaScript runs while this call is evaluated? |
| **Q5** | After the call, what is true of the caller’s tracked names and objects? |
| **Q6** | What value does the call produce? |

Every answered call yields **two products**:

- **Value** — what the expression is (Q6)
- **World** — what the caller must now believe (Q5)

They need not share an owner (a complete Lean summary may pin Value while
World is only a forget of the receiver).

**Not the org chart:** “encoded library / analyzed callee / declared
result only.” That is a **legend under Q6 only**. Body + declared type
often **meet** (`MeetKnown`).

**Runtime spine** (how one call runs — not a migration sequence):

```text
PrepareCall (Q1–Q2)
  → CheckCallArgumentObligations (Q3)   // static + builtin only
  → DetermineCallProducts (Q4 + Value∥World)
  → ApplyWorldUpdate (commit Q5)
  → return Value
```

Schema `.parse` / `.safeParse` throw-vs-value is decided inside the
library arm of `DetermineCallProducts` (see §3 Q3), not in a second
pre-products obligation pass.

---

## 2. End-state Interface

| Caller | Entry |
|---|---|
| `evaluate_expression.go` | `EvaluateCallExpression` |
| `evaluate_expression.go` | `EvaluateTaggedTemplate` |

Thin adapters: return `products.Value`. No private inline, admission, or
caller-env mutation.

### Types

```go
type PreparedCall struct {
	Site      *ast.Node
	Callee    *ast.Node // after .call/.apply/.bind peel
	This      *abstractdomain.AbstractValue
	ThisNode  *ast.Node
	Arguments EffectiveArguments // Exact = placement completeness
	Contract  *FunctionContract
	Receiver  *ast.Node
	// Evaluated is true once any effectful evaluateExpression ran
	// while building this value. Admission must not re-walk those nodes.
	Evaluated bool
}

type CallProducts struct {
	Value      abstractdomain.AbstractValue
	World      WorldUpdate
	Provenance ValueProvenance // legend under Q6 only — never a dispatch key
}

type ValueProvenance int
const (
	ValueFromLibraryRow ValueProvenance = iota
	ValueFromBody // walk, replay, or complete Lean summary
	ValueFromDeclaration
	ValueUnknown
)

// WorldUpdate — empty means LeaveIt.
// Three bags; ApplyWorldUpdate uses a fixed phase order (see below).
type WorldUpdate struct {
	WriteBacks []WriteBackOp
	Forgets    []ForgetOp
	Havocs     []HavocOp
}

type RunsNowKind int
const (
	RunsNothingLibrary RunsNowKind = iota
	RunsNothingGenerator
	RunsCalleeBody
	RunsCallbackNow
	RunsNothingNoBody
	RunsNothingAdmit
)
```

### World apply order (H1)

`ApplyWorldUpdate` applies **WriteBacks, then Forgets, then Havocs**.

Rationale (from today’s replay): the same root can receive both a
write-back and a forget/havoc on one call (e.g. `f(obj, obj)` with one
parameter captured and one written). Applying ops in parameter emission
order can write back *after* a havoc and resurrect a name that should
stay forgotten. Fixed phases make destructive updates win.

Object-literal WriteBack vs class ForgetThrough remain different
**products** from different BodyProducts arms — not two ops on one
product that need emission-order preservation.

`ReadsWithoutEffect` re-reads (identifier / `this` / property chain) are
not World ops.

### Prepare / Admit (H2)

```go
func PrepareCall(ctx *FlowContext, env Env, site *ast.Node) (PreparedCall, bool)
// ok=false only if zero caller-visible *effects* have run (shape/exactness
// gates). ReadsWithoutEffect re-reads do not count as effects.
// If any effectful evaluateExpression ran, ok must be true and Evaluated
// set; do not decline into a path that walks those nodes again.

func CheckCallArgumentObligations(ctx *FlowContext, env Env, prepared PreparedCall)
// User CheckContractArguments + CheckBuiltinContracts only.

func DetermineCallProducts(ctx *FlowContext, env Env, prepared PreparedCall) CallProducts
func ApplyWorldUpdate(ctx *FlowContext, env Env, world WorldUpdate)
func AdmitCallFromPrepared(ctx *FlowContext, env Env, prepared PreparedCall) CallProducts
// Uses Arguments / This already on prepared. Does not evaluateExpression
// those nodes again.

func EvaluateCallExpression(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	prepared, ok := PrepareCall(ctx, env, e)
	if !ok {
		// Unevaluated decline only — e.g. apply argArray not safe to read once.
		products := AdmitCallUnevaluated(ctx, env, e) // may walk the site once
		ApplyWorldUpdate(ctx, env, products.World)
		return products.Value
	}
	CheckCallArgumentObligations(ctx, env, prepared)
	products := DetermineCallProducts(ctx, env, prepared)
	ApplyWorldUpdate(ctx, env, products.World)
	return products.Value
}
```

Live hole this kills: `ApplyCallResult` evaluates `thisArg`, then
`!exact` → nil, then `EffectiveArgumentsOf` re-runs `thisArg`
(`withThis.apply(computeThis(), inexactXs)`). After effectful eval,
commit or hand values to Admission — never nil + re-walk.

`EvaluateTaggedTemplate` is the same spine. Private MeetKnown /
RecoverPure / admission copy in today’s tagged-template file is gone.

---

## 3. Invariants

| Law | Statement |
|---|---|
| **Two products** | Every answered call has Value and World (LeaveIt is a World). |
| **World phases** | Apply WriteBacks, then Forgets, then Havocs. |
| **Q3 static only** | `CheckCallArgumentObligations` = user + builtin contracts. Does not run `EvaluateParseOutcome`. |
| **Parse 7001 with parse outcome** | Schema `.parse` / `.parseAsync` Report 7001 inside the library arm when `outcomeKindThrows` and the method is not `safeParse` (which reifies the throw as a value). Same evaluation that decides Q6 — no second parse in Q3. |
| **Q3 never owns Q6** | Obligation fires are diagnostics, not the return. Worn return after parse 7001 stays legal. |
| **No Admit re-walk after effects** | `ok=false` ⇒ zero effects so far. Otherwise `AdmitCallFromPrepared`. |
| **Q4 never owns Q6 by accident** | Generator → `RunsNothingGenerator`; body does not walk at this `()`. |
| **Q1 peel, one product path** | `.call`/`.apply`/`.bind` → prepared site → same `DetermineCallProducts`. No parallel this-parameter inliner. |
| **Lean complete-only** | Porous cannot be `ValueFromBody`. |
| **Exact-argument summary decline** | Exact list into rest/array that summary topped → walk. |
| **MeetKnown on body+declared** | Only on body-derived Value. |
| **Object-literal vs class World** | WriteBack of `this.*` vs ForgetThrough receiver — different arms. |
| **Hand-over havoc** | Nested functions in args: Havoc when Q4 does not run them now. |
| **Caller-env mutation locality** | Only `ApplyWorldUpdate` mutates caller env on the call path. |
| **`readUnmodeledMethod` declines** | Residue is Admission’s. |
| **Admission owns Q6 fallback** | One place for worn/ground, parametric identity, opaque, residue. |
| **Same `[[Call]]` as direct** | Peeled `.call` uses BodyProducts (captures, memo, Lean, object-literal/class). |

---

## 4. End-state file map (`package walk`)

Same package. No `walk/calls/`. Target &lt;200 lines/file, max 300–400.

### New / primary

| File | Role |
|---|---|
| `CALLS.md` | This document |
| `call_products.go` | Types + `ApplyWorldUpdate` (phase order) |
| `prepare_call.go` | `PrepareCall` |
| `prepare_call_peel.go` | `.call`/`.apply`/`.bind` recognition only |
| `call_obligations.go` | Q3 static + builtin |
| `determine_call_products.go` | `DetermineCallProducts`, `WhatRunsNow` |
| `call_body_products.go` | BodyProducts arms |
| `call_library_products.go` | Library rows; schema parse Report lives here |
| `call_admission.go` | `AdmitCallFromPrepared` / unevaluated admit |
| `call_products_test.go`, `prepare_call_test.go` | Q-boundary pins |

### Existing — new role

| File | End state |
|---|---|
| `evaluate_call_expression.go` | Thin adapter. Fall-through chain deleted. |
| `evaluate_tagged_template.go` | Thin adapter. Private spine deleted. |
| `this_parameter_call.go` | Value APIs deleted; shape helpers → peel. |
| `apply_call.go` / `bind_call.go` | Peel only; `*CallResult(*AbstractValue)` deleted. Fix `!exact` after eval (commit or prepared admit — no nil). |
| `builtin_models.go` + `*_models.go` | `*CallProducts` or nil; no always-answer unmodeled tail. |
| `unmodeled_method_havoc.go` | Declining; World on product. |
| `unmodeled_call_result.go` | Deleted → admission. |
| `inline_contract_body.go` / `inline_replay.go` | Return products; no caller-env mutation. |
| `function_summaries.go` | Constant writes → World bags. |
| `kernel_summaries.go` | `CompleteLeanSummary` pairs ret + WorldFromSummaryEffects. |
| `generator_element.go` | Feeds `WhatRunsNow`; drain routes stay. |
| `callback_*.go` | `RunsCallbackNow`. |
| `method_this_writes.go` | Object-literal BodyProducts arm. |
| `schema_runtime_models.go` | Library arm; owns parse 7001 when throw + `.parse`. |
| `call_argument_contracts.go` / `builtin_contracts.go` | Behind Q3. |

### Out of this rewrite

| Out | Why |
|---|---|
| IR cluster (`IR.md`) | Not `CallProducts` |
| `evaluate_new_expression.go`, property data-reads, iteration `ElementOf` | Parallel paths; they have value-without-world / split-obligation issues, but sealing `()` does not require folding them into this commit |
| Getter-as-job on CallProducts | Same |
| `service/` WalkEntry / kernel seating / hover `InlineCallback` | Different export surface; unchanged unless this commit touches those symbols |
| `silence/`, `typereading/`, assignability leaf, `narrowing/`, Lean wire | Unchanged |

---

## 5. One-shot work list

One commit (or one branch merge). Intermediates need not compile. Before
the gate, **all** of the following exist. Grouped by concern, not by
landing order.

### Spine and types

- [ ] Types as in §2, including `PreparedCall.Evaluated` and World bags.
- [ ] `ApplyWorldUpdate`: WriteBacks → Forgets → Havocs.
- [ ] `EvaluateCallExpression` / `EvaluateTaggedTemplate`: thin adapters; no `!ok → AdmitCall(site)` after effects.
- [ ] No new Go package; no `walk/calls/`.

### Q1–Q2

- [ ] Peel `.call`/`.apply`/`.bind` in Prepare; apply decline-unevaluated for unsafe argArray; after effectful eval, no nil fallthrough (`!exact` fixed).
- [ ] One `EffectiveArgumentsOf` (or tagged equivalent) per site.
- [ ] `ThisParameterCallResult` / `ApplyCallResult` / `BindCallResult` / `thisParameterCallBindKnown` deleted as value APIs.

### Q3

- [ ] `CheckCallArgumentObligations` = `CheckContractArguments` + `CheckBuiltinContracts` only.
- [ ] `CheckBuiltinContracts` not a preface inside `ReadBuiltinCall`.
- [ ] Schema parse 7001 stays in schema library arm with `EvaluateParseOutcome`; not duplicated in Q3.

### Q4–Q6

- [ ] `WhatRunsNow` sole dispatch key in `DetermineCallProducts`.
- [ ] Generator → `RunsNothingGenerator`.
- [ ] `arr.map(cb)` → `RunsCallbackNow`.
- [ ] BodyProducts arms: RecoverPure / constant-write / complete Lean / memo / inline / object-literal / class.
- [ ] Complete Lean always pairs WorldFromSummaryEffects.
- [ ] Object-literal WriteBack vs class ForgetThrough as separate arm Worlds.
- [ ] Admission sole Q6 fallback; `readUnmodeledMethod` declines.
- [ ] MeetKnown only on body+declared.
- [ ] Library rows return `*CallProducts` or nil.

### Docs

- [x] This file; `IR.md` points here; `AGENT-BRIEF.md` Walk seams rewritten to questions/products (peel / complete Lean / World phases / parse Report site / AdmitFromPrepared).

---

## 6. Intentional fixes vs must preserve

### Intentional

1. Served Lean Value without World.
2. Parallel this-parameter inliner → BodyProducts.
3. Parallel tagged-template spine → one spine.
4. `readUnmodeledMethod` always-answer → declines.
5. `.call` stolen by builtin method tail → Q1 peel.
6. Porous as `ValueFromBody` → refused.
7. Generator body as call value → Q4 kind.
8. Apply `!exact` after evaluating thisArg → no nil re-walk.
9. Replay-style same-root write-then-havoc resurrection → phase order (havoc wins).

### Must preserve

MeetKnown on body+declared; serving rule (complete only); Exact rest/array
TOP decline; object-literal `bump()` WriteBack vs class ForgetThrough;
ClassMethodWalkCall this-binding; JoinSinkSummarized; InlineStoredClosure
const-only; hand-over havoc when not run-now; bodiless + Admission;
parametric `identity<T>`; Map.get havoc+worn; z.parse 7001 ∥ worn return;
safeParse reifies throw; re.exec/match maybe-opaque; apply unevaluated
decline for unsafe argArray; generator Opaque + args once + drain;
super ForgetThisHeld; callback folds; snapshots; AsCalleeResult; memo +
callResultsKept; silent inner walks; IR behavior; reason-note sentences.

---

## 7. Gate

```text
go test -C packages/refinedts/refined-ts-go ./internal/refinedts/walk/
pnpm go:recharts:wall
```

Untraced wall only.

Call-result API deletions (`*CallResult`, `ReadBuiltinCall` signature,
`EvaluateCallExpression` shape, etc.) are **walk-only** imports. Walk
tests that named those APIs must be rewritten; then walk `go test`
covers them.

If this commit also changes symbols `service/` imports
(`InlineCallback`, `StandardAnalyzers`, `SetEngineKernel`,
`SetTransferKernel`, `CallSiteBindings`), add:

```text
go test -C packages/refinedts/refined-ts-go ./internal/refinedts/service/
```

Full `./internal/refinedts/...` is not required by the import graph for
call-API deletions alone. Kernel proofs stay out.

### Q-boundary tests

| Test | Fails if |
|---|---|
| `TestPrepareCall_CallPeelBindsThisAndRest` | `.call` still builtin/unmodeled job |
| `TestPrepareCall_ApplyExactArrayPositions` | apply peel lost |
| `TestPrepareCall_ApplyUnsafeArgArrayEvaluatesNothing` | unsafe argArray evaluated before decline |
| `TestPrepareCall_ApplyInexactXsAfterThisArgDoesNotRewalkThis` | `computeThis()` runs twice (`!exact` hole) |
| `TestPrepareCall_BindConstConcatenatesPartials` | bind peel lost |
| `TestApplyWorldUpdate_HavocWinsOverWriteBackSameRoot` | write-back after havoc resurrects |
| `TestDetermineCallProducts_ReturnsWorldWithValue` | value without World |
| `TestCompleteLeanSummary_PairsReceiverForget` | complete summary, receiverTouched, empty World |
| `TestCompleteLeanSummary_PorousDeclines` | porous as ValueFromBody |
| `TestMeetKnown_OnlyOnBodyArm` | MeetKnown on library overlay / stated replaces recovered |
| `TestReadUnmodeledMethod_DeclinesUnknownUserMethod` | always-answer residue |
| `TestGenerator_RunsNothingNow` | generator body walks at call |
| `TestAdmission_UsesPreparedAfterEval` | Admit re-walks effectful args |
| `TestObjectLiteralMethod_WriteBackNotForget` | `person.age` wiped |
| `TestClassServedSummary_ForgetsReceiver` | stale Keys |
| `TestParseThrow_ReportedInLibraryArm` | 7001 missing or duplicated in Q3 |
| `TestSafeParse_ThrowIsValueNotDiagnostic` | throw reported as 7001 on safeParse |
| `TestTaggedTemplate_UsesSameProducts` | private tagged spine |
| `TestPeelUsesBodyProducts` | `f.call` ≠ this-bound body products |

---

## 8. Rollback

One commit / one branch merge. Gate fails → `git revert` that commit (or
abandon the branch). No partial revert. No patch-forward that brings back
a parallel inliner, always-answer unmodeled tail, value-without-world, or
`!ok → AdmitCall(site)` after effects.

---

## 9. Snippet

```go
func EvaluateCallExpression(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	prepared, ok := PrepareCall(ctx, env, e)
	if !ok {
		products := AdmitCallUnevaluated(ctx, env, e)
		ApplyWorldUpdate(ctx, env, products.World)
		return products.Value
	}
	CheckCallArgumentObligations(ctx, env, prepared)
	products := DetermineCallProducts(ctx, env, prepared)
	ApplyWorldUpdate(ctx, env, products.World)
	return products.Value
}

func DetermineCallProducts(ctx *FlowContext, env Env, p PreparedCall) CallProducts {
	switch WhatRunsNow(ctx, p).Kind {
	case RunsNothingLibrary:
		return LibraryRowProducts(ctx, env, p) // may Report parse 7001
	case RunsNothingGenerator:
		return GeneratorProducts(ctx, env, p)
	case RunsCalleeBody:
		value, world := BodyProducts(ctx, env, p)
		return CallProducts{Value: MeetWithDeclaredReturn(p, value), World: world, Provenance: ValueFromBody}
	case RunsCallbackNow:
		return HigherOrderProducts(ctx, env, p)
	case RunsNothingNoBody:
		return DeclarationOnlyProducts(ctx, env, p)
	default:
		return AdmitCallFromPrepared(ctx, env, p)
	}
}

func ApplyWorldUpdate(ctx *FlowContext, env Env, w WorldUpdate) {
	for _, op := range w.WriteBacks { /* UpdateTrackedEnv / WriteBackParameter */ }
	for _, op := range w.Forgets { /* ForgetThrough / ForgetThisHeld */ }
	for _, op := range w.Havocs { /* HavocEnv */ }
}
```

---

## 10. Choosing-work

- **Afterward:** intentional fixes in §6; no undetermined-count goal.
- **Success:** one spine, Q-boundary tests, untraced wall stable except named fixes.
- **Not success:** intermediate green builds (there are none).
