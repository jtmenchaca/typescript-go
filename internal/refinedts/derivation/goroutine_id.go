// goroutineID parses the id out of runtime.Stack's first line — the
// same helper tracing, diagnose, annotations, narrowing, and walk each
// carry (one copy per package, this port's convention). The Recorder
// registry keys on it: a walk's spans belong to the goroutine walking,
// and a sweep runs one goroutine per entry file.
package derivation

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
