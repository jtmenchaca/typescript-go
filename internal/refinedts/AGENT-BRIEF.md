# The fix-agent briefing supplement

PROMPT.md is the port-agent prompt; this file is its twin for FIX
agents — the facts every fix brief points at so agents stop
re-discovering them. Iterate HERE: every agent report ends with
"anything you had to search for that the brief should have told you,"
and each such item becomes a line below with the wave that earned it.

Every brief includes: `Read
/Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go/internal/refinedts/AGENT-BRIEF.md
in full before touching code.`

## Resolution and AST facts

- **Two SEPARATE root-recognition questions exist for a z.* chain, and
  they used to disagree.** `resolvesToAnnotationRoot`
  (annotations/chain_roots.go) — `resolvesToSurface(p, node) ||
  libraryAdapterOfNode(p, node) != nil` — is what `CompileAnnotation`
  asks to COMPILE a chain to its set (this always recognized the
  checker's own vendored surface, `refined-ts-typescript/surface/z.ts`
  / `p.SurfacePaths`). A second, narrower question lived inside
  `readSchemaRuntimeCall`'s exact-parse pipeline
  (walk/schema_runtime_models.go's `isZodRootHere`,
  walk/parse_evaluator.go's `run`/`runRoot`): it asked ONLY
  `libraryAdapterOfNode(...)?.name === "zod"` — real npm zod — in BOTH
  the TS source (evaluation/schema_runtime_models.ts's inline
  `isZodRoot` callback) and the original Go port. A schema built off
  the vendored surface (`z.number()` from `surface/z.ts`, not npm
  zod) therefore compiled fine (chain vocabulary correct) but its
  `.parse(arg)` calls answered "no claim" unconditionally —
  `EvaluateParseOutcome` never entered `runRoot` at all, for ANY
  argument, in-set or out. Fixed by widening `isZodRootHere` to the
  same union `resolvesToAnnotationRoot` already asks (checks
  `p.SurfacePaths` first, then `LibraryAdapterOfFile`). Before
  reasoning about "does the checker recognize this schema", check
  BOTH questions separately — compile-time recognition being right
  says nothing about the runtime/parse-eval side.
- **A PROVEN throw on `.parse`/`.parseAsync` is computed but was never
  reported.** `EvaluateParseOutcome` (walk/parse_evaluator.go) already
  decides `outcomeKindThrows` for an out-of-set exact argument — the
  whole `runRoot`/`runCheck` machinery mirrors zod's runtime exactly —
  but `readSchemaRuntimeCall` (walk/schema_runtime_models.go) only
  ever CONSUMED that outcome kind inside the `safeParse` branch
  (reifying it as `{success:false}`). For plain `.parse`/`.parseAsync`
  the throw fell straight through to the "unmodeled call" fallback,
  which returns the schema's OWN stated set (`WornOfObject`/
  `WornOfAnnotation`) with no diagnostic at all — an out-of-set
  literal argument type-checked silently against the declared return
  type. Same gap in the TS source (evaluation/schema_runtime_models.ts
  never calls anything on `outcome.kind === "throws"` outside the
  safeParse arm either) — not a porting slip, a genuine unhandled
  case in both trees. Fixed by reporting 7001 at the argument node
  when `hasOutcome && outcome.Kind == outcomeKindThrows`, before the
  existing fallback runs (the fallback's RETURN VALUE is unchanged —
  only the missing diagnostic was added).
- **The chain-method vocabulary (gte/gt/lte/lt/enum/union/regex/
  startsWith/length-window) was ALREADY fully compiled correctly** in
  both trees before any of this — annotations/chain_numeric_method.go
  maps gte/min→AtLeast, lte/max→AtMost (closed rays), gt→Above,
  lt→Below (STRICT rays) exactly per the surface
  (number_schema.ts/refinement_forms.ts); z.ts's `enum` literal-for-
  literal matches chain_root_constructor.go's `case "enum"`; `.regex`/
  `.startsWith` compile through FormatGrammar/StartsWithSet
  identically on both sides. Do not re-derive or re-verify this part
  again without a specific reason — the gap was never here, it was
  entirely in the `.parse` runtime-evaluation path above (both roots
  of a "chain vocabulary" task usually turn out to be the SAME
  underlying gap, since every `.parse(arg)` fixture row exercises the
  compiled chain only through the runtime path).

- **Alias-following symbol resolution is `symbolAt(checker, node)`**
  (walk/cast_and_await.go) — it follows `ast.SymbolFlagsAlias` through
  `GetAliasedSymbol`. A raw `GetSymbolAtLocation` misses every import
  alias; three separate agents found this independently.
- **`Unwrapped(node)`** (walk/tracked_bindings.go) peels parens, `as`,
  `!`, `satisfies`. Use it before any receiver/callee shape test.
- **The `const` flag lives on the VariableDeclarationList** — a
  VariableDeclaration's PARENT carries `ast.NodeFlagsConst`, not the
  declaration itself.
- **With-scope gating**: the binder stamps
  `ast.NodeFlagsInWithStatement` on every node under a with body; gate
  with `node.Flags&ast.NodeFlagsInWithStatement != 0` (the host
  checker's own pattern).
- **`ClosureWriteSlots` takes the closure NODE, never `.Body()`** — a
  bare block reads as an empty write set (pinned lesson).
- **Write-set scanners**: `AssignedNames` resolves element/property
  write ROOTS (`xs[0] = …` counts `xs`, via `TargetNames`) and counts
  non-read-only method receivers; `CallMediatedWrites` adds closed-over
  names written by in-view callees.

## Walk seams

- **The analyzer triple is `walk.StandardAnalyzers()`** (exported).
  `evaluateExpression` is unexported; never rebuild the triple by hand.
- **The serving rule**: only a COMPLETE summary serves; porous never
  serves (walk/kernel_summaries.go — measured 14× regression when
  violated). Changes near `applySummary` are performance-sensitive;
  the recharts wall sweep is their gate.
- **Callback re-entry guard**: `walkingCallbacks` /
  `walkingCallbackKey` (walk/promise_instance_models.go) — every
  handler/callback inline goes through it or a matching `ctx.Inlining`
  guard.
- **The one syntax-decline table** is walk/syntax_models.go; per-site
  decline sentences are `ReasonNote`s (assignability/decline_reasons
  twin).
- **Walk-route `()` / tagged-template org chart** is
  `walk/CALLS.md` — six questions (who / args / allowed / what runs /
  world / value), products Value∥World, Prepare → obligations →
  DetermineCallProducts → ApplyWorldUpdate. Not a plugin cascade; not
  “library / body / declared” as the spine (that legend sits under Q6
  only). World apply order is WriteBacks → Forgets → Havocs. After any
  effectful eval, Admission uses prepared values — never
  `!ok → AdmitCall(site)` re-walk. Schema `.parse` 7001 lives with the
  library arm that runs `EvaluateParseOutcome`, not in the static Q3
  door. Target end state: library rows return `*CallProducts` or nil;
  `readUnmodeledMethod` declines (residue is Admission’s). Until that
  rewrite lands, today’s fall-through spine is still
  `evaluate_call_expression.go`.
- **Builtin dispatch** (today) is a chain in walk/builtin_models.go —
  add new readers as one chain insertion; each reader declines with
  nil. Under CALLS.md end state these become library-row products
  behind `WhatRunsNow`, not a competing call cascade.
- **The comparison decider lives at walk/comparison_decision.go** (package walk) —
  there is NO internal/refinedts/comparison/ directory; the placeholder package named
  in older docs was deleted at integration.
- **TestOf** (walk/ir_guard_single_head.go) has NO arm for loose `!=` at all — only
  `===`/`!==`/`==` are read; loose `== null`/`!= null` deliberately never lower to a
  flavored test (true of both absent values).
- **TypeofRead** (walk/ir_guard_typeof_read.go) distinguishes quoting `"undefined"`
  (`IsUndefinedQuote` — lowers eqUndef; typeof null is `"object"`) from quoting the
  slot's own tag (lowers `IrTestDefined`, correct there).
- **Destructuring defaults and function parameter defaults** fire on exactly-undefined,
  never null (spec.html ~10111 "If |Initializer| is present and _v_ is *undefined*") —
  their lowerings use `IrTestEqUndef` with the default on the Then arm
  (ir_object_slots_destructuring.go, ir_summary_body_lowering_default_prelude.go). The
  `??` lowerings correctly stay `IrTestDefined` (`CoalesceExpression` is either-absent).
- **walk/ sort and state-admission are orthogonal**: a `BindingKindNumber`/`String` slot's
  kernel state can still carry a null/undef admission — never argue soundness from "the
  sort excludes absence".

## Compound-assignment facts

- **`compoundOperator` (walk/assignment_operators.go) only ever mapped
  the five `NumericOperator` tokens (`+= -= *= /= %=`) — no bitwise
  compound (`&= |= ^= <<= >>= >>>=`) reached `TransferBinary` at all,
  even though `TransferBitwise`/`BitwiseOperator`
  (walk/bitwise_transfer.go) were fully built and kernel-wired.** A
  bitwise compound token fell to every arm's own `default:
  next = silence.Residue()`, poisoning the target for every later read
  in the same body. The fix is a SEPARATE table
  (`compoundBitwiseOperator`, same file) mapping the six tokens to
  `BitwiseOperator` — `NumericOperator` and `BitwiseOperator` are
  different Go string-enum types over different kernel question
  shapes, so this can never be one table. `ir_assignment.go`'s
  `compoundOps` (the IR/loop-lowering route) already carried all eleven
  tokens plus `**=` in one `map[ast.Kind]kernelbridge.LoopEffectOp` —
  that map was the tell that the walk route's five-token table was
  incomplete, not evidence the walk route already covered bitwise.
- **The one arithmetic choice every compound-assign route makes
  (string-concat for `+=`, the five `NumericOperator` forms, the six
  bitwise forms, `**=`, or silence) is now factored into one function,
  `compoundResult` (walk/assignment_operators.go) — every new compound
  route (an element compound, an accessor compound) calls it rather
  than re-deriving the same switch.** Before this pass, the identifier
  arm and the plain-property arm each hand-kept an identical
  `switch { case concatenated != nil: … case hasOp: … case
  KindAsteriskAsteriskEqualsToken: … default: … }` — a third copy for
  every new route was the wrong direction.
- **A compound through an ElementAccessExpression (`a[i] += e`) matched
  NO arm before this pass — not `ReadAssignment` (gates on an
  identifier or property-access LEFT only), not `ReadIndexedWrite`
  (index_operators.go, gates on `ast.KindEqualsToken` only) — so it
  fell all the way to `ReadForgottenAssignment`, which evaluates the
  right side, forgets the whole receiver, and returns with NO judge
  ever run.** This is a silent MISS on an out-of-set compound element
  write, not a decline — nothing reports, nothing narrows. The fix,
  `ReadIndexedCompoundWrite` (index_operators.go), is a new dispatch
  member inserted into `ReadBinary`'s chain (evaluate_operators.go)
  right after `ReadIndexedWrite`, mirroring its exact-tuple/in-bounds
  gate and reusing `WriteElement` (assignments.go) — the same judge a
  direct `a[i] = v` write already runs — against the COMPUTED value.
- **`ConstructedInstance`'s own header comment says it outright:
  "Incomplete either way — methods and getters are keys too."** A
  get/set accessor pair is NEVER added as an object key by
  `ConstructedInstance` (constructed_instance.go) — only
  `PropertyDeclaration` fields are. So `box.age` where `age` is an
  accessor reads as an absent key on the walk route, and — critically —
  a COMPOUND write through it (`box.age += 5`,
  `ReadAssignment`'s property-compound arm) used to read that absence,
  compute garbage, and `WriteProperty` it back as a FRESH unknown-valued
  key named "age" sitting beside the real backing field — the setter
  never ran, so a backing field the setter writes
  (`this.held = value`) never moved. This is silent by omission, not a
  decline: nothing before this pass distinguished "no key because no
  accessor" from "no key because untracked."
- **`AccessorDeclarationsOf` (walk/ir_accessor_calls.go) already takes
  a bare `*FlowContext`, not a `*LoweringContext`** — despite every
  existing CALLER of it living in an `ir_*.go` file and taking
  `*LoweringContext`. This is the tell that the accessor-PAIR
  RESOLUTION itself was already walk-route-shaped; only the CALL
  machinery around it (`GetterReadEffect`, `SetterWriteStatements`,
  `setterReadModifyWrite`) is IR-specific (kernel summary blobs, temp
  allocation, `context.Hoisted`). The walk-route fix
  (`AccessorWalkReadModifyWrite`,
  ir_accessor_calls_read_modify_write.go) reuses
  `AccessorDeclarationsOf` unchanged and builds its OWN, simpler
  read-modify-write beside the IR one in the same file: a fresh call
  environment per half, `this` bound directly to the receiver's own
  `KindObject` value (`env.Set("this", receiver)`), the getter's
  `return` collected through `ReturnSink` exactly as
  `InlineStoredClosure` (walk/inliner.go) collects a block body's
  value, the setter's writes captured through `ThisWriteSink` exactly
  as a `this.key = value` write already sinks
  (assignment_operators.go), folded back with `setObjectKey`
  (index_operators.go) — the same key-rebuild `WriteProperty` already
  runs for a `this.key = v` write, applied to the OUTER receiver
  instead of `this`.
- **Binding `this` directly to a `KindObject` value in a synthetic env
  (`env.Set("this", receiver)`) is the ESTABLISHED way to make
  `ReadThisPropertyAccess` (walk/this_property_access.go) step ASIDE
  in favor of the general object-key read.** Its own
  `readThisFieldInvariant` explicitly checks
  `thisIsObject := hasThis && held.Kind == abstractdomain.KindObject`
  and declines (`return nil`) when true, with the comment "…unless the
  walk HOLDS a narrowed `this` object: then the general property read
  below answers through it" — the fallthrough is `ReadObjectKeyAccess`
  (object_key_access.go), which reads `this`'s own `.Keys` directly.
  This is NOT a special case built for the accessor route; it already
  existed for narrowed-`this` reads, and the accessor route's synthetic
  binding rides it for free — no new gate needed.
- **The `&&=`/`||=`/`??=` LogicalAssignment algs
  (sec-assignment-operators-runtime-semantics-evaluation,
  tmp/ecma262/spec.html) never call `PutValue` on the branch that keeps
  the left side unchanged** — `&&=`: "If ToBoolean(leftValue) is false,
  return leftValue" with NO further steps; `||=` the truthy-dual; `??=`:
  "If leftValue is neither undefined nor null, return leftValue." The
  right side is not merely unwritten on that branch — it is UNEVALUATED,
  so a sound walk-route reading must not evaluate `bin.Right` before
  knowing which branch a DECIDED left verdict takes (unlike every
  ordinary compound token, where both sides always evaluate). The
  existing bare `??` reading in `ReadBinary`
  (evaluate_operators.go:239) already has this exact shape
  (`if leftKnown.Kind == abstractdomain.KindUndef { return
  evaluateExpression(ctx, env, bin.Right) }` — the right side's
  evaluation is INSIDE the branch, not hoisted above the test) and is
  the template the `??=`/`&&=`/`||=` assignment arm
  (assignment_operators.go) copies.
- **Evaluating a compound's right side speculatively before knowing
  whether the chosen route will use it risks a DOUBLE EVALUATION of an
  effectful right side if a fallback arm re-evaluates it on decline.**
  The accessor-compound arm in `ReadAssignment` therefore checks
  `AccessorTargetOf` (a pure, effect-free pre-check — resolves the
  receiver and the accessor pair, evaluates nothing) BEFORE evaluating
  `bin.Right` at all; only once that returns true does `bin.Right`
  evaluate once, and `AccessorWalkReadModifyWrite` itself declines only
  where `AccessorTargetOf` already declined (so its own decline path
  never re-runs an effect). Any new compound-assign route that shares a
  fallback chain with an existing "evaluate right unconditionally" arm
  needs the same split (a resolve-only pre-check, then evaluate-once)
  or it inherits this double-eval risk silently.

## Type-reading facts

- **Fixed tuple types read positionally** (typereading/host_type.go's
  tuple branch, before the general array-like branch): every element
  `ElementFlagsRequired` → an exact `KnownList`; optional/rest/variadic
  tuples fall to the general branch (today, a refusal — never claim an
  exact list for a partial tuple). Public accessors needed:
  `IsTupleType()`, `TargetTupleType()`, `ElementFlags()` — all exported
  by `checker.TupleType`, no internal/checker edit needed (PORT.md's
  method list omits the latter two).
- **`hostTypeDepthLimit` (host_type.go) is the ONE recursion budget**
  shared by the peel/union/object gates — do not widen it for a single
  fixture; a chain that refuses usually refuses for SHAPE (e.g. a
  readonly-array-of-words member has no star-over-words reading), not
  depth. Verify by raising the limit temporarily before blaming it.
- **The null/undefined split is DECIDED in comparison_decision.go's strict
  absent-vs-absent rule**: exact `KindUndef`/`KindNull` now decide strict and
  loose equality (spec-cited rows in walk/comparison_decision.go). An exactly-
  undefined read `=== undefined` resolves true; an exactly-null read `=== undefined`
  resolves false. Do not open this rule again.
- **`declaredParamSort`/`declaredParamTypeof` (kernel_summaries.go)
  recognize only keyword types.** Widening them to literal unions
  changes WHICH bodies lower and serve — a previously-declining switch
  starts serving its entry-quantified join, and an EXACT-argument call
  then reads the whole union (a wrong answer, worse than the decline).
  Any sort widening there must land together with per-call entry
  narrowing, or not at all.
- **`Object.keys/values/entries` exact answers** live in
  object_static_models.go's `isObjectReceiver` block (complete objects
  → exact key/value lists); `WordTuplesOf`
  (refinementsets/codepoint_sets.go) is the general finite-word-set
  reader (word-union `.length` reads through it in
  evaluate_property_access.go).
- **typereading's absence conflation pattern**: type_node.go/host_type.go build the
  maybe wrapper through `sawAbsent`/`absentFlags` and recipes.go's `PresentUnion` —
  wrappers there are conflated by design; the null TYPE reads as `abstractdomain.Null`,
  undefined/void types as `Undef`.

## Kernel bridge facts

- **`LoadKernel` returns `*kernelbridge.RefinedTSKernel`** (a struct of
  function-typed fields, not an interface, and not `Kernel`).
- **`refinementsets.SimplificationKernel`** is method-shaped; adapt via
  `walk.SimplificationKernelOf(kernel)`.
- **Wire forms**: kernelbridge/wire_format.go (encode) and
  wire_decode.go (decode) must match the Lean decoder byte-for-byte;
  read both sides together before adding any form.
- **KnownStateWire carries split Undef/Null bools**: the send/read seam is
  walk/kernel_delegation.go's `StateOfKnown`/`KnownOfState` (flavored wrappers map to
  single flags; `KindNull` sends Null-only; `KindUndef` sends Undef-only — its producer
  audit is complete, and single-flag-over-∅ wires read back as the exact kind). The kernel
  state wire is `{"set","undef","null","nan","thrown"?}`; a legacy `{"absent": bool}`
  decodes as both admissions. The effect wire's `constState` carries the same
  `"undef"`/`"null"` pair (`AbsentConst()` = undef-only, `NullConst()` = null-only), and
  `{"varState": i}` is the verbatim whole-state copy — sound ONLY as an assign's whole
  effect, never as an operand.
- **Capture/pattern languages compile through
  `refinementsets.FormatGrammar("^"+src+"$", flags)`** — anchor
  sub-patterns yourself; FormatGrammar alone pads substring-anywhere.
- **kernelbridge IrBranchTest**: new no-operand tests must be added to `StmtWire`'s
  w-suppression guard (the else-if chain at the branch tail of the function, ~line 833
  area) or they emit a spurious "w" field. `IrTestEqUndef` ("js.eqUndef") and
  `IrTestEqNull` ("js.eqNull") exist; their true arm is "the slot IS that absent value".
- **Kernel question wire tracing** (kernel_trace.go): every question funnels through
  `KernelFromCalls`'s `ask1`/`ask2` closures (ask_kernel.go) into one shared `timed`
  seam — call `kernelbridge.TraceKernelTo(func(line string) { t.Logf("%s", line) })`
  (returns a restore closure; `SetKernelTraceWriter` is the same hook without the
  restore) in a test to read exact wires instead of writing a probe test. Three line
  shapes stream, each already newline-terminated by the writer's own call: `refinedts-kernel Q <op> <request-wire>` before the dylib call, `refinedts-kernel A <op> <answer-wire>` after (the raw JSON wire, unreformatted); a cache hit (AskCached, question_cache.go) that never reaches `timed` emits `refinedts-kernel C <op> <key, newlines flattened to " | "> => <cached answer>` — the disk-persisted cache otherwise hides every wire of a question any earlier run asked.
  The CLI carries the same hook: `refinedts-check-bin -kernel-trace <file.ts>` streams
  the lines to stderr LIVE — for a HANG, the diagnosis line is the last Q with no A
  (that is the question the kernel never answered; `sample <pid> 5` then names the
  spinning kernel function).

## Test-harness idioms (walk package)

- **Program-from-source**: `entryEnvTestProgram` (entry_env_test.go) is
  the canonical recipe (vfstest map + bundled.WrapFS + tsconfig +
  BindSourceFiles + GetTypeChecker with t.Cleanup(done)).
- **Kernel-gated tests**: skip when
  `!kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath)`; on
  load, seat all three: `SetEngineKernel`, `SetTransferKernel`,
  `narrowing.SetNarrowKernel` (see super_and_array_ctor_test.go).
- **`FormatAbstractValue` spells an exact scalar BARE** (`40`, never
  `{40}`); a numeric join builds a `KindSet` union form — assert joins
  through `exactWordsOfSet`, not by expecting `KindValues`.
- **Nil-checker contexts crash on any path through `SeededBinding` /
  `AfterReaders`** (they ask `GetTypeAtLocation`); use a real program.
- **FlowContext for tests** needs Registry/Objects/Contracts/Report/
  Aliases/Declared populated (empty maps + swallowed report sink).
- **Test-file conventions**: walk/ model files mix pure and program-based tests per file;
  files with zero direct coverage (evaluate_expression.go, type_seed_answer.go) get their
  tests in the nearest model-file test (e.g. syntax_models_test.go); `CompareKnown`'s
  tests live in walk/comparison_decision_test.go with the `FlowContext{}` zero-value idiom
  (the Undef/Null paths never touch `ctx.Kernel`).

## Abstract-domain facts

- **Null/undefined vocabulary** (post-split): `abstractdomain.KindNull/Null` = exactly null;
  `KindUndef/Undef` = exactly undefined (missing keys, OOB reads, void, uninitialized, the
  undefined literal). The maybe wrapper carries `AbsentSide` (`AbsentFlavor`: zero value =
  conflated, `AbsentFlavorUndefOnly`, `AbsentFlavorNullOnly`) built via `PossiblyAbsent(...)`;
  `PossiblyUndefined` delegates with the conflated flavor. A wrapper with `Inner=Null` means
  "null or undefined".
- **Null-flavor producers with spec cites**: the null literal (walk/evaluate_expression.go,
  walk/type_seed_answer.go) and `String.prototype.match`'s no-match branch
  (walk/string_method_models.go) produce `abstractdomain.Null`; `.exec`'s null rides
  inside a `PossiblyUndefined` wrapper (walk/regex_exec_capture.go).
- **`KindObjectStar` and `array_construction.go` are Go-native — there
  is NO TS counterpart.** `abstract_domain/abstract_value.ts` (the
  PORT.md source of truth) has no `"objectStar"` arm and there is no
  `evaluation/array_construction.ts` at all. PORT.md's "every `foo.ts`
  becomes `foo.go`, transcription never redesign" rule does not hold
  for this corner of the domain — it has already diverged Go-first.
  Treat new AbstractValue kinds/constructors here the same way
  (land in Go beside `KnownObjectStar`/`KnownList` in
  `abstractdomain/known_constructors.go`) unless told to bring the TS
  tree back in sync; do not block on finding a TS file to edit first.
- **A new `Kind` touches a checklist, not one file.** Grep `KindList`
  or `KindObjectStar` across `abstractdomain/` to find it fully:
  `abstract_value.go` (the tag + struct field), `known_constructors.go`
  (the constructor), `lattice_operations.go` (`SameKnown`, `Truthiness`,
  `MeetKnown`, `JoinKnown`, `SetOfKnown`), `format_abstract_values.go`
  (hover rendering), `typeof_words.go` (`KindOfClaim` AND
  `TypeofWordOfKnown` — two separate switches), `intersect_refinements.go`
  (the guard-narrowing whitelist), `memo_spell.go` (the inline-replay
  cache key). Missing an arm here is USUALLY safe (the functions'
  `default` cases degrade to unknown, not a wrong answer) but leaves a
  silent capability gap — check each one rather than assuming the
  default is what you want.
- **A new FIELD on AbstractValue touches a checklist, not one file**: `SameKnown`,
  `JoinKnown`/`MeetKnown`, `memo_spell.go`, format (maybe), plus every call site that
  rebuilds a wrapper from `.Inner` (search `KindPossiblyUndefined` tree-wide) — the
  mirror of the existing new-Kind checklist.
- **The grammar already spells the EMPTY SET — `OneOf []`
  (refinements/grammar.lean:35 + denotation.lean:36) — satisfied by no
  tuple, and emptiness is a canonical decided set function
  (set_functions/emptiness.lean, `RefinedSet.scalarEmptyB`, "A = ∅").**
  `refinements/grammar.lean`'s own header calls `Repeat` "the native
  encoding of length-bounded sequences." A first pass at this array
  problem concluded wrongly that no kernel form could spell an absent
  element and closed off `Repeat`/`OneOf []` entirely — WRONG. The
  correct reading: `RepeatOf(element, lo, hi)` is a claim about a SET
  OF TUPLES whose members are drawn from `element`
  (`RepeatN`, refinements/repeat.lean:32) — so `RepeatOf(∅, n, n)` for
  `n > 0` denotes `∅` ITSELF (no tuple can fill n positions from an
  empty alphabet). That makes `∅` the right claim for "the set of
  PRESENT elements a hole array admits," but wrong as the array's OWN
  `Set`/membership claim — an assignability check reading
  `receiver.Set = RepeatOf(∅, n, n)` would see `∅ ⊆ every target` and
  wrongly ACCEPT the array against any annotation. Keep the length
  claim (`{n}`, `OneOf [n]`) and the element claim (`∅`) as two
  SEPARATE values/fields, never fused into one `RepeatOf` question
  about the array's own denotation.
- **The tree's own frame for "array as two facts" is the two-slot
  vocabulary already in the walk** — `walk/ir_array_slots.go`'s
  `.len`/`.elem` pair (a length slot holding an ordinary number, an
  element slot holding the JOIN of everything written, read
  independently, never fused into one kernel question). That IR frame
  is for lowering array LOCALS inside a function body's kernel
  summary, not for an `evaluateExpression` return value — but the
  concept transfers directly: `abstractdomain.KindArrayHoles`
  (abstract_value.go) mirrors it at the `AbstractValue` layer by
  WRAPPING an existing `KindValues{n}` scalar (in `Inner`, read back via
  `LengthOfArrayHoles`) beside an `ElementSet` field holding the
  kernel's own `∅` (`OneOf(nil)`, the same shape kernel_delegation.go's
  package-private `emptySet` builds) — not a bare `int` beside them.
  `KindObjectStar` (element stated, NO length) and `KindList`
  (every slot materialized, exact length) both existed before this;
  neither carries an exact length beside a stated-but-unmaterialized
  element, which is the position `KindArrayHoles` fills — check first
  whether a value position genuinely needs a new `Kind` at all, or
  whether it can be answered by wrapping `{n}`/`∅` claims through
  carriers that already exist, before adding one.
- **A read-model's own "materialization ceiling" is a per-call-site
  Go constant, not a domain-wide one — check for existing siblings
  before assuming there's a shared bound.** `array_construction.go`'s
  `arrayConstructionHoleLimit` (10,000) and `array_method_models.go`'s
  `readArrayFrom` array-like branch each independently capped
  materialization at the SAME literal `10_000` before this pass —
  discovered only by grepping the literal, not from any shared
  constant. Both now read `arrayConstructionHoleLimit` (same package,
  `walk`, no import needed) — reuse it for any future array-like
  materialization site rather than inventing a new literal. There is
  NO equivalent "string materialization ceiling" constant anywhere in
  the tree (`refinementsets.CodepointsOf`, `String.prototype.repeat`'s
  model in `string_method_models.go` both materialize unconditionally)
  — `arrayConstructionHoleLimit` was reused for `.join()`'s exact-vs-
  windowed decision for lack of a dedicated one; if a genuine string-
  specific bound is ever wanted, it does not exist yet and needs its
  own ask.
- **`Array.from({length: n})` builds a DENSE array of `n` own
  properties holding `undefined` (sec-array.from's array-like branch,
  `CreateDataPropertyOrThrow` at every index 0..length-1,
  tmp/ecma262/spec.html); `new Array(n)` builds `n` SPARSE holes (no
  own property at any index at all).** `KindArrayHoles`' Length/
  ElementSet pair (length `{n}`, present-element-set `∅`) covers BOTH
  soundly for `.length`, an element read, `Array.isArray`,
  `instanceof`, truthiness, spread, for-of, `.join()` — every one of
  those reads identically regardless of density, because none of them
  distinguishes "own property holding undefined" from "no property,
  reads undefined." FIXED (the third bit this entry once said would
  be needed): `AbstractValue.Dense bool` + `DenseKnown bool`
  (abstract_value.go) — `DenseKnown` separates "affirmatively proved"
  from "not established" the same way `ProvedAbsent` does for
  `possiblyUndefined`, because a plain `Dense bool` cannot honestly
  carry both "definitely sparse" (`Object.keys` → `[]`) and "unproven"
  (decline) without one of `object_static_models.go`'s Object.keys/
  values/entries arms overclaiming from a `KindList`-joined value that
  proves neither. `KnownArrayHoles(length, grade, dense)` — every call
  site knows its density affirmatively, so the constructor always sets
  `DenseKnown: true`; only `JoinKnown`'s own struct-literal branches
  (a density DISAGREEMENT between two array-holes at the same length,
  or an array-holes joined with a plain `KindList`, whose `KindUndef`
  items carry no own-property fact at all — a pre-existing gap this
  join inherits) build the struct directly with `DenseKnown: false`.
  `SameKnown`'s `KindArrayHoles` arm now requires `DenseKnown`/`Dense`
  agreement too, which means `JoinKnown`'s own `SameKnown(a,b)` fast
  path (top of the function) is what actually serves the two-arms-
  agree case — the explicit `KindArrayHoles`×`KindArrayHoles` branch
  further down only ever sees a disagreement, so it does not re-check
  density itself (dead code otherwise). `object_static_models.go`'s
  Object.keys/values/entries: sparse answers the exact empty `KindList`
  (`EnumerableOwnProperties`, sec-enumerableownproperties, walks OWN
  keys only — sparse has none); dense `values` reuses
  `KnownArrayHoles` itself as the RESULT (the values are undefined at
  every position either way, AND the result array from
  `CreateArrayFromList`, sec-createarrayfromlist, is itself dense, so
  `Dense: true` carries forward); dense `keys`/`entries` decline — no
  existing vocabulary compresses "n distinct increasing decimal-string
  keys" the way `refinementsets.Repetition` compresses a single-
  character window, and a `KindArrayHoles` receiver's length is ALWAYS
  past `arrayConstructionHoleLimit` (below it, `ReadArrayConstruction`/
  `readArrayFrom` build a plain `KindList` instead), so there is no
  in-range case where materializing the key list is even an option.
- **`RepeatOf(element, n, n)` for a SINGLE-CHARACTER element is a
  legitimate, EXACT string claim** (`refinementsets.Repetition`,
  `array_method_models.go`'s `.join()` past-ceiling arm) — used where
  the content is fully determined (every piece of `new Array(n).join(
  ",")` is the empty string, so the joined result is provably
  `n−1` copies of the separator) but too long to materialize as a
  `KindValues` codepoint array. Caution: `.length` on ANY string-
  shaped `KindSet` answers only the window's FLOOR
  (`evaluate_property_access.go`'s stringy branch), never the exact
  count even when `lo == hi` — a pre-existing imprecision, not
  something this arm introduces, but worth knowing before assuming a
  `Repetition`-carried exact-length string reads its own length back
  exactly. FIXED in the precision-gaps wave below — read that entry
  before assuming this floor still holds unconditionally.
- **The astral-freedom gate for trusting a UTF-16 unit position is
  `refinementsets.AstralFree([]float64)`, but it only takes a
  MATERIALIZED codepoint list** — a `RefinedSet` window (the shape
  `.length`/`.at`/`.slice` all read off a `KindSet` receiver) has no
  such list to hand it. The gate for a `RefinedSet` alphabet is
  `walk.astralFreeSet(refinementsets.RefinedSet) bool`
  (walk/number_range.go, beside `RangeOfSet`): read the set's own sound
  enclosing range and check the upper bound sits below the astral floor
  (0x10000) — a bound (`AtMost(126)`, ASCII) qualifies as readily as an
  exact enumeration (`OneOf([44])`, a comma), with no materialization.
  `refinementsets` keeps its own `astralFloor` constant package-private
  (codepoint_sets.go), so a `walk`-side caller cannot import it and
  restates the literal under its own name (`astralCodepointFloor`) —
  grep for `0x10000` across both packages before assuming a shared
  constant exists; there isn't one.
- **`sequenceOfElements`'s (walk/array_literal.go) scalar-position fold
  is NOT the only place that needs updating for a HOLE-shaped element
  (KindUndef) to fold cleanly — `objectStarOfElements`, checked FIRST
  in the same function, already tolerates it for free.** `KnownObjectStar`
  (abstractdomain/known_constructors.go)'s `objectShaped` gate
  recurses through `KindPossiblyUndefined` on purpose ("a maybe over
  one" is an ordinary graph-shaped reading), and `objectStarOfElements`
  joins every element unconditionally via `JoinKnown` before checking
  `objectShaped` on the RESULT — so `JoinKnown(Undef, aGraphValue)`
  already produces the right maybe-wrapped answer with no change
  needed there. Only the scalar/`RefinedSet` fold path
  (`scalarPositionSet`'s all-or-nothing gate) needed the new Undef
  arm — check whether a "same problem, different fold" site has
  already solved it via an existing general-purpose join before
  patching a second one by hand.

## Doctrine the briefs repeat

- ECMA-262 claims cite the vendored tmp/ecma262/spec.html by clause;
  zod/surface claims cite the vendored surface files themselves.
- Fixture conviction rule: compute the marked line's runtime value BY
  HAND; a marker on an in-set value is wrong regardless of current
  capability; marker comments state the value's relation to the set,
  never capability.
- Shell: one command per Bash call (hooks reject `&&`, `;`, `|`), no
  grep/rg (search MCP), pnpm scripts only, no git, no builds/tests —
  the parent runs the single gate.

## Kernel proof facts

- **`compileStmt`/`summarize` (refined-lean/set_functions/summary.lean)
  are TOTAL — no `Option`/`Except` in their signature.** They mirror
  `walkStmt` case for case and always return a `SumBuild`/`Summary`.
  There is no Lean-side refusal channel inside the compiler itself for
  a statement kind the summary form can't capture faithfully.
- **The real refusal channel for an uncertifiable statement is the
  `IrStmt.inRange` HYPOTHESIS** (proofs/summary_inrange.lean),
  which `summarize_eq`/`summarize_admits`/`summarize_ok` all take as a
  premise. Define a constructor's case as `False` there and no caller
  can ever discharge the faithfulness theorem for a body containing
  it — `compileStmt` still produces SOME row (it has to, being total),
  but nothing can trust it. This is the pattern to reach for before
  reaching for a return-type change: it costs one `Prop`-level case
  per site, not a signature rewrite through every caller.
- **`Sim` (the compile/walk simulation invariant,
  proofs/summary_sim.lean `structure Sim`) demands STRICT
  EQUALITY in its `rows` field, never a containment/admission
  relation.** `compileStmt_sim`'s conclusion is
  `Sim entries (compiled row) (walkStmt tbl env s)` — exactly equal,
  not "the compiled row is sound for" or "admits." This is why
  compiling a statement to a WEAKER-but-sound row (e.g. an all-`top`
  havoc) only closes `compileStmt_sim` when the WALK's own answer for
  that statement kind is ALREADY that same weaker row by definition
  (`.loopStmts` is the precedent: `walkStmt`'s `.loopStmts` case is
  itself `loopStmtsExitAll env ws af cc none` — the exact function the
  compile targets — precisely BECAUSE `walkStmtCert`, the stronger
  certified walk, is kept as a separate function no summary has to
  mirror). A statement whose WALK answer is exact (not already a havoc
  reading) cannot be compiled to a havoc row and pass `compileStmt_sim`
  by that route — the `IrStmt.inRange = False` gate is what's needed
  instead, and `compileStmt_sim`'s case for it is one line:
  `intro hin _; nomatch hin`.
- **Every exhaustive `match`/`mutual` over `IrStmt` in
  refined-lean spans SIX sites, not the four/five a task
  description may name**: `IrStmt.writes` and `walkStmt`
  (set_functions/walk.lean), `compileStmt` (set_functions/summary.lean),
  and `IrStmt.inRange` (proofs/summary_inrange.lean),
  `compileStmt_width` (proofs/summary_width.lean),
  `compileStmt_appends` (proofs/summary_appends.lean),
  `compileStmt_sim` (proofs/summary_stmt_sim.lean),
  `compileStmt_curBound` (proofs/summary_curbound_stmt.lean)
  — `compileStmt_curBound` is easy to miss (it feeds `summarize_ok`,
  the well-formedness closing lemma) since it isn't named beside the
  width/appends/sim trio in the file's own header comment. `Runs`
  (set_functions/walk_runs.lean) and `RunsC`
  (proofs/walk_concrete_runs.lean) are
  `Prop` inductives, additive-only, not exhaustive matches — a new
  constructor there breaks nothing already compiled, but any
  `induction`/`cases` tactic block over them DOES need the new case.
  `RecRuns` (proofs/recursion_correct.lean) is a SEPARATE Prop
  inductive from `Runs`/`RunsC`, not automatically extended by either
  — a new `IrStmt` constructor with no `RecRuns` case simply means no
  concrete run can ever be derived through it (a silent capability
  gap, not a compile error).
- **Go-side counterpart**: `AskSummarize` (kernelbridge/summary_questions.go)
  turns any kernel-side panic/refusal into `ok=false`, which the
  registry (`walk/summary_registry.go`, `buildSummaryBlob`) remembers
  as a permanent decline per declaration — but this is a RUNTIME
  refusal path (a bad wire, a kernel `fail`), not the compile-time
  `IrStmt.inRange` gate above; the two are independent mechanisms and
  both exist.
- **A new `RecRuns` constructor for an `IrStmt` kind whose `Runs`
  version has a free-form solver premise (e.g. `.loop`) still mirrors
  `RunsC`'s premise, not `Runs`'s.** `RecRuns.loop`
  (proofs/recursion_correct.lean) copies `RunsC.loop`
  (proofs/walk_concrete_runs.lean) field for field — the depth-indexed
  form mirrors the CLOSED run relation throughout, since `RecRuns`
  itself plays `RunsC`'s role (concrete, discharge-shaped premises)
  one level up; `Runs`'s own `.loop`/`.loopCounted` cases carry an
  already-discharged `Admitted (walkStmt …) exitO` premise, which is
  what `RunsC`/`RecRuns` PROVE via `loopExit_admitted`/
  `walk_counted_correct`, not what they state as a premise. Landing
  `.loopCounted` in `RecRuns`: the constructor is `RunsC.loopCounted`
  verbatim (`stepsO`, `hentry`, `hlenO`, `hstepAt`), and the
  `recRuns_runs` case is `runsC_runs`'s `.loopCounted` case verbatim,
  discharged through the same `walk_counted_correct` call — the depth
  index carries through unchanged because a counted loop's body is
  `List Effect`, which cannot contain a self-call.
- **Naming trap inside `RecRuns`'s own constructors**: the inductive's
  fixed parameter is `(body : List IrStmt)` — the recursive function's
  OWN body. Any constructor whose statement carries a loop/effect body
  of its own (`.loop`'s `b`, `.loopCounted`'s `b`) must NOT name that
  field `body` — it shadows the outer one and silently changes what
  the constructor's conclusion `RecRuns tbl self retIdx width
  bodyWidth body d …` refers to. `RecRuns.loop`'s own field is already
  named `b` for exactly this reason; any new loop-shaped constructor
  must follow it.

## Destructuring / collection-identity facts

- **Assignment-position destructuring (`({ a } = x)`, `[a] = xs`) is a
  DIFFERENT AST shape from a declaration pattern, and had NO judged-
  write route at all before this pass.** `ReadAssignment`
  (assignment_operators.go) only recognized `bin.Left` as a plain
  identifier or property access; an object/array-literal LHS fell to
  `ReadForgottenAssignment`, which only `ForgetThrough`s every bound
  name (a havoc, never a judged write) — so `({ age } = over)` never
  fired even when `age` was declared `Age` and `over.age` was out of
  range. `WriteAssignmentPattern` (assignments.go) is the fix: it
  walks the SAME object/array-literal shape `ForgetThrough` and
  `dataflowfacts.TargetNames` already recognize
  (PropertyAssignment/ShorthandPropertyAssignment/SpreadAssignment on
  an ObjectLiteralExpression; plain elements/SpreadElement on an
  ArrayLiteralExpression — NOT BindingElement, which only a
  declaration pattern uses), but calls `WriteBinding` per leaf instead
  of havocking, reusing `SlotOf`/`SlotOfIndex` for the per-slot read.
- **`SlotOfIndex`'s exact-primitive-array branch
  (`KindValues{PrimitiveArray}`, destructuring.go) answered honest-
  Unknown past its own known length, unlike its `KindList` sibling,
  which already answered `Undef` (provably absent) past ITS length.**
  `Values` lists every element the array holds — as exact as a
  `KindList`'s `Items` — so the same "past-end is provably absent"
  rule applies. The old behavior silently discarded a destructuring
  DEFAULT: `withDefaultValue`'s `KindUnknown` branch residues the
  default away entirely (only its `KindUndef` branch RUNS the default
  expression), so `const [first = 18] = [] as number[]` bound `first`
  to Unknown rather than `18`, firing 7002 on the in-set leg.
- **A WeakMap/WeakSet key compares by REFERENCE, never by value —
  `collectionKey`/`CollectionEntry`/`SameKnown`'s `KindObject` case
  are all VALUE-equality machinery and must stay untouched for
  ordinary Map/Set.** The new identity path (collection_models.go:
  `weakEntryIdentity`, `weakEntrySymbol`, `sameWeakEntry`,
  `findWeakEntry`, `readWeakEntrySet`, `readWeakEntryGet`) is
  DELIBERATELY separate: it activates only when the key argument is
  `dataflowfacts.ReferenceTyped` (never intercepting a primitive Map
  key), and identifies a key by the declaring symbol of a plain,
  file-wide-unreassigned identifier (`narrowing.ReassignedNames`,
  file-scoped — coarser than a local scope check but sound). The
  identity marker reuses `AbstractValue.Symbol` (a field otherwise
  exclusive to `KindVariable`/generic type parameters) but keeps
  `Kind: KindObject` and a private comparison function
  (`sameWeakEntry`), so `SameKnown`'s own `KindObject` case (which
  compares by key SET, not symbol) never sees it. `KindVariable`
  itself must NOT be repurposed for this — it is built exclusively
  from `annotations.DeclaredVariable` (declared_value.go) and its
  `Bound`/`StarDepth` fields carry generic-parameter semantics that do
  not apply to an arbitrary object key.
- **`ReadCollectionConstruction` (collection_models.go) recognized
  only `"Map"`/`"Set"` by name — `"WeakMap"`/`"WeakSet"` fell through
  entirely, so a WeakMap was NEVER tracked as a `KindCollection` at
  all**, meaning `.set()`/`.get()` on one could never reach the
  precise entry-tracking readers, only the generic spec-fixed
  fallback (`readCollectionMethods`'s bottom block) that answers a
  bare `PossiblyUndefined(Residue)` regardless of what was `.set()`.
  Fixed by recognizing the BARE constructor only (`new WeakMap()`,
  matching Map's own bare-constructor empty-collection case) — a
  WeakMap's LITERAL-entries constructor argument stays unread, since
  its keys need identity comparison the literal-entries route's
  `collectionKey` cannot give at construction time.
- **Object member keys are always STRING keys at runtime
  (ToPropertyKey, sec-topropertykey step 2 calls ToString on a
  Number) — a NUMERIC-LITERAL key (`{ 0: 40 }`) was recognized by
  NEITHER the object-literal WRITE side (`object_literal.go`'s
  `EvaluateObjectLiteral`, which checked only `IsIdentifier`/
  `IsStringLiteral`) NOR the element-access READ side
  (`element_access.go`'s `ElementAccessOf`, which checked only
  `IsStringLiteral`/`IsNoSubstitutionTemplateLiteral` plus an
  EVALUATED `PrimitiveString` value — never a numeric literal's own
  node).** Both sides now also accept `ast.IsNumericLiteral`, reading
  the literal's own `.Text()` as the key string (sound for any
  integer literal spelled in ordinary decimal form, where the source
  spelling and `Number::toString` coincide). Fixed at BOTH sides —
  fixing only one leaves `{ 0: 40 }` writing under a key `person[0]`
  can't find, or vice versa.
- **`method_this_writes.go`'s object-literal-method `this`-write
  machinery (`LiteralMethodWriteStatements`,
  `literalThisBundleOf`) is wired ONLY into `ir_summary_call.go`** —
  the kernel-IR SUMMARY/lowering path, used when a function's body is
  being compiled into a summary for OTHER callers. It has NO
  counterpart in `EvaluateCallExpression`'s direct interpreter path
  (evaluate_call_expression.go), which is what determines a
  fixture row's own fire/silence when that row IS the function under
  direct judgment. A `person.bump()` call inside the SAME function
  that declares `person` as an object literal, with `bump` writing
  `this.age`, may fall through `EvaluateCallExpression`'s `ContractOf`
  resolution (untested here whether object-literal methods register
  in `ctx.Contracts` at all) to `readUnmodeledMethod`
  (unmodeled_method_havoc.go), which `ForgetThrough`s the receiver —
  havocking `person` entirely rather than carrying the `this.age =
  this.age + 1` write through. FIXED since: the walk route has its own
  counterpart now — `ObjectLiteralMethodWalkCall` /
  `objectLiteralMethodWalkTarget` (method_this_writes.go), tried in
  `InlineContractBody` BEFORE the memo/kernel-summary section. The
  ordering is load-bearing: the kernel-summary-direct route also
  succeeds on such a body, and its served-call forget
  (`SummaryReceiverEffects` → `ForgetThrough`) havocs the receiver —
  right for a class (whose `this.key` reads answer through a standing
  field invariant), wrong for an object literal, whose exact written
  value lives ONLY on the tracked object's own key. The class twin,
  `ClassMethodWalkCall` (inline_contract_body.go), sits AFTER the
  kernel-summary decline instead, binding `this` to
  `SummaryCallReceiver`'s per-call instance so `new Sealed(40)` and
  `new Sealed(200)` stop sharing one class-wide join.

## The syntax-wave facts (nine agents, 2026-08-15)

- **A row that fires in the judge but traces clean on the walk route
  is a KERNEL-LOADED divergence until proven otherwise.** Every test
  harness in this package runs kernel-less (the dylib-gated skip), so
  an agent's trace exercises the walk route only — the judge binary
  loads the dylib and tries the summary/serving routes FIRST. Two
  proven mechanisms: `applySummary` serving a TOP-derived answer
  (below), and the served-call forget havocking a receiver whose exact
  value only the walk route carries (the method_this_writes entry
  above). Check the serving path before re-tracing the walk models.
- **`summaryEntryStates` (kernel_summaries.go) TOP-fills BOTH
  flattened entries of an array-typed parameter** (`p.len`/`p.elem`)
  whatever exact array the call passed. `applySummary` already had the
  antidote for rest parameters — the `exact`-gated decline reading
  `declarationHasRestParameter` — and `declarationHasArrayParameter`
  (same file) is its twin: a TOP ret on a call whose own argument WAS
  exact declines the serving so walk recovery answers. The live
  array-parameter recognizer is `arrayParamSlotsIn`/`ArrayParameterOf`;
  `ArrayParametersOf` (ir_array_parameters.go) is DEAD CODE with zero
  callers — do not wire through it.
- **A body whose every return is the same bare record-expanded
  parameter lowers member-carrying via
  `returnedWholeParameterMembers`** (ir_summary_returned_shape.go) —
  RetMemberEntry rows ALIASING the parameter's own bundle-entry slot
  indices, no new slots. Tried only where `returnedLiteralShape` found
  nothing (ir_summary_body_lowering_slots.go).
- **Constructor declarations never register as `FunctionContract`s**
  (contract_file_facts.go's collector has no ConstructorDeclaration
  case), so `super(...)` can never resolve a contract.
  `ConstructedInstance` handles it itself: `findSuperCall` +
  `superConstructorFieldCandidates` walk the base constructor's body
  with parameters bound from the super call's own arguments, recursing
  up the chain.
- **`ReadPropertyAccess` tries `ReadStaticFieldAccess` FIRST** — a
  bare class reference's static type carries construct signatures, so
  it evaluates to `KindHostFunction`, and `ReadObjectKeyAccess`'s
  KindHostFunction branch answers a NON-NIL silence.Residue() that
  forecloses every later reader in the chain. General trap: a non-nil
  silence answer from an early reader shadows every reader after it —
  place new property-access readers accordingly.
- **The accessor-write family shares one core**: `accessorWalkTarget`,
  `accessorInlining`, `runAccessorGetter`, `runAccessorSetter`
  (ir_accessor_calls_read_modify_write.go) serve four routes — the
  ordinary compound (`AccessorWalkReadModifyWrite`), the plain `=`
  write (`AccessorWalkPlainWrite`, ir_accessor_calls_plain_write.go,
  setter-only resolution, no getter needed), the logical compound
  (`AccessorLogicalReadModifyWrite`, getter-first, right side
  UNEVALUATED on the kept branch per the LogicalAssignment algs), and
  the class-method call fold. Keep those helper names stable — the
  plain-write file depends on them by name.
- **`ConstructedInstance` seeds a construction-time getter SNAPSHOT
  key** (the accessor's own name beside the backing field), and a
  plain read (`ReadObjectKeyAccess`) is a literal key lookup with no
  getter awareness — so every setter-write route must refresh that
  snapshot or it serves the stale pre-write value forever.
  `runAccessorSetter` does it once for all routes via
  `refreshedAccessorSnapshot`: re-run the getter against the post-write
  receiver where a getter body is known, DROP the key where not.
- **`compoundResult` has NO arm for `||=`/`&&=`/`??=`** — those three
  tokens silently fall to `silence.Residue()` in any arm that reaches
  it. Logical-assignment routes must branch BEFORE calling it.
- **Destructuring gaps closed this wave, and their shape**: a nested
  pattern at an ARRAY element (`[[first]]`, `[{age}]`) recurses via
  `destructureInto` (destructure_binding.go); a computed object-pattern
  key resolves through `bindingElementKey` → `exactStringName`
  (object_literal.go's write-side rule — the ONE computed-key
  resolution both sides share); the array leaf branch applies
  `withDefaultValue` exactly as the object leaf always did. All three
  gaps exist UNFIXED in the TypeScript source tree too
  (control_flow/destructure_binding.ts) — a TS-side pass should port
  them rather than rediscover them.
- **`ElementAccessOf`'s call-result arm admits receiver SHAPES, not
  receiver kinds** — it gated on Call/ElementAccess receivers and
  excluded a bare ArrayLiteralExpression until this wave, so
  `[...xs][0]` never evaluated the literal it indexes. When widening a
  gate, enumerate every receiver shape the arm's own evaluated-value
  indexing already supports, not just the shapes a prior wave admitted.
- **`EvaluateArrayLiteral`'s spread gate runs the iterator readers for
  ANY unanswered spread value, Opaque included** — a generator call's
  own value is deliberately Opaque (GeneratorCallResult), and
  `GeneratorSequenceOf` answers off the CALL NODE, not the value; the
  old `!Opaque` exclusion blocked exactly that arm. Its async twin:
  `generatorFirstNextValue` serves async generators too
  (sec-asyncgeneratorresume runs the same body-forward resume; the
  promise wrapping is handled where the record is read).
- **A whole parameter's DEFAULT evaluates through
  `ParameterArgumentOrDefault`** (inliner.go) exactly when the
  position is genuinely missing — not merely spread-uncertain — and
  `BoundParameterKnown` rides it, so every inline route sees the
  default. (`ParameterKnown` alone still never evaluates one.)
  Destructured parameters bind through `BindInlineParameter` (one
  function, both inline routes) — an identifier-only parameter loop
  that `continue`s past patterns leaves every leaf unbound.
- **`JoinSinkSummarized` joins an in-flight recursion marker as
  `silence.Residue()`** — the marker still counts a drop
  (MarkerDropCount, memo safety), but the join no longer answers the
  base case alone for every depth (unsound: `countdown(NaN)` never
  terminates). A recursive result is honestly undetermined unless a
  fixpoint proves more.
- **`InlineStoredClosure`'s const-only gate is deliberate design**,
  repeated on purpose at its siblings (`ContractOf`'s reassigned-names
  check, `PinnedFunctionOf`); the one admissible let-bound case is
  `immediatelyPrecedingClosureAssignment` — the call's own immediately
  preceding sibling statement plainly reassigns the callee to an
  arrow/function expression, no branch or block crossed.
- **§E-shaped rows may already be pinned**: check
  `call_shape_contracts_test.go` (overloads, rest parameters) before
  re-deriving that machinery from scratch.
- **A row wrong on the walk route may never reach InlineContractBody
  at all** — check `Summarize`/`scanBody`'s `SelfContained` verdict
  first. `scanBody` marks any `this` read non-self-contained
  (function_summaries.go); before that, a `return this.#age` body
  routed to RecoverPure, whose `SummaryResultIn` hardcodes
  `unknownReceiver()` and TOPs every field. Same gap exists unfixed in
  the TS source (interprocedural/function_summaries.ts).
- **A short-circuit operand nested inside arithmetic lowers through
  `LoopEffectJoin`, whose kernel join borrows the left side's absent
  flag** (effectReadsFlagged) — sound but spells the whole expression
  possibly-NaN. `returnArithmeticOverShortCircuit`
  (lowering_to_kernel_ir_return_branch.go) composes the arithmetic
  inside a branch per arm instead; tried before RhsEffect.
- **Concurrent waves land in your files while you work.** The
  call-routing files (inline_contract_body.go, inliner.go,
  method_this_writes.go, constructed_instance.go, assignment_operators.go)
  are every wave's shared ground — re-read a file immediately before
  editing it, anchor on function names never line numbers, and expect
  helpers you planned to write to already exist under the names above.

## The 2026-08-17 wave facts

- A porous "call X" row is always evidence about X's OWN body, never
  the caller's serving: SummaryCallOrHavocNamed's summary tier
  requires SummaryOutcomeOf(callee) == SummaryComplete
  (ir_summary_call_statement.go). Diagnose the callee in isolation
  first.
- GetTypeAtLocation on a bare declaration name answers `any`; the
  SAME ask at a control-flow-reached READ occurrence answers the
  narrowed type from the assignments. A local's write-derived sort
  comes from its read sites, not its declaration.
- The census's construct names spell the SYMPTOM statement (the
  floor's first-return scan), not the blocking construct — a large
  "return inside X" tally can be one upstream sort/layout gap
  (declaredParamSort's keyword-only recognition was the 2026-08-17
  instance). Verify with RelowerSummaryBody + SummaryOutcomeOf on the
  isolated shape before reading position gates.
- "a binding-pattern parameter" is spelled by TWO routes: the plain
  declared-parameter layout (ir_summary_body_lowering_layout.go) and
  the arrow-argument route (ir_summary_body_lowering_parameters.go,
  whose string carries "…of an arrow argument"). Census rows under
  the short string are the plain family.
- Agents do not compile or test. Workers read, edit, and write pins
  only, reporting which tests they expect to fail pre-fix; the parent
  session runs one build and one suite per batch. Concurrent go
  builds from several workers serialize on the build-cache lock and
  jam the machine (observed 2026-08-17).
- Never redirect shell output (>, >>, 2>, &>) — a hook rejects it;
  the tool result already captures both streams.

## Change log

- Seeded 2026-08-15 from the three fix waves' reports (11 + 8 + 6
  agents): every line above was independently re-discovered by at
  least one agent whose brief lacked it.
- Added 2026-08-15 from the destructuring/object-literal/stdlib-
  surfaces fix pass: the destructuring-family and collection-identity
  facts above.
- 2026-08-15, the precision-gaps wave (1 agent): the two astral-freedom
  and object-star-tolerates-Undef lines above.
- 2026-08-15, the compound-assignment family wave (1 agent): the
  "Compound-assignment facts" section — bitwise compounds
  (compoundBitwiseOperator), the shared compoundResult factoring,
  element compounds (ReadIndexedCompoundWrite), the accessor
  read-modify-write on the walk route (AccessorWalkReadModifyWrite,
  reusing AccessorDeclarationsOf and the this-bound-to-KindObject
  fallthrough), and the ||=/&&=/??= no-PutValue-on-the-kept-branch
  reading plus the resolve-before-evaluate discipline it shares with
  the accessor arm.
- 2026-08-15, the syntax wave (9 agents over the judge's 37-line
  delta): "The syntax-wave facts" section, and the
  method_this_writes entry above rewritten from "open question" to
  the landed ObjectLiteralMethodWalkCall/ClassMethodWalkCall pair
  with the serving-order rule.
- 2026-08-17, the census waves: "The 2026-08-17 wave facts" section —
  callee-completeness precondition, read-site narrowing, symptom-named
  census constructs, the two binding-pattern routes, the
  agents-never-compile rule, and the no-redirection rule.
