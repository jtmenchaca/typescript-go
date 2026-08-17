// Pins the accounting the negative self-time defect broke.
//
// The report showed `narrowings ... self ms -23768.54` and
// `recoverPure ... -127.18`. Self time is elapsed minus the time a
// frame's children took, so a negative row means child time was
// charged to a frame that did not contain it. The cause was one shared
// span stack under CheckFiles' goroutine-per-entry sweep: Enter
// appended and Leave popped the TOP of that stack rather than its own
// frame, so a worker's Leave credited its elapsed to whatever frame
// another worker had open at that instant.
//
// These tests reproduce that shape — concurrent workers each running
// their own nested and recursive spans — and assert the two properties
// the report depends on: self time is never negative, and the rows sum
// to the window they are divided out of.

package tracing

import (
	"sync"
	"testing"
	"time"
)

// spanSnapshot copies the flat table so assertions read a stable view.
func spanSnapshot() map[string]TraceEntry {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	out := make(map[string]TraceEntry, len(Flat))
	for name, entry := range Flat {
		out[name] = *entry
	}
	return out
}

// TestSelfTimeNonNegativeUnderParallelWorkers is the defect's own
// shape: several goroutines walking at once, each opening an outer
// span with a cheap frequent span nested inside it — the
// analyzeFunction-over-narrowings nesting the recharts trace showed.
// The workers are deliberately unbalanced, so one worker's long span
// overlaps many of another's short ones, which is exactly what drove
// narrowings' child time past its own elapsed.
func TestSelfTimeNonNegativeUnderParallelWorkers(t *testing.T) {
	TraceStart(GrainStep)
	defer TraceStop()

	const workers = 6
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			closeScope := BeginSpanScope()
			defer closeScope()
			Span("outer", func() any {
				// one worker holds its outer span far longer than the
				// others, so its Leave lands in the middle of their
				// nested spans
				if w == 0 {
					time.Sleep(8 * time.Millisecond)
				}
				for i := 0; i < 20; i++ {
					Span("inner", func() any {
						time.Sleep(time.Millisecond / 4)
						return nil
					}, GrainStep)
				}
				return nil
			}, GrainStep)
		}(w)
	}
	wg.Wait()

	for name, entry := range spanSnapshot() {
		if entry.SelfMs < 0 {
			t.Errorf("%s: SelfMs = %f, want >= 0 — child time was charged to a frame that did not contain it",
				name, entry.SelfMs)
		}
		if entry.TotalMs < 0 {
			t.Errorf("%s: TotalMs = %f, want >= 0", name, entry.TotalMs)
		}
	}
	// the floor in Leave is an assertion, not a repair: a frame charged
	// more child time than its own elapsed means the nesting model is
	// wrong, which is precisely the defect this pins
	if n := NegativeSelfCount(); n != 0 {
		t.Errorf("NegativeSelfCount = %d, want 0 — %d frames closed with children longer than themselves", n, n)
	}

	entries := spanSnapshot()
	outer, held := entries["outer"]
	if !held {
		t.Fatal("no outer entry recorded")
	}
	if outer.Calls != workers {
		t.Errorf("outer.Calls = %d, want %d", outer.Calls, workers)
	}
	inner, held := entries["inner"]
	if !held {
		t.Fatal("no inner entry recorded")
	}
	if inner.Calls != workers*20 {
		t.Errorf("inner.Calls = %d, want %d", inner.Calls, workers*20)
	}
	// every worker's outer span genuinely contained its own inner
	// spans, so outer's inclusive time covers inner's — a property the
	// shared stack broke by dropping cross-goroutine "reentrant" totals
	if outer.TotalMs < inner.SelfMs {
		t.Errorf("outer.TotalMs = %f < inner.SelfMs = %f — inclusive time lost its own children",
			outer.TotalMs, inner.SelfMs)
	}
}

// TestSelfTimeNonNegativeUnderRecursion pins the other half of the old
// Active map: recursion depth is a question about ONE goroutine's
// stack. Shared across workers it answered for the sweep, so a
// worker entering a span another worker already had open was marked
// reentrant and its elapsed was dropped from TotalMs entirely — which
// is why analyzeFunction reported 109981 ms of self against 238.2 ms
// of total.
func TestSelfTimeNonNegativeUnderRecursion(t *testing.T) {
	TraceStart(GrainStep)
	defer TraceStop()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeScope := BeginSpanScope()
			defer closeScope()
			var recur func(depth int)
			recur = func(depth int) {
				Span("recursive", func() any {
					if depth > 0 {
						recur(depth - 1)
					}
					time.Sleep(time.Millisecond / 4)
					return nil
				}, GrainStep)
			}
			recur(5)
		}()
	}
	wg.Wait()

	entries := spanSnapshot()
	entry, held := entries["recursive"]
	if !held {
		t.Fatal("no recursive entry recorded")
	}
	if entry.SelfMs < 0 {
		t.Errorf("recursive.SelfMs = %f, want >= 0", entry.SelfMs)
	}
	if entry.Calls != 4*6 {
		t.Errorf("recursive.Calls = %d, want %d", entry.Calls, 4*6)
	}
	// inclusive time counts the OUTERMOST frame per goroutine, so four
	// workers contribute four totals — never zero, which is what a
	// cross-goroutine reentrancy verdict produced
	if entry.TotalMs <= 0 {
		t.Errorf("recursive.TotalMs = %f, want > 0 — every frame read as reentrant", entry.TotalMs)
	}
	// a recursive span's self time is real work, and cannot exceed the
	// inclusive time of the outermost frames that contained it
	if entry.SelfMs > entry.TotalMs+1 {
		t.Errorf("recursive.SelfMs = %f > recursive.TotalMs = %f — self time escaped its own inclusive window",
			entry.SelfMs, entry.TotalMs)
	}
	if n := NegativeSelfCount(); n != 0 {
		t.Errorf("NegativeSelfCount = %d, want 0", n)
	}
}

// TestAttributedSumsWithinWorkerWindow pins the report's other broken
// number: attributed self time read 298276 ms against a 22417 ms wall
// (1330%), because N workers produce N ms of span time per wall
// millisecond. The self-time column is divided out of the summed
// worker walls, so the attributed total never exceeds that window.
func TestAttributedSumsWithinWorkerWindow(t *testing.T) {
	TraceStart(GrainStep)

	const workers = 4
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeScope := BeginSpanScope()
			defer closeScope()
			Span("phase", func() any {
				for i := 0; i < 10; i++ {
					Span("step", func() any {
						time.Sleep(time.Millisecond / 2)
						return nil
					}, GrainStep)
				}
				return nil
			}, GrainStep)
		}()
	}
	wg.Wait()

	data := TraceData()
	defer TraceStop()

	attributed := 0.0
	for _, entry := range data.Entries {
		attributed += entry.SelfMs
	}
	if data.WorkerMs <= 0 {
		t.Fatalf("WorkerMs = %f, want > 0 — no worker scope closed", data.WorkerMs)
	}
	if attributed > data.WorkerMs {
		t.Errorf("attributed = %f ms exceeds the worker window %f ms — self time was counted outside the window it divides",
			attributed, data.WorkerMs)
	}
	// the workers ran concurrently, so the summed worker walls exceed
	// the process wall — the fact the old report's percentages ignored
	if data.WorkerMs < data.WallMs {
		t.Errorf("WorkerMs = %f < WallMs = %f — the window must cover the wall",
			data.WorkerMs, data.WallMs)
	}
}

// TestReportAfterStopKeepsWorkerWindow pins the order the CLI actually
// runs: TraceStop() and then EmitTraceReport()
// (cmd/refinedts-check/main.go). The self-time column is divided out of
// the worker window, so stopping must not clear it — a cleared window
// collapses to the wall and every percentage reads N times too high
// again.
func TestReportAfterStopKeepsWorkerWindow(t *testing.T) {
	TraceStart(GrainStep)

	const workers = 4
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeScope := BeginSpanScope()
			defer closeScope()
			Span("phase", func() any {
				time.Sleep(3 * time.Millisecond)
				return nil
			}, GrainStep)
		}()
	}
	wg.Wait()

	TraceStop()
	data := TraceData()

	if data.WorkerMs <= data.WallMs {
		t.Errorf("WorkerMs = %f, WallMs = %f — %d concurrent workers must sum past the wall, and the window must survive TraceStop",
			data.WorkerMs, data.WallMs, workers)
	}
	attributed := 0.0
	for _, entry := range data.Entries {
		attributed += entry.SelfMs
	}
	if attributed > data.WorkerMs {
		t.Errorf("attributed = %f ms exceeds the worker window %f ms after stop", attributed, data.WorkerMs)
	}
}

// TestUnscopedSpansStillAccount pins the single-goroutine path: a lone
// CheckFile, a test, or the editor path opens no scope at all, and its
// spans must still nest and account exactly as before.
func TestUnscopedSpansStillAccount(t *testing.T) {
	TraceStart(GrainStep)
	defer TraceStop()

	Span("lonely.outer", func() any {
		Span("lonely.inner", func() any {
			time.Sleep(2 * time.Millisecond)
			return nil
		}, GrainStep)
		return nil
	}, GrainStep)

	entries := spanSnapshot()
	outer, held := entries["lonely.outer"]
	if !held {
		t.Fatal("no lonely.outer entry recorded")
	}
	inner, held := entries["lonely.inner"]
	if !held {
		t.Fatal("no lonely.inner entry recorded")
	}
	if outer.SelfMs < 0 || inner.SelfMs < 0 {
		t.Errorf("SelfMs went negative without any concurrency: outer=%f inner=%f",
			outer.SelfMs, inner.SelfMs)
	}
	// the inner span's time belongs to inner, not to outer: outer's own
	// self time is what is left after its child
	if outer.SelfMs > inner.SelfMs {
		t.Errorf("outer.SelfMs = %f > inner.SelfMs = %f — the child's time stayed on the parent",
			outer.SelfMs, inner.SelfMs)
	}
}
