// from evaluation/string_method_models.ts
//
// The regex-facing string reads: translating a JS regex source/flags
// pair to a Go RE2 regexp, and `.match` with a LITERAL pattern (or a
// const-bound identifier resolving to one) on an exact string receiver.
// Split from string_method_models.go per file-length discipline.
//
// RE2 (Go's regexp) has no lookahead/lookbehind — a JS regex literal
// using those constructs fails regexToGo's compile and this file's
// readers answer nil/residue for it rather than a silently wrong
// match, per PORT.md's regex-divergence rule.

package walk

import (
	"regexp"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// regexToGo translates a JS regex source/flags pair to a Go RE2
// regexp, or (nil, false) where RE2 cannot express it (lookaround, a
// few JS-only escapes) — the caller answers residue/nil rather than a
// silently wrong match.
func regexToGo(source string, flags string) (*regexp.Regexp, bool) {
	prefix := ""
	if strings.Contains(flags, "i") {
		prefix += "i"
	}
	if strings.Contains(flags, "s") {
		prefix += "s"
	}
	if strings.Contains(flags, "m") {
		prefix += "m"
	}
	pattern := source
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, false
	}
	return compiled, true
}

// readStringMatchWithConstRegex is readStringMatchWithConstRegex in
// the TS source: `.match` with a LITERAL pattern on an exact string —
// the result is exactly specified (String.prototype.match through
// RegExp semantics), so the host computes the exact row — the string-
// read rule again. The regex OBJECT stays unmodeled; only the spec-
// pinned result of this call is claimed. No match is null, which
// leaves the model as the absent marker. The pattern may ride through
// a const name bound to a regex literal — but a sticky non-global
// regex reads its own lastIndex, which is process state, so that
// shape stays out.
func readStringMatchWithConstRegex(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver := site.Ctx, site.Env, site.E, site.Receiver
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	regexLiteralOf := func(argument *ast.Node) *ast.Node {
		if ast.IsRegularExpressionLiteral(argument) {
			return argument
		}
		if !ast.IsIdentifier(argument) {
			return nil
		}
		symbol := symbolAt(ctx.P.Checker, argument)
		if symbol == nil || len(symbol.Declarations) != 1 {
			return nil
		}
		declaration := symbol.Declarations[0]
		if !ast.IsVariableDeclaration(declaration) {
			return nil
		}
		varDecl := declaration.AsVariableDeclaration()
		if varDecl.Initializer == nil || !ast.IsRegularExpressionLiteral(varDecl.Initializer) {
			return nil
		}
		declList := declaration.Parent
		if declList == nil || !ast.IsVariableDeclarationList(declList) ||
			declList.Flags&ast.NodeFlagsConst == 0 {
			return nil
		}
		text := varDecl.Initializer.Text()
		flags := text[strings.LastIndex(text, "/")+1:]
		if strings.Contains(flags, "y") && !strings.Contains(flags, "g") {
			return nil
		}
		return varDecl.Initializer
	}
	var pattern *ast.Node
	if site.Method == "match" && len(arguments) == 1 {
		pattern = regexLiteralOf(arguments[0])
	}
	if pattern == nil {
		return nil
	}
	receiverKnown := receiver
	if site.HasTrackedName {
		if held, ok := env.Get(site.TrackedName); ok {
			receiverKnown = held
		} else {
			receiverKnown = silence.Residue()
		}
	}
	if !(receiverKnown.Kind == abstractdomain.KindValues && receiverKnown.KindTag == abstractdomain.PrimitiveString) {
		return nil
	}
	text := stringOf(receiverKnown.Values)
	literal := pattern.Text()
	lastSlash := strings.LastIndex(literal, "/")
	source := literal[1:lastSlash]
	flags := literal[lastSlash+1:]
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiverKnown), abstractdomain.TrustSpec)
	compiled, ok := regexToGo(source, flags)
	if !ok {
		out := silence.Residue()
		return &out
	}
	matched := compiled.FindStringSubmatch(text)
	if matched == nil {
		out := abstractdomain.AtTrustLevel(abstractdomain.Undef, grade)
		return &out
	}
	// an unmatched capture group is undefined — the absent slot. Go's
	// regexp reports an unmatched group as "" indistinguishably from an
	// empty match — FindStringSubmatchIndex tells them apart by index.
	indices := compiled.FindStringSubmatchIndex(text)
	items := make([]abstractdomain.AbstractValue, len(matched))
	for i, part := range matched {
		if i > 0 && indices[2*i] == -1 {
			items[i] = abstractdomain.Undef
			continue
		}
		items[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(part), abstractdomain.PrimitiveString, grade)
	}
	list := abstractdomain.KnownList(items, grade)
	// the same values, named a second time under the pattern's group
	// names on `.groups` (sec-regexpbuiltinexec step 34). The spans
	// walk answers nothing on a pattern it cannot read; the list then
	// carries no `groups` key and the read falls through as before.
	if spans, ok := captureGroupSourceSpans(source); ok && len(spans)+1 == len(items) {
		list = withNamedGroups(list, source, spans, items[1:], grade)
	}
	out := list
	return &out
}
