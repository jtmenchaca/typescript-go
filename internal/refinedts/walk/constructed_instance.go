// from evaluation/constructed_instance.ts
//
// What `new C(args)` holds from the class's own text: each property
// initializer, overlaid with every value the constructor writes to
// `this` — all JOINED, so whichever path runs is covered. Incomplete
// either way — methods and getters are keys too.

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
	classDecl := declaration.AsClassDeclaration()
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
		if baseDeclaration != nil && ast.IsClassDeclaration(baseDeclaration) && baseDeclaration != declaration {
			for _, member := range baseDeclaration.AsClassDeclaration().Members.Nodes {
				if ast.IsPropertyDeclaration(member) {
					pd := member.AsPropertyDeclaration()
					if pd.Initializer != nil && ast.IsIdentifier(pd.Name()) {
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
			if pd.Initializer != nil && ast.IsIdentifier(pd.Name()) {
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
			}
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
	return abstractdomain.KnownObject(joinedKeys, nil, false, abstractdomain.TrustProved, false)
}
