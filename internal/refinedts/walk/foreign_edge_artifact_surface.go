// Reading the target's inbound/outbound surface: which channel(s) the
// __main__ block reads its crossing value(s) from, and the one
// function it calls.

package walk

// foreignSurface is surfaceOf's whole reading: which channel(s) the
// target serves, the argv position an argv-scalar/mixed/file-json
// surface names, and the one function the __main__ block calls.
type foreignSurface struct {
	channel  ForeignSurfaceChannel
	argIndex int
	calls    string
}

// surfaceOf reads the target's inbound/outbound channel: the wire is
// JSON in both directions for "stdin-json", one argv string parsed as a
// float for "argv-scalar", both of those together for
// "stdin-json-argv-scalar", or a file's JSON content named at one argv
// position for "file-json" — the outbound leg (stdout) is JSON in every
// case, since every transport still prints `json.dumps(...)`. The
// edge's whole claim is about the ONE named function — a target whose
// surface names a kind other than these four, or calls nothing this
// artifact names, transports something the JSON model does not
// describe.
func surfaceOf(parsed map[string]any, artifactPath string) (foreignSurface, string) {
	surface, ok := parsed["surface"].(map[string]any)
	if !ok {
		// the producer emits no surface key at all for a harness shape it
		// does not recognize
		return foreignSurface{}, artifactPath + " states no callable surface for its __main__ block " +
			"— a harness shape this producer does not export a surface for — so nothing says what " +
			"the target does with its input and output"
	}
	kind, _ := surface["kind"].(string)
	switch kind {
	case string(ForeignSurfaceStdinJSON):
		return surfaceOfStdinJSON(surface, artifactPath)
	case string(ForeignSurfaceArgvScalar):
		return surfaceOfArgvScalar(surface, artifactPath)
	case string(ForeignSurfaceMixedStdinArgv):
		return surfaceOfMixedStdinArgv(surface, artifactPath)
	case string(ForeignSurfaceFileJSON):
		return surfaceOfFileJSON(surface, artifactPath)
	default:
		return foreignSurface{}, artifactPath + ` states a surface of kind ` + quotedOrNone(kind) +
			`, and this edge applies the JSON transport model only to "stdin-json", "argv-scalar", ` +
			`"stdin-json-argv-scalar", or "file-json"`
	}
}

// surfaceOfStdinJSON reads the stdio surface: JSON in both directions.
func surfaceOfStdinJSON(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdin, _ := surface["stdin"].(string)
	stdout, _ := surface["stdout"].(string)
	if stdin != "json" || stdout != "json" {
		return foreignSurface{}, artifactPath + " states a surface reading " + quotedOrNone(stdin) +
			" on stdin and writing " + quotedOrNone(stdout) +
			" on stdout, and this edge applies the JSON transport model to both legs"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{channel: ForeignSurfaceStdinJSON, calls: called}, ""
}

// surfaceOfArgvScalar reads the argv-scalar surface: the crossing value
// arrives as one argv string at `argIndex`, parsed with Python's
// float(); stdout is still JSON (schema-v2.md's exact spec: {"kind":
// "argv-scalar", "argIndex": 1, "parse": "float", "stdout": "json",
// "calls": "<fn>"} — no stdin field, since nothing crosses on stdin for
// this surface).
func surfaceOfArgvScalar(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdout, _ := surface["stdout"].(string)
	if stdout != "json" {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface writing " +
			quotedOrNone(stdout) + " on stdout, and this edge applies the JSON transport model " +
			"to the return leg"
	}
	parse, _ := surface["parse"].(string)
	if parse != "float" {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface parsing " +
			quotedOrNone(parse) + ", and this edge reads only the \"float\" parse — Python's " +
			"float(sys.argv[n])"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface with no argIndex, " +
			"so nothing says which argv position the target reads its value from"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceArgvScalar,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}

// surfaceOfMixedStdinArgv reads the mixed surface: one value crosses on
// stdin as JSON, and a SECOND value crosses at argv[argIndex], parsed
// with Python's float() — schema-v2.md's exact spec: {"kind":
// "stdin-json-argv-scalar", "stdin": "json", "argIndex": 1, "parse":
// "float", "stdout": "json", "calls": "<fn>"}. The target's own entry
// therefore has exactly TWO rows in this one function's fact: entry[0]
// is the stdin leg's own set, entry[1] the argv leg's — the caller
// (checkOutboundLeg's mixed branch) fits each leg through its own
// existing crossing function rather than through one combined question.
func surfaceOfMixedStdinArgv(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdin, _ := surface["stdin"].(string)
	stdout, _ := surface["stdout"].(string)
	if stdin != "json" || stdout != "json" {
		return foreignSurface{}, artifactPath + " states a mixed surface reading " + quotedOrNone(stdin) +
			" on stdin and writing " + quotedOrNone(stdout) +
			" on stdout, and this edge applies the JSON transport model to both legs"
	}
	parse, _ := surface["parse"].(string)
	if parse != "float" {
		return foreignSurface{}, artifactPath + " states a mixed surface parsing " +
			quotedOrNone(parse) + " on its argv leg, and this edge reads only the \"float\" parse — " +
			"Python's float(sys.argv[n])"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states a mixed surface with no argIndex, " +
			"so nothing says which argv position the target reads its second value from"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceMixedStdinArgv,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}

// surfaceOfFileJSON reads the file-carried surface: the crossing value
// arrives as JSON, but read from a FILE whose path is named at
// argv[argIndex] — the argv string itself carries no data, only the
// path — schema-v2.md's exact spec: {"kind": "file-json", "argIndex": 1,
// "stdout": "json", "calls": "<fn>"}. The target's entry has one row —
// the file's own JSON content — exactly as stdin-json's does; only the
// carrier differs (a file instead of the stdin stream), so the JSON
// transport model itself is shared, never re-derived.
func surfaceOfFileJSON(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdout, _ := surface["stdout"].(string)
	if stdout != "json" {
		return foreignSurface{}, artifactPath + " states a file-json surface writing " +
			quotedOrNone(stdout) + " on stdout, and this edge applies the JSON transport model " +
			"to the return leg"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states a file-json surface with no argIndex, " +
			"so nothing says which argv position names the file the target reads"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceFileJSON,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}
