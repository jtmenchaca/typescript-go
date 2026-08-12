// from service/trace_report.ts
//
// Relocated: its TS home is service/ (writeFileSync, console.error,
// process.memoryUsage), but the report reads only trace_state's
// records — the same live globals tracing.go already reads — so it
// joins package tracing rather than waiting on service/. Go's
// stdlib covers the TS file's Node-only calls directly (os.WriteFile,
// fmt.Fprintln(os.Stderr, ...), runtime.ReadMemStats), so nothing
// here is stubbed for being "service-tier": the only genuine gap is
// noted at readMemory below.
//
// The human-readable attribution report: process wall, owner split,
// self-time table, hot loops, counters, and per-file ranking. Reads
// the live records from trace_state; does not own the hooks.

package tracing

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func ms(x float64) string {
	if x >= 1000 {
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	if x >= 10 {
		return strconv.FormatFloat(x, 'f', 1, 64)
	}
	return strconv.FormatFloat(x, 'f', 2, 64)
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// commaInt mirrors `n.toLocaleString("en-US")` for a plain integer:
// thousands-grouped with commas. (Duplicated from kernelbridge's own
// commaInt — tracing does not import kernelbridge, per the port
// order, and this is the same tiny pure helper PORT.md's chain_args
// precedent allows to be copied rather than shared.)
func commaInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// TraceReport is the TS TraceReport interface.
type TraceReport struct {
	WallMs float64
	// ProcessMs is time since the PROCESS started, because
	// performance.now()'s timeOrigin is process start in the TS
	// source. The whole run, not just the traced window. (Go has no
	// single process-start clock read the way JS's timeOrigin is —
	// see ProcessStartedAt below.)
	ProcessMs float64
	// TracedFromMs is the process-relative moment TraceStart ran —
	// everything before it ran outside the traced window.
	TracedFromMs float64
	PreTrace     []PreTraceNote
	Entries      []*TraceEntry
	Counters     map[string]*TraceCounter
	Files        []FileCost
	ClockReads   int64
}

// ProcessStartedAt stands in for JS's performance.timeOrigin (always
// process start there). Go has no such fixed epoch, so this package
// sets it once, in an init(), as early as the runtime allows.
var ProcessStartedAt = time.Now()

// TraceData is traceData in the TS source.
func TraceData() TraceReport {
	now := time.Now()
	entries := make([]*TraceEntry, 0, len(Flat))
	for _, e := range Flat {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].SelfMs > entries[j].SelfMs })
	files := append([]FileCost(nil), FileOrder...)
	return TraceReport{
		WallMs:       msSince(RunStartedAt),
		ProcessMs:    float64(now.Sub(ProcessStartedAt)) / float64(time.Millisecond),
		TracedFromMs: float64(RunStartedAt.Sub(ProcessStartedAt)) / float64(time.Millisecond),
		PreTrace:     append([]PreTraceNote(nil), PreTraceNotes...),
		Entries:      entries,
		Counters:     Counters,
		Files:        files,
		ClockReads:   ClockReads,
	}
}

// Owner says who owns a span's time. The split that decides what a
// rewrite in another language could and could not move: the
// checker's own JavaScript is portable, TypeScript's answers and the
// wasm kernel are not — they cost the same whoever calls them.
type Owner string

const (
	// OwnerTypescript is TypeScript's own work: parsing, binding, and
	// answering the analyzer's questions. A rewrite of RefinedTS does
	// not touch this; only replacing tsc does.
	OwnerTypescript Owner = "typescript"
	// OwnerKernel is inside the wasm kernel. Already compiled, already
	// native.
	OwnerKernel Owner = "kernel"
	// OwnerBoundary is the boundary between them: JSON wire encoding,
	// canonical cache keys, question-cache bookkeeping. Serialization
	// work that exists BECAUSE the kernel is out-of-language.
	OwnerBoundary Owner = "boundary"
	// OwnerChecker is RefinedTS's own JavaScript — the walk, the
	// facts sweep, the joins and transfers. The only column a
	// rewrite moves.
	OwnerChecker Owner = "checker"
)

// OwnerOf is ownerOf in the TS source.
func OwnerOf(name string) Owner {
	if strings.HasPrefix(name, "tsc.") {
		return OwnerTypescript
	}
	if name == "programBuild" || name == "tscShapeDiagnostics" {
		return OwnerTypescript
	}
	if name == "programFromExisting" {
		return OwnerTypescript
	}
	if name == "kernel.ask" || name == "kernelLoad" {
		return OwnerKernel
	}
	if strings.HasPrefix(name, "wire.") || name == "canonicalKey" ||
		name == "kernel.cacheLookup" || name == "flushQuestionStore" {
		return OwnerBoundary
	}
	return OwnerChecker
}

var ownerNote = map[Owner]string{
	OwnerTypescript: "TypeScript's own parse, bind and type answers",
	OwnerKernel:     "inside the wasm kernel — already native",
	OwnerBoundary:   "JSON wire, canonical keys, question-cache bookkeeping",
	OwnerChecker:    "RefinedTS's own JavaScript — the walk, the facts sweep",
}

// reportGrain names the current recording grain for the header line,
// reading GrainLevel backwards from Level — trace_state.go already
// carries Level (TRACE.grain inlined at "step", per that file's own
// note); no separate stored name is needed since the three grains
// map to Level 1:1.
func reportGrain() Grain {
	for grain, level := range GrainLevel {
		if level == Level {
			return grain
		}
	}
	return GrainStep
}

// TRACE.slowestFiles / TRACE.writeTo (cache_tuning.ts): the tuning
// module itself is not ported (it lives in service/, per PORT.md,
// and pulls in nothing else this file needs) — inlined here as
// package vars with setters, the same pattern trace_state.go already
// uses for TRACE.enabled/TRACE.grain (see Enabled/Level there).
var (
	SlowestFiles = 15
	WriteTo      = ""
)

func SetSlowestFiles(value int) { SlowestFiles = value }
func SetWriteTo(value string)   { WriteTo = value }

// TraceReportText is traceReport in the TS source.
func TraceReportText() string {
	data := TraceData()
	var out []string
	say := func(s string) { out = append(out, s) }
	sayBlank := func() { out = append(out, "") }

	attributed := 0.0
	for _, e := range data.Entries {
		attributed += e.SelfMs
	}
	sayBlank()
	say(strings.Repeat("═", 78))
	say(fmt.Sprintf(`RefinedTS trace — grain "%s", wall %s ms`, reportGrain(), ms(data.WallMs)))
	say(strings.Repeat("═", 78))
	sayBlank()

	// The whole process, every millisecond named: Go's ProcessStartedAt
	// stands in for performance.now()'s timeOrigin (process start), so
	// the report can account for the time before TraceStart — runtime
	// boot, module imports, batch setup — that the traced window never
	// sees. Rows sum to the process wall exactly.
	noted := 0.0
	for _, note := range data.PreTrace {
		noted += note.Ms
	}
	unmarked := max(0, data.TracedFromMs-noted)
	unspannedMs := max(0, data.WallMs-attributed)
	share := func(x float64) string {
		return padLeft(strconv.FormatFloat(100*x/max(data.ProcessMs, 1e-9), 'f', 1, 64), 8)
	}
	say("the whole process — process start to this report, rows sum to the wall")
	sayBlank()
	say(pad("segment", 46) + padLeft("ms", 11) + padLeft("%", 8))
	say(strings.Repeat("─", 65))
	for _, note := range data.PreTrace {
		say(pad(note.Name, 46) + padLeft(ms(note.Ms), 11) + share(note.Ms))
	}
	say(pad("(before tracing, unmarked)", 46) + padLeft(ms(unmarked), 11) + share(unmarked))
	say(pad("traced window — attributed to spans below", 46) + padLeft(ms(attributed), 11) + share(attributed))
	say(pad("traced window — (unspanned)", 46) + padLeft(ms(unspannedMs), 11) + share(unspannedMs))
	say(strings.Repeat("─", 65))
	say(pad("process wall", 46) + padLeft(ms(data.ProcessMs), 11) + padLeft("100.0", 8))
	say("Composing this report and process exit run after the accounting;")
	say("GC pauses land inside whichever span was running when they hit.")
	sayBlank()
	say(fmt.Sprintf(
		"Attributed %s ms of %s ms (%s%%) in the traced window. The remainder ran outside every instrumented span.",
		ms(attributed), ms(data.WallMs),
		strconv.FormatFloat(100*attributed/max(data.WallMs, 1e-9), 'f', 1, 64),
	))
	sayBlank()

	// Who owns the time — the split a rewrite question turns on.
	owners := map[Owner]float64{}
	for _, entry := range data.Entries {
		owners[OwnerOf(entry.Name)] += entry.SelfMs
	}
	unattributed := max(0, data.WallMs-attributed)
	say("who owns the time")
	sayBlank()
	say(pad("owner", 14) + padLeft("self ms", 11) + padLeft("%", 8) + "  note")
	say(strings.Repeat("─", 78))
	ownerOrder := []Owner{OwnerChecker, OwnerTypescript, OwnerKernel, OwnerBoundary}
	for _, owner := range ownerOrder {
		held := owners[owner]
		say(pad(string(owner), 14) + padLeft(ms(held), 11) +
			padLeft(strconv.FormatFloat(100*held/max(data.WallMs, 1e-9), 'f', 1, 64), 8) +
			"  " + ownerNote[owner])
	}
	say(pad("(unspanned)", 14) + padLeft(ms(unattributed), 11) +
		padLeft(strconv.FormatFloat(100*unattributed/max(data.WallMs, 1e-9), 'f', 1, 64), 8) +
		"  ran outside every span — instrument before reading it")
	sayBlank()

	// SELF time is the attribution: it sums to the run, and a name's
	// share of it is that name's share of the cost.
	say("where the time is — SELF ms, the column that sums to the run")
	sayBlank()
	say(pad("span", 40) + padLeft("calls", 10) + padLeft("self ms", 11) +
		padLeft("self %", 8) + padLeft("total ms", 11))
	say(strings.Repeat("─", 80))
	for _, entry := range data.Entries {
		if entry.SelfMs < 0.5 && entry.Calls < 1000 {
			continue
		}
		say(pad(entry.Name, 40) +
			padLeft(commaInt(entry.Calls), 10) +
			padLeft(ms(entry.SelfMs), 11) +
			padLeft(strconv.FormatFloat(100*entry.SelfMs/max(data.WallMs, 1e-9), 'f', 1, 64), 8) +
			padLeft(ms(entry.TotalMs), 11))
	}
	sayBlank()

	type namedCounter struct {
		name    string
		counter *TraceCounter
	}
	var timed []namedCounter
	var plain []namedCounter
	for name, counter := range data.Counters {
		if counter.TotalMs > 0 {
			timed = append(timed, namedCounter{name, counter})
		} else {
			plain = append(plain, namedCounter{name, counter})
		}
	}

	if len(timed) > 0 {
		say("hot loops, timed in place")
		sayBlank()
		say("These are INSIDE the self time of some span above, not " +
			"additional to it — a loop that runs by the million is timed " +
			"with two clock reads rather than wrapped in a span, because " +
			"wrapping it changes how the engine optimizes it.")
		sayBlank()
		say(pad("loop", 40) + padLeft("times run", 12) + padLeft("ms", 11) + padLeft("% wall", 9))
		say(strings.Repeat("─", 72))
		sort.Slice(timed, func(i, j int) bool { return timed[i].counter.TotalMs > timed[j].counter.TotalMs })
		for _, nc := range timed {
			say(pad(nc.name, 40) +
				padLeft(commaInt(nc.counter.Calls), 12) +
				padLeft(ms(nc.counter.TotalMs), 11) +
				padLeft(strconv.FormatFloat(100*nc.counter.TotalMs/max(data.WallMs, 1e-9), 'f', 1, 64), 9))
		}
		sayBlank()
	}

	if len(plain) > 0 {
		say("counters")
		sayBlank()
		say(pad("counter", 40) + padLeft("count", 16))
		say(strings.Repeat("─", 56))
		sort.Slice(plain, func(i, j int) bool { return plain[i].counter.Calls > plain[j].counter.Calls })
		for _, nc := range plain {
			say(pad(nc.name, 40) + padLeft(commaInt(nc.counter.Calls), 16))
		}
		sayBlank()
	}

	if len(data.Files) > 0 {
		ranked := append([]FileCost(nil), data.Files...)
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].Ms > ranked[j].Ms })
		shown := ranked
		if len(shown) > SlowestFiles {
			shown = shown[:SlowestFiles]
		}
		total := 0.0
		for _, f := range data.Files {
			total += f.Ms
		}
		say(fmt.Sprintf("%d files checked, %s ms total, %s ms each on average",
			len(data.Files), ms(total), ms(total/float64(len(data.Files)))))
		sayBlank()
		// an O(n²) sweep shows here and nowhere else: the last file of a
		// batch costs what the first did only when per-file work does not
		// depend on how many files came before
		tenth := max(1, len(data.Files)/10)
		firstTenth := data.Files[:tenth]
		lastTenth := data.Files[len(data.Files)-tenth:]
		mean := func(xs []FileCost) float64 {
			sum := 0.0
			for _, f := range xs {
				sum += f.Ms
			}
			return sum / float64(len(xs))
		}
		say(fmt.Sprintf("first tenth %s ms/file, last tenth %s ms/file",
			ms(mean(firstTenth)), ms(mean(lastTenth))))
		sayBlank()
		say(fmt.Sprintf("slowest %d files", len(shown)))
		sayBlank()
		for _, file := range shown {
			say(fmt.Sprintf("  %s ms  %s", padLeft(ms(file.Ms), 9), file.Path))
		}
		sayBlank()
	}

	// allocation pressure, as far as the runtime will say: a heap this
	// size is time spent in the collector, and a GC pause lands inside
	// whichever span was running when it hit
	if line, ok := readMemory(); ok {
		say(line)
	}
	say(fmt.Sprintf(
		"%s clock reads. At ~25 ns each that is ~%s ms of the wall above — subtract it before comparing to an untraced run.",
		commaInt(data.ClockReads), ms(float64(data.ClockReads)*25e-6),
	))
	say(strings.Repeat("═", 78))
	return strings.Join(out, "\n")
}

// readMemory mirrors the TS try/process.memoryUsage() block. Go's
// runtime.MemStats has no twin for RSS or "external" (V8-specific,
// off-heap buffers) — those two columns are dropped rather than
// faked; HeapAlloc/HeapSys stand in for heapUsed/heapTotal, the pair
// Go can answer honestly from the stdlib alone.
func readMemory() (string, bool) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	mb := func(bytes uint64) string {
		return strconv.FormatFloat(float64(bytes)/1024/1024, 'f', 0, 64)
	}
	return fmt.Sprintf("heap %s MB used of %s MB", mb(stats.HeapAlloc), mb(stats.HeapSys)), true
}

// EmitTraceReport is emitTraceReport in the TS source: print the
// report where the options say. "" is the TS null (write to stderr
// instead of a file).
func EmitTraceReport() {
	text := TraceReportText()
	if WriteTo != "" {
		_ = os.WriteFile(WriteTo, []byte(text+"\n"), 0o644)
		fmt.Fprintf(os.Stderr, "trace written: %s\n", WriteTo)
		return
	}
	fmt.Fprintln(os.Stderr, text)
}
