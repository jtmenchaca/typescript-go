package ls

import (
	"github.com/microsoft/typescript-go/internal/ls/autoimport"
	"github.com/microsoft/typescript-go/internal/ls/lsconv"
	"github.com/microsoft/typescript-go/internal/ls/lsutil"
	"github.com/microsoft/typescript-go/internal/sourcemap"
)

type Host interface {
	UseCaseSensitiveFileNames() bool
	ReadFile(path string) (contents string, ok bool)
	Converters() *lsconv.Converters
	GetPreferences(activeFile string) lsutil.UserPreferences
	GetECMALineInfo(fileName string) *sourcemap.ECMALineInfo
	AutoImportRegistry() *autoimport.Registry
	// RefinementCache is the Session-owned refinement result store —
	// the SAME pointer from every Snapshot, so Fix payloads survive
	// the per-request LanguageService (refinement_cache.go).
	RefinementCache() *RefinementCache
	// ScriptVersion is the overlay version of an open file, 0 for a
	// disk file — the RefinementCache's freshness key.
	ScriptVersion(fileName string) int32

	// Used for module specifier completions.
	// ! Do not use for anything else, as this violates the principle that
	// the host is a snapshot-in-time.
	ReadDirectory(currentDir string, path string, extensions []string, excludes []string, includes []string, depth int) []string
	GetDirectories(path string) []string
	DirectoryExists(path string) bool
	FileExists(path string) bool
}
