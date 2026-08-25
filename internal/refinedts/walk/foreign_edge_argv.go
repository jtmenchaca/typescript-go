// Reads the runner word (argv[0]) and the script position (argv[1] or
// the compiled-binary row's own argv[0]) a recognized foreign call
// names: written literals, const-resolved identifiers, composed
// (concatenated/templated) constant strings, and a parameter whose
// call sites all pin the same literal.

package walk

import (
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// scriptPathLawTwoSentence is the one sentence a script-path element
// that is not a written string literal, and does not resolve to one
// through a const binding, owes — law 2: name exactly what would make
// it resolvable.
const scriptPathLawTwoSentence = "the script path is computed; spell it as a written string literal"

// runnerAndScriptArgvOf reads argv[0] (the runner word) and argv[1]
// (the argv array naming the script) together, since the shape of the
// second depends on the first: a plain interpreter (`python3`,
// `python`) takes either a single-element argv holding the script
// alone, or a TWO-element argv holding the script AND one crossing
// value (dataElement — the argv-scalar leg, CROSS-LANGUAGE-EDGE.md's
// data-leg list); `uv` takes `["run", <script>]` or `["run",
// <interpreter>, <script>]`, the runner word plus its own arguments in
// ONE array rather than two separate positions, and models no data
// element (a third argv slot beyond `uv`'s own runner-word/interpreter
// pair is a different, unmodeled shape).
//
// Answers (runnerWord, script, dataElement, true, "", nil) when a
// script name was read — whether written in place or resolved through
// a const — dataElement nil unless the python arm's argv carried
// exactly two elements. Answers (_, _, nil, false, "", nil) where
// argv[0] does not even read as a recognized runner word (nothing
// owed: the call may not be a Python edge at all), or where the argv
// array's own length is neither the plain nor the two-element python
// shape (three-or-more elements still models nothing: a target taking
// arguments this edge does not model is a different shape from a
// computed single element, so it stays a plain non-match). A script
// ELEMENT that is present but not a plain written literal, and does
// not resolve to one, owes the law-2 sentence rather than silence —
// the runner word itself is already known by that point, so the call
// IS this edge, only unfollowable.
//
// A COMPILED BINARY (argv[0] itself IS the target — no interpreter
// word at all) is a SEPARATE shape this function does not read:
// compiledBinaryArgvOf, below, is its own recognizer, tried by
// execFileSyncEdgeOf ahead of this one — see that function's own doc.
func runnerAndScriptArgvOf(ctx *FlowContext, runnerArgument *ast.Node, argvArgument *ast.Node) (runnerWord string, script string, dataElement *ast.Node, ok bool, sentence string, sentenceNode *ast.Node) {
	interpreter, interpreterOk := runnerWordOf(ctx, runnerArgument)
	if !interpreterOk {
		// the runner word itself is not a written literal, a const bound
		// to one, or a const-composed string (a variable holding a
		// computed value, a parameter) — the reader cannot yet see this
		// is python at all, so nothing is owed
		return "", "", nil, false, "", nil
	}
	array := Unwrapped(argvArgument)
	if array == nil || !ast.IsArrayLiteralExpression(array) {
		return "", "", nil, false, "", nil
	}
	elements := array.AsArrayLiteralExpression().Elements.Nodes
	switch {
	case pythonSpellings[interpreter]:
		if len(elements) < 1 || len(elements) > 2 {
			// more elements mean the target takes arguments this edge does
			// not model; that is a DIFFERENT shape from a computed single
			// element, so it stays a plain non-match (no sentence)
			return "", "", nil, false, "", nil
		}
		script, scriptOk, scriptSentence, scriptNode := scriptElementOf(ctx, elements[0])
		if scriptSentence != "" {
			return "", "", nil, false, scriptSentence, scriptNode
		}
		if !scriptOk {
			return "", "", nil, false, "", nil
		}
		var data *ast.Node
		if len(elements) == 2 {
			data = Unwrapped(elements[1])
		}
		return interpreter, script, data, true, "", nil
	case interpreter == "uv":
		// `uv run <script>` or `uv run <interpreter> <script>`: the runner
		// word's own argument list rides inside argv[1], not split across
		// two call arguments the way a plain interpreter spells it. No
		// data element is modeled for uv here — a fourth slot beyond the
		// runner-word/interpreter pair is a different, unmodeled shape.
		if len(elements) < 2 || len(elements) > 3 {
			return "", "", nil, false, "", nil
		}
		runWord, runOk := stringLiteralText(elements[0])
		if !runOk || runWord != "run" {
			return "", "", nil, false, "", nil
		}
		scriptElement := elements[len(elements)-1]
		if len(elements) == 3 {
			// the middle element names which interpreter uv runs the
			// script with; anything other than a recognized python
			// spelling there is a different program, not this edge
			middle, middleOk := stringLiteralText(elements[1])
			if !middleOk || !pythonSpellings[middle] {
				return "", "", nil, false, "", nil
			}
		}
		script, scriptOk, scriptSentence, scriptNode := scriptElementOf(ctx, scriptElement)
		if scriptSentence != "" {
			return "", "", nil, false, scriptSentence, scriptNode
		}
		if !scriptOk {
			return "", "", nil, false, "", nil
		}
		return "uv run", script, nil, true, "", nil
	}
	return "", "", nil, false, "", nil
}

// runnerWordOf reads argv[0] (the runner word) the same three ways
// scriptElementOf reads a script path: a written string literal (or
// no-substitution template) directly, an IDENTIFIER resolved through
// resolvedConstStringLiteral to its const initializer's own literal,
// or a COMPOSED string expression folded through foldedConstStringOf.
// Unlike scriptElementOf, an unresolved runner word owes no sentence —
// runnerAndScriptArgvOf's own doc already states why: the reader
// cannot yet tell this call is even a Python edge, so a variable
// runner word that resolves to nothing readable stays silent, not
// recognized-and-declined.
func runnerWordOf(ctx *FlowContext, element *ast.Node) (string, bool) {
	node := Unwrapped(element)
	if node == nil {
		return "", false
	}
	if text, literalOk := stringLiteralText(node); literalOk {
		return text, true
	}
	if ast.IsIdentifier(node) {
		if text, resolvedOk := resolvedConstStringLiteral(ctx, node); resolvedOk {
			return text, true
		}
	}
	return foldedConstStringOf(ctx, node)
}

// scriptElementOf reads one argv element as the script's own path: a
// written string literal (or no-substitution template) directly, an
// IDENTIFIER resolved through resolvedConstStringLiteral to its const
// initializer's own literal, or — past those two — a COMPOSED string
// expression (const-string concatenation, a template substitution
// naming a const) folded exactly through foldedConstStringOf. Anything
// past all three (a parameter reference, a call result, a template
// substitution that is itself not const-foldable) is a script path the
// checker can SEE but cannot yet name: recognized, not silent, carrying
// the law-2 sentence naming exactly what would make it resolvable.
func scriptElementOf(ctx *FlowContext, element *ast.Node) (script string, ok bool, sentence string, sentenceNode *ast.Node) {
	node := Unwrapped(element)
	if node == nil {
		return "", false, "", nil
	}
	if text, literalOk := stringLiteralText(node); literalOk {
		return text, true, "", nil
	}
	if ast.IsIdentifier(node) {
		if text, resolvedOk := resolvedConstStringLiteral(ctx, node); resolvedOk {
			return text, true, "", nil
		}
		// a PARAMETER-held identifier owes no invariant of its own — the
		// declaration itself does not fix a value — but every call site
		// that supplies the enclosing function's argument DOES, exactly
		// as declaredJoinUncached already reads a non-exported function's
		// callers for its ABSTRACT parameter values. Tried past the const
		// follow, before falling to the law-2 decline: a parameter whose
		// callers all pin the SAME literal path resolves here; a
		// parameter with no callers in view, or callers that disagree,
		// still declines below.
		if c := checkerOf(ctx); c != nil {
			if symbol := symbolAt(c, node); symbol != nil && symbol.ValueDeclaration != nil &&
				ast.IsParameterDeclaration(symbol.ValueDeclaration) {
				if text, resolvedOk := scriptPathFromParameterCallSites(ctx, node, symbol.ValueDeclaration); resolvedOk {
					return text, true, "", nil
				}
			}
		}
	}
	if text, foldedOk := foldedConstStringOf(ctx, node); foldedOk {
		return text, true, "", nil
	}
	// past this point the reader knows there IS a script argv element —
	// it is simply not one the checker can read a name from
	return "", false, scriptPathLawTwoSentence, node
}

// foldedConstStringOf is a purely SYNTACTIC constant-string fold — no
// runtime Env is consulted, only the same identifier→symbol→
// ValueDeclaration→const-initializer follow resolvedConstStringLiteral
// already performs, recursed over the two AST shapes a computed path
// literal-composed-of-literals actually takes:
//
//   - a `+` BinaryExpression whose two sides both fold (constant-string
//     concatenation, e.g. `directory + "level_ok.py"` where `directory`
//     is a same-file or cross-module const) — folds to the
//     concatenation of both sides' own folded text;
//   - a TemplateExpression (with or without substitutions) whose every
//     span expression folds — folds to the head text plus each span's
//     folded text plus the following literal text, in source order
//     (mirrors evaluateTemplate's own exact-template reading, but
//     syntactically rather than through the walk's runtime Env, since
//     this reader runs at RECOGNITION time before an edge — and
//     therefore before any Env — exists).
//
// A bare literal or a directly-resolvable identifier is read by the two
// checks scriptElementOf/argvLiteralTextOf already perform before
// calling this; this function exists for the COMPOSED shapes past
// those two, and answers ok=false for anything else (a parameter, a
// call result, a template substitution that is not itself foldable) —
// the genuinely unresolvable case stays declined with the law-2
// sentence, unchanged.
func foldedConstStringOf(ctx *FlowContext, node *ast.Node) (string, bool) {
	node = Unwrapped(node)
	if node == nil {
		return "", false
	}
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return "", false
		}
		left, leftOk := foldedConstStringLeafOf(ctx, bin.Left)
		if !leftOk {
			return "", false
		}
		right, rightOk := foldedConstStringLeafOf(ctx, bin.Right)
		if !rightOk {
			return "", false
		}
		return left + right, true
	}
	if ast.IsTemplateExpression(node) {
		template := node.AsTemplateExpression()
		text := template.Head.Text()
		for _, spanNode := range template.TemplateSpans.Nodes {
			span := spanNode.AsTemplateSpan()
			part, partOk := foldedConstStringLeafOf(ctx, span.Expression)
			if !partOk {
				return "", false
			}
			text += part
			text += span.Literal.Text()
		}
		return text, true
	}
	return "", false
}

// foldedConstStringLeafOf reads one OPERAND of a fold (a `+` side, a
// template span) as a constant string: a written literal directly, an
// identifier resolved through resolvedConstStringLiteral, or — recursed
// — another composed expression through foldedConstStringOf itself
// (so `a + b + "c"` and a template nesting a concatenation both fold,
// not just the single-level shapes the two fixture rows exercise).
func foldedConstStringLeafOf(ctx *FlowContext, expression *ast.Node) (string, bool) {
	node := Unwrapped(expression)
	if node == nil {
		return "", false
	}
	if text, literalOk := stringLiteralText(node); literalOk {
		return text, true
	}
	if ast.IsIdentifier(node) {
		if text, resolvedOk := resolvedConstStringLiteral(ctx, node); resolvedOk {
			return text, true
		}
	}
	return foldedConstStringOf(ctx, node)
}

// resolvedConstStringLiteral follows an identifier BACK to a `const`
// binding's own initializer and reads that initializer as a written
// string literal — the same "identifier → symbol → ValueDeclaration →
// const VariableDeclaration → initializer" resolution
// accumulationLengthNodeOf (relational_accumulation.go) already performs
// for a `.length` denominator, applied here to a literal string instead.
// A `let` binding, a parameter, or an initializer that is itself not a
// written literal all answer false — only a value FIXED by the
// declaration survives the follow.
func resolvedConstStringLiteral(ctx *FlowContext, identifier *ast.Node) (string, bool) {
	c := checkerOf(ctx)
	if c == nil {
		return "", false
	}
	symbol := symbolAt(c, identifier)
	if symbol == nil || symbol.ValueDeclaration == nil || !ast.IsVariableDeclaration(symbol.ValueDeclaration) {
		return "", false
	}
	declaration := symbol.ValueDeclaration
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return "", false
	}
	initializer := Unwrapped(declaration.AsVariableDeclaration().Initializer)
	if initializer == nil {
		return "", false
	}
	return stringLiteralText(initializer)
}

// scriptPathFromParameterCallSites is scriptElementOf's PARAMETER
// branch: an argv element that is an identifier whose ValueDeclaration
// is a ParameterDeclaration owes no invariant of its own
// (resolvedConstStringLiteral's own gate — a parameter's value is not
// fixed by its declaration the way a const's is) but IS pinned by
// every call site that supplies it, exactly as declaredJoinUncached
// (call_site_bindings.go) already reads a non-exported function's
// callers to join its parameters' ABSTRACT values. This asks the same
// question one register narrower: does the bound ARGUMENT EXPRESSION
// at each call site fold to a written string literal — through the
// same stringLiteralText / resolvedConstStringLiteral / foldedConstStringOf
// triad scriptElementOf itself already tries on the parameter's OWN
// identifier, applied instead to the actual value each caller passes.
//
// The gate is declaredJoinUncached's, unchanged: the enclosing function
// must be a non-exported FunctionDeclaration, and every use of its name
// in the entry file must be a direct call — an escape (a read, a
// reassignment, an argument to an unmodeled call, a callback argument)
// means callers are not all in view, so nothing is pinned. Answers
// ok=false wherever that gate fails, wherever there are zero call
// sites (nothing pins a parameter no one supplies), or wherever any one
// call site's own bound argument does not fold to a literal — a MIXED
// or PARTIALLY-folding caller set is not a script path this function
// can name a single answer for, so it declines the same as an
// unresolvable identifier would, rather than guessing from a subset of
// its callers.
func scriptPathFromParameterCallSites(ctx *FlowContext, identifier *ast.Node, parameter *ast.Node) (string, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return "", false
	}
	fn := EnclosingBlockBody(parameter)
	if fn == nil || !ast.IsFunctionDeclaration(fn) {
		return "", false
	}
	name := fn.Name()
	if name == nil {
		return "", false
	}
	if ast.HasSyntacticModifier(fn, ast.ModifierFlagsExport) {
		return "", false
	}
	// the parameter's own ordinal among the function's parameter list —
	// the slot each call site's arguments list is read at
	slot := -1
	for i, p := range fn.Parameters() {
		if p == parameter {
			slot = i
			break
		}
	}
	if slot < 0 {
		return "", false
	}
	target := symbolAt(ctx.P.Checker, name)
	if target == nil {
		return "", false
	}
	var directCalls []*ast.Node
	for _, node := range identifierUsesOf(ctx.P, name.Text()) {
		if node == name || symbolAt(ctx.P.Checker, node) != target {
			continue
		}
		parent := node.Parent
		if ast.IsCallExpression(parent) && parent.AsCallExpression().Expression == node {
			directCalls = append(directCalls, parent)
			continue
		}
		// any other use (a read, a reassignment, a non-call argument) means
		// callers are not all in view — the same escape declaredJoinUncached
		// refuses on
		return "", false
	}
	if len(directCalls) == 0 {
		return "", false
	}
	var resolved string
	for i, call := range directCalls {
		args, hasArgs := callArguments(call)
		if !hasArgs || slot >= len(args) {
			return "", false
		}
		text, ok := foldedConstStringLeafOf(ctx, args[slot])
		if !ok {
			return "", false
		}
		if i == 0 {
			resolved = text
			continue
		}
		if text != resolved {
			// two callers pin two different paths — no single script name
			// serves both, so the identifier stays unresolved
			return "", false
		}
	}
	return resolved, true
}

// compiledBinaryArgvOf reads argv[0] as a COMPILED BINARY's own path —
// the shape `execFileSync("./targets/cpp_level", [], {...})` takes,
// where the first call argument IS the target rather than an
// interpreter word. Recognized when the element resolves to a written
// (or const-resolved) string whose text is PATH-SHAPED — a leading
// "./", "../", or "/" — mirroring the Rust twin's own
// compiled_binary_path_of (foreign_edge.rs): a bare word with no
// leading path marker is not a recognized runner word either at this
// position, so it stays a plain non-match rather than a guess.
//
// Answers (path, true) on a match. Answers ("", false) where the
// element does not even resolve to a string, resolves to a recognized
// python/uv runner word instead, or resolves to a string that is not
// path-shaped — nothing owed: the call may be a plain runner-word row
// runnerAndScriptArgvOf already models (e.g. "python3"), and a bare
// word with no path marker is not distinguishable from an unmodeled
// interpreter spelling, so it stays a plain non-match rather than a
// guess.
func compiledBinaryArgvOf(ctx *FlowContext, element *ast.Node) (path string, ok bool) {
	text, resolvedOk := runnerWordOf(ctx, element)
	if !resolvedOk {
		return "", false
	}
	if pythonSpellings[text] || text == "uv" {
		// a recognized interpreter word is never also read as a compiled
		// binary's own path — the two shapes are mutually exclusive
		return "", false
	}
	if !isCompiledBinaryPathShaped(text) {
		return "", false
	}
	return text, true
}

// isCompiledBinaryPathShaped is whether text carries one of the three
// path markers a compiled-binary invocation's own argv[0] states — a
// leading "./", "../", or "/" — the same three prefixes the Rust
// twin's compiled_binary_path_of checks. A bare word with none of
// these (an unrecognized runner word, e.g. a typo'd interpreter) is
// NOT read as a binary path: the checker cannot tell "an interpreter
// this edge does not model" from "a compiled binary" by spelling
// alone once the path markers are absent, so it stays unrecognized
// rather than guessing.
func isCompiledBinaryPathShaped(text string) bool {
	return strings.HasPrefix(text, "./") || strings.HasPrefix(text, "../") || strings.HasPrefix(text, "/")
}

// resolveCompiledBinaryPath is resolveForeignScriptPath's own twin for
// a compiled binary: no extension premise (a compiled binary carries
// no ".py"/".ts" suffix this edge could check), only the relative-path
// resolution against the SOURCE FILE's own directory — the same
// reading every other recognized argv element in this file applies,
// so a binary path spelled relative to the checked file resolves the
// same way a script path does.
func resolveCompiledBinaryPath(call *ast.Node, binaryPath string) (resolvedPath string, sentence string) {
	sourceFile := ast.GetSourceFileOfNode(call)
	if sourceFile == nil {
		return "", ""
	}
	resolvedPath = binaryPath
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(filepath.Dir(sourceFile.FileName()), binaryPath)
	}
	return resolvedPath, ""
}

// resolveForeignScriptPath discharges the two premises common to every
// invocation shape once a script NAME is in hand: the name must end in
// `.py` (the checker only reads a fact for Python source), and a
// relative name resolves against the SOURCE FILE's own directory (the
// cwd of the eventual run is deployment, and resolving against it would
// make the checker's answer depend on where it was invoked). Answers
// ("", "") for a call whose source file cannot be found — the same
// silent decline the original inline reading gave.
func resolveForeignScriptPath(call *ast.Node, runnerWord string, script string) (resolvedPath string, sentence string) {
	if filepath.Ext(script) != ".py" {
		return "", "this call runs " + runnerWord + " on " + script +
			", which is not a .py file — the checker models the edge only where the argv " +
			"names Python source it can read a fact for"
	}
	sourceFile := ast.GetSourceFileOfNode(call)
	if sourceFile == nil {
		return "", ""
	}
	resolvedPath = script
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(filepath.Dir(sourceFile.FileName()), script)
	}
	return resolvedPath, ""
}
