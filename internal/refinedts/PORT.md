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
- RE2 (Go's `regexp`) has no lookahead/lookbehind. A TS regex literal
  using `(?!...)`/`(?=...)`/`(?<=...)` has no literal Go twin; port the
  matched TS behavior by hand (an explicit post-match character check,
  a second pass, …) and say so in the report rather than silently
  dropping the constraint.

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
- Symbols: `*ast.Symbol` with `.Name`, `.Flags` (`ast.SymbolFlagsAlias`
  etc.), `.ValueDeclaration`, `.Declarations`.

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
