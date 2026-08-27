// from comparison/comparison_decision.ts
//
// One comparison, decided. Every finite row is a MEMBERSHIP question
// to the kernel — `a < b` asks whether a is in the set below b — so
// the answer is proved rather than computed here. The rows the kernel
// cannot be asked are transcribed from the specification and marked
// as such: NaN fails every comparison and passes every disequality,
// absence compares only under `==`, and infinite words order by the
// extended reals rather than by float arithmetic.
//
// comparison/'s one file (compareKnown reads FlowContext) joins
// package walk at integration per PORT.md's "the walk package" note —
// ported here directly since evaluate_operators.ts and
// binary_comparison.ts need it now; the placeholder `comparison`
// package (internal/refinedts/comparison/comparison_decision.go) is
// deleted at integration.

package walk

import (
	"errors"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

var errKernelRefused = errors.New("kernel refused the question")

// compareSetAgainstNumber decides `<set> op <one number>` (either
// order) through the kernel's narrow claims: each arm's claim meets
// the set, and an EMPTY meet proves that arm impossible. A set
// excludes NaN by construction, so an impossible true arm reads
// "false on every run" and an impossible false arm "true on every
// run", both exactly. out is nil where a claim declines, both arms
// stay possible, or the kernel refuses (the recover); matched is
// false where the shape itself is not one numeric set against one
// exact number, so the caller can tell "wrong shape, keep looking
// for another row" apart from "right shape, genuinely either
// truth value" (out nil, matched true) — the latter still
// determines the boolean ground, never a decline.
func compareSetAgainstNumber(ctx *FlowContext, op ComparisonOp, a, b abstractdomain.AbstractValue, kernelRow func(bool) abstractdomain.AbstractValue) (out *abstractdomain.AbstractValue, matched bool) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	singleNumber := func(v abstractdomain.AbstractValue) (float64, bool) {
		if v.Kind == abstractdomain.KindValues && v.KindTag == abstractdomain.PrimitiveNumber && len(v.Values) == 1 {
			return v.Values[0], true
		}
		return 0, false
	}
	set := a
	number, isNumber := singleNumber(b)
	mirrored := false
	if a.Kind != abstractdomain.KindSet || a.SetKindTag != abstractdomain.SetKindTagNone || !isNumber {
		set = b
		number, isNumber = singleNumber(a)
		mirrored = true
		if b.Kind != abstractdomain.KindSet || b.SetKindTag != abstractdomain.SetKindTagNone || !isNumber {
			return nil, false
		}
	}
	mapped := op
	if mirrored {
		switch op {
		case CompareLt:
			mapped = CompareGt
		case CompareGt:
			mapped = CompareLt
		case CompareLe:
			mapped = CompareGe
		case CompareGe:
			mapped = CompareLe
		}
	}
	var tree kernelbridge.NarrowTree
	switch mapped {
	case CompareEq:
		tree = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: number}
	case CompareNe:
		eq := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: number}
		tree = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindNot, A: &eq}
	default:
		tree = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowCmpOp(mapped), K: number}
	}
	answer := ctx.Kernel.Narrow(tree)
	armImpossible := func(claim *kernelbridge.NarrowClaim) bool {
		if claim == nil {
			return false
		}
		met := refinementsets.RefinedSet{Forms: append(append([]refinementsets.Refinement{}, set.Set.Forms...), claim.Set.Forms...)}
		return ctx.Kernel.ScalarEmpty(met)
	}
	if armImpossible(answer.WhenTrue) {
		v := kernelRow(false)
		return &v, true
	}
	if armImpossible(answer.WhenFalse) {
		v := kernelRow(true)
		return &v, true
	}
	return nil, true
}

// kernelRefusedTheQuestionSaid is the sentence every callMember/
// callSeqLexLt recovery site attaches: the membership or ordering
// question was well-formed enough to ask, but the kernel declined to
// answer it.
const kernelRefusedTheQuestionSaid = "the kernel refused this membership or ordering question"

// compareSetAgainstString decides `<sequence set> === <one exact
// string>` (either order) through ONE membership question — is the
// literal's codepoint tuple a member of the set? A NEGATIVE answer
// proves both arms: === is false and !== is true on every run (the
// literal cannot equal any member of a set that excludes it), exactly
// the disjointness a narrowed grammar (a regex .test guard, say)
// proves against a literal outside its language. A POSITIVE answer
// does not decide the comparison (the set can hold other members
// too), so it falls through to the boolean ground the same way an
// undecided compareSetAgainstNumber arm does. Ordering (<, <=, >, >=)
// against a set has no row here — SeqLexLt's own callMember-style
// wrapper answers one pair, not a set-wide bound, so it stays out of
// scope for this row. matched is false where the shape is not one
// sequence set against one exact string, so the caller can keep
// looking for another row.
func compareSetAgainstString(ctx *FlowContext, op ComparisonOp, a, b abstractdomain.AbstractValue, kernelRow func(bool) abstractdomain.AbstractValue) (out *abstractdomain.AbstractValue, matched bool) {
	if op != CompareEq && op != CompareNe {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	set := a
	literal := b
	if a.Kind != abstractdomain.KindSet || a.SetKindTag != abstractdomain.SetKindTagNone {
		set = b
		literal = a
		if b.Kind != abstractdomain.KindSet || b.SetKindTag != abstractdomain.SetKindTagNone {
			return nil, false
		}
	}
	if literal.Kind != abstractdomain.KindValues || literal.KindTag != abstractdomain.PrimitiveString {
		return nil, false
	}
	if abstractdomain.KindOfClaim(set) != abstractdomain.ClaimSortString {
		return nil, false
	}
	member, err := callMember(ctx.Kernel, set.Set, literal.Values)
	if err != nil {
		return nil, true
	}
	if !member {
		v := kernelRow(op == CompareNe)
		return &v, true
	}
	return nil, true
}

// ComparisonOp is the "lt" | "gt" | "le" | "ge" | "eq" | "ne" union.
type ComparisonOp string

const (
	CompareLt ComparisonOp = "lt"
	CompareGt ComparisonOp = "gt"
	CompareLe ComparisonOp = "le"
	CompareGe ComparisonOp = "ge"
	CompareEq ComparisonOp = "eq"
	CompareNe ComparisonOp = "ne"
)

// CompareKnown is compareKnown in the TS source.
func CompareKnown(ctx *FlowContext, op ComparisonOp, strict bool, a, b abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	operandTrustLevel := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(a), abstractdomain.TrustLevelOf(b))
	// kernel-decided rows carry the operands' grade; the transcribed
	// corner tables (NaN, absence, the extended-real order) dip to spec
	kernelRow := func(v bool) abstractdomain.AbstractValue {
		return boolValue(v, operandTrustLevel)
	}
	boolAt := func(v bool) abstractdomain.AbstractValue {
		return boolValue(v, abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec))
	}
	if a.Kind == abstractdomain.KindNaN || b.Kind == abstractdomain.KindNaN {
		return boolAt(op == CompareNe)
	}
	// a possibly-NaN operand (Date.getTime()/valueOf(), a calendar getter
	// on an unknown receiver, any other spec window riding NaN beside it
	// per date_models.go's readDateMethods) decides in two arms: NaN
	// forces the NaN-corner answer above (op == CompareNe), and the real
	// half compares as usual — the same unwrap-compute-rewrap arithmetic_
	// transfer.go's TransferBinary already runs for +/-/etc, joined
	// instead of rewrapped since a comparison's result is a boolean, not
	// a number that stays possibly-NaN. Where the two arms agree the join
	// collapses to one exact boolean (JoinKnown's SameKnown shortcut);
	// where they disagree the honest answer is "could be either" — the
	// boolean ground below, not a decline.
	if a.Kind == abstractdomain.KindPossiblyNaN || b.Kind == abstractdomain.KindPossiblyNaN {
		innerA := a
		if a.Kind == abstractdomain.KindPossiblyNaN {
			innerA = *a.Inner
		}
		innerB := b
		if b.Kind == abstractdomain.KindPossiblyNaN {
			innerB = *b.Inner
		}
		nanArm := boolAt(op == CompareNe)
		realArm := CompareKnown(ctx, op, strict, innerA, innerB)
		if realArm.Kind == abstractdomain.KindUnknown {
			return realArm
		}
		return abstractdomain.JoinKnown(nanArm, realArm)
	}
	// KindUndef now means exactly-undefined and KindNull exactly-null (the
	// domain's AbsentMark split); a KindPossiblyUndefined wrapper is the
	// only value still admitting both flavors at once (PossiblyUndefined's
	// own comment: a wrapper with Inner.Kind == KindNull states "null or
	// undefined" and does not collapse). Flavored rows below therefore
	// gate on the EXACT kinds only — a wrapper or KindUnknown on either
	// side must fall through undecided rather than be read as one flavor.
	aExactAbsent := a.Kind == abstractdomain.KindUndef || a.Kind == abstractdomain.KindNull
	bExactAbsent := b.Kind == abstractdomain.KindUndef || b.Kind == abstractdomain.KindNull
	if aExactAbsent || bExactAbsent {
		if op != CompareEq && op != CompareNe {
			return silence.ResidueOf("an ordering comparison against null or undefined has no row — absence only compares under == or !=")
		}
		if aExactAbsent && bExactAbsent {
			// sec-isstrictlyequal step 1: SameType false -> false;
			// sec-islooselyequal steps 2-3: null/undefined cross-flavor
			// -> true; same-flavor case (SameType true) defers to
			// IsStrictlyEqual (islooselyequal step 1), which for a
			// non-Number SameType pair is SameValueNonNumber
			// (sec-samevaluenonnumber step 2: "If x is either undefined
			// or null, return true").
			sameFlavor := a.Kind == b.Kind
			if strict {
				return boolAt(sameFlavor == (op == CompareEq))
			}
			// loose: same-flavor -> true (via strict), cross-flavor ->
			// true (islooselyequal steps 2-3) — every combination of
			// exact null/undefined loose-equals every other
			return boolAt(op == CompareEq)
		}
		// exactly one side is an exact absent kind (Undef or Null); the
		// other side is some non-absent value
		other := a
		if aExactAbsent {
			other = b
		}
		if other.Kind == abstractdomain.KindValues || other.Kind == abstractdomain.KindObject ||
			other.Kind == abstractdomain.KindList || other.Kind == abstractdomain.KindArrayHoles ||
			other.Kind == abstractdomain.KindSet {
			// sec-isstrictlyequal step 1: SameType(absent, non-absent)
			// is false -> IsStrictlyEqual false, so === is false and
			// !== is true regardless of strict/loose — a non-absent
			// exact value is never null/undefined at runtime, and
			// IsLooselyEqual's own null/undefined steps (2-3) only
			// fire when the OTHER side is itself null or undefined, so
			// the loose reading agrees with the strict one here too
			//
			// A KindSet belongs in this list for exactly the reason
			// KindValues does: a refined set is a set of NUMBERS or
			// STRINGS (setSortOfForms answers only those two sorts, and
			// absence is never a form) — it carries no undefined and no
			// null member at all, so SameType against an absent side is
			// false on every run. Absence rides the PossiblyUndefined
			// WRAPPER, never the set inside it, and a wrapper still falls
			// through undecided by the exact-kind gate above. Without this
			// arm `y === undefined` on a plainly-typed `y: number` — the
			// most ordinary vacuous absence guard there is — decided
			// nothing.
			return boolAt(op == CompareNe)
		}
		return silence.ResidueOf("the non-absent side is not a plain value, set, object, list, or array — a wrapper or unresolved kind has no equality row against null or undefined here")
	}
	// a SET side against one exact number: the kernel's narrow claims
	// meet the set, and an EMPTY arm decides the comparison outright —
	// `x > 200` over x ∈ [0, 150] admits NO member (the true arm's
	// meet is empty, so the test is false on every run), and a set
	// excludes NaN by construction, so the verdict is exact in both
	// directions. Where the shape matches (a real numeric set against
	// one exact number) but NEITHER arm is impossible — `arr.length >
	// 0` over length ∈ [0, ∞) admits both outcomes — the comparison is
	// still a real relational operator over two known operands: it
	// answers true or false on every run, so the honest determination
	// is the boolean ground, never a decline (membership_ground_
	// models.go's own doctrine: "even an undecided test determines the
	// boolean ground — a value, never nothing").
	if verdict, matched := compareSetAgainstNumber(ctx, op, a, b, kernelRow); matched {
		if verdict != nil {
			return *verdict
		}
		return boolGround(operandTrustLevel)
	}
	// a SEQUENCE set (a regex-narrowed grammar, say) against one exact
	// string: ONE membership question — is the literal in the set? —
	// decides the false arm outright wherever it answers no (the
	// literal cannot equal any member of a set that excludes it), the
	// same disjointness compareSetAgainstNumber proves for a numeric
	// window. A yes answer does not pin the comparison (other members
	// exist too), so it falls through to the boolean ground exactly as
	// compareSetAgainstNumber's own undecided arm does.
	if verdict, matched := compareSetAgainstString(ctx, op, a, b, kernelRow); matched {
		if verdict != nil {
			return *verdict
		}
		return boolGround(operandTrustLevel)
	}
	// strict equality/disequality between two REFERENCE-kind operands
	// (arrays, Maps, Sets, Dates, Promises, plain objects) is an
	// IDENTITY question, not a value question — sec-isstrictlyequal's
	// SameType/SameValueNonNumber steps compare Object operands by
	// reference, never by shape. Two distinct bindings' identity is not
	// something this walk tracks (no alias graph proves or refutes it),
	// but the comparison is still a real === that returns true or false
	// on every run, so the honest determination is the boolean ground —
	// the same reasoning compareSetAgainstNumber's matched-but-
	// undecided arm already applies above. A single tracked place
	// compared with itself (`x === x`) is decided upstream, before
	// operands ever reach this function (evaluate_operators.go folds a
	// self-comparison before evaluating either side as a fresh
	// reference), so this row only ever sees two INDEPENDENT reference
	// values.
	if (op == CompareEq || op == CompareNe) && isReferenceKind(a) && isReferenceKind(b) {
		return boolGround(operandTrustLevel)
	}
	if a.Kind != abstractdomain.KindValues || b.Kind != abstractdomain.KindValues {
		// a same-sort STRING or BOOLEAN pair that isn't fully pinned to
		// one exact value each (an unbounded `string` parameter, a
		// boolean read off a call return) still compares as a real ===/
		// !== on every run — the specific truth value is unprovable
		// without the operands' exact content, but the comparison itself
		// is not: the boolean ground, not a decline. Number and boolean
		// claims are read as the SAME scalar ground here — KindOfClaim's
		// own doc: a sortless SET's forms "conflate number with
		// boolean (their words share the ground)" (true ↦ 1, the domain's
		// one root), so a KindValues boolean (KindOfClaim answers
		// ClaimSortBoolean exactly, since it reads the KindTag straight)
		// meets a KindSet scalar (ClaimSortNumber, the set reader's own
		// best-effort word) as the identical sort.
		//
		// ClaimSortString is the tuple layer's OWN conflation, not one
		// this row adds: a sequence-shaped KindSet reads ClaimSortString
		// whether it is really a string OR an unread array parameter
		// (setSortOfForms's own doc — "sequence forms say 'string':
		// strings and arrays share the tuple layer"). An array's === is
		// really the reference-identity question isReferenceKind answers
		// above; this row still gives the SAME boolGround answer for
		// that shape by coincidence of the one-root domain, never a
		// falsely-precise one, so the conflation costs no soundness —
		// only, harmlessly, this comment's own precision.
		if op == CompareEq || op == CompareNe {
			sortA, sortB := abstractdomain.KindOfClaim(a), abstractdomain.KindOfClaim(b)
			scalarGround := func(s abstractdomain.ClaimSort) bool {
				return s == abstractdomain.ClaimSortNumber || s == abstractdomain.ClaimSortBoolean
			}
			sameSort := sortA == sortB || (scalarGround(sortA) && scalarGround(sortB))
			if sortA != abstractdomain.ClaimSortNone && sameSort &&
				(sortA == abstractdomain.ClaimSortString || scalarGround(sortA)) {
				return boolGround(operandTrustLevel)
			}
		}
		return silence.ResidueOf("a side is not a plain known value — an object, wrapper, or unresolved kind has no comparison row here")
	}
	// strings: equality is tuple equality; ordering is code-unit
	// lexicographic (cmp.7)
	if a.KindTag == abstractdomain.PrimitiveString && b.KindTag == abstractdomain.PrimitiveString {
		if len(a.Values) == 0 || len(b.Values) == 0 {
			return compareKnownEmptyString(op, boolAt, len(a.Values), len(b.Values))
		}
		switch op {
		case CompareEq, CompareNe:
			target, ok := abstractdomain.SetOfKnown(b)
			if !ok {
				return silence.ResidueOf("the other string's exact codepoints don't form a set the kernel can be asked about")
			}
			equal, err := callMember(ctx.Kernel, target, a.Values)
			if err != nil {
				return silence.ResidueOf(kernelRefusedTheQuestionSaid)
			}
			if op == CompareEq {
				return kernelRow(equal)
			}
			return kernelRow(!equal)
		case CompareLt, CompareGt, CompareLe, CompareGe:
			return compareKnownStringOrder(ctx, op, kernelRow, a, b)
		}
		return silence.Residue()
	}
	// strict equality across sorts is decided by type alone: when the
	// sides wear different sorts, IsStrictlyEqual answers false before
	// any value is read (sec-isstrictlyequal, step 1: if SameType is
	// false, return false)
	if strict && a.KindTag != b.KindTag {
		if op == CompareEq {
			return boolAt(false)
		}
		if op == CompareNe {
			return boolAt(true)
		}
		return silence.ResidueOf("a strict ordering comparison across two different sorts has no row — SameType-false only decides equality, not order")
	}
	// arrays compare by REFERENCE — identity is not value knowledge
	if a.KindTag == abstractdomain.PrimitiveArray || b.KindTag == abstractdomain.PrimitiveArray {
		return silence.ResidueOf("arrays compare by reference, which is not value knowledge this reader holds")
	}
	if len(a.Values) != 1 || len(b.Values) != 1 {
		return silence.ResidueOf("a side is a known value that isn't pinned to one exact number or boolean — a set of values, not a single one")
	}
	x := a.Values[0]
	y := b.Values[0]
	if !isFinite(x) || !isFinite(y) {
		switch op {
		case CompareEq:
			return boolAt(x == y)
		case CompareNe:
			return boolAt(x != y)
		case CompareLt:
			return boolAt(x < y)
		case CompareGt:
			return boolAt(x > y)
		case CompareLe:
			return boolAt(x <= y)
		case CompareGe:
			return boolAt(x >= y)
		}
	}
	switch op {
	case CompareLt:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.Below(y)), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareGt:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.Above(y)), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareLe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.AtMost(y)), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareGe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.AtLeast(y)), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareEq:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{y})), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareNe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{y})), []float64{x})
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(!v)
	}
	return silence.Residue()
}

// isReferenceKind is whether a knowledge state denotes an Object at
// the spec level — the sorts SameType/IsStrictlyEqual compare by
// REFERENCE rather than by value (sec-isstrictlyequal, sec-
// samevaluenonnumber): an array, a Map/Set, a Date, a Promise, or a
// plain object. KindValues with KindTag == PrimitiveArray is a fully
// KNOWN array (a literal the walk built), which the pre-existing
// PrimitiveArray row just above already declines by name — this
// predicate is for the reference SORTS that never reach KindValues at
// all (an unread parameter, an opaque return).
func isReferenceKind(k abstractdomain.AbstractValue) bool {
	switch k.Kind {
	case abstractdomain.KindObject, abstractdomain.KindObjectStar, abstractdomain.KindList,
		abstractdomain.KindArrayHoles, abstractdomain.KindCollection, abstractdomain.KindPromise,
		abstractdomain.KindDate:
		return true
	}
	return false
}

func boolValue(v bool, grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	if v {
		return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
	}
	return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
}

// boolGround is the determined-but-either-way boolean: a real
// relational operator over two known operands returns true or false
// on every run (ToBoolean of a comparison never yields anything
// else), so where the specific truth value cannot be pinned the sound
// answer is BOTH truth values, not a decline — the same reading
// binary_comparison.go's ReadInstanceOf already gives its own
// undecided rows (boolPair) and unmodeled_method_havoc.go/
// membership_ground_models.go already give an unresolved but
// boolean-typed builtin result.
func boolGround(grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, grade)
}

// callMember mirrors the TS source's try/catch around
// ctx.kernel.member(...): Member has no ready-made recovering wrapper
// (kernelbridge's question methods panic on a refused question, per
// PORT.md), so this recovers the same way the TS catch does.
func callMember(kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet, tuple []float64) (member bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errKernelRefused
		}
	}()
	return kernel.Member(set, tuple), nil
}

// callSeqLexLt recovers a refused SeqLexLt ask the same way callMember
// recovers Member — the kernel panics on a refusal, never returns an
// error value, so every ask needs its own recovering wrapper.
func callSeqLexLt(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) (lt bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errKernelRefused
		}
	}()
	return kernel.SeqLexLt(a, b), nil
}

// compareKnownEmptyString decides a comparison where at least one side
// is the empty string, without asking the kernel: `""` is the least
// word under lexLtB (lexLtB [] l = (l ≠ [])), so ordering against an
// empty operand reads off length alone, and equality is length
// equality at this corner (both sides length 0).
func compareKnownEmptyString(op ComparisonOp, boolAt func(bool) abstractdomain.AbstractValue, aLen, bLen int) abstractdomain.AbstractValue {
	switch op {
	case CompareEq:
		return boolAt(aLen == 0 && bLen == 0)
	case CompareNe:
		return boolAt(!(aLen == 0 && bLen == 0))
	case CompareLt:
		return boolAt(aLen == 0 && bLen > 0)
	case CompareLe:
		return boolAt(aLen == 0)
	case CompareGt:
		return boolAt(bLen == 0 && aLen > 0)
	case CompareGe:
		return boolAt(bLen == 0)
	}
	return silence.Residue()
}

// compareKnownStringOrder decides `<`/`>`/`<=`/`>=` on two known
// nonempty strings — cmp.7, code-unit lexicographic order. Every row
// is phrased as ONE SeqLexLt ask: `<` and `>` ask it directly (in
// swapped operand order for `>`), and `<=`/`>=` are lt's negation the
// other way (a <= b iff not (b < a), the total order's trichotomy —
// lexLtB_asymm plus totality mean exactly one of a<b, a=b, b<a holds,
// so "not b<a" is exactly "a<=b").
func compareKnownStringOrder(ctx *FlowContext, op ComparisonOp, kernelRow func(bool) abstractdomain.AbstractValue, a, b abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	setA, okA := abstractdomain.SetOfKnown(a)
	setB, okB := abstractdomain.SetOfKnown(b)
	if !okA || !okB {
		return silence.ResidueOf("one string's exact codepoints don't form a set the kernel can be asked about")
	}
	switch op {
	case CompareLt:
		v, err := callSeqLexLt(ctx.Kernel, setA, setB)
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareGt:
		v, err := callSeqLexLt(ctx.Kernel, setB, setA)
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(v)
	case CompareLe:
		v, err := callSeqLexLt(ctx.Kernel, setB, setA)
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(!v)
	case CompareGe:
		v, err := callSeqLexLt(ctx.Kernel, setA, setB)
		if err != nil {
			return silence.ResidueOf(kernelRefusedTheQuestionSaid)
		}
		return kernelRow(!v)
	}
	return silence.Residue()
}
