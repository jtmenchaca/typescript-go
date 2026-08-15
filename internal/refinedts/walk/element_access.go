// from evaluation/element_access.ts
//
// Element reads: opaque cast roots, call-result indexing, declared
// object-array elements, optional-chain threading, object string
// keys, and list/value slots. In-bounds proofs live beside this
// file in element_in_bounds.ts.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// OpenMapAt is whether the type at a node is an open map — a
// `Record<K, V>` or anything with an index signature, `{[k: string]: V}`.
// Such a type names no fixed key set: any key may be present at runtime and
// any key may be missing, and the checker never witnessed which.
//
// One rule, three readers. It is the SOURCE test at the inline parameter
// binding (entry_env.go's entryStateMeet, reached from
// inline_contract_body.go and inliner.go), where a parameter node is the
// node handed in; and the read-side gate under a missing-key read here,
// under the dotted read (object_key_access.go), and under the `in`
// operator's absence half (binary_comparison.go), where a receiver
// expression is the node handed in.
//
// What it gates is the Complete arm of a missing-key read. Completeness is
// only ever earned by CONSTRUCTION — an object literal the walk itself built
// (object_literal.go), keys it enumerated (Object.fromEntries of a readable
// iterable, JSON.parse of a known document). It is never read off a type:
// every typereading path passes complete=false (recipes.go:115,
// host_type.go:253, type_node.go:135), so an open-map TYPE cannot mint the
// flag on its own.
//
// The flag arrives on an open-map value through a parameter binding: an
// inlined callee binds each parameter to the CALLER's argument value, so a
// caller passing an object literal hands the callee a Complete=true object
// even where the parameter is declared `Record<K, V>` — the callee's body
// then reads one call site's key set as if it were every call's. On recharts
// that made `errorBars[item.id]` answer an exact Undef, `?.filter` yield
// undefined, the callee's guards decide, and `errorDomain.length >= 2` fire
// as provably false at axisSelectors.ts:871 against a live ErrorBar path.
//
// The honest reading is that completeness proved at ONE call site is not
// completeness of the parameter, so a missing key on an open-map receiver
// answers the residue — not-known — rather than a definite absence.
func OpenMapAt(c *checker.Checker, at *ast.Node) bool {
	if at == nil {
		return false
	}
	atType := c.GetTypeAtLocation(at)
	if atType == nil {
		return false
	}
	return len(c.GetIndexInfosOfType(atType)) > 0
}

// openMapReceiver reads OpenMapAt for a receiver expression. The parameter
// binding now strips the completeness this once had to catch downstream, so
// the read-side gates are defense in depth: an evaluation-path object can
// still carry one-site completeness through routes that never cross a
// parameter binding (a literal assigned to a local whose declared type is a
// Record, a field seeded from an object literal), and the read is where that
// value meets its open-map type.
func openMapReceiver(ctx *FlowContext, receiver *ast.Node) bool {
	return OpenMapAt(ctx.P.Checker, receiver)
}

// ElementAccessOf is elementAccessOf in the TS source.
func ElementAccessOf(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	// `this[INSTANCE_ID_SYMBOL]` — a field spelled with a STABLE SYMBOL
	// const rather than a dot. The key names one field, so the read is
	// the dotted read's: the class's own invariant for that field, or the
	// opaque floor. It sits first because every reading below is about a
	// key that names a POSITION in a sequence or a string key in a map,
	// and a symbol key is neither.
	if symbolKeyed := ReadThisSymbolKeyedAccess(ctx, env, e); symbolKeyed != nil {
		return symbolKeyed
	}
	// an element read through a CAST of an opaque binding: the
	// assertion changes no runtime value, so the read stays opaque —
	// whatever the key's spelling (a symbol key included)
	if ast.IsElementAccessExpression(e) {
		elem := e.AsElementAccessExpression()
		root := elem.Expression
		for {
			if ast.IsParenthesizedExpression(root) {
				root = root.AsParenthesizedExpression().Expression
				continue
			}
			if ast.IsAsExpression(root) {
				root = root.AsAsExpression().Expression
				continue
			}
			if ast.IsNonNullExpression(root) {
				root = root.AsNonNullExpression().Expression
				continue
			}
			break
		}
		if ast.IsIdentifier(root) {
			if held, ok := env.Get(root.Text()); ok {
				if held.Kind == abstractdomain.KindUnknown && held.Opaque {
					evaluateExpression(ctx, env, elem.ArgumentExpression)
					out := abstractdomain.Opaque
					return &out
				}
			}
		}
	}
	// an element read DIRECTLY off a call's result: the list the call
	// answered indexes like any tracked list — `text.split("?")[0]`
	// reads the exact piece; past the end reads undefined (the list is
	// the whole answer)
	if ast.IsElementAccessExpression(e) && ast.IsCallExpression(e.AsElementAccessExpression().Expression) {
		elem := e.AsElementAccessExpression()
		called := evaluateExpression(ctx, env, elem.Expression)
		index := evaluateExpression(ctx, env, elem.ArgumentExpression)
		var exactIndex float64
		hasExactIndex := false
		if index.Kind == abstractdomain.KindValues && index.KindTag == abstractdomain.PrimitiveNumber &&
			len(index.Values) == 1 && isInteger(index.Values[0]) {
			exactIndex, hasExactIndex = index.Values[0], true
		}
		if called.Kind == abstractdomain.KindList && hasExactIndex {
			if exactIndex >= 0 && int(exactIndex) < len(called.Items) {
				out := called.Items[int(exactIndex)]
				return &out
			}
			out := abstractdomain.Undef
			return &out
		}
		if called.Kind == abstractdomain.KindValues && called.KindTag == abstractdomain.PrimitiveArray && hasExactIndex {
			if exactIndex >= 0 && int(exactIndex) < len(called.Values) {
				out := abstractdomain.KnownValues([]float64{called.Values[int(exactIndex)]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(called))
				return &out
			}
			out := abstractdomain.Undef
			return &out
		}
	}
	if ast.IsElementAccessExpression(e) {
		elem := e.AsElementAccessExpression()
		if ast.IsIdentifier(elem.Expression) {
			if _, ok := env.Get(elem.Expression.Text()); ok {
				receiver, hasReceiver := env.Get(elem.Expression.Text())
				if !hasReceiver {
					receiver = silence.Residue()
				}
				index := evaluateExpression(ctx, env, elem.ArgumentExpression)
				// an ARRAY OF RECORDS reads its element from the declared
				// statement: the object annotation outright when the exact index
				// sits under the count floor, or-absent otherwise (a get past
				// the end answers undefined) — the guard strips the maybe
				if declaredRoot, ok := ctx.Declared[elem.Expression.Text()]; ok && declaredRoot.Kind == annotations.DeclaredObjectArray {
					element := AbstractValueOfDeclared(annotations.DeclaredRefinement{Kind: annotations.DeclaredObject, Object: declaredRoot.Object})
					var exact float64
					hasExact := false
					if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 && isInteger(index.Values[0]) {
						exact, hasExact = index.Values[0], true
					}
					if hasExact && exact >= 0 && exact < declaredRoot.Lo {
						return &element
					}
					// POSITIVELY derived absence: the index may sit past the
					// guaranteed count, where a get answers undefined
					out := abstractdomain.PossiblyUndefined(element, "", false, true)
					return &out
				}
				// `o?.[i]`: the same optional-chain rule property links use
				if elem.QuestionDotToken != nil {
					threaded := ReadThroughMaybeReceiver(receiver, func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue {
						var at float64
						hasAt := false
						if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 && isInteger(index.Values[0]) {
							at, hasAt = index.Values[0], true
						}
						if hasAt && inner.Kind == abstractdomain.KindList && at >= 0 && int(at) < len(inner.Items) {
							return inner.Items[int(at)]
						}
						return silence.Residue()
					})
					if threaded != nil {
						return threaded
					}
				}
				if receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone &&
					!primitives.IsStringKind(ctx.P.Checker, elem.Expression) {
					inBounds := InBoundsElementOf(InBoundsElementOfParams{
						Ctx: ctx, Env: env, Expression: e, Receiver: receiver, Index: index,
					})
					if inBounds != nil {
						return inBounds
					}
				}
				// a string key on an OBJECT receiver reads that key: `b["lo"]`
				// and `b[k]` with an exact k ARE the dotted read — both evaluate
				// to the same Reference (sec-property-accessors)
				if receiver.Kind == abstractdomain.KindObject {
					var key string
					hasKey := false
					if ast.IsStringLiteral(elem.ArgumentExpression) || ast.IsNoSubstitutionTemplateLiteral(elem.ArgumentExpression) {
						key, hasKey = elem.ArgumentExpression.Text(), true
					} else if index.Kind == abstractdomain.KindValues && index.KindTag == abstractdomain.PrimitiveString {
						key, hasKey = stringOf(index.Values), true
					}
					if hasKey {
						if idx, ok := objectKeyIndex(receiver, key); ok {
							out := receiver.Keys[idx].Value
							return &out
						}
						if receiver.Complete && !openMapReceiver(ctx, elem.Expression) {
							// the same prototype-collision rule as the dotted read:
							// `errors["toString"]` on a complete plain object answers
							// the inherited FUNCTION, and any other missing key is
							// exactly undefined; a bare-prototype object inherits
							// nothing
							if receiver.BareProto {
								out := abstractdomain.Undef
								return &out
							}
							if abstractdomain.ObjectPrototypeFunctionKeys[key] {
								out := abstractdomain.HostFunction
								return &out
							}
							out := abstractdomain.Undef
							return &out
						}
						out := silence.Residue()
						return &out
					}
				}
				// a list element read is that slot's own knowledge.
				//
				// No absence wrapper rides along, and the in-bounds index is
				// the whole proof, because a KindList is HOLE-FREE by
				// construction: every KnownList in the tree is built
				// element-by-element from a source the walk already walked —
				// an array literal (array_literal.go, where an elision writes
				// Undef into its own slot), a split/entries/map/filter result,
				// a destructuring rest. Nothing grows a KindList by scattered
				// index write: an element-access write marks its whole
				// receiver (dataflowfacts/observed_paths.go markWriteTarget),
				// so `t[3] = x` retires the list rather than punching a hole
				// in it. A sequence that ARRIVES unproved — a parameter, a
				// summary read — is not a KindList at all; it reaches the
				// set-shaped arm above, which is where the absence is worn.
				if receiver.Kind == abstractdomain.KindList && index.Kind == abstractdomain.KindValues &&
					len(index.Values) == 1 && isInteger(index.Values[0]) {
					i := index.Values[0]
					if i >= 0 && int(i) < len(receiver.Items) {
						out := receiver.Items[int(i)]
						return &out
					}
					out := silence.Residue()
					return &out
				}
				if receiver.Kind == abstractdomain.KindValues && index.Kind == abstractdomain.KindValues && len(index.Values) == 1 {
					receiverStringy := primitives.IsStringKind(ctx.P.Checker, elem.Expression) || receiver.KindTag == abstractdomain.PrimitiveString
					// a string index is a UTF-16 unit position: it names a scalar
					// only where units and scalars coincide — astral-free tuples
					if receiverStringy && !refinementsets.AstralFree(receiver.Values) {
						out := silence.Residue()
						return &out
					}
					i := index.Values[0]
					if isInteger(i) && i >= 0 && int(i) < len(receiver.Values) {
						// a string's element is a 1-character STRING
						kindTag := abstractdomain.PrimitiveNumber
						if receiverStringy {
							kindTag = abstractdomain.PrimitiveString
						}
						out := abstractdomain.KnownValues(
							[]float64{receiver.Values[int(i)]},
							kindTag,
							abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(index)),
						)
						return &out
					}
				}
				out := silence.Residue()
				return &out
			}
		}
	}
	// an element read whose receiver is not a tracked identifier (a
	// field chain like `this.keys[i]`): the bounds evidence still
	// reads off the places and the ledger, so the same vouched facts
	// still speak
	if ast.IsElementAccessExpression(e) {
		evidence, hasEvidence := OutOfBoundsEvidence(OutOfBoundsEvidenceParams{Ctx: ctx, Env: env, Expression: e})
		if hasEvidence {
			ctx.Report(assignability.At(e, 7002, evidence+". "+assignability.AlertText))
		}
	}
	return nil
}
