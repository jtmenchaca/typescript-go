// Mutable tracing records: flat self-time, the span stack, the bounded
// tree, per-file costs, and pre-trace notes. Enter/Leave own the
// recursion and tree bookkeeping so the recording API stays thin.
//
// Ported 1:1 from service/trace_state.ts. The records are globals with
// no lock, exactly as the TS module's are: correct while one check is
// in flight at a time (the sweep's discipline; a sharded run gives
// each process its own globals). A goroutine-parallel walk inside one
// process would need this revisited — that is a design note, not a
// silent assumption.

package tracing

import "time"

// Grain names how fine a span records: "phase" | "step" | "node"
// (cache_tuning.ts's Grain; the tuning module lives in service/ and
// is not ported yet, so the two TRACE defaults are inlined below with
// a comment at each).
type Grain string

const (
	GrainPhase Grain = "phase"
	GrainStep  Grain = "step"
	GrainNode  Grain = "node"
)

type TraceSpan struct {
	Name     string
	TotalMs  float64
	Children []*TraceSpan
}

type TraceCounter struct {
	Calls   int64
	TotalMs float64
}

// TraceEntry is one name's share of the run.
type TraceEntry struct {
	Name  string
	Grain Grain
	Calls int64
	// TotalMs is time inside this span and everything it called.
	// Counted once per outermost entry, so recursion does not
	// multiply it.
	TotalMs float64
	// SelfMs is time inside this span and NOT inside any span it
	// called. The column that attributes a run.
	SelfMs float64
}

var GrainLevel = map[Grain]int{
	GrainPhase: 1,
	GrainStep:  2,
	GrainNode:  3,
}

// A tree deeper than this stops opening nodes and folds into its
// deepest ancestor. The flat record is unaffected.
const treeDepthCap = 16

// TRACE.enabled / TRACE.grain (cache_tuning.ts): off by default, at
// the step grain when turned on — the same resting state the TS
// tuning file ships.
var (
	Enabled = false
	Level   = GrainLevel[GrainStep]
)

func SetEnabled(value bool) { Enabled = value }
func SetLevel(value int)    { Level = value }

/* ── the flat record ─────────────────────────────────────────────── */

var Flat = map[string]*TraceEntry{}

// Active holds how many frames of each name are on the stack right
// now — the recursion guard for inclusive time and for the tree.
var Active = map[string]int{}
var Counters = map[string]*TraceCounter{}

type Frame struct {
	Name      string
	StartedAt time.Time
	// ChildMs is time this frame's children took, so self time is a
	// subtraction.
	ChildMs   float64
	Reentrant bool
	TreeHeld  *TraceSpan
	TreeDepth int
}

var Stack []*Frame

/* ── the tree record ─────────────────────────────────────────────── */

var (
	Root        *TraceSpan
	TreeCurrent *TraceSpan
	TreeDepth   int
)

func SetRoot(value *TraceSpan)        { Root = value }
func SetTreeCurrent(value *TraceSpan) { TreeCurrent = value }

// A tree node per (parent, name), created once and reused — the tree
// aggregates repeats rather than growing a node per call. (TS holds
// this in a WeakMap; the tree is reset-bounded here, so a plain map
// keyed on the parent pointer carries the same lifetime.)
var treeChildren = map[*TraceSpan]map[string]*TraceSpan{}

/* ── per-file record ─────────────────────────────────────────────── */

var FileMs = map[string]float64{}

// FileOrder holds each file's cost in the order the batch met them,
// so the report can say whether per-file cost grows as the batch
// goes on.
type FileCost struct {
	Path string
	Ms   float64
}

var FileOrder []FileCost

/* ── before the traced window ────────────────────────────────────── */

// PreTraceNotes are named segments that ran BEFORE TraceStart —
// runtime boot, module imports, batch setup. Not cleared by reset:
// noted before tracing.
type PreTraceNote struct {
	Name string
	Ms   float64
}

var PreTraceNotes []PreTraceNote

/* ── run-level bookkeeping ───────────────────────────────────────── */

var RunStartedAt time.Time

// ClockReads counts clock reads taken, so the report can price its
// own overhead.
var ClockReads int64

func AddClockReads(n int64) { ClockReads += n }

func msSince(t time.Time) float64 {
	return float64(time.Since(t)) / float64(time.Millisecond)
}

func Enter(name string, grain Grain) *Frame {
	already := Active[name]
	Active[name] = already + 1
	var treeHeld *TraceSpan
	if already == 0 && grain != GrainNode && TreeCurrent != nil &&
		TreeDepth < treeDepthCap {
		children := treeChildren[TreeCurrent]
		if children == nil {
			children = map[string]*TraceSpan{}
			treeChildren[TreeCurrent] = children
		}
		mine := children[name]
		if mine == nil {
			mine = &TraceSpan{Name: name}
			children[name] = mine
			TreeCurrent.Children = append(TreeCurrent.Children, mine)
		}
		treeHeld = TreeCurrent
		TreeCurrent = mine
		TreeDepth++
	}
	ClockReads++
	frame := &Frame{
		Name:      name,
		StartedAt: time.Now(),
		Reentrant: already > 0,
		TreeHeld:  treeHeld,
		TreeDepth: TreeDepth,
	}
	Stack = append(Stack, frame)
	return frame
}

func Leave(frame *Frame, grain Grain) {
	ClockReads++
	elapsed := msSince(frame.StartedAt)
	Stack = Stack[:len(Stack)-1]
	if len(Stack) > 0 {
		Stack[len(Stack)-1].ChildMs += elapsed
	}

	remaining := Active[frame.Name] - 1
	if remaining == 0 {
		delete(Active, frame.Name)
	} else {
		Active[frame.Name] = remaining
	}

	entry := Flat[frame.Name]
	if entry == nil {
		entry = &TraceEntry{Name: frame.Name, Grain: grain}
		Flat[frame.Name] = entry
	}
	entry.Calls++
	entry.SelfMs += elapsed - frame.ChildMs
	// inclusive time counts the OUTERMOST entry only, so a recursive
	// span does not multiply its own total
	if !frame.Reentrant {
		entry.TotalMs += elapsed
	}

	if frame.TreeHeld != nil && TreeCurrent != nil {
		TreeCurrent.TotalMs += elapsed
		TreeCurrent = frame.TreeHeld
		TreeDepth--
	}
}

// ResetRecords clears flat/tree/file counters and opens a fresh root.
// Leaves PreTraceNotes alone — those document time before tracing.
func ResetRecords() {
	Flat = map[string]*TraceEntry{}
	Active = map[string]int{}
	Counters = map[string]*TraceCounter{}
	Stack = Stack[:0]
	FileMs = map[string]float64{}
	FileOrder = FileOrder[:0]
	ClockReads = 0
	treeChildren = map[*TraceSpan]map[string]*TraceSpan{}
	Root = &TraceSpan{Name: "check"}
	TreeCurrent = Root
	TreeDepth = 0
	RunStartedAt = time.Now()
}
