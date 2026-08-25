// The checker's determinism-diagnosis channel — off by default, one
// flag, no environment variables. It exists because facts compiled
// under concurrent per-entry checkers were observed differing between
// runs, and the failure was silent at every layer: no panic, no
// error, just a different verdict on a rerun of the same files. This
// package gives every layer one place to say "here is what I did,
// and which goroutine did it" so a diff between two runs' logs can
// point at the first place they diverge.
//
// Ported from no TS source — this is a Go-only diagnostic; the TS
// checker's single-process, single-run shape never had concurrent
// per-entry checkers to diagnose.

package diagnose

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// enabled is set once, before any check begins (main.go's flag.Parse,
// then SetCapture), and never written again after that — so every
// read below (Enabled, EventOn) needs no lock.
var enabled bool

// full is true under bare `-diagnose` (every event kept); false under
// a prefix-filtered capture, where prefixes below decides.
var full bool

// prefixes holds the comma-separated prefix list a filtered capture
// was given (`-diagnose=walk.entryEnv,walk.contract`) — nil under
// full capture or when disabled.
var prefixes []string

// SetCapture turns the channel on, off, or on-with-a-filter, from the
// flag's raw value. "" disables; "true" is full capture; anything
// else is a comma-separated list of event-name prefixes — an event
// is kept when EventOn reports true for it (strings.HasPrefix against
// one of these). Called once, at startup, before any goroutine could
// be reading Enabled/EventOn concurrently.
func SetCapture(spec string) {
	switch spec {
	case "":
		enabled = false
		full = false
		prefixes = nil
	case "true":
		enabled = true
		full = true
		prefixes = nil
	default:
		enabled = true
		full = false
		var list []string
		for _, part := range strings.Split(spec, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				list = append(list, trimmed)
			}
		}
		prefixes = list
	}
}

// Enabled reports whether any capture — full or filtered — is on.
func Enabled() bool { return enabled }

// EventOn reports whether this event name should be captured: always
// false when disabled, always true under full capture, otherwise a
// prefix match against the filter list SetCapture stored. Call sites
// that pay a real cost to prepare a log line (goroutineID, building
// the fields) gate on EventOn instead of Enabled, so a hunt scoped to
// a few event names skips that cost for everything else.
func EventOn(event string) bool {
	if !enabled {
		return false
	}
	if full {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	return false
}

// startedAt anchors the monotonic-ms-since-start column — a wall
// clock printed here would just be noise; what matters is each line's
// position relative to the others.
var startedAt = time.Now()

// record is one captured event, held UNFORMATTED until Flush writes
// it out. at is the same milliseconds-with-microsecond-precision
// value the line's own timestamp column will carry — computed once,
// here, so Flush can sort without recomputing it. event and fields
// are exactly what Log received: no fmt work happens until Flush
// walks these records, which is what keeps a KEPT event's hot-path
// cost down to the append below.
//
// fields is captured by reference to whatever the caller passed —
// safe because every current call site builds fresh values (string,
// bool, int, a freshly formatted spelling) with nothing else holding
// a mutable reference to them. A FUTURE call site must keep that
// contract: never pass a value that mutates after this call (a slice
// or map the caller goes on to write into), since Flush reads it
// later, after the mutation would already have happened.
type record struct {
	at     float64
	gid    uint64
	event  string
	fields []any
}

// lineBuffer collects one goroutine's records. Only that goroutine
// ever appends to it, so appends need no lock.
type lineBuffer struct {
	records []record
}

// buffers maps a goroutine id (uint64) to its *lineBuffer. A goroutine
// claims its buffer once, on its first Log call, via LoadOrStore; every
// later call from the same goroutine appends to the buffer it already
// holds. Go can reuse a goroutine id after the original goroutine
// exits, but that is still safe here: at any instant at most one live
// goroutine is writing to a given id's buffer, which is exactly the
// single-writer condition this design needs.
var buffers sync.Map

// Log records one event when EventOn(event) is true, and does
// nothing — not even a goroutineID call — otherwise. Nothing is
// formatted here: the record holds the raw fields, by reference, and
// every fmt call (the key=value line, the quoting, the
// refinedts-diagnose prefix) happens in Flush instead, once, after
// every worker has joined. That deferral is what keeps a KEPT event's
// hot-path cost down to a goroutineID call, a time.Since, and a slice
// append — the same line shape Flush produces is documented on
// Flush, not here.
//
// See the record type's own comment for the fields-by-reference
// contract every call site (present and future) must keep.
func Log(event string, fields ...any) {
	if !EventOn(event) {
		return
	}
	gid := goroutineID()
	atMs := float64(time.Since(startedAt).Microseconds()) / 1000
	stored, _ := buffers.LoadOrStore(gid, &lineBuffer{})
	buf := stored.(*lineBuffer)
	buf.records = append(buf.records, record{at: atMs, gid: gid, event: event, fields: fields})
}

// LogIf logs only when cond holds — a one-line guard for call sites
// that would otherwise write `if cond { diagnose.Log(...) }`.
func LogIf(cond bool, event string, fields ...any) {
	if !cond {
		return
	}
	Log(event, fields...)
}

// goroutineID parses the id out of runtime.Stack's first line, the
// same way tracing.goroutineID does (tracing/active_file_detail.go) —
// this package cannot import that unexported helper, so it keeps its
// own copy. Only ever called when enabled: cheap next to the
// formatting and the slice append Log already pays, but never worth
// paying on a hot path with diagnosis off.
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
