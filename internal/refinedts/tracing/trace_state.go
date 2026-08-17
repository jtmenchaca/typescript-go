// Mutable tracing records: flat self-time, the span stack, the bounded
// tree, per-file costs, and pre-trace notes. Enter/Leave own the
// recursion and tree bookkeeping so the recording API stays thin.
//
// Ported 1:1 from service/trace_state.ts. The records are globals with
// no lock, exactly as the TS module's are: correct while one check is
// in flight at a time (the sweep's discipline; a sharded run gives
// each process its own globals). A goroutine-parallel walk inside one
// process would need this revisited — that is a design note, not a
// silent assumption.

package tracing

import (
	"sync"
	"sync/atomic"
	"time"
)

// Grain names how fine a span records: "phase" | "step" | "node"
// (cache_tuning.ts's Grain; the tuning module lives in service/ and
// is not ported yet, so the two TRACE defaults are inlined below with
// a comment at each).
type Grain string

const (
	GrainPhase Grain = "phase"
	GrainStep  Grain = "step"
	GrainNode  Grain = "node"
)

type TraceSpan struct {
	Name     string
	TotalMs  float64
	Children []*TraceSpan
}

type TraceCounter struct {
	Calls   int64
	TotalMs float64
}

// TraceEntry is one name's share of the run.
type TraceEntry struct {
	Name  string
	Grain Grain
	Calls int64
	// TotalMs is time inside this span and everything it called.
	// Counted once per outermost entry, so recursion does not
	// multiply it.
	TotalMs float64
	// SelfMs is time inside this span and NOT inside any span it
	// called. The column that attributes a run.
	SelfMs float64
}

var GrainLevel = map[Grain]int{
	GrainPhase: 1,
	GrainStep:  2,
	GrainNode:  3,
}

// A tree deeper than this stops opening nodes and folds into its
// deepest ancestor. The flat record is unaffected.
const treeDepthCap = 16

// TRACE.enabled / TRACE.grain (cache_tuning.ts): off by default, at
// the step grain when turned on — the same resting state the TS
// tuning file ships.
//
// Both are atomics: Span/Count/Clock/Recording on every walk
// goroutine read them first, before touching anything else, so the
// off path (Enabled false) must be race-free without taking the
// records mutex below — a single atomic load per call, same as the
// single bool read the TS source pays.
var (
	enabledFlag atomic.Bool
	levelValue  atomic.Int64
)

func init() {
	levelValue.Store(int64(GrainLevel[GrainStep]))
}

func SetEnabled(value bool) { enabledFlag.Store(value) }
func SetLevel(value int)    { levelValue.Store(int64(value)) }

// IsEnabled and CurrentLevel are the atomic reads every hot call site
// uses instead of the old plain Enabled/Level vars.
func IsEnabled() bool  { return enabledFlag.Load() }
func CurrentLevel() int { return int(levelValue.Load()) }

// recordsMu guards every mutable record below (the flat map, the
// active-frame counts, the counters, the span stack, the tree, the
// per-file costs, the pre-trace notes, and the run clock/reads). One
// lock rather than one per field because Enter/Leave/Count each touch
// several of these together as a single bookkeeping step (e.g. Enter
// pushes the stack AND may open a tree node AND bumps Active in one
// call) — splitting the lock would let those steps interleave with a
// concurrent goroutine's and tear the bookkeeping. Held only on the
// recording path; the off path (Enabled false) never reaches it, so
// it costs nothing when tracing is off.
var recordsMu sync.Mutex

/* ── the flat record ─────────────────────────────────────────────── */

var Flat = map[string]*TraceEntry{}

var Counters = map[string]*TraceCounter{}

type Frame struct {
	Name      string
	StartedAt time.Time
	// ChildMs is time this frame's children took, so self time is a
	// subtraction.
	ChildMs   float64
	Reentrant bool
	TreeHeld  *TraceSpan
	TreeDepth int
	// scope is the goroutine-owned stack this frame was pushed onto, so
	// Leave credits its elapsed to the frame that actually contained it
	// rather than to whatever another goroutine had open (span_scope.go).
	scope *spanScope
}

/* ── the tree record ─────────────────────────────────────────────── */

// Root is the tree's shared root. The CURSOR into the tree is
// per-scope (spanScope.treeCurrent) rather than global — two workers
// walking at once are at two different places in the tree, and one
// shared cursor made each worker's Enter reparent the other's nodes.
var Root *TraceSpan

// SetRoot is called by TraceStop, outside Enter/Leave's own locking,
// so it takes recordsMu itself.
func SetRoot(value *TraceSpan) {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	Root = value
}

// GetRoot reads Root under the same lock Enter/Leave/ResetRecords use
// to write it — TraceStop's read-then-clear needs both under one
// critical section so no Enter/Leave lands between them.
func GetRoot() *TraceSpan {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	return Root
}

// A tree node per (parent, name), created once and reused — the tree
// aggregates repeats rather than growing a node per call. (TS holds
// this in a WeakMap; the tree is reset-bounded here, so a plain map
// keyed on the parent pointer carries the same lifetime.)
var treeChildren = map[*TraceSpan]map[string]*TraceSpan{}

/* ── per-file record ─────────────────────────────────────────────── */

var FileMs = map[string]float64{}

// FileOrder holds each file's cost in the order the batch met them,
// so the report can say whether per-file cost grows as the batch
// goes on.
type FileCost struct {
	Path string
	Ms   float64
}

var FileOrder []FileCost

/* ── before the traced window ────────────────────────────────────── */

// PreTraceNotes are named segments that ran BEFORE TraceStart —
// runtime boot, module imports, batch setup. Not cleared by reset:
// noted before tracing.
type PreTraceNote struct {
	Name string
	Ms   float64
}

var PreTraceNotes []PreTraceNote

/* ── run-level bookkeeping ───────────────────────────────────────── */

var RunStartedAt time.Time

// ClockReads counts clock reads taken, so the report can price its
// own overhead.
var ClockReads int64

// ScopeReads counts goroutine-identity reads — one per Enter, to find
// which goroutine's span stack the frame belongs on. Priced separately
// from ClockReads in the report because it costs ~100x a clock read.
var ScopeReads int64

// negativeSelf counts frames whose elapsed came out below their own
// children's — impossible once child time is only ever charged within
// one goroutine's stack. The report prints it when non-zero, so the
// accounting says when it has broken rather than quietly flooring.
var negativeSelf atomic.Int64

// NegativeSelfCount is how many frames closed with children longer than
// themselves. Zero on a sound run.
func NegativeSelfCount() int64 { return negativeSelf.Load() }

// AddClockReads takes recordsMu itself: Clock() in tracing.go calls it
// standalone, not from inside an Enter/Leave critical section.
func AddClockReads(n int64) {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	ClockReads += n
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t)) / float64(time.Millisecond)
}

// Enter pushes a frame onto THIS goroutine's own span stack. The
// nesting bookkeeping (which frame is my parent, is a frame of my name
// already open above me, where am I in the tree) is per-goroutine, so
// it lives in spanScope; only the shared records — ClockReads, the tree
// nodes — take recordsMu.
//
// Enter finds its scope by goroutine identity, which costs ~3 us here
// against the ~30 ns of bookkeeping around it. That is paid on purpose:
// a shared stack attributed child time across workers, which is what
// printed negative self time, and a table nobody can trust is worth
// less than 1.7% of a traced wall (the measured share at this corpus's
// span counts). Leave pays nothing — it reads the scope back off the
// frame. ScopeReads carries the count so the report prices it beside
// the clock reads.
func Enter(name string, grain Grain) *Frame {
	scope := currentScope()

	recordsMu.Lock()
	ClockReads++
	ScopeReads++
	recordsMu.Unlock()

	frame := scope.enterScope(name, grain)
	frame.StartedAt = time.Now()
	return frame
}

// Leave closes a frame against the scope it was opened on. Self time
// is elapsed minus the time this frame's OWN children took, which is
// non-negative by construction: leaveScope only ever credits a child's
// elapsed to the frame directly beneath it on the same goroutine's
// stack, and a child's elapsed is bounded by its parent's because the
// parent's clock started first and stops later.
func Leave(frame *Frame, grain Grain) {
	elapsed := msSince(frame.StartedAt)
	scope := frame.scope
	if scope == nil {
		scope = currentScope()
	}
	scope.leaveScope(frame, elapsed)

	// Self time is non-negative BY CONSTRUCTION now: a frame is only
	// ever charged child time by frames directly above it on its own
	// goroutine's stack, and those ran strictly inside it. The floor is
	// kept as an assertion rather than a repair — NegativeSelf counts
	// any time it fires, and a non-zero count in the report means the
	// nesting model is wrong again, not that a row needed rounding.
	self := elapsed - frame.ChildMs
	if self < 0 {
		self = 0
		negativeSelf.Add(1)
	}

	recordsMu.Lock()
	defer recordsMu.Unlock()
	ClockReads++
	entry := Flat[frame.Name]
	if entry == nil {
		entry = &TraceEntry{Name: frame.Name, Grain: grain}
		Flat[frame.Name] = entry
	}
	entry.Calls++
	entry.SelfMs += self
	// inclusive time counts the OUTERMOST entry only, so a recursive
	// span does not multiply its own total
	if !frame.Reentrant {
		entry.TotalMs += elapsed
	}
}

// ResetRecords clears flat/tree/file counters and opens a fresh root.
// Leaves PreTraceNotes alone — those document time before tracing.
// Shared record maps clear under recordsMu; FileDetails and the
// summary-outcome tally have their own locks and clear after.
func ResetRecords() {
	recordsMu.Lock()
	Flat = map[string]*TraceEntry{}
	Counters = map[string]*TraceCounter{}
	FileMs = map[string]float64{}
	FileOrder = FileOrder[:0]
	ClockReads = 0
	ScopeReads = 0
	negativeSelf.Store(0)
	treeChildren = map[*TraceSpan]map[string]*TraceSpan{}
	Root = &TraceSpan{Name: "check"}
	RunStartedAt = time.Now()
	recordsMu.Unlock()
	// the span stacks and tree cursors are per-goroutine, so a fresh
	// trace drops them rather than rewinding one shared stack
	resetScopes()
	ClearFileDetails()
	ClearSummaryOutcomes()
}