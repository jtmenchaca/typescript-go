// The per-goroutine span stack.
//
// Self time is total minus children, so every millisecond a Leave
// credits as "child time" has to land on the frame that actually
// contained it. One shared stack cannot do that under CheckFiles'
// goroutine-per-entry sweep: Enter appends and Leave pops the TOP of
// the stack, not its own frame, so worker A's Leave pops worker B's
// frame and credits A's elapsed to whatever frame B happened to have
// open. A cheap, frequent span (narrowings) that is charged a heavy
// sibling's elapsed as child time reports elapsed - childMs far below
// zero — the negative self time the report showed.
//
// The stack is per-goroutine here instead. The goroutine identity is
// read ONCE per scope (an entry file's whole walk), never per span:
// runtime.Stack costs ~3.2 us on this machine against the ~30 ns the
// mutex-and-map bookkeeping in Enter/Leave costs, so paying it per
// span would cost more than the work being measured. Enter reaches its
// scope through the frame it returns, and Leave reads it back off the
// frame it is handed — the pairing Span's own defer already
// guarantees.
//
// A span opened outside any scope (a single CheckFile, a test, the
// editor path) lands in the fallback scope, which behaves exactly as
// the old single stack did — correct, because nothing else is walking
// beside it.

package tracing

import (
	"sync"
	"time"
)

// spanScope is one goroutine's own span stack and tree cursor. Owned by
// the goroutine that opened it for as long as that goroutine walks, so
// its fields need no lock of their own: Enter and Leave are the only
// writers, and they run on the owning goroutine.
//
// Active is per-scope too. It is the recursion guard for inclusive
// time — "is a frame of this name already open ABOVE me" — which is a
// question about one goroutine's own stack. Shared across workers it
// answered for the sweep instead: worker B entering analyzeFunction
// while worker A was inside one read as reentrant, so B's elapsed was
// dropped from TotalMs entirely. That is why analyzeFunction reported
// 109981 ms of self time against 238.2 ms of total.
type spanScope struct {
	stack  []*Frame
	active map[string]int

	treeCurrent *TraceSpan
	treeDepth   int

	// startedAt is when this scope opened, so its own wall can join the
	// worker window the self-time column divides up.
	startedAt time.Time
}

// fallbackScope carries spans opened outside any BeginSpanScope — a
// single-file check, a test, the editor path. Guarded because two
// unscoped goroutines could in principle reach it at once; a sweep
// never does, since every worker opens its own scope.
var (
	fallbackMu    sync.Mutex
	fallbackScope = &spanScope{active: map[string]int{}}
)

// scopeOfGoroutine holds the scope a goroutine opened, keyed by
// goroutine id. Enter reads it to find which stack its frame belongs
// on; Leave reads the scope back off the frame instead, so the lookup
// is paid once per span rather than twice.
var scopeOfGoroutine sync.Map // goid (uint64) -> *spanScope

// workerMs accumulates every closed scope's own wall. Self time is
// divided out of THIS window, not out of the process wall: N workers
// walking at once produce N ms of span time per ms of wall, so a
// percentage taken against the wall exceeds 100 by exactly the worker
// count. Guarded by recordsMu with the rest of the shared records.
var workerMs float64

// BeginSpanScope opens this goroutine's own span stack and returns the
// closer. CheckFiles' per-entry worker calls it around the entry's
// walk, beside BindFileDetail, so each worker's spans nest against
// their own frames rather than against whatever another worker had
// open.
//
// Identity when tracing is off: no scope is created and the returned
// closer does nothing, so the off path costs one atomic load.
func BeginSpanScope() func() {
	if !IsEnabled() {
		return func() {}
	}
	id := goroutineID()
	scope := &spanScope{active: map[string]int{}, startedAt: time.Now()}
	recordsMu.Lock()
	scope.treeCurrent = Root
	recordsMu.Unlock()
	scopeOfGoroutine.Store(id, scope)
	return func() {
		scopeOfGoroutine.Delete(id)
		elapsed := float64(time.Since(scope.startedAt)) / float64(time.Millisecond)
		recordsMu.Lock()
		workerMs += elapsed
		recordsMu.Unlock()
	}
}

// currentScope is this goroutine's scope, or the shared fallback. Only
// reached from an Enter with no parent frame — a scope's outermost
// span — so the goid read here is per phase, not per span.
func currentScope() *spanScope {
	if held, ok := scopeOfGoroutine.Load(goroutineID()); ok {
		return held.(*spanScope)
	}
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	return fallbackScope
}

// enterScope pushes a frame and returns it. The scope is carried ON the
// frame so Leave never has to look it up.
func (s *spanScope) enterScope(name string, grain Grain) *Frame {
	already := s.active[name]
	s.active[name] = already + 1

	var treeHeld *TraceSpan
	if already == 0 && grain != GrainNode && s.treeCurrent != nil &&
		s.treeDepth < treeDepthCap {
		// The tree is shared record state — its nodes are reachable from
		// Root, which the report reads — so opening a node takes
		// recordsMu even though the CURSOR is per-scope.
		recordsMu.Lock()
		children := treeChildren[s.treeCurrent]
		if children == nil {
			children = map[string]*TraceSpan{}
			treeChildren[s.treeCurrent] = children
		}
		mine := children[name]
		if mine == nil {
			mine = &TraceSpan{Name: name}
			children[name] = mine
			s.treeCurrent.Children = append(s.treeCurrent.Children, mine)
		}
		recordsMu.Unlock()
		treeHeld = s.treeCurrent
		s.treeCurrent = mine
		s.treeDepth++
	}

	frame := &Frame{
		Name:      name,
		Reentrant: already > 0,
		TreeHeld:  treeHeld,
		TreeDepth: s.treeDepth,
		scope:     s,
	}
	s.stack = append(s.stack, frame)
	return frame
}

// leaveScope pops the frame and credits its elapsed to its PARENT —
// the frame directly beneath it on this scope's own stack, which is
// the frame that actually contained the time.
//
// Popping by identity rather than by position: a frame that is not on
// top means a Leave arrived out of order (a span left across a scope
// boundary). Rather than corrupting every enclosing frame's child time,
// the out-of-order frame is removed where it sits and the frames above
// it are left alone — each still closes against its own parent.
func (s *spanScope) leaveScope(frame *Frame, elapsed float64) {
	at := len(s.stack) - 1
	for at >= 0 && s.stack[at] != frame {
		at--
	}
	if at < 0 {
		// Not on this scope's stack at all — a frame whose Enter ran
		// under a different scope. Its own self time is still its own,
		// but there is no parent here to charge, so nothing is charged.
		return
	}
	s.stack = append(s.stack[:at], s.stack[at+1:]...)
	if at > 0 {
		s.stack[at-1].ChildMs += elapsed
	}

	remaining := s.active[frame.Name] - 1
	if remaining <= 0 {
		delete(s.active, frame.Name)
	} else {
		s.active[frame.Name] = remaining
	}

	if frame.TreeHeld != nil && s.treeCurrent != nil {
		recordsMu.Lock()
		s.treeCurrent.TotalMs += elapsed
		recordsMu.Unlock()
		s.treeCurrent = frame.TreeHeld
		s.treeDepth--
	}
}

// closeScopes drops every open scope and re-seats the fallback at the
// current root. Called by TraceStop, so a later trace never opens tree
// nodes under a tree the stopped one already handed back. The worker
// window survives: the report is composed after TraceStop and divides
// the self-time column out of it.
func closeScopes() {
	scopeOfGoroutine.Range(func(key, _ any) bool {
		scopeOfGoroutine.Delete(key)
		return true
	})
	recordsMu.Lock()
	root := Root
	recordsMu.Unlock()
	fallbackMu.Lock()
	fallbackScope = &spanScope{
		active:      map[string]int{},
		treeCurrent: root,
		startedAt:   time.Now(),
	}
	fallbackMu.Unlock()
}

// resetScopes is closeScopes plus the worker window, for the start of a
// fresh trace: ResetRecords clears every other record here too, so the
// new run's percentages are its own.
func resetScopes() {
	closeScopes()
	recordsMu.Lock()
	workerMs = 0
	recordsMu.Unlock()
}
