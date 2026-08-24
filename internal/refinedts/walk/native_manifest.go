// RUNG 2 of the compiled-extension recognition ladder (ext.2,
// packages/cpp/findings/python-c-extension-boundary.md's own build
// order item 2): a per-module binding manifest, a committed JSON
// table mapping FUNCTION NAME -> ENTRY CONTRACT (the RULED cases-
// schema's own Case union, docs/one-checker/fact-schema.md — the SAME
// shape ExternalModule.cpp's manifest reader and FactExport's own
// producer both already speak) -> PRODUCER key naming the sibling
// `<producer>.facts.json` ext.3 reads for the return (via
// ReadCompiledBinaryArtifact, foreign_edge_artifact.go, already built
// and unit-tested for the compiled-binary crossing this file reuses
// verbatim — a manifest's producer half is not a new reader).
//
// DISCOVERY: a manifest for module `addon` is read from
// `<entry_directory>/addon.manifest.json` — beside the checked file,
// mirroring binding_manifest.rs's own "beside entry_directory"
// convention and ExternalModule.cpp's identical EntryDirectory
// reading, restated for a checked file's own directory here (this
// checker has no separate "entry directory" concept threaded through
// FlowContext yet — NativeManifestDiscoveryDir reads it from the
// callee's own enclosing source file, the file actually being
// checked at the call site, which is the honest analogue).
//
// A module named in NO manifest stays ext.1's plain named decline
// (NativeModuleCallName / UnmodeledCallResult's own reason note) —
// this file only narrows that naming for a module that DOES have one.
//
// ENTRY CONTRACT: unlike binding_manifest.rs's PythonArgParser string
// grammar, this reads the SAME cases-schema Case union the manifest's
// own return position (via ext.3's fact reader) already speaks — the
// task's own instruction that a schema choice prefers "the one ruled
// schema" once one exists, restated here since Go's foreign_edge_
// artifact.go already has casesOf and Case, so no second entry-
// contract grammar is invented.
package walk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// NativeManifestSuffix is the sibling manifest file's own suffix:
// `<module>.manifest.json`, beside the checked file — mirrors
// CompiledBinaryFactSuffix's identical "sibling of the checked
// artifact" convention (foreign_edge_artifact.go).
const NativeManifestSuffix = ".manifest.json"

// NativeManifestFunctionRow is one function's manifest row: its own
// ENTRY positions (the RULED schema's own Case union, per-parameter,
// in POSITIONAL order — a manifest states no parameter NAME, the same
// reading ExternalModule.cpp's own ManifestEntryCase keeps, since a
// call's own argument list is already positional) and the PRODUCER
// key naming the `<producer>.facts.json` sibling ext.3 reads for its
// return.
type NativeManifestFunctionRow struct {
	Entries  []Case
	Producer string
}

// NativeManifest is one module's whole manifest: every function it
// lists, keyed by name.
type NativeManifest struct {
	ModuleName string
	Functions  map[string]NativeManifestFunctionRow
}

// NativeManifestPath answers the sibling manifest path for a module
// named inside directory: "addon" under "/project/src" reads
// "/project/src/addon.manifest.json".
func NativeManifestPath(directory string, moduleName string) string {
	return filepath.Join(directory, moduleName+NativeManifestSuffix)
}

// ReadNativeManifest reads and parses directory/<moduleName>.manifest.json
// — three-rung answer, mirroring ReadCompiledBinaryArtifact's own
// (manifest, exists, sentence) contract exactly: exists is false ONLY
// when no file sits at the sibling path at all (rung 1's own plain
// decline territory — a module with no manifest is not an error), a
// file that exists but fails to parse or states a malformed row
// answers a named sentence, and a fully-readable manifest answers
// non-nil with no sentence.
func ReadNativeManifest(directory string, moduleName string) (manifest *NativeManifest, exists bool, sentence string) {
	if directory == "" {
		return nil, false, ""
	}
	manifestPath := NativeManifestPath(directory, moduleName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, false, ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, true, manifestPath + " is not readable JSON, so the module states no entry contracts this edge can use"
	}
	functions := make(map[string]NativeManifestFunctionRow, len(parsed))
	for functionName, rawRow := range parsed {
		row, ok := rawRow.(map[string]any)
		if !ok {
			return nil, true, manifestPath + " states an unreadable row for '" + functionName + "'"
		}
		producer, producerOk := row["producer"].(string)
		if !producerOk || producer == "" {
			return nil, true, manifestPath + " states a row for '" + functionName + "' with no \"producer\" symbol"
		}
		rawEntries, hasEntries := row["entry"].([]any)
		var entries []Case
		if hasEntries {
			for index, rawEntry := range rawEntries {
				entryRow, ok := rawEntry.(map[string]any)
				if !ok {
					return nil, true, manifestPath + " states an unreadable entry position " +
						strconv.Itoa(index) + " for '" + functionName + "'"
				}
				rawCases, hasCases := entryRow["cases"]
				if !hasCases {
					return nil, true, manifestPath + " states an entry position for '" + functionName +
						"' that is not a cases list"
				}
				cases, casesSentence := casesOf(rawCases, functionName, manifestPath)
				if casesSentence != "" {
					return nil, true, casesSentence
				}
				entries = append(entries, cases...)
			}
		}
		functions[functionName] = NativeManifestFunctionRow{Entries: entries, Producer: producer}
	}
	return &NativeManifest{ModuleName: moduleName, Functions: functions}, true, ""
}

// NativeManifestDiscoveryDir answers the directory a native-module
// manifest is discovered beside: the checked file's own enclosing
// source file directory — the honest analogue of Python's
// entry_directory/C++'s EntryDirectory in a tree with no separate
// "entry directory" concept threaded through FlowContext yet. "" for
// a node with no enclosing source file (a synthesized node), matching
// discover_manifest's own "no entry_directory, no discovery" reading.
func NativeManifestDiscoveryDir(node *ast.Node) string {
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		return ""
	}
	return filepath.Dir(sourceFile.FileName())
}

// NativeManifestEntryCrossingFits judges one argument's own
// AbstractValue against one manifest entry position's own cases list
// — the crossing-fit chain restated for a manifest entry rather than
// a foreign-edge return, reusing foreignScalarSubset's identical
// kernel-ask discipline (a kernel refusal leaves the crossing
// UNJUDGED, never wrongly refuted, matching entry_crossing_fits' own
// Err(()) posture). Only a number- or string-sorted entry (via
// scalarCaseSetOf, the same single-case reader foreign_edge.go's own
// crossing-fit chain already uses) is judged; a boolean/null/object
// entry, or an entry with more than one case, is left UNJUDGED here —
// this first unit's own floor, matching entry_crossing_fits' identical
// "only Kind::Values/Kind::Set operands ask the kernel" scope.
// Answers (fits, asked): asked=false means the kernel was never
// reached (an unjudgeable entry shape, or a value with no exact set of
// its own), and fits is meaningless in that case.
func NativeManifestEntryCrossingFits(
	ctx *FlowContext, value abstractdomain.AbstractValue, entryCases []Case, functionName string,
) (fits bool, asked bool) {
	entrySet, sentence := scalarCaseSetOf(entryCases, functionName)
	if sentence != "" {
		return false, false
	}
	var valueSet refinementsets.RefinedSet
	switch {
	case value.Kind == abstractdomain.KindValues && len(value.Values) == 1:
		valueSet = refinementsets.MakeRefinedSet(refinementsets.OneOf(value.Values))
	case value.Kind == abstractdomain.KindSet:
		valueSet = value.Set
	default:
		return false, false
	}
	return foreignScalarSubset(ctx, valueSet, entrySet)
}
