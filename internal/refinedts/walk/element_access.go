// from evaluation/element_access.ts
//
// Element reads: opaque cast roots, call-result indexing, declared
// object-array elements, optional-chain threading, object string
// keys, and list/value slots. In-bounds proofs live beside this
// file in element_in_bounds.ts.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
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
	tracing.CountBy("host.indexInfosOfType", 1)
	return len(c.GetIndexInfosOfType(atType)) > 0
}

// IndexSignatureValueTypeAt reads the DECLARED value type an index
// signature on at's type states — `number` in `{ [k: string]: number }`
// — as an AbstractValue, for a receiver OpenMapAt already found open.
// This answers a different question than OpenMapAt's own gate: whether
// a KEY is present is unproven for an open-map type (OpenMapAt's own
// doctrine, above), but the value an index signature states for
// WHICHEVER key is present is a fixed fact of the type itself, present
// or not. false when the type carries no index signature, or the host
// reader cannot resolve its value type.
func IndexSignatureValueTypeAt(ctx *FlowContext, at *ast.Node) (abstractdomain.AbstractValue, bool) {
	if at == nil {
		return abstractdomain.AbstractValue{}, false
	}
	atType := typereading.TypeAtLocation(ctx.P.Checker, at)
	if atType == nil {
		return abstractdomain.AbstractValue{}, false
	}
	tracing.CountBy("host.indexInfosOfType", 1)
	infos := ctx.P.Checker.GetIndexInfosOfType(atType)
	if len(infos) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	held, ok := typereading.ReadHostType(ctx.P.Checker, infos[0].ValueType(), at, 0)
	if !ok {
		return held, false
	}
	// the same TRUST GRADE a plain parameter's own annotation wears
	// (entry_env.go's InitialStateOfPlainParameter doc): an index
	// signature's stated value type is READ off the receiver's own
	// declaration, not proved by any execution or cross-call check —
	// TrustSpec, never the TrustProved a fresh host-type read
	// otherwise defaults to. Without this, a bare-ground value type
	// (`number` in `{ [k: string]: number }`) wraps as an UNGRADED
	// PossiblyNaN, which CheckPossiblyNaN reads as an unexamined
	// fallback seed and declines instead of asking the real subset
	// question against the target set.
	return abstractdomain.AtTrustLevel(held, abstractdomain.TrustSpec), true
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
		// ceiling): the PRESENT-element set is ∅, so no OWN numeric
		// property is ever found (sec-ordinaryget: [[GetOwnProperty]]
		// misses, and the read falls to the prototype chain) — this holds
		// for ANY index whose window is a provably nonnegative integer,
		// not only a single exact one, so the answer generalizes past the
		// exact-index case to the whole IndexWindow. A NON-numeric-sorted
		// index (KindUnknown, a string-shaped set, or anything ToPropertyKey
		// could stringify to a name like "toString"/"constructor") is a
		// real collision risk against the inherited Array.prototype/
		// Object.prototype members — sec-ordinaryget's prototype-chain
		// step answers THAT member, not undefined — so that case still
		// declines, naming the hazard rather than assuming Undef.
		if called.Kind == abstractdomain.KindArrayHoles {
			if IndexWindow(index) != nil {
				out := abstractdomain.Undef
				return &out
			}
			out := silence.ResidueOf("the index into an array-holes receiver isn't provably a " +
				"nonnegative integer, so it may spell an inherited Array.prototype/Object.prototype " +
				"member name instead of missing every own property")
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
				return silence.ResidueOf("re.exec(s)?.[1] threads the maybe wrapper, " +
					"but the present side isn't a tracked list, so which element the index names isn't pinned")
			})
			if threaded != nil {
				return threaded
			}
		}
	}
	if ast.IsElementAccessExpression(e) {
		elem := e.AsElementAccessExpression()
		if ast.IsIdentifier(elem.Expression) {
			if _, ok := env.Get(elem.Expression.Text()); !ok {
				// an identifier receiver env NEVER bound at all — not even
				// to a reasonless residue — is the ordinary shape a bare
				// walk-route test gives a function PARAMETER (this package's
				// helpers that call AnalyzeStatements straight on a
				// function's body statements, e.g.
				// compoundAssignFunctionStatements, never call
				// BindEntryEnv first; the live checker path does bind every
				// parameter, so this arm is defense in depth there too —
				// any other route that reaches an element read before its
				// receiver's own binding statement ran). Every arm below
				// this whole block assumes SOME tracked value, so none of
				// them can answer; the one thing still readable off the
				// unbound name is its OWN declared type, and an open-map
				// declared type (Record<K, V>, an index signature) is
				// exactly the same fact the tracked-KindObject arm below
				// already names — a missing key is not a definite absence,
				// because the key set is not fixed. Named here so an
				// untracked open-map parameter gets that reason too,
				// instead of falling to the terminal unknown with none.
				if openMapReceiver(ctx, elem.Expression) {
					out := silence.ResidueOf("the receiver names no fixed key set — an index signature or an " +
						"incomplete record admits any key at runtime, and the checker never witnessed which")
					return &out
				}
			}
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
						return silence.ResidueOf("o?.[i] threads the maybe wrapper, but the present " +
							"side isn't a tracked list at a known index, so which element i names isn't pinned")
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
					// every member this arm reads off `receiver` below
					// threads through memberValueGraded (object_key_access.go):
					// a receiver whose OWN Grade names a checked declaration
					// (typeGroundOf's AtTrustLevel stamp on a call's return,
					// e.g.) hands that same floor to the member it reads off
					// it — `handle["value"]` is the bracketed spelling of the
					// same read `handle.value` makes, and must carry the
					// identical grade.
					//
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
								out := memberValueGraded(receiver, receiver.Keys[idx].Value)
								return &out
							}
							out := silence.ResidueOf("the stable symbol slot doesn't match a written key, but " +
								"two different consts can spell the same registry symbol, so a missed slot doesn't witness a missing key")
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
						out := silence.ResidueOf("the key spells the #sym: prefix, which names a symbol slot, " +
							"not a string key — the slot vocabulary owns that spelling, so the read claims nothing")
						return &out
					}
					if hasKey {
						if idx, ok := objectKeyIndex(receiver, key); ok {
							out := memberValueGraded(receiver, receiver.Keys[idx].Value)
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
						// the key is absent from the tracked keys AND the
						// receiver's type carries an index signature: whether
						// the key is PRESENT at runtime is unproven
						// (OpenMapAt's own doctrine), but the VALUE the
						// signature states for whichever key answers is a
						// fixed fact of the type either way — `number` in
						// `{ [k: string]: number }`. Reading it here answers
						// the read with that fact instead of an absence
						// claim; the assignability layer downstream decides
						// whether the signature's stated value fits the
						// target refined set.
						if valueType, ok := IndexSignatureValueTypeAt(ctx, elem.Expression); ok && valueType.Kind != abstractdomain.KindUnknown {
							return &valueType
						}
						out := silence.ResidueOf("the receiver names no fixed key set — an index signature or an " +
							"incomplete record admits any key at runtime, and the checker never witnessed which")
						return &out
					}
					// a key that is ONE OF several exact strings reads the
					// JOIN of the named slots: ToPropertyKey of an exact
					// string is that string (sec-topropertykey), so the
					// runtime read lands on one of the spelled names and
					// every value it can answer is one the join admits. A
					// member the object does not name reads as the
					// missing-key rule above answers it; a member the walk
					// cannot resolve leaves the whole read unclaimed.
					//
					// THE SORT GATE, AND THE ONE READING THAT PASSES
					// WITHOUT IT. A spelling of bare exact values pins no
					// sort (`oneOf` — sortOfForms, kernel_delegation.go),
					// and a one-CODEPOINT word is spelled that way, so a
					// union of single-letter keys like `"a" | "b"` arrives
					// sort-unpinned even though every member is a string.
					// Reading such a set as numbers would name the code
					// units U+0061 and U+0062 as keys instead of "a" and
					// "b", which is why the gate is here at all.
					//
					// It is settled by the RECEIVER rather than by the
					// spelling: where every enumerated member names a key
					// the receiver actually states, the numeric reading is
					// refuted outright — an object naming "a" does not also
					// name the character U+0061, and no spelling in this
					// tree writes one. So the join runs on a sort-unpinned
					// set exactly when the object itself vouches for every
					// member, and stays refused otherwise.
					if index.Kind == abstractdomain.KindSet && index.SetKindTag == abstractdomain.SetKindTagNone {
						sortPinned := sortOfForms(index.Set.Forms) == BindingKindString
						words, wordsOK := exactWordsOfSet(index.Set)
						if wordsOK && len(words) > 0 && !sortPinned {
							everyMemberNamed := true
							for _, word := range words {
								name := stringOf(word)
								if symbolSlotKey(name) {
									everyMemberNamed = false
									break
								}
								if _, found := objectKeyIndex(receiver, name); !found {
									everyMemberNamed = false
									break
								}
							}
							sortPinned = everyMemberNamed
						}
						if wordsOK && len(words) > 0 && sortPinned {
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
									member = memberValueGraded(receiver, receiver.Keys[idx].Value)
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
							out := silence.ResidueOf("one of the exact key-set members names a symbol slot or an " +
								"unnamed key on a receiver with no fixed key set, so the join over the whole set can't be pinned")
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
				// element, and the form states NO COUNT of its own — so a
				// bare read of an object-star answers the element or
				// nothing (sec-array-exotic-objects: a get past the end
				// answers undefined), the absence POSITIVELY derived since
				// the star's own claim leaves the length unstated.
				//
				// A GUARD can still prove this index in bounds despite the
				// star carrying no floor: `i < arr.length` (or `i <=
				// arr.length - 1`, or a summed index against arr.length)
				// is a claim about the PLACE `arr.length` denotes, tracked
				// by the same DifferenceConstraints ledger a repetition-
				// shaped receiver's own in-bounds arm reads
				// (element_in_bounds.go's IndexBelowLengthPlace, factored
				// out of InBoundsElementOf for exactly this reuse) — the
				// ledger row does not care what SHAPE the receiver's own
				// AbstractValue wears, only what the guard proved about
				// the length place. In bounds, the read is the star's
				// element with no maybe wrapper at all; out of bounds (or
				// unproved), the absence stands.
				if element, ok := abstractdomain.ElementOfObjectStar(receiver); ok {
					if IndexBelowLengthPlace(ctx, env, elem.Expression, elem.ArgumentExpression) {
						return &element
					}
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
					// an out-of-bounds (negative or >= length) integer index
					// into a hole-free KindList reads exactly undefined, the
					// same answer the KindValues/PrimitiveArray twin below
					// gives (sec-array-exotic-objects: OrdinaryGet finds no
					// own property past the end and falls to the prototype
					// chain). ToPropertyKey of any float64 here is a plain
					// decimal numeral, never a name that could collide with
					// an inherited Array.prototype/Object.prototype member —
					// unlike KindArrayHoles's own non-numeric-index arm, this
					// index is ALREADY known to be one exact number, so there
					// is no residual key shape left to be unsure about.
					out := abstractdomain.Undef
					return &out
				}
				// a RANGED (set-shaped) numeric index into a KindList: the
				// runtime read lands on ONE position, whichever the index
				// picks that run — every position in [0, len) the index's own
				// admitted range overlaps contributes its element to the
				// join (sec-array-exotic-objects' OrdinaryGet: a numeric
				// property key that names an existing slot answers that
				// slot). Where the index ALSO admits a value outside [0,
				// len) — negative, non-integer (ToPropertyKey stringifies it
				// to a name no array index matches), or >= len — that run
				// reads no own property and answers exactly undefined
				// (never a decline for a plainly bounded window: the JOIN
				// stays exact, only the possibly-undefined arm rides beside
				// it). RangedListElementOf answers nil where the index is
				// not numeric at all (a string/array-sorted index reads no
				// element position here).
				//
				// A POSSIBLY-NaN index (a bare `number`-typed parameter,
				// annotations/type_node_sets.go's grounding, worn as
				// PossiblyNaN by declared_value.go's AbstractValueOfDeclared)
				// unwraps here rather than skipping the whole ranged-index
				// arm: NaN's own runtime read is exactly the out-of-bounds
				// case already reasoned about above — ToPropertyKey stringifies
				// NaN to the property name "NaN", which no array index
				// names, so that run answers undefined the same way a
				// negative or overlong index does (OrdinaryGet's own miss).
				// The real half still reads its in-bounds join normally; the
				// NaN half only ever CONTRIBUTES the possibly-undefined arm,
				// never blocks the real half's own determination.
				rangedIndex := index
				indexMayBeNaN := false
				if rangedIndex.Kind == abstractdomain.KindPossiblyNaN && rangedIndex.Inner != nil {
					indexMayBeNaN = true
					rangedIndex = *rangedIndex.Inner
				}
				if receiver.Kind == abstractdomain.KindList && rangedIndex.Kind == abstractdomain.KindSet &&
					rangedIndex.SetKindTag == abstractdomain.SetKindTagNone {
					if out := RangedListElementOf(receiver, rangedIndex); out != nil {
						if indexMayBeNaN {
							withNaN := abstractdomain.PossiblyAbsent(*out, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true)
							if out.Kind == abstractdomain.KindPossiblyUndefined {
								// the real half already carries its own
								// possibly-undefined arm (an out-of-bounds
								// admission RangedListElementOf found on its
								// own) — the NaN half adds no NEW flavor
								// beyond UndefOnly, already worn, so the
								// unwrapped answer stands as it came.
								withNaN = *out
							}
							return &withNaN
						}
						return out
					}
				}
				// an array-holes receiver's element read: the PRESENT-element
				// set (receiver.ElementSet) is ∅ — no OWN numeric property is
				// ever found (sec-ordinaryget: [[GetOwnProperty]] misses, and
				// the read falls to the prototype chain), for ANY index whose
				// window is a provably nonnegative integer — not only a
				// single exact one, so the answer generalizes past the
				// exact-index case to the whole IndexWindow. A NON-numeric-
				// sorted index (KindUnknown, a string-shaped set, or
				// anything ToPropertyKey could stringify to a name like
				// "toString"/"constructor") is a real collision risk against
				// the inherited Array.prototype/Object.prototype members —
				// sec-ordinaryget's prototype-chain step answers THAT
				// member, not undefined — so that case still declines,
				// naming the hazard rather than assuming Undef.
				if receiver.Kind == abstractdomain.KindArrayHoles {
					if IndexWindow(index) != nil {
						out := abstractdomain.Undef
						return &out
					}
					out := silence.ResidueOf("the index into an array-holes receiver isn't provably a " +
						"nonnegative integer, so it may spell an inherited Array.prototype/Object.prototype " +
						"member name instead of missing every own property")
					return &out
				}
				if receiver.Kind == abstractdomain.KindValues {
					receiverStringy := primitives.IsStringKind(ctx.P.Checker, elem.Expression) || receiver.KindTag == abstractdomain.PrimitiveString
					// a string index is a UTF-16 unit position: it names a scalar
					// only where units and scalars coincide — astral-free tuples.
					// This is the receiver's OWN property — true or false before
					// the index is even looked at — so it is checked ahead of
					// the exact-index gate below: a SYMBOLIC index (`s[i]` with
					// i an unconstrained number) into an astral string is exactly
					// as unsound to determine as an exact one, and naming the
					// astral hazard is the position's real first blocker, not
					// the weaker "index isn't exact" the generic fallthrough
					// would otherwise report.
					if receiverStringy && !refinementsets.AstralFree(receiver.Values) {
						out := silence.ResidueOf("the string carries an astral (surrogate-pair) character, " +
							"so a UTF-16 unit index doesn't name one scalar code point")
						return &out
					}
					if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 {
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
					// a WINDOWED index PROVABLY AN INTEGER inside the
					// tuple's own bounds reads the JOIN of the covered
					// positions — sec-array-exotic-objects reads one
					// position per admitted index, so the covered
					// members' own list is every value the read can
					// answer. The integrality form is required: a real
					// index between positions reads absence, which this
					// answer must not hide.
					if index.Kind == abstractdomain.KindSet {
						integerRequired := false
						for _, f := range index.Set.Forms {
							if f.Form == refinementsets.FormInteger {
								integerRequired = true
								break
							}
						}
						if integerRequired {
							if bounds, boundsOk := narrowing.BoundsOfKnown(index); boundsOk {
								lo := int(math.Ceil(bounds.Lo))
								hi := int(math.Floor(bounds.Hi))
								if lo >= 0 && hi >= lo && hi < len(receiver.Values) {
									kindTag := abstractdomain.PrimitiveNumber
									if receiverStringy {
										kindTag = abstractdomain.PrimitiveString
									}
									out := abstractdomain.KnownValues(
										append([]float64{}, receiver.Values[lo:hi+1]...),
										kindTag,
										abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(index)),
									)
									return &out
								}
							}
						}
					}
				}
				// an OPEN-MAP receiver that never became a tracked KindObject
				// at all — the ordinary shape for a plain `Record<K, V>`-typed
				// parameter with no refinement annotation and no object
				// literal ever bound to it: ReadDeclaredType's own syntax and
				// host readers both decline an index-signature type (neither
				// GetPropertiesOfType nor the type-node reader names any
				// PROPERTY for an index signature to iterate — read_type.go /
				// host_type.go), so InitialStateOfPlainParameter falls to
				// silence.Residue() and the receiver never reaches the
				// KindObject arm above, which is the only place this file's
				// OTHER open-map reason already lives. This is that same
				// fact — a Record's key set is not fixed, so a missing key is
				// not a definite absence — named for the receiver shape that
				// arm cannot see, ahead of the generic index-shaped fallback
				// below, which would otherwise blame the index for a gap that
				// is really about the receiver's own type.
				if receiver.Kind == abstractdomain.KindUnknown && !receiver.Opaque && openMapReceiver(ctx, elem.Expression) {
					// WHICH key answers is unproven, but WHAT a key answers
					// is not: the index signature states one value type for
					// every key it admits — `string` in
					// `Record<string, string>` — and that is a fact of the
					// receiver's own type, present key or not. So the read
					// answers the signature's stated value rather than
					// nothing, and the assignability layer downstream
					// decides whether that value fits the target set (the
					// same reading the tracked-KindObject arm above already
					// does for a key absent from its own keys).
					//
					// The absence rides only where the shape channel puts
					// one, the same rule element_in_bounds.go states for a
					// repetition read: with noUncheckedIndexedAccess ON,
					// GetTypeAtLocation answers `T | undefined` at every
					// indexed read and this wrapper agrees with it; with
					// the flag OFF — the default, and what tsc's `strict`
					// leaves it at — the host answers `T`, and wrapping
					// anyway would make this layer refuse a value the shape
					// channel has already accepted. sec-ordinaryget: a
					// missing own property falls to the prototype chain,
					// which for an ordinary record carries no such key, so
					// the miss answers exactly undefined — never null, so
					// the wrapper's absent side is UndefOnly.
					if valueType, ok := IndexSignatureValueTypeAt(ctx, elem.Expression); ok && valueType.Kind != abstractdomain.KindUnknown {
						if !UncheckedIndexedAccessHonored(ctx) {
							return &valueType
						}
						out := abstractdomain.PossiblyAbsent(valueType, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, false)
						return &out
					}
					out := silence.ResidueOf("the receiver names no fixed key set — an index signature or an " +
						"incomplete record admits any key at runtime, and the checker never witnessed which")
					return &out
				}
				// a KIND UNION receiver — the shape `JSON.parse(text)`
				// answers (coercion_models_json.go's anyJSONValue: a
				// number, a string, a boolean, null, or an object, one arm
				// each). The runtime read lands on whichever arm the value
				// is actually on, so what the read can answer is the JOIN
				// over the arms' own element readings — sound for exactly
				// the reason JoinKnown is sound anywhere else: every value
				// the read can produce is a value some arm produces.
				//
				// Arms that hold no sequence at all — a number, a boolean,
				// null — read no element position: an index into them
				// finds no own property and falls to the prototype chain
				// (sec-ordinaryget), which for these arms carries no
				// numeric slot, so the read is exactly undefined. That
				// undefined joins in rather than blocking the whole read,
				// which is the honest answer: a JSON document that spells
				// a number really does make `parsed[0]` undefined.
				//
				// An arm whose OWN element reading declines takes the
				// whole read with it — the join would otherwise claim the
				// union answers less than one of its arms might.
				if receiver.Kind == abstractdomain.KindKindUnion {
					if joined := unionElementOf(receiver, index); joined != nil {
						return joined
					}
					// an arm the reader could not answer for. The union's own
					// carried reason names where the union came from
					// (JSON.parse states one, coercion_models_json.go), which
					// is the position's real first blocker — the index is not
					// what stopped this read.
					if receiver.ResidueReason != "" {
						out := silence.ResidueOf(receiver.ResidueReason)
						return &out
					}
					out := silence.ResidueOf("the receiver is one of several kinds and at least one of " +
						"them states no element at this position, so the join over the arms can't be pinned")
					return &out
				}
				out := silence.ResidueOf("the index isn't a single exact value against a tracked value set, " +
					"so which element position it names isn't pinned")
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
