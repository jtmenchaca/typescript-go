// from control_flow/loop_fixpoint.ts
//
// The loop fixpoint — havoc invalidated. Iterate the body's effect;
// widen what refuses to stabilize; the KERNEL certifies the candidate
// invariant (set_functions/invariant.lean `invariant_certifies`). A
// failed certificate falls back to unknown — never a guess. One
// checked pass; what follows sees the certified facts, with the
// refuted condition narrowing in unless a break leaves early.
//
// do-while: first pass unguarded; re-entries wear the held condition.
// for-of: the element binding wears the iterable's element set.
//
// CROSS-DIRECTORY: readDestructuring is bindings/destructuring.ts's
// ReadDestructuring — already ported (walk/destructuring.go).
// differenceConstraintsOf is dataflow_facts/difference_constraints.ts
// — blocked in dataflowfacts today (see assume_condition.go's banner).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// structuralIterableNames: the two structural supertypes an array
// ALSO satisfies (`Iterable<T>`, `AsyncIterable<T>`), each carrying
// its element as its one type argument (lib.es2015.iterable.d.ts,
// lib.es2018.asyncgenerator.d.ts). generatorDeclaredElement
// (generator_element.go) deliberately excludes these two names from
// its own set — a function DECLARED to return one may hand back a
// plain array, and reading that call as an iterator object would
// discard what the sequence routes already say. Here there is no such
// risk: this arm runs only after every sequence and iterator-object
// reading above has already answered nothing, so a bare structural
// name is read for its element the same way builtinIteratorElementOf
// (iterator_protocol_models.go) reads MapIterator<T> and kin — first
// type argument, default-library symbol only.
var structuralIterableNames = map[string]bool{
	"Iterable": true, "AsyncIterable": true,
}

// structuralIterableElementType: the *checker.Type a host type names
// as its element where the type's own symbol spells Iterable or
// AsyncIterable — nil where the type is not one of those two
// structural shapes. A user's own type named Iterable answers
// nothing: the symbol has to be the default library's, the same
// standing builtinIteratorElementOf and generatorDeclaredElement both
// rest on.
func structuralIterableElementType(c *checker.Checker, t *checker.Type) *checker.Type {
	symbol := t.Symbol()
	if symbol == nil || !structuralIterableNames[symbol.Name] {
		return nil
	}
	tracing.CountBy("host.symbolInDefaultLib", 1)
	if !c.SymbolInDefaultLib(symbol) {
		return nil
	}
	if (t.ObjectFlags() & checker.ObjectFlagsReference) == 0 {
		return nil
	}
	tracing.CountBy("host.typeArguments", 1)
	arguments := c.GetTypeArguments(t)
	if len(arguments) == 0 {
		return nil
	}
	return arguments[0]
}

// LoopAnalyzers mirrors the TS LoopAnalyzers interface — the three
// callbacks solveLoop needs from its caller (analyze_statement.go)
// so this file does not import upward into the dispatcher.
type LoopAnalyzers struct {
	AnalyzeStatement   func(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool
	EvaluateExpression func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue
	// IterationElement: what one element of a for-of is where the
	// ITERABLE expression says so and the walked value does not — a
	// web collection's iterator, an Object.entries call. Nil where
	// nothing speaks.
	IterationElement func(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue
}

// foldableConditionSteps is the condition's pure unit steps the
// fixpoint can iterate — `n--`, `n++`, `--n`, `++n` standing directly
// as a comparison operand. Each returned node is the step expression
// itself, for bodyEffect to evaluate at the top of every pass; each
// folded name is REMOVED from `written`, since the fixpoint now models
// that write and the blanket decay would throw the modeled fact away.
//
// The gate, per name:
//
//   - the step's operand is a plain identifier, and the step stands as
//     a direct operand of the condition's comparison — a step nested in
//     a call argument or behind a short-circuit runs a number of times
//     the fixpoint cannot count;
//   - the condition writes that name ONCE and nowhere else — a second
//     write is a composed effect one evaluated step does not reproduce;
//   - the BODY does not write it either. A body write and a condition
//     write compose per trip, and the body's own walk already feeds its
//     half; folding the condition's half on top would step the binding
//     through the body's write rather than beside it.
//
// Everything else keeps the decay.
func foldableConditionSteps(
	ctx *FlowContext, loop *ast.Node, condition *ast.Node, written map[string]struct{},
) []*ast.Node {
	if len(written) == 0 {
		return nil
	}
	// how many times the condition writes each name, and where the ONE
	// step-shaped write to it stands
	writeCount := map[string]int{}
	stepAt := map[string]*ast.Node{}
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		var operator ast.Kind
		var operand *ast.Node
		stepShaped := false
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			operator, operand, stepShaped = unary.Operator, unary.Operand, true
		} else if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			operator, operand, stepShaped = unary.Operator, unary.Operand, true
		}
		if stepShaped && (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
			ast.IsIdentifier(operand) {
			name := operand.Text()
			writeCount[name]++
			if comparisonOperandStep(condition, node) {
				stepAt[name] = node
			}
			return
		}
		// any OTHER write inside the condition — an assignment, a
		// compound write, a step on a place — counts against every name it
		// touches, so the single-write test below sees it
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				other := map[string]struct{}{}
				AssignedNames(ctx.P.Checker, node, other)
				for name := range other {
					writeCount[name] += 2 // never single, whatever else is found
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(condition)

	// the body's own write set — a name both halves write stays decayed
	bodyWritten := map[string]struct{}{}
	if statement := loopStatementBody(loop); statement != nil {
		AssignedNames(ctx.P.Checker, statement, bodyWritten)
		CallMediatedWrites(ctx.P.Checker, ctx.Contracts, statement, bodyWritten, nil)
	}
	if ast.IsForStatement(loop) && loop.AsForStatement().Incrementor != nil {
		AssignedNames(ctx.P.Checker, loop.AsForStatement().Incrementor, bodyWritten)
	}

	var folded []*ast.Node
	for name := range written {
		step, isStep := stepAt[name]
		if !isStep || writeCount[name] != 1 {
			continue
		}
		if _, alsoBody := bodyWritten[name]; alsoBody {
			continue
		}
		folded = append(folded, step)
		delete(written, name)
	}
	return folded
}

// comparisonOperandStep reports whether `step` stands as a direct
// operand of the condition's own comparison — `n-- > 0`, `0 < --n`.
// A step anywhere else in the condition (a call argument, either arm
// of a short-circuit) runs a number of times per trip the fixpoint
// cannot count, so it does not qualify.
func comparisonOperandStep(condition *ast.Node, step *ast.Node) bool {
	bare := condition
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if !ast.IsBinaryExpression(bare) {
		return false
	}
	bin := bare.AsBinaryExpression()
	switch bin.OperatorToken.Kind {
	case ast.KindLessThanToken, ast.KindLessThanEqualsToken,
		ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken,
		ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
	default:
		return false
	}
	side := func(e *ast.Node) *ast.Node {
		for ast.IsParenthesizedExpression(e) {
			e = e.AsParenthesizedExpression().Expression
		}
		return e
	}
	return side(bin.Left) == step || side(bin.Right) == step
}

func loopStatementBody(loop *ast.Node) *ast.Node {
	switch {
	case ast.IsForStatement(loop):
		return loop.AsForStatement().Statement
	case ast.IsWhileStatement(loop):
		return loop.AsWhileStatement().Statement
	case ast.IsDoStatement(loop):
		return loop.AsDoStatement().Statement
	case ast.IsForOfStatement(loop), ast.IsForInStatement(loop):
		return loop.AsForInOrOfStatement().Statement
	}
	return nil
}

// SolveLoop is solveLoop in the TS source: solve one loop in place —
// `env` leaves holding the after-loop facts.
//
// bodyEntry: when non-nil, the environment the BODY runs under is
// copied here: the certified invariant, the condition's narrowing,
// and the element binding. `env` itself comes out holding what
// follows the loop, which is a different state and the one a check
// wants. A hover inside the body wants this one. Filled on every
// pass, so it ends holding the checked pass's.
func SolveLoop(ctx *FlowContext, env Env, loop *ast.Node, result *annotations.DeclaredRefinement, analyzers LoopAnalyzers, bodyEntry Env) {
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}

	// a for-initializer runs once, before everything, checked
	if ast.IsForStatement(loop) && loop.AsForStatement().Initializer != nil {
		initializer := loop.AsForStatement().Initializer
		if ast.IsVariableDeclarationList(initializer) {
			for _, declaration := range initializer.AsVariableDeclarationList().Declarations.Nodes {
				decl := declaration.AsVariableDeclaration()
				if !ast.IsIdentifier(decl.Name()) {
					continue
				}
				if decl.Initializer == nil {
					env.Set(decl.Name().Text(), silence.Residue())
				} else {
					env.Set(decl.Name().Text(), analyzers.EvaluateExpression(ctx, env, decl.Initializer))
				}
			}
		} else {
			analyzers.EvaluateExpression(ctx, env, initializer)
		}
	}

	var condition *ast.Node
	switch {
	case ast.IsWhileStatement(loop):
		condition = loop.AsWhileStatement().Expression
	case ast.IsDoStatement(loop):
		condition = loop.AsDoStatement().Expression
	case ast.IsForStatement(loop):
		condition = loop.AsForStatement().Condition
	}
	// checkAssignability whatever the condition itself calls, once
	if condition != nil {
		analyzers.EvaluateExpression(ctx, env.Clone(), condition)
	}
	// a literal-bounded loop runs an exactly known number of times over
	// exactly known values: step it that many times, precisely — no
	// widening, no invariant question (loop_unroll.go)
	if UnrollLiteralBoundedLoop(ctx, env, loop, result, analyzers, bodyEntry) {
		return
	}
	// names the LOOP writes anywhere — a comparison side rooted in one
	// is not loop-invariant, and its entry window would go stale
	loopWrites := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, loop, loopWrites)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, loop, loopWrites, nil)
	invariantSide := func(e *ast.Node) bool {
		root := e
		for ast.IsPropertyAccessExpression(root) {
			root = root.AsPropertyAccessExpression().Expression
		}
		if !ast.IsIdentifier(root) {
			return false
		}
		_, written := loopWrites[root.Text()]
		return !written
	}
	// the condition's cross-place rows hold at every body ENTRY (the
	// test just passed). Their scope is the CONDITION alone — a body
	// write RETIRES the row in walk order (writeBinding and havoc call
	// AliasClasses.invalidate), and each entry revalidates it, the condition
	// having just re-passed. So `while (i < xs.length) { use(xs[i]);
	// i++ }` keeps the row through the read and drops it at the
	// increment. A do-while body runs once unguarded, so it gets none.
	var conditionConstraints []dataflowfacts.DifferenceConstraint
	if condition != nil && !ast.IsDoStatement(loop) {
		conditionConstraints = dataflowfacts.DifferenceConstraintsOf(ctx.P.Checker, condition, condition, nil, nil, nil)
	}
	registerDifferenceConstraints(ctx, conditionConstraints)

	// the FULL transfer vocabulary — narrowings with value copies,
	// inverse factors, length guards — with comparison-side windows
	// read from the loop-entry state and gated to loop-invariant
	// names (an ungated window would go stale by the second iteration).
	// A condition whose rows landed WAS read — as a relation riding
	// the body entries and the exit channel — and the coverage report is told
	// so instead of counting the guard unread.
	var transfers *ConditionEnvTransfers
	if condition != nil {
		readElsewhere := narrowing.GuardReadNowhere
		if len(conditionConstraints) > 0 {
			readElsewhere = narrowing.GuardReadRelation
		}
		t := ConditionEnvTransfersOf(ctx, env, condition, ConditionEnvTransfersSite{
			At:            loop,
			ReadElsewhere: readElsewhere,
			SideWindow: func(e *ast.Node) (narrowing.Window, bool) {
				if !invariantSide(e) {
					return narrowing.Window{}, false
				}
				return narrowing.BoundsOfKnown(analyzers.EvaluateExpression(ctx, env.Clone(), e))
			},
		})
		transfers = &t
	}
	// a do-while body runs once before the condition is ever tested
	var bodyTransfers *ConditionEnvTransfers
	if !ast.IsDoStatement(loop) {
		bodyTransfers = transfers
	}

	// a condition (or a for-of iterable) with side effects steps
	// OUTSIDE the modeled discipline: the fixpoint iterates the BODY's
	// effect, so a name the condition writes would converge to its
	// stale entry value — `while (n-- > 0)` leaving n at 3. Every such
	// name decays to unknown before the solve, inside the body and
	// after the exit — the honest alert downstream, never a stale
	// fact. (The exit narrowing may still recover the final test's
	// refutation: it describes the tested place at test time.)
	conditionWritten := map[string]struct{}{}
	if condition != nil {
		AssignedNames(ctx.P.Checker, condition, conditionWritten)
	}
	if ast.IsForOfStatement(loop) || ast.IsForInStatement(loop) {
		AssignedNames(ctx.P.Checker, loop.AsForInOrOfStatement().Expression, conditionWritten)
	}
	// the tractable subset: a condition whose ONLY write to a name is a
	// pure unit step on the comparison operand — `while (n-- > 0)`,
	// `while (--n > 0)` — is folded into the fixpoint as a stepped
	// binding, exactly as a body-side `n--` is. A body step reaches the
	// fixpoint by being WALKED inside bodyEffect (analyzeStatement
	// evaluates it, readStepUnary writes the binding); the condition's
	// step is fed the same way, by evaluating the step expression at the
	// top of bodyEffect.
	//
	// THE ORDER ARGUMENT. One trip of `while (COND) BODY` is: evaluate
	// the condition, test it, run the body. So the state the body sees
	// on trip k is the entry state stepped k times, and the per-trip
	// transfer the fixpoint must iterate is (condition step) ∘ (body) —
	// step FIRST, which is where bodyEffect evaluates it.
	//
	// Post- and pre-decrement differ only in the value the step
	// EXPRESSION yields (`n--` reads the old value, `--n` the new); both
	// leave the binding one lower. The fixpoint reads only the binding,
	// never the expression's value, so the two spellings fold
	// identically. Nor can the condition's own narrowing land on the
	// wrong side of the step: the narrowing channel reads places off
	// comparison operands through TrackedPlaceOf, which reads no place
	// out of an update expression at all — so a stepped operand carries
	// no narrowing to misalign, on either side.
	//
	// Every name with any OTHER condition write keeps the decay: two
	// writes to one name in a condition, a step buried under a call, or
	// a step on a name the body also writes, all leave the composed
	// transfer outside what one evaluated step reproduces.
	var conditionSteps []*ast.Node
	if condition != nil {
		conditionSteps = foldableConditionSteps(ctx, loop, condition, conditionWritten)
	}
	for name := range conditionWritten {
		if _, ok := env.Get(name); ok {
			env.Set(name, silence.Residue())
		}
	}

	// for-of / for-in: the element binding — one name, or a
	// destructuring pattern over the element — and what one element is
	var elementName string
	hasElementName := false
	var elementBinding *ast.Node
	var elementPattern *ast.Node
	elementKnown := silence.Residue()
	if ast.IsForOfStatement(loop) || ast.IsForInStatement(loop) {
		forInOf := loop.AsForInOrOfStatement()
		iterable := analyzers.EvaluateExpression(ctx, env.Clone(), forInOf.Expression)
		if forInOf.Initializer != nil && ast.IsVariableDeclarationList(forInOf.Initializer) {
			declarations := forInOf.Initializer.AsVariableDeclarationList().Declarations.Nodes
			if len(declarations) == 1 {
				binding := declarations[0].AsVariableDeclaration().Name()
				if ast.IsIdentifier(binding) {
					elementName, hasElementName = binding.Text(), true
					elementBinding = binding
				} else {
					elementPattern = binding
				}
			}
		}
		if ast.IsForOfStatement(loop) {
			elementKnown = ElementOf(iterable)
		}
		// a for-in binding holds a property KEY, and the language yields
		// only strings there (ECMA-262 EnumerateObjectProperties: "all
		// the String-valued keys"; Symbol keys are never returned) — so
		// the binding wears the whole string sort
		if ast.IsForInStatement(loop) {
			elementKnown = abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		}
		if ast.IsForOfStatement(loop) && elementKnown.Kind == abstractdomain.KindUnknown {
			// an iterable the walk holds no sequence for may still SAY its
			// element: a web collection's iterator, an Object.entries call
			if said := analyzers.IterationElement(ctx, env.Clone(), forInOf.Expression); said != nil {
				elementKnown = *said
			}
		}
		if ast.IsForOfStatement(loop) && elementKnown.Kind == abstractdomain.KindUnknown {
			// the sort ground, extended to elements: iterating a
			// `number[]` the walk knows nothing more about still provably
			// yields doubles — any double, or NaN.
			//
			// The reading is SEMANTIC, not a spelling: the type resolves
			// through the checker, and the element type of any array
			// reference — `number[]`, `Array<number>`, `ReadonlyArray<number>`,
			// `readonly number[]`, an alias of any of them — is asked
			// directly. GetElementTypeOfArrayType answers for exactly the
			// references the language calls arrays (the global Array and
			// ReadonlyArray targets), and a tuple, a union, or a
			// non-array iterable answers nil and falls through unread, as
			// it did before. Identity against the checker's own number
			// type is the element test: no other type is `number`.
			t := typereading.TypeAtLocation(ctx.P.Checker, forInOf.Expression)
			if t != nil {
				tracing.CountBy("host.elementTypeOfArrayType", 1)
				element := ctx.P.Checker.GetElementTypeOfArrayType(t)
				isNumberElement := false
				if element != nil {
					tracing.CountBy("host.numberType", 1)
					isNumberElement = element == ctx.P.Checker.GetNumberType()
				}
				if isNumberElement {
					// the ARRAY's own static type is a claim tsc already
					// checked (a declared/inferred `number[]`, whatever
					// declaration produced it) — the same standing
					// return_type_ground.go's typeGroundOf stamps on every
					// ground it reads from a checked position, so this
					// ground carries LIBRARY grade too. Without the stamp
					// CheckPossiblyNaN cannot tell this claim apart from
					// AfterReaders' own ungraded fallback seed (nan_wrapper.go's
					// two-case split), and declines a value this walk
					// determined for real.
					//
					// The ground itself is R-bar (refinementsets.Numbers, the
					// -infinity ray) — never the bare RefinedSet{} zero value,
					// which is the untyped root that "holds every tuple" of
					// every sort and so cannot answer a scalar kernel
					// question at all (return_type_ground.go's number arm
					// carries the identical fix and doc).
					elementKnown = abstractdomain.AtTrustLevel(
						abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)),
						abstractdomain.TrustLibrary,
					)
				}
				// the same claim, extended past arrays: a host type whose
				// own symbol spells `Iterable<T>` or `AsyncIterable<T>` —
				// the two structural shapes a `for await` stream
				// annotation states — carries T as its first type
				// argument, and T is read through the same resolved-type
				// door every other element read in this tree goes
				// through. `for await` binds the AWAITED element, and T
				// here already IS the awaited type (`AsyncIterable<number>`
				// states its element as `number`, not `Promise<number>`),
				// so the read needs no further unwrap. A generator's OWN
				// declared return type is excluded from this reading the
				// same way generatorDeclaredElement excludes it from its
				// own set — that route (IterationElement above) already
				// answered those where it could.
				if elementKnown.Kind == abstractdomain.KindUnknown {
					if structural := structuralIterableElementType(ctx.P.Checker, t); structural != nil {
						if read, ok := typereading.ReadHostType(ctx.P.Checker, structural, forInOf.Expression, 0); ok {
							// the iterable's own declared type argument is a
							// claim tsc already checked — library grade, the
							// same reasoning the array-of-number arm above
							// now carries
							elementKnown = abstractdomain.AtTrustLevel(read, abstractdomain.TrustLibrary)
						}
					}
				}
			}
		}
	}

	statement := loopStatementBody(loop)

	// read-once for the solve's silent passes: the fixpoint's repeat
	// steps and a nested loop's re-solves replay a remembered image
	// when the whole (entry, step-input) state spells identically
	entrySpell, entrySpellOK := spellEnvForMemo(env)

	bodyEffect := func(fromEnv Env, reporting *FlowContext) Env {
		body := fromEnv.Clone()
		// the condition's folded unit steps run FIRST: the condition is
		// evaluated before every body pass, so the state the body reads on
		// trip k is the entry state stepped k times. Evaluating the step
		// expression here is what makes the fixpoint's per-trip transfer
		// the real one. Silent throughout — the condition was already
		// checkAssignability'd once above, and this evaluation exists to
		// move the binding, not to report a second time.
		for _, step := range conditionSteps {
			analyzers.EvaluateExpression(&silent, body, step)
		}
		if bodyTransfers != nil {
			bodyTransfers.ApplyWhenTrue(body)
		}
		if hasElementName && elementBinding != nil {
			body.Set(elementName, silence.SeededBinding(ctx.P.Checker, elementKnown, elementBinding))
		}
		// a destructured element binds through the shared reader
		// (destructure.ts): an exact pair's items land on their names,
		// an opaque element's slots stay opaque, a rest collects what
		// remains, and a defaulted slot admits the default honestly
		if elementPattern != nil {
			ReadDestructuring(elementPattern, elementKnown, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
				body.Set(name, silence.SeededBinding(ctx.P.Checker, held, at))
			})
		}
		// before the body runs, because the walk mutates it
		if bodyEntry != nil {
			bodyEntry.Range(func(k string, _ abstractdomain.AbstractValue) bool {
				bodyEntry.Delete(k)
				return true
			})
			body.Range(func(name string, known abstractdomain.AbstractValue) bool {
				bodyEntry.Set(name, known)
				return true
			})
		}
		// a silent pass replays a remembered image; the checked pass
		// (reporting == ctx) always walks — its walk reports
		effectState := ""
		if reporting == &silent && entrySpellOK {
			effectState = loopEffectStateOf(entrySpell, fromEnv)
			if effectState != "" {
				if held, ok := rememberedLoopEffect(ctx.P, loop, effectState); ok {
					return held
				}
			}
		}
		for i := range conditionConstraints {
			conditionConstraints[i].Dead = false
		}
		// a bare `continue` re-enters the NEXT iteration carrying its
		// branch's writes — recorded here and joined into the body's
		// exit, so a push-then-continue reaches the fixpoint
		var continued []Env
		inBody := *reporting
		inBody.ContinueSink = &continued
		if len(conditionConstraints) > 0 {
			inBody.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, reporting.DifferenceConstraints...), conditionConstraints...)
		}
		analyzers.AnalyzeStatement(&inBody, body, statement, result)
		for _, snapshot := range continued {
			body.Range(func(name string, held abstractdomain.AbstractValue) bool {
				body.Set(name, abstractdomain.JoinKnown(held, envOrResidue(snapshot, name)))
				return true
			})
		}
		if ast.IsForStatement(loop) && loop.AsForStatement().Incrementor != nil {
			analyzers.EvaluateExpression(reporting, body, loop.AsForStatement().Incrementor)
		}
		if effectState != "" {
			rememberLoopEffect(ctx.P, loop, effectState, body)
		}
		return body
	}

	// a do-while RE-enters its body only through the test: every state
	// a later iteration starts from is the previous step's image WITH
	// the condition held. The first pass alone runs unguarded, and the
	// raw entry already sits in every join. So the solve's step images
	// wear the held condition, which makes the fixpoint's candidate the
	// true body window — entry ∪ (condition-held step) — instead of the
	// unbounded raw iterate. (The held comparison also proves its sides
	// real, so NaN never rides in through the narrowing.)
	stepImage := func(fromEnv Env, reporting *FlowContext) Env {
		stepped := bodyEffect(fromEnv, reporting)
		if ast.IsDoStatement(loop) && transfers != nil {
			transfers.ApplyWhenTrue(stepped)
		}
		return stepped
	}

	assigned := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, loop, assigned)
	CallMediatedWrites(ctx.P.Checker, ctx.Contracts, loop, assigned, nil)
	var touched []string
	for name := range assigned {
		if _, ok := env.Get(name); ok {
			touched = append(touched, name)
		}
	}

	// an iterable the BODY may mutate cannot vouch its entry
	// elements — later iterations see what the writes made, so the
	// element decays to unknown (any name in the iterable expression,
	// or an alias of one, that the loop writes)
	if (hasElementName || elementPattern != nil) && (ast.IsForOfStatement(loop) || ast.IsForInStatement(loop)) {
		iterableNames := map[string]struct{}{}
		var collect func(node *ast.Node)
		collect = func(node *ast.Node) {
			if ast.IsIdentifier(node) {
				iterableNames[node.Text()] = struct{}{}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				collect(child)
				return false
			})
		}
		collect(loop.AsForInOrOfStatement().Expression)
		mutated := false
		for id := range iterableNames {
			for member := range ctx.Aliases.ClassOf(id) {
				if _, isAssigned := assigned[member]; isAssigned {
					mutated = true
					break
				}
			}
			if mutated {
				break
			}
		}
		// a mutated iterable's entry ELEMENTS cannot be vouched — but a
		// for-in binding never held elements, only property keys, and a
		// key is a string however the object changes mid-iteration
		if mutated && !ast.IsForInStatement(loop) {
			// APPEND-ONLY is the one mutation the entry reading survives.
			//
			// The array iterator holds an index and reads xs[i] at the
			// moment it hands the element over, so what a body write does
			// to the element binding depends entirely on WHERE it writes:
			//
			//   - a write BELOW the cursor (xs[0] = y after slot 0 was
			//     handed over) changes nothing already read and nothing
			//     still to be read;
			//   - a write AT OR ABOVE the cursor (xs[i+1] = y, splice, pop
			//     then push) replaces a value that has not been read yet —
			//     the next read sees the write, so the entry reading is
			//     simply wrong about it;
			//   - push appends at the CURRENT length, which is always past
			//     the cursor, so it disturbs no existing slot: every
			//     original element is still read, at its original index,
			//     holding its original value.
			//
			// So under push-only writes every element the loop ever hands
			// over is either an ENTRY element (undisturbed) or a PUSHED
			// value (read on a later trip, if the loop runs that far). The
			// binding is the JOIN of the two — not the entry reading
			// alone, which would be a wrong claim about trips 2 onward,
			// and not unknown, which throws away both halves.
			//
			// A pushed argument is read at the ENTRY state, so it only
			// counts when its value cannot move per trip: it may mention
			// no name the loop writes and none the element binding
			// introduces. `xs.push(x * 2)` reads the per-trip element and
			// does not qualify; `xs.push(seed)` with seed untouched does.
			// Anything else keeps the decay.
			appended, appendOnly := appendedIterableArguments(loop, iterableNames)
			recovered := false
			if appendOnly && elementKnown.Kind != abstractdomain.KindUnknown {
				bound := map[string]struct{}{}
				if hasElementName {
					bound[elementName] = struct{}{}
				}
				if elementPattern != nil {
					ReadDestructuring(elementPattern, silence.Residue(), func(name string, _ abstractdomain.AbstractValue, _ *ast.Node) {
						bound[name] = struct{}{}
					})
				}
				joined := elementKnown
				stable := true
				for _, argument := range appended {
					if !expressionStableAcrossTrips(argument, assigned, bound) {
						stable = false
						break
					}
					pushedKnown := analyzers.EvaluateExpression(&silent, env.Clone(), argument)
					if pushedKnown.Kind == abstractdomain.KindUnknown {
						stable = false
						break
					}
					joined = abstractdomain.JoinKnown(joined, pushedKnown)
				}
				if stable && joined.Kind != abstractdomain.KindUnknown {
					elementKnown = joined
					recovered = true
				}
			}
			if !recovered {
				elementKnown = silence.Residue()
			}
		}
	}
	// every touched name — declared or not — tracks through the exact
	// join: a declared binding's stated set is a sound CEILING (every
	// write is checked against it), but it is not a substitute for the
	// exact per-trip union the fixpoint would otherwise build. Settling
	// against the crude declared range up front, before any iteration,
	// threw away precision the walk could otherwise hold — a do-while's
	// exact two-trip union {1,2} read as the whole declared {0..120}
	// instead. SettleLoopCandidate seeds a declared name at its real
	// entry value and MEETS the settled result with the declared range
	// at the end, so the ceiling still holds and nothing here needs to
	// widen for it.
	fixpointed := append([]string{}, touched...)

	// a do-while runs its body once from the RAW entry before any
	// test. Unrolling that first pass into the premise lets the kernel
	// solve the REMAINING iterations under the condition — iterations
	// two onward genuinely enter with it true — instead of widening
	// unbounded to MAX_VALUE, whose dyadic bounds send every later
	// question over the set divergent (the do-then-anything hang).
	premiseEnv := env
	if ast.IsDoStatement(loop) && len(fixpointed) > 0 {
		first := bodyEffect(env.Clone(), &silent)
		joined := env.Clone()
		for _, name := range fixpointed {
			joined.Set(name, abstractdomain.JoinKnown(envOrResidue(env, name), envOrResidue(first, name)))
		}
		premiseEnv = joined
	}

	// ── settle the candidate (exact join → kernel / widen → certify) ──
	candidate := SettleLoopCandidate(SettleLoopCandidateInput{
		Ctx: ctx, Env: env, Loop: loop, Silent: &silent, StepImage: stepImage,
		Fixpointed: fixpointed, Touched: touched, ConditionWritten: conditionWritten,
		PremiseEnv: premiseEnv, Condition: condition,
		ElementName: elementName, HasElementName: hasElementName, ElementKnown: elementKnown,
	})

	// ── one checked pass against the certified facts ──────────────────
	// `candidate` is the union of every body-ENTRY state — for a
	// while/for it already carries the guard, since bodyEffect narrows
	// every step through bodyTransfers before walking (bodyTransfers ==
	// transfers there). A do-while's candidate carries the guard only
	// on the OUTPUT of stepImage (transfers.ApplyWhenTrue there narrows
	// what LEAVES the body, landing correctly on the entry to iteration
	// 2+); iteration 1 enters raw, unconditionally, before the guard is
	// ever tested — and that holds for a DECLARED name exactly as it
	// does for any other one: SettleLoopCandidate's candidate is the
	// exact per-trip union met with the declared ceiling, which still
	// carries entry-1's raw, unguarded value inside it (the union
	// always includes the real entry) — so narrowing the WHOLE
	// candidate by the guard here can still cut entry-1's value away
	// where the guard would have refused it. So: narrow every touched
	// name by the guard, then rejoin the raw entry-1 value — the same
	// entry-1-unrolled ∪ entry-2+-guarded shape premiseEnv already
	// computes for the kernel premise, applied here to what the ONE
	// reporting walk sees.
	checkedEntry := candidate
	if ast.IsDoStatement(loop) && transfers != nil {
		checkedEntry = candidate.Clone()
		transfers.ApplyWhenTrue(checkedEntry)
		for _, name := range touched {
			checkedEntry.Set(name, abstractdomain.JoinKnown(envOrResidue(checkedEntry, name), envOrResidue(env, name)))
		}
	}
	checkedStep := bodyEffect(checkedEntry, ctx)

	// ── what follows the loop ────────────────────────────────────────
	// zero iterations leave the entry state; any number leave the
	// invariant; the refuted condition narrows unless a break escapes.
	// A do-while has NO zero-iteration exit — the body always runs at
	// least once — so its only exit states are the body's step image,
	// checkedStep, which already carries the held guard on entries 2+
	// (checkedEntry, above) joined with the raw entry-1 value where
	// that matters. Joining the RAW entry env or the wide entry-window
	// candidate in on top, as a while/for legitimately does for their
	// genuine zero-iteration case, would carry the unguarded full
	// declared range back in for a do-while and undo checkedEntry's
	// narrowing right here.
	after := NewEnv()
	env.Range(func(name string, known abstractdomain.AbstractValue) bool {
		if ast.IsDoStatement(loop) {
			after.Set(name, envOrResidue(checkedStep, name))
		} else {
			after.Set(name, abstractdomain.JoinKnown(known, envOrResidue(candidate, name)))
		}
		return true
	})
	// the PROPERTY-PATH arrays the body pushed onto — `this.items` — which
	// the fixpointed name list cannot hold. A root qualifies when the
	// environment holds it, the same reading the name path uses to decide
	// a name is the walk's to speak about; `this` is such a root whenever
	// the walk holds one.
	pushPlaces := PushedPlaceCandidates(loop, func(name string) bool {
		_, held := env.Get(name)
		return held
	})
	GrowPushedArrays(GrowPushedArraysInput{
		Ctx: ctx, Env: env, Loop: loop, Candidate: candidate, After: after,
		Fixpointed: fixpointed, PushPlaces: pushPlaces, EvaluateExpression: analyzers.EvaluateExpression,
	})
	if transfers != nil && !ContainsBreak(statement) {
		transfers.ApplyWhenFalse(after)
	}
	if condition != nil {
		NoteNegatedLoopExit(NoteNegatedLoopExitInput{
			Ctx: ctx, Loop: loop, Condition: condition,
			ConditionConstraints: conditionConstraints, After: after,
		})
	}
	env.Range(func(k string, _ abstractdomain.AbstractValue) bool {
		env.Delete(k)
		return true
	})
	after.Range(func(name string, known abstractdomain.AbstractValue) bool {
		env.Set(name, known)
		return true
	})
}

// appendedIterableArguments is what a for-of BODY pushes onto its own
// iterable, and whether pushing is the only thing it does to it.
//
// Every mention of an iterable name inside the body must be either a
// plain read or the receiver of `push`; the arguments of every such
// push come back. Anything else — an element write, another method, the
// name handed to a call, a reassignment — answers (nil, false), since a
// write at or above the iterator's cursor changes a value that has not
// been read yet and the entry reading no longer describes it.
//
// The scan covers the body ALONE. The head's own `xs` is a read of the
// iterable, not a write to it, and the for-of/for-in head is where the
// name necessarily appears.
func appendedIterableArguments(loop *ast.Node, iterableNames map[string]struct{}) ([]*ast.Node, bool) {
	body := loopStatementBody(loop)
	if body == nil {
		return nil, false
	}
	var collected []*ast.Node
	appendOnly := true
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if !appendOnly {
			return
		}
		if ast.IsIdentifier(node) {
			if _, isIterable := iterableNames[node.Text()]; !isIterable {
				return
			}
			parent := node.Parent
			if parent == nil {
				appendOnly = false
				return
			}
			// `xs.push(...)` — the one write that appends
			if ast.IsPropertyAccessExpression(parent) &&
				parent.AsPropertyAccessExpression().Expression == node {
				member := parent.AsPropertyAccessExpression().Name().Text()
				call := parent.Parent
				isCall := call != nil && ast.IsCallExpression(call) && call.AsCallExpression().Expression == parent
				if member == "push" && isCall {
					if call.AsCallExpression().Arguments != nil {
						collected = append(collected, call.AsCallExpression().Arguments.Nodes...)
					}
					return
				}
				// a plain property read (`xs.length`) says nothing about the
				// contents; any other CALL on the receiver may move them
				if isCall {
					appendOnly = false
				}
				return
			}
			// `xs[i]` as a READ is fine; as an assignment target it writes a
			// slot the cursor may not have passed
			if ast.IsElementAccessExpression(parent) &&
				parent.AsElementAccessExpression().Expression == node {
				assignment := parent.Parent
				if assignment != nil && ast.IsBinaryExpression(assignment) &&
					assignment.AsBinaryExpression().Left == parent {
					kind := assignment.AsBinaryExpression().OperatorToken.Kind
					if kind >= ast.KindFirstAssignment && kind <= ast.KindLastAssignment {
						appendOnly = false
					}
				}
				return
			}
			// the name reassigned outright, or handed to a callee that may
			// write through it
			appendOnly = false
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(body)
	if !appendOnly {
		return nil, false
	}
	return collected, true
}

// expressionStableAcrossTrips reports whether an expression evaluates
// to the same value on every trip of a loop: it names nothing the loop
// writes, nothing the element binding introduces, and calls nothing (a
// call's answer can differ per trip whatever its arguments say).
func expressionStableAcrossTrips(e *ast.Node, written map[string]struct{}, bound map[string]struct{}) bool {
	stable := true
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if !stable {
			return
		}
		if ast.IsCallExpression(node) || ast.IsNewExpression(node) || ast.IsTaggedTemplateExpression(node) {
			stable = false
			return
		}
		if ast.IsIdentifier(node) {
			name := node.Text()
			if _, isWritten := written[name]; isWritten {
				stable = false
				return
			}
			if _, isBound := bound[name]; isBound {
				stable = false
			}
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(e)
	return stable
}
