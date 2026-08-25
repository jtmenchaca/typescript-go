// from evaluation/string_method_models.ts
//
// The replace-family exact readers: assembling String.prototype.replace
// / replaceAll's spec pieces (GetSubstitution's advance-by-searchLength
// scan), inlining a FUNCTION replacer's body once per match position,
// and the Go-regexp replacement-string escape this needs on top of Go's
// own `$name`/`$1` group syntax. Split from string_method_models.go per
// file-length discipline.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// replaceWithFunctionResult assembles String.prototype.replace /
// replaceAll over an exact receiver and an exact string pattern with a
// per-position replacement oracle. Positions are UTF-16 CODE-UNIT
// indices (StringIndexOf counts code units); replaceAll's match scan
// advances by max(1, _searchLength_) (sec-string.prototype.replaceall),
// and the pieces concatenate preserved-then-replacement with the tail
// appended (sec-string.prototype.replace steps 9-15). ("", false) when
// the oracle cannot pin a replacement.
func replaceWithFunctionResult(text string, pattern string, everyMatch bool, replacementAt func(matched string, position int) (string, bool)) (string, bool) {
	units := utf16UnitsOf(text)
	patternUnits := utf16UnitsOf(pattern)
	searchLength := len(patternUnits)
	advanceBy := searchLength
	if advanceBy < 1 {
		advanceBy = 1
	}
	// StringIndexOf over code units: the first start at or after `from`
	// where every pattern unit matches; an EMPTY pattern matches at
	// every index up to and including the length (sec-stringindexof)
	indexOfUnits := func(from int) int {
		for i := from; i+searchLength <= len(units); i++ {
			match := true
			for j := 0; j < searchLength; j++ {
				if units[i+j] != patternUnits[j] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}
	var positions []int
	position := indexOfUnits(0)
	if everyMatch {
		for position != -1 {
			positions = append(positions, position)
			position = indexOfUnits(position + advanceBy)
		}
	} else if position != -1 {
		positions = append(positions, position)
	}
	// no match: the receiver rides unchanged (sec-string.prototype.replace
	// returns _string_ when _position_ is ~not-found~)
	if len(positions) == 0 {
		return text, true
	}
	var built []uint16
	endOfLastMatch := 0
	for _, matchPosition := range positions {
		replacement, ok := replacementAt(pattern, matchPosition)
		if !ok {
			return "", false
		}
		built = append(built, units[endOfLastMatch:matchPosition]...)
		built = append(built, utf16UnitsOf(replacement)...)
		endOfLastMatch = matchPosition + searchLength
	}
	if endOfLastMatch < len(units) {
		built = append(built, units[endOfLastMatch:]...)
	}
	return utf16ToString(built), true
}

// inlineReplacerCall runs a function replacer's body once with the
// spec's three arguments bound — « _searchString_, 𝔽(_position_),
// _string_ » (sec-string.prototype.replace) — the same mechanics as
// InlineCallback (callback_models.go), widened to the three-argument
// shape. Whatever the body writes to outer names is forgotten: a
// replacer is still a write site.
func inlineReplacerCall(ctx *FlowContext, env Env, replacer *ast.Node, matched string, position int, whole string) abstractdomain.AbstractValue {
	callArguments := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues(refinementsets.CodepointsOf(matched), abstractdomain.PrimitiveString, abstractdomain.TrustSpec),
		abstractdomain.KnownValues([]float64{float64(position)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec),
		abstractdomain.KnownValues(refinementsets.CodepointsOf(whole), abstractdomain.PrimitiveString, abstractdomain.TrustSpec),
	}
	parameters := replacer.Parameters()
	bindings := map[string]abstractdomain.AbstractValue{}
	for i, argument := range callArguments {
		var parameter *ast.Node
		if len(parameters) > i {
			parameter = parameters[i]
		}
		BindParameter(ctx.P.Checker, parameter, argument, bindings)
	}
	callEnv := env.Clone()
	for name, known := range bindings {
		callEnv.Set(name, known)
	}
	body := replacer.Body()
	result := silence.Residue()
	if body != nil {
		if ast.IsBlock(body) {
			var sink []abstractdomain.AbstractValue
			sinkCtx := *ctx
			sinkCtx.ReturnSink = &sink
			AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			if len(sink) > 0 {
				joined := sink[0]
				for _, v := range sink[1:] {
					joined = abstractdomain.JoinKnown(joined, v)
				}
				result = joined
			}
		} else {
			result = evaluateExpression(ctx, callEnv, body)
		}
	}
	written := map[string]struct{}{}
	if body != nil {
		AssignedNames(ctx.P.Checker, body, written)
	}
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
	return result
}

// goReplacementOf mirrors a JS plain-string replacement against Go's
// regexp ReplaceAll, which reads `$name`/`$1` as its own group syntax
// — a literal `$` in a JS replacement string must escape to `$$`.
func goReplacementOf(replacement string) string {
	return strings.ReplaceAll(replacement, "$", "$$")
}
