package walk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// nativeManifestJSON is a well-formed manifest — a single "scale"
// function taking one number entry (-2..2), naming its producer
// symbol — the SAME RULED cases-schema Case union
// foreign_edge_artifact.go already reads for an entry position.
const nativeManifestJSON = `{
  "scale": {
    "entry": [{"cases": [{"sort": "number", "set": {"forms": [
      {"form": "atLeast", "a": {"num": -2, "exp": 0}},
      {"form": "atMost", "a": {"num": 2, "exp": 0}}
    ]}}]}],
    "producer": "addon_scale_impl"
  }
}`

// TestReadNativeManifest_AWellFormedManifestReadsItsRow pins the
// discovery + parse + serve ladder's top rung: a real sibling
// <module>.manifest.json beside a directory reads cleanly and binds
// the function's own entry cases and producer symbol.
func TestReadNativeManifest_AWellFormedManifestReadsItsRow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "addon.manifest.json"), []byte(nativeManifestJSON), 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	manifest, exists, sentence := ReadNativeManifest(dir, "addon")
	if sentence != "" {
		t.Fatalf("ReadNativeManifest declined: %s", sentence)
	}
	if !exists || manifest == nil {
		t.Fatalf("a real manifest file was not read")
	}
	row, ok := manifest.Functions["scale"]
	if !ok {
		t.Fatalf("the scale row did not read")
	}
	if row.Producer != "addon_scale_impl" {
		t.Errorf("row.Producer = %q, want %q", row.Producer, "addon_scale_impl")
	}
	if len(row.Entries) != 1 {
		t.Fatalf("len(row.Entries) = %d, want 1", len(row.Entries))
	}
	if row.Entries[0].Sort != CaseSortNumber {
		t.Errorf("row.Entries[0].Sort = %q, want %q", row.Entries[0].Sort, CaseSortNumber)
	}
}

// TestReadNativeManifest_NoSiblingFileAnswersNotExists pins the
// ladder's floor rung: no manifest file at all is rung 1's own plain
// decline territory, not an error this reader reports.
func TestReadNativeManifest_NoSiblingFileAnswersNotExists(t *testing.T) {
	dir := t.TempDir()
	manifest, exists, sentence := ReadNativeManifest(dir, "nonexistent")
	if exists {
		t.Errorf("exists = true, want false — no manifest file sits at that path")
	}
	if manifest != nil {
		t.Errorf("manifest = %#v, want nil", manifest)
	}
	if sentence != "" {
		t.Errorf("sentence = %q, want empty — a missing manifest is not an error", sentence)
	}
}

// TestReadNativeManifest_AnUnreadableManifestDeclinesNamingTheFile
// pins the middle rung: a manifest file that IS present but fails to
// parse names the file in its own decline sentence.
func TestReadNativeManifest_AnUnreadableManifestDeclinesNamingTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "addon.manifest.json"), []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("writing garbage: %v", err)
	}
	manifest, exists, sentence := ReadNativeManifest(dir, "addon")
	if !exists {
		t.Errorf("exists = false, want true — the file does exist, it just fails to parse")
	}
	if manifest != nil {
		t.Errorf("manifest = %#v, want nil on a parse failure", manifest)
	}
	if sentence == "" {
		t.Errorf("sentence is empty, want a decline naming the unreadable file")
	}
}

// TestNativeManifestEntryCrossingFits_AnInRangeArgumentFits and its
// escaping sibling pin the crossing-fit judge directly against a real
// loaded kernel — the numeric twin of judges_int_sorted_parameter_
// crossing (binding_manifest.rs's own test).
func TestNativeManifestEntryCrossingFits_AnInRangeArgumentFits(t *testing.T) {
	kernel := nanWrapperLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	entryCases := []Case{{Sort: CaseSortNumber, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2))}}
	fitting := abstractdomain.KnownValues([]float64{1.5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	fits, asked := NativeManifestEntryCrossingFits(ctx, fitting, entryCases, "scale")
	if !asked {
		t.Fatalf("the crossing was left unjudged, want a real kernel ask")
	}
	if !fits {
		t.Errorf("fits = false, want true — 1.5 sits inside [-2, 2]")
	}
}

func TestNativeManifestEntryCrossingFits_AnOutOfRangeArgumentEscapes(t *testing.T) {
	kernel := nanWrapperLoadKernel(t)
	ctx := &FlowContext{Kernel: kernel}
	entryCases := []Case{{Sort: CaseSortNumber, Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2))}}
	escaping := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	fits, asked := NativeManifestEntryCrossingFits(ctx, escaping, entryCases, "scale")
	if !asked {
		t.Fatalf("the crossing was left unjudged, want a real kernel ask")
	}
	if fits {
		t.Errorf("fits = true, want false — 5 escapes [-2, 2]")
	}
}
