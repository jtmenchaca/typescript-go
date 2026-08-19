// Loop and walk questions: entry premises, body effects, and the
// lowered IR statements the kernel iterates, widens, and certifies.
package kernelbridge

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// InvariantPremiseKind is the tag of an InvariantPremise.
type InvariantPremiseKind string

const (
	InvariantPremiseValues InvariantPremiseKind = "values"
	InvariantPremiseSet    InvariantPremiseKind = "set"
)

// InvariantPremise is one premise of the loop-invariant certificate: a
// concrete value (membership) or a set it lies in (subset).
type InvariantPremise struct {
	Kind   InvariantPremiseKind
	Values []float64
	Set    refinementsets.RefinedSet
}

// PremiseWire is premiseWire in the TS source.
func PremiseWire(p InvariantPremise) string {
	if p.Kind == InvariantPremiseValues {
		return fmt.Sprintf(`{"tuple":%s}`, EncodeTuple(p.Values))
	}
	return fmt.Sprintf(`{"set":%s}`, EncodeSet(p.Set))
}

// PremiseKey is premiseKey in the TS source.
func PremiseKey(p InvariantPremise) (string, bool) {
	if p.Kind == InvariantPremiseValues {
		return marshalWireValue(p.Values), true
	}
	key := CanonicalKeyOf(wireSet(p.Set))
	if key == nil {
		return "", false
	}
	return *key, true
}

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
	LoopEffectConst    LoopEffectKind = "const"
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

	Index int                       // "var" / "varState"
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
// (sec-value-properties-of-the-global-object-nan, tmp/ecma262/spec.html:
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
		// every other sequence op writes the two-field shape it always did
		if e.Op == LoopOpReplaceUnionSafe {
			return fmt.Sprintf(`{"seqOp":"%s","replSet":%s,"bump":%d,"A":%s}`,
				e.Op, EncodeSet(e.ReplSet), e.Bump, EffectWire(*e.A))
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

// LoopQuestion is the TS LoopQuestion interface.
type LoopQuestion struct {
	// Entry: per binding, the entry premise, or nil when nothing is
	// known.
	Entry []*InvariantPremise
	// Cond: per binding, the loop condition's narrowing set, if one
	// reads (nil otherwise).
	Cond []*refinementsets.RefinedSet
	// Body: per binding, the body's effect.
	Body []LoopEffect
	// CondCmp: the head when it compared two tracked slots. Nothing
	// constant bounds either side, so this rides instead of Cond and
	// the kernel cuts each pass entry by the bound the other slot's
	// own entry value states.
	CondCmp *IrLoopCondCmp
}

// LoopVarAnswerKind is the tag of a LoopVarAnswer.
type LoopVarAnswerKind string

const (
	LoopVarAnswerSet     LoopVarAnswerKind = "set"
	LoopVarAnswerUnknown LoopVarAnswerKind = "unknown"
)

// LoopVarAnswer is the TS LoopVarAnswer union.
type LoopVarAnswer struct {
	Kind LoopVarAnswerKind
	Set  refinementsets.RefinedSet
}

// IrStatementKind is the tag of an IrStatement.
type IrStatementKind string

const (
	IrStatementAssign IrStatementKind = "assign"
	IrStatementBranch IrStatementKind = "branch"
	// IrStatementBranchBoth is the branch whose CONDITION the walk does
	// not read: `if (m.has(k)) { … } else { … }` and every other
	// write-free test no leaf lowers. It reuses the Then and Else fields
	// and carries no On, Test, or operand — the walk claims nothing about
	// the condition, so both arms walk from the state as it stood and
	// their exits join. A concrete run may take either arm and the join
	// admits both, so an unreadable test costs precision at the merge and
	// never costs the body its lowering.
	IrStatementBranchBoth IrStatementKind = "branchBoth"
	IrStatementLoop       IrStatementKind = "loop"
	// IrStatementLoopStmts is the loop whose body is STATEMENTS rather
	// than one effect per binding. Any number of trips may run — zero
	// included — and the kernel havocs the body's own write set, which
	// it computes from the body statements itself and never trusts from
	// this wire. Written and Body stay nil: they are the effect-bodied
	// loop's fields.
	//
	// Cond, After and CondCmp DO ride here when the head reads, in the
	// effect loop's own spelling. The kernel refines the havoc with
	// them at the EXIT — a loop leaves only when its head fails, so
	// every slot intersects its falsity set and the two-slot head's
	// negation tightens on top.
	//
	// Cond also feeds the kernel's INVARIANT certificate, which the
	// certifying walk poses for this form: the entry row havocked on
	// the body's write set, cut by the head's TRUTH sets, is walked
	// through the body, and where it lands back inside itself the
	// kernel meets it into the exit. That is decided kernel-side from
	// these same fields — nothing new rides here for it — and a head
	// that does not read lowers all three empty, which certifies
	// nothing and leaves the plain havoc exit this form always had.
	// The certifying walk is opt-in at the walk question
	// (`certify`), because a compiled SUMMARY cannot mirror it.
	IrStatementLoopStmts IrStatementKind = "loopStmts"
	// IrStatementCall applies a callee's already-built summary. Callee
	// indexes the summary table the question carries beside the
	// statements; each Args entry is an effect over the CALLER's
	// bindings producing one callee entry state; Rets says where the
	// callee's out-states land — Rets[k] is the caller binding the
	// k-th out-state writes, and -1 there drops it.
	IrStatementCall IrStatementKind = "call"
	// IrStatementLoopCounted is the literal-bounded loop: the walk-side
	// exact unroll (walk/loop_unroll.go) steps a `for` whose trip count
	// the syntax pins a fixed number of times, no widening. This is its
	// kernel-portable twin — the same per-binding parallel effect the
	// effect-bodied loop carries in Body, composed with itself Count
	// times from the entry state rather than solved by the fixpoint.
	// Count comes from LiteralTripCountWith on the Go side; the kernel
	// mirrors LoopUnrollBudget as its own gate (loop_questions.go's
	// wire never carries a count past the budget — the Go lowering
	// falls back to the ordinary loop/loopStmts form there).
	IrStatementLoopCounted IrStatementKind = "loopCounted"
)

// IrBranchTest is the test field of a branch IrStatement.
type IrBranchTest string

const (
	IrTestDefined IrBranchTest = "js.defined"
	// IrTestEqUndef and IrTestEqNull are the STRICT flavored tests split
	// out of the old conflated absent marker: `x === undefined` /
	// `x === null` decide exactly one admission, leaving the other
	// (null on eqUndef's false arm, undefined on eqNull's false arm)
	// untouched — a strictly stronger claim than IrTestDefined's
	// either-admission split. Neither carries a `w` operand — the tested
	// value is fixed by the test's own name, the same shape
	// defined/truthyNum/truthyStr/isNan already have.
	IrTestEqUndef   IrBranchTest = "js.eqUndef"
	IrTestEqNull    IrBranchTest = "js.eqNull"
	IrTestTruthyNum IrBranchTest = "js.truthyNum"
	IrTestTruthyStr IrBranchTest = "js.truthyStr"
	IrTestIsNan     IrBranchTest = "binary64.isNan"
	IrTestEq        IrBranchTest = "eq"
	IrTestEqSeq     IrBranchTest = "eqSeq"
	IrTestLt        IrBranchTest = "lt"
	IrTestLe        IrBranchTest = "le"
	IrTestGt        IrBranchTest = "gt"
	IrTestGe        IrBranchTest = "ge"
	// The two-slot comparisons: the right operand is another tracked
	// slot (OnB), not a constant, so these carry no `w`. `i < n`
	// lowers here where `i < 10` lowers to IrTestLt. IrTestEqSlot is
	// the equality shape — `i === n` and `i == n`, which agree
	// between two number-sorted slots.
	IrTestLtSlot IrBranchTest = "ltSlot"
	IrTestLeSlot IrBranchTest = "leSlot"
	IrTestGtSlot IrBranchTest = "gtSlot"
	IrTestGeSlot IrBranchTest = "geSlot"
	IrTestEqSlot IrBranchTest = "eqSlot"
	// IrTestEqSeqSlot is SEQUENCE equality between two string-sorted
	// slots (`s === t`). It rides with OnB like the scalar two-slot
	// comparisons but narrows NEITHER arm: the two-slot tightening is
	// built from enclosure bounds and a word has none, so the kernel
	// walks both arms untouched and joins them. What it unlocks is
	// bodies that decline today because the guard has no lowering at
	// all.
	IrTestEqSeqSlot IrBranchTest = "eqSeqSlot"
)

// IsTwoSlotTest reports whether a branch test compares two tracked
// slots rather than a slot against a constant — the tests that ride
// with OnB and never with W.
func IsTwoSlotTest(t IrBranchTest) bool {
	switch t {
	case IrTestLtSlot, IrTestLeSlot, IrTestGtSlot, IrTestGeSlot, IrTestEqSlot,
		IrTestEqSeqSlot:
		return true
	}
	return false
}

// IrStatement is a lowered statement for the kernel's flow walk: an
// assignment of an effect to a binding, a branch that tests one
// binding and carries both arms, a branch that tests NOTHING and
// carries both arms (branchBoth), or a loop. The `w` operand rides only
// with test "eq". A loop carries, per binding of the whole walk:
// whether it writes the binding, the condition's narrowing set if one
// reads, and the body's effect ("var i" for a binding the body leaves
// alone) — the entry premises come from the walk's own states,
// kernel-side. A loop head that compared two tracked slots rides in
// CondCmp instead of the per-binding sets.
type IrStatement struct {
	Kind IrStatementKind

	// "assign"
	Target int
	Effect LoopEffect

	// "branch"
	On   int
	Test IrBranchTest
	W    *float64
	// OnB: the SECOND slot a two-slot comparison tests On against —
	// read only when Test is one of the *Slot comparisons, which
	// carry no W.
	OnB    int
	Points []float64 // "eqSeq": the compared tuple (a string's code points)
	Then   []IrStatement
	Else   []IrStatement

	// "loop"
	Written []bool
	Cond    []*refinementsets.RefinedSet
	// After: the condition's FALSITY set per binding — the loop exits
	// only when the condition fails, so the exit intersects it.
	After []*refinementsets.RefinedSet
	Body  []LoopEffect
	// CondCmp: a head that compared two tracked slots (`while (i < n)`)
	// rather than a slot against a constant. Nothing constant bounds
	// either side, so Cond and After stay nil and this rides instead:
	// the kernel tightens each slot's EXIT by the negated comparison's
	// ray read off the other slot's flagless exit.
	CondCmp *IrLoopCondCmp

	// "loopStmts"
	// Stmts: the body of a statement-bodied loop. Body above is the
	// effect-bodied loop's and stays nil here; Cond, After and CondCmp
	// are shared with the effect loop and carry this loop's head when
	// one reads, for the EXIT refinement alone.
	Stmts []IrStatement

	// "call"
	// Callee: which summary of the question's table this call applies.
	Callee int
	// Args: one effect per callee entry, read over the caller's bindings.
	Args []LoopEffect
	// Rets: per callee out-state, the caller binding it writes. -1 says
	// nothing reads that out-state and spells `null` on the wire.
	Rets []int

	// "loopCounted"
	// Count: the exact trip count the syntax pinned
	// (LiteralTripCountWith), gated at LoopUnrollBudget before this
	// form is ever built — the Go lowering falls back to "loop" past
	// the budget, so the kernel never has to re-check it, but the
	// kernel gates its own walk at the same budget anyway (mirrored
	// rather than trusted from the wire).
	Count int
	// CountedBody: one effect per binding, the SAME parallel form
	// LoopStatement's Body carries for the effect-bodied loop (the
	// incrementor folded in as the index binding's own effect) — the
	// kernel composes this with itself Count times from the entry
	// state, no widening.
	CountedBody []LoopEffect
}

// IrLoopCondCmp is a loop head comparing two tracked slots: On
// against OnB under Test, one of the *Slot comparisons.
type IrLoopCondCmp struct {
	On   int
	Test IrBranchTest
	OnB  int
}

func optSetWire(c *refinementsets.RefinedSet) string {
	if c == nil {
		return `{"none":true}`
	}
	return EncodeSet(*c)
}

// StmtWire is stmtWire in the TS source.
func StmtWire(s IrStatement) string {
	if s.Kind == IrStatementAssign {
		return fmt.Sprintf(`{"assign":{"target":%d,"e":%s}}`, s.Target, EffectWire(s.Effect))
	}
	if s.Kind == IrStatementCall {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = EffectWire(a)
		}
		rets := make([]string, len(s.Rets))
		for i, r := range s.Rets {
			// -1 is the out-state nothing reads: the wire says null there
			if r < 0 {
				rets[i] = "null"
			} else {
				rets[i] = strconv.Itoa(r)
			}
		}
		return fmt.Sprintf(
			`{"call":{"callee":%d,"args":[%s],"rets":[%s]}}`,
			s.Callee, strings.Join(args, ","), strings.Join(rets, ","),
		)
	}
	if s.Kind == IrStatementLoop {
		written := make([]string, len(s.Written))
		for i, w := range s.Written {
			written[i] = fmt.Sprintf("%v", w)
		}
		cond := make([]string, len(s.Cond))
		for i, c := range s.Cond {
			cond[i] = optSetWire(c)
		}
		after := make([]string, len(s.After))
		for i, a := range s.After {
			after[i] = optSetWire(a)
		}
		body := make([]string, len(s.Body))
		for i, b := range s.Body {
			body[i] = EffectWire(b)
		}
		condCmp := ""
		if s.CondCmp != nil {
			condCmp = fmt.Sprintf(
				`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
				s.CondCmp.On, s.CondCmp.Test, s.CondCmp.OnB,
			)
		}
		return fmt.Sprintf(
			`{"loop":{"written":[%s],"cond":[%s],"after":[%s],"body":[%s]%s}}`,
			strings.Join(written, ","), strings.Join(cond, ","),
			strings.Join(after, ","), strings.Join(body, ","), condCmp,
		)
	}
	if s.Kind == IrStatementBranchBoth {
		// no on, no test, no operand: the walk reads nothing about the
		// condition and both arms ride
		thn := make([]string, len(s.Then))
		for i, t := range s.Then {
			thn[i] = StmtWire(t)
		}
		els := make([]string, len(s.Else))
		for i, e := range s.Else {
			els[i] = StmtWire(e)
		}
		return fmt.Sprintf(
			`{"branchBoth":{"thn":[%s],"els":[%s]}}`,
			strings.Join(thn, ","), strings.Join(els, ","),
		)
	}
	if s.Kind == IrStatementLoopCounted {
		// count, then one effect per binding — the same per-binding shape
		// "loop" carries in Body, no cond/after/condCmp at all: the trip
		// count is exact, so there is nothing to widen and nothing to
		// certify
		body := make([]string, len(s.CountedBody))
		for i, e := range s.CountedBody {
			body[i] = EffectWire(e)
		}
		return fmt.Sprintf(
			`{"loopCounted":{"count":%d,"body":[%s]}}`,
			s.Count, strings.Join(body, ","),
		)
	}
	if s.Kind == IrStatementLoopStmts {
		// the body statements ride and the kernel reads the write set off
		// them; the head's falsity sets and two-slot shape ride beside
		// them when one reads, and are omitted entirely when it does not —
		// which is the wire this form has always spoken
		stmts := make([]string, len(s.Stmts))
		for i, st := range s.Stmts {
			stmts[i] = StmtWire(st)
		}
		head := ""
		if len(s.Cond) > 0 {
			cond := make([]string, len(s.Cond))
			for i, c := range s.Cond {
				cond[i] = optSetWire(c)
			}
			head += fmt.Sprintf(`,"cond":[%s]`, strings.Join(cond, ","))
		}
		if len(s.After) > 0 {
			after := make([]string, len(s.After))
			for i, a := range s.After {
				after[i] = optSetWire(a)
			}
			head += fmt.Sprintf(`,"after":[%s]`, strings.Join(after, ","))
		}
		if s.CondCmp != nil {
			head += fmt.Sprintf(
				`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
				s.CondCmp.On, s.CondCmp.Test, s.CondCmp.OnB,
			)
		}
		return fmt.Sprintf(
			`{"loopStmts":{"body":[%s]%s}}`,
			strings.Join(stmts, ","), head,
		)
	}
	operand := ""
	if IsTwoSlotTest(s.Test) {
		// the second operand is a slot, not a constant
		operand = fmt.Sprintf(`,"onB":%d`, s.OnB)
	} else if s.Test == IrTestEqSeq && s.Points != nil {
		operand = fmt.Sprintf(`,"t":%s`, EncodeTuple(s.Points))
	} else if s.Test != IrTestDefined && s.Test != IrTestEqUndef &&
		s.Test != IrTestEqNull && s.Test != IrTestTruthyNum &&
		s.Test != IrTestTruthyStr && s.Test != IrTestIsNan && s.W != nil {
		operand = fmt.Sprintf(`,"w":%s`, marshalWireValue(WireNumberOf(*s.W)))
	}
	thenParts := make([]string, len(s.Then))
	for i, t := range s.Then {
		thenParts[i] = StmtWire(t)
	}
	elseParts := make([]string, len(s.Else))
	for i, e := range s.Else {
		elseParts[i] = StmtWire(e)
	}
	return fmt.Sprintf(
		`{"branch":{"on":%d,"test":"%s"%s,"then":[%s],"else":[%s]}}`,
		s.On, s.Test, operand, strings.Join(thenParts, ","), strings.Join(elseParts, ","),
	)
}

// LoopWire is loopWire in the TS source.
func LoopWire(q LoopQuestion) string {
	entry := make([]string, len(q.Entry))
	for i, p := range q.Entry {
		if p == nil {
			entry[i] = `{"unknown":true}`
		} else {
			entry[i] = PremiseWire(*p)
		}
	}
	cond := make([]string, len(q.Cond))
	for i, c := range q.Cond {
		cond[i] = optSetWire(c)
	}
	body := make([]string, len(q.Body))
	for i, b := range q.Body {
		body[i] = EffectWire(b)
	}
	condCmp := ""
	if q.CondCmp != nil {
		condCmp = fmt.Sprintf(
			`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
			q.CondCmp.On, q.CondCmp.Test, q.CondCmp.OnB,
		)
	}
	return fmt.Sprintf(
		`{"entry":[%s],"cond":[%s],"body":[%s]%s}`,
		strings.Join(entry, ","), strings.Join(cond, ","),
		strings.Join(body, ","), condCmp,
	)
}

// InvariantWidenWireResult is the non-nil return of InvariantWidenWire.
type InvariantWidenWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// flattenUnionLeaves mirrors the TS flatten() closure in
// invariantWidenWire: walk a union tree's leaves in wire form.
func flattenUnionLeaves(set refinementsets.RefinedSet, leaves *[]string) {
	var f *refinementsets.Refinement
	if len(set.Forms) == 1 {
		f = &set.Forms[0]
	}
	if f != nil && f.Form == refinementsets.FormUnion {
		flattenUnionLeaves(*f.A_, leaves)
		flattenUnionLeaves(*f.B, leaves)
	} else {
		*leaves = append(*leaves, EncodeSet(set))
	}
}

// InvariantWidenWire is invariantWidenWire in the TS source: the
// widening invariant wire when the candidate is a union whose leaves
// include the step's set — the kernel builds base ∪ step itself
// (`invariantWidenB_certifies`). Returns ok=false when the shape does
// not apply (the TS `null`).
func InvariantWidenWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) (result InvariantWidenWireResult, ok bool) {
	if step.Kind != InvariantPremiseSet || len(candidate.Forms) != 1 {
		return InvariantWidenWireResult{}, false
	}
	form := candidate.Forms[0]
	if form.Form != refinementsets.FormUnion {
		return InvariantWidenWireResult{}, false
	}
	stepWire := EncodeSet(step.Set)
	var leaves []string
	flattenUnionLeaves(candidate, &leaves)
	at := -1
	for i, leaf := range leaves {
		if leaf == stepWire {
			at = i
			break
		}
	}
	var rest []string
	if at != -1 {
		for i, leaf := range leaves {
			if i != at {
				rest = append(rest, leaf)
			}
		}
	}
	if len(rest) == 0 {
		return InvariantWidenWireResult{}, false
	}
	base := rest[0]
	for _, v := range rest[1:] {
		base = fmt.Sprintf(`{"forms":[{"form":"union","A":%s,"B":%s}]}`, base, v)
	}
	wire := fmt.Sprintf(`{"entry":%s,"base":%s,"step":%s}`, PremiseWire(entry), base, stepWire)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	if canonC == nil || !canonEOK {
		return InvariantWidenWireResult{Wire: wire}, true
	}
	return InvariantWidenWireResult{
		Wire: wire, Key: fmt.Sprintf("widen:%s%s", *canonC, canonE), HasKey: true,
	}, true
}

// InvariantAskWireResult is the return of InvariantAskWire.
type InvariantAskWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// InvariantAskWire is invariantAskWire in the TS source.
func InvariantAskWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) InvariantAskWireResult {
	wire := fmt.Sprintf(
		`{"candidate":%s,"entry":%s,"step":%s}`,
		EncodeSet(candidate), PremiseWire(entry), PremiseWire(step),
	)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	canonS, canonSOK := PremiseKey(step)
	if canonC == nil || !canonEOK || !canonSOK {
		return InvariantAskWireResult{Wire: wire}
	}
	return InvariantAskWireResult{
		Wire: wire, Key: fmt.Sprintf("%s\x01%s\x01%s", *canonC, canonE, canonS), HasKey: true,
	}
}

// DecodeLoopVars is decodeLoopVars in the TS source.
func DecodeLoopVars(parsed map[string]any) []LoopVarAnswer {
	rawVars, ok := parsed["vars"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
	}
	vars := make([]LoopVarAnswer, len(rawVars))
	for i, rv := range rawVars {
		v, ok := rv.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
		}
		if v["kind"] == "set" {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerSet, Set: DecodeWireSet(v["set"])}
		} else {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerUnknown}
		}
	}
	return vars
}
