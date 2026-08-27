# Recharts gap plan — refined-ts-go vs tsgo

**Gauge (untraced):** `refinedts-check -wall -list …` → **WALL ~3.9s**
on 260 recharts `src/` files (2026-08-12). Bare `tsgo --noEmit` on the
same project ≈ **1.0s**. Target band: **~0.7–1.0s** (shape parity + a
thin refine band).

**What the checker will do after this work that it cannot do now:**
finish a full recharts refine sweep in roughly the same wall as shape
alone — not by special-casing recharts files, but by cutting the two
residual walk amplifiers that set the straggler floor.

**Defect kind chosen over:** wrong answers / unsoundness are not the
issue; this is **speed**, floored by algorithmic residual cost after
the read-once ports landed.

**Success shows as:** untraced WALL on the 260-file list (agent-
controlled). Traced ms are ranking-only — absolute times inflate under
`recordsMu` convoy + `runtime.Stack` in `ActiveFileDetail`.

---

## Diagnosis (measured)

### Phase ownership

| Phase | Role |
|---|---|
| program build | tens of ms — noise |
| `GetSemanticDiagnostics` | ≈ tsgo — **not the gap** |
| `programFacts` | sub-second CPU — noise |
| kernel | ~50 asks / ~0.15s — **not the gap** |
| `pass3.contractBodies` | **the gap** (~3–4s refine-only wall) |

`GOMAXPROCS` 1→4 helps (~2×); 4→8 almost flat. Wall is **straggler-
bound** under exclusive checker leases, not “need more cores.”

### Two diseases (same corpus, different mechanisms)

Use `-trace-time` **per-file** for mechanism; untraced WALL for product.

#### Disease A — join-owned file (`CartesianAxis.tsx`)

Single-file `-trace-time` (ratios trusted; ms inflated):

| Signal | Value |
|---|---|
| bodies | ~all `callSiteJoin` (~6168ms traced) vs `analyzeFunction` (~16ms) |
| `join.declared` | 6 |
| `join.reach.snapshot` | **0** |
| `join.reach.fallback` | 4 |
| `inlineContractCall` during that join | 789 (memo hits only 52) |
| Dominating inlines | `getSize` 356, `isVisible` 348 |

Helpers (`AxisLine`, `getTickLineCoord`, `TickItem`, …) are
non-exported `function` declarations with unstated params → each pays
`CallSiteBindings` / `declaredJoin` before its body walk.

**Why snapshots miss:** `walkContractBodies` schedules CALLERS-FIRST
over a call graph that only records **identifier `CallExpression`**
edges (`check.go` scan). These helpers are invoked as **JSX /
component** uses from `React.forwardRef(…)` — those edges are invisible
to the Kahn graph. Helpers stay in-degree 0, run in registration
order, and join **before** any owner walk records `CallSnapshotOf`.
Fallback is `AnalyzeToToken` + `evaluateExpression` on args → nested
`inlineContractCall` storm. That is residual cost of the same
algorithm, not a missed TS port (inline memo, snapshots, declaredJoin
memo already exist in Go).

#### Disease B — body/inline-owned files (`Sankey.tsx`, Grid, Text, …)

| Signal | Value |
|---|---|
| bodies | ~all `analyzeFunction` |
| Hot contract | `KindArrowFunction@11401` ≈ `computeData` (layout) |
| inlines | 6,250; memo hits 3,511 |
| **`inline.unkeyed.centerY`** | **1,371** |
| `inline.unkeyed.getSumOfIds` | 504 |
| `inline.unkeyed.getSumWithWeightedSource` | 392 |

Unkeyed = `computeInlineMemoKey` returned `""` because
`json.Marshal` failed on arg/observed AbstractValues (compiler
objects / non-JSON shapes). Every miss re-walks the callee body.
This is the speed-ladder **2b** residual, now standing next to a
~1s shape front end instead of a multi-second tsc column.

`CartesianGrid` / `Text` / `getTicks` are the same disease class
(heavy arrows / named contracts + inline volume), not a third
mechanism.

### What is already landed (do not re-solve)

- Goroutine-per-entry `CheckFiles` + checker pool leases
- Call-site snapshots + callers-first body order (works when the
  graph sees the caller)
- `declaredJoin` / inline memos + `inProgress` recursion guard
- Covering-tsconfig upward walk (`jsx: react`) — nested files no
  longer burn on TS6142

### Parallelism floor (separate from the two diseases)

Exclusive `GetTypeCheckerForFileExclusive` → wall ≈ max over pool
slots of Σ(files on that checker). Finishing
`parallel-sweep-audit.md` (context-carried walk state) or process
shards (ladder 5a) multiplies whatever remains after A/B; it does
not replace cutting straggler work.

---

## Plan — sequenced levers

### 0. Instrumentation hygiene (keep cheap)

Already added: per-file `callSiteJoin` / `analyzeFunction`, join
counters (`join.declared|callback|memo.*|reach.*`), inline ledger
(`inline.<name>`, `freshkey.*`, `unkeyed.*`).

**Rule:** diagnose with single-file `-trace-time`; ship gauges with
untraced `-wall`. Do not trust traced absolute ms for “are we at
0.7s.”

Optional follow-up (only if tracing stays on for parallel sweeps):
per-goroutine counters / drop `runtime.Stack` from the hot path so
trace mode stops lying about wall.

### 1. Disease A — make joins see their call sites (first cut)

**Goal:** `CartesianAxis`-class files: `join.reach.snapshot ≫
fallback`, and `callSiteJoin` wall collapses toward noise.

| Step | Move | Gauge |
|---|---|---|
| 1a | Extend the entry call-graph scan so **JSX tags / component
uses** of a local `function` create the same caller→callee edge as
`f(...)` (or equivalently: treat the enclosing component arrow /
`forwardRef` body as a caller for those helpers). | On
`CartesianAxis.tsx` alone: `reachSnap` rises, `reachFallback` → 0,
untraced single-file time drops. |
| 1b | If some sites still miss: record snapshots for the queryable
shapes those sites use, or seed joins from already-walked owner
envs without a full `AnalyzeToToken`. | `snapshot.miss` / 
`join.reach.fallback` on the file. |
| 1c | Index identifier→uses once per entry (binder / syntactic-
facts style) instead of `visit(p.Entry)` per `declaredJoinUncached`.
Secondary after 1a — cold cost O(decls × AST). | Join CPU on files
with many helpers; mongo-style contracts. |

**Do not:** special-case `CartesianAxis` or skip joins for JSX
helpers without a sound substitute.

**Expected product effect:** removes the worst straggler class from
the parallel floor (CartesianAxis was #1 under traced sweeps).

### 2. Disease B — stop unkeyed / freshkey inline blow-ups

**Goal:** Sankey/Grid/Text-class: inline count and self-time fall
without changing judgments.

| Step | Move | Gauge |
|---|---|---|
| 2a | **Why `centerY` / `getSumOfIds` unkey:** fix AbstractValue
(and observed-env) spelling for memo keys — pointer/canonical id
instead of `json.Marshal` on values that hold checker objects
(read-once **2e** / speed-ladder **2b**). | `inline.unkeyed.centerY`
→ 0 on Sankey; memo hit rate up; untraced Sankey + full WALL. |
| 2b | Read `inline.freshkey.*` leaders after 2a; close genuine
env-evolution leaks only where the ledger names them. | Per-callee
freshkey counts. |
| 2c | Longer arc (ladder **S5**): kernel-side transfer summaries so
repeat inlines become asks over argument sets — only after 2a/2b
stop the free wins. | prisma/recharts inline self-time. |

**Feedback loop:** single-file Sankey `-trace-time` for ledger; untraced
full list for WALL. Fixes must move other heavy arrows
(CartesianGrid `@13619`, Text `@8368`) the same way — prove breadth
on a second file before declaring the lever done.

### 3. Parallel / shard multiplier (after stragglers shrink)

| Step | Move | Gauge |
|---|---|---|
| 3a | Process shards (speed-ladder 5a) — N processes, partition
files, one program+kernel each. Zero audit. | WALL ÷ roughly
shard count until stragglers dominate a shard. |
| 3b | Finish `parallel-sweep-audit.md` — walk state into
FlowContext; mutex caches — so in-process width can exceed “one
lease per checker affinity.” | GOMAXPROCS curve: 4→8 should keep
scaling once stragglers are short. |

### 4. Explicit non-goals (this plan)

- Kernel / Lean decider work — already ~ms on this corpus.
- Shape / tsgo front end — already ≈1s.
- Recharts-only shortcuts, silence of real RTS fires, or new
  vocabulary schemes.
- Trusting traced SELF-TIME totals as the product timer.

---

## Sequencing recommendation

```
1a (JSX/component edges for callers-first)
    → untraced WALL + CartesianAxis single-file
2a (unkeyed memo spelling, led by centerY)
    → Sankey + full WALL
2b (named freshkey leaks)
3a shard if still above ~1.5s and stragglers are short
3b audit only if in-process width is the remaining story
```

**Why this order:** Disease A is a **scheduling/coverage defect**
(snapshots exist but never hit) — high confidence, localized to the
graph scan + join order. Disease B is the known **inline volume**
residual; unkeyed counts name the first patch. Parallelism without
cutting A/B only multiplies a 4s floor into “still multi-second on
the heavy shard.”

### Options for the first implementation unit

a) **1a only** — JSX/component call-graph edges; gauge CartesianAxis
   + full WALL.  
b) **2a only** — fix inline memo key spelling for unkeyed leaders;
   gauge Sankey + full WALL.  
c) **1a then 2a in one batch** — both levers, one WALL at the end.

**Recommend (a):** CartesianAxis is the clearest “snapshots should
have fired” failure (`reachSnap=0`); fixing the graph makes the
existing read-once machinery work as designed. Then (b) with a
Sankey ledger in hand.

---

## How to re-measure

```bash
# product timer
refinedts-check -wall -list /tmp/recharts-files.txt

# disease A
refinedts-check -trace-time=/tmp/cartesian-axis-trace.txt \
  …/cartesian/CartesianAxis.tsx
# look: join.reach.snapshot vs fallback; callSiteJoin vs analyzeFunction

# disease B
refinedts-check -trace-time=/tmp/sankey-trace.txt \
  …/chart/Sankey.tsx
# look: inline.unkeyed.* / inline.freshkey.* / slowest contracts
```

---

## Correspondence

| Document | Role |
|---|---|
| `parallel-sweep-audit.md` | what blocks wider in-process parallelism |
| `findings/read-once.md` (TS) | why snapshots / memos exist |
| `findings/speed-ladder.md` (TS) | 2b inline volume, 5a shards, S5 summaries |
| This file | Go residual after port + parallel CheckFiles; path to ~tsgo wall |
