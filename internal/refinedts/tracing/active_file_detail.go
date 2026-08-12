// Goroutine-bound active FileDetail so Count/Span sites without a
// FlowContext (kernel asks, wire encode) still attribute to the entry
// that is walking on this goroutine. CheckFiles runs one entry per
// goroutine for the walk's duration, so the binding is exact for a
// sweep; nested work on the same goroutine inherits the entry.

package tracing

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
)

var activeFileDetail sync.Map // goid (uint64) → *FileDetail

// BindFileDetail pins this goroutine's entry accumulator. Pass nil to
// clear. CheckFiles binds around runRefinements.
func BindFileDetail(detail *FileDetail) {
	id := goroutineID()
	if detail == nil {
		activeFileDetail.Delete(id)
		return
	}
	activeFileDetail.Store(id, detail)
}

// ActiveFileDetail is the entry this goroutine is currently walking,
// or nil.
func ActiveFileDetail() *FileDetail {
	v, ok := activeFileDetail.Load(goroutineID())
	if !ok {
		return nil
	}
	return v.(*FileDetail)
}

// goroutineID parses the id out of runtime.Stack's first line. Profiling
// only — never on a hot path with tracing off (Bind/Active are only
// reached from instrumented sites that already checked IsEnabled, or
// from CheckFiles' Bind around the whole entry).
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:\n"
	line := buf[:n]
	const prefix = "goroutine "
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0
	}
	line = line[len(prefix):]
	end := bytes.IndexByte(line, ' ')
	if end < 0 {
		return 0
	}
	id, err := strconv.ParseUint(string(line[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
