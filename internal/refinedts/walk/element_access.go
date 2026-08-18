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
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
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
	atType := typereading.TypeAtLocation(c, at)
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
	// an element read DIRECTLY off a call's result, off ANOTHER such
	// read chained onto it, off a FRESH array literal built in place, or
	// off a FRESH construction (`new Array(n)[0]`, `new Uint8Array([…])
	// [0]`): the list indexes like any tracked list — `text.split("?")[0]`
	// reads the exact piece; past the end reads undefined (the list is
	// the whole answer). The receiver may itself be an element access on
	// a call result — `Object.entries(o)[0][1]` reads the pair at [0]
	// through this same arm (the recursive evaluateExpression call
	// below), then reads its second slot through this arm again — so the
	// gate admits a receiver that is itself an ElementAccessExpression,
	// not only a bare CallExpression. An array literal receiver —
	// `[...xs.values()][0]`, `[...a.union(b)][0]` — has no tracked name
	// and is not a call or element access itself, so without this arm
	// ElementAccessOf never even evaluates it: every arm below this one
	// gates on `elem.Expression` being an identifier, so the literal's
	// own value (EvaluateArrayLiteral, which reads the spread's items
	// exactly) is left unvisited and the read falls to the type-seeded
	// fallback. A bare `new Array(n)[0]`/`new Uint8Array([…])[0]` has the
	// same shape: EvaluateNewExpression already reads it exactly
	// (array_construction.go, typed_array_models.go), but with no
	// tracked name of its own it is neither a call, an element access,
	// nor a literal — the same gap the array-literal receiver closed for
	// a fresh literal, now closed for a fresh construction.
	// A PROPERTY-ACCESS receiver — plain or under `!`/`as`/parens
	// (`grouped.young![0]`, j-stdlib-surfaces' objectGroupBy row) — is
	// the same gap one more time: the receiver's own reader
	// (ReadPropertyAccess through evaluateExpression, absence stripped
	// by the cast layer) answers the exact KindList the groupBy model
	// built, and every arm below this one gates on an identifier
	// receiver, so without admission here the built list is never
	// indexed.
	if ast.IsElementAccessExpression(e) && (ast.IsCallExpression(e.AsElementAccessExpression().Expression) ||
		ast.IsElementAccessExpression(e.AsElementAccessExpression().Expression) ||
		ast.IsArrayLiteralExpression(e.AsElementAccessExpression().Expression) ||
		ast.IsNewExpression(e.AsElementAccessExpression().Expression) ||
		(Unwrapped(e.AsElementAccessExpression().Expression) != nil &&
			ast.IsPropertyAccessExpression(Unwrapped(e.AsElementAccessExpression().Expression)))) {
		elem := e.AsElementAccessExpression()
		called := evaluateExpression(ctx, env, elem.Expression)
		index := evaluateExpression(ctx, env, elem.ArgumentExpression)
		var exactIndex float64
		hasExactIndex := false
		if index.Kind == abstractdomain.KindValues && index.KindTag == abstractdomain.PrimitiveNumber &&
			len(index.Values) == 1 && isInteger(index.Values[0]) {
			exactIndex, hasExactIndex = index.Values[0], true
		}
		// an array-holes receiver (new Array(n) past the materialization
		// ceiling): the PRESENT-element set is ∅, so no index — in range
		// or past it — ever names a present value; the same Undef
		// machinery the identifier-receiver arm below wears for a
		// tracked KindArrayHoles (this file's own comment there)
		if called.Kind == abstractdomain.KindArrayHoles {
			if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 {
				out := abstractdomain.Undef
				return &out
			}
			out := silence.Residue()
			return &out
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
		// a star/repetition-shaped sequence built by THIS literal, with no
		// tracked name of its own — `[...new GenAges().ages()][0]`:
		// EvaluateArrayLiteral's own spread arm (array_literal.go) answers
		// a star over the drained sequence, carrying a proven LOWER BOUND
		// as a Repetition where the source proved one (a generator's
		// leading unconditional yields — GeneratorSequenceOf,
		// generator_element.go). InBoundsElementOf (below, the
		// identifier-receiver arm's reader) wraps even an under-floor read
		// in PossiblyUndefined, because a receiver reached by NAME may
		// have arrived from anywhere — a parameter, a summary row — and
		// carries no proof it was never sparsified after the fact. A
		// value this arm just built, this expression, from this literal's
		// own spread has no such history: nothing between the build and
		// this read could have punched a hole in it, so an index proven
		// under the repetition's floor reads the element BARE.
		if called.Kind == abstractdomain.KindSet && called.SetKindTag == abstractdomain.SetKindTagNone &&
			hasExactIndex && !primitives.IsStringKind(ctx.P.Checker, elem.Expression) {
			if rep, repOk := refinementsets.AsRepetition(called.Set); repOk {
				window := IndexWindow(index)
				if window != nil && window.Hi < float64(rep.Lo) && !isStringGroundElement(rep.Element) {
					grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(called), abstractdomain.TrustLevelOf(index))
					var read abstractdomain.AbstractValue
					// the element set collapses to ONE number — every yield
					// this floor covers read the same constant (the generator
					// body's own straight-line reading, generator_element.go)
					// — so the position is that exact value, spelled the way
					// every other exact scalar position is (KnownValues), not
					// the set form a wider element would need
					if elementRange := RangeOfSet(rep.Element); elementRange != nil && elementRange.Lo == elementRange.Hi &&
						!elementRange.LoStrict && !elementRange.HiStrict {
						read = abstractdomain.KnownValues([]float64{elementRange.Lo}, abstractdomain.PrimitiveNumber, grade)
					} else {
						read = abstractdomain.KnownSet(rep.Element, nil, grade, abstractdomain.SetKindTagNone)
					}
					if called.NaNElements {
						out := abstractdomain.PossiblyNaN(read)
						return &out
					}
					return &read
				}
			}
		}
		// `re.exec(s)?.[1]`: the call's result rides the maybe wrapper
		// (null-or-match, sec-regexp.prototype.exec) — the SAME optional-
		// chain rule property and identifier-receiver element reads use
		// (maybe_receiver_access.go), applied here where the receiver is
		// the call expression itself rather than a tracked name
		if elem.QuestionDotToken != nil && hasExactIndex {
			threaded := ReadThroughMaybeReceiver(called, func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue {
				if inner.Kind == abstractdomain.KindList && exactIndex >= 0 && int(exactIndex) < len(inner.Items) {
					return inner.Items[int(exactIndex)]
				}
				return silence.Residue()
			})
			if threaded != nil {
				return threaded
			}
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
					// guaranteed count, where a get answers exactly undefined
					// (sec-ordinaryget: a missing own property, once the
					// prototype chain reaches null, returns undefined — never
					// null) — so the wrapper's own absent side is UndefOnly.
					out := abstractdomain.PossiblyAbsent(element, abstractdomain.AbsentFlavorUndefOnly, "", false, true)
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
					// a STABLE SYMBOL key reads the slot the literal wrote
					// under the derived #sym: name (object_literal.go /
					// keyed_slot_reads.go). A missed slot claims NOTHING —
					// not even absence: two different consts may spell the
					// SAME registry symbol (Symbol.for twice,
					// sec-symbol.for), so the missing spelled slot does not
					// witness a missing runtime key.
					if elem.QuestionDotToken == nil {
						if slot, _, stable := stableSymbolSlotOf(ctx.P.Checker, elem.ArgumentExpression); stable {
							if idx, found := objectKeyIndex(receiver, slot); found {
								out := receiver.Keys[idx].Value
								return &out
							}
							out := silence.Residue()
							return &out
						}
					}
					var key string
					hasKey := false
					if ast.IsStringLiteral(elem.ArgumentExpression) || ast.IsNoSubstitutionTemplateLiteral(elem.ArgumentExpression) {
						key, hasKey = elem.ArgumentExpression.Text(), true
					} else if ast.IsNumericLiteral(elem.ArgumentExpression) {
						// `person[0]` — ToPropertyKey converts the Number
						// argument through ToString (sec-topropertykey step
						// 2), the same key `{ 0: 40 }` writes
						// (object_literal.go); the literal's own source
						// spelling is that string
						key, hasKey = elem.ArgumentExpression.Text(), true
					} else if index.Kind == abstractdomain.KindValues && index.KindTag == abstractdomain.PrimitiveString {
						key, hasKey = stringOf(index.Values), true
					}
					// an exact string spelling the #sym: prefix would read a
					// symbol slot as if it were a string key — the slot
					// vocabulary owns that spelling, so the read claims
					// nothing
					if hasKey && symbolSlotKey(key) {
						out := silence.Residue()
						return &out
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
					// a key that is ONE OF several exact strings reads the
					// JOIN of the named slots: ToPropertyKey of an exact
					// string is that string (sec-topropertykey), so the
					// runtime read lands on one of the spelled names and
					// every value it can answer is one the join admits. A
					// member the object does not name reads as the
					// missing-key rule above answers it; a member the walk
					// cannot resolve leaves the whole read unclaimed. The
					// sort gate keeps a numeric one-member set from being
					// reread as code units (oneOf pins neither sort —
					// sortOfForms, kernel_delegation.go).
					if index.Kind == abstractdomain.KindSet && index.SetKindTag == abstractdomain.SetKindTagNone &&
						sortOfForms(index.Set.Forms) == BindingKindString {
						if words, wordsOK := exactWordsOfSet(index.Set); wordsOK && len(words) > 0 {
							var joined abstractdomain.AbstractValue
							hasJoined := false
							determined := true
							for _, word := range words {
								name := stringOf(word)
								var member abstractdomain.AbstractValue
								if symbolSlotKey(name) {
									determined = false
									break
								}
								if idx, found := objectKeyIndex(receiver, name); found {
									member = receiver.Keys[idx].Value
								} else if receiver.Complete && !openMapReceiver(ctx, elem.Expression) {
									switch {
									case receiver.BareProto:
										member = abstractdomain.Undef
									case abstractdomain.ObjectPrototypeFunctionKeys[name]:
										member = abstractdomain.HostFunction
									default:
										member = abstractdomain.Undef
									}
								} else {
									determined = false
									break
								}
								if !hasJoined {
									joined, hasJoined = member, true
								} else {
									joined = abstractdomain.JoinKnown(joined, member)
								}
							}
							if determined && hasJoined {
								out := abstractdomain.AtTrustLevel(joined,
									abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(joined), abstractdomain.TrustLevelOf(index)))
								return &out
							}
							out := silence.Residue()
							return &out
						}
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
				// an OBJECT-STAR slot: every position that exists holds the
				// element, and the form claims NO count — so no index is
				// provably in bounds and the read is the element or nothing
				// (sec-array-exotic-objects: a get past the end answers
				// undefined). The absence is POSITIVELY derived — the star
				// states the length is unclaimed, so a run where this index
				// is past the end is admitted, not merely unproved.
				if element, ok := abstractdomain.ElementOfObjectStar(receiver); ok {
					// sec-ordinaryget: a get past the end reaches no own
					// property and returns exactly undefined, never null —
					// the wrapper's own absent side is UndefOnly.
					out := abstractdomain.PossiblyAbsent(element, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true)
					return &out
				}
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
				// an array-holes receiver's element read: the PRESENT-element
				// set (receiver.ElementSet) is ∅ — nothing is a member of
				// it, so there is no present value any index could name, in
				// range or past it, whatever the index's own shape. The read
				// answers undefined not because the index missed a bound but
				// because the empty set never has a member to hand back —
				// the same Undef machinery a plain absent value wears.
				if receiver.Kind == abstractdomain.KindArrayHoles {
					if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 {
						out := abstractdomain.Undef
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
					// an integer index past the tuple — negative or beyond
					// the length — reads exactly undefined: an array's get
					// past the end (sec-array-exotic-objects), a
					// TypedArray's invalid integer index
					// (TypedArrayGetElement), and a string's out-of-range
					// unit position all answer absence, never a value
					if isInteger(i) && (receiver.KindTag == abstractdomain.PrimitiveArray || receiverStringy) {
						out := abstractdomain.Undef
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
