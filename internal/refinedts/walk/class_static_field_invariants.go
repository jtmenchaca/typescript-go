// Static field invariants: what a class's OWN static field provably
// holds at every read through the class's own name — the join of the
// field's initializer with every value the class's own text writes
// into it, spelled either `ClassName.field = v` or, inside a static
// block or static method, `this.field = v` (a static block's and a
// static method's `this` names the constructor object itself, sec-
// runtime-semantics-classdefinitionevaluation and sec-static-
// semantics-early-errors for class static blocks).
//
// A static field carries no privacy fence at all — `private static`
// is erased at runtime the same way an instance `private` field is —
// so this reads only the class's OWN declaration text: every static
// property initializer and every direct write inside the class body
// (a static block's top-level statements, a static method's top-level
// statements). A write reached through anything this scan cannot
// spell (a computed key, a destructuring target, a call that might
// write it) POISONS the field the same way class_field_invariants.go
// poisons an instance field it cannot fully collect — the invariant
// then answers nothing, never a stale guess.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

var (
	staticInvariantMemoMu sync.Mutex
	staticInvariantMemo   = map[*ast.Node]map[string]abstractdomain.AbstractValue{}
)

// StaticFieldInvariantsOf is the invariants of a class's static
// fields, memoized per declaration. computeStaticFieldInvariants does
// the collection; entries live as long as the program that produced
// the declaration (the same substitution class_field_invariants.go's
// invariantMemo takes for Go's lack of a WeakMap).
func StaticFieldInvariantsOf(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	staticInvariantMemoMu.Lock()
	if held, ok := staticInvariantMemo[declaration]; ok {
		staticInvariantMemoMu.Unlock()
		return held
	}
	staticInvariantMemoMu.Unlock()
	computed := computeStaticFieldInvariants(ctx, declaration)
	staticInvariantMemoMu.Lock()
	staticInvariantMemo[declaration] = computed
	staticInvariantMemoMu.Unlock()
	return computed
}

// staticWriteTargetName: the field name a PLAIN assignment writes —
// `ClassName.field = v` or `this.field = v`, receiver naming the
// given class. ("", false) for every other shape: a compound assign,
// an element access, a nested pattern, or a receiver that names
// neither the class nor `this` — the caller's own tests handle those.
func staticWriteTargetName(className string, node *ast.Node) (name string, isPlainWrite bool) {
	if !ast.IsBinaryExpression(node) {
		return "", false
	}
	be := node.AsBinaryExpression()
	if be.OperatorToken.Kind != ast.KindEqualsToken {
		return "", false
	}
	left := be.Left
	if !ast.IsPropertyAccessExpression(left) {
		return "", false
	}
	access := left.AsPropertyAccessExpression()
	nameNode := access.Name()
	if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
		return "", false
	}
	receiver := access.Expression
	isClassName := ast.IsIdentifier(receiver) && receiver.Text() == className
	isThis := receiver.Kind == ast.KindThisKeyword
	if !isClassName && !isThis {
		return "", false
	}
	return nameNode.Text(), true
}

// scanStaticWrites walks a static block's or static method's body for
// every write that could touch a static field: a plain `ClassName.f =
// v` / `this.f = v` at any depth (not just the top statement — an `if`
// branch's write still lands), recorded into sink; anything else that
// writes THROUGH the class name or `this` (a compound assign, an
// element access, a destructuring target, `delete`) poisons instead,
// because the collection cannot place what value landed.
//
// unconditional additionally records, per name, whether EVERY write
// sunk for it sits directly in the block's own straight-line statement
// list — never crossing an `if`/`for`/`while`/`switch`/`try`/label or
// any other construct that could skip it. A write reached that way
// always runs, in the order it is written, so the caller can fold the
// sequence as LAST-WRITE-WINS instead of joining it with the
// initializer — `static total = 0; static { OverCounted.total = 200;
// }` runs the write unconditionally, so `total` is never observably 0
// after class evaluation and joining the two would wrongly keep 0
// alive. A write reached through any conditional construct clears the
// name from unconditional (once cleared, never re-set), falling back
// to the existing join-based reading, which is sound wherever the
// write might not run.
// runsAtClassEvaluation says whether the scanned body RUNS when the
// class evaluates: true only for a static BLOCK. A static method's or
// accessor's body runs when something CALLS it — maybe never — so its
// straight-line writes must JOIN the invariant, never replace it: the
// last-write-wins fold below is only sound for a write that provably
// ran after the initializer.
func scanStaticWrites(className string, node *ast.Node, sink map[string][]*ast.Node, poisoned map[string]struct{}, unconditional map[string]bool, runsAtClassEvaluation bool) {
	var visitStraightLine func(n *ast.Node)
	var visitConditional func(n *ast.Node)
	markWrite := func(n *ast.Node, straightLine bool) bool {
		if !ast.IsBinaryExpression(n) {
			return false
		}
		be := n.AsBinaryExpression()
		name, isPlain := staticWriteTargetName(className, n)
		if !isPlain {
			return false
		}
		sink[name] = append(sink[name], be.Right)
		if straightLine {
			if _, seen := unconditional[name]; !seen {
				unconditional[name] = true
			}
		} else {
			unconditional[name] = false
		}
		return true
	}
	markPoison := func(n *ast.Node) {
		if ast.IsBinaryExpression(n) {
			be := n.AsBinaryExpression()
			// a compound assign or any other binary write through the
			// class name or `this` poisons the name it can place
			if be.OperatorToken.Kind >= ast.KindFirstCompoundAssignment && be.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
				ast.IsPropertyAccessExpression(be.Left) {
				access := be.Left.AsPropertyAccessExpression()
				receiver := access.Expression
				if (ast.IsIdentifier(receiver) && receiver.Text() == className) || receiver.Kind == ast.KindThisKeyword {
					if nameNode := access.Name(); nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
						poisoned[nameNode.Text()] = struct{}{}
						unconditional[nameNode.Text()] = false
					}
				}
			}
		}
		// `ClassName.f++` / `--` / `delete ClassName.f` — the same
		// receiver test, poisoning what it can place
		if ast.IsPostfixUnaryExpression(n) || ast.IsPrefixUnaryExpression(n) || ast.IsDeleteExpression(n) {
			var target *ast.Node
			switch {
			case ast.IsPostfixUnaryExpression(n):
				target = n.AsPostfixUnaryExpression().Operand
			case ast.IsPrefixUnaryExpression(n):
				target = n.AsPrefixUnaryExpression().Operand
			case ast.IsDeleteExpression(n):
				target = n.AsDeleteExpression().Expression
			}
			if target != nil && ast.IsPropertyAccessExpression(target) {
				access := target.AsPropertyAccessExpression()
				receiver := access.Expression
				if (ast.IsIdentifier(receiver) && receiver.Text() == className) || receiver.Kind == ast.KindThisKeyword {
					if nameNode := access.Name(); nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
						poisoned[nameNode.Text()] = struct{}{}
						unconditional[nameNode.Text()] = false
					}
				}
			}
		}
	}
	// visitStraightLine walks a node every enclosing construct so far
	// has run unconditionally. Its own direct statement children stay
	// straight-line; anything that names a construct which might skip
	// its body hands off to visitConditional for that subtree.
	visitStraightLine = func(n *ast.Node) {
		if n != node && (ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n)) {
			return
		}
		if markWrite(n, true) {
			visitConditional(n.AsBinaryExpression().Right)
			return
		}
		markPoison(n)
		if ast.IsIfStatement(n) || ast.IsForStatement(n) || ast.IsForInStatement(n) || ast.IsForOfStatement(n) ||
			ast.IsWhileStatement(n) || ast.IsDoStatement(n) || ast.IsSwitchStatement(n) ||
			ast.IsTryStatement(n) || ast.IsLabeledStatement(n) || ast.IsCatchClause(n) {
			visitConditional(n)
			return
		}
		n.ForEachChild(func(child *ast.Node) bool {
			visitStraightLine(child)
			return false
		})
	}
	// visitConditional walks a subtree some enclosing construct might
	// skip: writes still sink (the existing join reading covers them),
	// but never count as unconditional, regardless of their own local
	// shape.
	visitConditional = func(n *ast.Node) {
		if n != node && (ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n)) {
			return
		}
		if markWrite(n, false) {
			visitConditional(n.AsBinaryExpression().Right)
			return
		}
		markPoison(n)
		n.ForEachChild(func(child *ast.Node) bool {
			visitConditional(child)
			return false
		})
	}
	if runsAtClassEvaluation {
		visitStraightLine(node)
		return
	}
	visitConditional(node)
}

// computeStaticFieldInvariants collects a class's static field
// invariants: each field's own literal initializer, joined with every
// value a static block or static method body writes into it through
// the class's own name or `this`. A field no writer names keeps its
// initializer alone; a field some write poisons (an unspellable write
// landed on it) answers nothing — silence over a stale guess.
func computeStaticFieldInvariants(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	if declaration == nil || !ast.IsClassLike(declaration) {
		return map[string]abstractdomain.AbstractValue{}
	}
	className := ""
	if ast.IsClassDeclaration(declaration) {
		if name := declaration.Name(); name != nil && ast.IsIdentifier(name) {
			className = name.Text()
		}
	}
	members := declaration.ClassLikeData().Members.Nodes

	var candidateOrder []string
	candidates := map[string]abstractdomain.AbstractValue{}
	// sealed: the LANGUAGE keeps outside text from touching the field —
	// a `#` name (tsc refuses every access outside the class body) or
	// the `private` modifier (tsc refuses outside writes in every file
	// it checks). A PUBLIC static is writable by any holder of the class
	// name, so its invariant survives only when the FILE's own text
	// outside the class neither writes it nor lets the class object
	// escape (the census below — the static twin of the instance side's
	// public-field seal).
	sealed := map[string]bool{}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}

	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		pd := member.AsPropertyDeclaration()
		flags := ast.GetCombinedModifierFlags(member)
		if flags&ast.ModifierFlagsStatic == 0 {
			continue
		}
		name := pd.Name()
		if name == nil || !(ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)) {
			continue
		}
		nameText := name.Text()
		var value abstractdomain.AbstractValue
		if pd.Initializer == nil {
			value = abstractdomain.Undef
		} else {
			value = evaluateExpression(&silent, NewEnv(), pd.Initializer)
		}
		if _, seen := candidates[nameText]; !seen {
			candidateOrder = append(candidateOrder, nameText)
		}
		candidates[nameText] = value
		sealed[nameText] = ast.IsPrivateIdentifier(name) || flags&ast.ModifierFlagsPrivate != 0
	}
	if len(candidates) == 0 {
		return map[string]abstractdomain.AbstractValue{}
	}
	outsideWriteNodes, outsidePoisoned, classEscapes := publicStaticOutsideCensus(className, declaration)

	sink := map[string][]*ast.Node{}
	poisoned := map[string]struct{}{}
	unconditional := map[string]bool{}
	for _, member := range members {
		var body *ast.Node
		runsAtClassEvaluation := false
		switch {
		case ast.IsClassStaticBlockDeclaration(member):
			body = member.AsClassStaticBlockDeclaration().Body
			runsAtClassEvaluation = true
		case ast.IsMethodDeclaration(member) && ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0:
			body = member.Body()
		// a static GET/SET accessor's body is class text like a static
		// method's — a setter storing its parameter into the backing
		// field is a write this collection MUST see (its value joins as
		// unknown, which is what keeps the backing field's invariant from
		// claiming the initializer alone while a setter can move it)
		case (ast.IsGetAccessorDeclaration(member) || ast.IsSetAccessorDeclaration(member)) &&
			ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0:
			body = member.Body()
		default:
			continue
		}
		if body == nil {
			continue
		}
		scanStaticWrites(className, body, sink, poisoned, unconditional, runsAtClassEvaluation)
	}

	invariants := map[string]abstractdomain.AbstractValue{}
	for _, name := range candidateOrder {
		if _, isPoisoned := poisoned[name]; isPoisoned {
			continue
		}
		// an unsealed field of a class whose object escapes, or one an
		// outside UNSPELLABLE write touches (a compound assign, `++`/
		// `--`, `delete` — publicStaticOutsideCensus's own poison, the
		// same "cannot spell what landed" rule scanStaticWrites applies
		// to in-class text), keeps NO invariant: this collection cannot
		// state what the field holds, so the claim would be stale
		// exactly when it matters.
		if !sealed[name] {
			if classEscapes {
				continue
			}
			if _, isOutsidePoisoned := outsidePoisoned[name]; isOutsidePoisoned {
				continue
			}
		}
		var result abstractdomain.AbstractValue
		if unconditional[name] && len(sink[name]) > 0 && len(outsideWriteNodes[name]) == 0 {
			// every write sunk for this name sits in the block's own
			// straight-line statement list — it always runs, in the order
			// written, so the LAST one is what a read observes after class
			// evaluation. Class evaluation runs top-to-bottom
			// (sec-runtime-semantics-classdefinitionevaluation), and the
			// initializer is itself just the first write in that order —
			// an unconditional later write in the same order replaces it
			// outright rather than joining with it. An OUTSIDE write (module
			// text elsewhere) is never provably ordered against the class's
			// own text this way — module text runs whenever its own
			// enclosing function is called, which class evaluation does not
			// order — so its presence forces the join branch below even
			// where the in-class writes alone would qualify for last-write.
			last := sink[name][len(sink[name])-1]
			result = evaluateExpression(&silent, NewEnv(), last)
		} else {
			joined := candidates[name]
			for _, writeNode := range sink[name] {
				written := evaluateExpression(&silent, NewEnv(), writeNode)
				joined = abstractdomain.JoinKnown(joined, written)
			}
			// an unsealed field's SPELLABLE outside write (a plain
			// `ClassName.field = v` in module text this file can see) joins
			// in the same way an in-class write does: module text can run
			// this write at any point some enclosing function is called, so
			// a read anywhere — including the class's OWN text — must admit
			// it too, never just the class-text value alone.
			if !sealed[name] {
				for _, writeNode := range outsideWriteNodes[name] {
					written := evaluateExpression(&silent, NewEnv(), writeNode)
					joined = abstractdomain.JoinKnown(joined, written)
				}
			}
			result = joined
		}
		// the instance-field rule, verbatim (widenInvariantCollections,
		// class_field_invariants.go): the collection reads field
		// REASSIGNMENTS, so a collection-valued static keeps its identity
		// and sheds its pinned contents — `Registry.cache.set(k, v)` in a
		// static method is a content mutation no write sink records
		result = widenInvariantCollections(result)
		if result.Kind != abstractdomain.KindUnknown {
			invariants[name] = result
		}
	}
	return invariants
}

// publicStaticOutsideCensus walks the class's SOURCE FILE outside the
// class declaration for what module text does with the class object:
// which static fields it WRITES through the class name — a PLAIN
// assignment's own right-hand side sinks into writeNodes, spellable
// the same way an in-class write is (computeStaticFieldInvariants
// joins it into the invariant rather than dropping the field
// outright); a compound assignment, `++`/`--`, or `delete` poisons the
// name instead, since the written value depends on whatever the field
// held BEFORE this write — a claim this census cannot spell, the same
// "unspellable write" rule scanStaticWrites already applies to
// in-class text. `escapes` says whether the class object is handed
// somewhere this census cannot follow (an alias, an argument, an
// export list), after which any unsealed field may be written under a
// name the file never spells.
func publicStaticOutsideCensus(className string, classDeclaration *ast.Node) (writeNodes map[string][]*ast.Node, poisoned map[string]struct{}, escapes bool) {
	writeNodes = map[string][]*ast.Node{}
	poisoned = map[string]struct{}{}
	if className == "" {
		return writeNodes, poisoned, true
	}
	file := ast.GetSourceFileOfNode(classDeclaration)
	if file == nil {
		return writeNodes, poisoned, true
	}
	fieldNameOfTarget := func(target *ast.Node) (string, bool) {
		target = Unwrapped(target)
		if target == nil || !ast.IsPropertyAccessExpression(target) {
			return "", false
		}
		access := target.AsPropertyAccessExpression()
		receiver := Unwrapped(access.Expression)
		if receiver == nil || !ast.IsIdentifier(receiver) || receiver.Text() != className {
			return "", false
		}
		if field := access.Name(); field != nil && (ast.IsIdentifier(field) || ast.IsPrivateIdentifier(field)) {
			return field.Text(), true
		}
		return "", false
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == classDeclaration {
			return false
		}
		if ast.IsBinaryExpression(node) {
			be := node.AsBinaryExpression()
			op := be.OperatorToken.Kind
			if op == ast.KindEqualsToken {
				if name, ok := fieldNameOfTarget(be.Left); ok {
					writeNodes[name] = append(writeNodes[name], be.Right)
					be.Right.ForEachChild(visit)
					visit(be.Right)
					return false
				}
			} else if op >= ast.KindFirstAssignment && op <= ast.KindLastAssignment {
				if name, ok := fieldNameOfTarget(be.Left); ok {
					poisoned[name] = struct{}{}
					be.Right.ForEachChild(visit)
					visit(be.Right)
					return false
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			if name, ok := fieldNameOfTarget(node.AsPostfixUnaryExpression().Operand); ok {
				poisoned[name] = struct{}{}
				return false
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			operator := node.AsPrefixUnaryExpression().Operator
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				if name, ok := fieldNameOfTarget(node.AsPrefixUnaryExpression().Operand); ok {
					poisoned[name] = struct{}{}
					return false
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if name, ok := fieldNameOfTarget(node.AsDeleteExpression().Expression); ok {
				poisoned[name] = struct{}{}
				return false
			}
		}
		// a READ receiver (`C.total` in value position) consumes the
		// mention; a bare mention of the name anywhere else escapes
		if ast.IsPropertyAccessExpression(node) {
			access := node.AsPropertyAccessExpression()
			receiver := Unwrapped(access.Expression)
			if receiver != nil && ast.IsIdentifier(receiver) && receiver.Text() == className {
				return false
			}
		}
		if ast.IsIdentifier(node) && node.Text() == className && !isPropertyStepName(node) {
			escapes = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	file.AsNode().ForEachChild(visit)
	return writeNodes, poisoned, escapes
}

// ReadStaticFieldAccess reads `ClassName.field` through the class's
// own static field invariants — the constructor-object twin of
// ReadThisPropertyAccess/ReadObjectKeyAccess's instance reads — and
// `ClassName.prop` where prop is a static GET accessor trivially
// fronting a backing field (static_accessor_backing.go: the flow's
// place entry first, the backing invariant second). Nil wherever the
// receiver does not name a class declaration in reach, or the member
// carries no answer (no candidate, poisoned, an untrivial accessor).
func ReadStaticFieldAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsPropertyAccessExpression(e) {
		return nil
	}
	pa := e.AsPropertyAccessExpression()
	if !ast.IsIdentifier(pa.Expression) {
		return nil
	}
	declaration := staticClassDeclarationOf(ctx, pa.Expression)
	if declaration == nil {
		return nil
	}
	nameNode := pa.Name()
	if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
		return nil
	}
	// the flow's own place entry first — a write this walk already made
	// (`C.total = 200`, WriteStaticAccessorBacking) is newer than any
	// class-text invariant
	if held, has := env.Get(pa.Expression.Text() + "." + nameNode.Text()); has {
		return &held
	}
	invariants := StaticFieldInvariantsOf(ctx, declaration)
	if invariants != nil {
		if v, ok := invariants[nameNode.Text()]; ok {
			return &v
		}
	}
	return readStaticAccessorBacking(ctx, env, declaration, pa.Expression, nameNode.Text())
}
