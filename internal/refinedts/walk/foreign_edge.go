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
// every crossing shape applies (pure stdin above, pure argv-scalar, the
// MIXED shape carrying one value on each channel at once, and the
// FILE-CARRIED shape whose value is written to a file and read back
// through the same JSON model, only relocated); §5 is the list of
// premises the crossing rests on.
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
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// foreignArgvIndexModeled is the one argv position this edge reads a
// scalar (argv-scalar, the mixed shape's argv leg) or a path (file-json)
// from — schema-v2.md's own argIndex field, and this is the only value
// this reader was built against: argv[1], the position a plain
// interpreter's two-element argv (`[<script>, <data>]`) puts the second
// element at. A surface naming any other index is recognized-and-
// declined by name, not silently accepted.
const foreignArgvIndexModeled = 1

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
// call is, which .py file it names, which expression(s) cross out, and
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
	// crosses out on stdin (the ordinary and mixed shapes), or the value
	// written to the file FilePath names (the file-carried shape). Nil
	// where nothing stringifies at all (the pure argv-scalar shape).
	Payload *ast.Node
	// ArgvValue: the argv element carrying the crossing value ITSELF —
	// set for the two-element python argv shape (`[<script>, <data>]`,
	// pure argv-scalar) and for the mixed shape's own argv leg (both
	// Payload and ArgvValue set together). Nil for the ordinary
	// stdin-only and file-carried shapes, where the crossing value is
	// not the argv element's own text.
	ArgvValue *ast.Node
	// FilePath: the argv element NAMING a file — set only for the
	// file-carried shape, where the data itself is written to that path
	// by a preceding writeFileSync(FilePath, JSON.stringify(Payload))
	// statement and the target reads it back off disk, never off argv's
	// own text. Nil for every other shape. Exactly one of {ArgvValue,
	// FilePath} is ever set alongside a non-nil Payload for a recognized
	// mixed or file-carried edge; the pure shapes set neither.
	FilePath *ast.Node
	// StdoutName: the name the call's result binds, whose sole
	// JSON.parse consumer receives the return fact.
	StdoutName string
}

// SpawnReturnLeg is a RECOGNIZED spawn (async) return leg: the
// accumulator name the 'data' handler writes into and the JSON.parse
// node the 'close' handler reads it through. Recognizing this pair is
// only stage one — nothing here SERVES the fact yet (no NodeOverrides
// wiring, no callback-body walk route); a caller that finds this leg
// still names the same "not yet served" construct spawnAsyncEdgeOf's
// decline sentence always named, just narrower: the accumulator and
// the parse node are no longer missing, only the walk that would carry
// a fact onto ParseNode.
type SpawnReturnLeg struct {
	// AccumulatorName: the name the 'data' handler's `+=` writes, read
	// back by the 'close' handler's JSON.parse.
	AccumulatorName string
	// DataHandler: the 'data' callback whose body owns the `+=`.
	DataHandler *ast.Node
	// CloseHandler: the 'close' callback whose body owns the parse.
	CloseHandler *ast.Node
	// ParseNode: the `JSON.parse(<AccumulatorName>)` node inside
	// CloseHandler's body — where a served fact would attach, once a
	// callback-body walk route exists to reach it.
	ParseNode *ast.Node
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
	// TargetPath: the resolved foreign file this edge names, set on
	// every returned outcome (fired, declined, or served) — "consumed"
	// means the check looked at that file, not that it approved of what
	// it found. Read back through FlowContext.ConsumedForeignSink.
	TargetPath string
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
	edge, recognized, declineSentence, declineNode, targetPath := foreignEdgeOf(ctx, statements, index)
	if !recognized {
		if declineSentence == "" {
			return nil, false
		}
		// the call WAS an execFileSync-to-python invocation and something
		// about its spelling stopped the resolution — say which
		return &ForeignEdgeOutcome{Decline: declineSentence, DeclineNode: declineNode, TargetPath: targetPath}, true
	}
	artifact, artifactSentence := ReadForeignArtifact(edge.TargetPath)
	if artifactSentence != "" {
		return &ForeignEdgeOutcome{Decline: artifactSentence, DeclineNode: edge.Call, TargetPath: edge.TargetPath}, true
	}
	// the OUTBOUND leg: every §4/§5 premise about what crosses out,
	// discharged against the value the walk holds for it
	if outcome := checkOutboundLeg(ctx, env, edge, artifact); outcome != nil {
		outcome.TargetPath = edge.TargetPath
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
			TargetPath:  edge.TargetPath,
		}, true
	}
	// the RETURN leg: the target's own fact, attached to the parse
	parse, at, parseSentence := soleParseConsumerOf(statements, index, edge.StdoutName)
	if parseSentence != "" {
		return &ForeignEdgeOutcome{Decline: parseSentence, DeclineNode: edge.Call, TargetPath: edge.TargetPath}, true
	}
	// THE ±INFINITY CORNER (§4): `JSON.stringify` writes a JS number's
	// ±Infinity as the bare token `null` on the OUTBOUND leg (checked
	// above, nanFreedomObstacle's own premise); on this INBOUND leg the
	// hazard is the mirror and worse — the TARGET is Python, and
	// `json.dumps(float("inf"))` emits the bare token `Infinity`, which
	// is not legal JSON at all. `JSON.parse` of that text THROWS at
	// runtime. A return set admitting either corner is a claim this
	// transport cannot carry, so the fact never binds: it degrades to
	// the same named-undetermined channel every other undischarged
	// premise in this file uses.
	if sentence := foreignReturnCornerObstacle(ctx, artifact.Called.Return.Set); sentence != "" {
		return &ForeignEdgeOutcome{
			Decline:     "the target " + artifact.Called.Name + "'s stated return " + sentence,
			DeclineNode: parse,
			TargetPath:  edge.TargetPath,
		}, true
	}
	return &ForeignEdgeOutcome{
		Override: map[*ast.Node]abstractdomain.AbstractValue{
			parse: foreignReturnValue(artifact),
		},
		OverrideStatement: at,
		TargetPath:        edge.TargetPath,
	}, true
}

// foreignReturnCornerObstacle asks the kernel whether returnSet admits
// +Infinity or -Infinity as a MEMBER — the real x ∈ A ask
// (kernelbridge.RefinedTSKernel.Member, proved: memberB_iff), the same
// idiom effect_math_test.go's own Math.min/max corner checks and
// kernel_bridge_test.go's ℝ̄∖{0} row already ask of a live kernel,
// reused here rather than a fresh inspection of the set's forms. A
// refused or unavailable question (no kernel loaded, a panic inside the
// ask) recovers to "" — the SAME "no proof, no obstacle" reading
// foreignScalarSubset and subsetProved already give a refused question,
// so an untested corner never falsely degrades a set the kernel simply
// could not answer for.
func foreignReturnCornerObstacle(ctx *FlowContext, returnSet refinementsets.RefinedSet) (sentence string) {
	if ctx == nil || ctx.Kernel == nil || ctx.Kernel.Member == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			sentence = ""
		}
	}()
	if ctx.Kernel.Member(returnSet, []float64{math.Inf(1)}) {
		return "admits Infinity, which the JSON stdout leg cannot carry — " +
			"json.dumps(float(\"inf\")) writes the bare token Infinity, which is not legal JSON, " +
			"and JSON.parse throws on it — the crossing cannot be trusted at that corner"
	}
	if ctx.Kernel.Member(returnSet, []float64{math.Inf(-1)}) {
		return "admits -Infinity, which the JSON stdout leg cannot carry — " +
			"json.dumps(float(\"-inf\")) writes the bare token -Infinity, which is not legal JSON, " +
			"and JSON.parse throws on it — the crossing cannot be trusted at that corner"
	}
	return ""
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
//
// Called only once foreignReturnCornerObstacle has cleared the set of
// both infinite corners — a set that reaches here binds exactly as it
// always did.
func foreignReturnValue(artifact *ForeignArtifact) abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		artifact.Called.Return.Set, nil,
		abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

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
		// against; nothing crosses out at all
		return nil, false, "this call runs " + runnerWord + " on " + script + " and sends it no " +
			"JSON.stringify(...) input, so nothing crosses out on stdin and the transport " +
			"model has no outbound leg to apply", args[2], resolvedPath
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

// spawnAsyncEdgeOf is `spawn(<runner>, <argv>)` — async, no captured
// return value at the call site itself. The reference (the argv naming
// the script) is exactly as followable as execFileSync's; the call
// itself is recognized on that basis alone.
//
// What the call ALONE cannot yet determine is the return leg, and this
// answers "does not determine (yet)", never "cannot": an accumulate-
// then-parse spawn body —
//
//	const child = spawn(<runner>, <argv>);
//	let out = "";
//	child.stdout.on("data", (d) => { out += d; });
//	child.on("close", () => { const result = JSON.parse(out); ... });
//
// — DOES deterministically name both the file that runs next (the
// argv, exactly as execFileSync's does) and the expression the target's
// fact would attach to (the JSON.parse inside the close handler): nothing
// about the shape is dynamic or unbounded. spawnReturnLegOf (below) now
// reads exactly that pair out of the statements following this call —
// the accumulator name and the parse node are RECOGNIZED. What still
// blocks serving is not the recognition: it is a way for listWalk's
// override (analyze_statement.go's `foreignOverrideAt`) to reach a node
// INSIDE a callback body rather than only a top-level statement, since
// the override today pins the whole statement CONTAINING the parse, and
// here that statement is the `.on("close", cb)` call, not the parse
// expression itself — walking that statement has to actually enter the
// callback body for the pinned node to ever be visited, and no
// callback-body-walking route exists for an arbitrary `.on()` handler
// today (only Promise .then/.catch handlers inline, via
// promiseRunHandler/InlineCallback in promise_instance_models.go). That
// route is analyze_statement.go's to build, outside this file's
// ownership; a body this reader cannot recognize at all (no pair, a
// mismatched parameter, an intervening write) still owes its own,
// narrower sentence naming exactly what breaks the recognition itself.
func spawnAsyncEdgeOf(
	ctx *FlowContext, call *ast.Node, name string, statements []*ast.Node, index int,
) (*ForeignEdge, bool, string, *ast.Node, string) {
	args, _ := callArguments(call)
	if len(args) < 2 {
		return nil, false, "", nil, ""
	}
	runnerWord, script, _, scriptOk, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
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
	leg, legSentence := spawnReturnLegOf(statements, index, name)
	if legSentence == "" {
		// the pair IS recognized (the accumulator and the parse node are
		// both named) — what remains is the callback-body walk route, not
		// this reader's own gap
		return nil, false, "this call runs " + runnerWord + " on " + script + " with spawn — the argv names " +
			"the script exactly as execFileSync's does, and the accumulate-then-parse pair after it is " +
			"recognized (the accumulator " + leg.AccumulatorName + " and its JSON.parse are both named), " +
			"but this checker has no route that walks a callback body to attach a fact to a node inside " +
			"it — the edge is recognized and not yet served",
			call, resolvedPath
	}
	return nil, false, "this call runs " + runnerWord + " on " + script + " with spawn — the argv names " +
		"the script exactly as execFileSync's does, but its result does not determine (yet): " + legSentence,
		call, resolvedPath
}

/* ── the spawn (async) return leg: the accumulate-then-parse `.on()` pair ── */

// spawnReturnLegOf scans the statements AFTER a `spawn(...)` call bound
// to childName for the accumulate-then-parse pair —
//
//	<childName>.stdout.on("data", (d) => { out += d; });
//	<childName>.on("close", () => { ... JSON.parse(out) ... });
//
// — the same "scan the statements after this one" discipline
// soleParseConsumerOf already applies, one level deeper: past finding
// the two `.on()` calls, it steps INTO each handler's body to read the
// accumulator name (spawnAccumulatorNameOf) and the parse node
// (spawnParseNodeOf) the same way soleParseConsumerOf reads a top-level
// JSON.parse.
//
// Answers (leg, "") when the whole pair recognizes; ("", said)
// otherwise, naming the first construct that blocks it. A missing
// 'close' handler, a 'data' handler whose accumulator write does not
// read the handler's OWN parameter, or a write to the accumulator by a
// THIRD statement outside the two handlers all decline by name here —
// none of them are silent.
func spawnReturnLegOf(statements []*ast.Node, index int, childName string) (SpawnReturnLeg, string) {
	dataHandler, dataOk := spawnOnHandlerOf(statements, index, childName, true, "data")
	if !dataOk {
		return SpawnReturnLeg{}, "no " + childName + ".stdout.on(\"data\", ...) handler follows the call, " +
			"so there is no accumulator for a return fact to attach through"
	}
	accumulatorName, accumulatorOk := spawnAccumulatorNameOf(dataHandler)
	if !accumulatorOk {
		return SpawnReturnLeg{}, "the 'data' handler's body is not a single `<name> += <chunk>` statement " +
			"adding its own parameter into an outer name, so no accumulator is named"
	}
	closeHandler, closeOk := spawnOnHandlerOf(statements, index, childName, false, "close")
	if !closeOk {
		return SpawnReturnLeg{}, "no " + childName + ".on(\"close\", ...) handler follows the call, so " +
			"nothing reads " + accumulatorName + " back as the target's result"
	}
	parseNode, parseOk := spawnParseNodeOf(closeHandler, accumulatorName)
	if !parseOk {
		return SpawnReturnLeg{}, "the 'close' handler's body does not read " + accumulatorName +
			" through JSON.parse, so the target's stated result has no expression to land on"
	}
	// the intervening-write hazard: the 'data' handler's OWN `+=` is part
	// of the recognized shape, not a disqualifying write — every OTHER
	// statement between the call and the 'close' handler, and every
	// statement inside the 'close' handler's own body, must leave the
	// accumulator untouched
	if spawnAccumulatorWriteOutsideHandlers(statements, index, accumulatorName, dataHandler) {
		return SpawnReturnLeg{}, "a statement other than the 'data' handler writes " + accumulatorName +
			" after the call, so the value the 'close' handler parses is not the value the two " +
			"handlers alone accumulated"
	}
	return SpawnReturnLeg{
		AccumulatorName: accumulatorName,
		DataHandler:     dataHandler,
		CloseHandler:    closeHandler,
		ParseNode:       parseNode,
	}, ""
}

// spawnOnHandlerOf finds `<childName>.on(event, cb)` — or, when
// throughStdout is true, `<childName>.stdout.on(event, cb)` — among the
// expression-statement calls following index, and answers cb. These
// calls are bare expression statements (EventEmitter#on returns the
// emitter for chaining, but nothing here reads that return value), so
// they never enter constBoundCallOf's const-bound switch; this reads
// the shape directly.
func spawnOnHandlerOf(
	statements []*ast.Node, index int, childName string, throughStdout bool, event string,
) (*ast.Node, bool) {
	for _, statement := range statements[index+1:] {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		call := Unwrapped(statement.AsExpressionStatement().Expression)
		if call == nil || !ast.IsCallExpression(call) {
			continue
		}
		expr := call.AsCallExpression()
		callee := Unwrapped(expr.Expression)
		if callee == nil || !ast.IsPropertyAccessExpression(callee) {
			continue
		}
		access := callee.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "on" {
			continue
		}
		receiver := Unwrapped(access.Expression)
		if receiver == nil {
			continue
		}
		if throughStdout {
			if !ast.IsPropertyAccessExpression(receiver) {
				continue
			}
			stdoutAccess := receiver.AsPropertyAccessExpression()
			if stdoutAccess.QuestionDotToken != nil || !ast.IsIdentifier(stdoutAccess.Name()) ||
				stdoutAccess.Name().Text() != "stdout" {
				continue
			}
			receiver = Unwrapped(stdoutAccess.Expression)
			if receiver == nil {
				continue
			}
		}
		if !ast.IsIdentifier(receiver) || receiver.Text() != childName {
			continue
		}
		if expr.Arguments == nil || len(expr.Arguments.Nodes) != 2 {
			continue
		}
		eventWord, eventOk := stringLiteralText(expr.Arguments.Nodes[0])
		if !eventOk || eventWord != event {
			continue
		}
		handler := Unwrapped(expr.Arguments.Nodes[1])
		if handler == nil || (!ast.IsArrowFunction(handler) && !ast.IsFunctionExpression(handler)) {
			continue
		}
		return handler, true
	}
	return nil, false
}

// spawnAccumulatorNameOf reads the 'data' handler's body as exactly one
// statement, `<name> += <param>;`, where the right side is a bare read
// of the handler's OWN FIRST PARAMETER — the specific parameter, never
// a same-spelled identifier from an outer scope: a handler whose body
// happens to add some other in-reach `d` is not this shape, so the
// check is the parameter DECLARATION node's own identity (by position),
// not a name-equality test against the parameter's spelled text.
func spawnAccumulatorNameOf(handler *ast.Node) (string, bool) {
	parameters := handler.Parameters()
	if len(parameters) != 1 {
		return "", false
	}
	parameterName := parameters[0].AsParameterDeclaration().Name()
	if parameterName == nil || !ast.IsIdentifier(parameterName) {
		return "", false
	}
	body := StatementsOf(handler.Body())
	if len(body) != 1 || !ast.IsExpressionStatement(body[0]) {
		return "", false
	}
	add := Unwrapped(body[0].AsExpressionStatement().Expression)
	if add == nil || !ast.IsBinaryExpression(add) {
		return "", false
	}
	bin := add.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindPlusEqualsToken {
		return "", false
	}
	target := Unwrapped(bin.Left)
	if target == nil || !ast.IsIdentifier(target) {
		return "", false
	}
	// the accumulator must be a DIFFERENT binding than the parameter —
	// summing the chunk into itself writes back nothing an outer scope
	// can read
	if target.Text() == parameterName.Text() {
		return "", false
	}
	right := Unwrapped(bin.Right)
	if right == nil || !ast.IsIdentifier(right) || right.Text() != parameterName.Text() {
		return "", false
	}
	return target.Text(), true
}

// spawnParseNodeOf finds the sole `JSON.parse(<accumulatorName>)` node
// inside the 'close' handler's body — the same read isForeignParseOf
// already performs for a bare stdout binding, applied to the accumulator
// name instead. Unlike soleParseConsumerOf's top-level scan, this reads
// exactly ONE handler body rather than a run of statements, so there is
// no "two or more consumers" question here: the handler either contains
// one parse of the name or it does not.
func spawnParseNodeOf(closeHandler *ast.Node, accumulatorName string) (*ast.Node, bool) {
	body := closeHandler.Body()
	if body == nil {
		return nil, false
	}
	var found *ast.Node
	count := 0
	foreignParseCallsIn(body, accumulatorName, &found, &count)
	if count != 1 {
		return nil, false
	}
	return found, true
}

// spawnAccumulatorWriteOutsideHandlers is the intervening-write hazard
// check: whether any statement OTHER than the 'data' handler's own
// recognized `+=` writes the accumulator between the spawn call and the
// end of the scan — a statement in between the two handlers, a
// statement after the 'close' handler in the same list, or a write
// inside the 'close' handler's own body (AssignedNamesDirect walks INTO
// a callback literal same as any other subtree, so a write inside
// CloseHandler's body is already covered by the same scan). The 'data'
// handler's own statement is the one excluded: its `+=` is the
// recognized shape, not a disqualifying write.
func spawnAccumulatorWriteOutsideHandlers(
	statements []*ast.Node, index int, accumulatorName string, dataHandler *ast.Node,
) bool {
	written := map[string]struct{}{}
	for _, statement := range statements[index+1:] {
		if spawnStatementIsHandlerFor(statement, dataHandler) {
			continue
		}
		AssignedNamesDirect(statement, written)
	}
	_, moves := written[accumulatorName]
	return moves
}

// spawnStatementIsHandlerFor is whether statement is the bare
// expression-statement call that hands handler as one of its arguments
// — the recognized `.on(...)` statement itself, excluded from the
// write scan because its own `+=` is the shape being recognized, not a
// write to disqualify it.
func spawnStatementIsHandlerFor(statement *ast.Node, handler *ast.Node) bool {
	if !ast.IsExpressionStatement(statement) {
		return false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if call == nil || !ast.IsCallExpression(call) {
		return false
	}
	expr := call.AsCallExpression()
	if expr.Arguments == nil {
		return false
	}
	for _, argument := range expr.Arguments.Nodes {
		if Unwrapped(argument) == handler {
			return true
		}
	}
	return false
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

// resolvesToChildProcessMember is the callee test, widened past
// execFileSync alone: the name is memberName, and its symbol declares
// in a DECLARATION FILE whose path names child_process. That is the
// same shape resolvesToDefaultLib tests for the built-ins (a symbol's
// declaring file), widened to the one ambient module this edge
// consumes — `child_process` is not in the default lib, it arrives
// with @types/node, so the default-lib test answers false for it and
// cannot be reused unchanged.
//
// Both spellings resolve: the named import `import { <memberName> } from
// "node:child_process"` (an identifier callee, followed through the
// import alias by symbolAt) and the namespace form
// `childProcess.<memberName>(...)` (a property access, whose NAME node
// carries the same symbol). spawnSync and execSync are the same
// child_process export shape as execFileSync, so one test serves all
// three names.
func resolvesToChildProcessMember(ctx *FlowContext, callee *ast.Node, memberName string) bool {
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
	if name.Text() != memberName {
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
func runnerAndScriptArgvOf(ctx *FlowContext, runnerArgument *ast.Node, argvArgument *ast.Node) (runnerWord string, script string, dataElement *ast.Node, ok bool, sentence string, sentenceNode *ast.Node) {
	interpreter, interpreterOk := stringLiteralText(runnerArgument)
	if !interpreterOk {
		// the runner word itself is not a written literal (a variable, a
		// computed expression) — the reader cannot yet see this is python
		// at all, so nothing is owed
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

// scriptElementOf reads one argv element as the script's own path: a
// written string literal (or no-substitution template) directly, or —
// past this pass — an IDENTIFIER resolved through resolvedConstStringLiteral
// to its const initializer's own literal. Anything else that is not a
// plain written literal (a parameter reference, a computed expression
// such as string concatenation) is a script path the checker can SEE
// but cannot yet name: recognized, not silent, carrying the law-2
// sentence naming exactly what would make it resolvable.
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
	}
	// past this point the reader knows there IS a script argv element —
	// it is simply not one the checker can read a name from
	return "", false, scriptPathLawTwoSentence, node
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
	if !literalOk {
		return nil, false, execSyncShellStringSentence, args[0], ""
	}
	runnerWord, script, tokensOk := execSyncSimpleCommandTokens(command)
	if !tokensOk {
		return nil, false, execSyncShellStringSentence, args[0], ""
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
		Payload:    nil,
		StdoutName: name,
	}, true, "", nil, resolvedPath
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

// execFileSyncOptionsOf reads the options object shared by execFileSync,
// spawnSync, and execSync's own second argument: the `input` property
// whose value is `JSON.stringify(<payload>)`, and the `encoding`
// property whose word makes the result a string. execSync's own
// command string carries no `input` — it reads only for the encoding
// half there, and its payload comes from `child.stdin.write` instead
// where a caller writes one.
//
// Answers the payload expression (nil where input is absent or is not a
// stringify), whether the encoding admits a string result, and a
// sentence for the one case that is neither: an options argument the
// reader cannot see into at all.
func execFileSyncOptionsOf(argument *ast.Node) (*ast.Node, bool, string) {
	options := Unwrapped(argument)
	if options == nil || !ast.IsObjectLiteralExpression(options) {
		return nil, false, "this call's options are not written out as an object " +
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
// A recognized edge crosses on one of FOUR channel shapes — pure stdin
// (edge.Payload alone), pure argv-scalar (edge.ArgvValue alone), the
// mixed shape (edge.Payload AND edge.ArgvValue both set), or the
// file-carried shape (edge.Payload with edge.FilePath set) — and the
// FIRST premise, before any of the ones below, is that the crossing
// channel MEETS the target's own stated surface: a call on one shape at
// a target whose surface serves another is a channel mismatch, declined
// by name (checkArgvCrossing, checkMixedCrossing, and checkFileCrossing
// each carry their own channel match; the mirror for the pure-stdin
// path lives here).
//
// The stdin leg's own premises past the channel match, each a REAL
// check — shared verbatim by the pure-stdin, mixed, and file-carried
// shapes through stdinFitAgainst:
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
	switch {
	case edge.ArgvValue != nil && edge.Payload != nil:
		// the MIXED shape: both legs carry a value — channel match first
		// (a mixed call only fits a mixed surface), then each leg's own
		// existing fit chain, independently
		return checkMixedCrossing(ctx, env, edge, artifact)
	case edge.ArgvValue != nil:
		// the crossing rides on argv alone, not stdin — a DIFFERENT
		// inbound channel with its own premises (channel match, then the
		// written-literal/parse/fit chain), never the stdin NaN/subset
		// path below
		return checkArgvCrossing(ctx, edge, artifact)
	case edge.FilePath != nil:
		// the crossing rides on a FILE named in argv — the carrier
		// differs, but the payload crosses through the same stdin fit
		// chain once the channel match holds
		return checkFileCrossing(ctx, env, edge, artifact)
	}
	// the MIRROR channel mismatch: a call sending its value through
	// `input: JSON.stringify(...)` at a target whose surface reads argv
	// alone — symmetric with checkArgvCrossing's own channel check
	if artifact.Surface == ForeignSurfaceArgvScalar {
		return &ForeignEdgeOutcome{
			Decline: "the call passes the value as JSON on stdin, but the target's fact serves an argv[" +
				strconv.Itoa(artifact.ArgvIndex) + "] scalar — the channels do not meet",
			DeclineNode: edge.Payload,
		}
	}
	if artifact.Surface == ForeignSurfaceMixedStdinArgv {
		return &ForeignEdgeOutcome{
			Decline: "the call passes only the value as JSON on stdin, but the target's fact serves a mixed " +
				"surface reading a SECOND value from argv[" + strconv.Itoa(artifact.ArgvIndex) +
				"] as well — the channels do not meet: the argv leg is absent",
			DeclineNode: edge.Payload,
		}
	}
	if artifact.Surface == ForeignSurfaceFileJSON {
		return &ForeignEdgeOutcome{
			Decline: "the call passes the value as JSON on stdin, but the target's fact serves a file-json " +
				"surface reading the value from a file named at argv[" + strconv.Itoa(artifact.ArgvIndex) +
				"] — the channels do not meet",
			DeclineNode: edge.Payload,
		}
	}
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
	return stdinFitAgainst(ctx, env, edge.Payload, artifact, artifact.Called.Entry[0])
}

// stdinFitAgainst is the stdin leg's own fit chain, past the channel
// match and the entry-position count: NaN-freedom (§4), then the
// sequence or scalar crossing fit. checkOutboundLeg's pure-stdin path
// and checkMixedCrossing's stdin leg (entry[0]) both fit through this
// SAME function.
func stdinFitAgainst(
	ctx *FlowContext, env Env, payload *ast.Node, artifact *ForeignArtifact, entry ForeignEntry,
) *ForeignEdgeOutcome {
	crossing := evaluateExpression(ctx, env, payload)
	// NaN-FREEDOM (§4): the premise the fixture's own comment names.
	// This runs BEFORE the shape gates because the obstacle only speaks
	// for readings that DERIVE a NaN path — a pinned NaN, the
	// possibly-NaN wrapper, or a sequence reading whose elements admit
	// NaN — and each of those is a real stringify hazard regardless of
	// which entry shape receives it.
	if sentence := nanFreedomObstacle(crossing); sentence != "" {
		ctx.Report(foreignRefutation(payload,
			sentence+" — JSON.stringify writes NaN as null, so "+artifact.Called.Name+
				" would receive a value this program never computed",
			artifact))
		return &ForeignEdgeOutcome{}
	}
	if entry.IsSequence {
		return checkSequenceCrossing(ctx, payload, artifact, entry, crossing)
	}
	return checkScalarCrossing(ctx, payload, artifact, entry, crossing)
}

// checkMixedCrossing discharges the mixed shape's own premises, in
// order:
//
//   - CHANNEL MATCH: the target's surface must itself be the mixed
//     stdin-json-argv-scalar shape, at the modeled argv index — a mixed
//     CALL at any other surface declines with today's "checker models
//     one inbound channel per call" reading extended to name the surface
//     it actually found (a single-channel surface, or the wrong argv
//     index), never silently picking one leg;
//   - the target's entry must state EXACTLY TWO positions — entry[0] for
//     the stdin leg, entry[1] for the argv leg — since the harness hands
//     the mixed call's two values to exactly those two positions;
//   - EACH LEG's OWN fit, independently: the stdin leg through
//     stdinFitAgainst (against entry[0], the SAME function the pure
//     stdin shape uses), the argv leg through argvScalarFitAgainst
//     (against entry[1], the SAME function the pure argv-scalar shape
//     uses). A refutation on one leg fires its own 7001 with its own
//     sentence; the other leg is still judged, since the two crossings
//     are independent values with independent fates.
func checkMixedCrossing(
	ctx *FlowContext, env Env, edge *ForeignEdge, artifact *ForeignArtifact,
) *ForeignEdgeOutcome {
	if artifact.Surface != ForeignSurfaceMixedStdinArgv {
		return &ForeignEdgeOutcome{
			Decline: "this call sends a value on BOTH stdin (JSON.stringify(...) input) and argv[" +
				strconv.Itoa(foreignArgvIndexModeled) + "], but the target's fact serves " +
				string(artifact.Surface) + ", not the mixed stdin-json-argv-scalar surface — " +
				"the channels do not meet",
			DeclineNode: edge.Call,
		}
	}
	if artifact.ArgvIndex != foreignArgvIndexModeled {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states its mixed surface's argv leg at index " +
				strconv.Itoa(artifact.ArgvIndex) + ", and this call's data element sits at argv[" +
				strconv.Itoa(foreignArgvIndexModeled) + "] — the channels do not meet",
			DeclineNode: edge.ArgvValue,
		}
	}
	if len(artifact.Called.Entry) != 2 {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states " +
				strconv.Itoa(len(artifact.Called.Entry)) + " entry positions, and the mixed surface " +
				"hands it exactly two values (stdin, then argv) — the checker models no other split",
			DeclineNode: edge.Call,
		}
	}
	stdinOutcome := stdinFitAgainst(ctx, env, edge.Payload, artifact, artifact.Called.Entry[0])
	argvOutcome := argvScalarFitAgainst(ctx, edge.ArgvValue, artifact, artifact.Called.Entry[1])
	// each leg's own refutation already reported through ctx.Report as it
	// ran; a non-nil DECLINE outcome from either leg still needs to reach
	// the caller (a decline is not reported, only returned) — the stdin
	// leg's decline takes precedence only because there is exactly one
	// Decline slot to carry back, never because the argv leg went unjudged
	if stdinOutcome != nil && stdinOutcome.Decline != "" {
		return stdinOutcome
	}
	if argvOutcome != nil && argvOutcome.Decline != "" {
		return argvOutcome
	}
	if stdinOutcome != nil || argvOutcome != nil {
		// at least one leg fired its own 7001 (or both did) — nothing left
		// to publish, exactly as the pure-channel paths answer after a fire
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// checkFileCrossing discharges the file-carried shape's own premises,
// in order:
//
//   - CHANNEL MATCH: the target's surface must itself be file-json, at
//     the modeled argv index — a call whose data crosses through a
//     written file at any other surface declines by name;
//   - the target's entry must state EXACTLY ONE position — the file's
//     own JSON content, read as one value exactly as stdin-json's single
//     entry is;
//   - the PAYLOAD FIT: the SAME stdinFitAgainst function the pure stdin
//     shape uses, against entry[0] — the carrier (a file instead of
//     stdin) is the only difference; the JSON transport model itself,
//     and every premise it carries (NaN-freedom, the element/length
//     fit), is shared unchanged.
func checkFileCrossing(
	ctx *FlowContext, env Env, edge *ForeignEdge, artifact *ForeignArtifact,
) *ForeignEdgeOutcome {
	if artifact.Surface != ForeignSurfaceFileJSON {
		return &ForeignEdgeOutcome{
			Decline: "this call writes its value to a file and names that file at argv[" +
				strconv.Itoa(foreignArgvIndexModeled) + "], but the target's fact serves " +
				string(artifact.Surface) + ", not the file-json surface — the channels do not meet",
			DeclineNode: edge.Call,
		}
	}
	if artifact.ArgvIndex != foreignArgvIndexModeled {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states its file-json surface at argv index " +
				strconv.Itoa(artifact.ArgvIndex) + ", and this call names the file at argv[" +
				strconv.Itoa(foreignArgvIndexModeled) + "] — the channels do not meet",
			DeclineNode: edge.FilePath,
		}
	}
	if len(artifact.Called.Entry) != 1 {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states " +
				strconv.Itoa(len(artifact.Called.Entry)) + " entry positions, and the file-json " +
				"surface hands it one JSON value read from the file — the checker models no " +
				"splitting of that value across positions",
			DeclineNode: edge.Call,
		}
	}
	return stdinFitAgainst(ctx, env, edge.Payload, artifact, artifact.Called.Entry[0])
}

// checkArgvCrossing discharges the argv-scalar leg's own premises, in
// order:
//
//   - CHANNEL MATCH: the target's surface must itself be argv-scalar at
//     the modeled index — a call passing argv data at a target whose
//     surface serves JSON on stdin (or names a different argv index) is
//     a channel mismatch, declined by name, symmetric with the stdin
//     leg's own "no JSON.stringify input" decline;
//   - the value must be a WRITTEN STRING LITERAL (directly, or resolved
//     through a const binding — the same follow scriptElementOf already
//     performs for a script path) — anything else is recognized but
//     unpinnable, since only a literal's parsed value can be computed
//     without running Python;
//   - the literal must PARSE as a Python float() — a parse failure is a
//     fire at the call, not a decline: the value cannot arrive at all;
//   - NaN-FREEDOM: "nan" parses to a real NaN under float() exactly as
//     it does in JS, and the entry set — like every derived set — admits
//     no NaN member, so a NaN literal fires through the same obstacle
//     the stdin leg routes through;
//   - the FIT: the parsed value's exact singleton inside the entry's
//     stated set, asked of the kernel exactly as checkScalarCrossing
//     asks it.
func checkArgvCrossing(ctx *FlowContext, edge *ForeignEdge, artifact *ForeignArtifact) *ForeignEdgeOutcome {
	switch artifact.Surface {
	case ForeignSurfaceArgvScalar:
		// the one surface this leg's fit chain judges — fall through
	case ForeignSurfaceStdinJSON:
		return &ForeignEdgeOutcome{
			Decline: "the call passes the value as argv[1], but the target's fact serves JSON on stdin — " +
				"the channels do not meet",
			DeclineNode: edge.ArgvValue,
		}
	case ForeignSurfaceMixedStdinArgv:
		return &ForeignEdgeOutcome{
			Decline: "the call passes only the value as argv[1], but the target's fact serves a mixed " +
				"surface reading a SECOND value from stdin as well — the channels do not meet: " +
				"the stdin leg is absent",
			DeclineNode: edge.ArgvValue,
		}
	default:
		return &ForeignEdgeOutcome{
			Decline: "the call passes the value as argv[1], but the target's fact serves " +
				string(artifact.Surface) + ", not an argv-scalar surface — the channels do not meet",
			DeclineNode: edge.ArgvValue,
		}
	}
	if artifact.ArgvIndex != foreignArgvIndexModeled {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states its argv-scalar surface at index " +
				strconv.Itoa(artifact.ArgvIndex) + ", and this call's data element sits at argv[" +
				strconv.Itoa(foreignArgvIndexModeled) + "] — the channels do not meet",
			DeclineNode: edge.ArgvValue,
		}
	}
	if len(artifact.Called.Entry) != 1 {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " states " +
				strconv.Itoa(len(artifact.Called.Entry)) + " entry positions, and the argv-scalar " +
				"surface hands it one value — the checker models no splitting of that value across positions",
			DeclineNode: edge.Call,
		}
	}
	return argvScalarFitAgainst(ctx, edge.ArgvValue, artifact, artifact.Called.Entry[0])
}

// argvScalarFitAgainst is the argv-scalar leg's own fit chain, past the
// channel match and the entry-position count: a written literal → a
// Python float() parse → NaN-freedom → the singleton-subset ask.
// checkArgvCrossing (the pure argv-scalar surface, entry[0]) and the
// mixed surface's argv leg (entry[1]) both fit through this SAME
// function — the chain does not change shape depending on which entry
// position it is judging.
func argvScalarFitAgainst(
	ctx *FlowContext, argvValue *ast.Node, artifact *ForeignArtifact, entry ForeignEntry,
) *ForeignEdgeOutcome {
	if entry.IsSequence {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits a sequence at " + entry.Name +
				", and an argv string carries one scalar — nothing says whether it fits",
			DeclineNode: argvValue,
		}
	}
	literalText, isLiteral := argvLiteralTextOf(ctx, argvValue)
	if !isLiteral {
		return &ForeignEdgeOutcome{
			Decline: "the argv value is not a written string literal; its parsed value cannot be pinned",
			DeclineNode: argvValue,
		}
	}
	parsed, parseErr := strconv.ParseFloat(literalText, 64)
	if parseErr != nil {
		ctx.Report(assignability.At(argvValue, 7001,
			"the argv value "+strconv.Quote(literalText)+" crossing to "+artifact.Called.Name+
				" does not parse as a Python float() — the value cannot arrive"))
		return &ForeignEdgeOutcome{}
	}
	// NaN-FREEDOM: the same obstacle the stdin leg routes through — a
	// pinned NaN value, the third of the three shapes nanFreedomObstacle
	// recognizes.
	if parsedIsNaN(parsed) {
		sentence := nanFreedomObstacle(abstractdomain.AbstractValue{Kind: abstractdomain.KindNaN})
		ctx.Report(foreignRefutation(argvValue,
			sentence+" — the argv value "+strconv.Quote(literalText)+" parses as NaN, so "+
				artifact.Called.Name+" would receive a value this program never computed",
			artifact))
		return &ForeignEdgeOutcome{}
	}
	crossingSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{parsed}))
	fits, asked := foreignScalarSubset(ctx, crossingSet, entry.Set)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the argv value crossing out fits " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entry.Set) + ", so the crossing is not judged",
			DeclineNode: argvValue,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(argvValue,
			"the argv value crossing to "+artifact.Called.Name+" is "+foreignSetWords(crossingSet)+
				", and the target admits "+foreignSetWords(entry.Set)+
				" — the value can escape what the target states it accepts",
			artifact))
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// argvLiteralTextOf reads an argv element as a written string literal's
// own text — directly, or through a const binding's initializer
// (resolvedConstStringLiteral, the same follow scriptElementOf already
// performs for a script path).
func argvLiteralTextOf(ctx *FlowContext, element *ast.Node) (string, bool) {
	node := Unwrapped(element)
	if node == nil {
		return "", false
	}
	if text, ok := stringLiteralText(node); ok {
		return text, true
	}
	if ast.IsIdentifier(node) {
		if text, ok := resolvedConstStringLiteral(ctx, node); ok {
			return text, true
		}
	}
	return "", false
}

// parsedIsNaN is whether a Go strconv.ParseFloat result is NaN — Python's
// float() accepts the "nan" spelling (case-insensitively, optionally
// signed) exactly where Go's parser does, so a value that reaches here
// through strconv.ParseFloat succeeding is NaN under both languages'
// readings at once.
func parsedIsNaN(v float64) bool {
	return v != v
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
	ctx *FlowContext, payload *ast.Node, artifact *ForeignArtifact,
	entry ForeignEntry, crossing abstractdomain.AbstractValue,
) *ForeignEdgeOutcome {
	// a MODULE-LEVEL const array literal (`const samples = [0.5, -0.3,
	// 0.2]`) evaluates through EvaluateArrayLiteral's own flat path to a
	// bare KindValues{PrimitiveArray} tuple — never a KindSet, since
	// KindSet is what a DECLARED `number[]` parameter wears
	// (typereading's StarOfElement/Repetition seeding), not what a
	// literal's own values evaluate to. The fixture's own row
	// (d-data-legs.ts's jsonStdinCapturedStdoutRecognized) reads exactly
	// this shape, whether `samples` is declared at module scope or
	// inside the function — the value crossing out is the same tuple
	// either way. sequenceCrossingOfExactTuple rebuilds the same
	// Repetition-shaped window a declared array wears, so this gate and
	// everything past it read an exact literal identically to a
	// declared one; a shape it cannot convert (a non-literal element, a
	// literal join with a mutated value) falls through to the existing
	// decline unchanged.
	if crossing.Kind == abstractdomain.KindValues && crossing.KindTag == abstractdomain.PrimitiveArray {
		if converted, ok := sequenceCrossingOfExactTuple(crossing); ok {
			crossing = converted
		}
	}
	if crossing.Kind != abstractdomain.KindSet || crossing.SetKindTag != abstractdomain.SetKindTagNone {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits a sequence at " +
				entry.Name + ", and the value crossing out is not read as one here — " +
				"nothing says whether it fits",
			DeclineNode: payload,
		}
	}
	window, windowOk := refinementsets.AsRepetition(crossing.Set)
	if !windowOk {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits a sequence at " +
				entry.Name + " of at least " + strconv.Itoa(entry.LengthAtLeast) +
				" elements, and the value crossing out states no element set or length " +
				"window — nothing says whether it fits",
			DeclineNode: payload,
		}
	}
	// the ELEMENT fit — a real kernel ask
	fits, asked := foreignScalarSubset(ctx, window.Element, entry.Element)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the elements crossing out fit " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entry.Element) +
				", so the crossing is not judged",
			DeclineNode: payload,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(payload,
			"the elements crossing to "+artifact.Called.Name+" are "+
				foreignSetWords(window.Element)+", and the target admits "+
				foreignSetWords(entry.Element)+
				" — the value can escape what the target states it accepts",
			artifact))
		return &ForeignEdgeOutcome{}
	}
	// the LENGTH floor: the target's body relies on it (a division by
	// len, an indexed read), so a shorter sequence is a different program
	if window.Lo < entry.LengthAtLeast {
		ctx.Report(foreignRefutation(payload,
			"the sequence crossing to "+artifact.Called.Name+" holds at least "+
				strconv.Itoa(window.Lo)+" elements, and the target relies on at least "+
				strconv.Itoa(entry.LengthAtLeast),
			artifact))
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// sequenceCrossingOfExactTuple rebuilds an exact KindValues{PrimitiveArray}
// tuple as the Repetition-shaped KindSet a declared `number[]` parameter
// already wears: the union of the tuple's own values as the element,
// repeated exactly len(values) times. This is the array-literal twin of
// SetOfKnown's tuple-concatenation reading (lattice_operations.go) — that
// reading builds an exact CONCATENATION of singletons for the scalar-fit
// question checkScalarCrossing asks; this one builds the COUNTED-REPEAT
// window checkSequenceCrossing asks instead, since a sequence entry's own
// premises (the element fit, the length floor) are stated over a
// Repetition, not a concatenation.
//
// Answers ok=false for the one shape Repetition itself cannot spell back
// through AsRepetition: an exactly-one-element tuple, where Repetition's
// own lo=1/hi=1 special case collapses to the bare scalar element with no
// repeat wrapper (repetition_window_forms.go's own doc names this
// collapse) — that tuple stays undetermined with the ordinary "not read
// as one here" sentence, the same as any other shape this reader cannot
// convert, rather than silently misreading a 1-element array as a scalar.
func sequenceCrossingOfExactTuple(crossing abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	if len(crossing.Values) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	element := refinementsets.MakeRefinedSet(refinementsets.OneOf(crossing.Values))
	n := len(crossing.Values)
	window := refinementsets.Repetition(element, n, &n)
	if _, ok := refinementsets.AsRepetition(window); !ok {
		return abstractdomain.AbstractValue{}, false
	}
	return abstractdomain.KnownSet(
		window, nil, abstractdomain.TrustLevelOf(crossing), abstractdomain.SetKindTagNone,
	), true
}

// checkScalarCrossing judges a scalar payload against a scalar entry —
// the same ScalarSubset ask, without a length to carry.
func checkScalarCrossing(
	ctx *FlowContext, payload *ast.Node, artifact *ForeignArtifact,
	entry ForeignEntry, crossing abstractdomain.AbstractValue,
) *ForeignEdgeOutcome {
	crossingSet, ok := abstractdomain.SetOfKnown(crossing)
	if !ok {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits " +
				foreignSetWords(entry.Set) + " at " + entry.Name +
				", and the value crossing out is not read as a set here — " +
				"nothing says whether it fits",
			DeclineNode: payload,
		}
	}
	fits, asked := foreignScalarSubset(ctx, crossingSet, entry.Set)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the value crossing out fits " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entry.Set) +
				", so the crossing is not judged",
			DeclineNode: payload,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(payload,
			"the value crossing to "+artifact.Called.Name+" is "+
				foreignSetWords(crossingSet)+", and the target admits "+
				foreignSetWords(entry.Set)+
				" — the value can escape what the target states it accepts",
			artifact))
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

// foreignRefutation builds a 7001 at node, carrying the target's own
// provenance as a related step in the Python file rather than
// concatenated into the message text — the second step of the
// two-language explanation, at the shape RefinementDiagnostic.Steps
// renders (assignability/refinement_diagnostics.go, ls/refinedts_
// diagnostics.go's AddRelatedInfo). said is the whole message on its
// own; the provenance step is additive, never folded into it.
//
// The step's file/text/offset all come from ForeignProvenance, filled
// once in foreign_edge_artifact.go from the SAME bytes checkTargetIntegrity
// already read — nothing here reads the target file again. A
// provenance with no line (Length == 0: absent, or the artifact's line
// and the target's current text have drifted) still names the file:
// StepInForeignFile's own empty-text degrade pins the step to the
// file's head rather than dropping it.
func foreignRefutation(node *ast.Node, said string, artifact *ForeignArtifact) assignability.RefinementDiagnostic {
	finding := assignability.At(node, 7001, said)
	provenance := artifact.Called.Provenance
	if provenance.File == "" {
		return finding
	}
	return finding.WithSteps(assignability.StepInForeignFile(
		provenance.File, provenance.Text, provenance.Start, provenance.Length, provenance.Said,
	))
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

// isForeignParseOf is whether a node is exactly `JSON.parse(<name>)` or
// `JSON.parse(<name>.stdout)` — execFileSync's bound name IS the stdout
// string, so the bare identifier is the read; spawnSync's bound name is
// the whole result object, so its stdout string sits at `.stdout`. Both
// read the SAME binding for the write-check in soleParseConsumerOf
// (AssignedNamesDirect keys on the plain name either way).
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
	return isForeignParseArgumentOf(call.Arguments.Nodes[0], name)
}

// isForeignParseArgumentOf is whether a parse's one argument reads the
// bound name's stdout: the bare identifier (execFileSync/execSync), or
// `<name>.stdout` (spawnSync's result object).
func isForeignParseArgumentOf(argument *ast.Node, name string) bool {
	node := Unwrapped(argument)
	if node == nil {
		return false
	}
	if ast.IsIdentifier(node) {
		return node.Text() == name
	}
	if ast.IsPropertyAccessExpression(node) {
		access := node.AsPropertyAccessExpression()
		receiver := Unwrapped(access.Expression)
		return access.QuestionDotToken == nil && receiver != nil && ast.IsIdentifier(receiver) &&
			receiver.Text() == name && ast.IsIdentifier(access.Name()) && access.Name().Text() == "stdout"
	}
	return false
}
