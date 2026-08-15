// from control_flow/effect_expression.ts
//
// The EXPRESSION half of the effect grammar, shared by both
// lowerings — the loop solver (loop_effect.ts) and the flow IR
// (lowering_to_kernel_ir.ts): literals through parens/casts, tracked
// reads, negation and unary plus, the five arithmetic operators, the
// Math reads (five unary, min/max), and the ternary as a join (its
// condition must be write-free — both arms are admitted, sound).
// `ReadPlace` answers a spelled tracked (or known) name; `Opaque`
// says what an unmodeled shape becomes — the solver path answers
// unknown for write-free shapes, the IR path declines. (0-value,
// false) means the reading declines.

package walk

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var binOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskToken: kernelbridge.LoopOpMul,
	ast.KindSlashToken:    kernelbridge.LoopOpDiv,
	ast.KindPercentToken:  kernelbridge.LoopOpRem,
	// the bitwise and shift operators: the kernel reads these six op
	// names into LoopOp2 and evaluates them through transferBitwise,
	// which is exact on singleton operands, answers [0, mask] when one
	// side of an AND is a nonnegative singleton, and claims nothing
	// otherwise. No extra operand gate is needed: ToInt32 is defined
	// for every double including the infinities, so a number-sorted
	// operand of any magnitude is a legal input.
	ast.KindBarToken:                               kernelbridge.LoopOpBitOr,
	ast.KindAmpersandToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindCaretToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanToken: kernelbridge.LoopOpShr,
	// `**`: the kernel reads this name into LoopOp2 and evaluates it
	// through transferPow, the same pinned Number::exponentiate rows
	// the transfer wire answers with. Total on every pair of doubles —
	// the spec's own NaN rows are cells transferPow carries — so the
	// operand gate is the number sort alone, like the bitwise forms.
	ast.KindAsteriskAsteriskToken: kernelbridge.LoopOpPow,
}

var mathOps = map[string]kernelbridge.LoopEffectOp{
	"floor": kernelbridge.LoopOpFloor,
	"ceil":  kernelbridge.LoopOpCeil,
	"round": kernelbridge.LoopOpRound,
	"trunc": kernelbridge.LoopOpTrunc,
	"abs":   kernelbridge.LoopOpAbs,
	// the bounded-image four: the kernel answers the interval each
	// clause names, which holds for EVERY conforming engine and needs
	// nothing of the operand. sqrt lands at or above +0; sin and cos
	// inside [-1, 1]; atan inside [-2, 2].
	"sqrt": kernelbridge.LoopOpSqrt,
	"sin":  kernelbridge.LoopOpSin,
	"cos":  kernelbridge.LoopOpCos,
	"atan": kernelbridge.LoopOpAtan,
}

// mathBinaryOps: the two-argument Math reads the effect wire carries.
// `Math.pow(a, b)` is `a ** b` — the same kernel row — and
// `Math.atan2(y, x)` rides its own interval. Both take EXACTLY two
// arguments; min and max are variadic and fold separately below.
var mathBinaryOps = map[string]kernelbridge.LoopEffectOp{
	"pow":   kernelbridge.LoopOpPow,
	"atan2": kernelbridge.LoopOpAtan2,
}

// booleanBinaryTokens: every binary operator whose VALUE is exactly
// true or false. The effect claims the two-value set {0,1} — exact as
// a set, reading neither operand — so it is admissible only when
// evaluating the operands cannot move state (writeAndCallFree).
var booleanBinaryTokens = map[ast.Kind]struct{}{
	ast.KindLessThanToken:                {},
	ast.KindGreaterThanToken:             {},
	ast.KindLessThanEqualsToken:          {},
	ast.KindGreaterThanEqualsToken:       {},
	ast.KindEqualsEqualsToken:            {},
	ast.KindEqualsEqualsEqualsToken:      {},
	ast.KindExclamationEqualsToken:       {},
	ast.KindExclamationEqualsEqualsToken: {},
	ast.KindInstanceOfKeyword:            {},
	ast.KindInKeyword:                    {},
}

// logicalTokens: the short-circuit operators. `a && b` evaluates to a
// on a falsy a and to b otherwise, so the JOIN of both operands' sets
// admits every value the expression can take — a superset on the arm
// the short-circuit picked, never an exclusion. Same for || and ??.
var logicalTokens = map[ast.Kind]struct{}{
	ast.KindAmpersandAmpersandToken: {},
	ast.KindBarBarToken:             {},
	ast.KindQuestionQuestionToken:   {},
}

// booleanPairEffect is the constant two-value set a boolean-valued
// operator produces — true rides 1 and false rides 0, the same
// encoding the true/false keyword constants below use.
func booleanPairEffect() kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
	}
}

// pureBuiltinReaders: `<root>.<name>` callees whose spec behavior is a
// READ — they inspect their arguments and global state and move
// nothing. The bool says whether the value is a PREDICATE (exactly
// true or false — the two-value set); false means the value has no
// scalar spelling and rides unknown. Reflect's write half
// (defineMetadata, set, deleteProperty…) is deliberately absent.
var pureBuiltinReaders = map[string]map[string]bool{
	"Reflect": {
		"getMetadata": false, "getOwnMetadata": false,
		"hasMetadata": true, "hasOwnMetadata": true,
		"getMetadataKeys": false, "getOwnMetadataKeys": false,
	},
	"Object": {
		"keys": false, "values": false, "entries": false,
		"getPrototypeOf": false, "getOwnPropertyNames": false,
		"getOwnPropertyDescriptor": false, "getOwnPropertySymbols": false,
	},
	"Array":  {"isArray": true},
	"Number": {"isInteger": true, "isFinite": true, "isNaN": true, "isSafeInteger": true},
}

// pureBuiltinEffect reads a call to a curated pure builtin: the callee
// must be exactly `<root>.<name>` on the global root identifier, and
// every argument must move nothing (write- and call-free) — an
// argument that runs code would need a statement, which an effect is
// not. Predicates answer the exact two-value set; the rest answer
// unknown, which is what a metadata object or key list is worth in a
// scalar slot.
func pureBuiltinEffect(call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	names, knownRoot := pureBuiltinReaders[access.Expression.Text()]
	if !knownRoot {
		return kernelbridge.LoopEffect{}, false
	}
	isPredicate, knownName := names[access.Name().Text()]
	if !knownName {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			if !writeAndCallFree(argument) {
				return kernelbridge.LoopEffect{}, false
			}
		}
	}
	if isPredicate {
		return booleanPairEffect(), true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
}

// writeAndCallFree answers whether evaluating the subtree can move any
// state the lowering tracks: no write form (an assignment, ++/--,
// delete) and no code the lowering does not run (a call, a `new`, an
// await, a yield, a tagged template). A getter behind a plain property
// read still runs code this test does not see — the same standing gap
// the ternary's write-free condition and the opaque branch's test
// accept.
//
// The walk descends THROUGH a function literal, so a subtree building a
// closure whose body writes or calls answers false. That reading is
// wrong about evaluation — creating a closure runs none of its body —
// and right about every caller that HANDS THE VALUE OVER, where the
// closure's later run is exactly what may not be lost. The callers that
// only need the evaluation question ask inertValue below.
func writeAndCallFree(node *ast.Node) bool {
	if node == nil {
		return true
	}
	if ast.IsBinaryExpression(node) {
		operator := node.AsBinaryExpression().OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			return false
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		operator := node.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	switch node.Kind {
	case ast.KindDeleteExpression, ast.KindCallExpression, ast.KindNewExpression,
		ast.KindAwaitExpression, ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return false
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !writeAndCallFree(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// inertValue answers writeAndCallFree's question with the function
// boundary drawn where evaluation really draws it: EVALUATING this
// expression moves nothing.
//
// The difference from writeAndCallFree is one rule. The walk STOPS at a
// function or class literal, because creating a closure runs none of its
// body: `{ [APP_GUARD]: guard => this.config.addGlobalGuard(guard) }`
// builds an object holding a function value and calls nothing, and the
// same holds for an arrow in an array literal or a ternary arm. Only
// CALLING the closure runs it, and a call is its own node this predicate
// already catches wherever it is written. `havocEnumerable`
// (ir_opaque_havoc.go:277) draws the boundary at the same place for the
// same question — a nested function's transfers leave IT, not this
// statement — and this predicate now agrees with it.
//
// What the boundary does NOT settle is WHEN the closure runs. Whoever
// receives the value may call it later, and the closure's writes land
// then. So every caller of this predicate must ALSO discharge that
// obligation, which is what closureWritesTracked below is for. Asking
// this one alone would trade a call the lowering does not run for a
// write the lowering does not see, which is the worse of the two.
func inertValue(node *ast.Node) bool {
	if node == nil {
		return true
	}
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		return true
	}
	if ast.IsBinaryExpression(node) {
		operator := node.AsBinaryExpression().OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			return false
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		operator := node.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	switch node.Kind {
	case ast.KindDeleteExpression, ast.KindCallExpression, ast.KindNewExpression,
		ast.KindAwaitExpression, ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return false
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !inertValue(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// closureAssignedNames collects the names every function or class
// literal INSIDE a subtree can write — the write set of the closures the
// subtree hands to whoever receives its value.
//
// This is the other half of inertValue's boundary. That predicate stops
// at a function literal because building one runs nothing; this one
// walks the bodies it stopped at, because a closure that leaves this
// body may be called at a time the lowering cannot place, and every name
// it assigns is a name nothing here may believe afterwards.
//
// The reading is the syntactic one every write model in this package
// takes: an assignment target, a compound target, a ++/-- operand, and
// each of those through a destructuring pattern's leaves. Both spellings
// of a step are recorded — the plain identifier `total` and the one-step
// path `this.count` — because the slot vector holds each under its own
// name (SpelledNameOf's two forms). Over-collection is the safe
// direction: a name with no slot answers nothing, and a name whose
// closure never runs only costs the caller a decline.
func closureAssignedNames(node *ast.Node, into map[string]struct{}) {
	if node == nil {
		return
	}
	var noteTarget func(target *ast.Node)
	var inClosure func(child *ast.Node) bool
	noteTarget = func(target *ast.Node) {
		target = Unwrapped(target)
		if target == nil {
			return
		}
		// a DESTRUCTURING target is a pattern of store positions
		switch {
		case ast.IsObjectLiteralExpression(target):
			for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
				switch {
				case ast.IsPropertyAssignment(property):
					noteTarget(property.AsPropertyAssignment().Initializer)
				case ast.IsShorthandPropertyAssignment(property):
					noteTarget(property.AsShorthandPropertyAssignment().Name())
				case ast.IsSpreadAssignment(property):
					noteTarget(property.AsSpreadAssignment().Expression)
				}
			}
			return
		case ast.IsArrayLiteralExpression(target):
			for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
				if ast.IsSpreadElement(element) {
					noteTarget(element.AsSpreadElement().Expression)
					continue
				}
				noteTarget(element)
			}
			return
		}
		if spelled, ok := SpelledNameOf(target); ok {
			into[spelled] = struct{}{}
		}
	}
	// inClosure walks a closure's BODY: every write form inside it, and
	// on through the closures nested inside that one.
	inClosure = func(child *ast.Node) bool {
		if child == nil {
			return false
		}
		if ast.IsBinaryExpression(child) {
			binary := child.AsBinaryExpression()
			operator := binary.OperatorToken.Kind
			if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
				noteTarget(binary.Left)
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			unary := child.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				noteTarget(unary.Operand)
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			unary := child.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				noteTarget(unary.Operand)
			}
		}
		if ast.IsDeleteExpression(child) {
			noteTarget(child.AsDeleteExpression().Expression)
		}
		child.ForEachChild(inClosure)
		return false
	}
	// the OUTER walk finds the closures; their bodies go to inClosure
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if child == nil {
			return false
		}
		if ast.IsFunctionLike(child) || ast.IsClassLike(child) {
			child.ForEachChild(inClosure)
			return false
		}
		child.ForEachChild(visit)
		return false
	}
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		node.ForEachChild(inClosure)
		return
	}
	node.ForEachChild(visit)
}

// closureWritesTracked answers whether a subtree hands over a closure
// that writes a name this lowering holds a slot for. A caller admitting
// a value whose closures escape asks this and keeps its decline when the
// answer is yes: the closure runs at a time no statement here places, so
// the slot it writes cannot be believed for the rest of the body and
// there is no statement position at which to say so.
//
// The membership test is the reader's HoldsPlace — slot membership with
// no sort filter, because a slot of ANY sort holds state a later closure
// run would falsify.
//
// A reader that supplies NO HoldsPlace has not said which names it
// holds, so this answers yes for any closure that writes at all. That is
// the conservative direction and it is the one the readers without a
// slot vector want: the loop solver reads names through a state map this
// seam does not see, and its own write guard (unknownIfPure) used to
// catch a closure-bearing literal only because the literal fell through
// to Opaque. Answering yes here keeps that decline exactly where it was.
// A closure that writes NOTHING is inert for every reader alike.
func closureWritesTracked(node *ast.Node, reader EffectReader) bool {
	written := map[string]struct{}{}
	closureAssignedNames(node, written)
	if len(written) == 0 {
		return false
	}
	if reader.HoldsPlace == nil {
		return true
	}
	for name := range written {
		if reader.HoldsPlace(name) {
			return true
		}
	}
	return false
}

// ClosureEscapesTrackedWrite is THE SHARED BOUNDARY RULE between the
// local census and the havoc floor, asked of a LoweringContext directly
// rather than through an effect reader.
//
// The two predicates draw the function boundary in opposite places, on
// purpose:
//
//   - collectSummaryLocals (ir_summary_body.go) STEPS OVER a nested
//     function, because its declarations are the inner function's and
//     laying out slots for them would give this body names it cannot
//     read;
//   - havocSlotsOfStatement (ir_opaque_havoc.go) walks INTO one, because
//     a closure closes over THIS body's names and calling it writes them.
//
// Those two answer the same question — which names of this body may a
// nested function move — only while every route that admits a statement
// holding a closure either falls to the floor or asks this predicate.
// A route that believes a slot value while handing over an arrow that
// writes that same slot is the disagreement, and it is unsound: the
// closure runs at a time no statement here places, so the belief cannot
// be retracted at any position.
//
// The reading is closureWritesTracked's, with slot membership answered
// from the context's own vector (slotIndexOfName), which is the same
// membership the census laid out and the floor havocs. A context with no
// slots answers false: there is no belief to falsify.
func ClosureEscapesTrackedWrite(context *LoweringContext, node *ast.Node) bool {
	if context == nil || node == nil {
		return false
	}
	return closureWritesTracked(node, EffectReader{
		HoldsPlace: func(spelled string) bool {
			_, found := slotIndexOfName(context, spelled)
			return found
		},
	})
}

// ContainsWrite is containsWrite in the TS source: does the subtree
// perform any write? A shape mapped to an opaque effect must be
// write-free, or the lowering's state would miss the write. Shared
// by both lowerings.
func ContainsWrite(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsWrite(child)
		}
		return false
	})
	return found
}

// EffectReader mirrors the destructured `reader` parameter of
// lowerEffectExpression in the TS source.
//
// ReadNode is the Go addition: a DEEP path read (`p.a.b`) that
// SpelledNameOf cannot spell, which nested-record flattening makes an
// ordinary slot read. Tried after ReadPlace declines and before Opaque;
// nil leaves the reading exactly as ReadPlace left it.
//
// HoldsPlace answers whether a spelled name has a SLOT at all, with no
// sort filter. ReadPlace cannot serve that question: it answers only
// where the slot's sort admits the read it is building — EffectOf's
// number gate turns a held string-sorted name into "not readable" — and
// the closure census needs to know which names have state to invalidate,
// which every held slot does whatever its sort. A reader that leaves it
// nil has not said what it holds, and the census reads that as "assume
// everything" (closureWritesTracked's own rule).
type EffectReader struct {
	ReadPlace  func(spelled string) (kernelbridge.LoopEffect, bool)
	ReadNode   func(e *ast.Node) (kernelbridge.LoopEffect, bool)
	Opaque     func(e *ast.Node) (kernelbridge.LoopEffect, bool)
	HoldsPlace func(spelled string) bool
}

// stringSlotEffect is a tracked STRING-sorted read as an effect, or
// (zero, false). Sequence building admits only the string sort: a
// number-sorted operand of `+` is arithmetic, not concatenation, and
// an unknown-sorted one is a reread across sorts, which is never a
// claim.
func stringSlotEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	i, ok := IndexOf(context, e)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Sorts[i] != BindingKindString {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}, true
}

// concatOf pairs two operand effects into one concatenation effect.
func concatOf(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &a, B: &b}
}

// SequenceEffectOf reads an expression as a SEQUENCE effect — the
// string world's half of the effect grammar, beside the numeric
// LowerEffectExpression:
//
//   - a string literal is its exact tuple;
//   - a tracked string-sorted name is a read;
//   - `a + b` with BOTH sides sequence-readable is their
//     concatenation (a `+` with a number-sorted operand is arithmetic
//     and is not read here — it takes the numeric route as before);
//   - a template literal `a${x}b` is that concatenation spelled out,
//     its literal chunks as exact tuples and its substitutions as
//     sequence reads.
//
// A non-string or untracked part declines the whole reading, exactly
// as it did before this route existed. Parens and casts unwrap first.
//
// A CALL is read only where the syntax already committed the expression
// to being a sequence — inside a `+` chain or a template substitution.
// Handed a bare call as the WHOLE expression this declines, because
// SortOfArg (ir_guard.go) reads a success here as "this argument is
// string-sorted", and a numeric callee's result is not.
func SequenceEffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	return sequenceEffectOf(context, e, false /*inSequence*/)
}

// sequenceEffectOf is the reading, carrying whether the caller has already
// committed the expression to the sequence world.
func sequenceEffectOf(context *LoweringContext, e *ast.Node, inSequence bool) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsStringLiteral().Text),
		}, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsNoSubstitutionTemplateLiteral().Text),
		}, true
	}
	if slot, ok := stringSlotEffect(context, head); ok {
		return slot, true
	}
	// a free (or imported) CONST whose initializer is a string literal —
	// nest's `DEFAULT_METHOD_KEY` behind
	// `this.staticMethodKey ??= DEFAULT_METHOD_KEY as StaticMethodKey`.
	// The declaration is one stable node in the program's shared AST
	// forest and a const's initializer is its value, so the tuple reads
	// directly, exactly as the numeric route's FreeConstEffect reads a
	// numeric const. The cast around it unwrapped above.
	if tuple, ok := freeStringConstEffect(context, head); ok {
		return tuple, true
	}
	// `a[i]` on a string-sorted flattened array is a sequence read — the
	// element slot, or-absent where nothing bounds i
	if slot, ok := ArrayElementSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return ArrayIndexReadEffect(context, head)
	}
	// `m.get(k)` on a string-sorted collection is the same or-absent
	// read of the values slot
	if slot, ok := MapValueSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return MapGetReadEffect(context, head)
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return kernelbridge.LoopEffect{}, false
		}
		// a `+` chain commits BOTH sides to the sequence world, so a call in
		// either operand is a call inside a sequence
		a, aOk := sequenceEffectOf(context, bin.Left, true /*inSequence*/)
		if !aOk {
			return kernelbridge.LoopEffect{}, false
		}
		b, bOk := sequenceEffectOf(context, bin.Right, true /*inSequence*/)
		if !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return concatOf(a, b), true
	}
	if ast.IsTemplateExpression(head) {
		return templateSequenceOf(context, head)
	}
	// a STRING METHOD whose receiver and arguments are all exactly known —
	// `"a-b".split("-")[0]` is not this, but `"Recharts".slice(0, 3)` is:
	// the whole call computes to one string here in Go, and the exact
	// tuple is what the wire carries. Ahead of the hoist, which would
	// otherwise spend a temp slot and answer unknown for a value that is
	// pinned.
	if exact, ok := exactStringMethodEffect(context, head); ok {
		return exact, true
	}
	// the trims over a NON-exact sequence-readable receiver ride the
	// proved sequence-unary rows: the result's scalars are drawn from
	// the receiver's and it is no longer (sec-trimstring removes by
	// code point, so no surrogate pair splits). slice/case/replace
	// have no proved row and keep the decline — slice cuts at UTF-16
	// units and can mint a lone surrogate the receiver's set never
	// admitted, and the case mappings replace scalars outright.
	//
	// `split` and `indexOf` are not declines and are not here: neither
	// answers a STRING. A split answers an ARRAY, lowered to the two
	// slots by ir_array_slots.go (its elem slot takes the same
	// drawn-from row under an astral-safe separator gate, its len slot
	// takes unknown); an indexOf answers a NUMBER, read by the numeric
	// reader's stringIndexOfEffect in ir_assignment.go.
	if ast.IsCallExpression(head) {
		call := head.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) &&
			(call.Arguments == nil || len(call.Arguments.Nodes) == 0) {
			pa := call.Expression.AsPropertyAccessExpression()
			var seqOp kernelbridge.LoopEffectOp
			switch pa.Name().Text() {
			case "trim":
				seqOp = kernelbridge.LoopOpTrim
			case "trimStart":
				seqOp = kernelbridge.LoopOpTrimStart
			case "trimEnd":
				seqOp = kernelbridge.LoopOpTrimEnd
			}
			if seqOp != "" {
				if receiver, ok := sequenceEffectOf(context, pa.Expression, true /*inSequence*/); ok {
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectSeqUnary, Op: seqOp, A: &receiver}, true
				}
			}
		}
	}
	// a CALL inside the sequence — `"n=" + this.name()`, a template
	// substitution `${this.name()}` — hoists to a temp-slot call statement
	// ahead of this statement, and the concatenation reads the temp
	// (ir_call_hoist.go). Refused wherever no statement stream exists or
	// the reordering could be observed, and then the reading declines
	// exactly as it did before.
	//
	// The hoist is asked only for a call whose spelling is INSIDE a
	// sequence the syntax already committed to — a bare call handed to this
	// reader on its own is not a sequence, and answering one here would
	// tell SortOfArg (ir_guard.go) that a numeric callee's result is
	// string-sorted, since SortOfArg reads "SequenceEffectOf succeeded" as
	// exactly that claim. So the whole-expression case declines and the
	// numeric reader (EffectOf's Opaque) hoists it instead; the memo means
	// the two never build two statements for one site.
	if inSequence && (ast.IsCallExpression(head) || isAwaitedCallShape(head)) {
		return HoistCallEffect(context, head)
	}
	// a string-sorted GETTER read inside a sequence: the same hoisted
	// call, admitted only where the temp's sort came out string — the
	// same commitment gate the explicit call above wears, for the same
	// SortOfArg reason
	if inSequence && ast.IsPropertyAccessExpression(head) {
		if held, ok := GetterReadEffect(context, head); ok &&
			held.Kind == kernelbridge.LoopEffectVar &&
			held.Index < len(context.Sorts) &&
			context.Sorts[held.Index] == BindingKindString {
			return held, true
		}
	}
	return kernelbridge.LoopEffect{}, false
}

// exactStringMethodEffect computes a string method call whose receiver
// and every argument are EXACTLY KNOWN strings or numbers, and answers
// the result as its own exact tuple.
//
// Why only the exact case. The effect wire carries `const` (a set),
// `var`, `concat`, `join` and `orAbsent` — and no string-method
// operation of any kind. A method over a receiver the lowering only
// knows a SET for would need a kernel-side transfer to answer, and there
// is no wire field to send it through; that is a kernel work order, not
// something this side can spell. What IS spellable is the case where
// nothing is unknown: the whole call has one value, that value is a
// string, and an exact tuple is precisely how the wire carries a string.
// So the pinned calls are served exactly and every other one keeps the
// reading it has today.
//
// The transcribed methods, each read from the vendored spec rather than
// from memory:
//
//	slice(start, end)   — sec-string.prototype.slice
//	toUpperCase()       — sec-string.prototype.touppercase
//	toLowerCase()       — sec-string.prototype.tolowercase
//	trim()              — sec-string.prototype.trim
//	concat(…)           — sec-string.prototype.concat
//
// `replace`, `split`, `indexOf`, `match` and the rest are deliberately
// absent: replace carries pattern and `$`-substitution semantics, split
// answers an ARRAY rather than a string, and indexOf answers a number
// and belongs to the numeric reader, not this one.
//
// THE CODE-UNIT GATE. The spec indexes and measures strings in UTF-16
// CODE UNITS; Go indexes bytes and ranges runes, and the set encoding
// (refinementsets.CodepointsOf) is in CODE POINTS. The three agree
// exactly when every character is BMP and non-surrogate, which
// allBasicPlane checks on the receiver and on every string argument
// before any of this runs. A string carrying an astral character
// declines and keeps its old reading, so the divergence is refused
// rather than approximated.
func exactStringMethodEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	if !ast.IsCallExpression(e) {
		return kernelbridge.LoopEffect{}, false
	}
	call := e.AsCallExpression()
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := exactSyntacticStringOf(access.Expression)
	if !receiverOk || !allBasicPlane(receiver) {
		return kernelbridge.LoopEffect{}, false
	}
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	result, ok := exactStringMethodResult(receiver, access.Name().Text(), arguments)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(result),
	}, true
}

// exactStringMethodResult is the per-method computation, each step the
// spec's own. Answers false for a method not transcribed here or for an
// argument shape the method's steps cannot take exactly.
func exactStringMethodResult(receiver string, method string, arguments []*ast.Node) (string, bool) {
	switch method {
	case "toUpperCase":
		if len(arguments) != 0 {
			return "", false
		}
		return strings.ToUpper(receiver), true
	case "toLowerCase":
		if len(arguments) != 0 {
			return "", false
		}
		return strings.ToLower(receiver), true
	case "trim":
		// the spec trims the WhiteSpace and LineTerminator code points;
		// strings.TrimSpace trims Unicode space, which differs on a handful
		// of code points — so the trim is spelled out against the spec's own
		// set rather than delegated
		if len(arguments) != 0 {
			return "", false
		}
		return strings.Trim(receiver, ecmaWhitespace), true
	case "concat":
		out := receiver
		for _, argument := range arguments {
			text, ok := exactSyntacticStringOf(argument)
			if !ok || !allBasicPlane(text) {
				return "", false
			}
			out += text
		}
		return out, true
	case "slice":
		// sec-string.prototype.slice, steps 4-14, on a code-unit-indexable
		// receiver (the caller's allBasicPlane gate makes rune indexing the
		// same indexing)
		if len(arguments) == 0 || len(arguments) > 2 {
			return "", false
		}
		units := []rune(receiver)
		length := len(units)
		intStart, startOk := exactIntegerOf(arguments[0])
		if !startOk {
			return "", false
		}
		from := 0
		if intStart < 0 {
			from = max(length+intStart, 0)
		} else {
			from = min(intStart, length)
		}
		to := length
		if len(arguments) == 2 {
			intEnd, endOk := exactIntegerOf(arguments[1])
			if !endOk {
				return "", false
			}
			if intEnd < 0 {
				to = max(length+intEnd, 0)
			} else {
				to = min(intEnd, length)
			}
		}
		if from >= to {
			return "", true
		}
		return string(units[from:to]), true
	}
	return "", false
}

// ecmaWhitespace is the code points `String.prototype.trim` removes:
// the spec's WhiteSpace production (TAB, VT, FF, SP, NBSP, ZWNBSP, and
// the Unicode Space_Separator points) together with LineTerminator (LF,
// CR, LS, PS). Spelled as ESCAPES rather than as literal characters —
// the set includes a zero-width no-break space, which a Go compiler
// reads as a byte-order mark when it appears literally in source — and
// not delegated to strings.TrimSpace, whose set is Unicode's and
// differs.
const ecmaWhitespace = "\t\v\f \u00a0\ufeff\n\r\u2028\u2029" +
	"\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a" +
	"\u202f\u205f\u3000"

// allBasicPlane is whether every character is a BMP non-surrogate, which
// is when UTF-16 code units, Unicode code points, and Go runes all index
// and count the same. The exact string readings above run only on
// strings that pass.
func allBasicPlane(s string) bool {
	for _, r := range s {
		if r > 0xffff || (r >= 0xd800 && r <= 0xdfff) {
			return false
		}
	}
	return true
}

// exactSyntacticStringOf is the string an expression exactly IS, by syntax alone:
// a string literal, a substitution-free template, or a `+` chain of
// those. No name is read — a slot's set is a set, and this reader wants
// the one value case only.
func exactSyntacticStringOf(e *ast.Node) (string, bool) {
	head := Unwrapped(e)
	if head == nil {
		return "", false
	}
	if ast.IsStringLiteral(head) {
		return head.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return head.AsNoSubstitutionTemplateLiteral().Text, true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return "", false
		}
		left, leftOk := exactSyntacticStringOf(bin.Left)
		if !leftOk {
			return "", false
		}
		right, rightOk := exactSyntacticStringOf(bin.Right)
		if !rightOk {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// exactIntegerOf is the integer an argument exactly IS — a numeric
// literal or its negation, with a whole value. A fractional or
// non-literal argument declines: ToIntegerOrInfinity would truncate it,
// and this reader serves the pinned case rather than modelling the
// coercion.
func exactIntegerOf(e *ast.Node) (int, bool) {
	head := Unwrapped(e)
	if head == nil {
		return 0, false
	}
	sign := 1
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		switch unary.Operator {
		case ast.KindMinusToken:
			sign = -1
		case ast.KindPlusToken:
		default:
			return 0, false
		}
		head = Unwrapped(unary.Operand)
	}
	if head == nil || !ast.IsNumericLiteral(head) {
		return 0, false
	}
	value := float64(jsnum.FromString(head.AsNumericLiteral().Text))
	if value != math.Trunc(value) || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return sign * int(value), true
}

// isAwaitedCallShape is `await f(…)` — the one wrapper the hoist route
// peels, spelled here so the sequence reader can ask before handing the
// node over.
func isAwaitedCallShape(e *ast.Node) bool {
	operand, isAwait := AwaitedOperandOf(e)
	return isAwait && ast.IsCallExpression(operand)
}

// SpelledSequenceShape is whether an expression is a sequence by its
// own SYNTAX alone — a string literal, any template literal, or a `+`
// chain of those. It reads no names and consults no slot vector, so it
// answers the same in any context: the sort question a caller must
// settle before the callee's slots exist.
func SpelledSequenceShape(e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) || ast.IsNoSubstitutionTemplateLiteral(head) ||
		ast.IsTemplateExpression(head) {
		return true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return false
		}
		return SpelledSequenceShape(bin.Left) && SpelledSequenceShape(bin.Right)
	}
	return false
}

// templateSequenceOf is `a${x}b${y}c` as a right-nested chain of the
// concatenation effect: the head's literal text, then per span the
// substituted expression's sequence reading followed by that span's
// literal text. An empty literal chunk contributes the empty tuple,
// which concatenates to nothing — kept rather than special-cased, so
// the chain's shape is one rule.
func templateSequenceOf(context *LoweringContext, head *ast.Node) (kernelbridge.LoopEffect, bool) {
	template := head.AsTemplateExpression()
	if template.TemplateSpans == nil {
		return kernelbridge.LoopEffect{}, false
	}
	parts := []kernelbridge.LoopEffect{{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(template.Head.AsTemplateHead().Text),
	}}
	for _, spanNode := range template.TemplateSpans.Nodes {
		span := spanNode.AsTemplateSpan()
		// a substitution sits INSIDE a sequence the template already
		// committed to, so a call there hoists
		substituted, ok := sequenceEffectOf(context, span.Expression, true /*inSequence*/)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, substituted)
		var text string
		switch {
		case ast.IsTemplateMiddle(span.Literal):
			text = span.Literal.AsTemplateMiddle().Text
		case ast.IsTemplateTail(span.Literal):
			text = span.Literal.AsTemplateTail().Text
		default:
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(text),
		})
	}
	// fold right so the chain nests the way the kernel's Concatenation
	// form does
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		out = concatOf(parts[i], out)
	}
	return out, true
}

// LowerEffectExpression is lowerEffectExpression in the TS source.
func LowerEffectExpression(e *ast.Node, reader EffectReader) (kernelbridge.LoopEffect, bool) {
	if ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) {
		var inner *ast.Node
		switch {
		case ast.IsParenthesizedExpression(e):
			inner = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			inner = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			inner = e.AsNonNullExpression().Expression
		}
		return LowerEffectExpression(inner, reader)
	}
	if ast.IsNumericLiteral(e) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(jsnum.FromString(e.AsNumericLiteral().Text))})),
		}, true
	}
	if e.Kind == ast.KindTrueKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}, true
	}
	if e.Kind == ast.KindFalseKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))}, true
	}
	if spelled, ok := SpelledNameOf(e); ok {
		if held, ok := reader.ReadPlace(spelled); ok {
			return held, true
		}
		if reader.ReadNode != nil {
			if held, ok := reader.ReadNode(e); ok {
				return held, true
			}
		}
		return reader.Opaque(e)
	}
	// a DEEP path (`p.a.b`) has no one-step spelling; a flattened nested
	// record gives it a slot all the same
	if ast.IsPropertyAccessExpression(e) && reader.ReadNode != nil {
		if held, ok := reader.ReadNode(e); ok {
			return held, true
		}
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			if ast.IsNumericLiteral(unary.Operand) {
				return kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectConst,
					Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))})),
				}, true
			}
			a, ok := LowerEffectExpression(unary.Operand, reader)
			if !ok {
				return kernelbridge.LoopEffect{}, false
			}
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: kernelbridge.LoopOpNeg, A: &a}, true
		}
		if unary.Operator == ast.KindPlusToken {
			return LowerEffectExpression(unary.Operand, reader)
		}
		// `!x` always produces exactly true or false — the two-value set,
		// under the same moves-nothing gate the comparisons wear
		if unary.Operator == ast.KindExclamationToken && writeAndCallFree(unary.Operand) {
			return booleanPairEffect(), true
		}
		return reader.Opaque(e)
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		// a COMPARISON (and instanceof/in) always produces exactly true or
		// false: the two-value set is the exact answer, and no operand is
		// read — admitted only where evaluating the operands moves nothing.
		// A gate failure falls to Opaque, exactly what the operator did
		// before this arm existed.
		if _, isBoolean := booleanBinaryTokens[bin.OperatorToken.Kind]; isBoolean && writeAndCallFree(e) {
			return booleanPairEffect(), true
		}
		// a SHORT-CIRCUIT operator's value is one of its operands, so the
		// join of both admits every run. A side that does not lower falls
		// to Opaque — again the operator's old path.
		if _, isLogical := logicalTokens[bin.OperatorToken.Kind]; isLogical {
			a, aOk := LowerEffectExpression(bin.Left, reader)
			b, bOk := LowerEffectExpression(bin.Right, reader)
			if aOk && bOk {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
			}
			if held, ok := reader.Opaque(e); ok {
				return held, true
			}
			// a side with no scalar spelling — `radius[i] ?? 0` where the
			// index read finds no element slot — still produces ONE of two
			// operands, and evaluating both moved nothing. Unknown is exactly
			// what a slot can hold of that: the value is unconstrained and no
			// state changed, so the read costs precision here and nothing
			// anywhere else. (Gated on the whole expression moving nothing,
			// the same gate the comparison and literal arms wear; an operand
			// that RUNS something keeps the decline, since its effects need a
			// statement this grammar cannot emit.) An arm holding a CLOSURE
			// that writes a tracked name keeps the decline too — the value
			// escapes with the closure inside it, and no statement here
			// places the later run (closureWritesTracked's own rule).
			if inertValue(e) && !closureWritesTracked(e, reader) {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
			}
			return kernelbridge.LoopEffect{}, false
		}
		op, ok := binOps[bin.OperatorToken.Kind]
		if !ok {
			return reader.Opaque(e)
		}
		a, aOk := LowerEffectExpression(bin.Left, reader)
		b, bOk := LowerEffectExpression(bin.Right, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
	}
	if ast.IsConditionalExpression(e) {
		cond := e.AsConditionalExpression()
		if ContainsWrite(cond.Condition) {
			return kernelbridge.LoopEffect{}, false
		}
		a, aOk := LowerEffectExpression(cond.WhenTrue, reader)
		b, bOk := LowerEffectExpression(cond.WhenFalse, reader)
		if aOk && bOk {
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
		}
		// an ARM with no scalar spelling: the value is one of the two arms
		// and evaluating the whole thing moved nothing, so unknown is what
		// a slot holds of it — the same reading the short-circuit operators
		// take, which is what a ternary is a spelling of. An arm that RUNS
		// something keeps the decline; its effects need a statement. So
		// does an arm handing over a CLOSURE that writes a tracked name —
		// the later run has no statement position here.
		if inertValue(e) && !closureWritesTracked(e, reader) {
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
		}
		return kernelbridge.LoopEffect{}, false
	}
	// an OBJECT LITERAL, ARRAY LITERAL, or TEMPLATE whose evaluation
	// moves nothing: the value has no scalar spelling — unknown IS what
	// a slot can hold of it — and building it changed no state, so the
	// unknown claim costs the read and nothing else. A literal whose
	// parts run code keeps the old path (its effects need a statement).
	//
	// A literal carrying FUNCTION VALUES — nest's
	// `{ [APP_GUARD]: guard => this.config.addGlobalGuard(guard) }` —
	// builds them without running them, so it passes the gate above. The
	// second gate is the one those closures need: the literal is the
	// caller's now, and a closure that writes a name this body tracks may
	// run at any later time. Where it writes nothing tracked, the value
	// is inert here and the read stands; where it does, the decline stays
	// and the statement takes the floor, which havocs those names.
	if (ast.IsObjectLiteralExpression(e) || ast.IsArrayLiteralExpression(e) ||
		ast.IsTemplateExpression(e)) && inertValue(e) &&
		!closureWritesTracked(e, reader) {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
	}
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		// a PURE BUILTIN read — `Reflect.getMetadata(...)`, `Object.keys(x)`,
		// `Array.isArray(x)`: the callee reads without moving anything, so
		// with write-and-call-free arguments the whole expression moves
		// nothing and its value spells as its contract promises — the
		// two-value set for the predicates, unknown for the rest. The list
		// is curated read-only spec behavior, never guessed.
		if effect, pure := pureBuiltinEffect(call); pure {
			return effect, true
		}
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Math" {
				name := access.Name().Text()
				if un, ok := mathOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
					a, ok := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !ok {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: un, A: &a}, true
				}
				// `Math.pow(a, b)` and `Math.atan2(y, x)` — exactly two
				// arguments, in the order the clause names them. pow is the
				// same kernel row `a ** b` lowers to; atan2 answers its own
				// interval. Any other arity falls through to Opaque, which is
				// what these names did before this arm existed.
				if bin, ok := mathBinaryOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 2 {
					a, aOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					b, bOk := LowerEffectExpression(call.Arguments.Nodes[1], reader)
					if !aOk || !bOk {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectBinary, Op: bin, A: &a, B: &b,
					}, true
				}
				// `Math.min(a, b, c, …)` — variadic by the spec, and the wire
				// carries min and max as BINARIES. min is associative, so the
				// n-ary call folds left into nested binaries and means exactly
				// what the call means; the two-argument case is the fold's own
				// first step and lowers identically to what it always did.
				//
				// ONE argument (`Math.min(x)`) is the argument itself after
				// ToNumber, which the numeric reader already gives. ZERO
				// arguments answers +∞ for min and -∞ for max — the identity
				// each fold starts from — and neither is a set this reader
				// builds, so both decline.
				if (name == "min" || name == "max") && call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
					op := kernelbridge.LoopOpMin
					if name == "max" {
						op = kernelbridge.LoopOpMax
					}
					folded, foldedOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !foldedOk {
						return kernelbridge.LoopEffect{}, false
					}
					for _, argument := range call.Arguments.Nodes[1:] {
						next, nextOk := LowerEffectExpression(argument, reader)
						if !nextOk {
							return kernelbridge.LoopEffect{}, false
						}
						left, right := folded, next
						folded = kernelbridge.LoopEffect{
							Kind: kernelbridge.LoopEffectBinary, Op: op, A: &left, B: &right,
						}
					}
					return folded, true
				}
			}
		}
		return reader.Opaque(e)
	}
	return reader.Opaque(e)
}
