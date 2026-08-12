// Whether a string set sits inside zod's default Z-suffixed iso
// datetime grammar, and the Date time-value window that grammar
// admits. Evaluation models ask the window; the chain compiler reads
// the same patterns for z.iso.datetime/date/time.

package refinementsets

import "time"

// zod's own ISO grammars at their default arguments, transcribed from
// the vendored core/regexes.ts: dateSource (line 96), the default
// timeSource (line 108), and datetime's assembly with the Z-only
// suffix (lines 124-131).
const isoDateSource = `(?:(?:\d\d[2468][048]|\d\d[13579][26]|\d\d0[48]|[02468][048]00|` +
	`[13579][26]00)-02-29|\d{4}-(?:(?:0[13578]|1[02])-(?:0[1-9]|[12]\d|` +
	`3[01])|(?:0[469]|11)-(?:0[1-9]|[12]\d|30)|(?:02)-(?:0[1-9]|1\d|` +
	`2[0-8])))`
const isoTimeSource = `(?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:\.\d+)?)?`

// IsoPatterns holds "datetime", "date", "time".
var IsoPatterns = map[string]string{
	"date":     isoDateSource,
	"time":     isoTimeSource,
	"datetime": isoDateSource + "T(?:" + isoTimeSource + "(?:Z))",
}

// SeqSubsetAsker is the kernel seam IsoDatetimeWindow needs: whether
// set a is a subset of set b over the sequence (kernel_bridge's
// seqSubset). Standing in for the TS source's ad hoc
// { seqSubset(a, b): boolean } parameter type -- an inline interface
// in Go terms, kept as a named type since Go has no anonymous
// interface literal at a parameter position that reads as cleanly.
type SeqSubsetAsker interface {
	SeqSubset(a, b RefinedSet) bool
}

// DateWindow is the {lo, hi} time-value window IsoDatetimeWindow
// returns.
type DateWindow struct {
	Lo float64
	Hi float64
}

// IsoDatetimeWindow: every member of set is a Z-suffixed iso datetime
// -- the one string family whose Date parse is spec-deterministic --
// decided by the kernel's sequence subset against the grammar. The
// window a Date built from such a string can hold: the grammar admits
// years 0000-9999, so the time value lies between the host's
// spec-exact parses of the two edges.
func IsoDatetimeWindow(kernel SeqSubsetAsker, set RefinedSet) (DateWindow, bool) {
	grammar := FormatGrammar(IsoPatterns["datetime"], "")
	if !grammar.Ok {
		return DateWindow{}, false
	}
	if !kernelSeqSubsetSafe(kernel, set, grammar.Set) {
		return DateWindow{}, false
	}
	return DateWindow{
		Lo: dateParseUTC(0, time.January, 1, 0, 0, 0, 0),
		Hi: dateParseUTC(9999, time.December, 31, 23, 59, 59, 999),
	}, true
}

// dateParseUTC is Date.parse of an ISO-8601 UTC instant, computed the
// same way: milliseconds since the Unix epoch. time.Date with year 0
// is the proleptic Gregorian year 0000 (JS's Date.parse reads the same
// calendar for the four-digit-year form), so this is the exact
// substitute for the TS source's two literal Date.parse(...) calls
// rather than a hardcoded constant, and stays correct if PORT.md ever
// asks for a general Date.parse port.
func dateParseUTC(year int, month time.Month, day, hour, minute, second, ms int) float64 {
	t := time.Date(year, month, day, hour, minute, second, ms*1_000_000, time.UTC)
	return float64(t.UnixMilli())
}

// kernelSeqSubsetSafe wraps the kernel call to fold a panic into
// ok=false, mirroring the TS source's try { ... } catch { return null }.
func kernelSeqSubsetSafe(kernel SeqSubsetAsker, set, grammar RefinedSet) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return kernel.SeqSubset(set, grammar)
}
