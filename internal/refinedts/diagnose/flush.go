package diagnose

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Flush writes every goroutine's buffered records to stderr, ordered
// by their timestamp column, then empties the buffers. Call once,
// after every worker goroutine that might call Log has joined — a
// call while workers are still logging would miss whatever they add
// after the collection pass. A no-op when diagnosis is off or when
// nothing was logged.
//
// Every fmt call Log used to pay on its own hot path happens HERE
// instead, once per kept record, after the run: the key=value line
// building, the quoting, and the refinedts-diagnose prefix. Line
// shape (unchanged from before the deferral):
//
//	refinedts-diagnose <goroutineID> <ms-since-start, microsecond precision> <event> key=value key=value ...
//
// The timestamp carries three decimal places (microsecond precision,
// not whole milliseconds) because the sort below depends on it, and
// events on different goroutines routinely land inside the same
// millisecond.
//
// fields alternate key, value; a value is formatted with %v, and any
// value whose formatting contains a space or newline is quoted with
// %q so the line still splits cleanly on whitespace. An odd trailing
// field (a key with no paired value) prints as `key=<MISSING>` rather
// than panicking or dropping the key silently.
//
// The trade this design makes: a process that dies mid-check (a
// panic, a signal, an os.Exit reached before Flush runs) loses its
// trace, because nothing reaches stderr until Flush writes it. That
// is acceptable here because the corruption this channel hunts is a
// wrong answer on a clean exit, not a crash — the very thing the
// unbuffered, mutex-serialized writer was suspected of masking by
// changing the goroutines' scheduling.
func Flush() {
	if !enabled {
		return
	}
	var all []record
	buffers.Range(func(_, value any) bool {
		buf := value.(*lineBuffer)
		all = append(all, buf.records...)
		return true
	})
	if len(all) == 0 {
		return
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at < all[j].at })
	var out []byte
	for _, r := range all {
		out = appendFormattedLine(out, r)
		out = append(out, '\n')
	}
	fmt.Fprint(os.Stderr, string(out))
	buffers = sync.Map{}
}

// appendFormattedLine renders one record in the refinedts-diagnose
// line shape (Flush's own doc) and appends it to out.
func appendFormattedLine(out []byte, r record) []byte {
	var b strings.Builder
	b.WriteString("refinedts-diagnose ")
	b.WriteString(strconv.FormatUint(r.gid, 10))
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(r.at, 'f', 3, 64))
	b.WriteByte(' ')
	b.WriteString(r.event)
	fields := r.fields
	for i := 0; i < len(fields); i += 2 {
		b.WriteByte(' ')
		key := fmt.Sprintf("%v", fields[i])
		if i+1 >= len(fields) {
			b.WriteString(key)
			b.WriteString("=<MISSING>")
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		value := fmt.Sprintf("%v", fields[i+1])
		if strings.ContainsAny(value, " \n") {
			b.WriteString(fmt.Sprintf("%q", value))
		} else {
			b.WriteString(value)
		}
	}
	return append(out, b.String()...)
}
