// The tracing seam: where a check's time goes, measured rather than
// reasoned about. Off by default and free when off — every hook is a
// boolean test, nothing records, nothing times.
//
// Ported 1:1 from service/tracing.ts (records in trace_state.go; the
// printed report is service-tier and ports with service/). Three TS
// members have no Go twin here, each for a stated reason:
//   - spanAsync: the Go walk has no await boundary; Span covers it.
//   - wrapChecker: a JS Proxy over the tsc checker — in Go the
//     checker is called directly, so attribution happens at call
//     sites (or not at all; the in-process call is the cheap thing
//     the wrap existed to measure).
//   - the unload report hook: emitTraceReport lives in service/.

package tracing

import "time"

// NotePreTrace takes recordsMu: PreTraceNotes is shared record state
// like Flat/Counters/etc., appended to from outside the Enter/Leave
// path.
func NotePreTrace(name string, ms float64) {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	PreTraceNotes = append(PreTraceNotes, PreTraceNote{Name: name, Ms: ms})
}

func TraceStart(grain Grain) {
	SetEnabled(true)
	if grain != "" {
		SetLevel(GrainLevel[grain])
	}
	TraceReset()
}

func TraceReset() {
	ResetRecords()
}

type TraceResult struct {
	Root     *TraceSpan
	Counters map[string]*TraceCounter
}

// TraceStop reads Root and Counters under recordsMu (via GetRoot and
// the counters snapshot below) before clearing them, so no concurrent
// Enter/Leave/Count can land between the read and the clear.
func TraceStop() TraceResult {
	SetEnabled(false)
	recordsMu.Lock()
	held := Root
	if held == nil {
		held = &TraceSpan{Name: "check"}
	}
	counters := Counters
	recordsMu.Unlock()
	result := TraceResult{Root: held, Counters: counters}
	SetRoot(nil)
	// Drop every goroutine's span stack with the root it pointed into,
	// so a later trace never opens nodes under a tree this one returned.
	// The worker window is NOT cleared here: EmitTraceReport runs after
	// TraceStop (cmd/refinedts-check/main.go), and the self-time column
	// it prints is divided out of that window.
	closeScopes()
	return result
}

func Tracing() bool {
	return IsEnabled()
}

// Recording says whether this grain is being recorded. Call sites
// that would build a label or read a node's text ask first, so they
// pay nothing when off. Two atomic loads — no lock — so the off path
// stays race-free and near-zero cost under concurrent walk goroutines.
func Recording(grain Grain) bool {
	return IsEnabled() && CurrentLevel() >= GrainLevel[grain]
}

// Span runs inside a named span. Identity when tracing is off, or
// when the grain is finer than what the options ask to record.
//
// NOTE what this cannot do for you: the callback is allocated by the
// CALLER, before this function is entered, so a Span on a path taken
// hundreds of thousands of times per file costs a closure per call
// even with tracing off. On the walk's hot functions that was
// measured at ~1.65x the whole run. Guard those call sites with
// Recording(grain) first, or do not span them.
func Span[T any](name string, run func() T, grain Grain) T {
	if !IsEnabled() || CurrentLevel() < GrainLevel[grain] {
		return run()
	}
	frame := Enter(name, grain)
	defer Leave(frame, grain)
	return run()
}

// TraceFile times one entry file's whole check, so the report can
// rank files and show the trend across a batch. (TRACE.perFile is
// inlined true — the tuning module ports with service/.) FileMs and
// FileOrder are shared record state — one goroutine per entry file
// under the parallel sweep means concurrent TraceFile calls, so the
// map bump and the order append take recordsMu.
func TraceFile[T any](path string, run func() T) T {
	if !IsEnabled() {
		return run()
	}
	startedAt := time.Now()
	defer func() {
		elapsed := float64(time.Since(startedAt)) / float64(time.Millisecond)
		recordsMu.Lock()
		FileMs[path] += elapsed
		FileOrder = append(FileOrder, FileCost{Path: path, Ms: elapsed})
		recordsMu.Unlock()
	}()
	return run()
}

// Clock is a clock read that costs nothing when tracing is off.
//
// For hot LOOPS, where Span is the wrong instrument: handing the loop
// to a callback inside a defer changes how the compiler optimizes it,
// and on a loop that runs a billion times the measurement becomes
// mostly a measurement of the measuring. Timing in place leaves the
// loop exactly as the compiler saw it.
//
//	startedAt := tracing.Clock()
//	for … { … }
//	tracing.Count("the.loop", tracing.Clock()-startedAt)
func Clock() float64 {
	if !IsEnabled() {
		return 0
	}
	AddClockReads(1)
	recordsMu.Lock()
	startedAt := RunStartedAt
	recordsMu.Unlock()
	return float64(time.Since(startedAt)) / float64(time.Millisecond)
}

// Count bumps a counter, optionally with elapsed time. Free when off.
// Counters is shared record state, appended to from every walk
// goroutine, so the read-modify-write takes recordsMu.
//
// When an entry FileDetail is bound to this goroutine (CheckFiles'
// BindFileDetail around runRefinements), the same bump also lands on
// that entry — so per-file mechanism counts stay exact under a
// parallel sweep where the global Counters table mixes every file.
func Count(name string, ms float64) {
	if !IsEnabled() {
		return
	}
	recordsMu.Lock()
	held := Counters[name]
	if held == nil {
		Counters[name] = &TraceCounter{Calls: 1, TotalMs: ms}
	} else {
		held.Calls++
		held.TotalMs += ms
	}
	recordsMu.Unlock()
	if d := ActiveFileDetail(); d != nil {
		d.NoteCount(name, 1)
	}
}

// CountBy bumps a counter by a stated amount — how many entries a
// loop moved, how many bytes a wire carried. Free when off. Same lock
// as Count — both mutate the shared Counters map.
func CountBy(name string, amount int64) {
	if !IsEnabled() {
		return
	}
	recordsMu.Lock()
	defer recordsMu.Unlock()
	held := Counters[name]
	if held == nil {
		Counters[name] = &TraceCounter{Calls: amount}
	} else {
		held.Calls += amount
	}
}

// Counted times a call under a counter. Identity when off. Counters
// do not nest — use Span where self time matters.
func Counted[T any](name string, run func() T) T {
	if !IsEnabled() {
		return run()
	}
	AddClockReads(2)
	startedAt := time.Now()
	defer func() {
		Count(name, float64(time.Since(startedAt))/float64(time.Millisecond))
	}()
	return run()
}
