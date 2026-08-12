// Zod's own names, beyond the shared zod-shaped chain language the
// core reader already speaks (number/string/min/max/gt/lt/int/
// multipleOf/…). Each entry maps a zod name onto the combinators it
// means — an upstream rename is a one-line change here, and the
// adapter test is the drift detector.
//
// Ported 1:1 from annotations/library_adapters/zod/chain_vocabulary.ts.
package zod

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ZodRoots is ZOD_ROOTS in the TS source: root constructors --
// z.<name>(). Windows verified against zod 4.4.3
// (hover-lab/zod-semantics-probe.mjs).
var ZodRoots = map[string]func() refinementsets.RefinedSet{
	// zod 4's number() admits exactly the FINITE doubles -- the parse
	// gate is `typeof input === "number" && !Number.isNaN(input) &&
	// Number.isFinite(input)` (vendored tmp/zod-src, v4/core/
	// schemas.ts:1130, $ZodNumber). NaN was never an element of ℝ̄;
	// the ±∞ refusal is zod's own and the set states it.
	"number": func() refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(
			refinementsets.Above(math.Inf(-1)),
			refinementsets.Below(math.Inf(1)),
		)
	},
	// zod v4's top-level integer: Number.isInteger AND safe --
	// |x| <= 2^53 - 1 is part of what its runtime enforces
	"int": func() refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(
			refinementsets.Integer,
			refinementsets.AtLeast(-(math.Pow(2, 53) - 1)),
			refinementsets.AtMost(math.Pow(2, 53)-1),
		)
	},
	// the signed 32-bit window, integers only
	"int32": func() refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(
			refinementsets.Integer,
			refinementsets.AtLeast(-math.Pow(2, 31)),
			refinementsets.AtMost(math.Pow(2, 31)-1),
		)
	},
	// a string->boolean codec: the transform maps the truthy and
	// falsy word lists to true and false and refuses everything else,
	// so the OUTPUT is exactly a boolean whatever the word lists say
	// (vendored v4/core/api.ts:1738, _stringbool)
	"stringbool": func() refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))
	},
	// any FINITE double -- ±∞ and NaN are refused at runtime
	"float64": func() refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(
			refinementsets.Above(math.Inf(-1)),
			refinementsets.Below(math.Inf(1)),
		)
	},
}

// ZodChainVocabulary is ZOD_CHAIN_VOCABULARY in the TS source:
// argument-less chain methods: .<name>(). The case and trim maps
// TRANSFORM the value, so every accumulated fact about the earlier
// value is dropped -- the output is just a string again (checks
// after the map re-refine the transformed value; claims from before
// it would be wrong: min(6).trim() accepts " hell " and returns a
// 4-character string -- verified).
var ZodChainVocabulary = map[string]func(base refinementsets.RefinedSet) refinementsets.RefinedSet{
	"positive": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.Above(0))...)
	},
	"negative": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.Below(0))...)
	},
	"nonnegative": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.AtLeast(0))...)
	},
	"nonpositive": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.AtMost(0))...)
	},
	"finite": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		return refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.Above(math.Inf(-1)), refinementsets.Below(math.Inf(1)))...)
	},
	"nonempty": func(base refinementsets.RefinedSet) refinementsets.RefinedSet {
		tightened, ok := refinementsets.TightenRepetition(base, "min", 1, nil)
		if !ok {
			panic(".nonempty needs a sequence-shaped chain")
		}
		return tightened
	},
	// a compile-time marker; values unchanged
	"readonly":    func(base refinementsets.RefinedSet) refinementsets.RefinedSet { return base },
	"trim":        func(refinementsets.RefinedSet) refinementsets.RefinedSet { return refinementsets.Strings },
	"toUpperCase": func(refinementsets.RefinedSet) refinementsets.RefinedSet { return refinementsets.Strings },
	"toLowerCase": func(refinementsets.RefinedSet) refinementsets.RefinedSet { return refinementsets.Strings },
}
