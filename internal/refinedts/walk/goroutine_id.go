// goroutineID parses the id out of runtime.Stack's first line — the
// same helper tracing, diagnose, annotations, and narrowing carry
// (each package keeps its own copy by this port's convention). The
// reentrancy guards that key by (goroutine, node) need it: a cycle
// only exists within one goroutine's call stack, and a node-only key
// read another entry's concurrent walk as a cycle (the A1 sweep
// nondeterminism, 2026-08-24).
package walk

import (
	"bytes"
	"runtime"
	"strconv"
)

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
