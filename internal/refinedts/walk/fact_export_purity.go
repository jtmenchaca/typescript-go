// The channel-purity fact the exported artifact's return.stdoutPure
// field carries — foreign_edge.go:160-169's twin on the Go side reads
// exactly this bit off the Python side's own scan.
//
// Mirrors fact_export.rs's writes_nothing_to_stdout (lines 407-570)
// exactly in POLICY, TypeScript-shaped in SYNTAX:
//
//   - a call to a stdout writer (`console.log`, `console.info`,
//     `console.debug`, `process.stdout.write`) refuses the claim;
//   - a call to a STDERR writer (`console.error`, `console.warn`,
//     `process.stderr.write`) does NOT — stderr is not the wire, and
//     this is the one place this scan reads differently from a naive
//     "no console at all" reading;
//   - a call to another function DECLARED IN THE SAME FILE recurses
//     into that function's body, with a visited set so a recursive or
//     mutually recursive call terminates;
//   - a call to anything else — imported, a method on a receiver this
//     scan does not model, a computed callee — is OPAQUE and refuses
//     the claim, EXCEPT the small pure-builtin allowlist below, whose
//     own callbacks (a `.map` callback, for instance) still get
//     scanned themselves.
//
// The scan only ever ADMITS the claim; a construct it does not
// recognize always refuses it, never assumes it — the same
// conservative posture the Rust banner states for its own list.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// WritesNothingToStdout is whether declaration's body writes nothing to
// stdout, scanned transitively through same-file calls. sourceFile is
// the file declaration belongs to — same-file recursion is bounded to
// its top-level function declarations, mirroring top_level_defs in the
// Rust scan.
func WritesNothingToStdout(sourceFile *ast.SourceFile, declaration *ast.Node) bool {
	if sourceFile == nil || declaration == nil {
		return false
	}
	body := declaration.Body()
	if body == nil {
		return false
	}
	fileFunctions := map[string]*ast.Node{}
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		name := statement.AsFunctionDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) {
			continue
		}
		fileFunctions[name.Text()] = statement
	}
	return bodyIsStdoutPure(body, fileFunctions, map[string]struct{}{})
}

// bodyIsStdoutPure scans one body — a block or an arrow's expression
// body — following every same-file call it makes. visited names the
// functions already scanned, so a def already being scanned adds
// nothing new to the answer and a cycle terminates here.
func bodyIsStdoutPure(
	body *ast.Node, fileFunctions map[string]*ast.Node, visited map[string]struct{},
) bool {
	writesStdout := false
	hasOpaqueCall := false
	var calledNames []string

	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == nil || writesStdout {
			return true
		}
		if expressionWritesStdout(node) {
			writesStdout = true
			return true
		}
		if ast.IsCallExpression(node) {
			callExpression := node.AsCallExpression()
			callee := callExpression.Expression
			switch {
			case isCapturedStdoutSpawnCall(callee, callExpression.Arguments.Nodes):
				// checked BEFORE the bare-identifier case below —
				// execFileSync/spawnSync/execSync are themselves bare
				// identifiers, and this row must claim them first or
				// they fall into calledNames and get refused as an
				// unresolvable same-file name instead. Admitted per
				// isCapturedStdoutSpawnCall's own comment (each row's
				// capture-semantics reasoning): the child's stdout is
				// piped back as this call's own return value, never
				// written to the PARENT's stdout, so this call writes
				// nothing to the wire channel on its own account
			case ast.IsIdentifier(callee):
				calledNames = append(calledNames, callee.Text())
			case isPureBuiltinCall(callee):
				// the callee itself writes nothing; its arguments —
				// including any callback — are still walked below, so a
				// dirty callback (`.map(x => console.log(x))`) is still
				// caught
			default:
				hasOpaqueCall = true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)

	if writesStdout || hasOpaqueCall {
		return false
	}
	for _, name := range calledNames {
		if isPureBuiltinName(name) {
			continue
		}
		callee, declaredHere := fileFunctions[name]
		if !declaredHere {
			// a name this file does not declare: an import, or a global
			// this scan does not model. The scan cannot see that body, so
			// it cannot claim the channel is clean.
			return false
		}
		if _, already := visited[name]; already {
			continue
		}
		visited[name] = struct{}{}
		calleeBody := callee.Body()
		if calleeBody == nil || !bodyIsStdoutPure(calleeBody, fileFunctions, visited) {
			return false
		}
	}
	return true
}

// expressionWritesStdout is whether node is itself a write to stdout:
// `console.log(...)` / `console.info(...)` / `console.debug(...)`, or
// `process.stdout.write(...)`. `console.error` / `console.warn` and
// `process.stderr.write` are STDERR, not stdout, and do not match here
// — the one place this scan reads differently from a naive "no
// console at all" reading.
func expressionWritesStdout(node *ast.Node) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	callee := node.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	access := callee.AsPropertyAccessExpression()
	method := access.Name()
	if method == nil || !ast.IsIdentifier(method) {
		return false
	}
	switch receiverPathText(access.Expression) {
	case "console":
		switch method.Text() {
		case "log", "info", "debug":
			return true
		}
		return false
	case "process.stdout":
		return method.Text() == "write"
	}
	return false
}

// receiverPathText reads a dotted receiver's own spelling —
// `process.stdout` for `process.stdout.write(...)`'s receiver, or a
// bare identifier's text — answering "" for any shape wider than a
// plain identifier or dotted-identifier chain.
func receiverPathText(expression *ast.Node) string {
	node := Unwrapped(expression)
	if node == nil {
		return ""
	}
	if ast.IsIdentifier(node) {
		return node.Text()
	}
	if ast.IsPropertyAccessExpression(node) {
		access := node.AsPropertyAccessExpression()
		name := access.Name()
		if name == nil || !ast.IsIdentifier(name) {
			return ""
		}
		root := receiverPathText(access.Expression)
		if root == "" {
			return ""
		}
		return root + "." + name.Text()
	}
	return ""
}

// isPureBuiltinCall is whether a callee expression is one this scan
// admits as writing nothing to stdout on its own — the callee's own
// receiver/name shape, mirroring is_opaque_receiver_call's spirit:
// every attribute-callee shape is opaque except a small modelled list.
//
// Mirrors is_pure_builtin's Rust list, widened to this scan's own
// receiver-method shape (the Rust list is bare names; JS's equivalents
// are almost all `Receiver.method(...)`):
//
//   - Math.<anything> — the whole Math namespace, same posture as the
//     Rust list's math module;
//   - JSON.parse / JSON.stringify — the Rust list's json module,
//     narrowed to the two calls that read/write nothing to a channel;
//   - Number.isInteger / Number.isFinite / Number.isNaN /
//     Number.parseFloat / Number.parseInt — the Rust list's int/float
//     coercions, JS-shaped as Number's static methods;
//   - Array.isArray — the Rust list's list/tuple constructors' nearest
//     JS analogue: a brand check, not a channel write;
//   - the bare coercions Number(...), String(...), Boolean(...) — the
//     Rust list's int/float/str/bool;
//   - the array iteration methods (.map/.filter/.reduce/.slice/
//     .concat/.forEach/.some/.every/.find/.findIndex/.flatMap/.join/
//     .sort/.reverse/.includes/.indexOf/.flat) on ANY receiver — these
//     read their receiver and (for the callback-taking ones) their
//     callback's return value, and write nothing themselves; the Rust
//     list's sum/len/sorted/enumerate/zip/all/any/min/max/abs/round/
//     pow/divmod are exactly this shape moved onto a method receiver.
//     Their own callback argument, if any, is still walked by the
//     caller — a `.map(x => console.log(x))` callback still counts.
func isPureBuiltinCall(callee *ast.Node) bool {
	node := Unwrapped(callee)
	if node == nil || !ast.IsPropertyAccessExpression(node) {
		return false
	}
	access := node.AsPropertyAccessExpression()
	method := access.Name()
	if method == nil || !ast.IsIdentifier(method) {
		return false
	}
	receiver := receiverPathText(access.Expression)
	switch receiver {
	case "Math":
		return true
	case "console":
		// the STDERR writers: not the wire, and not opaque either — the
		// file banner's one deliberate divergence from a naive "no
		// console at all" scan. The stdout writers never reach here:
		// expressionWritesStdout already refused the claim for them.
		switch method.Text() {
		case "error", "warn":
			return true
		}
		return false
	case "process.stderr":
		return method.Text() == "write"
	case "JSON":
		switch method.Text() {
		case "parse", "stringify":
			return true
		}
		return false
	case "Number":
		switch method.Text() {
		case "isInteger", "isFinite", "isNaN", "parseFloat", "parseInt":
			return true
		}
		return false
	case "Array":
		return method.Text() == "isArray"
	}
	// a method call on ANY OTHER receiver — the array iteration methods
	// read their own receiver and callback, and write nothing on their
	// own account; the receiver itself is not walked as a callee, only
	// as a plain expression the traversal already reaches
	switch method.Text() {
	case "map", "filter", "reduce", "reduceRight", "slice", "concat",
		"forEach", "some", "every", "find", "findIndex", "flatMap",
		"join", "sort", "reverse", "includes", "indexOf", "lastIndexOf", "flat":
		return true
	}
	return false
}

// isPureBuiltinName is the bare-name half of the allowlist — the
// coercion functions called plainly, `Number(x)` / `String(x)` /
// `Boolean(x)`, the Rust list's int/float/str/bool moved onto JS's own
// spelling of a global coercion call.
func isPureBuiltinName(name string) bool {
	switch name {
	case "Number", "String", "Boolean":
		return true
	}
	return false
}

// capturedStdoutSpawnNames is the child_process member names whose
// SYNCHRONOUS call captures the child's stdout as this call's own
// return value rather than writing anything to the parent's stdout —
// tested by name alone, the same posture foreign_edge.go's own
// dispatch takes for these three names before it additionally checks
// symbol resolution (this scan has no *ast.Checker in reach, so it
// cannot run that additional check; a same-named local helper reads
// identically here, exactly as foreign_edge.go's writeFileSyncOf
// banner already accepts for its own by-name reads). `spawn` (async)
// is deliberately absent: its result is a ChildProcess whose stdout is
// a readable STREAM, not a captured return value at this call's own
// site, so this scan cannot admit it here on the same evidence.
var capturedStdoutSpawnNames = map[string]bool{
	"execFileSync": true,
	"spawnSync":    true,
	"execSync":     true,
}

// isCapturedStdoutSpawnCall is whether a call spawns a child process
// through a form whose stdout is CAPTURED rather than written to the
// parent's own stdout — admitted per Node's own documented `child_process`
// semantics, cited per row:
//
//   - execFileSync: the default `stdio` is `['pipe', 'pipe', 'pipe']`,
//     and with no `stdio` override the child's stdout is captured and
//     returned as this call's own return value (a Buffer, or a string
//     when `encoding` is set) — never written to the parent's stdout.
//     Admitted UNCONDITIONALLY: every call shape reaches this row,
//     because the only way to defeat the capture is an explicit
//     `stdio` override, checked below for every row alike.
//   - spawnSync: the identical default (`stdio: ['pipe', 'pipe',
//     'pipe']`), with the captured stdout read back at `result.stdout`
//     rather than as the bare return value — the capture premise is
//     the same, only the read site differs (a site this purity scan
//     does not need to know, since it is asking whether the CALL
//     writes the parent's stdout, not where the caller reads the
//     result).
//   - execSync: the same default stdio, the captured stdout returned
//     as this call's own return value exactly as execFileSync's is.
//
// The one shape that defeats every row above is an options object
// whose `stdio` property NAMES the parent's own stdout as the child's
// target — `stdio: "inherit"` (all three streams inherited) or an
// array whose index 1 (stdout) is `"inherit"` — which is checked
// against whichever argument position this callee's own options
// object occupies (execFileSync/spawnSync: argument 2; execSync:
// argument 1, since it has no argv array). An options object this
// scan cannot read into (not written as an object literal) refuses
// the claim rather than assuming the safe default, matching this
// file's own conservative-only-admits posture; a per-argument
// `stdio` reading this scan cannot see (a computed property, a
// non-literal value) also refuses, for the same reason.
func isCapturedStdoutSpawnCall(callee *ast.Node, arguments []*ast.Node) bool {
	node := Unwrapped(callee)
	if node == nil || !ast.IsIdentifier(node) {
		return false
	}
	name := node.Text()
	if !capturedStdoutSpawnNames[name] {
		return false
	}
	optionsIndex := 2
	if name == "execSync" {
		optionsIndex = 1
	}
	if optionsIndex >= len(arguments) {
		// no options argument at all: Node's own default stdio applies,
		// which captures stdout on every one of these three names
		return true
	}
	return !stdioOptionInheritsStdout(arguments[optionsIndex])
}

// stdioOptionInheritsStdout reads an options-argument expression's own
// `stdio` property (mirroring execFileSyncOptionsOf's per-property
// read of the same object literal) and answers whether it explicitly
// routes the child's stdout to the parent's own stdout: the bare
// string `"inherit"` (all three streams), or an array literal whose
// index 1 (the stdout slot, Node's own `[stdin, stdout, stderr]`
// order) is the string `"inherit"`. No `stdio` property, or a `stdio`
// value this scan cannot read as one of those two literal shapes,
// answers false — the capture default stands, per the same
// conservative-only-admits posture the rest of this file takes
// (a shape this scan cannot see through never registers as the
// dangerous case, but it also never registers as the safe one on
// invented grounds — it simply is not what defeats the capture).
func stdioOptionInheritsStdout(argument *ast.Node) bool {
	options := Unwrapped(argument)
	if options == nil || !ast.IsObjectLiteralExpression(options) {
		return false
	}
	for _, property := range options.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			continue
		}
		assignment := property.AsPropertyAssignment()
		key := assignment.Name()
		if key == nil || !ast.IsIdentifier(key) || key.Text() != "stdio" {
			continue
		}
		value := Unwrapped(assignment.Initializer)
		if value == nil {
			return false
		}
		if word, ok := stringLiteralText(value); ok {
			return word == "inherit"
		}
		if ast.IsArrayLiteralExpression(value) {
			elements := value.AsArrayLiteralExpression().Elements.Nodes
			if len(elements) < 2 {
				return false
			}
			word, ok := stringLiteralText(elements[1])
			return ok && word == "inherit"
		}
		return false
	}
	return false
}
