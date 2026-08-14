# The RefinedTS port briefing

Read this WHOLE file before porting anything. It exists so a porting
agent searches for nothing: the module layout, the tsgo API surface,
the conventions, and the shell rules are all here. If you had to go
searching for something anyway, say so in your report — it gets added
here.

## What this is

The RefinedTS checker (refinement types for TypeScript) is being
ported 1:1 from TypeScript to Go, into this fork of typescript-go, so
the walk lives in the same process as the type checker it questions.
The source of truth is the TS tree at:

    /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-typescript/

The port preserves structure, names, algorithms, and comments. It is
transcription of settled, fixture-specified behavior — never redesign.
Every `foo.ts` becomes `foo.go`; every `foo.test.ts` becomes
`foo_test.go` beside it with the same cases and case names.

## Where things go

- Go module path: `github.com/microsoft/typescript-go` (see /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go/go.mod)
- The port lives under `internal/refinedts/<dir>/`, one Go package per
  TS directory, `package <dir>` (e.g. `internal/refinedts/primitives/`
  is `package primitives`).
- Build:  `go build -C /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go ./internal/refinedts/...`
- Test:   `go test -C /Users/jtmenchaca/TypeRefinery/packages/refinedts/refined-ts-go ./internal/refinedts/<dir>/`
- Whole-tree equivalents exist as pre-approved pnpm scripts (run from
  /Users/jtmenchaca/TypeRefinery, each needs the attestation comment
  block like every pnpm command): `pnpm go:build`, `pnpm go:test`,
  `pnpm go:vet`, `pnpm go:fmt`. Prefer these when a command would
  otherwise prompt for approval; package-scoped `go test ./…/<dir>/`
  stays fine.

## Port order (dependency-shallow to deep)

primitives → refinement_sets → kernel_bridge → abstract_domain →
dataflow_facts → type_reading → narrowing / comparison → evaluation →
bindings / interprocedural → control_flow → assignability →
annotations → object_graphs → silence → service

A directory may import only directories earlier in this order (mirror
of the TS tree's own import graph).

## Conventions (the whole port follows these)

- Discriminated unions (`{ kind: "x" } | { kind: "y" }`) → one struct
  with a Kind field for pure data; a small interface with Kind() only
  when variants carry genuinely different payloads.
- `number` as integer → int or int64 (read the code); as float →
  float64; `bigint` → int64 unless the TS code exceeds it, then
  math/big.Int.
- `throw new Error` → returned error; assertion-of-impossibility →
  panic. Mirror the TS intent.
- Exported functions → CamelCase; keep the original TS name in a
  comment only when the rename loses the connection.
- Preserve every comment from the TS source.
- Destructured named parameters → a params struct at 3+ fields.
- WeakMap keyed by node/type → map keyed on the pointer
  (`map[*ast.Node]V`) — tsgo nodes are stable pointers. Go has no
  weak maps; note the substitution in a comment where the TS code
  relied on GC lifetime (functionally identical per program).
- `string | null` and friends → the zero value with a comment when
  the zero value is unambiguous (a typeof word is never ""); the
  `(T, bool)` pair otherwise. Never a *string.
- The CheckerHost/oracle ADAPTER dies in the port: where TS reads
  `ctx.p.host` for type/symbol answers, Go code calls
  `*checker.Checker` directly. The whole point of the port is that
  the host IS the checker, in-process. But `FlowContext` itself DOES
  port (as `evaluation`'s context struct): it is the walk's context —
  the kernel handle, the diagnostic report sink, declared statements,
  program facts — not just a host wrapper. Its host FIELD is replaced
  by `*checker.Checker`; the rest carries over 1:1. Until evaluation/
  lands, a module needing FlowContext is blocked (report it), never
  stubbed.
- The port-order table is a guide, not the truth: comparison/ imports
  FlowContext from evaluation/ (a forward dependency the TS layering
  tolerates because it is type-only there). When your directory's
  imports contradict the order, trust the imports and report it.
- A type from a LATER directory that this directory only compares by
  identity (never reads fields of) → an opaque comparable stand-in
  (`type FooRef = *struct{}` or an interface{}) with a comment naming
  the real type and where it lives — never a copied shape, never an
  import that inverts the dependency order. (Precedent:
  abstractdomain's ObjectAnnotationRef.) Functions that DO read the
  foreign shape stay unported and go in the report.
- A TS branch that is unreachable by the code's own invariant (e.g.
  reading undefined off an empty array where the constructor already
  collapsed that case) → the "not determined" answer plus a comment;
  do not build Go machinery to imitate undefined.
- A TRUE two-way runtime cycle within one directory's own subtree
  (not a type-only import TS allows freely) — e.g.
  annotations/schema_chain_compiler.ts's isUnsupported/Annotation
  called BY annotations/library_adapters/zod/*.ts, while
  library_adapters/library_adapter.ts's LibraryAdapter is needed BY
  schema_chain_compiler.ts's siblings — has no direct Go translation
  (Go bans the cycle outright, unlike the objectgraphs/kernelbridge
  precedent, which is a one-directional dependency Go already
  tolerates). Resolution: pull the two-way shape into a NEW LEAF
  package both sides import (precedent:
  annotations/libraryadapters/compiledshape, mirroring
  Annotation/Compiled/Unsupported structurally), and convert at the
  call boundary in the higher package. A tiny pure helper needed on
  BOTH sides of the same cycle (chain_args.ts's numberArg/stringArg/
  stringList, needed by both annotations/ and its own
  library_adapters/zod/) gets duplicated verbatim into the leaf side
  rather than exported from a package it would also have to import —
  noted in a header comment, never silently diverged.
- `encoding/json.Marshal` SORTS `map[string]any` keys alphabetically —
  it cannot reproduce a TS `JSON.stringify(objectLiteral)` field
  order. A wire string that must match the TS bytes exactly is built
  by hand as an ordered string (see kernelbridge/wire_format.go); the
  map form is only for order-insensitive uses. Also: JS stringifies
  ±Infinity/NaN as `null`; encoding/json panics — mirror the JS
  (kernel_asks.go's jsonNumberString).
- Do NOT add improvements, generalizations, or helpers. 1:1.
- A TS filename ending in a Go GOOS/GOARCH suffix (`_windows`,
  `_linux`, `_darwin`, `_386`, `_amd64`, `_arm64`, …, or one of those
  immediately before `_test`) is silently excluded from the build on
  every other platform — `go build`/`go vet` report no error, the
  package just compiles as if the file did not exist, and every symbol
  in it reads as `undefined` from sibling files. Rename the Go twin to
  dodge the reserved suffix (e.g. `repetition_windows.ts` →
  `repetition_window_forms.go`) and note the rename in the report;
  never rename the TS source.
- `BindingElement.Name()` (and its parser kin) can be NIL on an
  error-recovered node — TS's types say never-absent, Go's field says
  otherwise, and `ast.IsIdentifier(nil)` panics. Guard every `Name()`
  read with a nil check falling through to "no name here" (the
  answer the TS type system's impossibility already assumed).
- JS stack overflow is a CATCHABLE RangeError; Go's is a fatal
  runtime.throw. TS code that relies on try/catch absorbing runaway
  recursion (self-recursive declared functions re-entering their own
  computation) needs an explicit in-progress guard in Go that answers
  the same fallback the TS catch produced (precedent:
  walk/call_site_bindings.go's inProgress set).
- RE2 (Go's `regexp`) has no lookahead/lookbehind. A TS regex literal
  using `(?!...)`/`(?=...)`/`(?<=...)` has no literal Go twin; port the
  matched TS behavior by hand (an explicit post-match character check,
  a second pass, …) and say so in the report rather than silently
  dropping the constraint.

## The walk package — the one structural deviation

evaluation/, control_flow/, interprocedural/, and bindings/ are one
strongly-connected component in the TS import graph (evaluation calls
analyzeStatement, control_flow calls evaluateExpression,
interprocedural calls both, bindings is woven through). ES modules
tolerate that cycle; Go packages do not. They port into ONE package:

    internal/refinedts/walk/    (package walk)

- Every file keeps its 1:1 snake_case name and gains a first-line
  header comment `// from evaluation/flow_context.ts` (its TS home).
- On a FILE NAME collision between the four directories, the later
  file prefixes its TS directory (`controlflow_…`); on an unexported
  HELPER name collision, rename the later one with a comment.
- The package builds only when the whole component is in — porters of
  its stages verify with `gofmt -e` (syntax) per file and leave the
  type-level build to the component's final integration stage, which
  runs build/test/vet for the whole package and fixes until green.
- assignability/ SPLITS: its FlowContext-reading files
  (check_assignability, declared_value, dependent_edge,
  dependent_return, plain_sort, set_membership, worn_set_membership,
  sequence_measures, object_assignability, maybe_and_union,
  nan_wrapper, admitted_sort, temporal_membership, refine_on_exact)
  join package walk — the walk calls checkAssignability, so they are
  inside the component. coverage_sentences joins walk too (it reads
  control_flow's answer_types and is consumed by the answers). Its
  LEAF files (refinement_diagnostics, decline_reasons — no walk
  imports) stay `package assignability`, which walk imports one-way.
- comparison/'s one file (compareKnown reads FlowContext) joins
  package walk at integration; the placeholder package comparison
  is deleted then.

This mirrors tsgo's own structure — its checker is one large package
for the same reason.

## Cross-cutting seams already in the Go tree

- `internal/refinedts/tracing` — the tracing seam (service/tracing.ts
  + trace_state.ts, ported). Where TS imports span/count/recording
  from `../service/tracing.ts`, import this package and call
  `tracing.Span(name, func() T { … }, tracing.GrainStep)`,
  `tracing.Count`, `tracing.CountBy`, `tracing.Recording`,
  `tracing.Clock`. No `// tracing:` stubs — use the real seam.
- `internal/refinedts/kernelbridge` — `NativeKernel` (cgo dylib
  loader) with `Call1(symbol, input)` / `Call2(symbol, a, b)`; the
  higher asks layer is being ported on top of it.

## The tsgo API surface (what the checker layer uses)

Package `internal/checker`:

- The public wrapper methods live in `internal/checker/exports.go`
  (e.g. `func (c *Checker) IsArrayLikeType(t *Type) bool` wrapping the
  private `isArrayLikeType`). If a private checker function has no
  export wrapper yet, ADD one to exports.go (one-line wrapper, same
  style) rather than calling anything private from outside.
- `GetTypeAtLocation(node *ast.Node) *Type`, `GetSymbolAtLocation`,
  `GetTypeOfSymbolAtLocation`, `GetContextualType`,
  `GetCallSignatures`, `GetConstructSignatures`, `IsArrayLikeType` —
  the CheckerHost questions all have direct exported methods here.
- `Type` methods are in `internal/checker/types.go` (~line 683+):
  `Id()`, `Flags() TypeFlags`, `ObjectFlags()`, `Types()` (union /
  intersection constituents), `Symbol()`, `Target()`, `IsUnion()`,
  `IsStringLiteral()`, `IsNumberLiteral()`, `IsTupleType()`, and the
  `AsLiteralType()` / `AsUnionType()` / `AsTupleType()` downcasts.
- `TypeFlags` constants are in `internal/checker/types.go` (~line
  428+): `TypeFlagsAny`, `TypeFlagsUnknown`, `TypeFlagsObject` (1<<20),
  `TypeFlagsUnion` (1<<27), plus combined masks like
  `TypeFlagsStringLike`, `TypeFlagsUnionOrIntersection`.
- Literal values: `t.AsLiteralType().Value()` — the value is an `any`
  holding string / float64 / bool / jsnum.PseudoBigInt.

Package `internal/ast`:

- Node predicates are generated in `internal/ast/ast_generated.go`:
  `ast.IsBinaryExpression(node)`, `ast.IsIdentifier(node)`, etc.
- Downcasts mirror them: `node.AsBinaryExpression()`, and fields are
  Go-exported: `.OperatorToken.Kind`, `.Left`, `.Right`.
- The SAME pattern covers every TypeNode variant: `LiteralTypeNode`,
  `ArrayTypeNode`, `TypeOperatorNode`, `TypeReferenceNode`,
  `UnionTypeNode`, `TypeLiteralNode`, `ParenthesizedTypeNode`,
  `PropertySignatureDeclaration`, `EnumDeclaration`/`EnumMember`,
  `TypeAliasDeclaration`, `InterfaceDeclaration` — struct shapes,
  `As*()` downcasts, and `Is*()` predicates all in ast_generated.go.
- Numeric-literal TEXT (an enum initializer's `.text`, a literal type
  node's digits) parses with `internal/jsnum.FromString` — the
  checker's own ECMA StringToNumber — never `strconv.ParseFloat`,
  which diverges on hex/exponent forms.
- Function-like nodes (`ArrowFunction`, `FunctionExpression`,
  `FunctionDeclaration`, methods, …) have GENERIC accessors on `*ast.Node`
  itself, not just their downcasts: `node.Body() *Node` and
  `node.Parameters() []*ParameterDeclarationNode` read through
  `FunctionLikeData()`/`BodyData()` regardless of concrete kind — use
  these instead of a per-kind `switch { case IsArrowFunction: … case
  IsFunctionExpression: … }` on every site that only wants the body or
  parameter list (a TS `ts.isArrowFunction(fn) ? fn.body : ...` chain
  collapses to one `fn.Body()` call).
- `service/program_resolution.ts`'s CheckerProgram-level resolution
  questions map onto direct checker calls once `p.host` is the checker
  itself: `resolvesToDefaultLib(p, node)` is
  `c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))`;
  `symbolEntirelyInDefaultLib(p, symbol)` is the STRICTER "every
  declaration in the default lib" reading — a separate wrapper,
  `c.SymbolEntirelyInDefaultLib(symbol)`, added to exports.go (narrowing
  wave; SymbolInDefaultLib alone answers the weaker "some declaration"
  question and is the wrong gate for `instanceof`'s ambientConstructor
  check). `symbolAt(p, node)` (follow one alias hop through
  `GetAliasedSymbol`) has no ready-made wrapper yet; write it inline at
  the call site until enough callers want it to earn one.
- `kernelbridge.RefinedTSKernel`'s question methods PANIC on a refused
  question (mirroring the TS source's `throw`), never return an error
  value — a TS `try { kernel.foo(...) } catch { ... }` around a kernel
  ask becomes a `defer func() { if recover() != nil { ... } }()`
  wrapper in Go, not an `if err != nil` check.

## Building a program in a test

The canonical recipe is `internal/checker/checker_test.go`:
`compiler.NewProgram` over a `vfstest.FromMap` filesystem, then
`program.GetTypeChecker(ctx)`. `typereading/read_type_test.go` in
this tree is a live example sized to our needs — copy its helper
rather than reinventing it. (The TS tests' `programFromSource` with
the surface/zod virtual files is service-tier; port tests against
plain TS sources until service/ lands.)
- Kinds: `ast.KindPlusToken`, `ast.KindBinaryExpression`, … —
  NOTE: tsgo SyntaxKind NUMBERING differs from tsc's; never port a
  numeric kind literal, always the named constant.
- Positions: `node.Pos()`, `node.End()` are UTF-8 offsets (tsc's are
  UTF-16) — anything comparing positions to TS-recorded data must
  convert; inside pure Go code just use them consistently.
- TS `node.getStart()` SKIPS leading trivia; Go `node.Pos()` does
  not. The twin is `scanner.GetTokenPosOfNode(node,
  ast.GetSourceFileOfNode(node), false)` — every diagnostic span and
  every span-keyed record needs it, or the span hangs on the
  whitespace before the node (see assignability/
  refinement_diagnostics.go's At).
- Symbols: `*ast.Symbol` with `.Name`, `.Flags` (`ast.SymbolFlagsAlias`
  etc.), `.ValueDeclaration`, `.Declarations`.
- A TS test that greps the WHOLE source tree (e.g. silence/
  unknown.test.ts's "return UNKNOWN only appears in the silence
  allowlist" / "UNKNOWN is not imported outside…") checks the TS
  tree's own hygiene by text pattern — it has no Go-shaped twin: Go's
  equivalent constants (`abstractdomain.Unknown`/`Opaque`) are
  package-level vars constructible from any importer, not a grep
  target, and no Go-side allowlist convention exists yet. Port the
  FUNCTIONAL half of what such a test covers (here: each constructor
  returns the unknown atom) and say so in the file, rather than
  inventing a tree-scan test or silently dropping the coverage.

## Blocked functions inside an otherwise-portable file

When one function in a portable file needs an import outside the
directory's allowed set (or a not-yet-ported sibling function), do not
skip the whole file: port everything else, and give the blocked
function a body that returns the SAME fallback answer the TS source's
caller sees when that function's own reading finds nothing — never a
panic, never an empty stub with no comment. `narrowing/bound_condition.go`
was the pattern (NOW UNBLOCKED — the example is kept for its shape, not
as a live blocker): `ResolveBoundCondition` answered
`(ResolvedCondition{}, false)` because `dataflowfacts.WrittenNamesOf`
(needed transitively through `FunctionWrites`) was thought blocked on
`service/program_resolution.ts`'s `resolvesToDefaultLib` — and the TS
source's own `narrowingsOf` already falls through to reading the
condition plainly when `resolveBoundCondition` reads nothing, so the
blocked function's constant-false answer was the SOUND fallback, not a
lie. Every such function gets a file-header banner naming the blocking
import and every call site that is consequently degraded — a future wave
un-blocking the import only needs to fill the one function in, not
re-audit callers.

Before writing a new banner, CHECK the blocker still holds: this one had
already dissolved. `resolvesToDefaultLib` is not a service/ import at
all under the oracle-adapter rule above — it is
`c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))`, a direct checker
call several packages already write inline (narrowing/
array_shape_narrowing.go, annotations/program_resolution.go, walk/
iteration_elements.go). A banner naming a TS-side import as the blocker
is only true when the oracle-adapter rule does not already answer it.

## The kernel (for kernel_bridge and later)

The Lean kernel is a native dylib with a C ABI (the same one Deno
dlopens): `_init_lean_wasm()`, `_kernel_member(setPtr, tuplePtr)`,
`free_wasm_string`, … Wire format is JSON both ways. Go reaches it via
cgo. The TS reference is
`refined-ts-typescript/kernel_bridge/` (instantiate_kernel.ts,
wire_format.ts, wire_decode.ts, question_cache.ts).

## Shell rules (hooks enforce these; a violating command is BLOCKED)

- One command per Bash call — never chain with `&&`, `;`, `|`, or
  newlines.
- No `grep`/`find` in bash — use mcp__search-mcp__search /
  find_files / list_tree, or Read.
- `pnpm` / `deno test` / `deno check` commands require a 5-clause
  attestation comment block; `go build` / `go test` do not. Prefer go
  commands.

## The report every port unit ends with

- Files created; tests ported vs passing.
- The 1:1-impossible list: every place the conventions above did not
  cover, and what you did. Empty is the goal; anything on the list
  gets folded into this briefing.
- Anything you had to SEARCH for that this briefing should have told
  you.
