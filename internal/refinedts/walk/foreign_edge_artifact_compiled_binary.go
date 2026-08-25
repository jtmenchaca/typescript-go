/* ── compiled-binary targets ─────────────────────────────────────── */
//
// A COMPILED BINARY (an argv element with no recognized script
// extension — foreignEdgeOf's own compiled-binary row) has no
// checkable SOURCE this checker reads (the .cpp a human compiled it
// from is not code this checker ever opens) and no PRODUCER binary
// that regenerates a fact (resolveProducerPyPath's own
// refinedpy-check only exports Python facts, and this tree's own
// producer only exports TypeScript facts) — the cache-path/target-
// integrity-hash/producer-freshness/auto-export machinery above
// exists to serve exactly those two premises, neither of which a
// compiled binary carries. What a compiled binary's fact CAN state is
// unchanged: the same RULED cases schema, read through the SAME field
// readers this file already applies to a Python artifact (surfaceOf,
// functionFactOf — both already generic over the parsed envelope,
// with no Python-specific reading inside either), with `language`
// stating "cpp" in place of "python" and `runtime.band` stating this
// checker's own compiled-C++ pin. Mirrors the Rust consumer's own
// twin (foreign_edge_artifact.rs's compiled_binary_fact_path /
// read_compiled_binary_fact / check_compiled_binary_envelope)
// exactly: sibling-path discovery, the language check, the runtime
// band constant, and the three-rung ladder below.

package walk

import (
	"encoding/json"
	"os"
)

// CompiledBinaryArtifactLanguage is the `language` value a compiled
// binary's fact states — distinct from FactArtifactKindV2's Python
// reading, routed the same way dispatchArtifactEnvelope already
// routes on (kind, language).
const CompiledBinaryArtifactLanguage = "cpp"

// CompiledBinaryRuntimeBand is the runtime band a compiled-binary fact
// commits to — the ISO C++17 standard the triangle's own producer
// states it compiles against
// (examples/cross-language/audio-level-triangle/targets/cpp_level.cpp's
// own header comment: `c++ -std=c++17 -o cpp_level cpp_level.cpp`).
// Mirrors ForeignRuntimeBand's own role for Python: the SPEC LEVEL the
// target's checked code runs against, not one compiler binary.
const CompiledBinaryRuntimeBand = "c++17"

// CompiledBinaryFactSuffix is what a compiled binary's sibling fact
// file is named: `<binary_path>.facts.json`, never
// ForeignArtifactSuffix's `.refined.json` cache suffix — a compiled
// binary's fact sits NEXT TO the binary itself (hand- or
// tool-authored, committed alongside it), not in a derived project
// cache a producer regenerates.
const CompiledBinaryFactSuffix = ".facts.json"

// CompiledBinaryFactPath answers the sibling fact-file path for a
// compiled binary: binaryPath with CompiledBinaryFactSuffix appended —
// "./targets/cpp_level" reads "./targets/cpp_level.facts.json".
func CompiledBinaryFactPath(binaryPath string) string {
	return binaryPath + CompiledBinaryFactSuffix
}

// ReadCompiledBinaryArtifact reads a compiled binary's sibling fact
// file — the three-rung ladder this construct owns: no fact file at
// that path (the caller names this rung, since only it can tell apart
// "no sibling at all" from "a sibling that failed to parse" — see this
// function's own ("", false, "") answer below), a fact file that
// exists but fails to parse (this function's own sentence, naming the
// unreadable file), or a fact file that parses and serves (a non-nil
// *ForeignArtifact).
//
// Answers (artifact, exists, sentence): exists is FALSE only when no
// file sits at the sibling path at all — the caller (foreign_edge.go)
// checks that flag to choose between the generic compiled-binary
// no-fact sentence and this function's own unreadable-file sentence,
// exactly the disk-existence check the Rust twin's own caller performs
// (never string-sniffing the sentence text to tell the two rungs
// apart).
//
// No target-integrity hash, no producer-freshness check, no
// auto-export attempt: none of the three apply to a compiled binary
// (see this section's own banner comment).
func ReadCompiledBinaryArtifact(binaryPath string) (artifact *ForeignArtifact, exists bool, sentence string) {
	factPath := CompiledBinaryFactPath(binaryPath)
	raw, err := os.ReadFile(factPath)
	if err != nil {
		return nil, false, ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, true, factPath + " is not readable JSON, so the target states nothing this edge can use"
	}
	artifact, readSentence := readCompiledBinaryArtifactBody(parsed, binaryPath, factPath)
	return artifact, true, readSentence
}

// readCompiledBinaryArtifactBody is the compiled-binary body reader —
// checkCompiledBinaryEnvelope's own twin of readPythonArtifact, minus
// target integrity (a compiled binary has no source this checker
// reads) and minus the producer-freshness/auto-export machinery
// (there is no producer for this language).
func readCompiledBinaryArtifactBody(parsed map[string]any, binaryPath string, factPath string) (*ForeignArtifact, string) {
	if sentence := checkCompiledBinaryEnvelope(parsed, factPath); sentence != "" {
		return nil, sentence
	}
	band, bandOk := nestedString(parsed, "runtime", "band")
	if !bandOk {
		return nil, factPath + " names no runtime band, and the edge's claim inherits " +
			"whichever band the target's pins commit to"
	}
	if band != CompiledBinaryRuntimeBand {
		return nil, factPath + " commits to the runtime band " + band +
			", and this checker's compiled-binary pins commit to " + CompiledBinaryRuntimeBand +
			" — the edge cannot inherit semantics it has not transcribed"
	}
	surface, surfaceSentence := surfaceOf(parsed, factPath)
	if surfaceSentence != "" {
		return nil, surfaceSentence
	}
	fact, factSentence := functionFactOf(parsed, surface.calls, factPath, binaryPath, nil)
	if factSentence != "" {
		return nil, factSentence
	}
	return &ForeignArtifact{
		Path:        factPath,
		TargetFile:  binaryPath,
		RuntimeBand: band,
		Surface:     surface.channel,
		ArgvIndex:   surface.argIndex,
		Called:      *fact,
	}, ""
}

// checkCompiledBinaryEnvelope is dispatchArtifactEnvelope's own twin
// for a compiled binary's sibling fact: the same "refined"/"version"/
// "kind" checks, admitting language CompiledBinaryArtifactLanguage in
// place of "python". No version field is ever admitted here either —
// its presence is itself a decline, exactly as it is for a Python
// artifact.
func checkCompiledBinaryEnvelope(parsed map[string]any, factPath string) string {
	envelope, ok := parsed["refined"].(map[string]any)
	if !ok {
		return factPath + ` carries no "refined" envelope, so nothing identifies it as a fact artifact`
	}
	kind, _ := envelope["kind"].(string)
	language, _ := parsed["language"].(string)

	if _, hasVersion := envelope["version"]; hasVersion {
		return factPath + ` states a "version" field on its "refined" envelope, and the current ` +
			`schema states no version, ever — the envelope is a superseded shape, and states no fact this edge reads`
	}
	if kind != FactArtifactKindV2 {
		return factPath + ` states (kind "` + kind + `"), and this edge reads only (kind "` +
			FactArtifactKindV2 + `") — the field meanings are what the kind pins`
	}
	if language != CompiledBinaryArtifactLanguage {
		return factPath + ` states (kind "` + kind + `", language ` + quotedOrNone(language) +
			`), and this edge reads language "` + CompiledBinaryArtifactLanguage +
			`" for a compiled binary's fact — the language field is what selects the runtime-band pins`
	}
	return ""
}

// CompiledBinaryNoFactSentence is the ladder's floor rung: the binary
// is recognized (the checker CAN name the code that runs next — the
// argv text alone is enough for that), but no sibling
// `<binaryPath>.facts.json` sits beside it, so the checker looks for
// one and finds nothing. Named rather than the generic "there is no
// <path>.refined.json; write it with -export-fact" sentence
// readAndVerifyForeignArtifact states for a Python target — that
// sentence names a command that has no meaning for a target that is
// not TypeScript/Python source. Mirrors diagnostic_sentences.rs's own
// compiled_binary_no_fact exactly.
func CompiledBinaryNoFactSentence(binaryPath string) string {
	return binaryPath + " is a compiled binary, and there is no " + CompiledBinaryFactPath(binaryPath) +
		" beside it — the checker can name the code that runs next but has no fact stating what it does"
}
