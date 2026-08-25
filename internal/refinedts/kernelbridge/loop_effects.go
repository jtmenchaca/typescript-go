// Loop effects: the per-binding body-effect grammar the kernel's loop
// solver reads — variable reads, constants, arithmetic and sequence
// ops, joins — and its wire encoding.
package kernelbridge

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LoopEffectKind is the tag of a LoopEffect.
type LoopEffectKind string

const (
	LoopEffectVar LoopEffectKind = "var"
	// LoopEffectVarState is the VERBATIM COPY of a binding's whole
	// state: the set, its Undef/Null admissions, and the NaN flag all
	// hand through unchanged (`lo = p.lo`). This is NOT LoopEffectVar's
	// numeric read, which coerces an absent or NaN source to NaN — a
	// copy must carry the source position's exact state, absence
	// included, the same way a plain assignment of one binding to
	// another does at every other sort. Rides the Index field, like
	// LoopEffectVar.
	LoopEffectVarState LoopEffectKind = "varState"
	// LoopEffectSquare is the STRUCTURAL SQUARE: the value at variable
	// index Index, squared — one variable read twice, recognized at
	// lowering time where "same variable" is a fact about the SOURCE
	// (the same identifier on both sides of a `*`), never inferred from
	// two operand effects that merely happen to coincide. Rides the
	// Index field, the same one-index shape as LoopEffectVar. The
	// kernel answers the correlated square image [0, max²] for this —
	// tighter than the sign-straddling product transferMul gives two
	// INDEPENDENT operands — and no longer recognizes `x*x` by syntax
	// (that branch was removed as unsound under renaming), so the
	// adapter is the only source of this claim now.
	LoopEffectSquare LoopEffectKind = "sq"
	LoopEffectConst  LoopEffectKind = "const"
	// LoopEffectConstState is the const leaf carrying the WHOLE state:
	// the set beside the Undef, Null, and NaN flags. `x = null` and
	// `return undefined` write one of the two absent outcomes, which
	// live outside R-bar and so cannot ride in a RefinedSet. A const
	// with every flag down is exactly LoopEffectConst, and the wire
	// keeps them distinct so older forms decode unchanged.
	LoopEffectConstState LoopEffectKind = "constState"
	LoopEffectUnknown    LoopEffectKind = "unknown"
	LoopEffectUnary      LoopEffectKind = "un"
	LoopEffectBinary     LoopEffectKind = "bin"
	// LoopEffectConcat is the SEQUENCE binary: `a + b` where both sides
	// are string-sorted builds the concatenation of the two operand
	// sets. It is not an enclosure operation, so it never reaches the
	// arithmetic transfers and rides its own wire field.
	LoopEffectConcat LoopEffectKind = "concat"
	// LoopEffectSeqUnary is the SEQUENCE unary: a string METHOD over a
	// receiver known only as a set. Like concat it lives in the tuple
	// layer rather than the enclosure world, so it never reaches the
	// arithmetic transfers and rides its own wire field, carrying the
	// method name in Op and the receiver in A.
	LoopEffectSeqUnary LoopEffectKind = "seqUn"
	// LoopEffectSeqNum is the NUMERIC-FROM-SEQUENCE unary: a string
	// method whose RECEIVER is a word and whose RESULT is a number. It is
	// its own kind because the operand and the result live in different
	// worlds -- the receiver is read in the tuple layer, the answer is
	// stated as an enclosure -- and no other kind crosses that way. The
	// method name rides in Op and the receiver in A; there is no needle
	// operand, because the window the kernel claims holds for every
	// needle.
	LoopEffectSeqNum LoopEffectKind = "seqNum"
	LoopEffectJoin   LoopEffectKind = "join"
	// LoopEffectOrAbsent is the unguarded index read: `a[i]` with nothing
	// bounding i against the length produces the element or undefined.
	// The value part is the operand's — the element slot's effect, in the
	// A field like the unary forms — and the absent outcome rides beside
	// it, since the absent value lives outside every set.
	LoopEffectOrAbsent LoopEffectKind = "orAbsent"
	// LoopEffectThrown is what an ESCAPING THROW writes into the result
	// slot: a run that left by throwing produced no completion at all.
	// It is its own kind rather than a flag on constState, because the
	// outcome it writes is not a value and no set can hold it — the
	// kernel's fourth Outcome constructor, one step further out than
	// the absent VALUE.
	//
	// This is what keeps `if (x) throw new E(); return v` serving `v`
	// rather than `v ∪ undefined`: the throw arm writes the thrown
	// outcome, the join with the real return raises only the thrown
	// flag, and the kernel's ret-row split then reads the return alone.
	// Sending AbsentConst here instead is what merged the two.
	LoopEffectThrown LoopEffectKind = "thrown"
)

// LoopEffectOp is the op field of a unary or binary LoopEffect.
type LoopEffectOp string

const (
	LoopOpNeg   LoopEffectOp = LoopEffectOp(TransferOpNeg)
	LoopOpFloor LoopEffectOp = LoopEffectOp(TransferOpFloor)
	LoopOpCeil  LoopEffectOp = LoopEffectOp(TransferOpCeil)
	LoopOpRound LoopEffectOp = LoopEffectOp(TransferOpRound)
	LoopOpTrunc LoopEffectOp = LoopEffectOp(TransferOpTrunc)
	LoopOpAbs   LoopEffectOp = LoopEffectOp(TransferOpAbs)
	LoopOpAdd   LoopEffectOp = LoopEffectOp(TransferOpAdd)
	LoopOpSub   LoopEffectOp = LoopEffectOp(TransferOpSub)
	LoopOpMul   LoopEffectOp = LoopEffectOp(TransferOpMul)
	LoopOpDiv   LoopEffectOp = LoopEffectOp(TransferOpDiv)
	LoopOpRem   LoopEffectOp = LoopEffectOp(TransferOpRem)
	LoopOpMin   LoopEffectOp = LoopEffectOp(TransferOpMin)
	LoopOpMax   LoopEffectOp = LoopEffectOp(TransferOpMax)
	// The bitwise and shift operators. These spell the same six names
	// the transfer wire uses, and the kernel's loopOp2Of reads them
	// into the effect grammar's LoopOp2; their images come from
	// transferBitwise, the exactly-specified int32/uint32 functions.
	LoopOpBitOr  LoopEffectOp = LoopEffectOp(TransferOpBitOr)
	LoopOpBitAnd LoopEffectOp = LoopEffectOp(TransferOpBitAnd)
	LoopOpBitXor LoopEffectOp = LoopEffectOp(TransferOpBitXor)
	LoopOpShl    LoopEffectOp = LoopEffectOp(TransferOpShl)
	LoopOpSar    LoopEffectOp = LoopEffectOp(TransferOpSar)
	LoopOpShr    LoopEffectOp = LoopEffectOp(TransferOpShr)
	// The bounded-image unaries. These spell the same names the
	// transfer wire uses, and the kernel's loopOp1Of reads them into
	// LoopOp1. On the EFFECT wire their image is the interval each
	// operation's own clause names and nothing finer — the transfer
	// wire's tight windows need a singleton operand, which a
	// loop-carried binding is not:
	//
	//   sqrt → [0, +∞)   (sec-math.sqrt: every non-NaN row is a square
	//                     root or one of +0/-0/+∞)
	//   sin  → [-1, 1]   (sec-math.sin)
	//   cos  → [-1, 1]   (sec-math.cos)
	//   atan → [-2, 2]   (sec-math.atan: "in the inclusive interval
	//                     from 𝔽(-π / 2) to 𝔽(π / 2)")
	LoopOpSqrt LoopEffectOp = LoopEffectOp(TransferOpSqrt)
	LoopOpSin  LoopEffectOp = LoopEffectOp(TransferOpSin)
	LoopOpCos  LoopEffectOp = LoopEffectOp(TransferOpCos)
	LoopOpAtan LoopEffectOp = LoopEffectOp(TransferOpAtan)
	// LoopOpPow is `**` and Math.pow, which the kernel evaluates with
	// the same transferPow the transfer wire answers with: the pinned
	// Number::exponentiate rows (sec-numeric-types-number-exponentiate)
	// plus the exact integer path, and unknown where the
	// implementation-approximated remainder is reachable.
	LoopOpPow LoopEffectOp = LoopEffectOp(TransferOpPow)
	// LoopOpAtan2 is the two-argument inverse tangent, bounded by its
	// own clause's interval: sec-math.atan2 states the result "is in
	// the inclusive interval from -π to +π", so [-4, 4] encloses it.
	LoopOpAtan2 LoopEffectOp = LoopEffectOp(TransferOpAtan2)
	// The SEQUENCE unaries, read by the kernel's seqOp1Of. These are the
	// string methods whose result set is provable from the receiver's
	// set alone, and the claim they carry is the DRAWN-FROM one: every
	// scalar of the result already occurred in the receiver, and the
	// result is no longer. The kernel keeps the receiver's character
	// class and its length CEILING, and drops the floor -- a trim can
	// empty the word.
	//
	// This is sound because sec-trimstring removes leading and/or
	// trailing white space by CODE POINT, so no surrogate pair is split
	// and no scalar the receiver never held can appear.
	LoopOpTrim      LoopEffectOp = "seq.trim"
	LoopOpTrimStart LoopEffectOp = "seq.trimStart"
	LoopOpTrimEnd   LoopEffectOp = "seq.trimEnd"
	// LoopOpSplitElemSafe is the ELEM half of `s.split(sep)`: what one
	// piece may hold. A piece is a contiguous stretch of the receiver, so
	// its scalars all occurred there and it is no longer than the
	// receiver -- the same drawn-from claim the trims carry, which is why
	// it rides the same kernel row.
	//
	// The name carries a GATE, and the gate is what separates it from
	// slice. A slice cuts at a caller-chosen code-unit position and can
	// split a surrogate pair; a split cuts only where the separator
	// MATCHES, and a match of a well-formed separator begins and ends on
	// a scalar boundary. The exception is a separator that is itself a
	// lone surrogate, which can match one half of an astral pair. Send
	// this op ONLY with the separator established astral-safe, or the
	// receiver established astral-free; the kernel refuses a bare
	// "split", so an ungated lowering gets no claim rather than a wrong
	// one.
	LoopOpSplitElemSafe LoopEffectOp = "seq.splitElem"
	// LoopOpIndexOf is the numeric-from-sequence op: the UTF-16 index of
	// a first match, or -1 (sec-string.prototype.indexof). The kernel
	// answers the window the receiver's set supports -- {-1} u [0, 2*hi)
	// for a receiver with scalar-count ceiling hi, since TERMS-v2 §12
	// pins a scalar-count-n word's code-unit length at n <= length <= 2n
	// -- and {-1} u [0, +inf) with integrality when the receiver states
	// no ceiling. Nothing gates it: the window holds for every needle.
	LoopOpIndexOf LoopEffectOp = "seq.indexOf"
	// LoopOpCharCodeAt is the numeric-from-sequence op for
	// `s.charCodeAt(i)` (sec-string.prototype.charcodeat). Unlike
	// indexOf's window, this one does NOT depend on the receiver's own
	// set at all -- it is fixed by the language's own declaration
	// (bmpCap): every in-range answer is a UTF-16 code unit, an integer
	// in [0, 0xFFFF]. The kernel raises the NaN flag on the WRITTEN
	// STATE unconditionally rather than narrowing the window, since no
	// receiver-side fact bounds whether a given call falls in range
	// (evalSeqNum's `.charCodeAt` arm, set_functions/walk_sequences.lean)
	// -- so this op alone, un-wrapped, already states the full claim:
	// a code unit or NaN, never absent, never thrown.
	LoopOpCharCodeAt LoopEffectOp = "seq.charCodeAt"
	// LoopOpCodePointAt is charCodeAt's scalar-valued sibling for
	// `s.codePointAt(i)` (sec-string.prototype.codepointat): every
	// in-range answer is a scalar value, an integer in [0, 0x10FFFF],
	// the same receiver-independent claim charCodeAt carries. Out of
	// range the clause answers the undefined value rather than NaN --
	// an ABSENCE this family's window cannot spell on its own flags, so
	// the kernel leaves that half to the caller (evalSeqNum's
	// `.codePointAt` arm) exactly as an unguarded index read does: send
	// this op wrapped in LoopEffectOrAbsent, never bare.
	LoopOpCodePointAt LoopEffectOp = "seq.codePointAt"
	// LoopOpSliceBmp is `s.slice(...)` over a receiver whose alphabet the
	// KERNEL proves astral-free. String.prototype.slice cuts at UTF-16
	// code UNIT positions (sec-string.prototype.slice), so on an
	// astral-bearing receiver a cut can fall inside a surrogate pair and
	// mint a lone surrogate the receiver's set never admitted -- which is
	// why the bare op has no name at all. Where every scalar is in the
	// BMP each is exactly one code unit, so unit positions and scalar
	// positions coincide, every cut lands on a scalar boundary, and the
	// piece is a contiguous subsequence of the receiver: the same
	// drawn-from claim the trims carry, on the same row.
	//
	// UNLIKE LoopOpSplitElemSafe, this name carries NO promise. A split's
	// premise is about the SEPARATOR, which the kernel never sees, so the
	// adapter must establish it. This premise is about the receiver's own
	// SET, which the kernel holds -- so the kernel decides it itself
	// (`bmpAlphabetB`, set_functions/walk.lean) and answers top on a
	// receiver whose set does not state the bound. Send it on syntax
	// alone; an ungated receiver costs the claim, never soundness.
	LoopOpSliceBmp LoopEffectOp = "seq.sliceBmp"
	// LoopOpUpperAscii and LoopOpLowerAscii are `toUpperCase` and
	// `toLowerCase` over a receiver whose alphabet the KERNEL proves
	// ASCII. Case mapping REPLACES scalars, so no drawn-from claim holds
	// at all -- "a".toUpperCase() holds an "A" the receiver never did.
	// What holds instead is that the result is the receiver mapped
	// scalar-by-scalar: sec-string.prototype.tolowercase maps by code
	// point (StringToCodePoints, the Unicode Default Case Conversion,
	// then CodePointsToString), and toUpperCase is the same with the
	// toUppercase algorithm.
	//
	// The ASCII premise is what makes that a per-scalar function. The
	// clause's own note says the result "may not be the same length as
	// the source String" because "the case mapping of some code points
	// may produce multiple code points" -- the SpecialCasing rows it
	// names. Below U+0080 none applies, so the mapping is simple and
	// length is exactly preserved, which is why this row keeps BOTH
	// repetition bounds where the drawn-from rows drop the floor.
	//
	// The gate and the MAPPING are both the kernel's: it decides the
	// alphabet bound (`asciiAlphabetB`) and states the image class
	// itself, rather than trusting a mapped class off this wire. The
	// adapter cannot be the authority on a Unicode mapping the kernel is
	// claiming soundness for.
	LoopOpUpperAscii LoopEffectOp = "seq.upperAscii"
	LoopOpLowerAscii LoopEffectOp = "seq.lowerAscii"
	// LoopOpTan is Math.tan. Its image is the whole line -- the tangent
	// runs to both infinities between consecutive poles -- so the row
	// claims no bound on the VALUE. What it claims is the SORT, and that
	// is a real determination: refusing the op answers the kernel's
	// `top`, which admits the absent value and a thrown exit alongside
	// every number (`KnownState.denotes`, set_functions/known_state.lean).
	// The row says the slot holds a NUMBER, possibly NaN, and never
	// either of those, which is what a later `defined` test reads off.
	//
	// sec-math.tan: step 2 returns n itself at NaN and both zeros, step 3
	// answers NaN at both infinities, step 4 is the
	// implementation-approximated tangent. Every non-NaN outcome is a
	// Number, and the claimed interval is [-inf, +inf].
	LoopOpTan LoopEffectOp = LoopEffectOp(TransferOpTan)
	// LoopOpReplaceUnionSafe is `s.replace(pattern, replacement)` and
	// `s.replaceAll(...)` where the REPLACEMENT is a string this side
	// holds exactly. It replaced the old outright decline, whose reason
	// was that a substitution injects caller-chosen text so neither the
	// drawn-from nor the case-image closure applies. That reason holds
	// for a replacement this side does not know -- and stops holding
	// where it does.
	//
	// The claim is the closure over the UNION alphabet.
	// sec-string.prototype.replace returns the string-concatenation of
	// `preceding`, `replacement` and `following`; the outer two are
	// substrings of the receiver, so their scalars are the receiver's,
	// and the middle is GetSubstitution of a template this side holds,
	// so its scalars are the replacement's. Every result scalar is
	// therefore in A u B, which is the kernel's `repeatN_drawnFromUnion`
	// row.
	//
	// THE `$` SUBSTITUTIONS DO NOT LEAK THE ALPHABET. Read against
	// sec-getsubstitution on the string-pattern path: `$$` yields "$",
	// which a template spelling `$$` contains; "$`", "$&" and "$'" yield
	// spans of the receiver ("$&" is _matched_, which StringIndexOf found
	// IN the receiver); "$n" and "$<...>" fall through to the literal
	// _ref_ because _captures_ is "a new empty List" and _namedCaptures_
	// is *undefined* on this path; and the default row copies one code
	// unit of the template. Every branch lands in A u B already.
	//
	// They DO leak the LENGTH, which is why the adapter declines a
	// template containing `$`: "$&" expands to the match and "$`"/"$'"
	// to whole receiver spans, so the substituted text is not bounded by
	// the template's length and no finite Bump is sound.
	//
	// A FUNCTIONAL replacement is never sent: its text is the ToString of
	// a Call, so no set holds it and the union has no second half.
	//
	// THE CEILING rides in Bump, and it is why the two methods differ.
	// `replace` rewrites the FIRST match only, so the result is at most
	// the receiver plus the replacement -- a sound bump. `replaceAll`
	// loops every match, so the injected text multiplies by a count no
	// receiver set bounds; it is sent with NO ceiling to raise, which the
	// kernel reads as the ceiling-free claim.
	//
	// THE GATE IS THIS SIDE'S, like LoopOpSplitElemSafe and unlike
	// LoopOpSliceBmp: the premise is about the PATTERN and the
	// REPLACEMENT, values the kernel never sees. A string pattern's match
	// begins and ends on a scalar boundary for split's reason, and the
	// hole is split's hole -- a pattern that is itself a lone surrogate
	// can match half an astral pair. Send this op ONLY with the pattern
	// and the replacement established astral-safe.
	LoopOpReplaceUnionSafe LoopEffectOp = "seq.replaceUnion"
	// LoopOpReplaceSortSafe is the replace family's SORT row -- the
	// string world's LoopOpTan. Where the union row's gates fail, an
	// outright refusal answers the kernel's `top`, which admits the
	// absent value and a thrown exit beside every word. This row claims
	// exactly what `top` gives away: the result is a STRING -- some
	// word, no alphabet and no length bounded -- never absent, never
	// NaN, and the call never a thrown exit.
	//
	// The shapes it covers, each refused by the union row for a reason
	// that does not touch the sort:
	//   - a `g`-flagged regex and `replaceAll` (multi-match: no sound
	//     length ceiling exists, but every result is still the
	//     concatenation of receiver spans and GetSubstitution outputs --
	//     sec-regexp.prototype-%symbol.replace% steps 14-17,
	//     sec-string.prototype.replaceall steps 14-16);
	//   - a `$` in the replacement template (sec-getsubstitution: "$&",
	//     "$`" and "$'" expand to receiver spans, so no finite Bump is
	//     sound; every branch still yields a String);
	//   - a regex without `u`/`v` (the matcher walks code units, so a
	//     match can split an astral pair and mint lone surrogates the
	//     union alphabet never admitted; a lone surrogate is still a
	//     scalar and the result still a String).
	//
	// NEVER THROWN is the load-bearing half, and the gate is THIS
	// SIDE'S, carried in the name like LoopOpSplitElemSafe's: send it
	// only with the receiver stated a string, the pattern an
	// exactly-spelled string or a regex literal, the replacement an
	// exactly-spelled string (never a function), and -- under
	// `replaceAll` at a regex -- the `g` flag proven present in the
	// literal, because sec-string.prototype.replaceall step 2.a.iii
	// throws a TypeError without it and that is the family's one
	// throwing shape. On those shapes every `?` step of
	// sec-string.prototype.replace / .replaceall runs over Strings,
	// GetSubstitution is invoked with `!` (captures empty,
	// namedCaptures undefined), and the regex road runs over a regex
	// literal's intrinsic behaviour, none of which throws.
	LoopOpReplaceSortSafe LoopEffectOp = "seq.replaceSort"
	// LoopOpReplaceSortThrowSafe is the replace family's
	// FUNCTIONAL-REPLACER row -- LoopOpReplaceSortSafe's claim, PLUS the
	// thrown flag, for a REPLACEMENT that is caller code rather than an
	// exactly-known string. sec-string.prototype.replace,
	// sec-string.prototype.replaceall and
	// sec-regexp.prototype-%symbol.replace% each read
	// `_functionalReplace_ := IsCallable(_replaceValue_)` and, where
	// true, compute the replacement as
	// `? ToString(? Call(_replaceValue_, ...))` -- caller code this side
	// cannot see the body of, so it may throw. Every run that DOES
	// complete still reaches the same three-piece String concatenation
	// LoopOpReplaceSortSafe claims a word for, so the two outcomes
	// admitted are exactly "a word" and "a thrown exit" -- never absent,
	// never NaN.
	//
	// THE GATE is the receiver and pattern shape alone -- the same
	// premise LoopOpReplaceSortSafe needs minus the replacement's own
	// exactness, since this row does not need to read the replacement
	// at all: receiver stated a string, pattern an exactly-spelled
	// string or a regex literal, and -- under `replaceAll` at a regex
	// -- the `g` flag proven present (sec-string.prototype.replaceall
	// step 2.a.iii is a TypeError without it, independent of the
	// replacer).
	//
	// THE WRITE HALF is not this row's claim: a functional replacer may
	// also write names this body tracks, which is why the adapter sends
	// this op only alongside havocking the replacer's write set
	// (ClosureEscapesTrackedWrite names the boundary) -- exactly as any
	// other escaping-closure call site is handled.
	LoopOpReplaceSortThrowSafe LoopEffectOp = "seq.replaceSortThrow"
	// LoopOpRepeatElem is `s.repeat(n)` (sec-string.prototype.repeat): n
	// concatenated copies of the receiver. The claim is the SAME
	// drawn-from closure the trims and split carry -- every result
	// scalar already occurred in the receiver -- with the length
	// CEILING dropped entirely rather than bounded by n, since n itself
	// is not sent (`repeatElemForm`, theories/seq/repeat_elem.lean: the
	// receiver's own alphabet survives, unbounded length). Nothing
	// gates it: the closure holds for every receiver and every
	// non-negative count, which is why the count argument does not
	// ride the wire at all -- only the receiver, in A. A negative or
	// infinite count THROWS rather than returning (RangeError,
	// sec-string.prototype.repeat step 3/4), so the caller must
	// establish the count non-negative and finite before sending this
	// op; the row states nothing about the thrown shape.
	LoopOpRepeatElem LoopEffectOp = "seq.repeatElem"
	// LoopOpPadUnion is `s.padStart(n, pad)` / `s.padEnd(n, pad)`
	// (sec-stringpad) over a receiver the kernel reads as a REPETITION
	// shape (`Repeat A lo _`): the union-alphabet claim, `pad_union.lean`'s
	// `padUnionForm` -- the result's alphabet becomes the receiver's
	// scalars UNION the fill text's (every result scalar is drawn from
	// one side or the other, since StringPad only ever concatenates
	// fill text onto the receiver, never deletes from it), the FLOOR
	// kept unchanged (a pad never shortens), and the CEILING dropped
	// entirely -- no length-budget parameter rides this wire the way
	// replaceUnion's Bump does, so the kernel states no ceiling rather
	// than guess one from n.
	//
	// THE GATE is the receiver's own SET, which the kernel decides
	// itself (padUnionForm matches only a Repeat form) -- the same
	// shape-decided-kernel-side discipline LoopOpSliceBmp carries, not
	// the adapter-vouched premise LoopOpReplaceUnionSafe needs. Send it
	// on syntax alone; a receiver whose set is not Repeat-shaped costs
	// the claim, never soundness (padUnionSet's own filterMap drops any
	// non-Repeat form silently).
	//
	// The fill text rides in the effect's own PadSet field, the same
	// operand shape LoopOpReplaceUnionSafe's ReplSet carries -- the
	// kernel needs the fill's scalar set to STATE the union, and cannot
	// see it any other way. Neither n nor which side (start/end) rides:
	// the union-alphabet claim is symmetric in both, and n decides only
	// the ceiling, which this row already leaves unstated.
	LoopOpPadUnion LoopEffectOp = "seq.padUnion"
)

// LoopEffect is one binding's body effect, lowered for the kernel's
// loop solver: the binding values at entry to a pass, known sets for
// everything read from outside, the arithmetic the body performs, and
// joins where control flow splits. `unknown` is the honest leaf for
// anything the reading could not vouch for. Collapsed to one struct
// with a Kind tag; A/B hold operands (as *LoopEffect since the TS
// variant is recursive), Index the var index, Set the const set.
type LoopEffect struct {
	Kind LoopEffectKind

	Index int                       // "var" / "varState" / "sq"
	Set   refinementsets.RefinedSet // "const" / "constState"

	// Undef, Null, Nan: "constState" only — whether the written constant
	// may be exactly undefined, exactly null, or NaN, none of which any
	// set can hold. Undef and Null are the two SEPARATE absent
	// admissions (a legacy wire that sent one conflated `absent` bool
	// decodes as both); a state with both flags down and Nan down is
	// exactly LoopEffectConst.
	Undef bool
	Null  bool
	Nan   bool

	Op LoopEffectOp // "un" / "bin" / "seqUn" / "seqNum"
	A  *LoopEffect
	B  *LoopEffect // "bin" / "concat" / "join"

	// ReplSet, Bump: LoopOpReplaceUnionSafe only — the replacement's own
	// scalar set, and the number of scalars a single substitution may
	// add. The kernel needs both to STATE the union claim, which is what
	// separates this op from every gated one above: those carry premises
	// the kernel decides off the receiver's set, this one carries
	// OPERANDS the kernel cannot see and cannot reconstruct. A zero Bump
	// with no ceiling on the receiver is the ceiling-free form
	// `replaceAll` rides.
	ReplSet refinementsets.RefinedSet
	Bump    int

	// PadSet: LoopOpPadUnion only — the fill text's own scalar set. The
	// kernel needs it to STATE the union claim (padUnionForm's `A ∪ B`),
	// the same reason ReplSet exists for LoopOpReplaceUnionSafe — this
	// is an OPERAND the kernel cannot see and cannot reconstruct from
	// the receiver's set alone. No Bump twin: padUnionForm drops the
	// ceiling entirely rather than reading a length budget off the
	// wire.
	PadSet refinementsets.RefinedSet
}

// AbsentConst is the state constant exactly `undefined` writes: the
// empty set of values beside a raised Undef flag. Every caller means
// a missing or uninitialized read — an empty array element, a cleared
// map entry, an uninitialized declaration, an absent callback
// argument, a throw's own ret slot — never the null literal, which
// carries its own flag (Null) this constant leaves down. Under ANY
// target sort — the undefined value is not a number and not a word,
// so no sort can hold it in its set part.
func AbsentConst() LoopEffect {
	return LoopEffect{
		Kind:  LoopEffectConstState,
		Set:   refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
		Undef: true,
	}
}

// NullConst is the state constant exactly the `null` LITERAL writes:
// the empty set of values beside a raised Null flag, the undefined
// admission down — `x = null` then `x === undefined` is decidably
// false, which the conflated constant could never say.
func NullConst() LoopEffect {
	return LoopEffect{
		Kind: LoopEffectConstState,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
		Null: true,
	}
}

// NanConst is the state constant exactly the global `NaN` writes: the
// empty set of values beside a raised Nan flag — NaN is not an element
// of ℝ̄ (refinement_forms.go's boundary ruling), so it rides the state
// flag the wire already carries for it. The global NaN property's
// initial value is NaN and the property is non-writable
// (sec-value-properties-of-the-global-object-nan, specifications/javascript/spec.html:
// "The initial value of the "NaN" property of the global object is NaN
// ... This property has the attributes { [[Writable]]: false,
// [[Enumerable]]: false, [[Configurable]]: false }."), so a read of the
// unshadowed name is exactly this constant.
func NanConst() LoopEffect {
	return LoopEffect{
		Kind: LoopEffectConstState,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
		Nan:  true,
	}
}

// ThrownConst is what an escaping throw writes into the result slot:
// the run produced no completion at all. It carries no set and no
// flags — the outcome it names lives outside everything a set holds,
// and the kernel writes the empty set with the thrown flag alone.
//
// Use this, never AbsentConst, for a throw that leaves the body: a
// throw did not return undefined, it returned NOTHING, and the two
// are distinct outcomes to the kernel.
func ThrownConst() LoopEffect {
	return LoopEffect{Kind: LoopEffectThrown}
}

// EffectWire is effectWire in the TS source.
func EffectWire(e LoopEffect) string {
	switch e.Kind {
	case LoopEffectVar:
		return fmt.Sprintf(`{"var":%d}`, e.Index)
	case LoopEffectVarState:
		return fmt.Sprintf(`{"varState":%d}`, e.Index)
	case LoopEffectSquare:
		return fmt.Sprintf(`{"sq":%d}`, e.Index)
	case LoopEffectConst:
		return fmt.Sprintf(`{"set":%s}`, EncodeSet(e.Set))
	case LoopEffectConstState:
		return fmt.Sprintf(`{"set":%s,"undef":%v,"null":%v,"nan":%v}`,
			EncodeSet(e.Set), e.Undef, e.Null, e.Nan)
	case LoopEffectUnknown:
		return `{"unknown":true}`
	case LoopEffectUnary:
		return fmt.Sprintf(`{"op":"%s","A":%s}`, e.Op, EffectWire(*e.A))
	case LoopEffectBinary:
		return fmt.Sprintf(`{"op":"%s","A":%s,"B":%s}`, e.Op, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectConcat:
		return fmt.Sprintf(`{"concat":[%s,%s]}`, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectSeqUnary:
		// the substitution row carries its two operands beside the name;
		// padUnion carries its one extra operand (the fill's scalar set,
		// no length budget); every other sequence op writes the plain
		// two-field shape it always did
		if e.Op == LoopOpReplaceUnionSafe {
			return fmt.Sprintf(`{"seqOp":"%s","replSet":%s,"bump":%d,"A":%s}`,
				e.Op, EncodeSet(e.ReplSet), e.Bump, EffectWire(*e.A))
		}
		if e.Op == LoopOpPadUnion {
			return fmt.Sprintf(`{"seqOp":"%s","padSet":%s,"A":%s}`,
				e.Op, EncodeSet(e.PadSet), EffectWire(*e.A))
		}
		return fmt.Sprintf(`{"seqOp":"%s","A":%s}`, e.Op, EffectWire(*e.A))
	case LoopEffectSeqNum:
		return fmt.Sprintf(`{"seqNumOp":"%s","A":%s}`, e.Op, EffectWire(*e.A))
	case LoopEffectJoin:
		return fmt.Sprintf(`{"join":[%s,%s]}`, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectOrAbsent:
		return fmt.Sprintf(`{"orAbsent":%s}`, EffectWire(*e.A))
	case LoopEffectThrown:
		return `{"thrown":true}`
	}
	panic(fmt.Sprintf("EffectWire: unreached kind %q", e.Kind))
}
