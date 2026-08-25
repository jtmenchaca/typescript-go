// The cases → AbstractValue lowering for a foreign edge's return and
// intermediate captured-stdout binding: the JSON number grammar the
// captured stdout text itself holds, and the walk's own vocabulary a
// RULED cases list lowers into.

package walk

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the intermediate captured-stdout binding ────────────────────── */

// jsonNumberGrammarPattern is the JSON number production (RFC 8259 §6 /
// json.org's number diagram, the same grammar sec-json.parse's
// JSONNumber cites): an optional sign, an integer part that is either
// the single digit 0 or a nonzero digit followed by any run of digits
// (no leading zero — json.dumps never writes one), an optional
// fractional part, an optional exponent. Anchored ^...$ by
// FormatGrammar's own convention (AGENT-BRIEF.md's kernel-bridge-facts
// row) and followed by the ONE trailing newline execFileSync's
// captured stdout always carries (the harness's own print/stdout.write
// terminates its line) — the harness never writes a SECOND line for a
// scalar return, so exactly one \n, not a star of them.
const jsonNumberGrammarPattern = `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?\n`

// jsonNumberGrammarSet compiles jsonNumberGrammarPattern through the
// SAME FormatGrammar door z.string().regex compiles through
// (chain_method.go's ".regex" case) — the pattern vocabulary this
// binding reuses rather than hand-building the concatenation/union
// forms a regex source already denotes. A compile failure here is an
// impossible state (the pattern is fixed and already exercised by this
// file's own vocabulary: \d, character classes, ?, alternation, and
// the {lo,hi} quantifier all read as supported forms in
// regex_compiler_test.go) — panics rather than silently widening to
// Strings, so a future change to the supported regex subset that
// actually broke this pattern would fail loudly at the first call
// instead of quietly degrading every stdout binding to residue.
func jsonNumberGrammarSet() refinementsets.RefinedSet {
	// anchored ^...$ ourselves (AGENT-BRIEF.md's kernel-bridge-facts
	// row: "anchor sub-patterns yourself; FormatGrammar alone pads
	// substring-anywhere") — an unanchored compile would pad both sides
	// with C*, admitting text BEFORE or AFTER the number/newline that
	// json.dumps never writes, which would unsoundly widen the set.
	compiled := refinementsets.FormatGrammar("^"+jsonNumberGrammarPattern+"$", "")
	if !compiled.Ok {
		panic("jsonNumberGrammarPattern does not compile: " + compiled.Unsupported)
	}
	return compiled.Set
}

// foreignStdoutSerializedValue answers the SERIALIZED form of a
// discharged crossing's return cases — the string-sorted set the
// captured-stdout binding (`const stdout = execFileSync(...)`, or
// spawnSync's `<name>.stdout`) actually holds, read structurally off
// what the harness's own JSON encoder can spell for that return. The
// checker already knows the crossed value's full semantic type (the
// cases list itself), so it already knows the JSON serialization
// grammar of that type — this derives COMPOSITIONALLY over every case
// kind the RULED schema carries (number, string, boolean, null,
// object — walk/foreign_edge_artifact.go's casesOf), not number cases
// alone.
//
// A return whose every PRESENT case is number-sorted keeps its
// existing EXACT behavior: the cases' own Set HULL (their union) is
// tightened as one window (refinementsets.TightenedJSONNumberGrammar,
// json_number_grammar.go), never tightened per-case-then-unioned —
// tightening the combined hull can find a single sharper window a
// per-case tightening followed by union would miss (e.g. two adjacent
// number cases whose UNION is a plain [0, 1] window but whose
// INDIVIDUAL windows each fall to a wider case), so the existing
// hull-first reading is preserved exactly rather than folded into the
// general per-case composition below.
//
// Every other shape (a string, boolean, null, or object case present
// anywhere, alone or beside a number case) composes through
// refinementsets.JSONValueGrammar, arm by arm — see json_case_grammar.
// go's own per-kind soundness citations. ok=false only where the
// composition could not derive ANY arm at all (an empty cases list,
// or every case kind unrecognized) — the caller then leaves the
// intermediate stdout binding unbound rather than guess.
func foreignStdoutSerializedValue(cases []Case) (abstractdomain.AbstractValue, bool) {
	if len(cases) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	if foreignCasesAreAllNumber(cases) {
		grammar := jsonNumberGrammarSet()
		if tightened, ok := refinementsets.TightenedJSONNumberGrammar(foreignNumberCasesHull(cases)); ok {
			grammar = tightened
		}
		return abstractdomain.KnownSet(grammar, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone), true
	}
	jsonCases := foreignJSONCasesOf(cases)
	grammar, ok := refinementsets.JSONValueGrammar(jsonCases)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	known := abstractdomain.KnownSet(grammar, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	// an OBJECT case anywhere composes into a brace/colon/comma pattern
	// chain FormatForHover's own recognizers (format_for_hover.go's
	// starts/ends/includes trio) do not read back apart — the compiled
	// text would show as an unreadable raw pattern. Every other
	// composed shape (string/boolean/null alone or unioned) already
	// renders through FormatForHover's existing vocabulary (an exact
	// literal, a quoted-string window, a two-word alternation), so the
	// override applies only where an object case is actually present.
	if foreignCasesContainObject(cases) {
		if hoverWord, hoverOk := refinementsets.JSONValueHover(jsonCases); hoverOk {
			known = abstractdomain.KnownSetWithHoverWord(known, hoverWord)
		}
	}
	return known, true
}

// foreignCasesContainObject is whether cases carries an object case at
// THIS level. Checking only the top level is enough for the WHOLE
// tree: Members only exists on a case whose own Sort is
// CaseSortObject, so a nested object (a member's own cases list
// carrying a further object case) can only be reached by first passing
// through an object case at every level above it — a cases list with
// an object case buried inside a member necessarily has an object case
// at ITS OWN top level too, by the same construction.
func foreignCasesContainObject(cases []Case) bool {
	for _, c := range cases {
		if c.Sort == CaseSortObject {
			return true
		}
	}
	return false
}

// foreignCasesAreAllNumber is whether every present case is
// number-sorted — the gate foreignStdoutSerializedValue keeps ahead of
// the general composition so the pure-number shape stays on its
// existing hull-first tightening rather than the general per-case
// path (see that function's own doc for why the two are not the same
// derivation).
func foreignCasesAreAllNumber(cases []Case) bool {
	for _, c := range cases {
		if c.Sort != CaseSortNumber {
			return false
		}
	}
	return true
}

// foreignJSONCasesOf converts a RULED cases list into
// refinementsets.JSONCase — the package-neutral mirror JSONValueGrammar
// reads (refinementsets cannot import walk's own Case/CaseSort; see
// json_case_grammar.go's file banner), recursing into an object case's
// own Members so a nested object's member lowers through the identical
// conversion.
func foreignJSONCasesOf(cases []Case) []refinementsets.JSONCase {
	out := make([]refinementsets.JSONCase, len(cases))
	for i, c := range cases {
		out[i] = refinementsets.JSONCase{
			Kind:    refinementsets.JSONCaseKind(c.Sort),
			Set:     c.Set,
			Members: foreignJSONMembersOf(c.Members),
			Closed:  c.Closed,
		}
	}
	return out
}

// foreignJSONMembersOf converts an object case's Members map — each
// member's own []Case union through foreignJSONCasesOf, recursively.
func foreignJSONMembersOf(members map[string][]Case) map[string][]refinementsets.JSONCase {
	if len(members) == 0 {
		return nil
	}
	out := make(map[string][]refinementsets.JSONCase, len(members))
	for name, memberCases := range members {
		out[name] = foreignJSONCasesOf(memberCases)
	}
	return out
}

// foreignNumberCasesHull is the union of every number case's own Set —
// the syntactic hull TightenedJSONNumberGrammar reads its window off.
// A single-case return (the ordinary shape) hands its Set straight
// through unchanged; more than one number case (a union of numeric
// ranges) widens to their union first, exactly as any other hull over
// several sets in this tree is built.
func foreignNumberCasesHull(cases []Case) refinementsets.RefinedSet {
	hull := cases[0].Set
	for _, c := range cases[1:] {
		hull = refinementsets.MakeRefinedSet(refinementsets.Union(hull, c.Set))
	}
	return hull
}

// foreignAbstractValueOfCases lowers a RULED cases list into one
// AbstractValue at TrustSpec — the walk's OWN vocabulary for "a value
// that may be one of several sorts": a single present case (number,
// string, boolean, or object) reads directly; a present case ALONGSIDE
// a null case wraps through abstractdomain.PossiblyUndefined (the
// wrapper every possibly-absent value in this checker already wears);
// more than one PRESENT case — a genuine union of sorts, no null case
// involved, INCLUDING a Result-style return whose two present cases
// are both object-sorted (two distinct member structures joined) —
// folds through abstractdomain.KindUnionOf, the same sort-
// distinguished-arms union DerivedReturnOf's own join uses and the
// same channel multiple SCALAR cases already fold through, reused
// here rather than a new union kind for objects specifically. An
// empty cases list (declined upstream by casesOf, so unreached in
// practice) answers Unknown, keeping this function total.
func foreignAbstractValueOfCases(cases []Case) abstractdomain.AbstractValue {
	if len(cases) == 0 {
		return abstractdomain.Unknown
	}
	hasNull := false
	present := make([]Case, 0, len(cases))
	for _, c := range cases {
		if c.Sort == CaseSortNull {
			hasNull = true
			continue
		}
		present = append(present, c)
	}
	var value abstractdomain.AbstractValue
	switch {
	case len(present) == 0:
		// null alone: the absent value, with no inner claim to wrap
		return abstractdomain.Undef
	case len(present) == 1:
		value = foreignAbstractValueOfCase(present[0])
	default:
		arms := make([]abstractdomain.AbstractValue, 0, len(present))
		for _, c := range present {
			arms = append(arms, foreignAbstractValueOfCase(c))
		}
		value = abstractdomain.KindUnionOf(arms)
	}
	if hasNull && len(present) > 0 {
		return abstractdomain.PossiblyUndefined(value, abstractdomain.TrustSpec, true, false)
	}
	return value
}

// foreignAbstractValueOfCase lowers one present (non-null) Case to its
// own AbstractValue at TrustSpec: number/string cases wear their set
// through KnownSet exactly as the pre-cases reading did; boolean wears
// the whole {0,1} boolean-tagged domain KnownValues already carries
// for a declared boolean elsewhere in this checker; object lowers
// through foreignObjectValueOf into the walk's own object vocabulary
// (abstractdomain.KnownObject) — reused, never invented, so the
// consumer-side judge (CheckObjectTarget/CheckObjectKnown,
// object_assignability.go) works through the existing object-
// assignability laws unchanged.
func foreignAbstractValueOfCase(c Case) abstractdomain.AbstractValue {
	if c.Sort == CaseSortBoolean {
		return abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
	}
	if c.Sort == CaseSortObject {
		return foreignObjectValueOf(c)
	}
	return abstractdomain.KnownSet(c.Set, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

// foreignObjectValueOf lowers one object Case into
// abstractdomain.KnownObject: each member's own cases list lowers
// through foreignAbstractValueOfCases (the SAME cases-list lowering a
// top-level entry/return takes, recursed — a member's cases may
// themselves carry object cases, exactly as casesOf reads them),
// ordered by the member map's own sorted keys so the built AbstractValue
// is deterministic across runs (a Go map has no stable iteration order;
// ObjectKey.Name preserves the sort here rather than the producer's own
// insertion order, which the wire's JSON object already lost). Complete
// carries Closed unchanged — the producer's own completeness claim,
// never re-derived. Stated is nil (no annotations.ObjectAnnotation backs
// a foreign-crossed value) and bareProto is false (a JSON-parsed object
// wears the ordinary Object.prototype, the same assumption
// KnownObject's every other caller in this checker makes for a plain
// object literal).
func foreignObjectValueOf(c Case) abstractdomain.AbstractValue {
	names := make([]string, 0, len(c.Members))
	for name := range c.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	keys := make([]abstractdomain.ObjectKey, 0, len(names))
	for _, name := range names {
		keys = append(keys, abstractdomain.ObjectKey{
			Name:  name,
			Value: foreignAbstractValueOfCases(c.Members[name]),
		})
	}
	return abstractdomain.KnownObject(keys, nil, c.Closed, abstractdomain.TrustSpec, false)
}
