// from service/check.ts (incremental_file_cache.ts's factsCache half)
//
// programFactsCached: every reachable file's facts, merged in
// reachableFiles' import order, with reporting (emptiness
// diagnostics) only for the entry — plus sweepFactsStore, the
// caller-held cache that lets CheckFiles' many goroutines share one
// compile per shared file.

package service

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// programFactsCached is programFacts in the TS source: every
// reachable file's facts, merged in reachableFiles' import order,
// with reporting (emptiness diagnostics) only for the entry.
type programFactsResult struct {
	registry         annotations.AnnotationRegistry
	objects          annotations.ObjectRegistry
	contracts        map[*ast.Symbol]*walk.FunctionContract
	entryDiagnostics []assignability.RefinementDiagnostic
}

// The `cache` parameter is incremental_file_cache.ts's factsCache
// made caller-held: the sweep's lifetime stands in for the WeakMap's
// GC lifetime (the caller drops the map when the sweep ends), and nil
// is the uncached single-check regime. The TS validity rule carries
// over whole: a hit must hold diagnostics when the file IS the entry,
// and every imported interface must still MEAN the same thing; an
// entry recompile serves diagnostics, not a new interface — the cache
// keeps the first compile's facts and hash, the fresh diagnostics
// ride along (the anti-cascade rule the TS source traced to its
// root).
func programFactsCached(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, cache *sweepFactsStore) programFactsResult {
	merged := walk.FileFactsMerged{
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*walk.FunctionContract{},
	}
	currentHash := map[string]string{}
	var entryDiagnostics []assignability.RefinementDiagnostic

	files := annotations.ReachableFiles(p)
files:
	for _, file := range files {
		reporting := file == p.Entry
		// useHeld merges a valid cached row; validHeld applies the TS
		// validity rule (an entry needs its own diagnostics, and every
		// imported interface must still mean the same thing)
		useHeld := func(held *walk.FileFacts) {
			for symbol, a := range held.Annotations {
				merged.Registry[symbol] = a
			}
			for symbol, o := range held.Objects {
				merged.Objects[symbol] = o
			}
			for symbol, c := range held.Contracts {
				merged.Contracts[symbol] = c
			}
			currentHash[file.FileName()] = held.InterfaceHash
			if reporting {
				entryDiagnostics = held.Diagnostics
			}
		}
		validHeld := func() *walk.FileFacts {
			if cache == nil {
				return nil
			}
			held := cache.get(p.Checker, file)
			valid := held != nil && (!reporting || held.HasDiagnostics)
			if valid {
				for name, hash := range held.ImportHashes {
					if currentHash[name] != hash {
						valid = false
						break
					}
				}
			}
			if !valid {
				return nil
			}
			return held
		}
		if h := validHeld(); h != nil {
			useHeld(h)
			continue
		}
		// one compile per shared file at a time: a loser waits for the
		// winner's put, re-validates, and only compiles itself if its
		// own rule (entry diagnostics) still demands it
		claimed := false
		if cache != nil {
			for {
				ch, winner := cache.claim(p.Checker, file)
				if winner {
					claimed = true
					if h := validHeld(); h != nil {
						cache.finish(p.Checker, file)
						useHeld(h)
						continue files
					}
					break
				}
				<-ch
				if h := validHeld(); h != nil {
					useHeld(h)
					continue files
				}
			}
		}
		var held *walk.FileFacts
		if cache != nil {
			held = cache.get(p.Checker, file)
		}
		importHashes := map[string]string{}
		for _, imported := range annotations.ImportedUserFiles(p, file) {
			if hash, ok := currentHash[imported.FileName()]; ok {
				importHashes[imported.FileName()] = hash
			}
		}
		var facts walk.FileFacts
		func() {
			if claimed {
				// released even if a refused kernel question panics out —
				// a waiter must never sleep on a dead claim
				defer cache.finish(p.Checker, file)
			}
			facts = walk.CompileFileFacts(p, file, merged, kernel, reporting, importHashes)
			if cache != nil {
				// the TS miss-classification precedence: an already-cached
				// file recompiled AS the entry keeps its first hash and
				// facts (even over a hash mismatch — TS classifies
				// entryDiagnostics before hashMismatch); everything else
				// caches the fresh compile whole
				if held != nil && reporting && !held.HasDiagnostics {
					updated := *held
					updated.Diagnostics = facts.Diagnostics
					updated.HasDiagnostics = facts.HasDiagnostics
					cache.put(p.Checker, file, &updated)
					currentHash[file.FileName()] = held.InterfaceHash
				} else {
					fresh := facts
					cache.put(p.Checker, file, &fresh)
					currentHash[file.FileName()] = facts.InterfaceHash
				}
			} else {
				currentHash[file.FileName()] = facts.InterfaceHash
			}
		}()
		for symbol, a := range facts.Annotations {
			merged.Registry[symbol] = a
		}
		for symbol, o := range facts.Objects {
			merged.Objects[symbol] = o
		}
		for symbol, c := range facts.Contracts {
			merged.Contracts[symbol] = c
		}
		if reporting {
			entryDiagnostics = facts.Diagnostics
		}
	}

	return programFactsResult{
		registry:         merged.Registry,
		objects:          merged.Objects,
		contracts:        merged.Contracts,
		entryDiagnostics: entryDiagnostics,
	}
}

// sweepFactsStore is incremental_file_cache.ts's factsCache made
// sweep-shared: CheckFiles' entries compile and merge facts from many
// goroutines at once, so lookups and inserts lock. Compiles happen
// OUTSIDE the lock; a claim/await pair keeps N workers from compiling
// the SAME shared file at once (a heavy shared file costs ~50 ms per
// compile — measured duplicated across most workers before the claim
// existed). A waiter re-checks validity itself: an entry that needs
// its own diagnostics may still recompile the file it waited on.
// sweepFactsKey: facts are computed THROUGH a checker instance (the
// annotation/contract compile asks it), so a row computed under one
// worker's checker is that checker's own — serving it to another
// worker's entry let one file's presence silently change another
// file's judgment (measured: a three-entry batch missed a designated
// error the same entry reported alone). The checker in the key is the
// same discipline every walk memo now keeps. Now that CheckFiles hands
// every entry its own freshly built checker (never shared with another
// entry), this key naturally holds exactly one row per (fresh checker,
// its file) — a shared support file's facts recompile once per entry
// that reaches it rather than once per sweep. That is intended:
// correctness over cross-entry reuse.
type sweepFactsKey struct {
	checker *checker.Checker
	file    *ast.SourceFile
}

type sweepFactsStore struct {
	mu       sync.Mutex
	held     map[sweepFactsKey]*walk.FileFacts
	inFlight map[sweepFactsKey]chan struct{}
}

func (s *sweepFactsStore) get(c *checker.Checker, file *ast.SourceFile) *walk.FileFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[sweepFactsKey{checker: c, file: file}]
}

func (s *sweepFactsStore) put(c *checker.Checker, file *ast.SourceFile, facts *walk.FileFacts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[sweepFactsKey{checker: c, file: file}] = facts
}

// claim answers (nil, true) when the caller should compile `file` and
// then call finish, or (ch, false) when another worker is compiling —
// the caller waits on ch and re-reads the store.
func (s *sweepFactsStore) claim(c *checker.Checker, file *ast.SourceFile) (chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight == nil {
		s.inFlight = map[sweepFactsKey]chan struct{}{}
	}
	key := sweepFactsKey{checker: c, file: file}
	if ch, busy := s.inFlight[key]; busy {
		return ch, false
	}
	s.inFlight[key] = make(chan struct{})
	return nil, true
}

// finish releases a claim, waking every waiter.
func (s *sweepFactsStore) finish(c *checker.Checker, file *ast.SourceFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sweepFactsKey{checker: c, file: file}
	if ch, held := s.inFlight[key]; held {
		close(ch)
		delete(s.inFlight, key)
	}
}
