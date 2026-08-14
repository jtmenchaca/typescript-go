// Loop and walk questions: entry premises, body effects, and the
// lowered IR statements the kernel iterates, widens, and certifies.
package kernelbridge

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// InvariantPremiseKind is the tag of an InvariantPremise.
type InvariantPremiseKind string

const (
	InvariantPremiseValues InvariantPremiseKind = "values"
	InvariantPremiseSet    InvariantPremiseKind = "set"
)

// InvariantPremise is one premise of the loop-invariant certificate: a
// concrete value (membership) or a set it lies in (subset).
type InvariantPremise struct {
	Kind   InvariantPremiseKind
	Values []float64
	Set    refinementsets.RefinedSet
}

// PremiseWire is premiseWire in the TS source.
func PremiseWire(p InvariantPremise) string {
	if p.Kind == InvariantPremiseValues {
		return fmt.Sprintf(`{"tuple":%s}`, EncodeTuple(p.Values))
	}
	return fmt.Sprintf(`{"set":%s}`, EncodeSet(p.Set))
}

// PremiseKey is premiseKey in the TS source.
func PremiseKey(p InvariantPremise) (string, bool) {
	if p.Kind == InvariantPremiseValues {
		return marshalWireValue(p.Values), true
	}
	key := CanonicalKeyOf(wireSet(p.Set))
	if key == nil {
		return "", false
	}
	return *key, true
}

// LoopEffectKind is the tag of a LoopEffect.
type LoopEffectKind string

const (
	LoopEffectVar   LoopEffectKind = "var"
	LoopEffectConst LoopEffectKind = "const"
	// LoopEffectConstState is the const leaf carrying the WHOLE state:
	// the set beside the absent and NaN flags. `x = null` and `return
	// undefined` write the absent outcome, which lives outside R-bar
	// and so cannot ride in a RefinedSet. A const with both flags down
	// is exactly LoopEffectConst, and the wire keeps them distinct so
	// older forms decode unchanged.
	LoopEffectConstState LoopEffectKind = "constState"
	LoopEffectUnknown    LoopEffectKind = "unknown"
	LoopEffectUnary      LoopEffectKind = "un"
	LoopEffectBinary     LoopEffectKind = "bin"
	// LoopEffectConcat is the SEQUENCE binary: `a + b` where both sides
	// are string-sorted builds the concatenation of the two operand
	// sets. It is not an enclosure operation, so it never reaches the
	// arithmetic transfers and rides its own wire field.
	LoopEffectConcat LoopEffectKind = "concat"
	LoopEffectJoin   LoopEffectKind = "join"
	// LoopEffectOrAbsent is the unguarded index read: `a[i]` with nothing
	// bounding i against the length produces the element or undefined.
	// The value part is the operand's — the element slot's effect, in the
	// A field like the unary forms — and the absent outcome rides beside
	// it, since the absent value lives outside every set.
	LoopEffectOrAbsent LoopEffectKind = "orAbsent"
)

// LoopEffectOp is the op field of a unary or binary LoopEffect.
type LoopEffectOp string

const (
	LoopOpNeg   LoopEffectOp = "neg"
	LoopOpFloor LoopEffectOp = "floor"
	LoopOpCeil  LoopEffectOp = "ceil"
	LoopOpRound LoopEffectOp = "round"
	LoopOpTrunc LoopEffectOp = "trunc"
	LoopOpAbs   LoopEffectOp = "abs"
	LoopOpAdd   LoopEffectOp = "add"
	LoopOpSub   LoopEffectOp = "sub"
	LoopOpMul   LoopEffectOp = "mul"
	LoopOpDiv   LoopEffectOp = "div"
	LoopOpRem   LoopEffectOp = "rem"
	LoopOpMin   LoopEffectOp = "min"
	LoopOpMax   LoopEffectOp = "max"
)

// LoopEffect is one binding's body effect, lowered for the kernel's
// loop solver: the binding values at entry to a pass, known sets for
// everything read from outside, the arithmetic the body performs, and
// joins where control flow splits. `unknown` is the honest leaf for
// anything the reading could not vouch for. Collapsed to one struct
// with a Kind tag; A/B hold operands (as *LoopEffect since the TS
// variant is recursive), Index the var index, Set the const set.
type LoopEffect struct {
	Kind LoopEffectKind

	Index int                       // "var"
	Set   refinementsets.RefinedSet // "const" / "constState"

	// Absent, Nan: "constState" only — whether the written constant may
	// be the absent value or NaN, neither of which any set can hold.
	Absent bool
	Nan    bool

	Op LoopEffectOp // "un" / "bin"
	A  *LoopEffect
	B  *LoopEffect // "bin" / "concat" / "join"
}

// AbsentConst is the state constant `null`/`undefined` writes: the
// empty set of values beside a raised absent flag. Under ANY target
// sort — the absent value is not a number and not a word, so no sort
// can hold it in its set part.
func AbsentConst() LoopEffect {
	return LoopEffect{
		Kind:   LoopEffectConstState,
		Set:    refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
		Absent: true,
	}
}

// EffectWire is effectWire in the TS source.
func EffectWire(e LoopEffect) string {
	switch e.Kind {
	case LoopEffectVar:
		return fmt.Sprintf(`{"var":%d}`, e.Index)
	case LoopEffectConst:
		return fmt.Sprintf(`{"set":%s}`, EncodeSet(e.Set))
	case LoopEffectConstState:
		return fmt.Sprintf(`{"set":%s,"absent":%v,"nan":%v}`, EncodeSet(e.Set), e.Absent, e.Nan)
	case LoopEffectUnknown:
		return `{"unknown":true}`
	case LoopEffectUnary:
		return fmt.Sprintf(`{"op":"%s","A":%s}`, e.Op, EffectWire(*e.A))
	case LoopEffectBinary:
		return fmt.Sprintf(`{"op":"%s","A":%s,"B":%s}`, e.Op, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectConcat:
		return fmt.Sprintf(`{"concat":[%s,%s]}`, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectJoin:
		return fmt.Sprintf(`{"join":[%s,%s]}`, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectOrAbsent:
		return fmt.Sprintf(`{"orAbsent":%s}`, EffectWire(*e.A))
	}
	panic(fmt.Sprintf("EffectWire: unreached kind %q", e.Kind))
}

// LoopQuestion is the TS LoopQuestion interface.
type LoopQuestion struct {
	// Entry: per binding, the entry premise, or nil when nothing is
	// known.
	Entry []*InvariantPremise
	// Cond: per binding, the loop condition's narrowing set, if one
	// reads (nil otherwise).
	Cond []*refinementsets.RefinedSet
	// Body: per binding, the body's effect.
	Body []LoopEffect
	// CondCmp: the head when it compared two tracked slots. Nothing
	// constant bounds either side, so this rides instead of Cond and
	// the kernel cuts each pass entry by the bound the other slot's
	// own entry value states.
	CondCmp *IrLoopCondCmp
}

// LoopVarAnswerKind is the tag of a LoopVarAnswer.
type LoopVarAnswerKind string

const (
	LoopVarAnswerSet     LoopVarAnswerKind = "set"
	LoopVarAnswerUnknown LoopVarAnswerKind = "unknown"
)

// LoopVarAnswer is the TS LoopVarAnswer union.
type LoopVarAnswer struct {
	Kind LoopVarAnswerKind
	Set  refinementsets.RefinedSet
}

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
	// than one effect per binding. No condition is read, so any number
	// of trips may run — zero included — and the kernel havocs the
	// body's own write set, which it computes from the body statements
	// itself and never trusts from this wire. It carries Stmts and
	// nothing else: no Written, Cond, After, or CondCmp.
	IrStatementLoopStmts IrStatementKind = "loopStmts"
	// IrStatementCall applies a callee's already-built summary. Callee
	// indexes the summary table the question carries beside the
	// statements; each Args entry is an effect over the CALLER's
	// bindings producing one callee entry state; Rets says where the
	// callee's out-states land — Rets[k] is the caller binding the
	// k-th out-state writes, and -1 there drops it.
	IrStatementCall IrStatementKind = "call"
)

// IrBranchTest is the test field of a branch IrStatement.
type IrBranchTest string

const (
	IrTestDefined   IrBranchTest = "defined"
	IrTestTruthyNum IrBranchTest = "truthyNum"
	IrTestTruthyStr IrBranchTest = "truthyStr"
	IrTestIsNan     IrBranchTest = "isNan"
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
	On     int
	Test   IrBranchTest
	W      *float64
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
	// Stmts: the body of a statement-bodied loop. This is the whole
	// carrying field for that kind — Body above is the effect-bodied
	// loop's and stays nil here.
	Stmts []IrStatement

	// "call"
	// Callee: which summary of the question's table this call applies.
	Callee int
	// Args: one effect per callee entry, read over the caller's bindings.
	Args []LoopEffect
	// Rets: per callee out-state, the caller binding it writes. -1 says
	// nothing reads that out-state and spells `null` on the wire.
	Rets []int
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
	if s.Kind == IrStatementLoopStmts {
		// no condition and no per-binding sets: the body statements ride
		// alone and the kernel reads the write set off them
		stmts := make([]string, len(s.Stmts))
		for i, st := range s.Stmts {
			stmts[i] = StmtWire(st)
		}
		return fmt.Sprintf(
			`{"loopStmts":{"body":[%s]}}`,
			strings.Join(stmts, ","),
		)
	}
	operand := ""
	if IsTwoSlotTest(s.Test) {
		// the second operand is a slot, not a constant
		operand = fmt.Sprintf(`,"onB":%d`, s.OnB)
	} else if s.Test == IrTestEqSeq && s.Points != nil {
		operand = fmt.Sprintf(`,"t":%s`, EncodeTuple(s.Points))
	} else if s.Test != IrTestDefined && s.Test != IrTestTruthyNum &&
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

// LoopWire is loopWire in the TS source.
func LoopWire(q LoopQuestion) string {
	entry := make([]string, len(q.Entry))
	for i, p := range q.Entry {
		if p == nil {
			entry[i] = `{"unknown":true}`
		} else {
			entry[i] = PremiseWire(*p)
		}
	}
	cond := make([]string, len(q.Cond))
	for i, c := range q.Cond {
		cond[i] = optSetWire(c)
	}
	body := make([]string, len(q.Body))
	for i, b := range q.Body {
		body[i] = EffectWire(b)
	}
	condCmp := ""
	if q.CondCmp != nil {
		condCmp = fmt.Sprintf(
			`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
			q.CondCmp.On, q.CondCmp.Test, q.CondCmp.OnB,
		)
	}
	return fmt.Sprintf(
		`{"entry":[%s],"cond":[%s],"body":[%s]%s}`,
		strings.Join(entry, ","), strings.Join(cond, ","),
		strings.Join(body, ","), condCmp,
	)
}

// InvariantWidenWireResult is the non-nil return of InvariantWidenWire.
type InvariantWidenWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// flattenUnionLeaves mirrors the TS flatten() closure in
// invariantWidenWire: walk a union tree's leaves in wire form.
func flattenUnionLeaves(set refinementsets.RefinedSet, leaves *[]string) {
	var f *refinementsets.Refinement
	if len(set.Forms) == 1 {
		f = &set.Forms[0]
	}
	if f != nil && f.Form == refinementsets.FormUnion {
		flattenUnionLeaves(*f.A_, leaves)
		flattenUnionLeaves(*f.B, leaves)
	} else {
		*leaves = append(*leaves, EncodeSet(set))
	}
}

// InvariantWidenWire is invariantWidenWire in the TS source: the
// widening invariant wire when the candidate is a union whose leaves
// include the step's set — the kernel builds base ∪ step itself
// (`invariantWidenB_certifies`). Returns ok=false when the shape does
// not apply (the TS `null`).
func InvariantWidenWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) (result InvariantWidenWireResult, ok bool) {
	if step.Kind != InvariantPremiseSet || len(candidate.Forms) != 1 {
		return InvariantWidenWireResult{}, false
	}
	form := candidate.Forms[0]
	if form.Form != refinementsets.FormUnion {
		return InvariantWidenWireResult{}, false
	}
	stepWire := EncodeSet(step.Set)
	var leaves []string
	flattenUnionLeaves(candidate, &leaves)
	at := -1
	for i, leaf := range leaves {
		if leaf == stepWire {
			at = i
			break
		}
	}
	var rest []string
	if at != -1 {
		for i, leaf := range leaves {
			if i != at {
				rest = append(rest, leaf)
			}
		}
	}
	if len(rest) == 0 {
		return InvariantWidenWireResult{}, false
	}
	base := rest[0]
	for _, v := range rest[1:] {
		base = fmt.Sprintf(`{"forms":[{"form":"union","A":%s,"B":%s}]}`, base, v)
	}
	wire := fmt.Sprintf(`{"entry":%s,"base":%s,"step":%s}`, PremiseWire(entry), base, stepWire)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	if canonC == nil || !canonEOK {
		return InvariantWidenWireResult{Wire: wire}, true
	}
	return InvariantWidenWireResult{
		Wire: wire, Key: fmt.Sprintf("widen:%s%s", *canonC, canonE), HasKey: true,
	}, true
}

// InvariantAskWireResult is the return of InvariantAskWire.
type InvariantAskWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// InvariantAskWire is invariantAskWire in the TS source.
func InvariantAskWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) InvariantAskWireResult {
	wire := fmt.Sprintf(
		`{"candidate":%s,"entry":%s,"step":%s}`,
		EncodeSet(candidate), PremiseWire(entry), PremiseWire(step),
	)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	canonS, canonSOK := PremiseKey(step)
	if canonC == nil || !canonEOK || !canonSOK {
		return InvariantAskWireResult{Wire: wire}
	}
	return InvariantAskWireResult{
		Wire: wire, Key: fmt.Sprintf("%s\x01%s\x01%s", *canonC, canonE, canonS), HasKey: true,
	}
}

// DecodeLoopVars is decodeLoopVars in the TS source.
func DecodeLoopVars(parsed map[string]any) []LoopVarAnswer {
	rawVars, ok := parsed["vars"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
	}
	vars := make([]LoopVarAnswer, len(rawVars))
	for i, rv := range rawVars {
		v, ok := rv.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
		}
		if v["kind"] == "set" {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerSet, Set: DecodeWireSet(v["set"])}
		} else {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerUnknown}
		}
	}
	return vars
}
