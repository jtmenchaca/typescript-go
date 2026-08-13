// What questions actually cost, measured.
//
// This replaces a pre-gate that ESTIMATED a question's cost from its
// syntax and declined to ask when the estimate exceeded a fixed
// ceiling. That gate was silent — a declined question and a value
// nothing was known about produced the same output — and it did not
// bound what it claimed to: a question passed it, passed the kernel's
// own copy of it, and still exhausted the kernel's memory.
//
// So: ask, and record what it cost. Every question that crosses into
// the wasm is timed and its wire measured. Nothing here declines
// anything.
//
// An op earns its row the first time it is asked — the op string at
// the ask site IS the registration, and the units are the same for
// every question: wall-clock milliseconds around the crossing, and
// the byte length of the wire that crossed. See OpNames for the ops
// this package asks under.
package kernelbridge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// OpNames are the op strings the asks record under — every question
// this package sends, in the order kernel_asks.go binds them. The
// costs map builds itself from the ask sites, so this list is not read
// at ask time; it says which rows a run can produce and keeps the
// report's op column wide enough for the longest of them.
var OpNames = []string{
	"member", "scalarEmpty", "scalarSubset", "scalarDisjoint",
	"seqEmpty", "seqSubset", "structural", "checkAssignability",
	"calendar", "transfer", "linear", "envelope", "bounds", "members",
	"decimal", "invariant", "solveLoop", "narrow", "joinState",
	"narrowState", "walk", "summarize", "applySummary", "validateChain",
}

// QuestionCost mirrors the TS QuestionCost interface.
type QuestionCost struct {
	Op    string
	Ms    float64
	Bytes int
	// Wire is the question itself, so an expensive one can be read
	// rather than guessed at. Kept only for the slowest few.
	Wire string
	// HasWire distinguishes an absent Wire from an intentionally empty
	// one, mirroring the TS optional `wire?: string`.
	HasWire bool
}

type totals struct {
	asked      int
	ms         float64
	bytes      int
	worstMs    float64
	worstBytes int
}

// costsMu guards costTotals, worstQuestions, and questionTrace —
// instrument-only state, but every goroutine's kernel asks now call
// RecordQuestion concurrently, and an unguarded map write from two
// goroutines at once corrupts the map (a genuine crash, not just skew).
var costsMu sync.Mutex

var costTotals = map[string]*totals{}

// worstQuestions holds the most expensive questions seen, worst first.
var worstQuestions []QuestionCost

const worstKept = 20

// questionTrace: when set, every question streams here as it
// completes — the live view a hang hunt needs. A question that never
// returns is the one AFTER the last line printed; a stream that keeps
// flowing while a row never finishes says the loop is in the checker,
// not the kernel.
var questionTrace func(line string)

// SetQuestionTrace is setQuestionTrace in the TS source.
func SetQuestionTrace(trace func(line string)) {
	costsMu.Lock()
	defer costsMu.Unlock()
	questionTrace = trace
}

// TraceQuestionLine is traceQuestionLine in the TS source: stream one
// line to the live trace, if one is set — the seam cache hits report
// through, since they never reach RecordQuestion.
func TraceQuestionLine(line string) {
	costsMu.Lock()
	trace := questionTrace
	costsMu.Unlock()
	if trace != nil {
		trace(line)
	}
}

// RecordQuestion is recordQuestion in the TS source.
func RecordQuestion(cost QuestionCost) {
	costsMu.Lock()
	defer costsMu.Unlock()
	if questionTrace != nil {
		questionTrace(fmt.Sprintf("kernel %s %sms %db", cost.Op, msString(cost.Ms), cost.Bytes))
	}
	held, ok := costTotals[cost.Op]
	if !ok {
		held = &totals{}
		costTotals[cost.Op] = held
	}
	held.asked += 1
	held.ms += cost.Ms
	held.bytes += cost.Bytes
	if cost.Ms > held.worstMs {
		held.worstMs = cost.Ms
	}
	if cost.Bytes > held.worstBytes {
		held.worstBytes = cost.Bytes
	}

	if len(worstQuestions) < worstKept ||
		cost.Ms > worstQuestions[len(worstQuestions)-1].Ms {
		worstQuestions = append(worstQuestions, cost)
		sort.Slice(worstQuestions, func(i, j int) bool {
			return worstQuestions[i].Ms > worstQuestions[j].Ms
		})
		if len(worstQuestions) > worstKept {
			worstQuestions = worstQuestions[:worstKept]
		}
	}
}

// QuestionCostsByOp is the `byOp` half of questionCosts()'s return in
// the TS source — Go has no anonymous-struct-with-map-and-slice return
// idiom as convenient as TS's object literal, so this and
// QuestionCostsWorst are two functions instead of one struct-returning
// one. Op totals as a struct copy: (asked, ms, bytes, worstMs, worstBytes).
type OpTotals struct {
	Asked      int
	Ms         float64
	Bytes      int
	WorstMs    float64
	WorstBytes int
}

// QuestionCosts is questionCosts in the TS source.
func QuestionCosts() (byOp map[string]OpTotals, worst []QuestionCost) {
	costsMu.Lock()
	defer costsMu.Unlock()
	byOp = make(map[string]OpTotals, len(costTotals))
	for op, t := range costTotals {
		byOp[op] = OpTotals{
			Asked: t.asked, Ms: t.ms, Bytes: t.bytes,
			WorstMs: t.worstMs, WorstBytes: t.worstBytes,
		}
	}
	worst = append([]QuestionCost(nil), worstQuestions...)
	return byOp, worst
}

// ClearQuestionCosts is clearQuestionCosts in the TS source.
func ClearQuestionCosts() {
	costsMu.Lock()
	defer costsMu.Unlock()
	costTotals = map[string]*totals{}
	worstQuestions = nil
}

func msString(x float64) string {
	if x >= 10 {
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	return strconv.FormatFloat(x, 'f', 2, 64)
}

func kbString(b int) string {
	return strconv.FormatFloat(float64(b)/1024, 'f', 1, 64)
}

// QuestionCostReport is questionCostReport in the TS source: what the
// questions cost this run. Guarded like the other readers, even though
// it is normally called once after a sweep finishes — the report walks
// costTotals' pointer values (*totals) directly, and a straggling
// RecordQuestion from a not-quite-finished goroutine would otherwise
// race those field reads against its own writes.
func QuestionCostReport() string {
	costsMu.Lock()
	defer costsMu.Unlock()
	type entry struct {
		op string
		t  *totals
	}
	entries := make([]entry, 0, len(costTotals))
	for op, t := range costTotals {
		entries = append(entries, entry{op, t})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].t.ms > entries[j].t.ms })
	if len(entries) == 0 {
		return "no questions asked"
	}
	asked := 0
	spent := 0.0
	for _, e := range entries {
		asked += e.t.asked
		spent += e.t.ms
	}
	var out []string
	out = append(out, fmt.Sprintf(
		"%s questions asked, %s ms in the kernel", commaInt(asked), msString(spent),
	))
	out = append(out, "")
	out = append(out, padEnd("question", 18)+
		padStart("asked", 9)+padStart("total ms", 11)+
		padStart("slowest ms", 12)+padStart("largest KB", 12))
	out = append(out, strings.Repeat("─", 62))
	for _, e := range entries {
		out = append(out, padEnd(e.op, 18)+
			padStart(commaInt(e.t.asked), 9)+
			padStart(msString(e.t.ms), 11)+
			padStart(msString(e.t.worstMs), 12)+
			padStart(kbString(e.t.worstBytes), 12))
	}
	if len(worstQuestions) > 0 {
		out = append(out, "")
		out = append(out, "slowest single questions")
		limit := 6
		if len(worstQuestions) < limit {
			limit = len(worstQuestions)
		}
		for _, q := range worstQuestions[:limit] {
			out = append(out, fmt.Sprintf(
				"  %s ms  %s KB  %s",
				padStart(msString(q.Ms), 9), padStart(kbString(q.Bytes), 8), q.Op,
			))
			if q.HasWire {
				out = append(out, "      "+q.Wire)
			}
		}
	}
	return strings.Join(out, "\n")
}

// commaInt mirrors `n.toLocaleString("en-US")` for a plain integer:
// thousands-grouped with commas.
func commaInt(n int) string {
	s := strconv.Itoa(n)
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

func padEnd(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padStart(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}
