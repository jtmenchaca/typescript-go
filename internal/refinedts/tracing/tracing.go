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

func NotePreTrace(name string, ms float64) {
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

func TraceStop() TraceResult {
	SetEnabled(false)
	held := Root
	if held == nil {
		held = &TraceSpan{Name: "check"}
	}
	result := TraceResult{Root: held, Counters: Counters}
	SetRoot(nil)
	SetTreeCurrent(nil)
	return result
}

func Tracing() bool {
	return Enabled
}

// Recording says whether this grain is being recorded. Call sites
// that would build a label or read a node's text ask first, so they
// pay nothing when off.
func Recording(grain Grain) bool {
	return Enabled && Level >= GrainLevel[grain]
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
	if !Enabled || Level < GrainLevel[grain] {
		return run()
	}
	frame := Enter(name, grain)
	defer Leave(frame, grain)
	return run()
}

// TraceFile times one entry file's whole check, so the report can
// rank files and show the trend across a batch. (TRACE.perFile is
// inlined true — the tuning module ports with service/.)
func TraceFile[T any](path string, run func() T) T {
	if !Enabled {
		return run()
	}
	startedAt := time.Now()
	defer func() {
		elapsed := float64(time.Since(startedAt)) / float64(time.Millisecond)
		FileMs[path] += elapsed
		FileOrder = append(FileOrder, FileCost{Path: path, Ms: elapsed})
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
	if !Enabled {
		return 0
	}
	AddClockReads(1)
	return float64(time.Since(RunStartedAt)) / float64(time.Millisecond)
}

// Count bumps a counter, optionally with elapsed time. Free when off.
func Count(name string, ms float64) {
	if !Enabled {
		return
	}
	held := Counters[name]
	if held == nil {
		Counters[name] = &TraceCounter{Calls: 1, TotalMs: ms}
	} else {
		held.Calls++
		held.TotalMs += ms
	}
}

// CountBy bumps a counter by a stated amount — how many entries a
// loop moved, how many bytes a wire carried. Free when off.
func CountBy(name string, amount int64) {
	if !Enabled {
		return
	}
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
	if !Enabled {
		return run()
	}
	AddClockReads(2)
	startedAt := time.Now()
	defer func() {
		Count(name, float64(time.Since(startedAt))/float64(time.Millisecond))
	}()
	return run()
}
