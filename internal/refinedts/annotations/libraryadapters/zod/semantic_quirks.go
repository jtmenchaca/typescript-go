// The honest semantic deltas between zod's runtime and the model.
// The checker speaks the intended meaning; where zod's runtime
// disagrees with the meaning the user wrote, that is zod's bug,
// recorded here so the divergent cases stay pinned.
//
// Length units. `.min(k)` / `.max(k)` / `.length(k)` read as k
// CHARACTERS (Unicode scalar values) — the meaning the user wrote,
// and the same semantics the checker's own surface enforces. Zod's
// runtime counts UTF-16 code units (JavaScript's .length), so an
// astral scalar counts twice there: "😀😀" is 2 characters but zod
// counts 4. That is a zod runtime bug, not a checker limitation —
// the checker keeps the correct reading. A loosened compromise
// window was tried and retired — it accepted "ab" against
// z.string().min(3), which is the worse trade (unsound for every
// user, to serve a corner almost none hit).
//
// Other residues carried the same way:
// - z.number() compiles to R-bar minus NaN (the set denotation never
//   holds NaN). Zod v3 also admits +-infinity — an exact match; zod
//   v4 rejects +-infinity at runtime, so the claim is wider than
//   v4's enforcement: sound for parse results, imprecise for
//   infinity refutations.
// - multipleOf reads as the exact-multiple set; zod decides by float
//   remainder, which can disagree on denormal corners.
//
// This file intentionally exports nothing: the deltas above are
// documentation, and the length bounds compile through the shared
// exact reading in schema_chain_compiler.go.
//
// Ported 1:1 from annotations/library_adapters/zod/semantic_quirks.ts.
package zod
