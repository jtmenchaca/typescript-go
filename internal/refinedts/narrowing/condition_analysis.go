// A guard narrows the place it tests — and the SETS a guard proves
// are the kernel's to construct, not this file's. The reading here
// is the program: which place a comparison tests, which literal it
// tests against, which Number predicate was called, how the tests
// compose under !, &&, ||. That reading is lowered to the kernel's
// narrowing tree (one per tested place, every unreadable test the
// honest `other` leaf), and the kernel answers both branches' sets
// with their STRENGTHS — the NaN discipline (every comparison with
// NaN is false, so only truth admits a value into a set) decided and
// PROVED kernel-side (transfers/narrow_correct.lean).
//
// Two narrowings stay structural, because they are not set claims:
// an absence test (`=== undefined` / `=== null`) reads as
// definedness, and a held string equality pins the EXACT tuple as a
// working value.
//
// BLOCKED (partial): the TS source's final branch — recording an
// UNREAD GUARD via
// predicate_read.ts's mentionsTracked/recordUnreadGuard — is also
// dropped: recordUnreadGuard needs assignability/decline_reasons.ts's
// noteReason, which is outside this directory's allowed import set
// (assignability is a later wave). mentionsTracked itself has no such
// dependency and is ported in predicate_read.go.
package narrowing

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// narrowRefusable asks the kernel's Narrow question, turning a refusal
// (Narrow panics on a refused question, per kernel_asks.go) into an
// (answer, refused) pair — the TS source's try/catch around
// `narrowKernel.narrow(tree)`.
func narrowRefusable(tree kernelbridge.NarrowTree) (answer kernelbridge.NarrowAnswer, refused bool) {
	defer func() {
		if recover() != nil {
			answer, refused = kernelbridge.NarrowAnswer{}, true
		}
	}()
	return NarrowKernel().Narrow(tree), false
}

// Peeled is peeled in the TS source: parentheses say nothing; `await`
// hands the resolved value on — the branch a guard takes IS the awaited
// value's truthiness, so both peel before any reading.
func Peeled(condition *ast.Node) *ast.Node {
	e := condition
	for ast.IsParenthesizedExpression(e) || ast.IsAwaitExpression(e) {
		if ast.IsParenthesizedExpression(e) {
			e = e.AsParenthesizedExpression().Expression
		} else {
			e = e.AsAwaitExpression().Expression
		}
	}
	return e
}

// Narrowings is narrowings in the TS source.
func Narrowings(
	c *checker.Checker,
	condition *ast.Node,
	isTracked func(name string) bool,
	sideBounds SideBounds,
	readElsewhere GuardReadElsewhere,
) BranchNarrowings {
	// THE NARROWING DISPATCH SEAM of the derivation trace: one span per
	// guard read, carrying the CONDITION's own spelling and range. What
	// the guard proved is the answer; a guard that proved nothing about
	// any tracked place declines, and the gate names that. Off is one
	// atomic load inside Active().
	if derivation.Active() {
		span := derivation.BeginNode("narrowings", condition)
		defer func() {
			span.End()
		}()
		branch := narrowingsRecorded(c, condition, isTracked, sideBounds, readElsewhere)
		if len(branch.WhenTrue) == 0 && len(branch.WhenFalse) == 0 {
			span.Decline("the guard proves no set for any tracked place", derivation.Range(condition), "no narrowing")
		} else {
			span.Answer(spellNarrowings(branch))
		}
		return branch
	}
	return narrowingsRecorded(c, condition, isTracked, sideBounds, readElsewhere)
}

// narrowingsRecorded is Narrowings' body once the derivation span is
// handled — the existing timing-span path, unchanged.
func narrowingsRecorded(
	c *checker.Checker,
	condition *ast.Node,
	isTracked func(name string) bool,
	sideBounds SideBounds,
	readElsewhere GuardReadElsewhere,
) BranchNarrowings {
	if !tracing.Recording(tracing.GrainStep) {
		return NarrowingsOf(c, condition, isTracked, sideBounds, readElsewhere)
	}
	result := tracing.Span("narrowings", func() BranchNarrowings {
		return NarrowingsOf(c, condition, isTracked, sideBounds, readElsewhere)
	}, tracing.GrainStep)
	tracing.Count("narrowings", 0)
	return result
}

// NarrowingsOf is narrowingsOf in the TS source.
func NarrowingsOf(
	c *checker.Checker,
	condition *ast.Node,
	isTracked func(name string) bool,
	sideBounds SideBounds,
	readElsewhere GuardReadElsewhere,
) BranchNarrowings {
	// a condition BOUND TO A NAME keeps every fact it encodes:
	// `const ok = cond; if (ok)` — and the `ok === true`, `!ok`,
	// `a && b` spellings over bound names — read as the conditions
	// themselves wherever the resolution is sound. A condition that
	// resolves to no bound name falls straight through to the normal
	// reading below.
	if resolved, ok := ResolveBoundCondition(c, condition); ok {
		b := NarrowingsOf(c, resolved.Condition, isTracked, sideBounds, readElsewhere)
		if len(b.WhenTrue) > 0 || len(b.WhenFalse) > 0 {
			if resolved.Flipped {
				b = BranchNarrowings{WhenTrue: b.WhenFalse, WhenFalse: b.WhenTrue}
			}
			// THE BOUND NAME NARROWS TOO. The resolved reading states what
			// the guard proves about the condition's OWN operands (`if (ok)`
			// through `const ok = true !== f` proves f is false), and that
			// is the only thing this branch used to return — so `ok` itself
			// crossed its own guard holding both truth values, and a `true`
			// position then refused it. The bare place is a test of the
			// name, whatever the name was bound to: held truth keeps its
			// truthy values, held falsity the falsy ones. Both narrowings
			// apply intersectively, so stating the name's own fact beside
			// the operands' costs nothing where the operand reading already
			// pinned it.
			if place := dataflowfacts.TrackedPlaceOfWith(c, Peeled(condition), isTracked); place != nil {
				truthy := Narrowed{Binding: place.Binding, Path: place.Path, Definedness: "defined", Truthiness: "truthy"}
				falsy := Narrowed{Binding: place.Binding, Path: place.Path, Truthiness: "falsy"}
				b.WhenTrue = append(b.WhenTrue, truthy)
				b.WhenFalse = append(b.WhenFalse, falsy)
			}
			return b
		}
	}
	// `P === undefined || <numeric tests on P>` (either order): the
	// held disjunction admits the absent value OR a value the numeric
	// side proves — the present part narrows to the numeric side's own
	// whenTrue while absence stays admitted. Absence is the structural
	// channel's fact and the set is the kernel's; neither channel alone
	// can state the union, so the composition is read here.
	var crossChannel []Narrowed
	{
		cond := Peeled(condition)
		if ast.IsPrefixUnaryExpression(cond) && cond.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
			if inner, ok := ResolveBoundCondition(c, cond.AsPrefixUnaryExpression().Operand); ok {
				b := NarrowingsOf(c, inner.Condition, isTracked, sideBounds, readElsewhere)
				if len(b.WhenTrue) > 0 || len(b.WhenFalse) > 0 {
					if inner.Flipped {
						return b
					}
					return BranchNarrowings{WhenTrue: b.WhenFalse, WhenFalse: b.WhenTrue}
				}
			}
		}
		// a DISJUNCTION of literal string equalities on ONE place: the
		// held condition admits exactly those words — the same claim a
		// switch's case run states. Without this row, a two-alias union
		// value crossed the guard unshed and the kind-union arm check
		// refuted values the guard had excluded (recharts' layout).
		if ast.IsBinaryExpression(cond) && cond.AsBinaryExpression().OperatorToken.Kind == ast.KindBarBarToken {
			var words []string
			var place *dataflowfacts.TrackedPlace
			readable := true
			var readEq func(side *ast.Node)
			readEq = func(side *ast.Node) {
				bare := Peeled(side)
				if ast.IsBinaryExpression(bare) && bare.AsBinaryExpression().OperatorToken.Kind == ast.KindBarBarToken {
					readEq(bare.AsBinaryExpression().Left)
					readEq(bare.AsBinaryExpression().Right)
					return
				}
				if !readable || !ast.IsBinaryExpression(bare) ||
					bare.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsEqualsEqualsToken {
					readable = false
					return
				}
				bin := bare.AsBinaryExpression()
				// literal is the string-literal side; tested is the OTHER
				// side, computed unconditionally the way the TS source's
				// `literal === bare.right ? bare.left : bare.right` does
				// (only literal can be null; tested is always a side)
				var literal *ast.Node
				tested := bin.Right
				if ast.IsStringLiteral(bin.Right) {
					literal, tested = bin.Right, bin.Left
				} else if ast.IsStringLiteral(bin.Left) {
					literal, tested = bin.Left, bin.Right
				}
				testedPlace := dataflowfacts.TrackedPlaceOfWith(c, tested, isTracked)
				if literal == nil || testedPlace == nil {
					readable = false
					return
				}
				if place == nil {
					place = testedPlace
				} else if place.Binding != testedPlace.Binding || joinPath(place.Path) != joinPath(testedPlace.Path) {
					readable = false
					return
				}
				words = append(words, literal.Text())
			}
			readEq(cond)
			if readable && place != nil && len(words) >= 2 {
				tuples := make([][]float64, len(words))
				for i, w := range words {
					tuples[i] = refinementsets.CodepointsOf(w)
				}
				return BranchNarrowings{
					WhenTrue:  []Narrowed{{Binding: place.Binding, Path: place.Path, HasWordSet: true, WordSet: tuples}},
					WhenFalse: []Narrowed{{Binding: place.Binding, Path: place.Path, HasWordSetExcluded: true, WordSetExcluded: tuples}},
				}
			}
		}
		if ast.IsBinaryExpression(cond) &&
			cond.AsBinaryExpression().OperatorToken.Kind == ast.KindBarBarToken &&
			NarrowKernel() != nil {
			bin := cond.AsBinaryExpression()
			place, hasAbsence := AbsenceTestPlace(c, bin.Left, isTracked)
			numericSide := bin.Right
			if !hasAbsence {
				place, hasAbsence = AbsenceTestPlace(c, bin.Right, isTracked)
				numericSide = bin.Left
			}
			if hasAbsence {
				tree := TreeOf(c, numericSide, *place, isTracked, sideBounds, 0)
				if SaysAnything(tree) {
					if answer, refused := narrowRefusable(tree); !refused && answer.WhenTrue != nil {
						crossChannel = append(crossChannel, Narrowed{
							Binding: place.Binding, Path: place.Path,
							Forms: answer.WhenTrue.Set.Forms, Refuting: !answer.WhenTrue.Strong,
							KeepAbsent: true,
						})
					}
				}
			}
		}
		if ast.IsBinaryExpression(cond) {
			op := cond.AsBinaryExpression().OperatorToken.Kind
			if op == ast.KindAmpersandAmpersandToken || op == ast.KindBarBarToken {
				bin := cond.AsBinaryExpression()
				left, leftOk := ResolveBoundCondition(c, bin.Left)
				right, rightOk := ResolveBoundCondition(c, bin.Right)
				// only a resolution that READS something contributes; a
				// side left alone reads as itself
				readSide := func(r ResolvedCondition, rOk bool) (BranchNarrowings, bool) {
					if !rOk {
						return BranchNarrowings{}, false
					}
					b := NarrowingsOf(c, r.Condition, isTracked, sideBounds, readElsewhere)
					if len(b.WhenTrue) == 0 && len(b.WhenFalse) == 0 {
						return BranchNarrowings{}, false
					}
					if r.Flipped {
						return BranchNarrowings{WhenTrue: b.WhenFalse, WhenFalse: b.WhenTrue}, true
					}
					return b, true
				}
				lb, lbOk := readSide(left, leftOk)
				rb, rbOk := readSide(right, rightOk)
				if lbOk || rbOk {
					readPlain := func(side *ast.Node) BranchNarrowings {
						return NarrowingsOf(c, side, isTracked, sideBounds, readElsewhere)
					}
					l := lb
					if !lbOk {
						l = readPlain(bin.Left)
					}
					r := rb
					if !rbOk {
						r = readPlain(bin.Right)
					}
					// held `&&` holds both sides; refuted `||` refutes both;
					// the other parities decide nothing about a single side
					if op == ast.KindAmpersandAmpersandToken {
						return BranchNarrowings{WhenTrue: append(append([]Narrowed{}, l.WhenTrue...), r.WhenTrue...)}
					}
					return BranchNarrowings{WhenFalse: append(append([]Narrowed{}, l.WhenFalse...), r.WhenFalse...)}
				}
			}
		}
	}
	structural := StructuralRaw(c, condition, isTracked)
	whenTrue := append(append([]Narrowed{}, structural.WhenTrue...), crossChannel...)
	whenFalse := append([]Narrowed{}, structural.WhenFalse...)
	// whether any place's tree posed a question at all — a condition
	// that tests a tracked binding and poses none is an UNREAD guard,
	// which the coverage report records by its form
	anySaid := false

	if NarrowKernel() != nil {
		var places []dataflowfacts.TrackedPlace
		CollectPlaces(c, condition, isTracked, &places)
		for _, place := range places {
			tree := TreeOf(c, condition, place, isTracked, sideBounds, 0)
			if !SaysAnything(tree) {
				continue
			}
			anySaid = true
			answer, refused := narrowRefusable(tree)
			if refused {
				// a refused question narrows nothing — never a guess
				continue
			}
			// the kernel's answer rides BESIDE any structural claim on
			// the same place, never behind it: for a CONJUNCTION the
			// kernel's set carries every conjunct ([0.5, 1.5].includes(x)
			// && x !== 1.5 answers {0.5, 1.5} ∖ {1.5}), so it is
			// strictly tighter than the structural pin alone — dropping
			// it kept the refuted member alive (A2.guard.ne's
			// neSubtractionInside false positive). Both narrowings
			// apply intersectively (apply_narrowing.go), so a genuinely
			// redundant restatement costs nothing.
			if answer.WhenTrue != nil {
				whenTrue = append(whenTrue, Narrowed{
					Binding: place.Binding, Path: place.Path,
					Forms: answer.WhenTrue.Set.Forms, Refuting: !answer.WhenTrue.Strong,
				})
			}
			if answer.WhenFalse != nil {
				whenFalse = append(whenFalse, Narrowed{
					Binding: place.Binding, Path: place.Path,
					Forms: answer.WhenFalse.Set.Forms, Refuting: !answer.WhenFalse.Strong,
				})
			}
		}
	}

	// nothing narrowed, nothing posed, yet the condition mentions a
	// tracked binding: this guard's form has no reading — the second
	// site where unnamed silence used to enter, now counted. BLOCKED
	// (see the file banner): RecordUnreadGuard needs
	// assignability/decline_reasons.ts's noteReason, outside this
	// directory's allowed import set — so this branch reports nothing
	// (no silent stub: MentionsTracked itself is ported and callable,
	// only the recording sink is not).
	_ = anySaid

	settle := func(ns []Narrowed) []Narrowed {
		out := append([]Narrowed{}, ns...)
		sort.SliceStable(out, func(i, j int) bool {
			return boolToInt(out[i].Refuting) < boolToInt(out[j].Refuting)
		})
		return out
	}
	return BranchNarrowings{WhenTrue: settle(whenTrue), WhenFalse: settle(whenFalse)}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinPath(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}
