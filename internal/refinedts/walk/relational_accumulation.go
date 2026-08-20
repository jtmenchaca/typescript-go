// Accumulate-then-divide-by-count, recognized in the walk and lowered
// whole to the kernel's relational statement.
//
//	let total = 0;
//	for (const s of clamped) { total += s * s; }
//	const mean = total / clamped.length;
//
// and its return twin, where the division sits nested inside the
// returned expression rather than binding a name:
//
//	let total = 0;
//	for (const s of clamped) { total += s * s; }
//	return Math.sqrt(total / clamped.length);
//
// The three pieces are one fact and no fewer. The loop alone answers
// `total` as an enclosure — [0, +inf) when the count is a runtime
// length — and a division of that enclosure by the length's own
// enclosure gives [0, len], never [0, 1]. What ties them is the
// RELATION `total <= count * elemHi`, which the kernel carries
// internally across a "loopAccum" statement and consumes at a division
// of the same two slots. The relation lives for the length of one
// kernel walk, so the loop and the division lower into ONE program
// here — not two per-statement delegations.
//
// The route is the ordinary engine meet (kernel_delegation.go): the
// harvested slots' knowledge becomes the entry states, the kernel walks
// the two statements, and the exit claims for `total` and `mean` MEET
// what the walk already holds. A decline anywhere leaves both statements
// to their ordinary handling, unchanged.
//
// The ask is kernel.WalkRelational, not kernel.Walk — the certified walk
// Walk selects drops the linear ledger, so the relation would never
// reach the division. AskRelationalAccumulation's own comment carries
// the whole reason.
//
// HOW THE QUOTIENT REACHES THE WALK differs by shape, and that is the
// only difference between them. A declaration binds a name, so the
// quotient meets into that name. A return binds nothing, so it pins on
// the division NODE through ctx.NodeOverrides (flow_context.go) and the
// return statement then walks ordinarily around it — it is never
// skipped, and it judges against the enclosing annotation exactly as it
// would have, with the one node pre-narrowed.
//
// DESIGN NOTE — why the node override rather than lowering the return.
// The alternative considered was folding the whole returned expression
// into the kernel program: `Math.sqrt` has a kernel op (LoopOpSqrt), so
// `return Math.sqrt(total / len)` could lower as one more assign into a
// #ret slot with no new walk mechanism at all. It is recorded as the
// FALLBACK if the override's read cost ever shows on the recharts wall.
// It was not chosen because the effect grammar's op vocabulary is closed
// (effect_expression.go's own comment), so it recognizes strictly fewer
// return shapes than the node override does — and the Python adapter
// already recognizes the general shape. An adapter that recognizes less
// than its twin is the failure this staging exists to prevent.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// The slot layout the lowered program uses. Fixed, because the whole
// program is built here: nothing else allocates into this vector.
const (
	accumTotalSlot   = 0
	accumElementSlot = 1
	accumLenSlot     = 2
	accumMeanSlot    = 3
)

// RelationalAccumulation is one recognized accumulate-then-divide run:
// the two source statements it consumed, the names it writes back, and
// the lowered program the kernel walks.
type RelationalAccumulation struct {
	// TotalName: the accumulator the loop sums into.
	TotalName string
	// MeanName: the name the division writes, for the declaration shape.
	// "" for the return shape, which binds nothing — its quotient lands
	// on DivisionNode instead.
	MeanName string
	// DivisionNode: the division expression the proved quotient pins on,
	// for the return shape. Nil for a declaration.
	DivisionNode *ast.Node
	// States: the entry state per slot, in the fixed slot order above.
	States []kernelbridge.KnownStateWire
	// Stmts: the loopAccum statement and the division assignment that
	// consumes its relation — one program, in this order.
	Stmts []kernelbridge.IrStatement
	// Grade: the weakest trust among the participants' held knowledge.
	Grade abstractdomain.TrustLevel
}

// RelationalAccumulationOf recognizes the pattern starting at
// statements[index] and builds the program the kernel walks. Answers
// (nil, false) — no claim, ordinary handling stands — wherever any
// piece declines.
//
// The conservative declines, each because the relation it would carry
// is not the relation the source states:
//
//   - the loop is not a plain `for (const s of xs)` over a tracked name,
//     or awaits each element (`for await`), or binds a pattern;
//   - the accumulator does not hold an EXACT start value at the loop
//     (a `let total = 0` the walk already read as {0}, or any other
//     single value);
//   - the loop body is anything but one `total += <term>` statement, or
//     the term reads a name outside the element and the accumulator;
//   - the sequence's element knowledge is not a scalar state, or its
//     `.length` is not a number-sorted state;
//   - the division does not divide the SAME accumulator by the SAME
//     sequence's length — a second sequence's count, or a length read
//     off a name the loop never iterated, carries no relation at all;
//   - the second statement is neither a single-declarator declaration
//     nor a return;
//   - a RETURN whose expression holds the division zero times (nothing
//     to fold) or two or more times (one published quotient cannot
//     stand for two nodes), or holds it only inside a nested function
//     body, which is a separate scope running an unstated number of
//     times;
//   - the divided name collides with a participant, or the denominator
//     is a `let` binding whose value at the division the declaration
//     does not fix;
//   - either statement writes the accumulator, the sequence, or the
//     divided name — through the term, through a call the term makes,
//     or through the division's own right side;
//   - an entry state is past the meet budget, or the caller has no
//     checker to evaluate the length read with.
func RelationalAccumulationOf(
	ctx *FlowContext, env Env, statements []*ast.Node, index int,
) (*RelationalAccumulation, bool) {
	if index+1 >= len(statements) {
		return nil, false
	}
	loop, loopOk := accumulationLoopOf(statements[index])
	if !loopOk {
		return nil, false
	}
	division, divisionOk := accumulationDivisionOf(ctx, statements[index+1], loop)
	if !divisionOk {
		return nil, false
	}
	if !accumulationWritesAreOnlyTheSum(ctx, loop, statements[index], statements[index+1], division) {
		return nil, false
	}
	return accumulationProgram(ctx, env, loop, division)
}

// accumulationWritesAreOnlyTheSum is the write gate: across the two
// statements, the ONLY name that moves is the accumulator, and it moves
// only through the recognized `total += <term>`. A write to the sequence
// or to the length binding — by the term, by a call the term makes, or
// by the division's own right side — would make the count the program
// sends a different count from the one the passes ran, so the relation
// would tie the total to a number the source never divided by.
//
// The scan reads the loop's TERM rather than the whole loop: the `+=`
// into the accumulator is the shape the recognizer already matched, and
// the pass binding is the loop's own fresh declaration.
func accumulationWritesAreOnlyTheSum(
	ctx *FlowContext, loop accumulationLoop, loopStatement *ast.Node,
	divisionStatement *ast.Node, division accumulationDivision,
) bool {
	c := checkerOf(ctx)
	written := map[string]struct{}{}
	AssignedNames(c, loop.Term, written)
	CallMediatedWrites(c, contractsOf(ctx), loop.Term, written, nil)
	// the sequence expression the loop steps and the second statement
	// each evaluate here too — a getter or a call in either could move
	// any of the participants. For the RETURN shape this covers the whole
	// returned expression, which is where a wrapper call around the
	// division would sit (`Math.sqrt(bump(total) / xs.length)`).
	AssignedNames(c, loopStatement.AsForInOrOfStatement().Expression, written)
	AssignedNames(c, divisionStatement, written)
	CallMediatedWrites(c, contractsOf(ctx), divisionStatement, written, nil)
	participants := []string{loop.TotalName, loop.SourceName}
	if division.MeanName != "" {
		// the return shape binds no name, so it has none to protect
		participants = append(participants, division.MeanName)
	}
	for _, name := range participants {
		if _, moves := written[name]; moves {
			return false
		}
	}
	// the length read's own receiver is the sequence, already checked
	// above; a `const n = xs.length` denominator adds no name of its own
	// that the two statements could write
	return true
}

// contractsOf is the contract map CallMediatedWrites reads, or nil where
// no flow context carries one.
func contractsOf(ctx *FlowContext) map[*ast.Symbol]*FunctionContract {
	if ctx == nil {
		return nil
	}
	return ctx.Contracts
}

// accumulationLoop is the loop half the recognizer read: which name the
// pass binds, which sequence it iterates, which accumulator the body
// sums into, and the term one pass adds.
type accumulationLoop struct {
	ElementName string
	SourceName  string
	TotalName   string
	Term        *ast.Node
}

// accumulationLoopOf reads `for (const s of xs) { total += <term>; }`.
// Everything else declines: a pattern binding, a `for await`, a
// non-identifier sequence, a body with any statement other than the one
// compound add.
func accumulationLoopOf(statement *ast.Node) (accumulationLoop, bool) {
	if !ast.IsForOfStatement(statement) {
		return accumulationLoop{}, false
	}
	forOf := statement.AsForInOrOfStatement()
	// `for await (… of …)` binds the AWAITED value, which the element
	// state does not hold
	if forOf.AwaitModifier != nil {
		return accumulationLoop{}, false
	}
	source := Unwrapped(forOf.Expression)
	if source == nil || !ast.IsIdentifier(source) {
		return accumulationLoop{}, false
	}
	elementName, elementOk := accumulationElementNameOf(forOf.Initializer)
	if !elementOk {
		return accumulationLoop{}, false
	}
	body := StatementsOf(forOf.Statement)
	if len(body) != 1 || !ast.IsExpressionStatement(body[0]) {
		return accumulationLoop{}, false
	}
	add := Unwrapped(body[0].AsExpressionStatement().Expression)
	if add == nil || !ast.IsBinaryExpression(add) {
		return accumulationLoop{}, false
	}
	bin := add.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindPlusEqualsToken {
		return accumulationLoop{}, false
	}
	target := Unwrapped(bin.Left)
	if target == nil || !ast.IsIdentifier(target) {
		return accumulationLoop{}, false
	}
	// the accumulator and the pass binding are two distinct names; a body
	// summing the element into itself is not this shape
	if target.Text() == elementName || target.Text() == source.Text() {
		return accumulationLoop{}, false
	}
	return accumulationLoop{
		ElementName: elementName,
		SourceName:  source.Text(),
		TotalName:   target.Text(),
		Term:        bin.Right,
	}, true
}

// accumulationElementNameOf reads the pass binding's name from a for-of
// initializer: `const s` / `let s`, one declarator, a plain identifier.
// A destructuring pattern declines — the element slot holds one scalar,
// and a pattern reads into a shape it does not have — and so does
// `for (s of xs)` over an outer name, whose value survives the loop and
// would need a write-back this program does not carry.
func accumulationElementNameOf(initializer *ast.Node) (string, bool) {
	if initializer == nil || !ast.IsVariableDeclarationList(initializer) {
		return "", false
	}
	declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return "", false
	}
	name := declarations[0].AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return "", false
	}
	return name.Text(), true
}

// accumulationDivision is the division half: the node whose `.length`
// it divides by — the length read itself, so the entry state comes from
// evaluating exactly what the source spells — plus how the quotient
// reaches the rest of the walk.
//
// The two shapes differ only in that last part. A DECLARATION binds the
// quotient to a name (`const mean = total / xs.length`), so the meet
// lands on the name and MeanName carries it. A RETURN computes the
// quotient inside an expression (`return Math.sqrt(total / xs.length)`)
// and binds nothing, so there is no name to meet into: DivisionNode
// names the division node itself, and the proved quotient is pinned
// there through ctx.NodeOverrides while the return walks ordinarily
// around it.
type accumulationDivision struct {
	// MeanName: the bound name, for the declaration shape; "" for a
	// return, which binds nothing.
	MeanName string
	// DivisionNode: the division expression itself, for the return
	// shape — the node the proved quotient pins on. Nil for a
	// declaration, whose quotient reaches the walk through its name.
	DivisionNode *ast.Node
	LengthNode   *ast.Node
}

// accumulationDivisionOf reads `const mean = total / xs.length` — the
// SAME accumulator the loop summed, over the SAME sequence it iterated.
// The denominator may also be a name the walk resolved to that
// sequence's own length (`const n = xs.length` before the loop), which
// accumulationLengthNodeOf resolves back to the length read.
func accumulationDivisionOf(ctx *FlowContext, statement *ast.Node, loop accumulationLoop) (accumulationDivision, bool) {
	if ast.IsReturnStatement(statement) {
		return accumulationReturnDivisionOf(ctx, statement, loop)
	}
	if !ast.IsVariableStatement(statement) {
		return accumulationDivision{}, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return accumulationDivision{}, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) || declaration.Initializer == nil {
		return accumulationDivision{}, false
	}
	initializer := Unwrapped(declaration.Initializer)
	if initializer == nil || !ast.IsBinaryExpression(initializer) {
		return accumulationDivision{}, false
	}
	bin := initializer.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindSlashToken {
		return accumulationDivision{}, false
	}
	numerator := Unwrapped(bin.Left)
	if numerator == nil || !ast.IsIdentifier(numerator) || numerator.Text() != loop.TotalName {
		return accumulationDivision{}, false
	}
	lengthNode, lengthOk := accumulationLengthNodeOf(ctx, bin.Right, loop.SourceName)
	if !lengthOk {
		return accumulationDivision{}, false
	}
	// the division's own name must not collide with any participant —
	// the four slots are distinct bindings
	written := name.Text()
	if written == loop.TotalName || written == loop.SourceName || written == loop.ElementName {
		return accumulationDivision{}, false
	}
	return accumulationDivision{MeanName: written, LengthNode: lengthNode}, true
}

// accumulationReturnDivisionOf reads `return <expr>` where <expr>
// CONTAINS the division `total / <len>` exactly once, at any depth —
// `return Math.sqrt(total / clamped.length)` is the fixture's own shape,
// where the division is a call argument rather than the returned
// expression itself.
//
// Exactly once, and the count is the gate. Zero means there is nothing
// to fold. Two or more means one published quotient would have to stand
// for two nodes: both would read the same value, so the honest move is
// to fold neither and let the ordinary walk evaluate them all.
func accumulationReturnDivisionOf(
	ctx *FlowContext, statement *ast.Node, loop accumulationLoop,
) (accumulationDivision, bool) {
	returned := statement.AsReturnStatement().Expression
	if returned == nil {
		return accumulationDivision{}, false
	}
	found, count := accumulationDivisionsIn(ctx, returned, loop)
	if count != 1 || found == nil {
		return accumulationDivision{}, false
	}
	lengthNode, lengthOk := accumulationLengthNodeOf(
		ctx, found.AsBinaryExpression().Right, loop.SourceName)
	if !lengthOk {
		return accumulationDivision{}, false
	}
	// no bound name: the quotient reaches the walk pinned on the node
	return accumulationDivision{DivisionNode: found, LengthNode: lengthNode}, true
}

// accumulationDivisionsIn counts every `total / <len>` inside an
// expression and remembers the first, walking every subexpression.
// The walk never stops early once one is found — telling "exactly one"
// from "more than one" is the whole point of the count.
//
// A NESTED FUNCTION BODY is not walked. An arrow, a function expression
// or a class body is a separate scope whose `total` is a different
// binding, and a division inside one runs an unstated number of times
// (never, once per call, many) — so it can never be shown to evaluate
// exactly once here. This is CollectLocals' own boundary
// (tracked_bindings.go), spelled the same way.
func accumulationDivisionsIn(
	ctx *FlowContext, expression *ast.Node, loop accumulationLoop,
) (*ast.Node, int) {
	var found *ast.Node
	count := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			return false
		}
		if isRelationalDivision(ctx, node, loop) {
			if found == nil {
				found = node
			}
			count++
			// the operands are a name and a length read: neither can hold
			// a second occurrence, so the walk stops descending here
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(expression)
	return found, count
}

// isRelationalDivision is whether a node is exactly
// `<total> / <the sequence's length>` for the accumulator and sequence
// this accumulation named.
func isRelationalDivision(ctx *FlowContext, node *ast.Node, loop accumulationLoop) bool {
	if node == nil || !ast.IsBinaryExpression(node) {
		return false
	}
	bin := node.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindSlashToken {
		return false
	}
	numerator := Unwrapped(bin.Left)
	if numerator == nil || !ast.IsIdentifier(numerator) || numerator.Text() != loop.TotalName {
		return false
	}
	_, lengthOk := accumulationLengthNodeOf(ctx, bin.Right, loop.SourceName)
	return lengthOk
}

// accumulationLengthNodeOf resolves the denominator to a `.length` read
// on the iterated sequence: `xs.length` written out, or a `const n =
// xs.length` binding whose declaration spells the same read. A `let`
// binding declines — its value at the division is not fixed by the
// declaration — and so does any denominator naming a different sequence,
// which carries no relation to the count the loop ran.
func accumulationLengthNodeOf(ctx *FlowContext, denominator *ast.Node, sourceName string) (*ast.Node, bool) {
	read := Unwrapped(denominator)
	if read == nil {
		return nil, false
	}
	if isLengthReadOf(read, sourceName) {
		return read, true
	}
	c := checkerOf(ctx)
	if !ast.IsIdentifier(read) || c == nil {
		return nil, false
	}
	symbol := symbolAt(c, read)
	if symbol == nil || symbol.ValueDeclaration == nil || !ast.IsVariableDeclaration(symbol.ValueDeclaration) {
		return nil, false
	}
	declaration := symbol.ValueDeclaration
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return nil, false
	}
	initializer := Unwrapped(declaration.AsVariableDeclaration().Initializer)
	if initializer == nil || !isLengthReadOf(initializer, sourceName) {
		return nil, false
	}
	return initializer, true
}

// isLengthReadOf is `<name>.length` on the given name, through the
// parens and casts every receiver test peels first.
func isLengthReadOf(node *ast.Node, name string) bool {
	if node == nil || !ast.IsPropertyAccessExpression(node) {
		return false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) ||
		access.Name().Text() != "length" {
		return false
	}
	receiver := Unwrapped(access.Expression)
	return receiver != nil && ast.IsIdentifier(receiver) && receiver.Text() == name
}

// accumulationProgram builds the four entry states and the two
// statements, or declines. Every state comes from the knowledge the
// walk already holds — the accumulator's own value, the sequence's
// element, the length read's own evaluation — so nothing here invents a
// fact the ordinary walk did not already carry.
func accumulationProgram(
	ctx *FlowContext, env Env, loop accumulationLoop, division accumulationDivision,
) (*RelationalAccumulation, bool) {
	// the length read below evaluates through the ordinary expression
	// route, which asks the host's type at the occurrence — a ctx-less
	// caller has no such reach and declines here rather than crashing in it
	if checkerOf(ctx) == nil {
		return nil, false
	}
	held, heldOk := env.Get(loop.TotalName)
	if !heldOk {
		return nil, false
	}
	// the accumulator's start is EXACT or nothing: the relation the
	// kernel carries is about what the passes added, and an entry
	// enclosure with room in it would leave the total's own floor
	// unrelated to the count
	if held.Kind != abstractdomain.KindValues || len(held.Values) != 1 {
		return nil, false
	}
	totalState, totalOk := StateOfKnown(held)
	if !totalOk || totalState.Top {
		return nil, false
	}
	sequence, sequenceOk := env.Get(loop.SourceName)
	if !sequenceOk {
		return nil, false
	}
	elementState, elementOk := StateOfKnown(ElementOf(sequence))
	if !elementOk || elementState.Top {
		return nil, false
	}
	length := evaluateExpression(ctx, env, division.LengthNode)
	lengthState, lengthOk := StateOfKnown(length)
	if !lengthOk || lengthState.Top {
		return nil, false
	}
	// the count must READ as a number: a sequence whose length answered a
	// word set (a receiver the length read never grounded) relates to
	// nothing the division consumes
	if sort, pinned := SortFromKnown(length); !pinned || sort != BindingKindNumber {
		return nil, false
	}
	states := []kernelbridge.KnownStateWire{
		accumTotalSlot:   totalState,
		accumElementSlot: elementState,
		accumLenSlot:     lengthState,
		accumMeanSlot:    {Top: true},
	}
	for _, state := range states[:accumMeanSlot] {
		// an accreted entry state past the meet budget declines the route,
		// exactly as the ordinary engine entry declines it — the questions
		// it would ask are refused or worse
		if setNodes(state.Set) > MeetNodeBudget {
			return nil, false
		}
	}
	context := &LoweringContext{
		// the element slot wears the PASS BINDING's own name, so the term
		// `s * s` reads through EffectOf as `mul(var 1, var 1)` — the
		// loopAccum body reading its iteration value from slot src, with no
		// substitution step of its own
		Bindings: []string{loop.TotalName, loop.ElementName, accumLenSlotName, division.MeanName},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	term, termOk := EffectOf(context, loop.Term)
	if !termOk {
		return nil, false
	}
	// the term reads the ELEMENT and constants only. A term reading the
	// accumulator (`total += total * s`) is not a sum of per-element
	// terms at all, and one reading the count relates the total to a
	// factor the relation does not carry.
	if !accumulationTermReadsElementOnly(term) {
		return nil, false
	}
	lenVar := varEffect(accumLenSlot)
	grade := abstractdomain.MinTrustLevel(
		abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(held), abstractdomain.TrustLevelOf(sequence)),
		abstractdomain.TrustLevelOf(length),
	)
	totalVar := varEffect(accumTotalSlot)
	return &RelationalAccumulation{
		TotalName:    loop.TotalName,
		MeanName:     division.MeanName,
		DivisionNode: division.DivisionNode,
		States:       states,
		Stmts: []kernelbridge.IrStatement{
			{
				Kind:       kernelbridge.IrStatementLoopAccum,
				AccumTotal: accumTotalSlot,
				AccumSrc:   accumElementSlot,
				AccumLen:   accumLenSlot,
				AccumBody:  term,
			},
			{
				Kind:   kernelbridge.IrStatementAssign,
				Target: accumMeanSlot,
				Effect: kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectBinary,
					Op:   kernelbridge.LoopOpDiv,
					A:    &totalVar,
					B:    &lenVar,
				},
			},
		},
		Grade: grade,
	}, true
}

// accumLenSlotName is the spelled name the count slot carries. It is
// deliberately unspellable in source — the denominator resolves to the
// length read, never to a binding of this name — so no term in the loop
// body can ever read the count slot by writing its name.
const accumLenSlotName = "#accum.len"

// accumulationTermReadsElementOnly is whether one pass's term reads the
// ELEMENT slot and constants and nothing else. The relation the kernel
// carries is `total <= count * termHi` with termHi read off the element
// alone; a term reading the total or the count breaks that premise.
func accumulationTermReadsElementOnly(e kernelbridge.LoopEffect) bool {
	switch e.Kind {
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectVarState:
		return e.Index == accumElementSlot
	case kernelbridge.LoopEffectConst, kernelbridge.LoopEffectConstState:
		return true
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent,
		kernelbridge.LoopEffectSeqUnary, kernelbridge.LoopEffectSeqNum:
		return accumulationTermReadsElementOnly(*e.A)
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectConcat,
		kernelbridge.LoopEffectJoin:
		return accumulationTermReadsElementOnly(*e.A) && accumulationTermReadsElementOnly(*e.B)
	}
	// unknown, thrown, and anything else: the term is not read whole, so
	// no relation can be stated about it
	return false
}

// RelationalAccumulationAnswer is what the kernel proved: the two
// written slots' exit STATES, kept in wire form so the landings below
// meet them through MeetEngineState — the same budgeted meet the
// ordinary engine route uses, whose node cap is what keeps repeated
// meets from concatenating form lists without bound.
type RelationalAccumulationAnswer struct {
	// Total: the accumulator's exit state after the loop.
	Total kernelbridge.KnownStateWire
	// Quotient: the division's exit state, derived from the linear
	// relation the accumulation left behind.
	Quotient kernelbridge.KnownStateWire
	// Grade: the trust the whole program carries — the weakest among
	// its participants.
	Grade abstractdomain.TrustLevel
}

// AskRelationalAccumulation walks the recognized program kernel-side and
// reads the two written slots back. (nil, false) on any refusal — the
// caller then walks both statements ordinarily, which is exactly what it
// did before this route existed and never weaker.
//
// The ask is WalkRelational, never Walk. Walk sends `"certify":true`,
// which selects walkStmtsCert (boundary/exports_walk.lean) — the
// certified statement-loop walk, which drops the linear ledger entirely.
// The relation this program's "loopAccum" statement leaves behind would
// then never reach the division that consumes it, and the division would
// answer plain interval arithmetic: [0, len] rather than [0, 1], which
// is exactly the answer the ordinary per-statement route already gives.
// WalkRelational omits the field so the plain path runs walkProgramRel,
// which carries the ledger across the statement list.
func AskRelationalAccumulation(accumulation *RelationalAccumulation) (*RelationalAccumulationAnswer, bool) {
	kernel := EngineKernelHeld()
	if kernel == nil || accumulation == nil || kernel.WalkRelational == nil {
		return nil, false
	}
	exit, ok := func() (exit []kernelbridge.KnownStateWire, ok bool) {
		defer func() {
			if recover() != nil {
				exit, ok = nil, false
			}
		}()
		return kernel.WalkRelational(accumulation.States, accumulation.Stmts), true
	}()
	if !ok || len(exit) != len(accumulation.States) {
		return nil, false
	}
	return &RelationalAccumulationAnswer{
		Total:    exit[accumTotalSlot],
		Quotient: exit[accumMeanSlot],
		Grade:    accumulation.Grade,
	}, true
}

// MeetRelationalTotalInto meets the proved total into the accumulator's
// own binding. Both the declaration and the return shape land this: the
// accumulator survives both statements under its own name, so the walk's
// reading and the kernel's claim both hold and the meet keeps both.
func MeetRelationalTotalInto(env Env, accumulation *RelationalAccumulation, answer *RelationalAccumulationAnswer) {
	if accumulation == nil || answer == nil {
		return
	}
	held, heldOk := env.Get(accumulation.TotalName)
	if !heldOk {
		return
	}
	env.Set(accumulation.TotalName, abstractdomain.AtTrustLevel(
		MeetEngineState(held, answer.Total), answer.Grade))
}

// MeetRelationalQuotientInto meets the proved quotient into the name the
// DECLARATION shape bound. The return shape binds nothing and never
// calls this — its quotient rides ctx.NodeOverrides instead.
func MeetRelationalQuotientInto(env Env, accumulation *RelationalAccumulation, answer *RelationalAccumulationAnswer) {
	if accumulation == nil || answer == nil || accumulation.MeanName == "" {
		return
	}
	held, heldOk := env.Get(accumulation.MeanName)
	if !heldOk {
		// the divided name is DECLARED by the statement this program
		// consumed, so nothing holds it yet — the kernel's claim is the
		// whole of what is known about it
		held = silence.Residue()
	}
	env.Set(accumulation.MeanName, abstractdomain.AtTrustLevel(
		MeetEngineState(held, answer.Quotient), answer.Grade))
}

// RelationalQuotientOverride is the one-entry override map the return
// shape walks its statement under: the division node bound to the proved
// quotient. The caller SETS ctx.NodeOverrides to this around that one
// statement and restores what it found afterwards — flow_context.go's
// NodeOverrides carries the obligation.
//
// The pinned value is the kernel's claim MET with what the ordinary
// reading of that division would answer, not the kernel's claim alone:
// both hold on every run, so meeting them keeps whichever is tighter and
// can never be weaker than walking the node. A quotient the kernel
// refused to bound (top) therefore pins nothing at all — (nil, false) —
// and the division walks exactly as it would have.
//
// (nil, false) for the declaration shape too, which pins nothing: its
// quotient reaches the walk through the name it bound.
func RelationalQuotientOverride(
	ctx *FlowContext, env Env,
	accumulation *RelationalAccumulation, answer *RelationalAccumulationAnswer,
) (map[*ast.Node]abstractdomain.AbstractValue, bool) {
	if accumulation == nil || answer == nil || accumulation.DivisionNode == nil {
		return nil, false
	}
	if answer.Quotient.Top {
		return nil, false
	}
	walked := evaluateExpression(ctx, env, accumulation.DivisionNode)
	pinned := abstractdomain.AtTrustLevel(
		MeetEngineState(walked, answer.Quotient), answer.Grade)
	return map[*ast.Node]abstractdomain.AbstractValue{
		accumulation.DivisionNode: pinned,
	}, true
}
