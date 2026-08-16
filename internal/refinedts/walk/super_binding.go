// Resolving what a `super` call runs — no TS twin (the TS source never
// resolved a super callee; `CalleeOfCallLike` hands back a
// SuperKeyword-rooted expression and `ContractOf` reads no symbol off
// it, so every super call answered "nothing resolved").
//
// A `super.m(…)` runs a NAMED base-class member and a derived
// constructor's `super(…)` runs the base's constructor. Both are
// declarations this walk can read, so both get a declaration here:
// the enclosing class, its extends clause, the base declaration, then
// the member or the constructor. Every step that cannot be read
// answers nil, and the callers keep the answer they gave before —
// forget what is held about `this`, read the result as opaque.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// SuperRootedCallee: whether a call-like node's callee is rooted at
// `super` — a bare `super(…)`, or a `super.m(…)` / `super[k](…)`
// chain. The root read matches unmodeled_call_result.go's, so the two
// files agree about which calls are super calls.
func SuperRootedCallee(callee *ast.Node) bool {
	return SuperCalleeRoot(callee) != nil
}

// SuperCalleeRoot is the SuperKeyword node a callee is rooted at, or
// nil. One property or element step is read through — `super.m`,
// `super[k]` — the same one step the receiver-effect rows speak for;
// a longer chain (`super.a.m`) roots at a value the base handed back,
// which no row covers, so it answers nil.
func SuperCalleeRoot(callee *ast.Node) *ast.Node {
	if callee == nil {
		return nil
	}
	if callee.Kind == ast.KindSuperKeyword {
		return callee
	}
	if ast.IsPropertyAccessExpression(callee) {
		inner := callee.AsPropertyAccessExpression().Expression
		if inner != nil && inner.Kind == ast.KindSuperKeyword {
			return inner
		}
		return nil
	}
	if ast.IsElementAccessExpression(callee) {
		inner := callee.AsElementAccessExpression().Expression
		if inner != nil && inner.Kind == ast.KindSuperKeyword {
			return inner
		}
		return nil
	}
	return nil
}

// SuperCallDeclaration is the declaration a super call RUNS, or nil.
//
// `super.m(…)` answers the base class's member named m, walking up the
// heritage chain for a member the immediate base inherits;
// `super(…)` answers the base class's constructor, and where the base
// declares none, the base's own base's constructor — the same
// walk-up-until-a-body rule constructorDeclarationOf reads for
// `new X(…)`.
//
// Nil means the walk read nothing it can stand behind: `super` outside
// a class body this route places, a class with no extends clause, an
// extends expression that resolves to no class declaration (a mixin
// call, an ambient base, a value), a computed member name, or a
// member name no class in the chain declares.
//
// INSIDE AN INLINED BASE BODY: the enclosing-class read below runs on
// the `super` node's OWN parent chain, and the inline route copies
// environments rather than trees — a body's nodes keep the parents
// their source file gave them. So a `super` written in a base method
// resolves against the BASE class's extends clause when that method is
// inlined at a derived class's `super.m(…)`, which is the class whose
// base it names. No re-resolution against the calling class can happen,
// so no body needs turning away for mentioning `super`.
func SuperCallDeclaration(ctx *FlowContext, callee *ast.Node) *ast.Node {
	root := SuperCalleeRoot(callee)
	if root == nil {
		return nil
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil
	}
	// the class whose base this `super` names: the SAME containers that
	// keep the surrounding `this` keep the surrounding `super` (an arrow
	// inherits its method's, a function expression binds neither), so
	// the enclosing-class read is shared rather than redrawn. A nested
	// class's own method sits between the `super` and the outer class,
	// so this stops at the NEAREST enclosing class body — the boundary
	// MentionsSuper draws from the other direction when it stops
	// descending at a nested class.
	enclosing := dataflowfacts.EnclosingThisClass(root)
	if enclosing == nil {
		return nil
	}
	base := BaseClassDeclarationOf(ctx, enclosing)
	if base == nil {
		return nil
	}
	if root == callee {
		// a bare `super(…)`: the base's constructor, or the first
		// constructor above it in the chain
		return inheritedConstructorDeclaration(ctx, base)
	}
	if !ast.IsPropertyAccessExpression(callee) {
		// `super[k](…)`: the member name is computed, so which base body
		// runs is not read here
		return nil
	}
	name := callee.AsPropertyAccessExpression().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	return inheritedMemberDeclaration(ctx, base, name.Text())
}

// BaseClassDeclarationOf is the class declaration a class-like node
// EXTENDS, or nil. The extends clause's first type expression resolves
// through the checker to its value declaration, and only a class-LIKE
// declaration in a non-declaration file answers — so `extends
// (class { … })` and `const B = class { … }; class D extends B` both
// resolve, while `extends Error`, `extends mixin(Base)`, and a base
// from a .d.ts answer nil.
//
// The resolution shape is constructed_instance.go's base-class read
// (its heritage scan at :104-137) and ir_summary_call.go's
// declaration-file and class-LIKE gates, asked here for the super
// route rather than duplicated behaviour.
func BaseClassDeclarationOf(ctx *FlowContext, classLike *ast.Node) *ast.Node {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	classData := classLike.ClassLikeData()
	if classData == nil || classData.HeritageClauses == nil {
		return nil
	}
	var heritage *ast.Node
	for _, clause := range classData.HeritageClauses.Nodes {
		if clause.AsHeritageClause().Token == ast.KindExtendsKeyword {
			heritage = clause
			break
		}
	}
	if heritage == nil {
		return nil
	}
	types := heritage.AsHeritageClause().Types
	if types == nil || len(types.Nodes) == 0 {
		return nil
	}
	baseExpression := types.Nodes[0].AsExpressionWithTypeArguments().Expression
	if baseExpression == nil {
		return nil
	}
	// an INLINE base — `extends (class { … })` — names its own
	// declaration outright; there is no symbol to look up
	if inline := Unwrapped(baseExpression); inline != nil && ast.IsClassLike(inline) {
		if ast.GetSourceFileOfNode(inline).IsDeclarationFile {
			return nil
		}
		return inline
	}
	symbol := symbolAt(ctx.P.Checker, baseExpression)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	base := symbol.ValueDeclaration
	if !ast.IsClassLike(base) || ast.GetSourceFileOfNode(base).IsDeclarationFile {
		return nil
	}
	if base == classLike {
		return nil
	}
	return base
}

// heritageChainLimit caps the heritage walk: a chain longer than this
// is not read further, and the caller keeps its "nothing resolved"
// answer. The visited set already stops a cycle
// (`class A extends B`, `class B extends A` through an alias); this
// stops a pathologically long chain from costing a walk per call site.
const heritageChainLimit = 32

// inheritedConstructorDeclaration is the CONSTRUCTOR a `super(…)`
// runs: the base's own constructor where it declares one with a body,
// otherwise the first such constructor above it in the heritage chain
// — a class declaring none inherits its base's. Nil where no class in
// the chain declares a constructor with a body (the implicit
// constructor runs base initializers and nothing this walk reads).
func inheritedConstructorDeclaration(ctx *FlowContext, base *ast.Node) *ast.Node {
	visited := map[*ast.Node]struct{}{}
	cursor := base
	for steps := 0; cursor != nil && steps < heritageChainLimit; steps++ {
		if _, seen := visited[cursor]; seen {
			return nil
		}
		visited[cursor] = struct{}{}
		classData := cursor.ClassLikeData()
		if classData == nil || classData.Members == nil {
			return nil
		}
		for _, member := range classData.Members.Nodes {
			if ast.IsConstructorDeclaration(member) && member.Body() != nil {
				return member
			}
		}
		cursor = BaseClassDeclarationOf(ctx, cursor)
	}
	return nil
}

// NewExpressionDispatchTarget: whether a method callee is read off a
// DIRECT `new C(…)` construction and `declaration` is the very member
// class C dispatches for that name. A value spelled `new C(…)` has C
// as its EXACT runtime class — no subclass instance can stand behind
// the expression — so the dispatch is as static as a super call's, and
// ContractOf's override gate (which guards virtual dispatch through
// receivers whose runtime class may be derived) has nothing to guard.
//
// The reading is deliberately syntactic: only a receiver that IS the
// construction (parens and casts stepped through) qualifies. A name a
// construction was bound to answers false here — the binding's class
// exactness is flow knowledge this gate does not carry.
func NewExpressionDispatchTarget(ctx *FlowContext, callee *ast.Node, declaration *ast.Node) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return false
	}
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	access := callee.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return false
	}
	name := access.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	receiver := Unwrapped(access.Expression)
	if receiver == nil || !ast.IsNewExpression(receiver) {
		return false
	}
	constructorName := Unwrapped(receiver.AsNewExpression().Expression)
	if constructorName == nil {
		return false
	}
	var classLike *ast.Node
	if ast.IsClassLike(constructorName) {
		// `new (class { … })().m()` names its class inline
		classLike = constructorName
	} else {
		if !ast.IsIdentifier(constructorName) {
			return false
		}
		symbol := symbolAt(ctx.P.Checker, constructorName)
		if symbol == nil || symbol.ValueDeclaration == nil {
			return false
		}
		classLike = symbol.ValueDeclaration
		if !ast.IsClassLike(classLike) {
			return false
		}
		// a class NAME the file reassigns may hold a different
		// constructor at runtime — the same doubt ContractOf reads for
		// a reassigned function name
		if _, reassigned := narrowing.ReassignedNames(ast.GetSourceFileOfNode(classLike))[constructorName.Text()]; reassigned {
			return false
		}
	}
	if ast.GetSourceFileOfNode(classLike).IsDeclarationFile {
		return false
	}
	// the member C's own chain dispatches for the name — the same
	// walk a super call reads, started at C itself
	return inheritedMemberDeclaration(ctx, classLike, name.Text()) == declaration
}

// inheritedMemberDeclaration is the METHOD a `super.m(…)` runs: the
// member named m on the base, or on the first class above it in the
// heritage chain that declares one with a body. A STATIC member is
// not what `super.m(…)` reaches from an instance method, so it is
// stepped over rather than answered.
func inheritedMemberDeclaration(ctx *FlowContext, base *ast.Node, memberName string) *ast.Node {
	visited := map[*ast.Node]struct{}{}
	cursor := base
	for steps := 0; cursor != nil && steps < heritageChainLimit; steps++ {
		if _, seen := visited[cursor]; seen {
			return nil
		}
		visited[cursor] = struct{}{}
		classData := cursor.ClassLikeData()
		if classData == nil || classData.Members == nil {
			return nil
		}
		for _, member := range classData.Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			if (ast.GetCombinedModifierFlags(member) & ast.ModifierFlagsStatic) != 0 {
				continue
			}
			name := member.Name()
			if name == nil || !ast.IsIdentifier(name) || name.Text() != memberName {
				continue
			}
			if member.Body() == nil {
				// an overload signature or an ambient method: nothing to
				// read a receiver row off, and a later class in the chain
				// does not declare THIS class's member
				return nil
			}
			return member
		}
		cursor = BaseClassDeclarationOf(ctx, cursor)
	}
	return nil
}

// SuperCallContract is the contract held for what a super call runs,
// or nil. The declaration the super route resolved is looked up in the
// by-declaration-node index the contract registry already builds
// (contract_lookup.go's ContractIndexOf) — `super.m(…)` names no
// symbol ContractBySymbol can follow, so the declaration node is the
// only identity available.
//
// NO OVERRIDE GATE rides here. ContractOf's gate exists because an
// ordinary method callee dispatches virtually — the receiver's runtime
// class picks the body. A `super.m(…)` lookup starts at the HOME
// object's prototype (sec-super-keyword's MakeSuperPropertyReference),
// never at the instance, so the base body the heritage walk above
// resolved is the body that runs whatever the instance's own class
// declares — an override of the name elsewhere never changes which
// body a super call runs.
func SuperCallContract(ctx *FlowContext, callee *ast.Node) *FunctionContract {
	declaration := SuperCallDeclaration(ctx, callee)
	if declaration == nil {
		return nil
	}
	held, ok := ContractIndexOf(&ctx.Contracts)[declaration]
	if !ok {
		return nil
	}
	return held
}
