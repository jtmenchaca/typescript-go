// Reading and verifying the Python artifact: the envelope dispatch,
// target-integrity hashing, and the runtime-band/surface checks that
// readPythonArtifact discharges before answering a fact.

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

// readAndVerifyForeignArtifact is the read itself: parse, then dispatch
// on the envelope triple to the reader whose field meanings it pins.
func readAndVerifyForeignArtifact(targetPath string, artifactPath string) (*ForeignArtifact, string) {
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, "the Python target " + targetPath + " states no fact for this edge — " +
			"there is no " + artifactPath + "; write it with `" +
			ForeignExportCommand + " " + targetPath + "`"
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, artifactPath + " is not readable JSON, so the target states nothing this edge can use"
	}
	return dispatchArtifactEnvelope(parsed, targetPath, artifactPath)
}

// dispatchArtifactEnvelope reads the `refined` envelope and the
// `language` field, then routes to the reader whose field meanings the
// (kind, language) pair pins: only ("fact-artifact", "python") is
// read. A "version" field anywhere in the envelope is itself a
// superseded shape (the RULED schema states no version, ever) and
// declines by name before the kind/language pair is even asked — the
// reader parses the CURRENT shape strictly, and the old versioned
// envelope is NO-FACT like any other unrecognized shape, never a
// silently-tolerated extra field. Any other (kind, language) pair
// declines by name, naming the one accepted form.
func dispatchArtifactEnvelope(parsed map[string]any, targetPath string, artifactPath string) (*ForeignArtifact, string) {
	envelope, ok := parsed["refined"].(map[string]any)
	if !ok {
		return nil, artifactPath + ` carries no "refined" envelope, so nothing identifies it as a fact artifact`
	}
	kind, _ := envelope["kind"].(string)
	language, _ := parsed["language"].(string)

	if _, hasVersion := envelope["version"]; hasVersion {
		return nil, artifactPath + ` states a "version" field on its "refined" envelope, and the current ` +
			`schema states no version, ever — the envelope is a superseded shape, and states no fact this edge reads`
	}

	switch {
	case kind == FactArtifactKindV2 && language == "python":
		return readPythonArtifact(parsed, targetPath, artifactPath)
	default:
		return nil, artifactPath + ` states (kind "` + kind + `", language "` + language +
			`"), and this edge reads only ("` + FactArtifactKindV2 + `", "python")`
	}
}

// readPythonArtifact is the body reader for language "python": target
// integrity, runtime band, then one called function named through
// `surface`, whose `kind` is either "stdin-json" or "argv-scalar"
// (schema-v2.md's two modeled transports).
func readPythonArtifact(parsed map[string]any, targetPath string, artifactPath string) (*ForeignArtifact, string) {
	sentence, targetBytes := checkTargetIntegrity(parsed, targetPath, artifactPath)
	if sentence != "" {
		return nil, sentence
	}
	band, bandOk := nestedString(parsed, "runtime", "band")
	if !bandOk {
		return nil, artifactPath + " names no runtime band, and the edge's claim inherits " +
			"whichever band the target's pins commit to"
	}
	if band != ForeignRuntimeBand {
		return nil, artifactPath + " commits to the runtime band " + band +
			", and this checker's Python pins commit to " + ForeignRuntimeBand +
			" — the edge cannot inherit semantics it has not transcribed"
	}
	surface, surfaceSentence := surfaceOf(parsed, artifactPath)
	if surfaceSentence != "" {
		return nil, surfaceSentence
	}
	fact, factSentence := functionFactOf(parsed, surface.calls, artifactPath, targetPath, targetBytes)
	if factSentence != "" {
		return nil, factSentence
	}
	return &ForeignArtifact{
		Path:        artifactPath,
		TargetFile:  targetPath,
		RuntimeBand: band,
		Surface:     surface.channel,
		ArgvIndex:   surface.argIndex,
		Called:      *fact,
	}, ""
}

// checkTargetIntegrity is CROSS-LANGUAGE-EDGE.md §5's target-integrity
// premise, discharged by hashing the file the check will run against
// and comparing it to the hash the producer recorded. The artifact's
// own `target.file` string is NOT trusted as the identity — a path can
// be stale or relative to another root; the hash is the identity.
//
// Answers the bytes it read alongside the sentence, so a caller past
// this premise (functionFactOf, building the provenance step) can
// place a line in the target's text without a second read of the same
// file — nil whenever the sentence is non-empty.
func checkTargetIntegrity(parsed map[string]any, targetPath string, artifactPath string) (string, []byte) {
	stated, statedOk := nestedString(parsed, "target", "contentHash")
	if !statedOk {
		return artifactPath + " records no target contentHash, so nothing ties its claim to " +
			targetPath + " — the target-integrity premise cannot be discharged", nil
	}
	bytes, err := os.ReadFile(targetPath)
	if err != nil {
		return "the Python target " + targetPath + " cannot be read, so its stated fact cannot be tied to it", nil
	}
	sum := sha256.Sum256(bytes)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != stated {
		return artifactPath + " states the fact of a target whose contents hash to " + stated +
			", and " + targetPath + " hashes to " + actual +
			" — the exported fact is about different code than the code being checked; " +
			"re-export it with `" + ForeignExportCommand + " " + targetPath + "`", nil
	}
	return "", bytes
}
