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
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// ForeignEntryRow is one exported parameter: its name, and the shape
// its declaration states — a SEQUENCE (the window's element set plus
// the length floor the declaration's own window carries) or a SCALAR
// (one set). Mirrors fact_export.rs's EntryRow/EntryShape as one flat
// struct — the Go side has no sum type, so IsSequence tags which
// fields the row carries.
type ForeignEntryRow struct {
	Name          string
	IsSequence    bool
	Element       refinementsets.RefinedSet
	LengthAtLeast int
	Set           refinementsets.RefinedSet
}

// ExportFunctionFact reads contract's entry rows and derives its
// return set, or answers one omission sentence naming the construct
// that stopped it. Never both: an omission answers a zero entry
// slice and a zero-value RefinedSet alongside it.
//
// The consumer this artifact will feed already declines any function
// with more than one entry (foreign_edge_artifact.go's ForeignEntry
// is read one row at a time, and the stdin harness hands one JSON
// value to one parameter) — the exporter refuses symmetrically here,
// rather than exporting rows a caller can never fill.
func ExportFunctionFact(ctx *FlowContext, contract *FunctionContract) (entry []ForeignEntryRow, returnSet refinementsets.RefinedSet, omission string) {
	parameters := contract.Declaration.Parameters()
	if len(parameters) > 1 {
		return nil, refinementsets.RefinedSet{}, "the function declares more than one parameter, and the stdin harness hands one JSON value to one parameter"
	}
	rows, entrySentence := foreignEntryRowsOf(parameters, contract.Params)
	if entrySentence != "" {
		return nil, refinementsets.RefinedSet{}, entrySentence
	}
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		return nil, refinementsets.RefinedSet{}, "the body's returns derived no value this walk could read"
	}
	set, returnSentence := FaithfulReturnSet(derived)
	if returnSentence != "" {
		return nil, refinementsets.RefinedSet{}, returnSentence
	}
	return rows, set, ""
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
// unlike the Rust side's `spelling.starts_with` check); every other
// declared kind states a shape no single entry row can carry, named
// plainly.
func foreignEntryRowOf(declared *annotations.DeclaredRefinement, name string) (ForeignEntryRow, string) {
	if declared == nil {
		return ForeignEntryRow{}, "parameter '" + name + "' carries no refinement this checker reads"
	}
	switch declared.Kind {
	case annotations.DeclaredSet:
		if repeated, ok := refinementsets.AsRepetition(*declared.Set); ok {
			return ForeignEntryRow{
				Name:          name,
				IsSequence:    true,
				Element:       repeated.Element,
				LengthAtLeast: repeated.Lo,
			}, ""
		}
		return ForeignEntryRow{Name: name, Set: *declared.Set}, ""
	case annotations.DeclaredObject:
		return ForeignEntryRow{}, "parameter '" + name + "' states an object, which crosses no single set"
	case annotations.DeclaredObjectArray:
		return ForeignEntryRow{}, "parameter '" + name + "' states an array of records, which crosses no single set"
	case annotations.DeclaredVariable:
		return ForeignEntryRow{}, "parameter '" + name + "' states a refinement variable, which crosses no single set"
	case annotations.DeclaredPossiblyUndefined:
		return ForeignEntryRow{}, "parameter '" + name + "' admits the absent value, which crosses no single set"
	}
	return ForeignEntryRow{}, "parameter '" + name + "' carries no refinement this checker reads"
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

// FaithfulReturnSet wraps abstractdomain.SetOfKnown (lattice_operations.go:468):
// the derived value read as the tuple-layer set it denotes, or the
// plain-words reason it has none — an object, a nested sequence, an
// opaque unknown all refuse there, and this names which.
func FaithfulReturnSet(value abstractdomain.AbstractValue) (refinementsets.RefinedSet, string) {
	set, ok := abstractdomain.SetOfKnown(value)
	if !ok {
		return refinementsets.RefinedSet{}, "the derived return is " + returnKindWords(value) + ", which has no faithful set reading"
	}
	if len(set.Forms) == 0 {
		return refinementsets.RefinedSet{}, "the derived return is the empty set, which states no crossable fact"
	}
	return set, ""
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
// bound and the derived return, spelled through
// refinementsets.FormatForDiagnostics, the same formatter every
// refinement sentence in this checker is spelled through. Mirrors
// fact_export.rs's provenance_sentence (lines 379-405).
func ProvenanceSaidOf(entry []ForeignEntryRow, returnSet refinementsets.RefinedSet) string {
	words := ""
	for i, row := range entry {
		if i > 0 {
			words += " and "
		}
		if row.IsSequence {
			words += "'" + row.Name + "' whose every element is " +
				refinementsets.FormatForDiagnostics(row.Element) +
				" and whose length is at least " + strconv.Itoa(row.LengthAtLeast)
			continue
		}
		words += "'" + row.Name + "' is " + refinementsets.FormatForDiagnostics(row.Set)
	}
	return "given " + words + ", this body's returns derive " + refinementsets.FormatForDiagnostics(returnSet)
}
