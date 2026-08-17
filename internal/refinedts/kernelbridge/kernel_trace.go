// Kernel question tracing at the ask seam: the raw request/answer wires
// that cross into the native dylib, for a diagnosis that needs to read
// what was actually asked rather than write a probe test to find out.
//
// This is a SEPARATE concern from question_costs.go's SetQuestionTrace
// (which streams op/ms/bytes, never the wire itself) — kept as its own
// file/hook rather than folded into that one because the two are read
// for different reasons: costs answer "what is slow," this answers
// "what was asked and what came back." Both may be set at once; each
// fires independently at its own seam.
package kernelbridge

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// kernelTraceWriter holds the active tracer behind an atomic pointer —
// every kernel ask reads it (including cache hits), so the disabled
// path (the default, nil) must cost one atomic load and a nil compare,
// never a mutex lock. A *func(line string) rather than a bare
// func(line string) because atomic.Pointer needs a pointer-shaped type
// to swap.
var kernelTraceWriter atomic.Pointer[func(string)]

// SetKernelTraceWriter installs fn as the kernel question tracer: once
// set, every question crossing into the native kernel writes two lines
// to fn — the request wire, then the answer wire, each already
// terminated (each call to fn is exactly one line, no embedded
// newline). nil (the default) disables tracing.
func SetKernelTraceWriter(fn func(line string)) {
	if fn == nil {
		kernelTraceWriter.Store(nil)
		return
	}
	kernelTraceWriter.Store(&fn)
}

// TraceKernelTo installs fn as the kernel question tracer and returns a
// restore closure that puts back whatever tracer was active before —
// the test-harness idiom (`defer restore()` / `t.Cleanup(restore)`).
//
// This is the exported adapter rather than a testing.TB-typed helper:
// no non-test file in this package imports "testing" anywhere in the
// tree (checked — every testing import in kernelbridge sits in a
// _test.go file), so a bare func(line string) callback matches the
// package's own convention (SetQuestionTrace, question_costs.go, is
// the same shape) instead of introducing testing.TB into a shipped
// file. A test wraps it with t.Logf itself:
//
//	restore := kernelbridge.TraceKernelTo(func(line string) { t.Logf("%s", line) })
//	defer restore()
func TraceKernelTo(fn func(line string)) (restore func()) {
	previous := kernelTraceWriter.Swap(&fn)
	return func() {
		if previous == nil {
			kernelTraceWriter.Store(nil)
			return
		}
		kernelTraceWriter.Store(previous)
	}
}

// traceKernelQuestion writes the Q line for a question about to cross
// into the kernel. The disabled path is one atomic load and a nil
// check — no string building happens before it.
func traceKernelQuestion(op string, wire string) {
	fn := kernelTraceWriter.Load()
	if fn == nil {
		return
	}
	(*fn)(fmt.Sprintf("refinedts-kernel Q %s %s", op, wire))
}

// traceKernelAnswer writes the A line for a question's raw answer wire.
// Same disabled-path cost as traceKernelQuestion.
func traceKernelAnswer(op string, wire string) {
	fn := kernelTraceWriter.Load()
	if fn == nil {
		return
	}
	(*fn)(fmt.Sprintf("refinedts-kernel A %s %s", op, wire))
}

// traceKernelCacheHit writes the C line for a question the cache
// answered without asking the kernel — with the question KEY and the
// cached ANSWER on the line, newlines flattened, so a diagnosis reads
// the same wires a fresh ask would have shown (the disk-persisted
// cache otherwise hides every wire of a question asked in any earlier
// run). Same disabled-path cost as traceKernelQuestion.
func traceKernelCacheHit(op string, key string, answer string) {
	fn := kernelTraceWriter.Load()
	if fn == nil {
		return
	}
	flat := strings.ReplaceAll(key, "\n", " | ")
	(*fn)(fmt.Sprintf("refinedts-kernel C %s %s => %s", op, flat, answer))
}
