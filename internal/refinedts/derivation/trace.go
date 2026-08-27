// The derivation trace carrier — the Go implementation of
// packages/tests/DERIVATION-TRACE.md. The specification is that
// document; this file is one of three adapters against it.
//
// A trace is a tree of spans: id, name, a two-value status, the
// refinery.* attributes, optional duration, children in evaluation
// order. Nothing else — no SDK, no exporter, no resource envelope.
// The JSON this package emits validates against
// packages/tests/diagnostics/trace.schema.json.
//
// OFF IS A NIL TEST. Every seam that pushes a span calls Active(),
// which reads one atomic and — when a trace is running at all —
// consults the per-goroutine cursor. With no trace requested the
// atomic is zero and the seam pays a single load and a branch. The
// wall gate never turns the atomic on.

package derivation

import (
	"sync"
	"sync/atomic"
	"time"
)

// Status is the span's two-value verdict — the only two states.
type Status string

const (
	Answered Status = "answered"
	Declined Status = "declined"
)

// The refinery.* attribute keys, spelled once.
const (
	AttrLanguage  = "refinery.language"
	AttrPosition  = "refinery.position"
	AttrConstruct = "refinery.construct"
	AttrRange     = "refinery.range"
	AttrAnswer    = "refinery.answer"
	AttrGate      = "refinery.gate"
	AttrOperand   = "refinery.operand"
	AttrHeld      = "refinery.held"
	AttrQuestion  = "refinery.question"
	AttrLastTouch = "refinery.last-touch"
)

// Span is one step of the derivation. The JSON field names are the
// schema's own.
type Span struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     Status            `json:"status"`
	DurationNs *int64            `json:"durationNs,omitempty"`
	Attributes map[string]string `json:"attributes"`
	Children   []*Span           `json:"children,omitempty"`
}

// Trace is the whole artifact: the language, the judged position it
// explains, the root span, and — when the main root's leaf is a bare
// name — the binding-ledger roots behind that name.
type Trace struct {
	Language string  `json:"language"`
	Position string  `json:"position"`
	Root     *Span   `json:"root"`
	Chain    []*Span `json:"chain,omitempty"`
}

// Recorder holds one trace under construction: the id counter, the
// root, and the open-span stack. One Recorder belongs to one
// goroutine's walk for the length of that walk, so its fields need no
// lock of their own — Begin/End are the only writers and they run on
// the owning goroutine. The registry that maps goroutines to Recorders
// is the shared part, and that is a sync.Map.
type Recorder struct {
	language string
	position string
	// requestedLine is the 1-based line -explain asked about; a judged
	// position on any other line is not recorded. 0 records every
	// position (the tests' whole-file mode).
	requestedLine int

	nextID int
	root   *Span
	stack  []*Span

	// finished holds the traces whose root has closed, in the order
	// they closed. -explain prints them all: one judged position can
	// sit beside another on the same line.
	finished []Trace

	// guards is THE GUARD LEDGER: the last narrowing span that wrote a
	// fact about each place, by place name.
	//
	// A guard is part of the derivation of every later read of the place
	// it narrowed, and it sits OUTSIDE that read's own range — the guard
	// on line 16 establishes what the sink on line 17 carries. Range
	// containment cannot reach it, so the fact is remembered where it is
	// written (the env write in walk/assume_condition.go) and reclaimed
	// where it is read (the judge). Without it the answered path is less
	// legible than the declined one: the trace would state the set the
	// sink carried and never say which guard proved it.
	guards map[string]*Span

	// lastNarrowing is the narrowing span this recorder most recently
	// closed — what the env write that follows it reads to name the guard
	// it is landing the fact of.
	lastNarrowing *Span

	// bindings is THE BINDING LEDGER (DERIVATION-TRACE.md, "The
	// projection rule"): the span subtree that produced the value written
	// into each place, by place name.
	//
	// A read whose derivation stops at a bare name has nothing below it —
	// the name was resolved out of the environment, and the construct that
	// actually produced the value sits at the binding statement, outside
	// the judged range. Range containment (AbsorbInto) cannot reach it and
	// the guard ledger holds narrowings only, so the producing subtree is
	// remembered where it is written (WriteBinding) and reclaimed where
	// the leaf is a bare name (the judge, into the document's `chain`).
	bindings map[string]*Span

	// touches is THE LAST-TOUCH LEDGER: the most recent environment
	// write to reach each place, by place name — kind ("written",
	// "forgotten", or "havocked"), the construct that did it, and that
	// construct's range. Where the binding ledger remembers WHAT
	// produced a value, this remembers WHAT LAST MOVED the place at
	// all, including a move that leaves nothing behind (a havoc, a
	// forget) — a leaf a decline or an unspellable answer can name even
	// when there is no producing subtree to chain into. Mirrors
	// bindings' own current()-gated, off-costs-nothing shape exactly.
	touches map[string]lastTouch

	// site is the construct/range TouchSite most recently named for the
	// mutation in flight — what a last-touch chokepoint reads when it
	// records a touch. Nil when no site is currently named.
	site *touchSite
}

// lastTouch is one recorded environment write: what kind of write it
// was, the construct that performed it, and that construct's range.
// construct and rng travel empty when the chokepoint that recorded the
// touch had no site to name — TouchSite below is how a caller supplies
// one.
type lastTouch struct {
	kind      string
	construct string
	rng       string
}

// BindingSubtree is one remembered environment write: the span that
// produced the written value, and the place it was written to. The place
// travels with it so a recursive reclaim can refuse a cycle by place.
type BindingSubtree struct {
	Place string
	Span  *Span
}

// NarrowingName is the span name the narrowing dispatch seam opens
// under — what marks a closed span as a guard the ledger can remember.
const NarrowingName = "narrowings"

// tracing is 1 while any Recorder is registered. Every seam reads this
// one atomic first, so a run with no -explain pays a load and a branch
// per seam and never touches the map.
var tracing atomic.Int32

// timing is 1 when spans should carry durationNs.
//
// PER THE SPEC, durationNs "is present only under timing": the field
// populates when the adapter's existing timing flag (-trace here) is
// passed TOGETHER with -explain, and -explain alone leaves it absent.
// Keeping the two separate matters because timing changes what the
// trace COSTS to collect — a clock read per span — and because a trace
// carrying wall figures is not byte-comparable against one that does
// not, which is what conformance diffs read.
var timing atomic.Int32

// SetTiming turns durationNs on or off for the spans opened after it.
// Called once from the entry point when -trace and -explain are both
// passed.
func SetTiming(on bool) {
	if on {
		timing.Store(1)
		return
	}
	timing.Store(0)
}

// recorders maps a goroutine id to the Recorder walking on it.
var recorders sync.Map // uint64 -> *Recorder

// Active answers whether THIS goroutine is recording a derivation. The
// single call every seam makes; false is the whole off path.
func Active() bool {
	if tracing.Load() == 0 {
		return false
	}
	_, held := recorders.Load(goroutineID())
	return held
}

// current is this goroutine's Recorder, or nil.
func current() *Recorder {
	if tracing.Load() == 0 {
		return nil
	}
	if held, ok := recorders.Load(goroutineID()); ok {
		return held.(*Recorder)
	}
	return nil
}

// CurrentRecorder is this goroutine's Recorder, or nil — the seam that
// needs to ask WantsLine before it opens a root.
func CurrentRecorder() *Recorder { return current() }

// Open answers whether a span is currently open on this recorder — the
// test BeginNode makes before deciding whether an off-line node records.
func (r *Recorder) Open() bool {
	return r != nil && len(r.stack) > 0
}

// AbsorbInto moves every already-finished trace whose root range sits
// INSIDE the given range under the currently open span, in the order
// they closed.
//
// The judge runs AFTER the expression walk that produced the value it
// judges: evaluateExpression has already opened, closed, and finished
// its own trace by the time checkAssignability opens a root at the same
// node. Without this the trace would carry the judged position's own
// decline and none of the sub-reads that led to it — question 1 (WHERE
// the derivation stopped, on which sub-expression) would go unanswered.
// So the judge reclaims them: they are its children by derivation even
// though they closed before it opened.
func AbsorbInto(rng string) {
	recorder := current()
	if recorder == nil || len(recorder.stack) == 0 || rng == "" {
		return
	}
	parent := recorder.stack[len(recorder.stack)-1]
	kept := recorder.finished[:0]
	for _, trace := range recorder.finished {
		if trace.Root != nil && rangeContains(rng, trace.Root.Attributes[AttrRange]) {
			// a root that becomes a child sheds the root-only attributes
			delete(trace.Root.Attributes, AttrLanguage)
			delete(trace.Root.Attributes, AttrPosition)
			parent.Children = append(parent.Children, trace.Root)
			continue
		}
		kept = append(kept, trace)
	}
	recorder.finished = kept
}

// BeginRecording registers a Recorder for this goroutine and returns
// it together with the closer that unregisters it. language is the
// adapter's own tag; position is the judged position the trace
// explains; requestedLine gates which positions are recorded (0 = all).
func BeginRecording(language string, position string, requestedLine int) (*Recorder, func()) {
	recorder := &Recorder{language: language, position: position, requestedLine: requestedLine}
	id := goroutineID()
	recorders.Store(id, recorder)
	tracing.Add(1)
	return recorder, func() {
		recorders.Delete(id)
		tracing.Add(-1)
	}
}

// Adopt registers an EXISTING Recorder on the calling goroutine. The
// walk spawns worker goroutines per entry file; each one adopts the
// run's Recorder so its spans land in the same trace. The returned
// closer unregisters this goroutine only.
func (r *Recorder) Adopt() func() {
	if r == nil {
		return func() {}
	}
	id := goroutineID()
	recorders.Store(id, r)
	tracing.Add(1)
	return func() {
		recorders.Delete(id)
		tracing.Add(-1)
	}
}

// WantsLine answers whether a judged position on this line is one the
// request asked about.
func (r *Recorder) WantsLine(line int) bool {
	if r == nil {
		return false
	}
	return r.requestedLine == 0 || r.requestedLine == line
}

// Traces are the completed traces, in the order their roots closed.
//
// A trace is a JUDGED POSITION's derivation. The walk opens spans for
// sub-reads on the requested line that no judge ever claims — a guard's
// own condition, a void statement — and those closed as roots of their
// own because nothing was open above them. They are not judged
// positions, so they are not traces: JudgedName is what a root has to
// carry to count. Dropping them here rather than refusing to open them
// keeps AbsorbInto's reclaim working, which is what puts the sub-reads
// under the position that actually asked for them.
//
// A JUDGE CAN CLOSE AS SOMEONE ELSE'S CHILD. Checking a call's
// arguments (walk/call_argument_contracts.go's CheckContractArguments)
// runs from inside evaluateExpression's own span for the call
// expression — evaluating a call necessarily checks its arguments
// before the call's return value is known, so the argument's judge
// seam opens while the call's own evaluateExpression span is still on
// the stack. When the judge closes it is not the bottom of the stack,
// so End() files it under the call's span rather than into r.finished
// as a trace of its own (the same "discard" End() gives any other
// off-root span) — the derivation is real and reachable, but Traces()'s
// root-only scan never reaches it. So this walks every finished tree
// looking for a checkAssignability span at ANY depth, not only at the
// root, and reports each one it finds as its own judged position — the
// nested span becomes that trace's Root, keeping its already-recorded
// children (the judge's own reads) as its subtree exactly as if it had
// closed as a root itself.
func (r *Recorder) Traces() []Trace {
	if r == nil {
		return nil
	}
	var judged []Trace
	for _, trace := range r.finished {
		if trace.Root != nil && trace.Root.Name == JudgedName {
			// the ordinary case: the judge closed at the bottom of the
			// stack, so it is the trace End() already built — keep it
			// whole, Chain included (numberTrace below numbers the
			// chain too; collectNestedJudged only ever finds spans with
			// no chain of their own, since the binding ledger reclaims
			// only at a root's own close). A judge can still recurse
			// into another judge below itself — CheckObjectTarget calls
			// CheckAssignability per key — so the root's OWN children
			// are searched too, the same as any non-judge root's are.
			judged = append(judged, trace)
			for _, child := range trace.Root.Children {
				collectNestedJudged(child, trace.Language, &judged)
			}
			continue
		}
		collectNestedJudged(trace.Root, trace.Language, &judged)
	}
	for at := range judged {
		numberTrace(judged[at])
	}
	return judged
}

// collectNestedJudged walks a finished trace's span whose ROOT is not
// itself a judge (trace.Root.Name != JudgedName, already ruled out by
// Traces' own caller) looking for a checkAssignability span at any
// depth below it, and appends one Trace per span it finds. language
// travels down from the enclosing finished trace since a nested judge's
// own span carries no refinery.language (only a root span does, per
// Begin's root-only attributes) — and carries no Chain either, since
// the binding ledger only ever reclaims at End()'s at==0 close.
func collectNestedJudged(span *Span, language string, out *[]Trace) {
	if span == nil {
		return
	}
	if span.Name == JudgedName {
		position := span.Attributes[AttrPosition]
		if position == "" {
			position = span.Attributes[AttrRange]
		}
		*out = append(*out, Trace{Language: language, Position: position, Root: span})
	}
	for _, child := range span.Children {
		collectNestedJudged(child, language, out)
	}
}

// numberTrace assigns the schema's ordinal ids over the document that is
// actually EMITTED: the root, then depth-first through its subtree, then
// each chained root after the main root's subtree.
//
// IDS ARE A PROPERTY OF THE EMITTED DOCUMENT, NOT OF THE WALK. Two
// identical trees produce identical ids, and comparisons never normalize
// them away. Numbering at Begin could not hold that: the walk opens
// spans it then discards — an off-line sub-read no judged position
// reclaims, a binding producer whose place is never read — and each
// discard burned an ordinal, so a trace's ids depended on how much had
// been thrown away before the walk reached the position. Adding the
// binding ledger's line-gate exemption shifted every id in
// A6.guard.band:17 by a constant while its tree stayed identical, which
// is exactly the drift this numbering removes.
//
// The spans are renumbered in place, and doing it again changes nothing:
// the same tree walked the same way yields the same ordinals. That
// matters because Traces() has more than one caller per run — -explain
// renders the tree and -explain-json encodes it, each asking for the
// traces separately.
func numberTrace(trace Trace) {
	next := 0
	var number func(span *Span)
	number = func(span *Span) {
		if span == nil {
			return
		}
		next++
		span.ID = spanID(next)
		for _, child := range span.Children {
			number(child)
		}
	}
	number(trace.Root)
	for _, bound := range trace.Chain {
		number(bound)
	}
}

// JudgedName is the span name the judge opens its root under — the one
// name that marks a trace as explaining a judged position.
const JudgedName = "checkAssignability"

// RecordGuard remembers which narrowing span proved a fact about a
// place. Called from the env write that lands the guard's fact, so the
// ledger names exactly the guard that wrote what a later read carries.
// The span recorded is the innermost narrowing span currently open, or
// the one most recently closed on this recorder.
func RecordGuard(place string, span *Span) {
	recorder := current()
	if recorder == nil || span == nil || place == "" {
		return
	}
	if recorder.guards == nil {
		recorder.guards = map[string]*Span{}
	}
	recorder.guards[place] = span
}

// LastNarrowingSpan is the most recent narrowing span this recorder
// closed — what the env write hands RecordGuard.
func LastNarrowingSpan() *Span {
	recorder := current()
	if recorder == nil {
		return nil
	}
	return recorder.lastNarrowing
}

// AttachGuard hangs the guard that proved a fact about `place` under the
// currently open span, so an ANSWERED trace shows the guard fact meeting
// the read. Attached at most once per span, and never a guard already
// present in the tree — a guard reclaimed by range containment stays
// where the containment put it.
func AttachGuard(place string) {
	recorder := current()
	if recorder == nil || len(recorder.stack) == 0 {
		return
	}
	guard := recorder.guards[place]
	if guard == nil {
		return
	}
	parent := recorder.stack[len(recorder.stack)-1]
	if spanPresent(parent, guard) {
		return
	}
	// a guard that closed as a root of its own is no longer a trace in
	// its own right once a judged position claims it
	kept := recorder.finished[:0]
	for _, trace := range recorder.finished {
		if trace.Root == guard {
			delete(guard.Attributes, AttrLanguage)
			delete(guard.Attributes, AttrPosition)
			continue
		}
		kept = append(kept, trace)
	}
	recorder.finished = kept
	parent.Children = append(parent.Children, guard)
}

// spanPresent answers whether a span is already somewhere in a tree.
func spanPresent(root *Span, wanted *Span) bool {
	if root == nil {
		return false
	}
	if root == wanted {
		return true
	}
	for _, child := range root.Children {
		if spanPresent(child, wanted) {
			return true
		}
	}
	return false
}

// Begin pushes a span. name is the adapter-local reader id; construct
// is the sub-expression's own source spelling; rng is its
// path:line:col-line:col. The returned handle is what the seam records
// its answer or decline on; a nil handle is the off path and every
// method on it is a no-op.
func Begin(name string, construct string, rng string) *Handle {
	recorder := current()
	if recorder == nil {
		return nil
	}
	recorder.nextID++
	// A span with no source position of its own — a kernel ask, which
	// crosses to the dylib rather than reading a sub-expression — takes
	// the range of the reader that opened it. The schema requires a
	// path:line:col-line:col range on EVERY span, so a blank is never a
	// legal answer, and the asking reader's own range is the truthful
	// one: that is where the question came from.
	if rng == "" && len(recorder.stack) > 0 {
		rng = recorder.stack[len(recorder.stack)-1].Attributes[AttrRange]
	}
	span := &Span{
		Name:   name,
		Status: Declined,
		Attributes: map[string]string{
			AttrConstruct: construct,
			AttrRange:     rng,
		},
	}
	// A PROVISIONAL ID, in walk order. What the emitted document carries
	// is numberTrace's, assigned in tree order when Traces() hands the
	// trace out; this one only keeps open spans distinguishable while the
	// walk is still building them, and never reaches an artifact.
	span.ID = spanID(recorder.nextID)
	if len(recorder.stack) == 0 {
		// the outermost open span is this trace's root, and it carries
		// the root-only attributes
		span.Attributes[AttrLanguage] = recorder.language
		span.Attributes[AttrPosition] = recorder.position
		recorder.root = span
	} else {
		parent := recorder.stack[len(recorder.stack)-1]
		parent.Children = append(parent.Children, span)
	}
	recorder.stack = append(recorder.stack, span)
	handle := &Handle{recorder: recorder, span: span}
	// the clock is read only under timing, so an untimed trace pays no
	// syscall per span
	if timing.Load() != 0 {
		handle.startedAt = time.Now()
	}
	return handle
}

// OpenSpan is the innermost span currently open on this goroutine —
// what the decline helper records onto without being handed a handle.
// Nil when nothing is open.
func OpenSpan() *Span {
	recorder := current()
	if recorder == nil || len(recorder.stack) == 0 {
		return nil
	}
	return recorder.stack[len(recorder.stack)-1]
}

// DeclineOpen records a decline on the innermost open span. The decline
// helper's own writer; a seam holding a handle calls Handle.Decline
// instead.
func DeclineOpen(gate string, operandRange string, held string) {
	span := OpenSpan()
	if span == nil {
		return
	}
	span.Status = Declined
	delete(span.Attributes, AttrAnswer)
	if gate != "" {
		span.Attributes[AttrGate] = gate
	}
	if operandRange != "" {
		span.Attributes[AttrOperand] = operandRange
	}
	if held != "" {
		span.Attributes[AttrHeld] = held
	}
}

// Handle is one open span. Every method tolerates a nil receiver, so a
// seam writes `h := derivation.Begin(...); defer h.End()` without a
// second nil test.
type Handle struct {
	recorder *Recorder
	span     *Span
	// startedAt is the zero Time unless timing is on; End reads it to
	// fill durationNs.
	startedAt time.Time
}

// Answer records the derived set or window and marks the span
// answered.
func (h *Handle) Answer(spelling string) {
	if h == nil {
		return
	}
	h.span.Status = Answered
	if spelling != "" {
		h.span.Attributes[AttrAnswer] = spelling
	}
}

// Decline records the named premise that failed, the failing operand's
// range, and what that operand held, and marks the span declined. Any
// of the three may be empty; an absent gate on a declined span is a
// visible work item, per the spec, not an error here.
func (h *Handle) Decline(gate string, operandRange string, held string) {
	if h == nil {
		return
	}
	h.span.Status = Declined
	if gate != "" {
		h.span.Attributes[AttrGate] = gate
	}
	if operandRange != "" {
		h.span.Attributes[AttrOperand] = operandRange
	}
	if held != "" {
		h.span.Attributes[AttrHeld] = held
	}
}

// Question records a kernel ask's wire text both ways on this span —
// used by the kernel.<op> spans the ask chokepoint opens.
func (h *Handle) Question(question string, answer string) {
	if h == nil {
		return
	}
	if question != "" {
		h.span.Attributes[AttrQuestion] = question
	}
	if answer != "" {
		h.span.Attributes[AttrAnswer] = answer
	}
}

// Span is the open span itself, for the projection to read before the
// handle closes.
func (h *Handle) Span() *Span {
	if h == nil {
		return nil
	}
	return h.span
}

// End pops the span. When it was the root, the finished trace is
// recorded and the recorder is ready for the next judged position.
func (h *Handle) End() {
	if h == nil {
		return
	}
	recorder := h.recorder
	if !h.startedAt.IsZero() {
		elapsed := time.Since(h.startedAt).Nanoseconds()
		h.span.DurationNs = &elapsed
	}
	at := len(recorder.stack) - 1
	for at >= 0 && recorder.stack[at] != h.span {
		at--
	}
	if at < 0 {
		return
	}
	recorder.stack = recorder.stack[:at]
	if h.span.Name == NarrowingName {
		recorder.lastNarrowing = h.span
	}
	if at == 0 {
		root := recorder.root
		// THE BINDING LEDGER'S RECLAIM (binding_ledger.go). A judged
		// position whose derivation stopped at a bare name reclaims the
		// binding statement's own derivation here, at the moment its root
		// closes: the ledger then holds exactly the writes that ran before
		// the read, which is what "the binding behind this name" means.
		// Only a judged position chains — a sub-read that closed as a root
		// of its own is about to be reclaimed by the judge above it, and a
		// chain on it would be claimed twice.
		var chain []*Span
		if root != nil && root.Name == JudgedName {
			chain = recorder.ChainFor(root)
			// THE LAST-TOUCH ATTACHMENT (last_touch.go). Runs on the same
			// bare-name leaf the chain reclaim just tested: a leaf that
			// stopped at a plain name and carries a recorded touch for it
			// gets refinery.last-touch, so the emitted document names
			// where the value was last moved even with no producing
			// subtree to chain into (a havoc, a forget).
			recorder.attachLastTouch(root)
		}
		recorder.finished = append(recorder.finished, Trace{
			Language: recorder.language,
			Position: recorder.position,
			Root:     root,
			Chain:    chain,
		})
		recorder.root = nil
	}
}

// SetPosition names the judged position the CURRENT trace explains.
// The judge learns the position after its span is already open (the
// range is computed from the node it is handed), so the root's
// refinery.position is filled in rather than passed to Begin.
func SetPosition(position string) {
	recorder := current()
	if recorder == nil || recorder.root == nil {
		return
	}
	recorder.position = position
	recorder.root.Attributes[AttrPosition] = position
}

// spanID is the schema's ordinal id: "s1", "s2", …
func spanID(n int) string {
	// small-n fast path; the general case falls through to strconv
	if n < 10 {
		return "s" + string(rune('0'+n))
	}
	digits := make([]byte, 0, 8)
	for v := n; v > 0; v /= 10 {
		digits = append(digits, byte('0'+v%10))
	}
	out := make([]byte, 0, len(digits)+1)
	out = append(out, 's')
	for i := len(digits) - 1; i >= 0; i-- {
		out = append(out, digits[i])
	}
	return string(out)
}
