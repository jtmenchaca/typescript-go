// from assignability/sequence_measures.ts
//
// Sequence measures and the `**` alert: an exact tuple's sum or
// order against what the position states, a syntactically detected
// power that should name its repair, and the codepoint-door test
// that keeps a scalar union of characters from looking like a
// cross-sort refutation.

package walk

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// formatJSNumberLocal renders a float64 the way JavaScript's
// String(x) would — the ECMA Number::toString reading (jsnum.Number
// already implements it; refinementsets.FormatNumber is the wrong
// tool here since it spells ±∞ with the algebra's own symbols, not
// JS's `Infinity`/`-Infinity`).
func formatJSNumberLocal(x float64) string {
	return jsnum.Number(x).String()
}

// jsonQuote mirrors JSON.stringify on a single string.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// MeasureReported is measureReported in the TS source: a stated
// sequence measure checked against the value: an exact tuple
// computes the measure — a mismatch refutes — a value wearing the
// same parse-checked measure passes by identity, and anything else
// keeps the alert. True when a diagnostic was reported.
func MeasureReported(
	ctx *FlowContext,
	stated *annotations.Measures,
	elements []float64,
	elementsOk bool,
	worn *abstractdomain.Measures,
	node *ast.Node,
	what string,
	spelledValue string,
) bool {
	if stated == nil {
		return false
	}
	statedSum := stated.Sum
	hasStatedSum := stated.HasSum
	statedSorted := stated.Sorted
	sumCarried := !hasStatedSum || (worn != nil && worn.HasSum && worn.Sum == statedSum)
	sortCarried := !statedSorted || (worn != nil && worn.Sorted)
	if sumCarried && sortCarried {
		return false
	}
	if !elementsOk {
		var measureWords string
		if hasStatedSum {
			measureWords = "a sum of exactly " + formatJSNumberLocal(statedSum)
		}
		if statedSorted {
			if measureWords != "" {
				measureWords += " and "
			}
			measureWords += "non-decreasing element order"
		}
		ctx.Report(assignability.At(
			node,
			7002,
			assignability.AlertText+" The position states "+measureWords+", which is not verified here.",
		))
		return true
	}
	if hasStatedSum {
		// left-to-right machine addition — the same fold the runtime
		// reduce computes
		var total float64
		for _, v := range elements {
			total += v
		}
		if total != statedSum {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of type '"+spelledValue+"' is not assignable — its "+
					"elements sum to "+formatJSNumberLocal(total)+", and the position states a sum "+
					"of exactly "+formatJSNumberLocal(statedSum),
			))
			return true
		}
	}
	if statedSorted {
		drop := -1
		for i, v := range elements {
			if i > 0 && v < elements[i-1] {
				drop = i
				break
			}
		}
		if drop >= 0 {
			ctx.Report(assignability.At(
				node,
				7001,
				what+" of type '"+spelledValue+"' is not assignable — "+
					"element "+strconv.Itoa(drop)+" is smaller than the one before it, and "+
					"the position states non-decreasing order",
			))
			return true
		}
	}
	return false
}

// ContainsPow is containsPow in the TS source: `**` under a checked
// position — the unknown traces to the refusal ruling, and the
// alert should say so — detected syntactically, so the message
// fires exactly where the ripple reaches a judgment.
func ContainsPow(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind == ast.KindAsteriskAsteriskToken ||
			bin.OperatorToken.Kind == ast.KindAsteriskAsteriskEqualsToken {
			return true
		}
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsPow(child)
		}
		return false
	})
	return found
}

var whitespaceRunRe = regexp.MustCompile(`\s+`)

// PowAlert is powAlert in the TS source: the `**` alert, site-aware
// — where the stated set spells a liftable guard, the message shows
// the exact repair: their expression bound to `r`, the comparison
// that proves the set.
func PowAlert(node *ast.Node, target annotations.DeclaredRefinement) string {
	if target.Kind != annotations.DeclaredSet {
		return assignability.PowAlertText
	}
	guard, ok := refinementsets.FormatAsGuardCode("r", *target.Set)
	if !ok {
		return assignability.PowAlertText
	}
	expression := whitespaceRunRe.ReplaceAllString(sourceTextOf(node), " ")
	return assignability.PowAlertStem + ": `const r = " + expression + "; " +
		"if (" + guard + ")` proves `" + StatedSetWords(*target.Set, target.Word) + "`."
}

// GuardFixResult is the repair a diagnostic can carry — the TS
// source's inline `{ title, newText, insertAt }` object.
type GuardFixResult struct {
	Title    string
	NewText  string
	InsertAt int
}

// GuardFix is guardFix in the TS source: the repair an alert can
// carry — where the checked position is a plain name and its stated
// set spells a guard, the fix inserts a throwing check before the
// enclosing statement — the comparison narrows the walk (present,
// real, and inside the set), so the alert clears on the re-check.
func GuardFix(node *ast.Node, target annotations.DeclaredRefinement) (GuardFixResult, bool) {
	if target.Kind != annotations.DeclaredSet {
		return GuardFixResult{}, false
	}
	if !ast.IsIdentifier(node) {
		return GuardFixResult{}, false
	}
	name := node.Text()
	guard, ok := refinementsets.FormatAsGuardCode(name, *target.Set)
	if !ok {
		return GuardFixResult{}, false
	}
	statement := node
	for !ast.IsStatement(statement) {
		if statement.Parent == nil {
			return GuardFixResult{}, false
		}
		statement = statement.Parent
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	start := int(scanner.GetTokenPosOfNode(statement, sourceFile, false))
	lineStarts := scanner.GetECMALineStarts(sourceFile)
	line := scanner.GetECMALineOfPosition(sourceFile, start)
	lineStart := int(lineStarts[line])
	text := sourceFile.Text()
	indent := text[lineStart:start]
	message := name + " must be " + StatedSetWords(*target.Set, target.Word)
	return GuardFixResult{
		Title:    "Guard " + name + " — if (!(" + guard + ")) throw",
		NewText:  "if (!(" + guard + ")) throw new Error(" + jsonQuote(message) + ");\n" + indent,
		InsertAt: start,
	}, true
}

// StatesSequence is statesSequence in the TS source: whether a set
// DEMONSTRABLY states a sequence — a string or an array shape: a
// star, a concatenation, a repetition, or the empty tuple sits among
// its forms. A positive test: relation-shaped or empty sets answer
// false and keep their own paths. The one implementation lives in
// refinementsets (objectgraphs' value encoder asks it too); this name
// stays for the walk's many call sites.
func StatesSequence(set refinementsets.RefinedSet) bool {
	return refinementsets.StatesSequence(set)
}

// CodepointScalar is codepointScalar in the TS source: one admitted
// scalar that could be a one-character string's codepoint.
func CodepointScalar(v float64) bool {
	return v == float64(int64(v)) && v >= 0 &&
		(v <= 0xD7FF || (v >= 0xE000 && v <= 0x10FFFF))
}

// WithinCodepointDoor is withinCodepointDoor in the TS source:
// whether EVERY value a scalar set admits sits inside the codepoint
// alphabet — the sorted-door test: such a set is indistinguishable
// from a union of one-character strings. Two spellings pass:
// enumerated codepoints (oneOf), and INTEGER-constrained windows
// wholly inside one side of the surrogate gap — which is exactly
// what `z.string().length(1)` compiles to. Windows without the
// integer form answer false (they admit non-codepoint reals), and
// the window test is conservative: `above`/`below` are widened to
// their closed bounds, so the door never opens wrongly.
func WithinCodepointDoor(set refinementsets.RefinedSet, integerInherited bool) bool {
	if len(set.Forms) == 0 {
		return false
	}
	integer := integerInherited
	if !integer {
		for _, f := range set.Forms {
			if f.Form == refinementsets.FormInteger {
				integer = true
				break
			}
		}
	}
	lo := math.Inf(-1)
	hi := math.Inf(1)
	content := false
	for _, f := range set.Forms {
		switch f.Form {
		case refinementsets.FormInteger:
			continue
		case refinementsets.FormOneOf:
			for _, w := range f.W {
				if !CodepointScalar(w) {
					return false
				}
			}
			content = true
		case refinementsets.FormAtLeast, refinementsets.FormAbove:
			lo = math.Max(lo, f.A)
		case refinementsets.FormAtMost, refinementsets.FormBelow:
			hi = math.Min(hi, f.A)
		case refinementsets.FormUnion:
			if !WithinCodepointDoor(*f.A_, integer) || !WithinCodepointDoor(*f.B, integer) {
				return false
			}
			content = true
		default:
			return false
		}
	}
	if lo != math.Inf(-1) || hi != math.Inf(1) {
		if !integer {
			return false
		}
		if lo > hi {
			return false
		}
		inLow := lo >= 0 && hi <= 0xD7FF
		inHigh := lo >= 0xE000 && hi <= 0x10FFFF
		if !inLow && !inHigh {
			return false
		}
		content = true
	}
	return content
}
