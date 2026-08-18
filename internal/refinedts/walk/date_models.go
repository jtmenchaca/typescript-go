// from evaluation/date_models.ts
//
// Date models: construction, Date.now, the getter windows the spec
// pins, and the exact reads off exact millis. Split from
// builtin_models.ts per the v2 tree.

package walk

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// seqSubsetAsker adapts *kernelbridge.RefinedTSKernel's SeqSubset
// FUNCTION FIELD to refinementsets.SeqSubsetAsker's METHOD interface
// -- the kernel struct carries its questions as closures
// (kernel_bridge.go builds it that way, mirroring the TS interface of
// live function properties), so a call site needing the method form
// wraps it once here.
type seqSubsetAsker struct {
	kernel *kernelbridge.RefinedTSKernel
}

func (s seqSubsetAsker) SeqSubset(a, b refinementsets.RefinedSet) bool {
	return s.kernel.SeqSubset(a, b)
}

func seqSubsetAskerOf(kernel *kernelbridge.RefinedTSKernel) seqSubsetAsker {
	return seqSubsetAsker{kernel: kernel}
}

var isoDateShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})?)?$`)

// jsDateParse mirrors Date.parse for the ISO_DATE_SHAPE-gated inputs
// this file feeds it: RFC3339-ish strings the shape regex already
// vetted. Returns (millis, false) when Go's time.Parse cannot read
// the text (the TS runtime's Date.parse is more permissive in corners
// this shape gate does not reach; those inputs read as NaN here, the
// same terminal ofMillis(NaN) -> nil the TS source's own callers see).
func jsDateParse(text string) (float64, bool) {
	layouts := []string{
		"2006-01-02T15:04:05.000Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, text); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}

// ReadDateConstruction is readDateConstruction in the TS source:
// `new Date(<literal>)` on the default-lib constructor: a spec-format
// string parses to the host's spec-exact time value; a numeric
// literal is its own. Bare `new Date()` reads the wall clock —
// nondeterministic, left to its reason row.
func ReadDateConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.Text() != "Date" ||
		!resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	var argCount int
	if newExpr.Arguments != nil {
		argCount = len(newExpr.Arguments.Nodes)
	}
	if argCount != 1 {
		// `new Date()` — the current time: an unknown VALID date, whose
		// time value is integral and inside the TimeClip range
		// (sec-time-values-and-time-range)
		if argCount == 0 {
			out := abstractdomain.AbstractValue{
				Kind: abstractdomain.KindDate,
				Millis: millisPtr(abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-8.64e15), refinementsets.AtMost(8.64e15)),
					nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
				)),
			}
			return &out
		}
		return nil
	}
	a := newExpr.Arguments.Nodes[0]
	ofMillis := func(t float64) *abstractdomain.AbstractValue {
		if math.IsNaN(t) {
			return nil
		}
		out := abstractdomain.AbstractValue{
			Kind:   abstractdomain.KindDate,
			Millis: millisPtr(abstractdomain.KnownValues([]float64{t}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)),
		}
		return &out
	}
	if ast.IsNumericLiteral(a) {
		v := float64(jsnum.FromString(a.Text()))
		return ofMillis(v)
	}
	if ast.IsStringLiteral(a) && isoDateShape.MatchString(a.Text()) {
		if t, ok := jsDateParse(a.Text()); ok {
			return ofMillis(t)
		}
		return ofMillis(math.NaN())
	}
	// an exact KNOWN argument reads the same way — a bound parameter,
	// a const — the spec-format gate unchanged
	known := evaluateExpression(ctx, env, a)
	if known.Kind == abstractdomain.KindValues && len(known.Values) >= 1 {
		if known.KindTag == abstractdomain.PrimitiveNumber && len(known.Values) == 1 {
			return ofMillis(known.Values[0])
		}
		if known.KindTag == abstractdomain.PrimitiveString {
			text := stringOf(known.Values)
			if isoDateShape.MatchString(text) {
				if t, ok := jsDateParse(text); ok {
					return ofMillis(t)
				}
				return ofMillis(math.NaN())
			}
		}
	}
	// a SET-known string provably inside the iso-datetime grammar
	// builds a valid Date whose time value lies in the grammar's
	// window — every member parses spec-deterministically
	if known.Kind == abstractdomain.KindSet {
		window, ok := refinementsets.IsoDatetimeWindow(seqSubsetAskerOf(ctx.Kernel), known.Set)
		if ok {
			out := abstractdomain.AbstractValue{
				Kind: abstractdomain.KindDate,
				Millis: millisPtr(abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(refinementsets.AtLeast(window.Lo), refinementsets.AtMost(window.Hi), refinementsets.Integer),
					nil, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(known), abstractdomain.TrustSpec), abstractdomain.SetKindTagNone,
				)),
			}
			return &out
		}
	}
	// any OTHER argument still builds a Date — possibly the invalid
	// one: the [[DateValue]] is NaN or an integral time value in the
	// TimeClip range, and NaN rides beside the window until a check
	// strips it
	out := abstractdomain.AbstractValue{
		Kind: abstractdomain.KindDate,
		Millis: millisPtr(abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-8.64e15), refinementsets.AtMost(8.64e15)),
			nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
		))),
	}
	return &out
}

func millisPtr(v abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	return &v
}

// dateGetterWindow is one entry of DATE_GETTER_WINDOWS in the TS
// source.
type dateGetterWindow struct {
	lo, hi float64
}

// dateGetterWindows is DATE_GETTER_WINDOWS in the TS source: the
// spec-pinned windows of the Date calendar getters, read from the
// vendored spec (tmp/ecma262/spec.html): MonthFromTime 0–11
// (sec-monthfromtime), DateFromTime 1–31 (sec-datefromtime), WeekDay
// 0–6 (sec-weekday), HourFromTime 0–23 (sec-hourfromtime), MinFromTime
// 0–59 (sec-minfromtime), SecFromTime 0–59 (sec-secfromtime),
// msFromTime 0–999 (sec-msfromtime). The year window comes from the
// time-value range ±8.64e15 (sec-time-values-and-time-range): years
// -271821 through 275760. Local and UTC variants share each window.
var dateGetterWindows = map[string]dateGetterWindow{
	"getMonth": {0, 11}, "getUTCMonth": {0, 11},
	"getDate": {1, 31}, "getUTCDate": {1, 31},
	"getDay": {0, 6}, "getUTCDay": {0, 6},
	"getHours": {0, 23}, "getUTCHours": {0, 23},
	"getMinutes": {0, 59}, "getUTCMinutes": {0, 59},
	"getSeconds": {0, 59}, "getUTCSeconds": {0, 59},
	"getMilliseconds": {0, 999}, "getUTCMilliseconds": {0, 999},
	"getFullYear": {-271821, 275760}, "getUTCFullYear": {-271821, 275760},
}

// readDateNow is readDateNow in the TS source: Date.now() — unknown,
// but a TIME VALUE.
func readDateNow(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, receiverExpression, method := site.Ctx, site.ReceiverExpression, site.Method
	call := site.E.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	// Date.now(): the current UTC time value — unknown, but a TIME
	// VALUE: integral and inside the TimeClip range (sec-date.now,
	// sec-time-values-and-time-range)
	if ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "Date" && method == "now" &&
		resolvesToDefaultLib(ctx, receiverExpression) && argCount == 0 {
		out := abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-8.64e15), refinementsets.AtMost(8.64e15)),
			nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
		)
		return &out
	}
	return nil
}

// dateGetterOf runs one calendar getter of Go's time.Time, mirroring
// the JS Date.prototype getter names this file transfers exactly —
// the UTC family only (the LOCAL family reads the host time zone,
// which this file never computes exactly; see the window fallback
// below).
func dateGetterOf(t time.Time, method string) (float64, bool) {
	switch method {
	case "getUTCMonth":
		return float64(int(t.Month()) - 1), true
	case "getUTCDate":
		return float64(t.Day()), true
	case "getUTCDay":
		return float64(int(t.Weekday())), true
	case "getUTCHours":
		return float64(t.Hour()), true
	case "getUTCMinutes":
		return float64(t.Minute()), true
	case "getUTCSeconds":
		return float64(t.Second()), true
	case "getUTCMilliseconds":
		return float64(t.Nanosecond() / 1_000_000), true
	case "getUTCFullYear":
		return float64(t.Year()), true
	}
	return 0, false
}

// readDateMethods is readDateMethods in the TS source: the Date
// reads: getTime/valueOf/toISOString and the calendar getters on a
// built date, and the spec windows on an unknown receiver whose
// static type is Date.
func readDateMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, receiver, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Receiver, site.Method
	collectionReceiver := receiver
	if site.HasTrackedName {
		if held, ok := env.Get(site.TrackedName); ok {
			collectionReceiver = held
		}
	}
	call := e.AsCallExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	// a Date read: the time value, and its spec-exact ISO spelling
	if collectionReceiver.Kind == abstractdomain.KindDate && argCount == 0 {
		m := collectionReceiver.Millis
		if method == "getTime" || method == "valueOf" {
			return m
		}
		if method == "toISOString" && m != nil && m.Kind == abstractdomain.KindValues && len(m.Values) == 1 {
			iso, ok := jsToISOString(m.Values[0])
			if !ok {
				out := silence.Residue()
				return &out
			}
			out := abstractdomain.KnownValues(refinementsets.CodepointsOf(iso), abstractdomain.PrimitiveString, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(*m), abstractdomain.TrustSpec))
			return &out
		}
		// the calendar getters: each is spec-pinned to a window
		// (dateGetterWindows cites the clauses). A UTC getter is a pure
		// function of the time value, so exact millis compute the exact
		// answer; a LOCAL getter reads the host time zone (LocalTime),
		// which the checker cannot pin, so it answers the window. Millis
		// that may be NaN carry NaN beside the window — each getter's
		// own step 2 returns NaN for an invalid date.
		if window, hasWindow := dateGetterWindows[method]; hasWindow {
			grade := abstractdomain.TrustSpec
			if m != nil {
				grade = abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(*m), abstractdomain.TrustSpec)
			}
			if strings.HasPrefix(method, "getUTC") && m != nil && m.Kind == abstractdomain.KindValues &&
				len(m.Values) == 1 && !math.IsNaN(m.Values[0]) {
				t := time.UnixMilli(int64(m.Values[0])).UTC()
				exact, ok := dateGetterOf(t, method)
				if ok && !math.IsInf(exact, 0) {
					out := abstractdomain.KnownValues([]float64{exact}, abstractdomain.PrimitiveNumber, grade)
					return &out
				}
			}
			bounded := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(window.lo), refinementsets.AtMost(window.hi)), nil, grade, abstractdomain.SetKindTagNone)
			if m != nil && m.Kind == abstractdomain.KindPossiblyNaN {
				out := abstractdomain.PossiblyNaN(bounded)
				return &out
			}
			return &bounded
		}
	}
	// an UNKNOWN receiver whose STATIC type is Date: the getters still
	// answer their spec windows, with NaN riding beside them — an
	// invalid date returns NaN (each getter's step 2), and nothing here
	// vouches the date is valid
	if argCount == 0 {
		_, hasWindow := dateGetterWindows[method]
		if hasWindow || method == "getTime" || method == "valueOf" {
			receiverType := typereading.TypeAtLocation(ctx.P.Checker, receiverExpression)
			if receiverType != nil && receiverType.Symbol() != nil && receiverType.Symbol().Name == "Date" {
				// getTime/valueOf return [[DateValue]]: NaN, or an
				// integral time value in the TimeClip range
				// (sec-time-values-and-time-range)
				window, hasWindow := dateGetterWindows[method]
				if !hasWindow {
					window = dateGetterWindow{-8.64e15, 8.64e15}
				}
				inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(window.lo), refinementsets.AtMost(window.hi)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
				out := abstractdomain.PossiblyNaN(inner)
				return &out
			}
		}
	}
	return nil
}

// jsToISOString mirrors Date.prototype.toISOString
// (sec-date.prototype.toisostring): the extended ISO 8601 form, or
// ("", false) where the time value is outside the representable
// range the method itself throws a RangeError over.
func jsToISOString(millis float64) (string, bool) {
	if math.IsNaN(millis) || math.Abs(millis) > 8.64e15 {
		return "", false
	}
	t := time.UnixMilli(int64(millis)).UTC()
	year := t.Year()
	if year < 0 || year > 9999 {
		return "", false // this file's exact reads stay inside the 4-digit calendar year form
	}
	return t.Format("2006-01-02T15:04:05.000Z"), true
}
