// from service/program_disk_host.ts
//
// Disk-backed program construction: the real-filesystem compiler
// host, and the held-program cache keyed by mtimes.
//
// The tsgo-oracle branches (tsgoCheckerHost/TsgoOracle, oracleFor,
// hostFor's bits/oracle switch, parseOnlyBitsOf/parseOnlyProgram,
// setTsgoOracle) have NO Go twin per PORT.md's instruction: this
// tree's checker IS tsgo's, in-process — there is no second,
// out-of-process type oracle to hand a program off to, so
// builtProgram always takes the real-ts.Program path the TS source's
// own "missing binary" fallback already describes as sound. diskHost's
// shared-parse-tree caching (the default library, the checker's own
// surface files) has no twin either: a Go *compiler.Program is built
// fresh per call from a real vfs.FS, and the FS layer itself already
// caches file reads at the OS level — there is no ts.SourceFile parse
// tree to share across builds the way the TS host shares them.
// wrapChecker (tracing.ts) is also skipped: this tree's tracing seam
// (internal/refinedts/tracing) wraps at call sites, not at the
// checker construction boundary.

package service

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// mtimeOf is the TS source's mtimeOf: a file's modification time in
// milliseconds, or -1 when it cannot be read.
func mtimeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.ModTime().UnixMilli()
}

// HeldProgram is the TS source's HeldProgram interface: rechecking an
// UNCHANGED file should feel like the editor. The held program is
// keyed by entry path and validated by the mtimes of every user file
// it read — any touched file rebuilds.
type HeldProgram struct {
	Built  *program.CheckerProgram
	Stamps map[string]int64
}

// programCacheCapacity is PROGRAM_CACHE_CAPACITY in the TS source:
// cache_tuning.ts's PROJECTS_KEPT (landed alongside this file as
// service.ProjectsKept, same package — no import needed).
const programCacheCapacity = ProjectsKept

var (
	programCacheMu    sync.Mutex
	programCache      = map[string]*HeldProgram{}
	programCacheOrder []string // insertion order, oldest first — LRU eviction
)

// StampsOf is stampsOf in the TS source: the mtime of every non-
// declaration file the program read.
func StampsOf(p *compiler.Program) map[string]int64 {
	stamps := map[string]int64{}
	for _, file := range p.SourceFiles() {
		if file.IsDeclarationFile {
			continue
		}
		stamps[file.FileName()] = mtimeOf(file.FileName())
	}
	return stamps
}

// diskHost is diskHost in the TS source, narrowed: the real OS
// filesystem, wrapped for the bundled default-library files. No
// shared-parse caching layer (see file header).
func diskHost(options *core.CompilerOptions) compiler.CompilerHost {
	return compiler.NewCompilerHost("/", bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
}

// BuiltProgram is builtProgram in the TS source, narrowed to the
// real-ts.Program path (the tsgo-oracle parse-only path has no Go
// twin — see file header): a real *compiler.Program built from the
// first entry's covering project options.
func BuiltProgram(entryPaths []string) *compiler.Program {
	first := entryPaths[0]
	options := OptionsFor(first)
	host := diskHost(options)
	config := tsoptions.NewParsedCommandLine(options, fileNamesOf(entryPaths), tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: true,
		CurrentDirectory:          "/",
	})
	p := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
	p.BindSourceFiles()
	return p
}

// fileNamesOf resolves every entry to an absolute path — the same
// normalization CoveringProject applies, so the built program's file
// set matches what OptionsFor resolved against.
func fileNamesOf(entryPaths []string) []string {
	out := make([]string, len(entryPaths))
	for i, entryPath := range entryPaths {
		resolved, err := filepath.Abs(entryPath)
		if err != nil {
			resolved = entryPath
		}
		out[i] = resolved
	}
	return out
}

// StillCurrent is stillCurrent in the TS source: tsc's answers cannot
// go stale on an unchanged program — they are functions of the very
// files the stamp covers.
func StillCurrent(held *HeldProgram) bool {
	for path, stamp := range held.Stamps {
		if mtimeOf(path) != stamp {
			return false
		}
	}
	return true
}

// CachedProgram is cachedProgram in the TS source.
func CachedProgram(entryPath string) (*HeldProgram, bool) {
	programCacheMu.Lock()
	defer programCacheMu.Unlock()
	held, ok := programCache[entryPath]
	return held, ok
}

// RememberProgram is rememberProgram in the TS source: an LRU cache
// capped at programCacheCapacity, oldest entry evicted first.
func RememberProgram(entryPath string, held *HeldProgram) {
	programCacheMu.Lock()
	defer programCacheMu.Unlock()
	if _, exists := programCache[entryPath]; !exists && len(programCache) >= programCacheCapacity {
		if len(programCacheOrder) > 0 {
			oldest := programCacheOrder[0]
			programCacheOrder = programCacheOrder[1:]
			delete(programCache, oldest)
		}
	}
	if _, exists := programCache[entryPath]; !exists {
		programCacheOrder = append(programCacheOrder, entryPath)
	}
	programCache[entryPath] = held
}

// TouchCachedProgram is touchCachedProgram in the TS source: LRU
// refresh — delete then re-set, so the entry moves to the back of the
// eviction order.
func TouchCachedProgram(entryPath string, held *HeldProgram) {
	programCacheMu.Lock()
	defer programCacheMu.Unlock()
	if _, exists := programCache[entryPath]; exists {
		for i, path := range programCacheOrder {
			if path == entryPath {
				programCacheOrder = append(programCacheOrder[:i], programCacheOrder[i+1:]...)
				break
			}
		}
		programCacheOrder = append(programCacheOrder, entryPath)
	}
	programCache[entryPath] = held
}

// ClearDiskProgramCache is clearDiskProgramCache in the TS source:
// bench seam, forget the held programs so a profile can measure the
// fresh-build regime. Never called by the checker itself.
func ClearDiskProgramCache() {
	programCacheMu.Lock()
	defer programCacheMu.Unlock()
	programCache = map[string]*HeldProgram{}
	programCacheOrder = nil
}
