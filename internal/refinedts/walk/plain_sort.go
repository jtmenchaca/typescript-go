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
		// LAZY, matching the TS `||`: an atLeast(-Infinity) form never
		// reaches mustJSON below — encoding/json panics on ±Inf/NaN
		// (unlike JS's JSON.stringify, which prints `null`; PORT.md's
		// convention), so evaluating every branch eagerly (as this
		// file did before) crashed on exactly the form this first
		// branch exists to short-circuit past.
		if form.Form == refinementsets.FormAtLeast && math.IsInf(form.A, -1) {
			continue
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
	var demanded string
	switch typeNode.Kind {
	case ast.KindStringKeyword:
		demanded = "string"
	case ast.KindNumberKeyword:
		demanded = "number"
	case ast.KindBooleanKeyword:
		demanded = "boolean"
	default:
		return
	}
	offending, ok := SortOutsidePlain(known, demanded)
	if !ok {
		return
	}
	ctx.Report(assignability.At(
		node,
		7001,
		what+" may be a "+offending+", which is not assignable to "+
			"type '"+demanded+"'",
	))
}

// SortOutsidePlain is sortOutsidePlain in the TS source: the sort a
// claim PROVABLY wears that a plain demand excludes, or ("", false).
// Conservative: only the unambiguous carriers answer — the symbol
// and bigint kinds, a sort-tagged set, an exact word's kindTag — and
// a union answers on any offending arm. Wrappers read through: NaN
// is number-sorted and absence is tsc's own strict check, so neither
// arm speaks here.
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
		for _, arm := range known.Arms {
			if outside, ok := SortOutsidePlain(arm, demanded); ok {
				return outside, true
			}
		}
		return "", false
	case abstractdomain.KindPossiblyNaN, abstractdomain.KindPossiblyUndefined:
		return SortOutsidePlain(*known.Inner, demanded)
	default:
		return "", false
	}
}
