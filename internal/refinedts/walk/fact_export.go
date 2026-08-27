// The TypeScript half of the fact-export surface: one function's
// CHECKED facts, read call-site-free, in the shape a Python consumer
// reads across the FFI edge (docs/one-checker/reverse-pair.md, Half
// A — the mirror of fact_export.rs's export_function).
//
// What crosses is what the checker DERIVED, never what an annotation
// claimed for the RETURN position: DerivedReturnOf runs the same
// walk InlineContractBody runs (parameters bound from their
// declarations, the body walked statement by statement) and answers
// the join of every value the body's returns produced. The entry
// rows are the declared refinements the walk itself would bind from
// (AbstractValueOfDeclared, declared_value.go:23), so a consumer
// reading the entry and the return reads exactly the two ends of one
// derivation.
//
// EVERY FIELD IS COMPUTED. A parameter carrying no declared
// refinement, a rest parameter, a function with more than one
// parameter, or a derived return with no faithful set reading is
// OMITTED with the reason named — never a stub, a placeholder, or a
// widened stand-in for a fact this checker did not derive.
//
// Nothing here reaches the kernel's wire encoding or the artifact
// envelope — service/export_fact.go (a sibling unit) is what turns
// these answers into the JSON the Python consumer reads.

package walk

import (
	"sort"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// CaseSort is the one-word sort a Case names — the RULED cases schema's
// own tag (no version field anywhere in the envelope; this tag is what
// tells a number case from a string case, since the wire set itself
// carries no sort of its own: EncodeSet's {"forms":[...]} is the same
// shape whether the forms denote numbers or a codepoint sequence).
type CaseSort string

const (
	CaseSortNumber  CaseSort = "number"
	CaseSortString  CaseSort = "string"
	CaseSortBoolean CaseSort = "boolean"
	CaseSortNull    CaseSort = "null"
	CaseSortObject  CaseSort = "object"
)

// Case is one member of a "cases" list — the RULED schema's own union
// arm: a number or string case carries the full kernel wire set
// (Set), reused verbatim from the existing kernelbridge codec; a
// boolean or null case carries no set at all (the whole-sort floor
// for boolean; the absent value for null — both match what actually
// crosses the wire, since JSON.stringify's own bare tokens are what
// crosses); an object case carries Members (a key's own cases list,
// recursively — a nested object member is itself a Case whose Sort is
// "object") and Closed (true when the producer states the exact key
// set the value holds — abstractdomain.AbstractValue.Complete's own
// fact, carried across unchanged). The RULED schema's object case:
//
//	{"sort": "object", "members": {"<key>": [Case, ...], ...}, "closed": bool}
//
// A Result-style union (two shapes, e.g. {ok,value} | {ok,error}) is
// two object Cases in the SAME cases list — one list already carries
// a union of sorts (a possibly-null return already spells this way),
// so an object union needs no new list shape, only two object-sorted
// members in it.
type Case struct {
	Sort    CaseSort
	Set     refinementsets.RefinedSet
	Members map[string][]Case
	Closed  bool
}

// ForeignEntryRow is one exported parameter: its name, and the shape
// its declaration states — a SEQUENCE (the window's element cases plus
// the length floor the declaration's own window carries) or a SCALAR
// (a cases list). Mirrors fact_export.rs's EntryRow/EntryShape as one
// flat struct — the Go side has no sum type, so IsSequence tags which
// fields the row carries.
type ForeignEntryRow struct {
	Name          string
	IsSequence    bool
	ElementCases  []Case
	LengthAtLeast int
	Cases         []Case
}

// ExportFunctionFact reads contract's entry rows and derives its
// return cases, or answers one omission sentence naming the construct
// that stopped it. Never both: an omission answers a zero entry slice
// and a nil cases list alongside it.
//
// The consumer this artifact will feed already declines any function
// with more than one entry (foreign_edge_artifact.go's ForeignEntry
// is read one row at a time, and the stdin harness hands one JSON
// value to one parameter) — the exporter refuses symmetrically here,
// rather than exporting rows a caller can never fill.
func ExportFunctionFact(ctx *FlowContext, contract *FunctionContract) (entry []ForeignEntryRow, returnCases []Case, omission string) {
	parameters := contract.Declaration.Parameters()
	if len(parameters) > 1 {
		return nil, nil, "the function declares more than one parameter, and the stdin harness hands one JSON value to one parameter"
	}
	rows, entrySentence := foreignEntryRowsOf(parameters, contract.Params)
	if entrySentence != "" {
		return nil, nil, entrySentence
	}
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		return nil, nil, "the body's returns derived no value this walk could read"
	}
	cases, returnSentence := FaithfulReturnCases(derived)
	if returnSentence != "" {
		return nil, nil, returnSentence
	}
	return rows, cases, ""
}

// foreignEntryRowsOf reads every parameter into an entry row, or
// answers the reason the whole function cannot be exported — checked
// eagerly per position so the sentence names the first parameter
// that blocks it, not a summary of the whole signature. declared is
// contract.Params, positionally aligned with parameters — the same
// read CompileContractFileFacts already produced when the contract
// was compiled (contract_file_facts.go), never re-derived here.
func foreignEntryRowsOf(parameters []*ast.Node, declared []*annotations.DeclaredRefinement) ([]ForeignEntryRow, string) {
	rows := make([]ForeignEntryRow, 0, len(parameters))
	for i, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		name := parameterDisplayName(parameter)
		if pd.DotDotDotToken != nil {
			return nil, "parameter '" + name + "' is a rest parameter, which states no fixed entry shape"
		}
		var stated *annotations.DeclaredRefinement
		if i < len(declared) {
			stated = declared[i]
		}
		row, sentence := foreignEntryRowOf(stated, name)
		if sentence != "" {
			return nil, sentence
		}
		rows = append(rows, row)
	}
	return rows, ""
}

// parameterDisplayName is the parameter's own bound name for a
// sentence, or "?" for a destructured pattern that binds no single
// name.
func parameterDisplayName(parameter *ast.Node) string {
	name := parameter.AsParameterDeclaration().Name()
	if name != nil && ast.IsIdentifier(name) {
		return name.Text()
	}
	return "?"
}

// foreignEntryRowOf reads one parameter's already-compiled declared
// refinement (contract.Params[i]) into its entry row: a DeclaredSet
// splits sequence-vs-scalar through AsRepetition (the declared set
// already carries Star/Repeat, so no spelling match is needed —
// unlike the Rust side's `spelling.starts_with` check); a
// DeclaredPossiblyUndefined (X | null / X | undefined, already
// collapsed to one wrapper by the union type-node reader) reads the
// INNER statement's own cases plus the null case appended — the RULED
// schema's vocabulary now carries a possibly-absent parameter, where
// foreignEntryRowOf's own refusal used to be the only reading; every
// other declared kind states a shape no cases list can carry, named
// plainly.
//
// A scalar DeclaredSet whose KindTag reads "boolean" (chain_root_
// constructor.go's z.boolean() root, now tagged the same way bigint/
// symbol already are) emits {"sort":"boolean"} directly, ahead of
// caseOfSet's own number/string split — caseOfSet itself still sees
// only a bare RefinedSet, and a boolean parameter's set is the same
// {0,1} OneOf shape a two-element numeric set would compile to, so
// the tag has to be read HERE, before the set reaches caseOfSet, or
// it is unrecoverable (fact_export_test.go's own
// TestDerivedReturnOf_ABooleanLiteralReturnEmitsTheWholeSortFloorCase
// pins the identical read on the derived-RETURN side, off the
// checker's own AbstractValue instead of the declared side).
func foreignEntryRowOf(declared *annotations.DeclaredRefinement, name string) (ForeignEntryRow, string) {
	if declared == nil {
		return ForeignEntryRow{}, "parameter '" + name + "' carries no refinement this checker reads"
	}
	switch declared.Kind {
	case annotations.DeclaredSet:
		if declared.KindTag == "boolean" {
			return ForeignEntryRow{Name: name, Cases: []Case{{Sort: CaseSortBoolean}}}, ""
		}
		if repeated, ok := refinementsets.AsRepetition(*declared.Set); ok {
			return ForeignEntryRow{
				Name:          name,
				IsSequence:    true,
				ElementCases:  []Case{caseOfSet(repeated.Element)},
				LengthAtLeast: repeated.Lo,
			}, ""
		}
		return ForeignEntryRow{Name: name, Cases: []Case{caseOfSet(*declared.Set)}}, ""
	case annotations.DeclaredObject:
		return ForeignEntryRow{}, "parameter '" + name + "' states an object, which crosses no single set"
	case annotations.DeclaredObjectArray:
		return ForeignEntryRow{}, "parameter '" + name + "' states an array of records, which crosses no single set"
	case annotations.DeclaredVariable:
		return ForeignEntryRow{}, "parameter '" + name + "' states a refinement variable, which crosses no single set"
	case annotations.DeclaredTuple:
		// the row carries ONE element case for a sequence, and a tuple's
		// slots may each state a different one — there is no slot to put
		// the disagreement in, so the row says what it cannot carry
		// rather than collapsing the slots into a single case that
		// claims more than any one of them does.
		return ForeignEntryRow{}, "parameter '" + name + "' states a tuple, whose slots may differ and the entry row carries one element case"
	case annotations.DeclaredPossiblyUndefined:
		if declared.Inner == nil {
			return ForeignEntryRow{}, "parameter '" + name + "' admits the absent value, which crosses no single set"
		}
		inner, sentence := foreignEntryRowOf(declared.Inner, name)
		if sentence != "" {
			return ForeignEntryRow{}, sentence
		}
		if inner.IsSequence {
			return ForeignEntryRow{}, "parameter '" + name + "' admits the absent value alongside a sequence, which the entry row has no cases slot for on a sequence shape"
		}
		return ForeignEntryRow{Name: name, Cases: append(append([]Case{}, inner.Cases...), Case{Sort: CaseSortNull})}, ""
	}
	return ForeignEntryRow{}, "parameter '" + name + "' carries no refinement this checker reads"
}

// caseOfSet wraps a plain declared RefinedSet as its own Case: a
// string case where the set's own forms are string-shaped, a number
// case otherwise. String-shaped is either of two tests, either firing
// being enough — refinementsets.StatesSequence (the fast, non-
// recursive positive test: one sequence-shaped form anywhere in the
// set) or refinementsets.SequenceShaped (the recursive test: EVERY
// top-level form is itself a sequence form, including through a
// Union/Difference of sequence-shaped operands). StatesSequence alone
// misses a DERIVED string whose top form is a Union of sequence-
// shaped branches rather than a bare sequence form — e.g.
// `["ok", "warn", "error"][code]`'s own join over a bounded index
// builds Union(Concatenation, Concatenation) at the top, never a bare
// Concatenation — which is exactly the shape a string-literal-union
// return derives to. Ported from fact_export.rs's is_string_shaped
// (fact_export.rs:155): `states_sequence(set) || sequence_shaped(set)`.
//
// RefinedSet ITSELF still carries no "boolean" tag (chain_root_
// constructor.go's "boolean" root compiles to the SAME {0,1} OneOf
// shape a two-element numeric set would) — the tag rides one layer
// up, on the DeclaredRefinement/Annotation that WRAPS the set
// (KindTag), which is why foreignEntryRowOf reads it before the set
// ever reaches here, at the one call site (the top-level scalar
// position) where a DeclaredRefinement is still in scope. The
// repetition ELEMENT call site (repeated.Element, above) has already
// dropped to a bare RefinedSet by the time it reaches caseOfSet —
// AsRepetition answers no per-element DeclaredRefinement — so a
// `z.array(z.boolean())` element case still reads as a number case
// here; that narrower gap is not this fix's scope.
func caseOfSet(set refinementsets.RefinedSet) Case {
	if refinementsets.StatesSequence(set) || refinementsets.SequenceShaped(set) {
		return Case{Sort: CaseSortString, Set: set}
	}
	return Case{Sort: CaseSortNumber, Set: set}
}

// DerivedReturnOf answers the call-site-free derived return: a fresh
// Env with each parameter bound to what its own declaration states
// (AbstractValueOfDeclared, declared_value.go:23), the body walked
// silently (no diagnostics escape, no caller state is touched), and
// every return statement's value joined through JoinSinkSummarized —
// the same join InlineContractBody itself takes when a marker (a
// self-recursive branch) may ride the sink (function_summaries.go:667-691).
// A plain join (inline_contract_body.go:242-248's bases-only fold)
// would silently drop a recursive function's own recursive branch
// rather than joining it as unknown; JoinSinkSummarized is what keeps
// that branch honestly imprecise instead of erased, and a
// call-site-free derivation has no caller context to rule recursion
// out, so the tolerant join is the correct one here — not merely a
// convenient one.
//
// ok is false wherever the declaration has no body, or where a param
// binds nothing this reads through (contract.Params width mismatched
// against the declaration's own parameter list — a shape earlier
// passes would already have caught, checked again here because this
// function runs independent of them).
func DerivedReturnOf(ctx *FlowContext, contract *FunctionContract) (abstractdomain.AbstractValue, bool) {
	body := contract.Declaration.Body()
	if body == nil {
		return abstractdomain.AbstractValue{}, false
	}
	parameters := contract.Declaration.Parameters()
	if len(contract.Params) != len(parameters) {
		return abstractdomain.AbstractValue{}, false
	}
	env := NewEnv()
	for i, parameter := range parameters {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		declared := contract.Params[i]
		if declared == nil {
			env.Set(name.Text(), silence.Residue())
			continue
		}
		env.Set(name.Text(), AbstractValueOfDeclared(*declared))
	}

	var sink []abstractdomain.AbstractValue
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	silent.ReturnSink = &sink

	if ast.IsBlock(body) {
		AnalyzeStatements(&silent, env, body.AsBlock().Statements.Nodes, contract.Result)
	} else {
		sink = append(sink, evaluateExpression(&silent, env, body))
	}

	if len(sink) == 0 {
		return silence.Residue(), true
	}
	return JoinSinkSummarized(sink), true
}

// FaithfulReturnCases reads the derived value into its RULED cases
// list, or the plain-words reason it has none — a nested sequence, an
// opaque unknown, an object this walk cannot enumerate all refuse,
// and this names which.
//
// KindPossiblyUndefined (X | null / X | undefined) emits the INNER
// value's own cases plus {"sort":"null"} appended — the RULED
// schema's own vocabulary for a possibly-absent return, where
// returnKindWords' "a possibly-absent value" omission used to be the
// only reading. KindValues{PrimitiveBoolean} emits the whole-sort
// boolean case rather than falling through to SetOfKnown's own {0,1}
// numeric reading (SetOfKnown does not carry KindTag through, so this
// check runs BEFORE it, on the still-tagged AbstractValue). KindObject
// emits the object case (objectCaseOf, below) rather than refusing —
// the RULED schema's object vocabulary, item 1: a return the walk
// knows as an object with member structure crosses as
// {"sort":"object","members":{...},"closed":bool} rather than an
// omission. KindKindUnion whose every arm is itself object-shaped
// (a Result-style return — two distinct object shapes joined, e.g.
// {ok:true,value} | {ok:false,error}) emits one object Case per arm in
// the SAME cases list, through the same union channel scalar
// multi-cases already use — a mixed union (one object arm beside a
// number arm) still refuses, since the schema's object vocabulary
// only covers a return whose EVERY arm is a member structure this
// walk can enumerate. Every other faithful-set reading emits exactly
// one case, its sort read off the set's own forms (caseOfSet's
// StatesSequence test) — TypeofWordOfKnown is not reused here because
// it returns "" for an ambiguous scalar set (the {0,1} boolean/number
// conflation TypeofWordOfKnown itself documents), and that ambiguity
// is exactly what the boolean check above already resolved by that
// point.
func FaithfulReturnCases(value abstractdomain.AbstractValue) ([]Case, string) {
	if value.Kind == abstractdomain.KindPossiblyUndefined {
		if value.Inner == nil {
			return nil, "the derived return is " + returnKindWords(value) + ", which has no faithful set reading"
		}
		inner, sentence := FaithfulReturnCases(*value.Inner)
		if sentence != "" {
			return nil, sentence
		}
		return append(append([]Case{}, inner...), Case{Sort: CaseSortNull}), ""
	}
	if value.Kind == abstractdomain.KindValues && value.KindTag == abstractdomain.PrimitiveBoolean {
		return []Case{{Sort: CaseSortBoolean}}, ""
	}
	if value.Kind == abstractdomain.KindObject {
		c, sentence := objectCaseOf(value)
		if sentence != "" {
			return nil, sentence
		}
		return []Case{c}, ""
	}
	if value.Kind == abstractdomain.KindKindUnion {
		cases := make([]Case, 0, len(value.Arms))
		for _, arm := range value.Arms {
			if arm.Kind != abstractdomain.KindObject {
				return nil, "the derived return is a union of sorts whose arms are not all objects " +
					"the walk can enumerate, which has no faithful object-union reading"
			}
			c, sentence := objectCaseOf(arm)
			if sentence != "" {
				return nil, sentence
			}
			cases = append(cases, c)
		}
		return cases, ""
	}
	set, ok := abstractdomain.SetOfKnown(value)
	if !ok {
		return nil, "the derived return is " + returnKindWords(value) + ", which has no faithful set reading"
	}
	if len(set.Forms) == 0 {
		return nil, "the derived return is the empty set, which states no crossable fact"
	}
	return []Case{caseOfSet(set)}, ""
}

// objectCaseOf reads one KindObject AbstractValue into the RULED
// schema's object Case: each key's own cases list (through
// FaithfulReturnCases, recursively — a member's cases may themselves
// be object cases, the schema's own "members recursive" rule) and
// Closed from the object's own Complete fact (abstractdomain's
// completeness claim — "the producer states the exact key set" —
// carried across unchanged, never re-derived). A key the walk cannot
// read as a faithful cases list (a nested unknown, a function value)
// stops the WHOLE object from crossing, named by which key and why —
// the RULED schema states no partial-object reading, so one unreadable
// member is the same omission a top-level unreadable value already is.
func objectCaseOf(value abstractdomain.AbstractValue) (Case, string) {
	members := make(map[string][]Case, len(value.Keys))
	for _, key := range value.Keys {
		keyCases, sentence := FaithfulReturnCases(key.Value)
		if sentence != "" {
			return Case{}, "the derived return's key '" + key.Name + "' is " +
				returnKindWords(key.Value) + ", which has no faithful set reading"
		}
		members[key.Name] = keyCases
	}
	return Case{Sort: CaseSortObject, Members: members, Closed: value.Complete}, ""
}

// returnKindWords is plain words for what a return derived to, for
// the omission sentence — the Go twin of fact_export.rs's
// return_kind_words. No stringer exists on abstractdomain.Kind (it
// is a bare string type carrying no message), so this is the one
// place that names each refusing kind for a reader.
func returnKindWords(value abstractdomain.AbstractValue) string {
	switch value.Kind {
	case abstractdomain.KindObject:
		return "an object"
	case abstractdomain.KindObjectStar:
		return "an object"
	case abstractdomain.KindList, abstractdomain.KindCollection:
		return "a nested sequence"
	case abstractdomain.KindPromise:
		return "an awaitable"
	case abstractdomain.KindDate:
		return "a date"
	case abstractdomain.KindSymbol:
		return "a symbol"
	case abstractdomain.KindHostFunction:
		return "a function"
	case abstractdomain.KindBigints:
		return "an arbitrary-width integer"
	case abstractdomain.KindRegex:
		return "a regular expression"
	case abstractdomain.KindUndef, abstractdomain.KindNull:
		return "the absent value"
	case abstractdomain.KindNaN:
		return "NaN"
	case abstractdomain.KindPossiblyUndefined:
		return "a possibly-absent value"
	case abstractdomain.KindPossiblyNaN:
		return "a possibly-NaN value"
	case abstractdomain.KindKindUnion:
		return "a union of sorts"
	case abstractdomain.KindArrayHoles:
		return "a sequence of holes"
	case abstractdomain.KindValues:
		return "the empty tuple"
	case abstractdomain.KindUnknown:
		return "a value this walk never determined"
	case abstractdomain.KindSet, abstractdomain.KindVariable:
		return "a set of values whose members are not plain numbers"
	}
	return "a value this walk never determined"
}

// ProvenanceLineOf is the 1-based line the declaration's own NAME
// node sits on — the def-name convention fact_export.rs's line_of
// takes (a decorated/annotated declaration's statement range can
// start earlier; the name node is always on the line a reader means
// by "the function's line"). Uses the same scanner call
// cmd/refinedts-check/main.go:154 makes for a diagnostic's position.
func ProvenanceLineOf(sourceFile *ast.SourceFile, declaration *ast.Node) int {
	nameNode := declaration.Name()
	pos := declaration.Pos()
	if nameNode != nil {
		pos = nameNode.Pos()
	}
	line, _ := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, pos)
	return line + 1
}

// ProvenanceSaidOf renders the one sentence provenance.said states,
// assembled from the facts the artifact already carries — each entry
// bound and the derived return, spelled through formatCases (which
// itself spells a number/string case through
// refinementsets.FormatForDiagnostics, the same formatter every
// refinement sentence in this checker is spelled through). Mirrors
// fact_export.rs's provenance_sentence (lines 379-405).
func ProvenanceSaidOf(entry []ForeignEntryRow, returnCases []Case) string {
	words := ""
	for i, row := range entry {
		if i > 0 {
			words += " and "
		}
		if row.IsSequence {
			words += "'" + row.Name + "' whose every element is " +
				formatCases(row.ElementCases) +
				" and whose length is at least " + strconv.Itoa(row.LengthAtLeast)
			continue
		}
		words += "'" + row.Name + "' is " + formatCases(row.Cases)
	}
	return "given " + words + ", this body's returns derive " + formatCases(returnCases)
}

// formatCases spells a cases list for a reader: one case's own words,
// or several joined with " or " — a number/string case through
// refinementsets.FormatForDiagnostics, boolean/null through their own
// plain words (neither carries a Set to format), object through
// formatObjectCase (its own member-by-member spelling, since an object
// case carries Members rather than a Set — the default arm's
// FormatForDiagnostics(c.Set) would read an object case's zero-value
// Set and spell nonsense).
func formatCases(cases []Case) string {
	if len(cases) == 0 {
		return "any value"
	}
	words := ""
	for i, c := range cases {
		if i > 0 {
			words += " or "
		}
		switch c.Sort {
		case CaseSortBoolean:
			words += "a boolean"
		case CaseSortNull:
			words += "absent"
		case CaseSortObject:
			words += formatObjectCase(c)
		default:
			words += refinementsets.FormatForDiagnostics(c.Set)
		}
	}
	return words
}

// formatObjectCase spells one object case as "{key: <cases>, ...}" —
// each member's own cases list through formatCases, recursively (a
// member may itself carry an object case), keys in SORTED order so
// the sentence is deterministic across runs (Members is a Go map,
// with no iteration order of its own).
func formatObjectCase(c Case) string {
	names := make([]string, 0, len(c.Members))
	for name := range c.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	words := "{"
	for i, name := range names {
		if i > 0 {
			words += ", "
		}
		words += name + ": " + formatCases(c.Members[name])
	}
	return words + "}"
}
