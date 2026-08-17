// from assignability/plain_sort.ts
//
// Plain TypeScript positions: absence exclusion and sort-only
// refutation. A spelled plain type states one fact — whether it
// admits the absent value — and a sort demand only refutes when a
// value provably wears another sort. Everything unprovable stays
// silent: plain TypeScript is tsc's jurisdiction.

package walk

import (
	"encoding/json"
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// TypeAdmitsAbsence is typeAdmitsAbsence in the TS source: whether a
// host type admits the absent value anywhere. NULL counts: the
// walk's absent marker conflates undefined and null (the model has
// one absence), so a type admitting null may be exactly what the
// marker stands for — alerting there would claim what the walk
// cannot tell apart.
func TypeAdmitsAbsence(t *checker.Type) bool {
	if (t.Flags() & (checker.TypeFlagsAny | checker.TypeFlagsUnknown |
		checker.TypeFlagsUndefined | checker.TypeFlagsVoid |
		checker.TypeFlagsNull)) != 0 {
		return true
	}
	if t.IsUnion() {
		for _, member := range t.Types() {
			if TypeAdmitsAbsence(member) {
				return true
			}
		}
		return false
	}
	return false
}

// stringGroundFormJSON is the STRING_GROUND_FORM constant in the TS
// source: strings.forms[0] (refinementsets.Strings — Star(Codepoints),
// codepoint_sets.ts's `strings`), stringified for the shape
// comparison addsNothingSet makes.
var stringGroundFormJSON = mustJSON(refinementsets.Strings.Forms[0])

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// AddsNothingSet is addsNothingSet in the TS source: whether a set
// only restates a sort's GROUND — no forms, the whole scalar line,
// the star of every string, or a star of either (an array of ground
// elements). Such a statement narrows nothing the host type does not
// already say.
func AddsNothingSet(set refinementsets.RefinedSet) bool {
	for _, form := range set.Forms {
		// the whole-line rays admit every element of ℝ̄ and say nothing:
		// atLeast(-∞) and atMost(+∞). The STRICT rays are not among them —
		// above(-∞) and below(+∞) each EXCLUDE their own infinite
		// endpoint, which ℝ̄ admits as an element, so they narrow.
		if form.Form == refinementsets.FormAtLeast && math.IsInf(form.A, -1) {
			continue
		}
		if form.Form == refinementsets.FormAtMost && math.IsInf(form.A, 1) {
			continue
		}
		// a form carrying a non-finite number anywhere else is never the
		// (finite) string ground, and encoding/json panics on ±Inf/NaN
		// (unlike JS's JSON.stringify, which prints `null`; PORT.md's
		// convention) — so it answers WITHOUT the round-trip
		if formCarriesNonFinite(form) {
			if form.Form == refinementsets.FormStar && form.A_ != nil && AddsNothingSet(*form.A_) {
				continue
			}
			return false
		}
		if mustJSON(form) == stringGroundFormJSON {
			continue
		}
		if form.Form == refinementsets.FormStar && AddsNothingSet(*form.A_) {
			continue
		}
		return false
	}
	return true
}

// formCarriesNonFinite reports whether the form holds ±Inf or NaN in
// any numeric field, at any nesting depth — the shapes encoding/json
// refuses.
func formCarriesNonFinite(form refinementsets.Refinement) bool {
	if math.IsInf(form.A, 0) || math.IsNaN(form.A) {
		return true
	}
	for _, w := range form.W {
		if math.IsInf(w, 0) || math.IsNaN(w) {
			return true
		}
	}
	for _, nested := range []*refinementsets.RefinedSet{form.A_, form.B} {
		if nested == nil {
			continue
		}
		for _, inner := range nested.Forms {
			if formCarriesNonFinite(inner) {
				return true
			}
		}
	}
	return false
}

// StatedSetWords is statedSetWords in the TS source: the stated set
// spelled for a message — the surface's own word when it covers
// every form (an enum's `0 | 1 | 2`, a z.enum's names) — the same
// voice hovers use — and the algebra otherwise. A scalar enum's
// union of singletons would read as codepoint strings through the
// algebra; the word says what the author wrote.
//
// The TS source takes `{ readonly set; readonly word?: {...} }` — an
// inline shape matching DeclaredRefinement's own `set`/`word`
// fields; every call site here passes those two fields directly.
func StatedSetWords(setValue refinementsets.RefinedSet, word *annotations.WordSpelling) string {
	if word != nil && word.Covers == len(setValue.Forms) {
		return word.Text
	}
	return refinementsets.FormatForDiagnostics(setValue)
}

// AlertPlainAbsence is alertPlainAbsence in the TS source: the one
// set fact a PLAIN spelled type states — whether it admits the
// absent value. A value the walk PROVED may be undefined (an index
// past the guaranteed length, an unguarded optional), written where
// a spelled plain type admits no undefined, breaks the stated claim
// — the same alert discipline stated refinements carry, restricted
// to the absence axis. Only a SPELLED annotation is a claim; an
// inferred type states nothing and alerts nowhere.
func AlertPlainAbsence(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	typeNode *ast.Node,
	atNode *ast.Node,
	what string,
) {
	// the PROVENANCE gate: only a positively derived absence fires —
	// a maybe born of a join or a partial summary is ignorance, and
	// ignorance against a plain type is not a finding
	if known.Kind != abstractdomain.KindPossiblyUndefined || !known.ProvedAbsent {
		return
	}
	t := ctx.P.Checker.GetTypeAtLocation(typeNode)
	if TypeAdmitsAbsence(t) {
		return
	}
	ctx.Report(assignability.At(
		atNode,
		7001,
		what+" reads an index the checker cannot prove in bounds — "+
			"out of bounds the read is undefined, and the stated type "+
			"'"+sourceTextOf(typeNode)+"' does not admit it: guard the index, "+
			"or state the type with `| undefined`",
	))
}

// sourceTextOf mirrors TS's node.getText(): the node's own source
// substring (start SKIPS leading trivia, per PORT.md's getStart()
// note).
func sourceTextOf(node *ast.Node) string {
	sourceFile := ast.GetSourceFileOfNode(node)
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	return sourceFile.Text()[start:node.End()]
}

// RefutePlainSort is refutePlainSort in the TS source: a PLAIN-typed
// position judges SORT only, and only to refute: a value that
// provably wears another sort — a symbol reaching a `string` domain
// past a cast — is wrong on every admitted run, so it earns the 7001
// tsc's cast silenced. Everything unprovable stays silent: plain
// TypeScript is tsc's jurisdiction, and this check never alerts.
func RefutePlainSort(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	typeNode *ast.Node,
	node *ast.Node,
	what string,
) {
	if typeNode == nil {
		return
	}
	demanded := plainKeywordDemand(ctx, typeNode, 0)
	if demanded == "" {
		return
	}
	// the UNIVERSAL reading: this sentence states what the value wears
	// on every admitted run, so a union offending on only some arms
	// must answer nothing here (SortMaybeOutsidePlain holds that
	// reading for a channel whose wording carries a maybe)
	offending, ok := SortOutsidePlain(known, demanded)
	if !ok {
		return
	}
	ctx.Report(assignability.At(
		node,
		7001,
		what+" is a "+offending+", which is not assignable to "+
			"type '"+demanded+"'",
	))
}

// plainKeywordDemand is the plain primitive keyword a spelled type
// node demands — "string", "number", "boolean", or "" for every other
// spelling. A NAME resolves through the checker to the type alias it
// stands for and reads the alias's own target: `type Name = string`
// demands exactly what the bare keyword demands, and the position
// spelling the name is the same position. Only a target that IS one of
// the three keywords qualifies; a union, an object, a generic alias,
// or a chain past aliasHopLimit hops keeps the current silence.
func plainKeywordDemand(ctx *FlowContext, typeNode *ast.Node, hops int) string {
	switch typeNode.Kind {
	case ast.KindStringKeyword:
		return "string"
	case ast.KindNumberKeyword:
		return "number"
	case ast.KindBooleanKeyword:
		return "boolean"
	case ast.KindParenthesizedType:
		return plainKeywordDemand(ctx, typeNode.AsParenthesizedTypeNode().Type, hops)
	}
	if hops >= aliasHopLimit || !ast.IsTypeReferenceNode(typeNode) {
		return ""
	}
	typeRef := typeNode.AsTypeReferenceNode()
	// a name carrying type arguments stands for an instantiation, not
	// for one plain keyword
	if typeRef.TypeArguments != nil || !ast.IsIdentifier(typeRef.TypeName) {
		return ""
	}
	symbol := symbolAt(ctx.P.Checker, typeRef.TypeName)
	if symbol == nil {
		return ""
	}
	for _, d := range symbol.Declarations {
		if !ast.IsTypeAliasDeclaration(d) {
			continue
		}
		aliasDecl := d.AsTypeAliasDeclaration()
		// a GENERIC alias's target reads under its parameters, which this
		// position does not carry
		if aliasDecl.TypeParameters != nil && len(aliasDecl.TypeParameters.Nodes) > 0 {
			return ""
		}
		return plainKeywordDemand(ctx, aliasDecl.Type, hops+1)
	}
	return ""
}

// aliasHopLimit bounds the alias chain plainKeywordDemand follows. A
// self-referential alias is not a spelling tsc admits, but the walk
// reads error-recovered trees too, and a bounded walk answers the same
// "" a cyclic chain would.
const aliasHopLimit = 8

// SortOutsidePlain is sortOutsidePlain in the TS source: the sort a
// claim PROVABLY wears that a plain demand excludes, or ("", false).
// Only the unambiguous carriers answer — the symbol and bigint kinds,
// a sort-tagged set, an exact word's kindTag, and every kind whose
// sort is decided by CONSTRUCTION (an object, a list, a collection, a
// promise, a date, a regex, a host function are each built as what
// they are and wear that sort on every run). Wrappers read through:
// NaN is number-sorted and absence is tsc's own strict check, so
// neither arm speaks here. The genuinely ambiguous kinds — unknown, a
// sortless set whose forms speak for two sorts, a refinement variable
// — stay silent.
//
// A kind UNION answers only when EVERY arm offends the demand: the
// value is one of the arms on each run, so "every arm is outside" is
// the only reading under which the value provably wears an offending
// sort. One arm of several offending means the value MAY be outside
// on some run and inside on another, which refutes nothing — that
// reading lives in SortMaybeOutsidePlain below, for a caller whose
// wording can carry a maybe.
func SortOutsidePlain(known abstractdomain.AbstractValue, demanded string) (string, bool) {
	switch known.Kind {
	case abstractdomain.KindSymbol:
		return "symbol", true
	case abstractdomain.KindBigints:
		return "bigint", true
	case abstractdomain.KindSet:
		if known.SetKindTag == abstractdomain.SetKindTagNone {
			return "", false
		}
		return string(known.SetKindTag), true
	case abstractdomain.KindValues:
		tag := string(known.KindTag)
		if tag != demanded && (tag == "string" || tag == "number" || tag == "boolean" || tag == "array") {
			return tag, true
		}
		return "", false
	case abstractdomain.KindKindUnion:
		// EVERY arm must offend, and an armless union states nothing —
		// the empty product would otherwise answer true vacuously.
		if len(known.Arms) == 0 {
			return "", false
		}
		firstOutside := ""
		for _, arm := range known.Arms {
			outside, ok := SortOutsidePlain(arm, demanded)
			if !ok {
				// this arm is admitted, or the walk cannot tell — either
				// way the value may be inside the demand on some run
				return "", false
			}
			if firstOutside == "" {
				firstOutside = outside
			}
		}
		return firstOutside, true
	case abstractdomain.KindPossiblyNaN, abstractdomain.KindPossiblyUndefined:
		return SortOutsidePlain(*known.Inner, demanded)
	case abstractdomain.KindObject, abstractdomain.KindObjectStar,
		abstractdomain.KindList,
		abstractdomain.KindCollection, abstractdomain.KindPromise,
		abstractdomain.KindDate, abstractdomain.KindRegex,
		abstractdomain.KindHostFunction:
		// the constructed carriers: each is built as its own kind, so the
		// word it answers is the same on every run. `typeof` speaks that
		// word (the "object"/"function" the domain's own reader gives),
		// and none of the three plain primitive demands admits it.
		word := abstractdomain.TypeofWordOfKnown(known)
		if word == "" || word == demanded {
			return "", false
		}
		return word, true
	case abstractdomain.KindNaN:
		// NaN is number-sorted — it refutes a string or boolean demand
		// the way any exact number does
		if demanded == "number" {
			return "", false
		}
		return "number", true
	default:
		return "", false
	}
}

// SortMaybeOutsidePlain is the EXISTENTIAL reading of the same
// question: an offending sort SOME run may hand the demand, or ("",
// false). It differs from SortOutsidePlain on one kind — a union
// answers on ANY offending arm, because one arm being outside is
// exactly what "may be" states. Every other kind carries one sort on
// every run, so the two readings agree there and this function hands
// them straight to SortOutsidePlain.
//
// Nothing calls this today: RefutePlainSort is SortOutsidePlain's only
// consumer, and its sentence promises a claim the value wears on every
// run, so it takes the universal reading. This function holds the
// existential reading for a channel whose wording carries a maybe —
// a hover line or an alert that says "may be" and means it.
func SortMaybeOutsidePlain(known abstractdomain.AbstractValue, demanded string) (string, bool) {
	switch known.Kind {
	case abstractdomain.KindKindUnion:
		for _, arm := range known.Arms {
			if outside, ok := SortMaybeOutsidePlain(arm, demanded); ok {
				return outside, true
			}
		}
		return "", false
	case abstractdomain.KindPossiblyNaN, abstractdomain.KindPossiblyUndefined:
		return SortMaybeOutsidePlain(*known.Inner, demanded)
	default:
		return SortOutsidePlain(known, demanded)
	}
}
