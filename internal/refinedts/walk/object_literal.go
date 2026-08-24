// from evaluation/object_literal.ts
//
// Object-literal evaluation: each key wears what its value expression
// means. The literal's key set is COMPLETE by construction — the
// flag survives spreads only when the spread source is complete.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// exactStringName is the ONE property name a computed key writes, where
// the key expression pins exactly one string. ToPropertyKey of a string
// is that string unchanged (sec-topropertykey), so the member lands on
// exactly this name — no other key is touched, and the literal keeps
// its completeness. Any other value (a number, a set, several strings,
// an unknown) names no one key and answers ("", false).
func exactStringName(keyValue abstractdomain.AbstractValue) (string, bool) {
	if keyValue.Kind != abstractdomain.KindValues || keyValue.KindTag != abstractdomain.PrimitiveString {
		return "", false
	}
	return stringOf(keyValue.Values), true
}

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
	// the SYMBOL slots this literal wrote, with each key's construction —
	// what the aliasing discipline below reads to decide whether a later
	// symbol member may overwrite an earlier slot at runtime
	symbolSlots := map[string]symbolKeyConstruction{}
	// a member whose KEY is not known costs the literal its completeness
	// and every key written before it — the unknown name may land on any
	// of them. The keys stay OBJECT keys, unstated where they were
	// overwritten, so no absence claim rides on the unknown remainder.
	openOnUnknownKey := func() {
		for _, name := range keyOrder {
			keys[name] = abstractdomain.Opaque
		}
		complete = false
	}
	for _, property := range lit.Properties.Nodes {
		if ast.IsPropertyAssignment(property) {
			pa := property.AsPropertyAssignment()
			var name string
			hasName := false
			if ast.IsIdentifier(pa.Name()) {
				name, hasName = pa.Name().Text(), true
			} else if ast.IsStringLiteral(pa.Name()) {
				name, hasName = pa.Name().Text(), true
			} else if ast.IsNumericLiteral(pa.Name()) {
				// ToPropertyKey converts a Number argument through ToString
				// (sec-topropertykey step 2) — `{ 0: 40 }` writes the STRING
				// key "0", not a numeric one; the literal's own source
				// spelling IS that string for every integer literal a
				// member key can be written as
				name, hasName = pa.Name().Text(), true
			}
			if !hasName {
				// a SYMBOL-keyed computed property collides with no
				// string key — every string-key claim survives it
				// untouched. Under a STABLE symbol const it also names
				// ONE slot, stored under the derived #sym: name the
				// element read derives (keyed_slot_reads.go), so the
				// value reads back.
				if ast.IsComputedPropertyName(pa.Name()) &&
					(typereading.TypeAtLocation(ctx.P.Checker, pa.Name().AsComputedPropertyName().Expression).Flags()&checker.TypeFlagsESSymbolLike) != 0 {
					keyExpression := pa.Name().AsComputedPropertyName().Expression
					if slot, construction, stable := stableSymbolSlotOf(ctx.P.Checker, keyExpression); stable {
						// an earlier symbol slot this key is not provably
						// distinct from may be the SAME runtime key
						// (Symbol.for twice) — this write would overwrite
						// it, so the earlier slot's claim drops. A slot
						// that arrived through a spread carries no
						// construction to compare, so only a FRESH key is
						// provably apart from it.
						for _, heldName := range keyOrder {
							if heldName == slot || !symbolSlotKey(heldName) {
								continue
							}
							heldConstruction, spelledHere := symbolSlots[heldName]
							if spelledHere && provablyDistinctSymbolKeys(heldConstruction, construction) {
								continue
							}
							if !spelledHere && !construction.Registry {
								continue
							}
							keys[heldName] = abstractdomain.Opaque
						}
						symbolSlots[slot] = construction
						setKey(slot, evaluateExpression(ctx, env, pa.Initializer))
						continue
					}
					evaluateExpression(ctx, env, pa.Initializer)
					continue
				}
				// a computed key whose expression evaluates to ONE exact
				// string IS that name: ToPropertyKey of an exact string
				// is the string itself (sec-topropertykey step 1 returns
				// the already-string key unchanged), so the property is
				// written exactly as if it had been spelled
				if ast.IsComputedPropertyName(pa.Name()) {
					keyValue := evaluateExpression(ctx, env, pa.Name().AsComputedPropertyName().Expression)
					if exact, ok := exactStringName(keyValue); ok && !symbolSlotKey(exact) {
						setKey(exact, evaluateExpression(ctx, env, pa.Initializer))
						continue
					}
				}
				// a computed key lands anywhere: the initializer still
				// runs, and the literal keeps its named keys with
				// completeness spent
				evaluateExpression(ctx, env, pa.Initializer)
				openOnUnknownKey()
				continue
			}
			// a SOURCE-SPELLED string key wearing the #sym: prefix would
			// collide with the symbol-slot vocabulary
			// (keyed_slot_reads.go) — the literal keeps its other keys
			// and spends completeness instead of entering it
			if symbolSlotKey(name) {
				evaluateExpression(ctx, env, pa.Initializer)
				openOnUnknownKey()
				continue
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
				openOnUnknownKey()
				continue
			}
			// a spread of exactly undefined contributes no keys at
			// all — CopyDataProperties returns before reading any
			// property when the source is undefined or null
			// (sec-copydataproperties step 1)
			if spread.Kind == abstractdomain.KindUndef {
				continue
			}
			// a NON-OBJECT source still copies own enumerable properties
			// (sec-copydataproperties calls ToObject on the source
			// first): an exact string spreads its code units under index
			// names, an exact sequence its elements — `length` is not
			// enumerable on either, so it never rides along. Every other
			// source shape (a set, a collection, a plain unknown) names
			// keys this walk cannot spell, so it costs completeness and
			// the keys written before it, never the whole literal.
			if spread.Kind != abstractdomain.KindObject {
				if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveArray {
					grade := abstractdomain.TrustLevelOf(spread)
					for i, v := range spread.Values {
						setKey(strconv.Itoa(i), abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade))
					}
					continue
				}
				// a string's index names count UTF-16 CODE UNITS, which
				// coincide with this encoding's scalar slots only below
				// the astral floor — an astral-bearing string names keys
				// the tuple cannot spell
				if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveString &&
					refinementsets.AstralFree(spread.Values) {
					grade := abstractdomain.TrustLevelOf(spread)
					for i, v := range spread.Values {
						setKey(strconv.Itoa(i), abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveString, grade))
					}
					continue
				}
				if spread.Kind == abstractdomain.KindList {
					for i, item := range spread.Items {
						setKey(strconv.Itoa(i), item)
					}
					continue
				}
				openOnUnknownKey()
				continue
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
			// a STRING-literal name spells its key exactly the way an
			// identifier does — `{ "a"() {} }` writes "a". A spelled
			// #sym: prefix collides with the symbol-slot vocabulary and
			// falls to the unknown-key rule below instead.
			if nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsStringLiteral(nameNode)) && !symbolSlotKey(nameNode.Text()) {
				name, hasName = nameNode.Text(), true
			}
		}
		if !hasName {
			// a COMPUTED-named method or accessor: an exact string name
			// is the key it writes; anything else lands anywhere, and
			// costs only completeness and the keys written before it.
			// Either way the member itself carries no tracked data.
			nameNode := property.Name()
			if nameNode != nil && ast.IsComputedPropertyName(nameNode) {
				keyExpression := nameNode.AsComputedPropertyName().Expression
				// a SYMBOL-keyed member collides with no string key —
				// every string-key claim survives it untouched, the same
				// reading the property-assignment case takes
				if (typereading.TypeAtLocation(ctx.P.Checker, keyExpression).Flags() & checker.TypeFlagsESSymbolLike) != 0 {
					continue
				}
				if exact, ok := exactStringName(evaluateExpression(ctx, env, keyExpression)); ok && !symbolSlotKey(exact) {
					setKey(exact, silence.Residue())
					continue
				}
			}
			openOnUnknownKey()
			continue
		}
		if _, ok := keys[name]; !ok {
			// a METHOD's own value, whatever it does, is always a
			// function (sec-runtime-semantics-propertydefinitionevaluation)
			// — its SORT is proved by the syntax right here, without
			// evaluating the body, so the slot is entered at
			// TrustProved rather than the shared abstractdomain.
			// HostFunction constant's TrustSpec: that constant's grade
			// answers a DIFFERENT question — an externally-sourced
			// function of unknown provenance (a host call's return, a
			// parameter's declared type) — and stamping it here would
			// wrongly cap every SIBLING key's own read through
			// KnownObject's floor-by-minimum (this object's other
			// keys are proved outright; a method row sitting beside
			// them names no uncertainty about THEM). A getter's or
			// setter's read value is not fixed by its declaration
			// alone (the getter body decides it), so those two keep
			// the plain residue; a getter returning one literal earns
			// its exact value below.
			if ast.IsMethodDeclaration(property) {
				setKey(name, abstractdomain.AbstractValue{Kind: abstractdomain.KindHostFunction})
			} else {
				setKey(name, silence.Residue())
			}
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
						} else {
							// a body past the single-literal-return shape
							// still reads through `this` — the same walk
							// the spread-source route already runs
							// (below, for a getter copied in through
							// `{...source}`), applied here to the getter
							// sitting directly in THIS literal. `this`
							// binds to the object as built so far: every
							// key a PRIOR property in this same literal
							// wrote is exactly what a runtime `this` read
							// inside the getter would see (object
							// literals build their own keys in source
							// order, sec-runtime-semantics-
							// propertydefinitionevaluation), and a key
							// this getter's own read reaches that has not
							// been written yet is the walk's own honest
							// gap, not a fact to invent.
							soFar := make([]abstractdomain.ObjectKey, len(keyOrder))
							for i, heldName := range keyOrder {
								soFar[i] = abstractdomain.ObjectKey{Name: heldName, Value: keys[heldName]}
							}
							thisValue := abstractdomain.KnownObject(soFar, nil, false, abstractdomain.TrustProved, false)
							priorThis, hadThis := env.Get("this")
							env.Set("this", thisValue)
							var sink []abstractdomain.AbstractValue
							inner := *ctx
							inner.ReturnSink = &sink
							AnalyzeStatements(&inner, env, ga.Body.AsBlock().Statements.Nodes, nil)
							if hadThis {
								env.Set("this", priorThis)
							} else {
								env.Delete("this")
							}
							if len(sink) > 0 {
								joined := sink[0]
								for _, v := range sink[1:] {
									joined = abstractdomain.JoinKnown(joined, v)
								}
								setKey(name, joined)
							}
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
