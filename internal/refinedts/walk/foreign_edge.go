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
//  5. ALSO attach a fact to the CALL node itself, ALWAYS — the
//     intermediate `stdout` binding reads at least the plain-string
//     ground the call's own written `encoding` option already
//     established at recognition time, whether the outbound leg fired,
//     declined, or discharged clean. On a DISCHARGED crossing (no
//     outbound fire) that ground UPGRADES to the serialized form of the
//     target's stated return fact — the intermediate binding reads as
//     the tighter string set the harness's own JSON encoder can spell
//     (foreignStdoutSerializedValue) rather than the plain ground, not
//     bare residue, between the call and the parse.
//
// The attach rides ctx.NodeOverrides, the seam the relational
// accumulation already uses for a value no re-walk can reach: the fact
// on `JSON.parse(stdout)` comes from ANOTHER LANGUAGE'S checker, and
// nothing in this file's walk can derive it. Today that node evaluates
// to residue (coercion_models.go's readJsonMethods: "whatever JSON
// value the text spells"), and the override supersedes it —
// evaluateExpression reads NodeOverrides before it walks. Step 5's
// attach rides the identical seam, one statement earlier and keyed on
// the call expression rather than the parse.
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
//
// FILE MAP. This file holds only the orchestration entry points
// (ForeignEdgeAt, the artifact-read routing, the ±Infinity return
// corner) and the file-wide constants both legs share.
// foreign_edge_recognize.go reads execFileSync/spawnSync/execSync/the
// const-bound call shape; foreign_edge_spawn.go reads spawn's own
// accumulate-then-parse async return leg; foreign_edge_argv.go reads
// the runner/script argv positions; foreign_edge_keywords.go reads the
// child_process callee test and the options-object/shell-string
// keywords; foreign_edge_crossing.go discharges the outbound leg's own
// fit premises; foreign_edge_parse_consumer.go finds the return leg's
// sole JSON.parse consumer; foreign_edge_cases.go lowers a RULED cases
// list to an AbstractValue.

package walk

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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
	// IsCompiledBinary: true when TargetPath names a COMPILED BINARY
	// (argv[0] itself is the target, no interpreter word) rather than
	// Python source — set by compiledBinaryArgvOf's own recognition.
	// ForeignEdgeAt reads this to route the artifact lookup to
	// ReadCompiledBinaryArtifact's sibling-fact-file ladder instead of
	// ReadForeignArtifact's project-cache path, mirroring the Rust
	// consumer's own Runner::CompiledBinary branch (foreign_edge.rs:483).
	IsCompiledBinary bool
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
	// CallOverride: a SECOND, EARLIER pin — the intermediate captured-
	// stdout binding's own value (`const stdout = execFileSync(...)`
	// or `execSync(...)`, whose bound name IS the stdout string
	// itself), keyed on edge.Call rather than the parse node.
	// CallOverride carries ONE OF TWO claims, at two different grades:
	//
	//   - the PLAIN-STRING ground (TrustProved, `refinementsets.Strings`
	//     unconstrained) on ANY recognized bare-string-binding edge — the
	//     call's own written `encoding` option already established this
	//     at recognition time (execFileSyncEdgeOf's own decline for a
	//     missing/non-string encoding), so it holds regardless of the
	//     outbound leg's own fit, fire, or decline.
	//   - the SERIALIZED-SET upgrade (foreignStdoutSerializedValue,
	//     TrustSpec) — the target's own stated return shape narrowed to
	//     the JSON grammar plus the trailing newline every stdout capture
	//     ends with — ONLY where the crossing is FULLY DISCHARGED.
	//     checkOutboundLeg's outbound outcome must be the LITERAL nil
	//     pointer, never merely Decline == "": a fired fit refutation
	//     (checkScalarCrossing et al.) reports 7001 through ctx.Report
	//     and answers a non-nil &ForeignEdgeOutcome{} with Decline == ""
	//     too, and that shape is NOT discharged — a fired crossing's
	//     stdout binding stays at the plain-string ground, because the
	//     unvalidated-parse reading is load-bearing for the generic-union
	//     return model's own None-arm fire.
	//
	// Nil only for spawnSync (whose bound name is an OBJECT carrying
	// `.stdout`, not the string itself — foreignCallBindsBareStdoutString's
	// own gate).
	CallOverride map[*ast.Node]abstractdomain.AbstractValue
	// CallOverrideStatement: the statement index CallOverride pins —
	// always edge.Call's own statement (the caller's current index),
	// never OverrideStatement's later one.
	CallOverrideStatement int
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

// readForeignEdgeArtifact routes to the artifact reader whose premises
// fit edge's own shape: a COMPILED BINARY reads its fact from a
// SIBLING file (ReadCompiledBinaryArtifact's `<path>.facts.json`),
// never the Python reader's project-cache path (ReadForeignArtifact) —
// a compiled binary has no `.refined/cache/` entry any producer in
// this checker writes. Mirrors the Rust consumer's own
// `edge.runner == Runner::CompiledBinary` branch (foreign_edge.rs:483)
// exactly, including its own three-rung ladder: no sibling file at all
// (the generic compiled-binary no-fact sentence,
// CompiledBinaryNoFactSentence), a sibling that exists but failed to
// parse (ReadCompiledBinaryArtifact's own sentence, naming the
// unreadable file), or a sibling that parses and serves. The two
// rungs are told apart by a DISK EXISTENCE CHECK
// (ReadCompiledBinaryArtifact's own `exists` flag) — never by
// string-sniffing the sentence text.
func readForeignEdgeArtifact(edge *ForeignEdge) (*ForeignArtifact, string) {
	if !edge.IsCompiledBinary {
		return ReadForeignArtifact(edge.TargetPath)
	}
	artifact, exists, sentence := ReadCompiledBinaryArtifact(edge.TargetPath)
	if sentence != "" {
		// the sibling file EXISTS but failed to read as a fact — name the
		// unreadable file, not the generic no-fact sentence, which is only
		// true when there is no fact file at all
		return nil, sentence
	}
	if !exists {
		return nil, CompiledBinaryNoFactSentence(edge.TargetPath)
	}
	return artifact, ""
}

// foreignArtifactStatesNothing tells apart the two shapes an artifact
// read's decline sentence can take: the artifact was READ successfully
// and its CONTENT states nothing usable (no callable surface at all,
// or a surface naming a function the artifact carries no row for) —
// answers true, and ForeignEdgeAt lets the ordinary evaluation of
// stdout/JSON.parse(stdout) proceed rather than reporting 7002 — versus
// every other decline, which means the artifact could not be TRUSTED
// at all (missing file, unparseable JSON, an unrecognized or
// superseded envelope, a target-integrity/runtime-band mismatch, a
// malformed surface field, a malformed cases/entries shape, a set the
// kernel grammar cannot decode) and keeps refusing the call exactly as
// before.
//
// STRING-MATCHED AGAINST THE SENTENCE, not a typed signal
// (foreign_edge_artifact.go's own three-rung compiled-binary ladder
// docs this same distinction and explicitly prefers a disk-existence
// flag over sentence-sniffing) — this file does not own
// foreign_edge_artifact.go this unit, so the two sentences' FIXED
// (non-interpolated) wording is matched here instead: surfaceOf's "
// states no callable surface for its __main__ block" (the artifact
// carries no "surface" key at all) and functionFactOf's "as the
// surface's called function and then states no fact for it" (the
// artifact's "functions" map has no row for the named function). Both
// literals must stay byte-identical to foreign_edge_artifact.go's own
// two sentences (surfaceOf's no-surface-key branch, functionFactOf's
// row-missing branch) — a wording change on either side without the
// matching change here silently stops this classification from firing
// (the decline would then wrongly keep reporting 7002) rather than
// misclassifying a genuinely-unreadable artifact as content-states-
// nothing, since the match is a Contains, never a prefix/suffix of the
// WHOLE message — every other decline in that file uses different
// fixed wording and cannot accidentally match either literal.
func foreignArtifactStatesNothing(sentence string) bool {
	return strings.Contains(sentence, "states no callable surface for its __main__ block") ||
		strings.Contains(sentence, "as the surface's called function and then states no fact for it")
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
	artifact, artifactSentence := readForeignEdgeArtifact(edge)
	if artifactSentence != "" {
		if foreignArtifactStatesNothing(artifactSentence) {
			// the artifact was READ successfully and names no additional
			// fact for this call (no callable surface at all, or a surface
			// naming a function the artifact carries no row for) — this is
			// NOT a reason to refuse the call the way a genuinely unreadable
			// artifact is (missing file, unparseable JSON, a wrong/
			// superseded envelope, a target-integrity/runtime-band
			// mismatch, a malformed surface or cases shape): the crossing
			// is recognized, but nothing more is known about it than an
			// ORDINARY unrecognized call already carries (execFileSync's
			// own declared `string | Buffer`, JSON.parse's own `any`). So
			// Decline/DeclineNode/Override/CallOverride all stay nil/empty
			// — no 7002 reports, no NodeOverrides pin rides ctx, and the
			// walk falls through to the SAME ordinary evaluation an
			// unrecognized call gets — a downstream consumer-side guard
			// (or its absence, refused normally at Age/whatever declared
			// type consumes the parsed value) is what determines the
			// outcome, exactly as D5.count/D5.grade/D5.guard/D5.label/
			// D5.propagate/D5.raise/D5.set's own docstrings state the
			// crossing is designed to work: "the consumer-side bounding
			// guard is the path to a determination — the edge itself
			// would otherwise be the named blocker." TargetPath still
			// rides the outcome (isEdge stays true) so
			// analyze_statement.go's ConsumedForeignSink still records
			// this file as consumed — a recognized-and-read edge that
			// simply has nothing more to say is still a target this check
			// looked at, exactly as a fired or served edge is.
			return &ForeignEdgeOutcome{TargetPath: edge.TargetPath}, true
		}
		return &ForeignEdgeOutcome{Decline: artifactSentence, DeclineNode: edge.Call, TargetPath: edge.TargetPath}, true
	}
	// the OUTBOUND leg: every §4/§5 premise about what crosses out,
	// discharged against the value the walk holds for it. Its outcome is
	// held, NOT returned yet: an outbound fire or decline says nothing
	// about whether the artifact ALSO states a return fact — the two legs
	// are independent truths (the outbound leg judges what this call
	// SENDS; the return leg judges what the artifact's own harness
	// STATES it sends back), and a bound `JSON.parse(stdout)` result
	// still deserves the fact its own artifact carries even where the
	// outbound leg has nothing left to say. Only the FIRST outcome with
	// a real Decline is ever carried back (one Decline slot), the same
	// precedence checkMixedCrossing's own two-leg merge already applies;
	// the return leg still runs regardless, so its Override is never
	// lost to an outbound decline or fire.
	outboundOutcome := checkOutboundLeg(ctx, env, edge, artifact)
	// CHANNEL PURITY (§5): the wire is stdout, and the claim assumes
	// stdout carries exactly the serialized result
	if !artifact.Called.Return.StdoutPure {
		if outboundOutcome != nil && outboundOutcome.Decline != "" {
			outboundOutcome.TargetPath = edge.TargetPath
			return outboundOutcome, true
		}
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
		if outboundOutcome != nil && outboundOutcome.Decline != "" {
			outboundOutcome.TargetPath = edge.TargetPath
			return outboundOutcome, true
		}
		return &ForeignEdgeOutcome{Decline: parseSentence, DeclineNode: edge.Call, TargetPath: edge.TargetPath}, true
	}
	if parse == nil {
		// NO expression consumes the target's stdout at all (the "no
		// JSON.parse consumer" case, distinguished above from every real
		// decline by carrying no sentence) — a recognized crossing whose
		// result nothing reads needs NO fact: there is no return-leg node
		// left for one to attach to. The outbound leg's own outcome (a
		// decline, a fire's empty outcome, or nothing at all) is the
		// whole answer here, unchanged from before this fix — this
		// branch never had a return-leg fact to lose.
		if outboundOutcome != nil {
			outboundOutcome.TargetPath = edge.TargetPath
			return outboundOutcome, true
		}
		return &ForeignEdgeOutcome{TargetPath: edge.TargetPath}, true
	}
	// THE ±INFINITY CORNER (§4, sec-json.stringify / the JSON.parse
	// grammar, specifications/javascript/spec.html): `JSON.stringify`
	// writes a JS number's ±Infinity as the bare token `null` on the
	// OUTBOUND leg (checked above, nanFreedomObstacle's own premise); on
	// this INBOUND leg the hazard is the mirror and worse — the TARGET is
	// Python, and `json.dumps(float("inf"))` emits the bare token
	// `Infinity` rather than a JSON number literal (JSON itself carries
	// no fault here: `1e999` is a legal JSON number and parses to
	// Infinity in both runtimes; the bare `Infinity` TOKEN is what
	// Python's default serializer chooses to write instead, and that
	// token is not a legal JSON value — sec-json.parse's JSONNumber
	// production admits no such token, so `JSON.parse` THROWS a
	// SyntaxError at runtime whenever the concrete call actually derives
	// that value).
	//
	// THE DETERMINATION (replacing an earlier decline): a completed
	// `JSON.parse` call never actually returns ±Infinity, because every
	// concrete run that would have carried it throws first and the
	// assignment this fact attaches to is never reached on that arm —
	// the same "returned half excludes the thrown exit" reading
	// KnownStateWire.Returned() already states for statement-sequencing
	// (kernelbridge/narrow_questions.go). There is no expression-level
	// "value or throws" AbstractValue kind in this domain (abstractdomain
	// carries KindNaN/KindPossiblyNaN for NaN's own wire hazard, and
	// nothing analogous for a thrown parse), so the nearest SOUND
	// determination is applied directly to the returned set: the corner
	// value is DIFFERENCED OUT of the case's claimed window before it
	// binds, per refinementsets.Difference (the same set-difference form
	// kernel_bridge_test.go's own ℝ̄∖{0} row already asks the kernel
	// about). The narrowed set is strictly weaker than the target's own
	// stated return — a fact any completed call still satisfies — so the
	// crossing judges normally against it rather than declining. The
	// corner rule runs PER NUMBER CASE, exactly as before.
	narrowedCases := make([]Case, len(artifact.Called.Return.Cases))
	copy(narrowedCases, artifact.Called.Return.Cases)
	for i, c := range narrowedCases {
		if c.Sort != CaseSortNumber {
			continue
		}
		narrowedCases[i].Set = foreignFiniteReturnSet(ctx, c.Set)
	}
	returned := &ForeignEdgeOutcome{
		Override: map[*ast.Node]abstractdomain.AbstractValue{
			parse: foreignAbstractValueOfCases(narrowedCases),
		},
		OverrideStatement: at,
		TargetPath:        edge.TargetPath,
	}
	// the intermediate captured-stdout binding (`const stdout =
	// execFileSync(...)`) itself. Two SEPARATE claims apply here, at two
	// different grades, and only one of them needs the outbound leg
	// discharged:
	//
	//   - THE PLAIN-STRING GROUND: execFileSyncOptionsOf already refused
	//     to recognize this edge at all unless the call's own `encoding`
	//     option names a string encoding (execFileSyncEdgeOf's own
	//     "without a string encoding... the return leg has no text to
	//     parse" decline) — so a RECOGNIZED bare-string-binding edge has
	//     already established, from the call's own written syntax alone,
	//     that the value is a string, never the declared type's other
	//     arm (execFileSync/execSync answer `string | Buffer`, and the
	//     Buffer arm is exactly what the encoding option rules out here).
	//     This claim cites nothing about the target artifact — it holds
	//     whether the outbound leg is clean, fired, or declined — so it
	//     binds UNCONDITIONALLY on every recognized bare-string edge.
	//     Without this pin, `stdout` falls to the ordinary (unoverridden)
	//     evaluation of the call's declared `string | Buffer` return,
	//     which the general return-type reader cannot narrow the same
	//     way (it has no access to the encoding option's own syntax) and
	//     so answers residue there — never the unsound direction, but a
	//     needless one this narrower, always-true fact avoids.
	//   - THE SERIALIZED-SET UPGRADE (foreignStdoutSerializedValue): the
	//     TARGET's own stated return shape, trustworthy only once the
	//     outbound leg is FULLY DISCHARGED — outboundOutcome == nil, and
	//     nothing else. checkOutboundLeg's own doc states the three
	//     shapes an outbound outcome takes, and only the first is safe to
	//     upgrade on:
	//
	//       - nil: the leg is CLEAN — nothing to check, or the fit
	//         passed. This is the only DISCHARGED case.
	//       - non-nil with Decline == "": a FIT FAILURE FIRED 7001
	//         already, through ctx.Report, inside checkScalarCrossing/
	//         checkSequenceCrossing/checkMixedCrossing/stdinFitAgainst —
	//         the empty &ForeignEdgeOutcome{} these functions return
	//         after Report is the FIRE signal, not a "nothing wrong"
	//         signal. A first pass here read this shape as clean
	//         (Decline == "" reads the same as the truly-clean nil case
	//         unless the POINTER itself is also checked) and wrongly
	//         narrowed `stdout` to the target's serialized-JSON-grammar
	//         claim on a fired crossing — the plain-string reading above
	//         is load-bearing for the generic-union return model's own
	//         None-arm fire on that path, so the UPGRADE must never touch
	//         it, though the plain-string ground still does.
	//       - non-nil with Decline != "": a real channel-mismatch/
	//         RTS7002 decline (merged in just below) — also not
	//         discharged.
	//
	//     foreignStdoutSerializedValue itself further answers ok=false
	//     wherever the return cases are not a shape it can compose, so an
	//     unrecognized return shape leaves the plain-string ground as
	//     `stdout`'s whole claim, unchanged.
	//
	// GATED TO THE BARE-STRING SHAPE (execFileSync/execSync): the call
	// expression edge.Call is what the bound name's INITIALIZER
	// evaluates to (AnalyzeVariableStatement's `evaluateExpression(ctx,
	// env, decl.Initializer)`), so pinning NodeOverrides[edge.Call]
	// binds the WHOLE initializer value. execFileSync/execSync answer
	// the stdout string directly — the pin is exactly the bound name's
	// own value there. spawnSync answers an OBJECT
	// (`{stdout, stderr, ...}`, StdoutName riding at `.stdout`,
	// isForeignParseOf's own dual reading) — pinning edge.Call there
	// would wrongly force the whole result object to a bare string set.
	// foreignCallBindsBareStdoutString reads the callee name to tell
	// the two apart the same way foreignEdgeOf's own dispatch does.
	if foreignCallBindsBareStdoutString(ctx, edge.Call) {
		stdoutValue := abstractdomain.KnownSet(
			refinementsets.Strings, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
		if outboundOutcome == nil {
			if serialized, ok := foreignStdoutSerializedValue(artifact.Called.Return.Cases); ok {
				stdoutValue = serialized
			}
		}
		returned.CallOverride = map[*ast.Node]abstractdomain.AbstractValue{
			edge.Call: stdoutValue,
		}
		returned.CallOverrideStatement = index
	}
	// the outbound leg's own decline (if any) rides ALONGSIDE this
	// return-leg fact, never replaced by it: the outbound leg's fire
	// already reported through ctx.Report as it ran (checkOutboundLeg's
	// own contract), and a real Decline sentence is still owed to the
	// caller exactly as it was before this leg ran — the return fact
	// this artifact states is a SEPARATE truth from whatever the
	// outbound value's own fit determined, and publishing one is never a
	// reason to drop the other's report.
	if outboundOutcome != nil && outboundOutcome.Decline != "" {
		returned.Decline = outboundOutcome.Decline
		returned.DeclineNode = outboundOutcome.DeclineNode
	}
	return returned, true
}

// foreignFiniteReturnSet answers returnSet with any ±Infinity corner
// DIFFERENCED OUT — the finite portion a completed JSON.parse call can
// actually still produce, per foreignReturnCornerObstacle's own Member
// ask. A set admitting neither corner (the ordinary case) answers
// unchanged; a refused or unavailable question (no kernel loaded, a
// panic inside the ask) also answers unchanged — the SAME "no proof, no
// obstacle" reading foreignReturnCornerObstacle itself gives a refused
// question, so an untested corner never falsely narrows a set the
// kernel simply could not answer for.
func foreignFiniteReturnSet(ctx *FlowContext, returnSet refinementsets.RefinedSet) refinementsets.RefinedSet {
	if ctx == nil || ctx.Kernel == nil || ctx.Kernel.Member == nil {
		return returnSet
	}
	narrowed := returnSet
	admitsPosInf := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		return ctx.Kernel.Member(narrowed, []float64{math.Inf(1)})
	}
	admitsNegInf := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		return ctx.Kernel.Member(narrowed, []float64{math.Inf(-1)})
	}
	if admitsPosInf() {
		narrowed = refinementsets.MakeRefinedSet(refinementsets.Difference(
			narrowed, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1)})),
		))
	}
	if admitsNegInf() {
		narrowed = refinementsets.MakeRefinedSet(refinementsets.Difference(
			narrowed, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(-1)})),
		))
	}
	return narrowed
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
		return "admits Infinity, which json.dumps spells as a token JSON.parse rejects on this leg — " +
			"json.dumps(float(\"inf\")) writes the bare token Infinity rather than a JSON number literal " +
			"(1e999 would parse fine; the bare token does not), and JSON.parse throws on it — " +
			"the crossing cannot be trusted at that corner"
	}
	if ctx.Kernel.Member(returnSet, []float64{math.Inf(-1)}) {
		return "admits -Infinity, which json.dumps spells as a token JSON.parse rejects on this leg — " +
			"json.dumps(float(\"-inf\")) writes the bare token -Infinity rather than a JSON number literal, " +
			"and JSON.parse throws on it — the crossing cannot be trusted at that corner"
	}
	return ""
}

// foreignReturnValue is the fact the parse result wears: the target's
// stated return cases, lowered to one AbstractValue at the grade the
// crossing's weakest cited boundary admits.
//
// TrustSpec, and the reason is the file banner's: the value is not the
// kernel's own decision about this expression, it is another language's
// claim carried across a transport whose identity §4 states as a CITED
// PREMISE (the round-trip theorem is a named open problem) under a
// runtime band cited from the Python pins. TrustSpec is the tree's
// existing grade for exactly that boundary — a spec clause read
// correctly, not a theorem discharged.
//
// Called only once the corner loop above has cleared every number case
// of both infinite corners — a cases list that reaches here binds
// exactly as it always did.
func foreignReturnValue(artifact *ForeignArtifact) abstractdomain.AbstractValue {
	return foreignAbstractValueOfCases(artifact.Called.Return.Cases)
}
