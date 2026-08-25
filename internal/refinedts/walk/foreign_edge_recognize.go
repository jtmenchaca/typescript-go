// Recognition of one foreign-edge statement: `execFileSync`/`spawnSync`/
// `execSync`, the callee dispatch that routes to each, the file-carried
// data leg's own preceding-`writeFileSync` scan, and the const-bound
// binding shape every recognized call shares.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── recognition (Q1 side) ───────────────────────────────────────── */

// foreignEdgeOf reads statements[index] as one of the recognized
// invocation-function shapes: `const <name> = execFileSync(<runner>,
// <argv>, {input: JSON.stringify(<payload>), encoding: <string
// encoding>})`, the same call through `spawnSync` (whose result is an
// object, read at `.stdout`), or `execSync` given a single written
// shell-command string. `spawn` (async) is recognized on the same
// argv-naming basis; its return leg reads the following statements for
// the accumulate-then-parse `.on()` pair (spawnAsyncEdgeOf), and answers
// its own sentence naming whatever construct still blocks it rather than
// falling through to "not this shape". execFileSync/spawnSync also take
// statements/index — the file-carried shape's own look-BACK (a preceding
// writeFileSync statement) needs them exactly as spawn's look-ahead does.
func foreignEdgeOf(ctx *FlowContext, statements []*ast.Node, index int) (edge *ForeignEdge, recognized bool, declineSentence string, declineNode *ast.Node, targetPath string) {
	statement := statements[index]
	name, call, ok := constBoundCallOf(statement)
	if !ok {
		return nil, false, "", nil, ""
	}
	callee := calleeOf(call)
	switch {
	case resolvesToChildProcessMember(ctx, callee, "execFileSync"):
		return execFileSyncEdgeOf(ctx, call, name, statements, index)
	case resolvesToChildProcessMember(ctx, callee, "spawnSync"):
		return spawnSyncEdgeOf(ctx, call, name, statements, index)
	case resolvesToChildProcessMember(ctx, callee, "execSync"):
		return execSyncEdgeOf(call, name)
	case resolvesToChildProcessMember(ctx, callee, "spawn"):
		return spawnAsyncEdgeOf(ctx, call, name, statements, index)
	}
	// a local helper of the same name is not the Node built-in, and
	// nothing here knows what it runs — an ordinary call
	return nil, false, "", nil, ""
}

// execFileSyncEdgeOf reads `execFileSync(<runner>, <argv>, {input:
// JSON.stringify(<payload>), encoding: <string encoding>})` — the
// sync exec whose bound name is itself the stdout string. The argv
// itself may ALSO carry a two-element [script, dataElement] shape (the
// argv-value leg); a call sending a value on BOTH stdin and argv[1] at
// once is now a RECOGNIZED mixed edge (Payload and ArgvValue both set) —
// checkOutboundLeg's mixed branch is the one place that judges whether
// the target's own surface actually reads both legs, per-leg fit
// discharged only once the channel match itself holds.
//
// A call with NO options-object input at all still recognizes the
// file-carried shape: fileCrossingOf looks at the PRECEDING statement
// for a `writeFileSync(<path>, JSON.stringify(<payload>))` write whose
// path matches one of this call's own argv elements.
func execFileSyncEdgeOf(
	ctx *FlowContext, call *ast.Node, name string, statements []*ast.Node, index int,
) (*ForeignEdge, bool, string, *ast.Node, string) {
	args, _ := callArguments(call)
	if len(args) < 3 {
		return nil, false, "", nil, ""
	}
	// the COMPILED-BINARY row: argv[0] itself is the target, no
	// interpreter word — tried AHEAD of the python/uv runner read, since
	// a compiled-binary path is never also a recognized runner word (the
	// two shapes are mutually exclusive by construction, and this order
	// mirrors the Rust twin's own argv-length dispatch: the one-element
	// runner rows are tried before the bare-binary fallback).
	if binaryPath, isBinary := compiledBinaryArgvOf(ctx, args[0]); isBinary {
		resolvedPath, pathSentence := resolveCompiledBinaryPath(call, binaryPath)
		if pathSentence != "" {
			return nil, false, pathSentence, call, resolvedPath
		}
		if resolvedPath == "" {
			return nil, false, "", nil, ""
		}
		payload, encodingOk, optionsSentence := execFileSyncOptionsOf(args[2])
		if optionsSentence != "" {
			return nil, false, optionsSentence, args[2], resolvedPath
		}
		if !encodingOk {
			return nil, false, "this call runs the compiled binary " + binaryPath + " without a string encoding, " +
				"so its result is a Buffer rather than the target's JSON text — " +
				"the return leg has no text to parse", args[2], resolvedPath
		}
		return &ForeignEdge{
			Call:             call,
			TargetPath:       resolvedPath,
			Payload:          payload,
			StdoutName:       name,
			IsCompiledBinary: true,
		}, true, "", nil, resolvedPath
	}
	runnerWord, script, dataElement, scriptOk, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		return nil, false, sentence, sentenceNode, ""
	}
	if !scriptOk {
		return nil, false, "", nil, ""
	}
	resolvedPath, pathSentence := resolveForeignScriptPath(call, runnerWord, script)
	if pathSentence != "" {
		return nil, false, pathSentence, call, resolvedPath
	}
	if resolvedPath == "" {
		return nil, false, "", nil, ""
	}
	payload, encodingOk, optionsSentence := execFileSyncOptionsOf(args[2])
	if optionsSentence != "" {
		return nil, false, optionsSentence, args[2], resolvedPath
	}
	if !encodingOk {
		return nil, false, "this call runs " + runnerWord + " on " + script + " without a string encoding, " +
			"so its result is a Buffer rather than the target's JSON text — " +
			"the return leg has no text to parse", args[2], resolvedPath
	}
	if payload == nil && dataElement != nil {
		// no stdin `input`, but a second argv element exists — that
		// element is either the FILE-CARRIED shape's own path (a
		// preceding writeFileSync wrote it) or the pure argv-scalar
		// shape's data itself; the write-back check decides which,
		// since the two read as the identical AST shape otherwise
		filePayload, filePath, fileSentence, fileOk := fileCrossingOf(ctx, statements, index, args)
		if fileSentence != "" {
			return nil, false, fileSentence, dataElement, resolvedPath
		}
		if fileOk {
			return &ForeignEdge{
				Call:       call,
				TargetPath: resolvedPath,
				Payload:    filePayload,
				FilePath:   filePath,
				StdoutName: name,
			}, true, "", nil, resolvedPath
		}
		return &ForeignEdge{
			Call:       call,
			TargetPath: resolvedPath,
			ArgvValue:  dataElement,
			StdoutName: name,
		}, true, "", nil, resolvedPath
	}
	if payload == nil {
		// no stdin `input` and no second argv element at all — the
		// file-carried shape still needs SOME argv element to name the
		// path, so a preceding writeFileSync here has nothing to match
		// against. This is a RECOGNIZED edge with no outbound payload at
		// all (Payload nil, ArgvValue nil, FilePath nil): checkOutboundLeg's
		// own no-channel branch is where the THE NO-COMPLETED-RUN
		// DETERMINATION for a stdin-reading target lives (mirroring
		// checkArgvCrossing's ForeignSurfaceMixedStdinArgv case) — never
		// declined here, since whether this call's empty stdin actually
		// contradicts anything depends on the target's own stated
		// surface, which this recognizer has not read yet.
		return &ForeignEdge{
			Call:       call,
			TargetPath: resolvedPath,
			StdoutName: name,
		}, true, "", nil, resolvedPath
	}
	return &ForeignEdge{
		Call:       call,
		TargetPath: resolvedPath,
		Payload:    payload,
		ArgvValue:  dataElement,
		StdoutName: name,
	}, true, "", nil, resolvedPath
}

// fileCrossingOf recognizes the FILE-CARRIED data leg: a preceding
// `writeFileSync(<written path>, JSON.stringify(<payload>))` statement
// whose path (const-resolved paths allowed, via resolvedConstStringLiteral
// — the same follow scriptElementOf already performs) is named by one
// of callArgs[1]'s own argv elements. The CARRIER PREMISE — the bytes
// written are the bytes read — holds only when that write is the
// IMMEDIATELY PRECEDING statement, no exceptions: any statement between
// the write and the call could have touched the file first, so this
// reader requires index-1 specifically, never merely "somewhere
// earlier".
//
// Answers (payload, filePathElement, "", true) on a full match; (nil,
// nil, "", false) where NO statement in this body — preceding or not —
// is a writeFileSync at all — silent, since a call with no writeFileSync
// anywhere and no stdin input is simply not this shape (a caller with
// nothing to model owes no sentence any more than the ordinary "not
// this call" cases above it do); (nil, nil, said, false) in the two
// RECOGNIZED-and-blocked cases, each named: the immediately preceding
// statement IS a writeFileSync but its own written path names NO argv
// element of this call (a path mismatch — the write and the call both
// exist, but do not name the same file), or an EARLIER statement (not
// the immediately preceding one) writes a path this call's argv DOES
// name (an intervening statement — the write exists and the path
// matches, but the carrier premise still refuses it).
func fileCrossingOf(
	ctx *FlowContext, statements []*ast.Node, index int, callArgs []*ast.Node,
) (payload *ast.Node, filePathElement *ast.Node, sentence string, ok bool) {
	if index == 0 || len(callArgs) < 2 {
		return nil, nil, "", false
	}
	argv := Unwrapped(callArgs[1])
	if argv == nil || !ast.IsArrayLiteralExpression(argv) {
		return nil, nil, "", false
	}
	argvElements := argv.AsArrayLiteralExpression().Elements.Nodes
	// the immediately preceding statement, the ONLY position the carrier
	// premise can hold at
	precedingPath, precedingPayload, precedingWriteOk := writeFileSyncOf(ctx, statements[index-1])
	if precedingWriteOk {
		for _, element := range argvElements {
			if text, elementOk := argvLiteralTextOf(ctx, element); elementOk && text == precedingPath {
				return precedingPayload, Unwrapped(element), "", true
			}
		}
	}
	// scan the statements STRICTLY BEFORE that one: a writeFileSync
	// naming a path this call's argv also names, but separated from the
	// call by at least one intervening statement, is a RECOGNIZED write
	// the carrier premise still refuses — name the gap, don't stay silent
	for earlier := index - 2; earlier >= 0; earlier-- {
		writtenPath, _, writeOk := writeFileSyncOf(ctx, statements[earlier])
		if !writeOk {
			continue
		}
		for _, element := range argvElements {
			if text, elementOk := argvLiteralTextOf(ctx, element); elementOk && text == writtenPath {
				return nil, nil, "a statement writes " + strconv.Quote(writtenPath) +
					" earlier in this body, but it is not the statement immediately before this call — " +
					"an intervening statement could have touched the file first, so the carrier premise " +
					"(the bytes written are the bytes read) does not hold", false
			}
		}
	}
	// the immediately preceding statement IS a writeFileSync, but names a
	// path none of this call's argv elements name — a genuine path
	// mismatch, recognized and named rather than silently read as "no
	// writeFileSync at all"
	if precedingWriteOk {
		return nil, nil, "the immediately preceding statement writes " + strconv.Quote(precedingPath) +
			", and this call's own argv names no element with that same path — the carrier premise " +
			"(the bytes written are the bytes read) does not hold, so the written file is not this call's data leg", false
	}
	return nil, nil, "", false
}

// writeFileSyncOf reads `writeFileSync(<path>, JSON.stringify(<payload>))`
// as a bare expression statement — fs's own two-argument sync write, the
// same shape execFileSyncOptionsOf already reads for the `input` property,
// applied to a direct call rather than an object property. The callee
// test is BY NAME ONLY, the same discipline jsonStringifyArgumentOf
// already states for `JSON.stringify` ("the resolvesToDefaultLib check
// that would ground it belongs to the evaluation of that call") —
// writeFileSync never gates WHICH invocation reader runs the way
// execFileSync/spawnSync/execSync/spawn's own callee does
// (resolvesToChildProcessMember, at foreignEdgeOf's dispatch), so there is
// no dispatch moment this name needs to be exclusive at; a same-named
// local helper reads identically here, exactly as a local `JSON` shadow
// would for the stringify read. The path element follows the same
// written-literal-or-const-resolved rule scriptElementOf and
// argvLiteralTextOf already apply, so a computed path is not recognized
// here any more than a computed script path is recognized there.
func writeFileSyncOf(ctx *FlowContext, statement *ast.Node) (path string, payload *ast.Node, ok bool) {
	if statement == nil || !ast.IsExpressionStatement(statement) {
		return "", nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if call == nil || !ast.IsCallExpression(call) {
		return "", nil, false
	}
	callee := calleeOf(call)
	if callee == nil {
		return "", nil, false
	}
	var name *ast.Node
	if ast.IsIdentifier(callee) {
		name = callee
	} else if ast.IsPropertyAccessExpression(callee) {
		name = callee.AsPropertyAccessExpression().Name()
	}
	if name == nil || name.Text() != "writeFileSync" {
		return "", nil, false
	}
	args, hasArgs := callArguments(call)
	if !hasArgs || len(args) < 2 {
		return "", nil, false
	}
	pathText, pathOk := argvLiteralTextOf(ctx, args[0])
	if !pathOk {
		return "", nil, false
	}
	inner, innerOk := jsonStringifyArgumentOf(args[1])
	if !innerOk {
		return "", nil, false
	}
	return pathText, inner, true
}

// spawnSyncEdgeOf reads the same argv/options shape as execFileSync
// through spawnSync — the difference is entirely in the RESULT: spawnSync
// answers an object (`{stdout, stderr, status, ...}`), never a bare
// string, so the bound name's stdout rides at `<name>.stdout` rather
// than at `<name>` itself. StdoutName carries the same binding name;
// soleParseConsumerOf's shared reading (foreign_edge.go's return leg)
// accepts either the bare name or `<name>.stdout` as the read of it.
func spawnSyncEdgeOf(
	ctx *FlowContext, call *ast.Node, name string, statements []*ast.Node, index int,
) (*ForeignEdge, bool, string, *ast.Node, string) {
	return execFileSyncEdgeOf(ctx, call, name, statements, index)
}

// constBoundCallOf reads `const <name> = <call>(...)` — one declarator
// binding one call, whatever the callee's name. A const binding is what
// lets the return leg follow the name to its parse: a `let` the
// following statements could rewrite carries no such guarantee, and
// declines here. Shared by every invocation function this file reads
// (execFileSync, spawnSync, execSync) — the binding shape is the same
// regardless of which one was called.
func constBoundCallOf(statement *ast.Node) (string, *ast.Node, bool) {
	if statement == nil || !ast.IsVariableStatement(statement) {
		return "", nil, false
	}
	list := statement.AsVariableStatement().DeclarationList
	if list == nil || (list.Flags&ast.NodeFlagsConst) == 0 {
		return "", nil, false
	}
	declarations := list.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return "", nil, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) || declaration.Initializer == nil {
		return "", nil, false
	}
	call := Unwrapped(declaration.Initializer)
	if call == nil || !ast.IsCallExpression(call) {
		return "", nil, false
	}
	return name.Text(), call, true
}

// foreignCallBindsBareStdoutString is whether call's own BOUND NAME —
// the identifier a `const <name> = call(...)` declaration's initializer
// evaluates to — is the captured stdout string itself, rather than an
// object CARRYING a `.stdout` property. execFileSync and execSync both
// answer the string (or Buffer) directly; spawnSync answers
// `{stdout, stderr, status, ...}`, and StdoutName rides at `<name>.stdout`
// (spawnSyncEdgeOf's own doc, isForeignParseOf's dual bare/`.stdout`
// reading). The intermediate binding this file pins
// (foreignStdoutSerializedValue's CallOverride) keys on the call
// expression itself — the exact node AnalyzeVariableStatement evaluates
// for the bound name's own value — so it is sound ONLY for the shapes
// where that value IS the string, never spawnSync's object.
func foreignCallBindsBareStdoutString(ctx *FlowContext, call *ast.Node) bool {
	callee := calleeOf(call)
	return resolvesToChildProcessMember(ctx, callee, "execFileSync") ||
		resolvesToChildProcessMember(ctx, callee, "execSync")
}

// execSyncShellStringSentence is the law-2 decline for the whole
// family of execSync/exec shapes this file cannot read a runner and
// script out of: a command that is not a written string literal at
// all (a template with a substitution, a variable, a concatenation),
// or one whose tokens carry shell syntax this reader does not model
// (quoting, `$`, pipes, `&&`, `;`, output redirection, backticks).
const execSyncShellStringSentence = "the command is a shell string the checker cannot read; " +
	"spell it as an argv list"

// execSyncUnsupportedShellTokenChars is the set of characters whose
// presence in ANY token marks the command string as shell syntax this
// reader does not model, rather than a plain word or a `./path`: a
// quote, `$`, `|`, `&`, `;`, `>`, `<`, or a backtick. `<` doubles as the
// `< file` stdin-from-file shape's own opening character, but this
// reader has no fixture row exercising that shape, so `<` stays in the
// unsupported set rather than getting a special reading no row proves.
const execSyncUnsupportedShellTokenChars = "'\"$|&;><`"

// execSyncEdgeOf reads `execSync(<command>, {encoding: <string
// encoding>})` (or the bare one-argument form) — a single shell
// COMMAND STRING rather than an argv array. Recognized only when the
// command is a WRITTEN string literal (or no-substitution template)
// tokenizable on single spaces into plain words and `./path` tokens
// with none of the unsupported shell characters: that is the one shape
// where the string names a runner and a script as deterministically as
// an argv array does. Anything else — a template with a substitution,
// a variable, a concatenation, or a literal string carrying shell
// syntax this reader does not model — owes the one law-2 sentence:
// the command is unreadable as spelled, and an argv list is what would
// resolve it.
func execSyncEdgeOf(call *ast.Node, name string) (*ForeignEdge, bool, string, *ast.Node, string) {
	args, _ := callArguments(call)
	if len(args) < 1 {
		return nil, false, "", nil, ""
	}
	command, literalOk := stringLiteralText(args[0])
	var runnerWord, script string
	var payload *ast.Node
	if literalOk {
		var tokensOk bool
		runnerWord, script, tokensOk = execSyncSimpleCommandTokens(command)
		if !tokensOk {
			return nil, false, execSyncShellStringSentence, args[0], ""
		}
	} else {
		// the ONE substitution shape this reader still recognizes: a
		// template whose constant prefix names `<runner> <script> <<<`
		// and whose single substitution is JSON.stringify(<payload>) —
		// the stdin-json convention spelled through a shell here-string
		// rather than an options object's `input` key.
		var heredocOk bool
		runnerWord, script, payload, heredocOk = execSyncHeredocCommandOf(args[0])
		if !heredocOk {
			return nil, false, execSyncShellStringSentence, args[0], ""
		}
	}
	resolvedPath, pathSentence := resolveForeignScriptPath(call, runnerWord, script)
	if pathSentence != "" {
		return nil, false, pathSentence, call, resolvedPath
	}
	if resolvedPath == "" {
		return nil, false, "", nil, ""
	}
	// execSync's own bound name (constBoundCallOf already required a
	// const binding) IS the stdout string, exactly like execFileSync's —
	// there is no options-object input/encoding pair to read here, since
	// the whole command is the one string argument; a second argument,
	// where present, only ever carries `encoding`.
	if len(args) >= 2 {
		if _, encodingOk, optionsSentence := execFileSyncOptionsOf(args[1]); optionsSentence != "" {
			return nil, false, optionsSentence, args[1], resolvedPath
		} else if !encodingOk {
			return nil, false, "this call runs " + runnerWord + " on " + script + " without a string " +
				"encoding, so its result is a Buffer rather than the target's JSON text — " +
				"the return leg has no text to parse", args[1], resolvedPath
		}
	}
	return &ForeignEdge{
		Call:       call,
		TargetPath: resolvedPath,
		Payload:    payload,
		StdoutName: name,
	}, true, "", nil, resolvedPath
}

// execSyncHeredocOperator is the shell here-string operator this
// reader recognizes as the ONE way a template substitution spells the
// stdin-json convention through execSync's shell string rather than
// execFileSync's options object.
const execSyncHeredocOperator = "<<<"

// execSyncHeredocCommandOf reads a template literal shaped exactly
// `<argv tokens...> <<< '${JSON.stringify(<payload>)}'` (the closing
// quote optional, and either single or double) — the stdin-json
// convention spelled through a shell here-string. This is a
// RECOGNIZER, not a shell interpreter: it accepts exactly this shape
// and no other, tokenizing the constant prefix through the same
// unsupported-character gate execSyncSimpleCommandTokens already
// applies to a plain literal command, so a prefix carrying any other
// shell metacharacter (a pipe, a second substitution, a second `<<<`)
// is refused rather than partially read.
//
// Answers ok=false for anything past that one shape: more than one
// template span, a substitution that is not JSON.stringify(...), a
// constant prefix whose tokens do not end in the heredoc operator, or
// trailing literal text past the one optional closing quote.
func execSyncHeredocCommandOf(argument *ast.Node) (runnerWord string, script string, payload *ast.Node, ok bool) {
	node := Unwrapped(argument)
	if node == nil || !ast.IsTemplateExpression(node) {
		return "", "", nil, false
	}
	template := node.AsTemplateExpression()
	spans := template.TemplateSpans.Nodes
	if len(spans) != 1 {
		return "", "", nil, false
	}
	span := spans[0].AsTemplateSpan()
	inner, stringifyOk := jsonStringifyArgumentOf(span.Expression)
	if !stringifyOk {
		return "", "", nil, false
	}
	// the trailing literal text — everything after the substitution —
	// must be nothing but one optional closing quote (matching whatever
	// quote character opened the here-string in the prefix, read below)
	trailing := span.Literal.Text()
	prefixWord, prefixOk := execSyncHeredocPrefixTokens(template.Head.Text(), trailing)
	if !prefixOk {
		return "", "", nil, false
	}
	runnerWord, script, tokensOk := execSyncSimpleCommandTokens(prefixWord)
	if !tokensOk {
		return "", "", nil, false
	}
	return runnerWord, script, inner, true
}

// execSyncHeredocPrefixTokens reads the template's constant prefix as
// `<runner> <script> <<< <quote>` and the trailing literal (past the
// substitution) as that SAME quote character alone (or nothing, for
// an unquoted here-string) — the two ends of one matched optional
// quote wrapping the substitution. Answers the `<runner> <script>`
// words alone (space-joined, ready for execSyncSimpleCommandTokens),
// discarding the operator and the quote once both are confirmed to
// match.
func execSyncHeredocPrefixTokens(head string, trailing string) (string, bool) {
	quote := ""
	switch {
	case strings.HasSuffix(head, "'"):
		quote = "'"
	case strings.HasSuffix(head, "\""):
		quote = "\""
	}
	head = strings.TrimSuffix(head, quote)
	if trailing != quote {
		// the quote that opens the here-string (if any) must be the SAME
		// one that closes it, immediately after the substitution and
		// nothing else — a mismatched or extra trailing character is
		// shell syntax this reader does not model
		return "", false
	}
	head = strings.TrimSuffix(head, " ")
	if !strings.HasSuffix(head, execSyncHeredocOperator) {
		return "", false
	}
	head = strings.TrimSuffix(head, execSyncHeredocOperator)
	head = strings.TrimSuffix(head, " ")
	if head == "" || strings.ContainsAny(head, execSyncUnsupportedShellTokenChars) {
		return "", false
	}
	return head, true
}

// execSyncSimpleCommandTokens splits a command string on single spaces
// and reads it as `<runner> <script>` — exactly two tokens, neither
// carrying any unsupported shell character, the second ending `.py`.
// More or fewer tokens, or any unsupported character in either one,
// answers false: this reader models only the plain two-word command,
// not a program's own arguments or any shell construct.
func execSyncSimpleCommandTokens(command string) (runnerWord string, script string, ok bool) {
	tokens := splitOnSingleSpaces(command)
	if len(tokens) != 2 {
		return "", "", false
	}
	for _, token := range tokens {
		if token == "" || strings.ContainsAny(token, execSyncUnsupportedShellTokenChars) {
			return "", "", false
		}
	}
	if !pythonSpellings[tokens[0]] {
		return "", "", false
	}
	return tokens[0], tokens[1], true
}

// splitOnSingleSpaces is strings.Split(s, " ") under its own name — the
// tokenizer's own stated rule ("tokenize it on single spaces") rather
// than a general whitespace split, so a tab or a run of spaces inside
// the string is left for the caller's per-token check to catch instead
// of silently collapsing.
func splitOnSingleSpaces(s string) []string {
	return strings.Split(s, " ")
}
