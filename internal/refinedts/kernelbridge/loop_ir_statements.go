// Loop IR statements: the lowered kernel-walk statement grammar —
// assignments, branches, loops (effect-bodied, statement-bodied,
// counted, accumulating), and calls — and its wire encoding.
package kernelbridge

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// IrStatementKind is the tag of an IrStatement.
type IrStatementKind string

const (
	IrStatementAssign IrStatementKind = "assign"
	IrStatementBranch IrStatementKind = "branch"
	// IrStatementBranchBoth is the branch whose CONDITION the walk does
	// not read: `if (m.has(k)) { … } else { … }` and every other
	// write-free test no leaf lowers. It reuses the Then and Else fields
	// and carries no On, Test, or operand — the walk claims nothing about
	// the condition, so both arms walk from the state as it stood and
	// their exits join. A concrete run may take either arm and the join
	// admits both, so an unreadable test costs precision at the merge and
	// never costs the body its lowering.
	IrStatementBranchBoth IrStatementKind = "branchBoth"
	IrStatementLoop       IrStatementKind = "loop"
	// IrStatementLoopStmts is the loop whose body is STATEMENTS rather
	// than one effect per binding. Any number of trips may run — zero
	// included — and the kernel havocs the body's own write set, which
	// it computes from the body statements itself and never trusts from
	// this wire. Written and Body stay nil: they are the effect-bodied
	// loop's fields.
	//
	// Cond, After and CondCmp DO ride here when the head reads, in the
	// effect loop's own spelling. The kernel refines the havoc with
	// them at the EXIT — a loop leaves only when its head fails, so
	// every slot intersects its falsity set and the two-slot head's
	// negation tightens on top.
	//
	// Cond also feeds the kernel's INVARIANT certificate, which the
	// certifying walk poses for this form: the entry row havocked on
	// the body's write set, cut by the head's TRUTH sets, is walked
	// through the body, and where it lands back inside itself the
	// kernel meets it into the exit. That is decided kernel-side from
	// these same fields — nothing new rides here for it — and a head
	// that does not read lowers all three empty, which certifies
	// nothing and leaves the plain havoc exit this form always had.
	// The certifying walk is opt-in at the walk question
	// (`certify`), because a compiled SUMMARY cannot mirror it.
	IrStatementLoopStmts IrStatementKind = "loopStmts"
	// IrStatementCall applies a callee's already-built summary. Callee
	// indexes the summary table the question carries beside the
	// statements; each Args entry is an effect over the CALLER's
	// bindings producing one callee entry state; Rets says where the
	// callee's out-states land — Rets[k] is the caller binding the
	// k-th out-state writes, and -1 there drops it.
	IrStatementCall IrStatementKind = "call"
	// IrStatementLoopCounted is the literal-bounded loop: the walk-side
	// exact unroll (walk/loop_unroll.go) steps a `for` whose trip count
	// the syntax pins a fixed number of times, no widening. This is its
	// kernel-portable twin — the same per-binding parallel effect the
	// effect-bodied loop carries in Body, composed with itself Count
	// times from the entry state rather than solved by the fixpoint.
	// Count comes from LiteralTripCountWith on the Go side; the kernel
	// mirrors LoopUnrollBudget as its own gate (loop_questions.go's
	// wire never carries a count past the budget — the Go lowering
	// falls back to the ordinary loop/loopStmts form there).
	IrStatementLoopCounted IrStatementKind = "loopCounted"
	// IrStatementLoopAccum is the ACCUMULATION over a sequence: slot
	// AccumSrc holds the sequence's ELEMENT state, slot AccumLen holds
	// the element COUNT, and the loop runs AccumLen times evaluating
	// AccumBody — an ordinary effect over the slot environment reading
	// the iteration value from AccumSrc — and adding its result into slot
	// AccumTotal, which starts at 0.
	//
	// What separates it from every loop form above is the RELATION it
	// leaves behind. The counted and solved loops answer one enclosure per
	// slot, so `total` exits as [0, +inf) whenever the count is a runtime
	// length, and a later `total / len` divides two unrelated enclosures.
	// This form ties the two: the kernel carries `total <= count * elemHi`
	// (and the lower twin) internally, so a division of AccumTotal by
	// AccumLen LATER IN THE SAME LOWERED PROGRAM is narrowed by the
	// relation before the plain division transfer runs. The relation lives
	// kernel-side for the length of one walk — it does not cross a program
	// boundary — so the loop and the division it feeds must lower into ONE
	// statement list.
	IrStatementLoopAccum IrStatementKind = "loopAccum"
)

// IrBranchTest is the test field of a branch IrStatement.
type IrBranchTest string

const (
	IrTestDefined IrBranchTest = "js.defined"
	// IrTestEqUndef and IrTestEqNull are the STRICT flavored tests split
	// out of the old conflated absent marker: `x === undefined` /
	// `x === null` decide exactly one admission, leaving the other
	// (null on eqUndef's false arm, undefined on eqNull's false arm)
	// untouched — a strictly stronger claim than IrTestDefined's
	// either-admission split. Neither carries a `w` operand — the tested
	// value is fixed by the test's own name, the same shape
	// defined/truthyNum/truthyStr/isNan already have.
	IrTestEqUndef   IrBranchTest = "js.eqUndef"
	IrTestEqNull    IrBranchTest = "js.eqNull"
	IrTestTruthyNum IrBranchTest = "js.truthyNum"
	IrTestTruthyStr IrBranchTest = "js.truthyStr"
	IrTestIsNan     IrBranchTest = "binary64.isNan"
	IrTestEq        IrBranchTest = "eq"
	IrTestEqSeq     IrBranchTest = "eqSeq"
	IrTestLt        IrBranchTest = "lt"
	IrTestLe        IrBranchTest = "le"
	IrTestGt        IrBranchTest = "gt"
	IrTestGe        IrBranchTest = "ge"
	// The two-slot comparisons: the right operand is another tracked
	// slot (OnB), not a constant, so these carry no `w`. `i < n`
	// lowers here where `i < 10` lowers to IrTestLt. IrTestEqSlot is
	// the equality shape — `i === n` and `i == n`, which agree
	// between two number-sorted slots.
	IrTestLtSlot IrBranchTest = "ltSlot"
	IrTestLeSlot IrBranchTest = "leSlot"
	IrTestGtSlot IrBranchTest = "gtSlot"
	IrTestGeSlot IrBranchTest = "geSlot"
	IrTestEqSlot IrBranchTest = "eqSlot"
	// IrTestEqSeqSlot is SEQUENCE equality between two string-sorted
	// slots (`s === t`). It rides with OnB like the scalar two-slot
	// comparisons but narrows NEITHER arm: the two-slot tightening is
	// built from enclosure bounds and a word has none, so the kernel
	// walks both arms untouched and joins them. What it unlocks is
	// bodies that decline today because the guard has no lowering at
	// all.
	IrTestEqSeqSlot IrBranchTest = "eqSeqSlot"
)

// IsTwoSlotTest reports whether a branch test compares two tracked
// slots rather than a slot against a constant — the tests that ride
// with OnB and never with W.
func IsTwoSlotTest(t IrBranchTest) bool {
	switch t {
	case IrTestLtSlot, IrTestLeSlot, IrTestGtSlot, IrTestGeSlot, IrTestEqSlot,
		IrTestEqSeqSlot:
		return true
	}
	return false
}

// IrStatement is a lowered statement for the kernel's flow walk: an
// assignment of an effect to a binding, a branch that tests one
// binding and carries both arms, a branch that tests NOTHING and
// carries both arms (branchBoth), or a loop. The `w` operand rides only
// with test "eq". A loop carries, per binding of the whole walk:
// whether it writes the binding, the condition's narrowing set if one
// reads, and the body's effect ("var i" for a binding the body leaves
// alone) — the entry premises come from the walk's own states,
// kernel-side. A loop head that compared two tracked slots rides in
// CondCmp instead of the per-binding sets.
type IrStatement struct {
	Kind IrStatementKind

	// "assign"
	Target int
	Effect LoopEffect

	// "branch"
	On   int
	Test IrBranchTest
	W    *float64
	// OnB: the SECOND slot a two-slot comparison tests On against —
	// read only when Test is one of the *Slot comparisons, which
	// carry no W.
	OnB    int
	Points []float64 // "eqSeq": the compared tuple (a string's code points)
	Then   []IrStatement
	Else   []IrStatement

	// "loop"
	Written []bool
	Cond    []*refinementsets.RefinedSet
	// After: the condition's FALSITY set per binding — the loop exits
	// only when the condition fails, so the exit intersects it.
	After []*refinementsets.RefinedSet
	Body  []LoopEffect
	// CondCmp: a head that compared two tracked slots (`while (i < n)`)
	// rather than a slot against a constant. Nothing constant bounds
	// either side, so Cond and After stay nil and this rides instead:
	// the kernel tightens each slot's EXIT by the negated comparison's
	// ray read off the other slot's flagless exit.
	CondCmp *IrLoopCondCmp

	// "loopStmts"
	// Stmts: the body of a statement-bodied loop. Body above is the
	// effect-bodied loop's and stays nil here; Cond, After and CondCmp
	// are shared with the effect loop and carry this loop's head when
	// one reads, for the EXIT refinement alone.
	Stmts []IrStatement

	// "call"
	// Callee: which summary of the question's table this call applies.
	Callee int
	// Args: one effect per callee entry, read over the caller's bindings.
	Args []LoopEffect
	// Rets: per callee out-state, the caller binding it writes. -1 says
	// nothing reads that out-state and spells `null` on the wire.
	Rets []int

	// "loopCounted"
	// Count: the exact trip count the syntax pinned
	// (LiteralTripCountWith), gated at LoopUnrollBudget before this
	// form is ever built — the Go lowering falls back to "loop" past
	// the budget, so the kernel never has to re-check it, but the
	// kernel gates its own walk at the same budget anyway (mirrored
	// rather than trusted from the wire).
	Count int
	// CountedBody: one effect per binding, the SAME parallel form
	// LoopStatement's Body carries for the effect-bodied loop (the
	// incrementor folded in as the index binding's own effect) — the
	// kernel composes this with itself Count times from the entry
	// state, no widening.
	CountedBody []LoopEffect

	// "loopAccum"
	// AccumTotal: the slot the running sum lands in. It holds the
	// accumulator's start value at entry and the summed total at exit.
	AccumTotal int
	// AccumSrc: the slot holding the sequence's ELEMENT state — one
	// value drawn from it per pass, which AccumBody reads as `{"var":
	// AccumSrc}`.
	AccumSrc int
	// AccumLen: the slot holding the element COUNT — how many passes run,
	// and the denominator the relation ties the total to.
	AccumLen int
	// AccumBody: the term one pass adds, as one effect over the slot
	// environment. `total += s * s` sends `mul(var AccumSrc, var
	// AccumSrc)` here — the ADDITION into AccumTotal is the form's own,
	// never spelled in this effect.
	AccumBody LoopEffect
}

// IrLoopCondCmp is a loop head comparing two tracked slots: On
// against OnB under Test, one of the *Slot comparisons.
type IrLoopCondCmp struct {
	On   int
	Test IrBranchTest
	OnB  int
}

func optSetWire(c *refinementsets.RefinedSet) string {
	if c == nil {
		return `{"none":true}`
	}
	return EncodeSet(*c)
}

// StmtWire is stmtWire in the TS source.
func StmtWire(s IrStatement) string {
	if s.Kind == IrStatementAssign {
		return fmt.Sprintf(`{"assign":{"target":%d,"e":%s}}`, s.Target, EffectWire(s.Effect))
	}
	if s.Kind == IrStatementCall {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = EffectWire(a)
		}
		rets := make([]string, len(s.Rets))
		for i, r := range s.Rets {
			// -1 is the out-state nothing reads: the wire says null there
			if r < 0 {
				rets[i] = "null"
			} else {
				rets[i] = strconv.Itoa(r)
			}
		}
		return fmt.Sprintf(
			`{"call":{"callee":%d,"args":[%s],"rets":[%s]}}`,
			s.Callee, strings.Join(args, ","), strings.Join(rets, ","),
		)
	}
	if s.Kind == IrStatementLoop {
		written := make([]string, len(s.Written))
		for i, w := range s.Written {
			written[i] = fmt.Sprintf("%v", w)
		}
		cond := make([]string, len(s.Cond))
		for i, c := range s.Cond {
			cond[i] = optSetWire(c)
		}
		after := make([]string, len(s.After))
		for i, a := range s.After {
			after[i] = optSetWire(a)
		}
		body := make([]string, len(s.Body))
		for i, b := range s.Body {
			body[i] = EffectWire(b)
		}
		condCmp := ""
		if s.CondCmp != nil {
			condCmp = fmt.Sprintf(
				`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
				s.CondCmp.On, s.CondCmp.Test, s.CondCmp.OnB,
			)
		}
		return fmt.Sprintf(
			`{"loop":{"written":[%s],"cond":[%s],"after":[%s],"body":[%s]%s}}`,
			strings.Join(written, ","), strings.Join(cond, ","),
			strings.Join(after, ","), strings.Join(body, ","), condCmp,
		)
	}
	if s.Kind == IrStatementBranchBoth {
		// no on, no test, no operand: the walk reads nothing about the
		// condition and both arms ride
		thn := make([]string, len(s.Then))
		for i, t := range s.Then {
			thn[i] = StmtWire(t)
		}
		els := make([]string, len(s.Else))
		for i, e := range s.Else {
			els[i] = StmtWire(e)
		}
		return fmt.Sprintf(
			`{"branchBoth":{"thn":[%s],"els":[%s]}}`,
			strings.Join(thn, ","), strings.Join(els, ","),
		)
	}
	if s.Kind == IrStatementLoopCounted {
		// count, then one effect per binding — the same per-binding shape
		// "loop" carries in Body, no cond/after/condCmp at all: the trip
		// count is exact, so there is nothing to widen and nothing to
		// certify
		body := make([]string, len(s.CountedBody))
		for i, e := range s.CountedBody {
			body[i] = EffectWire(e)
		}
		return fmt.Sprintf(
			`{"loopCounted":{"count":%d,"body":[%s]}}`,
			s.Count, strings.Join(body, ","),
		)
	}
	if s.Kind == IrStatementLoopAccum {
		// three slot indices and ONE effect — no per-binding vector at
		// all: every slot but AccumTotal is left exactly as it stood, and
		// AccumTotal's own step is the form's addition, not a spelled
		// effect. The kernel reads the iteration value off AccumSrc and
		// the pass count off AccumLen.
		return fmt.Sprintf(
			`{"loopAccum":{"total":%d,"src":%d,"len":%d,"body":%s}}`,
			s.AccumTotal, s.AccumSrc, s.AccumLen, EffectWire(s.AccumBody),
		)
	}
	if s.Kind == IrStatementLoopStmts {
		// the body statements ride and the kernel reads the write set off
		// them; the head's falsity sets and two-slot shape ride beside
		// them when one reads, and are omitted entirely when it does not —
		// which is the wire this form has always spoken
		stmts := make([]string, len(s.Stmts))
		for i, st := range s.Stmts {
			stmts[i] = StmtWire(st)
		}
		head := ""
		if len(s.Cond) > 0 {
			cond := make([]string, len(s.Cond))
			for i, c := range s.Cond {
				cond[i] = optSetWire(c)
			}
			head += fmt.Sprintf(`,"cond":[%s]`, strings.Join(cond, ","))
		}
		if len(s.After) > 0 {
			after := make([]string, len(s.After))
			for i, a := range s.After {
				after[i] = optSetWire(a)
			}
			head += fmt.Sprintf(`,"after":[%s]`, strings.Join(after, ","))
		}
		if s.CondCmp != nil {
			head += fmt.Sprintf(
				`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
				s.CondCmp.On, s.CondCmp.Test, s.CondCmp.OnB,
			)
		}
		return fmt.Sprintf(
			`{"loopStmts":{"body":[%s]%s}}`,
			strings.Join(stmts, ","), head,
		)
	}
	operand := ""
	if IsTwoSlotTest(s.Test) {
		// the second operand is a slot, not a constant
		operand = fmt.Sprintf(`,"onB":%d`, s.OnB)
	} else if s.Test == IrTestEqSeq && s.Points != nil {
		operand = fmt.Sprintf(`,"t":%s`, EncodeTuple(s.Points))
	} else if s.Test != IrTestDefined && s.Test != IrTestEqUndef &&
		s.Test != IrTestEqNull && s.Test != IrTestTruthyNum &&
		s.Test != IrTestTruthyStr && s.Test != IrTestIsNan && s.W != nil {
		operand = fmt.Sprintf(`,"w":%s`, marshalWireValue(WireNumberOf(*s.W)))
	}
	thenParts := make([]string, len(s.Then))
	for i, t := range s.Then {
		thenParts[i] = StmtWire(t)
	}
	elseParts := make([]string, len(s.Else))
	for i, e := range s.Else {
		elseParts[i] = StmtWire(e)
	}
	return fmt.Sprintf(
		`{"branch":{"on":%d,"test":"%s"%s,"then":[%s],"else":[%s]}}`,
		s.On, s.Test, operand, strings.Join(thenParts, ","), strings.Join(elseParts, ","),
	)
}
