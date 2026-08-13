// Per-entry file detail for parallel sweeps.
//
// The flat SELF-TIME table mixes every entry's spans when CheckFiles
// walks many files at once — useful for "what mechanisms cost," not
// for "why is this file slow." FileDetail is the per-entry answer:
// each goroutine accumulates into its own struct (no shared mutation
// while walking), then EndFileDetail publishes it under recordsMu.
//
// Phases and contract rows are timed with time.Since at the call
// sites that already own the work — not nested Spans — so the numbers
// stay meaningful under concurrency and do not inflate the hot path
// the way Span-on-every-node would.

package tracing

import (
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// FileDetail is one entry file's refinement cost, broken into the
// phases a sweep actually runs and the slowest contracts inside
// pass3.contractBodies.
type FileDetail struct {
	Path string

	// WallMs is TraceFile's whole-entry wall (shape diagnostics are
	// outside runRefinements; this is refinement wall for the entry).
	WallMs float64

	FactsMs  float64
	ObjectMs float64
	TopMs    float64
	BodiesMs float64
	FlushMs  float64

	Contracts       int
	CallSiteJoinMs  float64
	AnalyzeFunction float64 // sum of AnalyzeFunction walls inside bodies

	// mechanism counts local to this entry (not global Counters —
	// those mix files under a parallel sweep).
	CheckAssignability int64
	InlineContractCall int64
	KernelAsk          int64
	KernelCacheHit     int64
	Narrowings         int64
	EffectScan         int64

	// Call-site join internals — which half of CallSiteBindings costs.
	JoinDeclared       int64 // declaredJoin (named FunctionDeclaration)
	JoinCallback       int64 // callbackSitePins (arrow / expression)
	JoinReachSnapshot  int64 // reach()/callback used a recorded snapshot
	JoinReachFallback  int64 // reach()/callback paid AnalyzeToToken
	JoinMemoHit        int64 // declaredJoin memo hit
	JoinMemoMiss       int64 // declaredJoin computed fresh

	// Summaries is this entry's covered-ness tally: one outcome per
	// contracted body, with the first havocked construct or decline
	// reason named. Filled through NoteSummaryOutcome
	// (summary_outcomes.go), on the goroutine that walks the entry.
	Summaries SummaryOutcomeCounts

	// SlowContracts are the costliest AnalyzeFunction calls in this
	// file, kept ranked as they arrive.
	SlowContracts []ContractCost
}

// ContractCost is one contract body's AnalyzeFunction wall.
type ContractCost struct {
	Name string
	Ms   float64
}

const slowContractsKept = 8

// fileDetailsMu guards FileDetails. Each FileDetail is owned by one
// goroutine until EndFileDetail publishes it.
var (
	fileDetailsMu sync.Mutex
	FileDetails   []*FileDetail
)

// detailOnly turns on the per-entry timers WITHOUT the global spans
// and counters. Those ride one shared mutex and inflate a parallel
// sweep 6–8×; the FileDetail timers are per-goroutine time.Since
// reads at sites that already own the work, so this mode's numbers
// are honest wall — the mechanism × file decomposition an estimate
// prices against.
var detailOnly atomic.Bool

// SetDetailOnly turns the detail-only mode on or off.
func SetDetailOnly(value bool) { detailOnly.Store(value) }

// DetailOnly reports whether the detail-only mode is on.
func DetailOnly() bool { return detailOnly.Load() }

// BeginFileDetail starts a per-entry accumulator. Identity when
// neither tracing nor detail-only mode is on — callers still call
// End with a nil-safe pattern.
func BeginFileDetail(path string) *FileDetail {
	if !IsEnabled() && !DetailOnly() {
		return nil
	}
	return &FileDetail{Path: path}
}

// EndFileDetail publishes a finished entry row. No-op on nil.
func EndFileDetail(detail *FileDetail, wallMs float64) {
	if detail == nil {
		return
	}
	detail.WallMs = wallMs
	fileDetailsMu.Lock()
	FileDetails = append(FileDetails, detail)
	fileDetailsMu.Unlock()
}

// ClearFileDetails drops published rows (TraceReset / TraceStart).
func ClearFileDetails() {
	fileDetailsMu.Lock()
	FileDetails = nil
	fileDetailsMu.Unlock()
}

// SnapshotFileDetails copies published rows for the report.
func SnapshotFileDetails() []*FileDetail {
	fileDetailsMu.Lock()
	defer fileDetailsMu.Unlock()
	out := make([]*FileDetail, len(FileDetails))
	copy(out, FileDetails)
	return out
}

// NotePhase adds milliseconds to a named phase on this entry.
func (d *FileDetail) NotePhase(phase string, started time.Time) {
	if d == nil {
		return
	}
	ms := float64(time.Since(started)) / float64(time.Millisecond)
	switch phase {
	case "facts":
		d.FactsMs += ms
	case "objectGraphs":
		d.ObjectMs += ms
	case "topLevel":
		d.TopMs += ms
	case "bodies":
		d.BodiesMs += ms
	case "flush":
		d.FlushMs += ms
	case "callSiteJoin":
		d.CallSiteJoinMs += ms
	case "analyzeFunction":
		d.AnalyzeFunction += ms
	}
}

// NoteContract records one AnalyzeFunction wall, keeping the slowest.
func (d *FileDetail) NoteContract(name string, started time.Time) {
	if d == nil {
		return
	}
	ms := float64(time.Since(started)) / float64(time.Millisecond)
	d.Contracts++
	d.AnalyzeFunction += ms
	d.SlowContracts = append(d.SlowContracts, ContractCost{Name: name, Ms: ms})
	sort.Slice(d.SlowContracts, func(i, j int) bool {
		return d.SlowContracts[i].Ms > d.SlowContracts[j].Ms
	})
	if len(d.SlowContracts) > slowContractsKept {
		d.SlowContracts = d.SlowContracts[:slowContractsKept]
	}
}

// NoteCount bumps a per-entry mechanism counter.
func (d *FileDetail) NoteCount(name string, n int64) {
	if d == nil || n == 0 {
		return
	}
	switch name {
	case "checkAssignability":
		d.CheckAssignability += n
	case "inlineContractCall":
		d.InlineContractCall += n
	case "kernel.ask":
		d.KernelAsk += n
	case "kernel.cacheHit":
		d.KernelCacheHit += n
	case "narrowings":
		d.Narrowings += n
	case "effectScan":
		d.EffectScan += n
	case "join.declared":
		d.JoinDeclared += n
	case "join.callback":
		d.JoinCallback += n
	case "join.reach.snapshot":
		d.JoinReachSnapshot += n
	case "join.reach.fallback":
		d.JoinReachFallback += n
	case "join.memo.hit":
		d.JoinMemoHit += n
	case "join.memo.miss":
		d.JoinMemoMiss += n
	}
}

// BaseName is the entry's file name for compact report rows.
func (d *FileDetail) BaseName() string {
	if d == nil {
		return ""
	}
	return filepath.Base(d.Path)
}
