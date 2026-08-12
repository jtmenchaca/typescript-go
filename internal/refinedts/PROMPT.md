# The port-agent prompt

The exact prompt a porting agent receives, with `<DIR>` / `<PKG>` /
`<DEPS>` filled per directory. Iterate HERE — every prompt change is a
diff in this file, and the change note at the bottom says why each
line exists.

## Template

```
Port one directory of the RefinedTS checker from TypeScript to Go,
1:1. You are the porter; do all the work yourself with your own
Read/Write/Bash calls, starting now.

Step 1: Read
/Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go/internal/refinedts/PORT.md
in full and follow everything in it — conventions, layout, build/test
commands, shell rules, and the report format.

Step 2: Port the whole directory
  /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-typescript/<DIR>/
to
  /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go/internal/refinedts/<PKG>/
as `package <PKG>` (Go package names cannot hold underscores; FILE
names keep their snake_case 1:1 names). The target directory does not
exist; create it. Every .ts becomes its .go twin, every .test.ts
becomes _test.go beside it with the same cases, TS case names as
subtest names, table-driven where the TS file enumerates cases.

Imports allowed: <DEPS>, and the Go standard library — nothing else of
RefinedTS exists yet. If a file imports something outside that set and
this directory, port the file's independent parts and list what was
blocked in your report; do not invent stubs silently. Do not create
files that have no .ts twin (no extracted helper files — the 1:1 rule
covers file boundaries too).

Step 3: Verify:
  go build -C /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go ./internal/refinedts/...
  go test -C /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go ./internal/refinedts/<PKG>/
  go vet -C /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go ./internal/refinedts/...
All three must pass; fix until green. Only touch files under your own
<PKG>/ directory — other agents own other directories, and a shared
file conflict is a report item, not something to resolve yourself.

Step 4: Return the report PORT.md specifies: files created, tests
ported vs passing, the 1:1-impossible list, and anything you had to
search for that PORT.md should have told you.
```

## Waves run so far

| wave | dirs | agent model |
|---|---|---|
| practice 1 | primitives | sonnet |
| practice 2 | refinement_sets | sonnet |
| wave 1 | kernel_bridge, abstract_domain, dataflow_facts, type_reading, comparison, silence | sonnet |

## Why each line is there (change log)

- "You are the porter; do all the work yourself … starting now" —
  practice 2's first run answered as a bystander ("I'll wait for the
  port agent to finish") after reading a prompt that opened with "You
  are porting…" and deferred instructions. Imperative opening +
  explicit self-execution killed it.
- "The target directory does not exist; create it" — a fresh, owned
  directory per agent. Practice 2 also taught: never RESUME a porter
  whose completion looks wrong — a resume can leave two continuations
  of the same task racing in one directory. Replace, never resume.
- "Only touch files under your own <PKG>/" — wave safety: exclusive
  directory ownership is the concurrency rule. The one shared file
  agents may want is checker/exports.go (adding wrappers); that is the
  named exception in PORT.md and collisions there are report items.
- "port the file's independent parts and list what was blocked" — lets
  waves run broader than the strict dependency order without agents
  inventing stubs.
- "no extracted helper files" — the race in practice 2 surfaced an
  unexplained set_equality.go; file boundaries are part of 1:1.
- Report's "what you had to search for" — PORT.md self-improves; the
  practice rounds each converted ~45 discovery tool-calls into
  briefing lines.
