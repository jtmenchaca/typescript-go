// Loop and walk questions: entry premises, body effects, and the
// lowered IR statements the kernel iterates, widens, and certifies.
package kernelbridge

import (
	"fmt"
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
	LoopEffectVar     LoopEffectKind = "var"
	LoopEffectConst   LoopEffectKind = "const"
	LoopEffectUnknown LoopEffectKind = "unknown"
	LoopEffectUnary   LoopEffectKind = "un"
	LoopEffectBinary  LoopEffectKind = "bin"
	LoopEffectJoin    LoopEffectKind = "join"
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
	Set   refinementsets.RefinedSet // "const"

	Op LoopEffectOp // "un" / "bin"
	A  *LoopEffect
	B  *LoopEffect // "bin" / "join"
}

// EffectWire is effectWire in the TS source.
func EffectWire(e LoopEffect) string {
	switch e.Kind {
	case LoopEffectVar:
		return fmt.Sprintf(`{"var":%d}`, e.Index)
	case LoopEffectConst:
		return fmt.Sprintf(`{"set":%s}`, EncodeSet(e.Set))
	case LoopEffectUnknown:
		return `{"unknown":true}`
	case LoopEffectUnary:
		return fmt.Sprintf(`{"op":"%s","A":%s}`, e.Op, EffectWire(*e.A))
	case LoopEffectBinary:
		return fmt.Sprintf(`{"op":"%s","A":%s,"B":%s}`, e.Op, EffectWire(*e.A), EffectWire(*e.B))
	case LoopEffectJoin:
		return fmt.Sprintf(`{"join":[%s,%s]}`, EffectWire(*e.A), EffectWire(*e.B))
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
	IrStatementLoop   IrStatementKind = "loop"
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
	// lowers here where `i < 10` lowers to IrTestLt.
	IrTestLtSlot IrBranchTest = "ltSlot"
	IrTestLeSlot IrBranchTest = "leSlot"
	IrTestGtSlot IrBranchTest = "gtSlot"
	IrTestGeSlot IrBranchTest = "geSlot"
)

// IsTwoSlotTest reports whether a branch test compares two tracked
// slots rather than a slot against a constant — the tests that ride
// with OnB and never with W.
func IsTwoSlotTest(t IrBranchTest) bool {
	switch t {
	case IrTestLtSlot, IrTestLeSlot, IrTestGtSlot, IrTestGeSlot:
		return true
	}
	return false
}

// IrStatement is a lowered statement for the kernel's flow walk: an
// assignment of an effect to a binding, a branch that tests one
// binding and carries both arms, or a loop. The `w` operand rides only
// with test "eq". A loop carries, per binding of the whole walk:
// whether it writes the binding, the condition's narrowing set if one
// reads, and the body's effect ("var i" for a binding the body leaves
// alone) — the entry premises come from the walk's own states,
// kernel-side.
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
		return fmt.Sprintf(
			`{"loop":{"written":[%s],"cond":[%s],"after":[%s],"body":[%s]}}`,
			strings.Join(written, ","), strings.Join(cond, ","),
			strings.Join(after, ","), strings.Join(body, ","),
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
	return fmt.Sprintf(
		`{"entry":[%s],"cond":[%s],"body":[%s]}`,
		strings.Join(entry, ","), strings.Join(cond, ","), strings.Join(body, ","),
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
