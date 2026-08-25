// Discharges the outbound leg's own premises — the value crossing OUT
// to the target — against the value the walk holds for it: channel
// match (stdin/argv/mixed/file-carried), NaN-freedom, and the
// element/length/scalar fit questions asked of the kernel.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

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
	if edge.Payload == nil && artifact.Surface == ForeignSurfaceStdinJSON {
		// THE NO-COMPLETED-RUN DETERMINATION (mirroring checkArgvCrossing's
		// ForeignSurfaceMixedStdinArgv case): the target's own harness reads
		// its ONE value from stdin (`json.load(sys.stdin)` or the
		// equivalent), and this call closes stdin with no bytes written at
		// all — the same EOF-on-empty-stream throw the mixed case's own
		// missing-stdin-leg reasoning already names. Every concrete run
		// throws at the target's own stdin read before the harness ever
		// reaches a value to return, so there is no completed run for a
		// return fact to attach to, and nothing about that outbound leg
		// CONTRADICTS the target's stated entry — there is simply no value
		// crossing out to judge against it. Recognized and determined
		// (never declined): the outbound leg has nothing to check because
		// the call itself never reaches the point where anything crosses.
		return nil
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
		// THE MISSING-STDIN-LEG DETERMINATION (replacing an earlier
		// decline): the target's own mixed harness reads BOTH channels —
		// this call sends only argv[1], so its stdin closes with no bytes
		// written at all. The Python harness's own stdin read (json.load
		// on an EOF-empty stream) throws json.JSONDecodeError before the
		// argv leg is ever consulted — the same "a completed run never
		// reaches the state this claim would contradict" reading
		// foreignFiniteReturnSet applies to the return leg's ±Infinity
		// corner, applied here to the CALL itself: every concrete run
		// throws at the target's own stdin read, so there is no completed
		// run for a return fact to attach to at all (the call already has
		// no JSON.parse fact bound on this path — soleParseConsumerOf's
		// own machinery runs downstream of this leg regardless), and
		// nothing here CONTRADICTS that outcome. What still has a real
		// premise to discharge is the argv leg's OWN fit — if the caller
		// ever adds the missing stdin leg, the argv value crossing here
		// must already fit the target's stated entry, so that check runs
		// normally rather than being skipped for a channel mismatch that
		// no longer stops the crossing.
		if len(artifact.Called.Entry) != 2 {
			return &ForeignEdgeOutcome{
				Decline: "the target " + artifact.Called.Name + " states " +
					strconv.Itoa(len(artifact.Called.Entry)) + " entry positions, and its mixed surface " +
					"hands it two values (stdin, then argv) — the checker models no other split",
				DeclineNode: edge.Call,
			}
		}
		return argvScalarFitAgainst(ctx, edge.ArgvValue, artifact, artifact.Called.Entry[1])
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
	entrySet, entrySentence := scalarCaseSetOf(entry.Cases, artifact.Called.Name)
	if entrySentence != "" {
		return &ForeignEdgeOutcome{Decline: entrySentence, DeclineNode: argvValue}
	}
	crossingSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{parsed}))
	fits, asked := foreignScalarSubset(ctx, crossingSet, entrySet)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the argv value crossing out fits " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entrySet) + ", so the crossing is not judged",
			DeclineNode: argvValue,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(argvValue,
			"the argv value sent to "+artifact.Called.Name+" is of type '"+foreignSetWords(crossingSet)+
				"', which is not assignable to the target's stated entry '"+foreignSetWords(entrySet)+"'",
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
	// a MIXED-ELEMENT array literal (`[this.level, -0.3, 0.2]`, a range
	// beside exact numbers) evaluates through EvaluateArrayLiteral's own
	// non-flat path to KindList — one AbstractValue per slot, never
	// KindValues{PrimitiveArray}, since that flat form is reserved for a
	// literal whose EVERY element is an exact singleton number
	// (array_literal.go's own flat gate). sequenceCrossingOfKindList reads
	// each slot the same way an array literal's own per-position claim
	// already does (scalarPositionSet, array_literal.go) and rebuilds the
	// same Repetition-shaped window an exact tuple or a declared array
	// wears, so this gate and everything past it judge a mixed-element
	// literal identically to an all-exact one. A slot that poses no
	// per-position set at all (an object, an opaque read, a nested
	// sequence) falls through to the existing decline unchanged — this
	// converter is total only over literals every one of whose slots is
	// itself scalar-shaped.
	if crossing.Kind == abstractdomain.KindList {
		if converted, ok := sequenceCrossingOfKindList(crossing); ok {
			crossing = converted
		}
	}
	// an OBJECT crossing at a sequence entry is a determined shape
	// mismatch, not an undetermined reading — the value's own KIND is
	// known (Object.keys(...).reduce(...) into an accumulator object,
	// the plain-object literal shape callback_outcome.go's reduce
	// reading answers), and an object is never a sequence whatever its
	// keys hold. This fires the same crossing-fit refusal
	// checkScalarCrossing already reports for a determined scalar
	// mismatch, rather than falling into the "not read as one here"
	// decline below, which is reserved for a crossing this walk could
	// not read AT ALL (Opaque, an unconverted list, …).
	if crossing.Kind == abstractdomain.KindObject {
		ctx.Report(foreignRefutation(payload,
			"the value sent to "+artifact.Called.Name+" is of type 'object', which is not "+
				"assignable to the target's stated sequence entry '"+entry.Name+"'",
			artifact))
		return &ForeignEdgeOutcome{}
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
	elementSet, elementSentence := scalarCaseSetOf(entry.ElementCases, artifact.Called.Name)
	if elementSentence != "" {
		return &ForeignEdgeOutcome{Decline: elementSentence, DeclineNode: payload}
	}
	fits, asked := foreignScalarSubset(ctx, window.Element, elementSet)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the elements crossing out fit " +
				artifact.Called.Name + "'s stated " + foreignSetWords(elementSet) +
				", so the crossing is not judged",
			DeclineNode: payload,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(payload,
			"the elements sent to "+artifact.Called.Name+" are of type '"+foreignSetWords(window.Element)+
				"', which is not assignable to the target's stated entry '"+foreignSetWords(elementSet)+"'",
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
//
// The built window is marked PROVED DENSE (KnownSetDense,
// abstract_value.go's SeqDense/SeqDenseKnown pair): crossing.Values comes
// from EvaluateArrayLiteral's flat KindValues{PrimitiveArray} reading
// (array_literal.go), which is built one literal slot at a time — the same
// element-by-element construction MapOutcome's own dense mark rests on
// (callback_element_outcome.go) — and no hole grammar exists for a
// KindValues array, so every counted position really is present.
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
	built := abstractdomain.KnownSet(
		window, nil, abstractdomain.TrustLevelOf(crossing), abstractdomain.SetKindTagNone,
	)
	// dense per this function's own doc above — a no-op unless built reads
	// back as a KindSet repetition, which the AsRepetition check just did
	return abstractdomain.KnownSetDense(built), true
}

// sequenceCrossingOfKindList rebuilds a KindList array literal (one
// AbstractValue per slot, at least one slot NOT an exact singleton
// number — otherwise EvaluateArrayLiteral would have collapsed it to
// the flat KindValues{PrimitiveArray} tuple sequenceCrossingOfExactTuple
// already reads) as the SAME Repetition-shaped window: the union of
// every slot's own per-position set (scalarPositionSet, array_literal.go
// — a range, an exact number, or any other scalar-set form a single
// array position can hold), repeated exactly len(Items) times.
//
// Answers ok=false where ANY slot poses no per-position set at all — an
// object-shaped element, an Opaque/Unknown read, a nested sequence
// whose own set spells several positions rather than one — since a
// literal with such a slot has no sound single-element claim to build;
// that literal stays undetermined with the ordinary "not read as one
// here" sentence, the same as any other unconvertible shape. Also
// false for the empty list (no element to union) and for the one shape
// Repetition itself cannot spell back through AsRepetition (a single
// slot, which collapses to the bare scalar element — see
// sequenceCrossingOfExactTuple's own doc on that corner).
//
// The built window is marked PROVED DENSE (KnownSetDense,
// abstract_value.go's SeqDense/SeqDenseKnown pair): crossing.Items comes
// from EvaluateArrayLiteral's non-flat KindList reading (array_literal.go),
// which is built one literal slot at a time — the same element-by-element
// construction MapOutcome's own dense mark rests on
// (callback_element_outcome.go) — and no hole grammar exists for a
// KindList array, so every counted position really is present.
func sequenceCrossingOfKindList(crossing abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	if len(crossing.Items) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	var union *refinementsets.RefinedSet
	for _, item := range crossing.Items {
		set, ok := scalarPositionSet(item)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		if union == nil {
			first := set
			union = &first
		} else {
			joined := unionOf(*union, set)
			union = &joined
		}
	}
	n := len(crossing.Items)
	window := refinementsets.Repetition(*union, n, &n)
	if _, ok := refinementsets.AsRepetition(window); !ok {
		return abstractdomain.AbstractValue{}, false
	}
	built := abstractdomain.KnownSet(
		window, nil, abstractdomain.TrustLevelOf(crossing), abstractdomain.SetKindTagNone,
	)
	// dense per this function's own doc above — a no-op unless built reads
	// back as a KindSet repetition, which the AsRepetition check just did
	return abstractdomain.KnownSetDense(built), true
}

// checkScalarCrossing judges a scalar payload against a scalar entry —
// the same ScalarSubset ask, without a length to carry.
func checkScalarCrossing(
	ctx *FlowContext, payload *ast.Node, artifact *ForeignArtifact,
	entry ForeignEntry, crossing abstractdomain.AbstractValue,
) *ForeignEdgeOutcome {
	entrySet, entrySentence := scalarCaseSetOf(entry.Cases, artifact.Called.Name)
	if entrySentence != "" {
		return &ForeignEdgeOutcome{Decline: entrySentence, DeclineNode: payload}
	}
	crossingSet, ok := abstractdomain.SetOfKnown(crossing)
	if !ok {
		return &ForeignEdgeOutcome{
			Decline: "the target " + artifact.Called.Name + " admits " +
				foreignSetWords(entrySet) + " at " + entry.Name +
				", and the value crossing out is not read as a set here — " +
				"nothing says whether it fits",
			DeclineNode: payload,
		}
	}
	fits, asked := foreignScalarSubset(ctx, crossingSet, entrySet)
	if !asked {
		return &ForeignEdgeOutcome{
			Decline: "the kernel refused the question of whether the value crossing out fits " +
				artifact.Called.Name + "'s stated " + foreignSetWords(entrySet) +
				", so the crossing is not judged",
			DeclineNode: payload,
		}
	}
	if !fits {
		ctx.Report(foreignRefutation(payload,
			"the value sent to "+artifact.Called.Name+" is of type '"+foreignSetWords(crossingSet)+
				"', which is not assignable to the target's stated entry '"+foreignSetWords(entrySet)+"'",
			artifact))
		return &ForeignEdgeOutcome{}
	}
	return nil
}

// scalarCaseSetOf answers the ONE number/string set a cases list
// states, for the crossing-fit code below that asks a single ScalarSubset
// question against a single RefinedSet: the RULED schema's "cases" list
// can name more than one sort at once (a possibly-null return, a
// kindUnion of sorts), and judging a crossing against such a list is a
// DIFFERENT question this fit chain does not yet ask — so this answers
// ok=false, naming which shape stopped it, for anything other than
// exactly one number-or-string case. The one-case common path (today's
// only shape a producer states) reads through unchanged.
func scalarCaseSetOf(cases []Case, forName string) (set refinementsets.RefinedSet, sentence string) {
	if len(cases) == 0 {
		return refinementsets.RefinedSet{}, "the target " + forName + " states no cases at all, so nothing bounds the crossing"
	}
	if len(cases) > 1 {
		return refinementsets.RefinedSet{}, "the target " + forName + " states more than one case, and the crossing-fit " +
			"chain judges a value against one number or string set at a time — a multi-case fit is recognized " +
			"and not yet served"
	}
	switch cases[0].Sort {
	case CaseSortNumber, CaseSortString:
		return cases[0].Set, ""
	case CaseSortBoolean:
		return refinementsets.RefinedSet{}, "the target " + forName + " states a boolean case, and the crossing-fit " +
			"chain judges a value against a number or string set — a boolean crossing is recognized and not yet served"
	default:
		return refinementsets.RefinedSet{}, "the target " + forName + " states a null case alone, so nothing " +
			"but the absent value crosses at that position — the crossing-fit chain judges a present value's set"
	}
}

// foreignScalarSubset asks the kernel A ⊆ B, answering (fits, asked).
// A refused question answers (false, false) — the same try/catch shape
// nan_wrapper.go's own ScalarSubset call wears, so a kernel that cannot
// decide leaves the crossing unjudged rather than refuting it.
//
// The question is picked by the operands' sort: a sequence-shaped
// operand (a string window, a concatenation, a union of words) asks
// SeqSubset — the decider whose grammar reads those shapes — and
// scalars ask ScalarSubset. Sending a string set through the scalar
// decider is a question the kernel rightly panics on, which used to
// read here as a refusal.
func foreignScalarSubset(
	ctx *FlowContext, a refinementsets.RefinedSet, b refinementsets.RefinedSet,
) (fits bool, asked bool) {
	if ctx == nil || ctx.Kernel == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			fits, asked = false, false
		}
	}()
	sequenceShaped := refinementsets.StatesSequence(a) || refinementsets.SequenceShaped(a) ||
		refinementsets.StatesSequence(b) || refinementsets.SequenceShaped(b)
	if sequenceShaped {
		if ctx.Kernel.SeqSubset == nil {
			return false, false
		}
		return ctx.Kernel.SeqSubset(a, b), true
	}
	if ctx.Kernel.ScalarSubset == nil {
		return false, false
	}
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
