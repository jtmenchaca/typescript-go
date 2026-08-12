// The questions, given a live call: encode, ask, decode. Abort
// detection stays with ask_kernel.go — these bodies never see a dead
// module. (`structural`/`checkAssignability` take the encoded wire —
// see kernel_interface.go's cycle note; objectgraphs owns the typed
// wrappers and the encoder.)
//
// The TS source dispatches through `kernel: KernelCalls` (a struct of
// per-question C function pointers `ask1`/`ask2` apply generically).
// The Go loader (kernelbridge.NativeKernel, instantiate_kernel.go)
// dispatches by SYMBOL NAME instead — Call1("kernel_member", input) —
// so there is no function-pointer table to build here: ask1/ask2 take
// the symbol string directly where the TS took `kernel._kernel_member`
// etc. This collapses instantiate_kernel.ts's KernelCalls/KernelFn
// indirection entirely; noted in the port report.
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// Ask1 asks a one-argument question by symbol name, through the cache
// and cost recorder. Mirrors the TS `Ask1` type.
type Ask1 func(op string, symbol string, input string, key ...string) (string, error)

// Ask2 asks a two-argument question by symbol name. Mirrors the TS
// `Ask2` type.
type Ask2 func(op string, symbol string, first string, second string, key ...string) (string, error)

// KernelAsksInput mirrors the destructured `{ kernel, ask1, ask2 }`
// parameter of kernelAsks in the TS source. `kernel` itself (the
// KernelCalls function-pointer table) has no Go twin — see the file
// comment — so this struct carries only ask1/ask2.
type KernelAsksInput struct {
	Ask1 Ask1
	Ask2 Ask2
}

// KernelAsks is kernelAsks in the TS source: the questions over a live
// call, minus InitMs (the caller sets that — mirrors the TS
// `Omit<RefinedTSKernel, "initMs">` return type).
func KernelAsks(input KernelAsksInput) *RefinedTSKernel {
	ask1, ask2 := input.Ask1, input.Ask2

	member := func(set refinementsets.RefinedSet, tuple []float64) bool {
		key, hasKey := CanonicalPairOfSetAndTuple(set, tuple)
		raw, err := ask2WithOptionalKey(ask2, "member", "kernel_member", EncodeSet(set), EncodeTuple(tuple), key, hasKey)
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "member")
	}

	kernel := &RefinedTSKernel{}
	kernel.Member = member
	kernel.ScalarEmpty = func(set refinementsets.RefinedSet) bool {
		raw, err := ask1WithOptionalKey(ask1, "scalarEmpty", "kernel_scalar_empty", EncodeSet(set), CanonicalKeyOf(wireSet(set)))
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "empty")
	}
	kernel.ScalarSubset = func(a, b refinementsets.RefinedSet) bool {
		key, hasKey := CanonicalPair(CanonicalKeyOf(wireSet(a)), CanonicalKeyOf(wireSet(b)))
		raw, err := ask2WithOptionalKey(ask2, "scalarSubset", "kernel_scalar_subset", EncodeSet(a), EncodeSet(b), key, hasKey)
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "subset")
	}
	kernel.ScalarDisjoint = func(a, b refinementsets.RefinedSet) bool {
		key, hasKey := CanonicalPair(CanonicalKeyOf(wireSet(a)), CanonicalKeyOf(wireSet(b)))
		raw, err := ask2WithOptionalKey(ask2, "scalarDisjoint", "kernel_scalar_disjoint", EncodeSet(a), EncodeSet(b), key, hasKey)
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "disjoint")
	}
	kernel.SeqEmpty = func(set refinementsets.RefinedSet) bool {
		raw, err := ask1WithOptionalKey(ask1, "seqEmpty", "kernel_seq_empty", EncodeSet(set), CanonicalKeyOf(wireSet(set)))
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "empty")
	}
	kernel.SeqSubset = func(a, b refinementsets.RefinedSet) bool {
		// A singleton left side IS a membership question — {w} ⊆ B ⇔
		// w ∈ B — and membership is exact in both directions
		// (memberB_iff) and linear in the word, where the subset
		// question's cost measure would refuse the word-as-set shape.
		if word, ok := refinementsets.WordOf(a); ok {
			return member(b, word)
		}
		wireA := EncodeSet(a)
		wireB := EncodeSet(b)
		// one set is inside itself — the identity flow of a stated
		// annotation, answered without the kernel (syntactic equality
		// of the wire spellings; trivially sound)
		if wireA == wireB {
			return true
		}
		key, hasKey := CanonicalPair(CanonicalKeyOf(wireSet(a)), CanonicalKeyOf(wireSet(b)))
		raw, err := ask2WithOptionalKey(ask2, "seqSubset", "kernel_seq_subset", wireA, wireB, key, hasKey)
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "subset")
	}
	// Structural and CheckAssignability take the specification ALREADY
	// ENCODED (objectgraphs.EncodeSpecification): the Specification
	// type lives in objectgraphs, which imports this package for its
	// kernel calls — a typed parameter here would close an import
	// cycle Go forbids (the TS tree's type-only import has no Go
	// twin). objectgraphs provides the typed wrappers.
	kernel.Structural = func(specWire string) bool {
		raw, err := ask1("structural", "kernel_structural", specWire)
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "structural")
	}
	kernel.CheckAssignability = func(specWire string) JudgeAnswer {
		raw, err := ask1("checkAssignability", "kernel_judge", specWire)
		if err != nil {
			panic(err.Error())
		}
		return DecodeJudgeAnswer(Answered(raw))
	}
	kernel.Calendar = func(question CalendarQuestion) map[string]any {
		raw, err := ask1(
			"calendar", "kernel_calendar", marshalWireValue(calendarQuestionWire(question)),
		)
		if err != nil {
			panic(err.Error())
		}
		return Answered(raw)
	}
	kernel.Transfer = func(question TransferQuestion) TransferAnswer {
		wire := TransferWire(question)
		key, hasKey := canonicalKeyOfTransferQuestion(question)
		raw, err := ask1WithOptionalKey(ask1, "transfer", "kernel_transfer", wire, optionalKey(key, hasKey))
		if err != nil {
			panic(err.Error())
		}
		return DecodeTransferAnswer(Answered(raw))
	}
	kernel.LinearImplies = func(facts []LinearFact, target LinearFact) bool {
		wire := LinearWire(LinearWireInput{Facts: facts, Target: target})
		raw, err := ask1("linear", "kernel_linear", wire)
		if err != nil {
			panic(err.Error())
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			panic(fmt.Sprintf("kernel: unparseable answer: %v", err))
		}
		// a refusal or a false is the same refusal to claim
		implied, _ := parsed["implied"].(bool)
		return implied
	}
	kernel.Envelope = func(op string, a, b refinementsets.RefinedSet) (float64, bool) {
		keyA := CanonicalKeyOf(wireSet(a))
		keyB := CanonicalKeyOf(wireSet(b))
		var key string
		hasKey := keyA != nil && keyB != nil
		if hasKey {
			key = fmt.Sprintf("env:%s:%s:%s", op, *keyA, *keyB)
		}
		raw, err := ask1WithOptionalKey(
			ask1, "envelope", "kernel_envelope",
			fmt.Sprintf(`{"op":"%s","A":%s,"B":%s}`, op, EncodeSet(a), EncodeSet(b)),
			optionalKey(key, hasKey),
		)
		if err != nil {
			panic(err.Error())
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			panic(fmt.Sprintf("kernel: unparseable answer: %v", err))
		}
		// a refusal keeps the caller's row unwidened, never throws
		envelope, held := parsed["envelope"]
		if !held || envelope == nil {
			return 0, false
		}
		return DecodeWireNumber(envelope), true
	}
	kernel.Bounds = func(set refinementsets.RefinedSet) BoundsResult {
		raw, err := ask1WithOptionalKey(ask1, "bounds", "kernel_bounds", EncodeSet(set), CanonicalKeyOf(wireSet(set)))
		if err != nil {
			panic(err.Error())
		}
		return DecodeBounds(Answered(raw))
	}
	kernel.Members = func(set refinementsets.RefinedSet, cap int) []float64 {
		key := CanonicalKeyOf(wireSet(set))
		var keyStr string
		hasKey := key != nil
		if hasKey {
			keyStr = fmt.Sprintf("%s#%d", *key, cap)
		}
		raw, err := ask2WithOptionalKey(
			ask2, "members", "kernel_members", EncodeSet(set), strconv.Itoa(cap), keyStr, hasKey,
		)
		if err != nil {
			panic(err.Error())
		}
		return DecodeMembers(Answered(raw))
	}
	kernel.Decimal = func(value float64) (string, bool) {
		if isInfOrNaN(value) {
			return "", false
		}
		d, err := primitives.DyadicOfNumber(value)
		if err != nil {
			panic(fmt.Sprintf("kernel.Decimal: %v", err))
		}
		raw, err := ask1(
			"decimal", "kernel_decimal", marshalWireValue(d), fmt.Sprintf("dec:%de%d", d.Num, d.Exp),
		)
		if err != nil {
			panic(err.Error())
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			panic(fmt.Sprintf("kernel: unparseable answer: %v", err))
		}
		// a refusal keeps the row with the host, never throws
		decimal, ok := parsed["decimal"].(string)
		if !ok {
			return "", false
		}
		return decimal, true
	}
	kernel.Invariant = func(candidate refinementsets.RefinedSet, entry, step InvariantPremise) bool {
		// TS: `const widen = kernel._kernel_invariant_widen; if (widen
		// !== undefined) { … }` — the TS KernelCalls type marks
		// _kernel_invariant_widen optional and only some builds carry
		// it. The Go native loader (instantiate_kernel.go's
		// oneArgSymbols) always resolves "kernel_invariant_widen" at
		// InstantiateNative time — a missing symbol fails load, never
		// ask — so the Go twin always has the symbol and always tries
		// the widened wire first, unconditionally.
		if widened, ok := InvariantWidenWire(candidate, entry, step); ok {
			raw, err := ask1WithOptionalKey(
				ask1, "invariant", "kernel_invariant_widen", widened.Wire, optionalKey(widened.Key, widened.HasKey),
			)
			if err != nil {
				panic(err.Error())
			}
			return BooleanField(raw, "invariant")
		}
		wire := InvariantAskWire(candidate, entry, step)
		raw, err := ask1WithOptionalKey(ask1, "invariant", "kernel_invariant", wire.Wire, optionalKey(wire.Key, wire.HasKey))
		if err != nil {
			panic(err.Error())
		}
		return BooleanField(raw, "invariant")
	}
	kernel.SolveLoop = func(question LoopQuestion) []LoopVarAnswer {
		wire := LoopWire(question)
		raw, err := ask1WithOptionalKey(ask1, "solveLoop", "kernel_solve_loop", wire, canonicalKeyOfLoopQuestion(question))
		if err != nil {
			panic(err.Error())
		}
		return DecodeLoopVars(Answered(raw))
	}
	kernel.Narrow = func(tree NarrowTree) NarrowAnswer {
		GateNarrow(tree)
		wire := fmt.Sprintf(`{"tree":%s}`, NarrowWire(tree))
		raw, err := ask1WithOptionalKey(ask1, "narrow", "kernel_narrow", wire, canonicalKeyOfNarrowTree(tree))
		if err != nil {
			panic(err.Error())
		}
		return DecodeNarrowAnswer(Answered(raw))
	}
	kernel.JoinState = func(a, b KnownStateWire) KnownStateWire {
		wire := fmt.Sprintf(`{"a":%s,"b":%s}`, StateWire(a), StateWire(b))
		raw, err := ask1("joinState", "kernel_join_state", wire)
		if err != nil {
			panic(err.Error())
		}
		parsed := Answered(raw)
		return DecodeWireState(parsed["state"])
	}
	kernel.NarrowState = func(state KnownStateWire, op string, w float64, hasW bool) (KnownStateWire, KnownStateWire) {
		if op == "eq" && (!hasW || isNaN(w)) {
			panic("the eq narrowing takes a real word")
		}
		operand := ""
		if op == "eq" && hasW {
			operand = fmt.Sprintf(`,"w":%s`, marshalWireValue(WireNumberOf(w)))
		}
		wire := fmt.Sprintf(`{"state":%s,"op":"%s"%s}`, StateWire(state), op, operand)
		raw, err := ask1("narrowState", "kernel_narrow_state", wire)
		if err != nil {
			panic(err.Error())
		}
		parsed := Answered(raw)
		return DecodeWireState(parsed["whenTrue"]), DecodeWireState(parsed["whenFalse"])
	}
	kernel.Walk = func(states []KnownStateWire, stmts []IrStatement) []KnownStateWire {
		stateParts := make([]string, len(states))
		for i, s := range states {
			stateParts[i] = StateWire(s)
		}
		stmtParts := make([]string, len(stmts))
		for i, s := range stmts {
			stmtParts[i] = StmtWire(s)
		}
		wire := fmt.Sprintf(`{"states":[%s],"stmts":[%s]}`, joinComma(stateParts), joinComma(stmtParts))
		raw, err := ask1("walk", "kernel_walk", wire)
		if err != nil {
			panic(err.Error())
		}
		parsed := Answered(raw)
		out, ok := parsed["states"].([]any)
		if !ok {
			panic(fmt.Sprintf("kernel walk answered an unexpected shape: %v", parsed))
		}
		result := make([]KnownStateWire, len(out))
		for i, s := range out {
			result[i] = DecodeWireState(s)
		}
		return result
	}
	kernel.ValidateChain = func(chain Chain) ValidateChainResult {
		raw, err := ask1("validateChain", "kernel_validate_chain", EncodeChain(chain))
		if err != nil {
			panic(err.Error())
		}
		return DecodeValidateChain(Answered(raw))
	}
	return kernel
}

// ask1WithOptionalKey and ask2WithOptionalKey mirror the TS default
// parameter `key: string = input` / `key: string = first + second` —
// Go has no default-parameter sugar, so the "key defaults to the wire
// itself" behavior is spelled explicitly at each call site via these
// two small wrappers (a nil key means "use input as key", matching the
// TS default).
func ask1WithOptionalKey(ask1 Ask1, op, symbol, input string, key *string) (string, error) {
	if key == nil {
		return ask1(op, symbol, input)
	}
	return ask1(op, symbol, input, *key)
}

func ask2WithOptionalKey(ask2 Ask2, op, symbol, first, second string, key string, hasKey bool) (string, error) {
	if !hasKey {
		return ask2(op, symbol, first, second)
	}
	return ask2(op, symbol, first, second, key)
}

func optionalKey(key string, hasKey bool) *string {
	if !hasKey {
		return nil
	}
	return &key
}

// CanonicalPairOfSetAndTuple builds the member() cache key: the TS
// `canonicalPair(canonicalKeyOf(set), JSON.stringify(tuple))`.
//
// `JSON.stringify` on a JS number serializes ±Infinity/NaN as the bare
// token `null` (there is no JSON representation of a non-finite
// number) — unlike Go's `encoding/json`, which refuses to marshal a
// non-finite float64 at all (json: unsupported value: +Inf). jsonNumberString
// below mirrors the JS behavior so this cache key never panics on a
// tuple holding ±∞.
func CanonicalPairOfSetAndTuple(set refinementsets.RefinedSet, tuple []float64) (string, bool) {
	setKey := CanonicalKeyOf(wireSet(set))
	parts := make([]string, len(tuple))
	for i, x := range tuple {
		parts[i] = jsonNumberString(x)
	}
	tupleKey := "[" + joinComma(parts) + "]"
	return CanonicalPair(setKey, &tupleKey)
}

// jsonNumberString mirrors what `JSON.stringify` does to a single JS
// number: ±Infinity and NaN serialize as the bare token `null`;
// every other value prints as its ordinary JSON numeral.
func jsonNumberString(x float64) string {
	if isNaN(x) || math.IsInf(x, 0) {
		return "null"
	}
	return marshalWireValue(x)
}

func canonicalKeyOfTransferQuestion(q TransferQuestion) (string, bool) {
	key := CanonicalKeyOf(transferQuestionWireValue(q))
	if key == nil {
		return "", false
	}
	return *key, true
}

// transferQuestionWireValue mirrors what canonicalKeyOf(question) would
// walk in the TS source: the parsed wire JSON of the question, since
// TransferQuestion is not itself a plain wire-shaped value (it holds
// Go structs, not map[string]any).
func transferQuestionWireValue(q TransferQuestion) any {
	var parsed any
	if err := json.Unmarshal([]byte(TransferWire(q)), &parsed); err != nil {
		return nil
	}
	return parsed
}

func canonicalKeyOfLoopQuestion(q LoopQuestion) *string {
	var parsed any
	if err := json.Unmarshal([]byte(LoopWire(q)), &parsed); err != nil {
		return nil
	}
	return CanonicalKeyOf(parsed)
}

func canonicalKeyOfNarrowTree(t NarrowTree) *string {
	var parsed any
	if err := json.Unmarshal([]byte(NarrowWire(t)), &parsed); err != nil {
		return nil
	}
	return CanonicalKeyOf(parsed)
}

func isInfOrNaN(x float64) bool {
	return x != x || x > 1.7976931348623157e+308 || x < -1.7976931348623157e+308
}

func calendarQuestionWire(q CalendarQuestion) map[string]any {
	switch q.Op {
	case CalendarOpEpochDays, CalendarOpValidDate:
		return map[string]any{"op": string(q.Op), "year": q.Year, "month": q.Month, "day": q.Day}
	case CalendarOpIsoDate:
		return map[string]any{"op": string(q.Op), "days": q.Days}
	case CalendarOpValidDuration:
		return map[string]any{"op": string(q.Op), "fields": q.Fields}
	case CalendarOpCompareDuration:
		return map[string]any{"op": string(q.Op), "a": q.A, "b": q.B}
	}
	panic(fmt.Sprintf("calendarQuestionWire: unreached op %q", q.Op))
}
