// The cross-language call edge, recognized in the walk.
//
//	const stdout = execFileSync("python3", ["./audio_level.py"], {
//	  input: JSON.stringify(boosted),
//	  encoding: "utf8",
//	});
//	return JSON.parse(stdout);
//
// CROSS-LANGUAGE-EDGE.md §2's corollary is what makes this a REAL edge
// and not a manifest: the argv deterministically NAMES the code that
// runs next, so the checker treats the invocation the way it treats an
// import. §11 is this exact spelling; §4 is the JSON transport model
// both legs apply; §5 is the list of premises the crossing rests on.
//
// WHAT THE ROUTE DOES, in order:
//
//  1. RECOGNIZE the call (Q1 side — the argv-shape reading
//     builtin_contracts.go already does for its Q3 shell-redirection
//     row, promoted here to resolve a target). Anything unrecognized
//     declines, and every decline NAMES what broke.
//  2. READ the target's exported fact off disk and discharge the
//     artifact-side premises — target integrity, runtime identity,
//     harness shape (foreign_edge_artifact.go).
//  3. DISCHARGE the outbound leg's premises against the value actually
//     being stringified: NaN-freedom (§4 — NaN stringifies to null, so
//     the target never sees the number the caller sent), and the
//     crossing fit (the argument's element set inside the entry's, its
//     length floor at or above the entry's). A fit FAILURE is not a
//     decline: it is a 7001 at the call, because the value can escape
//     what the target states it admits.
//  4. DISCHARGE channel purity (§5) and ATTACH the return fact to the
//     JSON.parse node that reads the captured stdout.
//
// The attach rides ctx.NodeOverrides, the seam the relational
// accumulation already uses for a value no re-walk can reach: the fact
// on `JSON.parse(stdout)` comes from ANOTHER LANGUAGE'S checker, and
// nothing in this file's walk can derive it. Today that node evaluates
// to residue (coercion_models.go's readJsonMethods: "whatever JSON
// value the text spells"), and the override supersedes it —
// evaluateExpression reads NodeOverrides before it walks.
//
// TRUST GRADE. The attached fact is stamped TrustSpec, not TrustProved.
// Every premise above is discharged by a real check, but the crossing
// itself rests on CITED SPEC BEHAVIOUR that this tree has not proved:
// §4's number round-trip (shortest-round-trip ∘ nearest-parse =
// identity on finite binary64) is a NAMED OPEN PROBLEM in the kernel —
// its own files say so — and stands as a premise citing both languages'
// commitments; the runtime band is a citation of the Python pins, not a
// theorem. TrustSpec is exactly what the tree already stamps on a fact
// whose weakest boundary is a spec clause rather than a kernel decision
// (literal_values.go, bitwise_transfer.go's spec-cited windows). The
// grade is met with the target's own reading, so nothing here can
// overstate what the artifact carried.
//
// SCOPING. The override is set around ONE statement's walk and restored
// after — flow_context.go's NodeOverrides states the obligation, and
// this route follows it exactly as listWalk's relational arm does.

package walk

import (
	"path/filepath"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// pythonSpellings are the argv[0] words this recognizer reads as "the
// CPython interpreter". Anything else declines by name: `python3.11`
// and a bare path both name an interpreter whose band the artifact's
// runtime premise cannot be matched against by spelling alone.
var pythonSpellings = map[string]bool{"python3": true, "python": true}

// stringEncodings are the execFileSync `encoding` words under which the
// call ANSWERS A STRING rather than a Buffer (Node child_process: with
// no encoding the sync exec answers a Buffer, and JSON.parse of a
// Buffer is a different reading entirely). Only the encodings that
// round-trip the target's JSON text are admitted.
var stringEncodings = map[string]bool{"utf8": true, "utf-8": true}

// ForeignEdge is one recognized cross-language call: which node the
// call is, which .py file it names, which expression crosses out, and
// which name catches the target's stdout.
type ForeignEdge struct {
	// Call: the execFileSync call expression — where a fit refutation
	// points.
	Call *ast.Node
	// TargetPath: the .py file, resolved against the SOURCE FILE's own
	// directory (a relative argv entry is relative to the file that
	// wrote it, which is the only reading that survives a moved cwd).
	TargetPath string
	// Payload: the expression handed to JSON.stringify — the value that
	// actually crosses out.
	Payload *ast.Node
	// StdoutName: the name the call's result binds, whose sole
	// JSON.parse consumer receives the return fact.
	StdoutName string
}

// ForeignEdgeOutcome is what the route decided at one statement. Exactly
// one of Override and Decline is meaningful: a green crossing publishes
// the parse node's fact, and everything else says one sentence naming
// the premise that stopped it.
//
// Fires is separate from both: a REFUTED crossing (the outbound value
// escapes the target's entry) reports 7001 and publishes nothing — the
// call is wrong, so there is no fact to carry back.
type ForeignEdgeOutcome struct {
	// Override: the one-entry map the caller walks ONE statement under,
	// pinning the parse result. Nil unless every premise came back green.
	Override map[*ast.Node]abstractdomain.AbstractValue
	// OverrideStatement: the index in the caller's own statement list of
	// the statement that CONTAINS the pinned parse. The caller sets the
	// override around exactly that statement's walk and restores after —
	// flow_context.go's scoping obligation, kept as tight as the shape
	// allows rather than left live for the whole list.
	OverrideStatement int
	// Decline: the sentence naming the premise that stopped the edge,
	// or "" where the edge was never recognized at all (no sentence is
	// owed for an ordinary call).
	Decline string
	// DeclineNode: where the decline sentence points.
	DeclineNode *ast.Node
}

// ForeignEdgeAt recognizes a cross-language call at statements[index]
// and, on all premises green, answers the override the CALLER walks the
// following statements under.
//
// Answers (nil, false) for every statement that is not this shape —
// the ordinary walk is untouched and pays one recognizer's worth of
// syntax tests. A recognized edge that cannot be completed answers an
// outcome carrying a Decline sentence, which the caller reports: an
// edge the checker sees and cannot serve is a work-queue item, never a
// silence.
func ForeignEdgeAt(
	ctx *FlowContext, env Env, statements []*ast.Node, index int,
) (*ForeignEdgeOutcome, bool) {
	edge, recognized, declineSentence, declineNode := foreignEdgeOf(ctx, statements[index])
	if !recognized {
		if declineSentence == "" {
			return nil, false
		}
		// the call WAS an execFileSync-to-python invocation and something
		// about its spelling stopped the resolution — say which
		return &ForeignEdgeOutcome{Decline: declineSentence, DeclineNode: declineNode}, true
	}
	artifact, artifactSentence := ReadForeignArtifact(edge.TargetPath)
	if artifactSentence != "" {
		return &ForeignEdgeOutcome{Decline: artifactSentence, DeclineNode: edge.Call}, true
	}
	// the OUTBOUND leg: every §4/§5 premise about what crosses out,
	// discharged against the value the walk holds for it
	if outcome := checkOutboundLeg(ctx, env, edge, artifact); outcome != nil {
		return outcome, true
	}
	// CHANNEL PURITY (§5): the wire is stdout, and the claim assumes
	// stdout carries exactly the serialized result
	if !artifact.Called.Return.StdoutPure {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " does not state that it writes " +
				"nothing else to stdout, and this edge reads its result off stdout — " +
				"the channel-purity premise is undischarged",
			DeclineNode: edge.Call,
		}, true
	}
	// the RETURN leg: the target's own fact, attached to the parse
	parse, at, parseSentence := soleParseConsumerOf(statements, index, edge.StdoutName)
	if parseSentence != "" {
		return &ForeignEdgeOutcome{Decline: parseSentence, DeclineNode: edge.Call}, true
	}
	return &ForeignEdgeOutcome{
		Override: map[*ast.Node]abstractdomain.AbstractValue{
			parse: foreignReturnValue(artifact),
		},
		OverrideStatement: at,
	}, true
}

// foreignReturnValue is the fact the parse result wears: the target's
// stated return set, at the grade the crossing's weakest cited boundary
// admits.
//
// TrustSpec, and the reason is the file banner's: the value is not the
// kernel's own decision about this expression, it is another language's
// claim carried across a transport whose identity §4 states as a CITED
// PREMISE (the round-trip theorem is a named open problem) under a
// runtime band cited from the Python pins. TrustSpec is the tree's
// existing grade for exactly that boundary — a spec clause read
// correctly, not a theorem discharged.
func foreignReturnValue(artifact *ForeignArtifact) abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		artifact.Called.Return.Set, nil,
		abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

/* ── recognition (Q1 side) ───────────────────────────────────────── */

// foreignEdgeOf reads one statement as `const <name> = execFileSync(
// <python>, [<script>], {input: JSON.stringify(<payload>), encoding:
// <string encoding>})`.
//
// The four answers it can give:
//
//	(edge, true,  "", nil)     — recognized whole
//	(nil,  false, "", nil)     — not this shape at all; no sentence owed
//	(nil,  false, said, node)  — an execFileSync-to-python call whose
//	                             spelling stopped the resolution
//
// The declines that DO owe a sentence are exactly those where the
// reader can see a cross-language call and cannot serve it. A call to
// some other program, or a `spawn` of a shell, is not this edge and
// says nothing.
func foreignEdgeOf(ctx *FlowContext, statement *ast.Node) (*ForeignEdge, bool, string, *ast.Node) {
	name, call, ok := execFileSyncBindingOf(statement)
	if !ok {
		return nil, false, "", nil
	}
	if !resolvesToChildProcessExecFileSync(ctx, calleeOf(call)) {
		// a local helper named execFileSync is not the Node built-in, and
		// nothing here knows what it runs — an ordinary call
		return nil, false, "", nil
	}
	args, _ := callArguments(call)
	if len(args) < 3 {
		return nil, false, "", nil
	}
	interpreter, interpreterOk := stringLiteralText(args[0])
	if !interpreterOk || !pythonSpellings[interpreter] {
		// some other program: not a Python edge, nothing owed
		return nil, false, "", nil
	}
	// past this point the reader KNOWS it is looking at a python
	// invocation, so every remaining decline names what stopped it
	script, scriptOk := singleScriptArgvOf(args[1])
	if !scriptOk {
		return nil, false, "this call runs python3, and its argv is not one written string naming " +
			"a script — the checker cannot name the code that runs next, so it models no edge here", call
	}
	if filepath.Ext(script) != ".py" {
		return nil, false, "this call runs python3 on " + script +
			", which is not a .py file — the checker models the edge only where the argv " +
			"names Python source it can read a fact for", call
	}
	sourceFile := ast.GetSourceFileOfNode(call)
	if sourceFile == nil {
		return nil, false, "", nil
	}
	// a RELATIVE argv entry is relative to the file that wrote it: the
	// cwd of the eventual run is deployment, and resolving against it
	// would make the checker's answer depend on where it was invoked
	targetPath := script
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(filepath.Dir(sourceFile.FileName()), script)
	}
	payload, encodingOk, optionsSentence := execFileSyncOptionsOf(args[2])
	if optionsSentence != "" {
		return nil, false, optionsSentence, args[2]
	}
	if !encodingOk {
		return nil, false, "this call runs python3 on " + script + " without a string encoding, " +
			"so its result is a Buffer rather than the target's JSON text — " +
			"the return leg has no text to parse", args[2]
	}
	if payload == nil {
		return nil, false, "this call runs python3 on " + script + " and sends it no " +
			"JSON.stringify(...) input, so nothing crosses out on stdin and the transport " +
			"model has no outbound leg to apply", args[2]
	}
	return &ForeignEdge{
		Call:       call,
		TargetPath: targetPath,
		Payload:    payload,
		StdoutName: name,
	}, true, "", nil
}

// execFileSyncBindingOf reads `const <name> = <call>(...)` — one
// declarator binding one call. A const binding is what lets the return
// leg follow the name to its parse: a `let` the following statements
// could rewrite carries no such guarantee, and declines here.
func execFileSyncBindingOf(statement *ast.Node) (string, *ast.Node, bool) {
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

// resolvesToChildProcessExecFileSync is the callee test: the name is
// execFileSync, and its symbol declares in a DECLARATION FILE whose
// path names child_process. That is the same shape resolvesToDefaultLib
// tests for the built-ins (a symbol's declaring file), widened to the
// one ambient module this edge consumes — `child_process` is not in the
// default lib, it arrives with @types/node, so the default-lib test
// answers false for it and cannot be reused unchanged.
//
// Both spellings resolve: the named import `import { execFileSync } from
// "node:child_process"` (an identifier callee, followed through the
// import alias by symbolAt) and the namespace form
// `childProcess.execFileSync(...)` (a property access, whose NAME node
// carries the same symbol).
func resolvesToChildProcessExecFileSync(ctx *FlowContext, callee *ast.Node) bool {
	if ctx == nil || ctx.P == nil || callee == nil {
		return false
	}
	var name *ast.Node
	if ast.IsIdentifier(callee) {
		name = callee
	} else if ast.IsPropertyAccessExpression(callee) {
		name = callee.AsPropertyAccessExpression().Name()
	} else {
		return false
	}
	if name.Text() != "execFileSync" {
		return false
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return false
	}
	for _, declaration := range symbol.Declarations {
		declaredFile := ast.GetSourceFileOfNode(declaration)
		if declaredFile == nil || !declaredFile.IsDeclarationFile {
			continue
		}
		if isChildProcessDeclarationPath(declaredFile.FileName()) {
			return true
		}
	}
	return false
}

// isChildProcessDeclarationPath is whether a declaration file is
// child_process's: `.../child_process.d.ts` under any @types root. The
// test is on the file's BASE NAME, so a vendored or pnpm-nested copy
// reads the same.
func isChildProcessDeclarationPath(fileName string) bool {
	base := filepath.Base(fileName)
	return base == "child_process.d.ts" || base == "child_process.d.mts" ||
		base == "child_process.d.cts"
}

// singleScriptArgvOf reads `["./audio_level.py"]` — an argv array
// written in place holding exactly one string literal. More elements
// mean the target takes arguments this edge does not model; a spread or
// a variable means the argv is not a written literal, and the checker
// cannot name the code that runs next from it.
func singleScriptArgvOf(argument *ast.Node) (string, bool) {
	array := Unwrapped(argument)
	if array == nil || !ast.IsArrayLiteralExpression(array) {
		return "", false
	}
	elements := array.AsArrayLiteralExpression().Elements.Nodes
	if len(elements) != 1 {
		return "", false
	}
	return stringLiteralText(elements[0])
}

// execFileSyncOptionsOf reads the options object: the `input` property
// whose value is `JSON.stringify(<payload>)`, and the `encoding`
// property whose word makes the result a string.
//
// Answers the payload expression (nil where input is absent or is not a
// stringify), whether the encoding admits a string result, and a
// sentence for the one case that is neither: an options argument the
// reader cannot see into at all.
func execFileSyncOptionsOf(argument *ast.Node) (*ast.Node, bool, string) {
	options := Unwrapped(argument)
	if options == nil || !ast.IsObjectLiteralExpression(options) {
		return nil, false, "this call's execFileSync options are not written out as an object " +
			"literal, so the checker cannot see what crosses on stdin or whether the result " +
			"is text — no edge is modeled here"
	}
	var payload *ast.Node
	encodingOk := false
	for _, property := range options.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			continue
		}
		assignment := property.AsPropertyAssignment()
		key := assignment.Name()
		if key == nil || !ast.IsIdentifier(key) {
			continue
		}
		switch key.Text() {
		case "input":
			if inner, ok := jsonStringifyArgumentOf(assignment.Initializer); ok {
				payload = inner
			}
		case "encoding":
			if word, ok := stringLiteralText(assignment.Initializer); ok && stringEncodings[word] {
				encodingOk = true
			}
		}
	}
	return payload, encodingOk, ""
}

// jsonStringifyArgumentOf reads `JSON.stringify(<expr>)` and answers
// the single argument. The receiver test is by name only here: the
// resolvesToDefaultLib check that would ground it belongs to the
// evaluation of that call, which the payload's own evaluation performs.
func jsonStringifyArgumentOf(expression *ast.Node) (*ast.Node, bool) {
	call := Unwrapped(expression)
	if call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	callee := call.AsCallExpression().Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return nil, false
	}
	access := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != "JSON" ||
		access.Name().Text() != "stringify" {
		return nil, false
	}
	arguments := call.AsCallExpression().Arguments
	if arguments == nil || len(arguments.Nodes) != 1 {
		return nil, false
	}
	return arguments.Nodes[0], true
}

// stringLiteralText is a written string literal's own text — a template
// with no substitution reads the same way, since both spell one fixed
// word.
func stringLiteralText(expression *ast.Node) (string, bool) {
	node := Unwrapped(expression)
	if node == nil {
		return "", false
	}
	if ast.IsStringLiteral(node) || node.Kind == ast.KindNoSubstitutionTemplateLiteral {
		return node.Text(), true
	}
	return "", false
}

/* ── the outbound leg (§4 / §5, discharged) ──────────────────────── */

// checkOutboundLeg discharges every premise about the value that
// crosses OUT, against the value the walk holds for it. Answers nil
// where the leg is clean; an outcome (a decline sentence, or nothing at
// all after a 7001 fired) where it is not.
//
// The premises, each a REAL check:
//
//   - the artifact states an entry position for the payload to fit
//     (no position, no crossing to judge);
//   - NaN-FREEDOM (§4): NaN stringifies to `null`, so a payload that
//     may carry NaN sends the target a value it never wrote. The check
//     is on the value's SHAPE — a PossiblyNaN wrapper anywhere, or a
//     sequence carrying the NaNElements flag, is the obstacle;
//   - the CROSSING FIT: the payload's element set inside the entry's,
//     asked of the kernel (ScalarSubset — a real ask, not a syntactic
//     comparison), and the payload's repetition floor at or above the
//     entry's stated lengthAtLeast.
//
// A FIT FAILURE fires 7001 at the call rather than declining: the
// target states what it admits, the caller can send something else, and
// that is a defect in this program — exactly the shape a refutation
// takes anywhere else in the checker.
func checkOutboundLeg(
	ctx *FlowContext, env Env, edge *ForeignEdge, artifact *ForeignArtifact,
) *ForeignEdgeOutcome {
	if len(artifact.Called.Entry) == 0 {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states no entry position, so " +
				"nothing says what the value crossing out must be",
			DeclineNode: edge.Call,
		}
	}
	// the harness hands the WHOLE parsed stdin value to the called
	// function, so exactly one entry position receives it
	if len(artifact.Called.Entry) != 1 {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states " +
				strconv.Itoa(len(artifact.Called.Entry)) + " entry positions, and this " +
				"harness hands it one JSON value from stdin — the checker models no " +
				"splitting of that value across positions",
			DeclineNode: edge.Call,
		}
	}
	entry := artifact.Called.Entry[0]
	crossing := evaluateExpression(ctx, env, edge.Payload)
	// NaN-FREEDOM (§4): the premise the fixture's own comment names
	if sentence := nanFreedomObstacle(crossing); sentence != "" {
		ctx.Report(assignability.At(edge.Payload, 7001, foreignMessage(
			sentence+" — JSON.stringify writes NaN as null, so "+artifact.Called.Name+
				" would receive a value this program never computed",
			artifact)))
		return &ForeignEdgeOutcome{}
	}
	if entry.IsSequence {
		return checkSequenceCrossing(ctx, edge, artifact, entry, crossing)
	}
	return checkScalarCrossing(ctx, edge, artifact, entry, crossing)
}

// nanFreedomObstacle answers the sentence naming why a value may carry
// NaN across the wire, or "" where the shape excludes it.
//
// The derived sets exclude NaN BY CONSTRUCTION — a RefinedSet denotes a
// subset of the reals, and NaN is a member of no refined set
// (nan_wrapper.go says exactly this). So the check is on the value's
// SHAPE: the two ways NaN rides beside a set are the PossiblyNaN
// wrapper and, for a sequence, the NaNElements flag its element reading
// consults (iteration_element.go). A pinned NaN is the third.
func nanFreedomObstacle(crossing abstractdomain.AbstractValue) string {
	switch {
	case crossing.Kind == abstractdomain.KindNaN:
		return "the value crossing to the Python target is NaN"
	case crossing.Kind == abstractdomain.KindPossiblyNaN:
		return "the value crossing to the Python target may be NaN"
	case crossing.Kind == abstractdomain.KindSet && crossing.NaNElements:
		return "the sequence crossing to the Python target may hold NaN elements"
	}
	return ""
}

// checkSequenceCrossing judges an array payload against a sequence
// entry: the elements inside the stated element set, and the length
// floor at or above the stated one.
func checkSequenceCrossing(
	ctx *FlowContext, edge *ForeignEdge, artifact *ForeignArtifact,
	entry ForeignEntry, crossing abstractdomain.AbstractValue,
) *ForeignEdgeOutcome {
	if crossing.Kind != abstractdomain.KindSet || crossing.SetKindTag != abstractdomain.SetKindTagNone {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits a sequence at " +
				entry.Name + ", and the value crossing out is not read as one here — " +
				"nothing says whether it fits",
			DeclineNode: edge.Payload,
		}
	}
	window, windowOk := refinementsets.AsRepetition(crossing.Set)
	if !windowOk {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits a sequence at " +
				entry.Name + " of at least " + strconv.Itoa(entry.LengthAtLeast) +
				" elements, and the value crossing out states no element set or length " +
				"window — nothing says whether it fits",
			DeclineNode: edge.Payload,
		}
	}
	// the ELEMENT fit — a real kernel ask
	fits, asked := foreignScalarSubset(ctx, window.Element, entry.Element)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the elements crossing out fit " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entry.Element) +
				", so the crossing is not judged",
			DeclineNode: edge.Payload,
		}
	}
	if !fits {
		ctx.Report(assignability.At(edge.Payload, 7001, foreignMessage(
			"the elements crossing to "+artifact.Called.Name+" are "+
				foreignSetWords(window.Element)+", and the target admits "+
				foreignSetWords(entry.Element)+
				" — the value can escape what the target states it accepts",
			artifact)))
		return &ForeignEdgeOutcome{}
	}
	// the LENGTH floor: the target's body relies on it (a division by
	// len, an indexed read), so a shorter sequence is a different program
	if window.Lo < entry.LengthAtLeast {
		ctx.Report(assignability.At(edge.Payload, 7001, foreignMessage(
			"the sequence crossing to "+artifact.Called.Name+" holds at least "+
				strconv.Itoa(window.Lo)+" elements, and the target relies on at least "+
				strconv.Itoa(entry.LengthAtLeast),
			artifact)))
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// checkScalarCrossing judges a scalar payload against a scalar entry —
// the same ScalarSubset ask, without a length to carry.
func checkScalarCrossing(
	ctx *FlowContext, edge *ForeignEdge, artifact *ForeignArtifact,
	entry ForeignEntry, crossing abstractdomain.AbstractValue,
) *ForeignEdgeOutcome {
	crossingSet, ok := abstractdomain.SetOfKnown(crossing)
	if !ok {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits " +
				foreignSetWords(entry.Set) + " at " + entry.Name +
				", and the value crossing out is not read as a set here — " +
				"nothing says whether it fits",
			DeclineNode: edge.Payload,
		}
	}
	fits, asked := foreignScalarSubset(ctx, crossingSet, entry.Set)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the value crossing out fits " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entry.Set) +
				", so the crossing is not judged",
			DeclineNode: edge.Payload,
		}
	}
	if !fits {
		ctx.Report(assignability.At(edge.Payload, 7001, foreignMessage(
			"the value crossing to "+artifact.Called.Name+" is "+
				foreignSetWords(crossingSet)+", and the target admits "+
				foreignSetWords(entry.Set)+
				" — the value can escape what the target states it accepts",
			artifact)))
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// foreignScalarSubset asks the kernel A ⊆ B, answering (fits, asked).
// A refused question answers (false, false) — the same try/catch shape
// nan_wrapper.go's own ScalarSubset call wears, so a kernel that cannot
// decide leaves the crossing unjudged rather than refuting it.
func foreignScalarSubset(
	ctx *FlowContext, a refinementsets.RefinedSet, b refinementsets.RefinedSet,
) (fits bool, asked bool) {
	if ctx == nil || ctx.Kernel == nil || ctx.Kernel.ScalarSubset == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			fits, asked = false, false
		}
	}()
	return ctx.Kernel.ScalarSubset(a, b), true
}

// foreignMessage appends the target's own provenance to a crossing
// refutation — the second step of the two-language explanation, in the
// message-text form. See ForeignProvenance.ProvenanceSentence for the
// named relatedInformation work item this stands in for.
func foreignMessage(said string, artifact *ForeignArtifact) string {
	provenance := artifact.Called.Provenance.ProvenanceSentence()
	if provenance == "" {
		return said
	}
	return said + ". " + provenance
}

/* ── the return leg ──────────────────────────────────────────────── */

// soleParseConsumerOf finds the `JSON.parse(<stdoutName>)` node the
// target's return fact attaches to, scanning the statements AFTER the
// call in the same function — the same same-function, count-the-
// occurrences discipline the relational accumulation's return shape
// uses to find its division.
//
// The declines, each because the fact would land on the wrong value:
//
//   - no parse of the name at all: nothing reads the target's output as
//     JSON here, so there is nothing to attach to;
//   - TWO OR MORE parses: one published fact cannot stand for two
//     nodes, and both would read it;
//   - an intervening WRITE to the name: the value the parse reads is
//     then not the value the call produced. (The recognizer already
//     requires a `const` binding, so this catches the shadowing and
//     reassignment shapes a const cannot prevent by itself.)
//
// A parse inside a NESTED FUNCTION BODY is not counted: that scope runs
// an unstated number of times, so the fact cannot be pinned to one
// evaluation — CollectLocals' own boundary, spelled the same way
// accumulationDivisionsIn spells it.
func soleParseConsumerOf(
	statements []*ast.Node, index int, stdoutName string,
) (*ast.Node, int, string) {
	var found *ast.Node
	foundAt := -1
	count := 0
	written := map[string]struct{}{}
	for offset, statement := range statements[index+1:] {
		// AssignedNamesDirect, not AssignedNames: the question here is
		// whether the TEXT rewrites the binding, and the call-mediated
		// reading would count the very `JSON.parse(stdout)` this route is
		// looking for as a possible write to it
		AssignedNamesDirect(statement, written)
		before := count
		foreignParseCallsIn(statement, stdoutName, &found, &count)
		if foundAt < 0 && count > before {
			foundAt = index + 1 + offset
		}
	}
	if _, moves := written[stdoutName]; moves {
		return nil, -1, "the stdout binding " + stdoutName + " is written after the call, so the " +
			"value parsed is not the value the Python target produced — no fact is attached"
	}
	if count == 0 {
		return nil, -1, "nothing reads " + stdoutName + " through JSON.parse after the call, so the " +
			"target's stated result has no expression to land on"
	}
	if count > 1 {
		return nil, -1, stdoutName + " is parsed " + strconv.Itoa(count) + " times after the call, " +
			"and one stated result cannot stand for more than one expression — no fact is attached"
	}
	return found, foundAt, ""
}

// foreignParseCallsIn counts every `JSON.parse(<name>)` in a statement
// and remembers the first, never descending into a nested function.
func foreignParseCallsIn(node *ast.Node, name string, found **ast.Node, count *int) {
	var visit func(n *ast.Node) bool
	visit = func(n *ast.Node) bool {
		if ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n) {
			return false
		}
		if isForeignParseOf(n, name) {
			if *found == nil {
				*found = n
			}
			*count++
			// the one argument is the bare name — no second occurrence can
			// hide inside it
			return false
		}
		n.ForEachChild(visit)
		return false
	}
	visit(node)
}

// isForeignParseOf is whether a node is exactly `JSON.parse(<name>)`.
func isForeignParseOf(node *ast.Node, name string) bool {
	if node == nil || !ast.IsCallExpression(node) {
		return false
	}
	call := node.AsCallExpression()
	callee := call.Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	access := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != "JSON" ||
		access.Name().Text() != "parse" {
		return false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return false
	}
	argument := Unwrapped(call.Arguments.Nodes[0])
	return argument != nil && ast.IsIdentifier(argument) && argument.Text() == name
}
