// from evaluation/constructed_instance.ts
//
// What `new C(args)` holds from the class's own text: each property
// initializer, overlaid with every value the constructor writes to
// `this` — all JOINED, so whichever path runs is covered, plus every
// non-static GET accessor's own return, run once against the fields
// and constructor writes already collected. Still incomplete — a
// method is a key too, and a getter chained off another getter's key
// is not threaded through this pass.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// ErrorConstructors is the Error constructors sharing one shape: the
// message is the FIRST argument (AggregateError's is second, so it
// stays out).
var ErrorConstructors = map[string]bool{
	"Error":          true,
	"TypeError":      true,
	"RangeError":     true,
	"SyntaxError":    true,
	"EvalError":      true,
	"ReferenceError": true,
	"URIError":       true,
}

// constructing: the classes currently CONSTRUCTING under this walk: a
// field initializer or constructor body that constructs the same
// class again (`x = new Self()`, mutual A↔B) is a cycle — the
// revisit answers the bare instance instead of recursing forever
// (prisma's first mutually-recursive constructor pair took a worker
// down).
//
// The TS source's Set<ts.ClassDeclaration> is one walk's own recursion
// guard — PER-CHECK working state, not a fact about the program that
// outlives it. Under goroutine-per-entry parallelism, two checks can
// walk overlapping code from different entries at once, so one shared
// map would let one check's in-progress class silence another's
// legitimate (non-recursive) construction. constructingSets holds one
// guard set per check, keyed on ctx.P — the per-check view pointer
// every FlowContext in a check shares (see PORT.md's parallel-sweep
// audit) — guarded by constructingMu the way call_site_snapshots.go's
// snapshotStores guards its own per-program map.
var (
	constructingMu   sync.Mutex
	constructingSets = map[*program.CheckerProgram]map[*ast.Node]bool{}
)

// ConstructedInstance evaluates what `new declaration(args)` holds.
// The declaration is any class-LIKE node — a class declaration or the
// class expression a `const C = class { … }` binds.
func ConstructedInstance(ctx *FlowContext, declaration *ast.Node, argKnowns []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	bare := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false)
	constructingMu.Lock()
	set, ok := constructingSets[ctx.P]
	if !ok {
		set = map[*ast.Node]bool{}
		constructingSets[ctx.P] = set
	}
	already := set[declaration]
	if !already {
		set[declaration] = true
	}
	constructingMu.Unlock()
	if already {
		return bare
	}
	defer func() {
		constructingMu.Lock()
		delete(constructingSets[ctx.P], declaration)
		constructingMu.Unlock()
	}()
	return constructedInstanceInner(ctx, declaration, argKnowns, bare)
}

func constructedInstanceInner(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	bare abstractdomain.AbstractValue,
) abstractdomain.AbstractValue {
	// class-LIKE data: a declaration and a `const C = class { … }`
	// expression carry the same heritage clauses and member list, so
	// both read through this one accessor
	classDecl := declaration.ClassLikeData()
	var keyOrder []string
	candidates := map[string][]abstractdomain.AbstractValue{}
	addCandidate := func(name string, v abstractdomain.AbstractValue) {
		if _, ok := candidates[name]; !ok {
			keyOrder = append(keyOrder, name)
		}
		candidates[name] = append(candidates[name], v)
	}

	// heritage: a user base in reach initialStates its property
	// initializers under the subclass's own; an AMBIENT base (extends
	// Error) adds keys the walk cannot read, which the incomplete
	// flag already says; anything else stays the bare object
	var heritage *ast.Node
	if classDecl.HeritageClauses != nil {
		for _, clause := range classDecl.HeritageClauses.Nodes {
			if clause.AsHeritageClause().Token == ast.KindExtendsKeyword {
				heritage = clause
				break
			}
		}
	}
	if heritage != nil {
		types := heritage.AsHeritageClause().Types
		var baseExpression *ast.Node
		if types != nil && len(types.Nodes) > 0 {
			baseExpression = types.Nodes[0].AsExpressionWithTypeArguments().Expression
		}
		var symbol *ast.Symbol
		if baseExpression != nil {
			symbol = ctx.P.Checker.GetSymbolAtLocation(baseExpression)
		}
		var baseDeclaration *ast.Node
		ambientBase := false
		if symbol != nil {
			baseDeclaration = symbol.ValueDeclaration
			if len(symbol.Declarations) > 0 {
				ambientBase = true
				for _, d := range symbol.Declarations {
					if !ast.GetSourceFileOfNode(d).IsDeclarationFile {
						ambientBase = false
						break
					}
				}
			}
		}
		if baseDeclaration != nil && ast.IsClassLike(baseDeclaration) && baseDeclaration != declaration {
			for _, member := range baseDeclaration.ClassLikeData().Members.Nodes {
				if ast.IsPropertyDeclaration(member) {
					pd := member.AsPropertyDeclaration()
					// a `#name` field is a key like any other — the private
					// brand narrows who may SPELL the access, not what the
					// instance holds
					if pd.Initializer != nil && (ast.IsIdentifier(pd.Name()) || ast.IsPrivateIdentifier(pd.Name())) {
						silent := *ctx
						silent.Report = func(assignability.RefinementDiagnostic) {}
						addCandidate(pd.Name().Text(), evaluateExpression(&silent, NewEnv(), pd.Initializer))
					}
				}
			}
		} else if !ambientBase {
			return bare
		}
	}
	for _, member := range classDecl.Members.Nodes {
		if ast.IsPropertyDeclaration(member) {
			pd := member.AsPropertyDeclaration()
			if pd.Initializer != nil && (ast.IsIdentifier(pd.Name()) || ast.IsPrivateIdentifier(pd.Name())) {
				silent := *ctx
				silent.Report = func(assignability.RefinementDiagnostic) {}
				addCandidate(pd.Name().Text(), evaluateExpression(&silent, NewEnv(), pd.Initializer))
			}
		}
	}
	var constructorDeclaration *ast.Node
	for _, member := range classDecl.Members.Nodes {
		if ast.IsConstructorDeclaration(member) {
			constructorDeclaration = member
			break
		}
	}
	var body *ast.Node
	if constructorDeclaration != nil {
		body = constructorDeclaration.AsConstructorDeclaration().Body
	}
	if body != nil {
		// `this` escaping beyond its own key writes voids the key
		// claims
		escapes := false
		var scan func(node *ast.Node)
		scan = func(node *ast.Node) {
			if escapes {
				return
			}
			if node.Kind == ast.KindThisKeyword {
				parent := node.Parent
				plainAccess := parent != nil && ast.IsPropertyAccessExpression(parent) &&
					parent.AsPropertyAccessExpression().Expression == node &&
					!(parent.Parent != nil && ast.IsCallExpression(parent.Parent) &&
						parent.Parent.AsCallExpression().Expression == parent)
				if !plainAccess {
					escapes = true
				}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				scan(child)
				return escapes
			})
		}
		scan(body)
		if escapes {
			return bare
		}
		callEnv := NewEnv()
		if constructorDeclaration != nil {
			for i, parameter := range constructorDeclaration.AsConstructorDeclaration().Parameters.Nodes {
				pd := parameter.AsParameterDeclaration()
				if !ast.IsIdentifier(pd.Name()) {
					continue
				}
				if i < len(argKnowns) {
					callEnv.Set(pd.Name().Text(), argKnowns[i])
				} else {
					callEnv.Set(pd.Name().Text(), abstractdomain.Undef)
				}
				// a PARAMETER PROPERTY (`constructor(readonly age: number)`)
				// declares a field and fills it with the argument before the
				// body's first statement — the runtime's own prelude. The
				// argument joins the candidates the way an initializer does;
				// a body write to the same key joins beside it, so whichever
				// ran last is covered. A missing argument lands the default
				// where one is written, undefined otherwise.
				if !isParameterPropertyDeclaration(parameter) {
					continue
				}
				if i < len(argKnowns) && argKnowns[i].Kind != abstractdomain.KindUndef {
					addCandidate(pd.Name().Text(), argKnowns[i])
					// an argument that MAY be undefined takes the default at
					// runtime; the default joins so both outcomes are covered
					if argKnowns[i].Kind != abstractdomain.KindPossiblyUndefined || pd.Initializer == nil {
						continue
					}
				}
				if pd.Initializer != nil {
					silent := *ctx
					silent.Report = func(assignability.RefinementDiagnostic) {}
					addCandidate(pd.Name().Text(), evaluateExpression(&silent, NewEnv(), pd.Initializer))
				} else if i >= len(argKnowns) || argKnowns[i].Kind == abstractdomain.KindUndef {
					addCandidate(pd.Name().Text(), abstractdomain.Undef)
				}
			}
		}
		// `super(...)` in the body: EvaluateCallExpression's ordinary
		// contract resolution never finds one for it — a constructor
		// declaration never registers as a FunctionContract at all
		// (contract_file_facts.go's collector registers function
		// declarations, methods, and arrow/function-expression
		// properties, never a ConstructorDeclaration) — so the base
		// constructor's own `this.x = ...` writes never inline through
		// the ordinary call-expression walk below. Run the base's own
		// constructor body here instead, its parameters bound from the
		// super call's evaluated arguments (read against THIS callEnv,
		// so `super(age)` reads the derived parameter `age` already
		// bound above), folded into the same candidates this class's own
		// writes join. Recurses up the chain on its own, so a base whose
		// constructor itself calls `super(...)` is covered too.
		if superCall := findSuperCall(body); superCall != nil {
			superConstructorFieldCandidates(ctx, declaration, superCall, callEnv, addCandidate)
		}
		sink := map[string][]abstractdomain.AbstractValue{}
		silent := *ctx
		silent.Report = func(assignability.RefinementDiagnostic) {}
		silent.ReturnSink = nil
		silent.ThisWriteSink = sink
		AnalyzeStatements(&silent, callEnv, body.AsBlock().Statements.Nodes, nil)
		for key, writes := range sink {
			if _, ok := candidates[key]; !ok {
				keyOrder = append(keyOrder, key)
			}
			candidates[key] = append(candidates[key], writes...)
		}
	}
	var joinedKeys []abstractdomain.ObjectKey
	for _, key := range keyOrder {
		values := candidates[key]
		if len(values) == 0 {
			continue
		}
		joined := values[0]
		for _, v := range values[1:] {
			joined = abstractdomain.JoinKnown(joined, v)
		}
		joinedKeys = append(joinedKeys, abstractdomain.ObjectKey{Name: key, Value: joined})
	}
	// a GET ACCESSOR is a key too — the header comment above says the
	// gap outright. A non-static getter with a plain or private-`#name`
	// identifier and a body runs its own statements once, `this` bound
	// to the object the fields and constructor already built, its
	// return collected through ReturnSink exactly as object_literal.go
	// runs a shorthand-object getter. A getter that reads another
	// getter-backed key sees this SAME pass's own candidates only for
	// FIELDS and constructor writes — getter-to-getter chains are not
	// threaded here, so a getter reading another getter's key falls to
	// that key's own absence (unknown), same as any field this walk
	// never determined.
	if len(classDecl.Members.Nodes) > 0 {
		soFar := abstractdomain.KnownObject(joinedKeys, nil, false, abstractdomain.TrustProved, false)
		for _, member := range classDecl.Members.Nodes {
			if !ast.IsGetAccessorDeclaration(member) {
				continue
			}
			ga := member.AsGetAccessorDeclaration()
			flags := ast.GetCombinedModifierFlags(member)
			if flags&ast.ModifierFlagsStatic != 0 {
				continue
			}
			name := ga.Name()
			if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
				continue
			}
			if ga.Body == nil {
				continue
			}
			nameText := name.Text()
			getterEnv := NewEnv()
			getterEnv.Set("this", soFar)
			var sink []abstractdomain.AbstractValue
			silent := *ctx
			silent.Report = func(assignability.RefinementDiagnostic) {}
			silent.ReturnSink = &sink
			silent.ThisWriteSink = nil
			AnalyzeStatements(&silent, getterEnv, ga.Body.AsBlock().Statements.Nodes, nil)
			if len(sink) == 0 {
				continue
			}
			joined := sink[0]
			for _, v := range sink[1:] {
				joined = abstractdomain.JoinKnown(joined, v)
			}
			if _, already := candidates[nameText]; !already {
				keyOrder = append(keyOrder, nameText)
				joinedKeys = append(joinedKeys, abstractdomain.ObjectKey{Name: nameText, Value: joined})
			}
			candidates[nameText] = append(candidates[nameText], joined)
		}
	}
	return abstractdomain.KnownObject(joinedKeys, nil, false, abstractdomain.TrustProved, false)
}

// findSuperCall is the first bare `super(...)` call inside a
// constructor body — a top-level statement in valid TypeScript (a
// derived constructor must run it before any `this`/`super` use), so
// one scan finds it. Nested inside a nested function expression is not
// a real super call for THIS constructor (its own `super` would bind
// to whatever class contains that nested function, if any), so the
// scan does not descend into one.
func findSuperCall(body *ast.Node) *ast.Node {
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsFunctionLike(node) {
			return true
		}
		if ast.IsCallExpression(node) && node.AsCallExpression().Expression.Kind == ast.KindSuperKeyword {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found
}

// superConstructorFieldCandidates runs the BASE class's own constructor
// body for its `this.x = ...` writes and parameter-property fills,
// folding every one into addCandidate — the same treatment
// constructedInstanceInner already gives the DERIVED constructor's own
// body, extended up the heritage chain. `callerEnv` is the calling
// constructor's own environment (its parameters already bound), which
// is what the super call's argument expressions read against —
// `super(age)` names the derived constructor's OWN parameter `age`.
//
// Recurses on its own: where the base constructor's body itself calls
// `super(...)`, that call is found and walked the same way, so a
// three-level chain (`GrandchildCtor -> ChildCtor -> BaseCtor`) folds
// every level's writes into the one candidate set the outermost
// `new` expression reads.
func superConstructorFieldCandidates(
	ctx *FlowContext,
	derivedDeclaration *ast.Node,
	superCall *ast.Node,
	callerEnv Env,
	addCandidate func(name string, v abstractdomain.AbstractValue),
) {
	base := BaseClassDeclarationOf(ctx, derivedDeclaration)
	if base == nil {
		return
	}
	baseClassDecl := base.ClassLikeData()
	if baseClassDecl == nil {
		return
	}
	var baseConstructor *ast.Node
	for _, member := range baseClassDecl.Members.Nodes {
		if ast.IsConstructorDeclaration(member) && member.Body() != nil {
			baseConstructor = member
			break
		}
	}
	if baseConstructor == nil {
		// no constructor of its own: the base's FIELD initializers are
		// already read by constructedInstanceInner's heritage block, and
		// there is no constructor body to walk further up for — an
		// implicit constructor runs nothing this reading can see
		return
	}
	// the super call's own arguments, evaluated against the CALLING
	// constructor's environment (silently — the derived body's own walk
	// reports these same argument expressions again when it reaches the
	// statement, and a second diagnostic for one expression is a false
	// duplicate)
	call := superCall.AsCallExpression()
	var argKnowns []abstractdomain.AbstractValue
	if call.Arguments != nil {
		argKnowns = make([]abstractdomain.AbstractValue, len(call.Arguments.Nodes))
		silentArgs := *ctx
		silentArgs.Report = func(assignability.RefinementDiagnostic) {}
		for i, argument := range call.Arguments.Nodes {
			argKnowns[i] = evaluateExpression(&silentArgs, callerEnv, argument)
		}
	}
	baseEnv := NewEnv()
	for i, parameter := range baseConstructor.AsConstructorDeclaration().Parameters.Nodes {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) {
			continue
		}
		if i < len(argKnowns) {
			baseEnv.Set(pd.Name().Text(), argKnowns[i])
		} else {
			baseEnv.Set(pd.Name().Text(), abstractdomain.Undef)
		}
		// a parameter property on the BASE's own constructor — the same
		// prelude constructedInstanceInner's own parameter loop runs for
		// the derived constructor
		if !isParameterPropertyDeclaration(parameter) {
			continue
		}
		if i < len(argKnowns) && argKnowns[i].Kind != abstractdomain.KindUndef {
			addCandidate(pd.Name().Text(), argKnowns[i])
			if argKnowns[i].Kind != abstractdomain.KindPossiblyUndefined || pd.Initializer == nil {
				continue
			}
		}
		if pd.Initializer != nil {
			silent := *ctx
			silent.Report = func(assignability.RefinementDiagnostic) {}
			addCandidate(pd.Name().Text(), evaluateExpression(&silent, NewEnv(), pd.Initializer))
		} else if i >= len(argKnowns) || argKnowns[i].Kind == abstractdomain.KindUndef {
			addCandidate(pd.Name().Text(), abstractdomain.Undef)
		}
	}
	baseBody := baseConstructor.AsConstructorDeclaration().Body
	// the base's OWN super call, if it extends further — walked before
	// this level's body so an ancestor's writes join in the same order
	// constructedInstanceInner's heritage-then-own-body order keeps
	if nested := findSuperCall(baseBody); nested != nil {
		superConstructorFieldCandidates(ctx, base, nested, baseEnv, addCandidate)
	}
	sink := map[string][]abstractdomain.AbstractValue{}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	silent.ReturnSink = nil
	silent.ThisWriteSink = sink
	AnalyzeStatements(&silent, baseEnv, baseBody.AsBlock().Statements.Nodes, nil)
	for key, writes := range sink {
		joined := writes[0]
		for _, v := range writes[1:] {
			joined = abstractdomain.JoinKnown(joined, v)
		}
		addCandidate(key, joined)
	}
}
