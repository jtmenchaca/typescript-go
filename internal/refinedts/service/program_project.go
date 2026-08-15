// from service/program_project.ts
//
// Covering-project resolution: the nearest tsconfig.json that covers
// an entry, its adopted compiler options, and the per-process caches
// that keep a sweep from re-parsing every config on every file.
//
// ts.findConfigFile / ts.readConfigFile have no Go twin in this tree
// (internal/tsoptions exposes GetParsedCommandLineOfConfigFile but not
// the upward directory search or the raw JSON reader) — the upward
// walk is done here by hand with os.Stat, and the config is parsed
// via tsoptions.GetParsedCommandLineOfConfigFile, the same call
// typereading/read_type_test.go's testProgram helper uses.

package service

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// Options is OPTIONS in the TS source: the checker's own needs
// (strict, target, module resolution, noEmit) — a project's stated
// rows override these in adoptedOptions, and anything unreadable
// falls back to this alone.
//
// NoUncheckedIndexedAccess is deliberately absent: tsc's `strict` does
// NOT imply it, and the checker gates on == TSTrue (checker.go's
// getIndexedAccessTypeOrUndefined), so the unset TSUnknown zero value
// reads as off — the same answer tsc gives a file with no covering
// project. A project that states the flag brings it through
// adoptedOptions.
func Options() *core.CompilerOptions {
	return &core.CompilerOptions{
		Strict:                     core.TSTrue,
		Target:                     core.ScriptTargetESNext,
		Module:                     core.ModuleKindESNext,
		ModuleResolution:           core.ModuleResolutionKindBundler,
		AllowImportingTsExtensions: core.TSTrue,
		NoEmit:                     core.TSTrue,
		SkipLibCheck:               core.TSTrue,
	}
}

// CoveringProjectResult is the {configPath, options} pair
// coveringProject returns.
type CoveringProjectResult struct {
	ConfigPath string
	Options    *core.CompilerOptions
}

// parsedConfig is the TS source's `{ options, fileSet }` held per
// config path.
type parsedConfig struct {
	options *core.CompilerOptions
	fileSet map[string]bool
}

// parsedConfigsCache is parsedConfigs in the TS source: one PARSE per
// tsconfig per process. A missing entry is distinguished from a
// present-but-nil (unparsable config) entry the way the TS Map does,
// via the second `ok` return.
var (
	parsedConfigsMu    sync.Mutex
	parsedConfigsCache = map[string]*parsedConfig{}
)

// OptionsFor is optionsFor in the TS source: the RESOLUTION options of
// the project an entry file belongs to.
func OptionsFor(entryPath string) *core.CompilerOptions {
	return CoveringProject(entryPath).Options
}

// CoveringProject is coveringProject in the TS source: the covering
// project's identity and its adopted options. A config whose file set
// does not include the entry is not the entry's project, and the walk
// continues upward past it.
func CoveringProject(entryPath string) CoveringProjectResult {
	none := CoveringProjectResult{ConfigPath: "", Options: Options()}
	entryResolved, err := filepath.Abs(entryPath)
	if err != nil {
		return none
	}
	searchDir := filepath.Dir(entryResolved)
	for len(searchDir) > 1 {
		configPath, found := findConfigFile(searchDir)
		if !found {
			return none
		}
		parsed, ok := ParsedConfigOf(configPath)
		if ok && parsed.fileSet[entryResolved] {
			return CoveringProjectResult{ConfigPath: configPath, Options: parsed.options}
		}
		configDir := filepath.Dir(configPath)
		parent := filepath.Dir(configDir)
		if parent == configDir {
			return none
		}
		searchDir = parent
	}
	return none
}

// findConfigFile is the upward-search half of ts.findConfigFile,
// narrowed to "tsconfig.json" (the TS source never states a different
// search name). Walks from searchDir toward the filesystem root; a
// one-directory Stat would miss every project whose entries live in
// subfolders (recharts' src/cartesian/*.tsx never saw the root
// tsconfig's jsx: "react", and shape diagnostics fired TS6142).
func findConfigFile(searchDir string) (string, bool) {
	for {
		candidate := filepath.Join(searchDir, "tsconfig.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			return "", false
		}
		searchDir = parent
	}
}

// ParsedConfigOf is parsedConfigOf in the TS source: one PARSE per
// tsconfig per process — parseJsonConfigFileContent enumerates the
// config's whole file glob, so re-parsing per asking directory turned
// the config walk itself into the sweep's cost.
func ParsedConfigOf(configPath string) (*parsedConfig, bool) {
	parsedConfigsMu.Lock()
	defer parsedConfigsMu.Unlock()
	if held, ok := parsedConfigsCache[configPath]; ok {
		return held, held != nil
	}
	host := compiler.NewCompilerHost(filepath.Dir(configPath), bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
	parsedLine, errors := tsoptions.GetParsedCommandLineOfConfigFile(configPath, &core.CompilerOptions{}, nil, host, nil)
	if len(errors) > 0 || parsedLine == nil {
		parsedConfigsCache[configPath] = nil
		return nil, false
	}
	fileSet := map[string]bool{}
	for _, name := range parsedLine.FileNames() {
		resolved, err := filepath.Abs(name)
		if err != nil {
			resolved = name
		}
		fileSet[resolved] = true
	}
	built := &parsedConfig{
		options: adoptedOptions(parsedLine.CompilerOptions()),
		fileSet: fileSet,
	}
	parsedConfigsCache[configPath] = built
	return built, true
}

// projectByDirCache is projectByDir in the TS source: the covering
// project, memoized by the entry's DIRECTORY.
var (
	projectByDirMu    sync.Mutex
	projectByDirCache = map[string]CoveringProjectResult{}
)

// CoveringProjectCached is coveringProjectCached in the TS source.
func CoveringProjectCached(entryPath string) CoveringProjectResult {
	resolved, err := filepath.Abs(entryPath)
	if err != nil {
		resolved = entryPath
	}
	dir := filepath.Dir(resolved)
	projectByDirMu.Lock()
	if held, ok := projectByDirCache[dir]; ok {
		projectByDirMu.Unlock()
		return held
	}
	projectByDirMu.Unlock()
	found := CoveringProject(entryPath)
	projectByDirMu.Lock()
	projectByDirCache[dir] = found
	projectByDirMu.Unlock()
	return found
}

// adoptedOptions is adoptedOptions in the TS source: the rows adopted
// from a covering project's options, over ours. core.CompilerOptions
// has no per-field "was this stated" flag the way ts.CompilerOptions'
// `undefined` does — Tristate's TSUnknown zero value stands in for
// "not stated" on every Tristate row ported here, mirroring the TS
// `!== undefined` guards exactly (a project that never mentions
// `strict` leaves TSUnknown, so ours is kept, same as the TS spread).
func adoptedOptions(project *core.CompilerOptions) *core.CompilerOptions {
	adopted := *Options()
	if project.ModuleResolution != core.ModuleResolutionKindUnknown {
		adopted.ModuleResolution = project.ModuleResolution
	}
	if project.Module != core.ModuleKindNone {
		adopted.Module = project.Module
	}
	if project.BaseUrl != "" {
		adopted.BaseUrl = project.BaseUrl
	}
	if project.Paths != nil {
		adopted.Paths = project.Paths
		adopted.PathsBasePath = project.PathsBasePath
	}
	if project.Jsx != core.JsxEmitNone {
		adopted.Jsx = project.Jsx
	}
	if project.JsxImportSource != "" {
		adopted.JsxImportSource = project.JsxImportSource
	}
	if project.AllowJs != core.TSUnknown {
		adopted.AllowJs = project.AllowJs
	}
	if project.ResolveJsonModule != core.TSUnknown {
		adopted.ResolveJsonModule = project.ResolveJsonModule
	}
	if project.ESModuleInterop != core.TSUnknown {
		adopted.ESModuleInterop = project.ESModuleInterop
	}
	if project.AllowSyntheticDefaultImports != core.TSUnknown {
		adopted.AllowSyntheticDefaultImports = project.AllowSyntheticDefaultImports
	}
	// the STRICTNESS rows a covering project states are its own shape
	// contract — the shape channel mirrors the repo's tsc, not ours
	if project.Strict != core.TSUnknown {
		adopted.Strict = project.Strict
	}
	if project.NoImplicitAny != core.TSUnknown {
		adopted.NoImplicitAny = project.NoImplicitAny
	}
	if project.StrictNullChecks != core.TSUnknown {
		adopted.StrictNullChecks = project.StrictNullChecks
	}
	if project.StrictPropertyInitialization != core.TSUnknown {
		adopted.StrictPropertyInitialization = project.StrictPropertyInitialization
	}
	if project.UseUnknownInCatchVariables != core.TSUnknown {
		adopted.UseUnknownInCatchVariables = project.UseUnknownInCatchVariables
	}
	// noUncheckedIndexedAccess decides what GetTypeAtLocation answers for
	// EVERY indexed read: `T` with the flag off, `T | undefined` with it
	// on. Dropping it made a project that states the flag get analyzed
	// with it off — arr[i] read back as a plain object, so a KindObject
	// receiver was unconditionally truthy, `obj == undefined` decided
	// false, and if_statement fired 7001 "provably false" against the
	// very guards tsc REQUIRES the author to write. Sixteen such fires on
	// recharts were each proved wrong with a witness.
	if project.NoUncheckedIndexedAccess != core.TSUnknown {
		adopted.NoUncheckedIndexedAccess = project.NoUncheckedIndexedAccess
	}
	// exactOptionalPropertyTypes changes an optional property's type:
	// with it off `{ a?: string }` reads as `string | undefined`, with it
	// on the undefined is NOT admitted by an assignment, and the read of
	// a missing key is still absent. It moves what the checker hands us
	// for every optional key, so the shape channel has to see the
	// project's own answer rather than ours.
	if project.ExactOptionalPropertyTypes != core.TSUnknown {
		adopted.ExactOptionalPropertyTypes = project.ExactOptionalPropertyTypes
	}
	// strictFunctionTypes decides whether a parameter position compares
	// contravariantly, which changes which call signatures a value's type
	// admits — a type we read off a callback-bearing value differs
	// between the two settings.
	if project.StrictFunctionTypes != core.TSUnknown {
		adopted.StrictFunctionTypes = project.StrictFunctionTypes
	}
	// strictBindCallApply decides whether bind/call/apply answer their
	// precise signature or the loose `any`-shaped one — a read through
	// any of the three gets a different type per setting.
	if project.StrictBindCallApply != core.TSUnknown {
		adopted.StrictBindCallApply = project.StrictBindCallApply
	}
	// strictBuiltinIteratorReturn decides whether a built-in iterator's
	// `next()` answers `IteratorResult<T, undefined>` or `<T, any>` — the
	// value read out of an iteration differs between the two.
	if project.StrictBuiltinIteratorReturn != core.TSUnknown {
		adopted.StrictBuiltinIteratorReturn = project.StrictBuiltinIteratorReturn
	}
	// noImplicitThis decides whether an unannotated `this` reads as `any`
	// or is an error position — it changes the type of every `this` the
	// walk reads inside a plain function.
	if project.NoImplicitThis != core.TSUnknown {
		adopted.NoImplicitThis = project.NoImplicitThis
	}
	// useDefineForClassFields decides whether a declared-but-unassigned
	// class field exists as own-property `undefined` at construction or
	// is absent — the field's value differs between the two, and the
	// class-field invariants read that value.
	if project.UseDefineForClassFields != core.TSUnknown {
		adopted.UseDefineForClassFields = project.UseDefineForClassFields
	}
	return &adopted
}
