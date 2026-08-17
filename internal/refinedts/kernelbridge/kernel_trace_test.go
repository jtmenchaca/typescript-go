package kernelbridge

import (
	"strings"
	"testing"
)

// TestKernelTraceWritesQAndALinesForARealQuestion asks one cheap
// question through the loaded kernel with tracing on and checks the
// seam wrote exactly the Q/A shape a diagnosis reads: the question
// name, and the request wire verbatim on the Q line.
func TestKernelTraceWritesQAndALinesForARealQuestion(t *testing.T) {
	kernel := loadRoundTripKernel(t)
	ClearQuestionCache() // force a real ask, not a cache hit

	var lines []string
	restore := TraceKernelTo(func(line string) { lines = append(lines, line) })
	defer restore()

	impossible := impossibleSet()
	wireInput := EncodeSet(impossible)
	if got := kernel.ScalarEmpty(impossible); !got {
		t.Fatalf("scalarEmpty(impossible) = %v, want true", got)
	}

	var qLine, aLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "refinedts-kernel Q scalarEmpty ") {
			qLine = line
		}
		if strings.HasPrefix(line, "refinedts-kernel A scalarEmpty ") {
			aLine = line
		}
	}
	if qLine == "" {
		t.Fatalf("no Q line for scalarEmpty in trace: %v", lines)
	}
	if aLine == "" {
		t.Fatalf("no A line for scalarEmpty in trace: %v", lines)
	}
	if !strings.Contains(qLine, wireInput) {
		t.Errorf("Q line missing the request wire verbatim: %q, want it to contain %q", qLine, wireInput)
	}
}

// TestKernelTraceRestoreReturnsThePreviousWriter checks the restore
// closure hands the previous tracer back rather than always clearing to
// nil — a nested trace scope must not silently drop its caller's own
// tracer when it's done.
func TestKernelTraceRestoreReturnsThePreviousWriter(t *testing.T) {
	var outer []string
	restoreOuter := TraceKernelTo(func(line string) { outer = append(outer, line) })
	defer restoreOuter()

	var inner []string
	restoreInner := TraceKernelTo(func(line string) { inner = append(inner, line) })
	traceKernelQuestion("probe", "wire")
	restoreInner()

	traceKernelQuestion("probe2", "wire2")

	if len(inner) != 1 {
		t.Fatalf("inner trace lines = %v, want 1 line", inner)
	}
	if len(outer) != 1 {
		t.Fatalf("outer trace lines after restore = %v, want 1 line (only the post-restore question)", outer)
	}
}
