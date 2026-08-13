// from evaluation/object_literal.ts
//
// Object-literal evaluation: each key wears what its value expression
// means. The literal's key set is COMPLETE by construction — the
// flag survives spreads only when the spread source is complete.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// EvaluateObjectLiteral evaluates an object literal expression.
func EvaluateObjectLiteral(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	lit := e.AsObjectLiteralExpression()
	var keyOrder []string
	keys := map[string]abstractdomain.AbstractValue{}
	setKey := func(name string, v abstractdomain.AbstractValue) {
		if _, ok := keys[name]; !ok {
			keyOrder = append(keyOrder, name)
		}
		keys[name] = v
	}
	complete := true
	for _, property := range lit.Properties.Nodes {
		if ast.IsPropertyAssignment(property) {
			pa := property.AsPropertyAssignment()
			var name string
			hasName := false
			if ast.IsIdentifier(pa.Name()) {
				name, hasName = pa.Name().Text(), true
			} else if ast.IsStringLiteral(pa.Name()) {
				name, hasName = pa.Name().Text(), true
			}
			if !hasName {
				// a SYMBOL-keyed computed property collides with no
				// string key — every string-key claim survives it
				// untouched
				if ast.IsComputedPropertyName(pa.Name()) &&
					(ctx.P.Checker.GetTypeAtLocation(pa.Name().AsComputedPropertyName().Expression).Flags()&checker.TypeFlagsESSymbolLike) != 0 {
					evaluateExpression(ctx, env, pa.Initializer)
					continue
				}
				return silence.Residue() // a computed key lands anywhere
			}
			setKey(name, evaluateExpression(ctx, env, pa.Initializer))
			continue
		}
		if ast.IsShorthandPropertyAssignment(property) {
			spa := property.AsShorthandPropertyAssignment()
			if ast.IsIdentifier(spa.Name()) {
				name := spa.Name().Text()
				if held, ok := env.Get(name); ok {
					setKey(name, held)
				} else {
					setKey(name, silence.Residue())
				}
				continue
			}
		}
		// a spread copies the source's keys in place — later
		// properties override; an unreadable source leaves the whole
		// literal open
		if ast.IsSpreadAssignment(property) {
			sa := property.AsSpreadAssignment()
			spread := evaluateExpression(ctx, env, sa.Expression)
			// a spread of an out-of-scope CONST object resolves
			// through its initializer — the same pin the getter walk
			// below uses: a const literal never moves, so its keys
			// are its keys
			if spread.Kind == abstractdomain.KindUnknown && ast.IsIdentifier(sa.Expression) {
				symbol := ctx.P.Checker.GetSymbolAtLocation(sa.Expression)
				var declaration *ast.Node
				if symbol != nil {
					declaration = symbol.ValueDeclaration
				}
				if declaration != nil && ast.IsVariableDeclaration(declaration) {
					vd := declaration.AsVariableDeclaration()
					if vd.Initializer != nil && ast.IsObjectLiteralExpression(vd.Initializer) &&
						declaration.Parent != nil && ast.IsVariableDeclarationList(declaration.Parent) &&
						(declaration.Parent.Flags&ast.NodeFlagsConst) != 0 {
						spread = evaluateExpression(ctx, env, vd.Initializer)
					}
				}
			}
			// an OPAQUE spread may override every key written so far,
			// so those become opaque with it — but the literal built
			// here is still an OBJECT, its remaining keys unstated
			if spread.Kind == abstractdomain.KindUnknown && spread.Opaque {
				for _, name := range keyOrder {
					keys[name] = abstractdomain.Opaque
				}
				complete = false
				continue
			}
			// a spread of exactly undefined contributes no keys at
			// all — CopyDataProperties returns before reading any
			// property when the source is undefined or null
			// (sec-copydataproperties step 1)
			if spread.Kind == abstractdomain.KindUndef {
				continue
			}
			if spread.Kind != abstractdomain.KindObject {
				return silence.Residue()
			}
			if !spread.Complete {
				complete = false
			}
			for _, sk := range spread.Keys {
				// an OPTIONAL source key copies only when PRESENT
				// (CopyDataProperties walks own enumerable keys):
				// absent keeps what an earlier property wrote, so the
				// merged key is the JOIN of both worlds — the absent
				// arm resting on tsc's own optional-spread reading of
				// the result type
				earlier, hasEarlier := keys[sk.Name]
				if sk.Value.Kind == abstractdomain.KindPossiblyUndefined && hasEarlier {
					setKey(sk.Name, abstractdomain.JoinKnown(earlier, *sk.Value.Inner))
				} else {
					setKey(sk.Name, sk.Value)
				}
			}
			// a spread MATERIALIZES accessor keys: each getter runs
			// once, right here — the source literal's getter bodies
			// walk on this environment, and their returns are the
			// stored values
			var source *ast.Node = sa.Expression
			if ast.IsIdentifier(source) {
				symbol := ctx.P.Checker.GetSymbolAtLocation(source)
				var declaration *ast.Node
				if symbol != nil {
					declaration = symbol.ValueDeclaration
				}
				source = nil
				if declaration != nil && ast.IsVariableDeclaration(declaration) {
					vd := declaration.AsVariableDeclaration()
					if vd.Initializer != nil &&
						declaration.Parent != nil && ast.IsVariableDeclarationList(declaration.Parent) &&
						(declaration.Parent.Flags&ast.NodeFlagsConst) != 0 {
						source = vd.Initializer
					}
				}
			}
			if source != nil && ast.IsObjectLiteralExpression(source) {
				for _, member := range source.AsObjectLiteralExpression().Properties.Nodes {
					if ast.IsGetAccessorDeclaration(member) {
						ga := member.AsGetAccessorDeclaration()
						if ga.Body != nil && ast.IsIdentifier(ga.Name()) {
							var sink []abstractdomain.AbstractValue
							inner := *ctx
							inner.ReturnSink = &sink
							AnalyzeStatements(&inner, env, ga.Body.AsBlock().Statements.Nodes, nil)
							if len(sink) > 0 {
								joined := sink[0]
								for _, v := range sink[1:] {
									joined = abstractdomain.JoinKnown(joined, v)
								}
								setKey(ga.Name().Text(), joined)
							} else {
								setKey(ga.Name().Text(), silence.Residue())
							}
						}
					}
				}
			}
			continue
		}
		// an accessor or method is PRESENT but not tracked data — its
		// key carries nothing, except a getter that returns one
		// literal
		var name string
		hasName := false
		if ast.IsGetAccessorDeclaration(property) || ast.IsSetAccessorDeclaration(property) || ast.IsMethodDeclaration(property) {
			nameNode := property.Name()
			if nameNode != nil && ast.IsIdentifier(nameNode) {
				name, hasName = nameNode.Text(), true
			}
		}
		if !hasName {
			return silence.Residue()
		}
		if _, ok := keys[name]; !ok {
			setKey(name, silence.Residue())
		}
		if ast.IsGetAccessorDeclaration(property) {
			ga := property.AsGetAccessorDeclaration()
			if ga.Body != nil {
				statements := ga.Body.AsBlock().Statements.Nodes
				if len(statements) == 1 {
					only := statements[0]
					if ast.IsReturnStatement(only) {
						rs := only.AsReturnStatement()
						if rs.Expression != nil && ast.IsNumericLiteral(rs.Expression) {
							n := float64(jsnum.FromString(rs.Expression.Text()))
							setKey(name, abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
						}
					}
				}
			}
		}
	}
	out := make([]abstractdomain.ObjectKey, len(keyOrder))
	for i, name := range keyOrder {
		out[i] = abstractdomain.ObjectKey{Name: name, Value: keys[name]}
	}
	return abstractdomain.KnownObject(out, nil, complete, abstractdomain.TrustProved, false)
}
