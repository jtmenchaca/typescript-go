// The calendar lens: an ISO spelling read onto its CHART -- epoch days
// for a date, nanoseconds of day for a time, the (day, ns) pair for a
// date-time or an exact instant, epoch months for a year-month, the
// ten-field vector for a duration. The field split runs here on
// GRAMMAR-VERIFIED text (mirroring §13.31's field structure); every
// calendar conversion and every duration comparison is decided by the
// KERNEL's calendar (the normative §13.1-13.3 port with per-answer
// self-certification). A spelling whose exact point needs time zone
// DATA (a zone name without an offset) is UNPROVABLE here -- the
// honest alert, never a guess.

package refinementsets

import (
	"regexp"
	"strconv"
	"strings"
)

// TemporalChart is the Temporal charts (the vendored spec's types).
// Each annotation set is the type's ACCEPTED SPELLING language
// (§13.31); the chart names which calendar/clock reading orders it,
// and .min/.max state chart bounds as their own ISO spellings.
type TemporalChart string

const (
	ChartPlainDate      TemporalChart = "plainDate"
	ChartPlainDateTime  TemporalChart = "plainDateTime"
	ChartPlainTime      TemporalChart = "plainTime"
	ChartPlainYearMonth TemporalChart = "plainYearMonth"
	ChartPlainMonthDay  TemporalChart = "plainMonthDay"
	ChartInstant        TemporalChart = "instant"
	ChartZonedDateTime  TemporalChart = "zonedDateTime"
	ChartDuration       TemporalChart = "duration"
	ChartTimeZone       TemporalChart = "timeZone"
	ChartCalendar       TemporalChart = "calendar"
)

// TemporalAnnotation is TemporalAnnotation in the TS source. Min/Max
// use (string, bool) pairs rather than *string, per the port's
// convention for `T | null` where the zero value ("") is ambiguous
// with a real (if degenerate) ISO spelling -- an empty string is never
// a valid spelling here, but the explicit bool keeps the port's rule
// uniform rather than special-casing this one field.
type TemporalAnnotation struct {
	Chart  TemporalChart
	Min    string
	HasMin bool
	Max    string
	HasMax bool
}

// CalendarQuestion is the kernel calendar seam's question shape: an
// op tag plus op-specific fields, carried as a plain map (the TS
// source's `{ op: string; [key: string]: unknown }`).
type CalendarQuestion map[string]any

// CalendarAnswer is the kernel calendar seam's answer shape (the TS
// source's `Record<string, unknown>`).
type CalendarAnswer map[string]any

// CalendarAsk is the kernel's calendar seam (structurally the
// boundary's calendar method).
type CalendarAsk func(question CalendarQuestion) CalendarAnswer

const nsPerDay = 86_400_000_000_000

/* ── Field splitting (on grammar-verified text) ─────────────────── */

var dateRe = regexp.MustCompile(`^([+-]\d{6}|\d{4})-?(\d{2})-?(\d{2})`)
var timeRe = regexp.MustCompile(`^(\d{2})(?::?(\d{2})(?::?(\d{2})(?:[.,](\d{1,9}))?)?)?`)

// ymReBase is the TS source's YM_RE with its `(?![-\d])` negative
// lookahead removed -- Go's RE2 engine does not support lookahead. See
// the 1:1-impossible note in the port report; matchYearMonth below
// re-checks the character following the match by hand instead, which
// is exactly what the lookahead tests.
var ymReBase = regexp.MustCompile(`^([+-]\d{6}|\d{4})-?(\d{2})`)

func matchYearMonth(text string) []string {
	m := ymReBase.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	rest := text[len(m[0]):]
	if len(rest) > 0 && (rest[0] == '-' || isDigitByte(rest[0])) {
		return nil
	}
	return m
}

func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}

func fractionNs(digits string, has bool) float64 {
	if !has {
		return 0
	}
	padded := digits
	for len(padded) < 9 {
		padded += "0"
	}
	v, _ := strconv.ParseFloat(padded, 64)
	return v
}

type timeParts struct {
	ns   float64
	rest string
}

func readTime(text string) (timeParts, bool) {
	m := timeRe.FindStringSubmatch(text)
	if m == nil || len(m[0]) == 0 {
		return timeParts{}, false
	}
	hour, _ := strconv.ParseFloat(m[1], 64)
	minute := 0.0
	if m[2] != "" {
		minute, _ = strconv.ParseFloat(m[2], 64)
	}
	// the leap-second spelling :60 reads as :59 (the spec constrains)
	second := 0.0
	if m[3] != "" {
		s, _ := strconv.ParseFloat(m[3], 64)
		second = minFloat(s, 59)
	}
	ns := hour*3_600_000_000_000 + minute*60_000_000_000 +
		second*1_000_000_000 + fractionNs(m[4], m[4] != "")
	return timeParts{ns: ns, rest: text[len(m[0]):]}, true
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type offsetParts struct {
	ns   float64
	rest string
}

var offsetRe = regexp.MustCompile(`^([+-])(\d{2})(?::?(\d{2})(?::?(\d{2})(?:[.,](\d{1,9}))?)?)?`)

// readOffset is the offset in nanoseconds; "Z" reads as 0; ok=false =
// none present.
func readOffset(text string) (offsetParts, bool) {
	if strings.HasPrefix(text, "Z") || strings.HasPrefix(text, "z") {
		return offsetParts{ns: 0, rest: text[1:]}, true
	}
	m := offsetRe.FindStringSubmatch(text)
	if m == nil {
		return offsetParts{}, false
	}
	sign := 1.0
	if m[1] == "-" {
		sign = -1
	}
	hourVal, _ := strconv.ParseFloat(m[2], 64)
	magnitude := hourVal * 3_600_000_000_000
	if m[3] != "" {
		v, _ := strconv.ParseFloat(m[3], 64)
		magnitude += v * 60_000_000_000
	}
	if m[4] != "" {
		v, _ := strconv.ParseFloat(m[4], 64)
		magnitude += v * 1_000_000_000
	}
	magnitude += fractionNs(m[5], m[5] != "")
	return offsetParts{ns: sign * magnitude, rest: text[len(m[0]):]}, true
}

// dateTimeParts is DateTimeParts in the TS source. TimeNs / OffsetNs
// use (value, bool) pairs for "no time part was spelled" / "no offset
// was spelled" -- the TS source's `number | null`.
type dateTimeParts struct {
	year      float64
	month     float64
	day       float64
	timeNs    float64
	hasTimeNs bool
	offsetNs  float64
	hasOffset bool
}

var dateTimeSeparatorRe = regexp.MustCompile(`^[Tt ]`)

// leadingTDesignatorStripped mirrors text.replace(/^[Tt]/, "") --
// strips one leading T or t designator, case-insensitively, if
// present.
func leadingTDesignatorStripped(text string) string {
	if len(text) > 0 && (text[0] == 'T' || text[0] == 't') {
		return text[1:]
	}
	return text
}

func readDateTime(text string) (dateTimeParts, bool) {
	m := dateRe.FindStringSubmatch(text)
	if m == nil {
		return dateTimeParts{}, false
	}
	year, _ := strconv.ParseFloat(m[1], 64)
	month, _ := strconv.ParseFloat(m[2], 64)
	day, _ := strconv.ParseFloat(m[3], 64)
	rest := text[len(m[0]):]
	parts := dateTimeParts{year: year, month: month, day: day}
	if dateTimeSeparatorRe.MatchString(rest) {
		if time, ok := readTime(rest[1:]); ok {
			parts.timeNs = time.ns
			parts.hasTimeNs = true
			rest = time.rest
			if offset, ok := readOffset(rest); ok {
				parts.offsetNs = offset.ns
				parts.hasOffset = true
			}
		}
	}
	return parts, true
}

/* ── Charts ─────────────────────────────────────────────────────── */

// ChartReading is ChartReading in the TS source: a discriminated union
// collapsed to a struct with a Kind field, per the port's convention.
type ChartReading struct {
	Kind  string // "point" | "unprovable"
	Point []float64
	Why   string
}

func chartPoint(values ...float64) ChartReading {
	return ChartReading{Kind: "point", Point: values}
}

func chartUnprovable(why string) ChartReading {
	return ChartReading{Kind: "unprovable", Why: why}
}

func epochDaysOf(ask CalendarAsk, parts dateTimeParts) float64 {
	answer := ask(CalendarQuestion{
		"op":    "epochDays",
		"year":  parts.year,
		"month": parts.month,
		"day":   parts.day,
	})
	return answer["days"].(float64)
}

// normalizedDayNs normalizes a (day, ns) pair after an offset
// subtraction.
func normalizedDayNs(day, ns float64) (float64, float64) {
	d := day
	n := ns
	for n < 0 {
		d -= 1
		n += nsPerDay
	}
	for n >= nsPerDay {
		d += 1
		n -= nsPerDay
	}
	return d, n
}

// durationFieldUnitRe matches one duration field: a count, an optional
// fraction, and a unit letter.
var durationFieldUnitRe = regexp.MustCompile(`^(\d+)(?:[.,](\d{1,9}))?([YyMmWwDdHhSs])`)
var durationSignPrefixRe = regexp.MustCompile(`^[+-]?[Pp]`)
var durationTimeMarkerRe = regexp.MustCompile(`^[Tt]`)

// durationFields is ten duration fields from a spelled duration,
// fractions folded exactly into nanoseconds. ok=false where a count
// exceeds the exact double range (more than 15 digits).
func durationFields(text string) ([]float64, bool) {
	sign := 1.0
	if strings.HasPrefix(text, "-") {
		sign = -1
	}
	rest := durationSignPrefixRe.ReplaceAllString(text, "")
	fields := struct{ y, mo, w, d, h, mi, s, ns float64 }{}
	inTime := false
	for len(rest) > 0 {
		if durationTimeMarkerRe.MatchString(rest) {
			inTime = true
			rest = rest[1:]
			continue
		}
		m := durationFieldUnitRe.FindStringSubmatch(rest)
		if m == nil {
			break
		}
		if len(m[1]) > 15 {
			return nil, false // beyond exact doubles
		}
		count, _ := strconv.ParseFloat(m[1], 64)
		frac := fractionNs(m[2], m[2] != "")
		unit := strings.ToUpper(m[3])
		if !inTime {
			switch unit {
			case "Y":
				fields.y = count
			case "M":
				fields.mo = count
			case "W":
				fields.w = count
			case "D":
				fields.d = count
			}
		} else {
			switch unit {
			case "H":
				fields.h = count
				fields.ns += frac * 3600
			case "M":
				fields.mi = count
				fields.ns += frac * 60
			case "S":
				fields.s = count
				fields.ns += frac
			}
		}
		rest = rest[len(m[0]):]
	}
	return []float64{
		sign * fields.y,
		sign * fields.mo,
		sign * fields.w,
		sign * fields.d,
		sign * fields.h,
		sign * fields.mi,
		sign * fields.s,
		0,
		0,
		sign * fields.ns,
	}, true
}

// ChartReadingOrPanic reads a spelling onto its chart. Kernel refusals
// (an invalid or out-of-range date) PANIC (the TS throw) -- the caller
// reads a panic on the VALUE as a refutation and a panic on a stated
// BOUND as the annotation's own fault.
func chartReading(ask CalendarAsk, chart TemporalChart, text string) ChartReading {
	switch chart {
	case ChartPlainDate:
		parts, ok := readDateTime(text)
		if !ok {
			return chartUnprovable("the spelling did not split")
		}
		return chartPoint(epochDaysOf(ask, parts))
	case ChartPlainDateTime:
		parts, ok := readDateTime(text)
		if !ok {
			return chartUnprovable("the spelling did not split")
		}
		timeNs := 0.0
		if parts.hasTimeNs {
			timeNs = parts.timeNs
		}
		return chartPoint(epochDaysOf(ask, parts), timeNs)
	case ChartPlainTime:
		dated, dateOk := readDateTime(text)
		if dateOk && dated.hasTimeNs {
			return chartPoint(dated.timeNs)
		}
		time, ok := readTime(leadingTDesignatorStripped(text))
		if !ok {
			return chartUnprovable("the spelling did not split")
		}
		return chartPoint(time.ns)
	case ChartPlainYearMonth:
		m := matchYearMonth(text)
		if m == nil {
			m = dateRe.FindStringSubmatch(text)
		}
		if m == nil {
			return chartUnprovable("the spelling did not split")
		}
		year, _ := strconv.ParseFloat(m[1], 64)
		month, _ := strconv.ParseFloat(m[2], 64)
		return chartPoint(year*12 + (month - 1))
	case ChartInstant, ChartZonedDateTime:
		parts, ok := readDateTime(text)
		if !ok {
			return chartUnprovable("the spelling did not split")
		}
		if !parts.hasOffset {
			return chartUnprovable("the exact time of a zone-named spelling needs the zone's data")
		}
		day := epochDaysOf(ask, parts)
		timeNs := 0.0
		if parts.hasTimeNs {
			timeNs = parts.timeNs
		}
		d, n := normalizedDayNs(day, timeNs-parts.offsetNs)
		return chartPoint(d, n)
	case ChartDuration:
		fields, ok := durationFields(text)
		if !ok {
			return chartUnprovable("a stated count exceeds the exact number range")
		}
		return ChartReading{Kind: "point", Point: fields}
	case ChartPlainMonthDay, ChartTimeZone, ChartCalendar:
		return chartUnprovable("a " + string(chart) + " has no ordering chart")
	}
	return chartUnprovable("unrecognized chart")
}

// CompareResult is the (number | ChartReading) union CompareOnChart
// returns in the TS source, collapsed to a struct: Ok true carries the
// sign, Ok false carries the unprovable reading.
type CompareResult struct {
	Ok      bool
	Sign    int
	Reading ChartReading
}

// CompareOnChart compares two spellings on one chart: -1, 0, 1 --
// durations through the kernel's exact totals, points
// lexicographically.
func CompareOnChart(ask CalendarAsk, chart TemporalChart, a, b string) CompareResult {
	if chart == ChartDuration {
		fa, faOk := durationFields(a)
		fb, fbOk := durationFields(b)
		if !faOk || !fbOk {
			return CompareResult{Reading: chartUnprovable("a stated count exceeds the exact number range")}
		}
		answer := ask(CalendarQuestion{"op": "compareDuration", "a": fa, "b": fb})
		sign := answer["sign"].(float64)
		return CompareResult{Ok: true, Sign: int(sign)}
	}
	ra := chartReading(ask, chart, a)
	if ra.Kind != "point" {
		return CompareResult{Reading: ra}
	}
	rb := chartReading(ask, chart, b)
	if rb.Kind != "point" {
		return CompareResult{Reading: rb}
	}
	length := len(ra.Point)
	if len(rb.Point) > length {
		length = len(rb.Point)
	}
	for i := 0; i < length; i++ {
		x := pointAt(ra.Point, i)
		y := pointAt(rb.Point, i)
		if x < y {
			return CompareResult{Ok: true, Sign: -1}
		}
		if y < x {
			return CompareResult{Ok: true, Sign: 1}
		}
	}
	return CompareResult{Ok: true, Sign: 0}
}

func pointAt(point []float64, i int) float64 {
	if i < len(point) {
		return point[i]
	}
	return 0
}

// BoundsVerdict is BoundsVerdict in the TS source: a discriminated
// union collapsed to a struct with a Kind field.
type BoundsVerdict struct {
	Kind    string // "proved" | "refuted" | "alert"
	Against string // "min" | "max", set when Kind == "refuted"
	Why     string // set when Kind == "alert"
}

// ValidityVerdict is ValidityVerdict in the TS source.
type ValidityVerdict struct {
	Kind string // "valid" | "refuted"
	Why  string
}

func refutedValidity(why string) ValidityVerdict {
	return ValidityVerdict{Kind: "refuted", Why: why}
}

// ChartValidity is the chart's own validity, beyond the grammar: the
// representable ranges (PlainDate/PlainDateTime through the kernel's
// calendar; PlainYearMonth's edge months and the instant's exact
// endpoints per the spec's WithinLimits operations) and a duration's
// field bounds (IsValidDuration through the kernel). The grammar
// already carries field validity and the leap rule, so what remains
// here is exactly the range layer.
//
// Kernel refusals inside chartReading / epochDaysOf PANIC (the TS
// throw); this function recovers that panic into a refuted verdict,
// mirroring the TS source's try/catch.
func ChartValidity(ask CalendarAsk, chart TemporalChart, text string) (result ValidityVerdict) {
	defer func() {
		if r := recover(); r != nil {
			result = refutedValidity(panicMessage(r))
		}
	}()
	switch chart {
	case ChartPlainDate, ChartPlainDateTime:
		parts, ok := readDateTime(text)
		if !ok {
			return ValidityVerdict{Kind: "valid"}
		}
		epochDaysOf(ask, parts) // panics outside the range
		return ValidityVerdict{Kind: "valid"}
	case ChartPlainYearMonth:
		// ISOYearMonthWithinLimits: years -271821...275760, with the
		// edge years capped at April and September
		m := matchYearMonth(text)
		if m == nil {
			m = dateRe.FindStringSubmatch(text)
		}
		if m == nil {
			return ValidityVerdict{Kind: "valid"}
		}
		year, _ := strconv.ParseFloat(m[1], 64)
		month, _ := strconv.ParseFloat(m[2], 64)
		if year < -271821 || year > 275760 {
			return refutedValidity("the year-month is outside the representable range")
		}
		if year == -271821 && month < 4 {
			return refutedValidity("the year-month is outside the representable range")
		}
		if year == 275760 && month > 9 {
			return refutedValidity("the year-month is outside the representable range")
		}
		return ValidityVerdict{Kind: "valid"}
	case ChartInstant, ChartZonedDateTime:
		parts, ok := readDateTime(text)
		if !ok {
			return ValidityVerdict{Kind: "valid"}
		}
		day := epochDaysOf(ask, parts) // the date part's range
		if parts.hasOffset {
			// the exact instant: within +-10^8 days of ns, endpoints
			// exact (nsMaxInstant = 10^8 * nsPerDay)
			timeNs := 0.0
			if parts.hasTimeNs {
				timeNs = parts.timeNs
			}
			d, n := normalizedDayNs(day, timeNs-parts.offsetNs)
			belowMin := d < -100_000_000
			aboveMax := d > 100_000_000 || (d == 100_000_000 && n > 0)
			if belowMin || aboveMax {
				return refutedValidity("the instant is outside the representable range")
			}
		}
		return ValidityVerdict{Kind: "valid"}
	case ChartDuration:
		fields, ok := durationFields(text)
		if !ok {
			return refutedValidity("a stated count exceeds the exact number range")
		}
		answer := ask(CalendarQuestion{"op": "validDuration", "fields": fields})
		if valid, _ := answer["valid"].(bool); valid {
			return ValidityVerdict{Kind: "valid"}
		}
		return refutedValidity("the duration is outside its field bounds")
	default:
		return ValidityVerdict{Kind: "valid"}
	}
}

func panicMessage(r any) string {
	if err, ok := r.(error); ok {
		return err.Error()
	}
	if s, ok := r.(string); ok {
		return s
	}
	return "unknown panic"
}

// BoundsVerdictOf is the stated chart bounds against one spelled
// value. Named BoundsVerdictOf, not BoundsVerdict, to keep the type
// name BoundsVerdict free.
func BoundsVerdictOf(ask CalendarAsk, temporal TemporalAnnotation, value string) (result BoundsVerdict) {
	defer func() {
		if r := recover(); r != nil {
			result = BoundsVerdict{Kind: "alert", Why: panicMessage(r)}
		}
	}()
	sides := []struct {
		name  string
		bound string
		has   bool
	}{
		{"min", temporal.Min, temporal.HasMin},
		{"max", temporal.Max, temporal.HasMax},
	}
	for _, side := range sides {
		if !side.has {
			continue
		}
		cmp := CompareOnChart(ask, temporal.Chart, value, side.bound)
		if !cmp.Ok {
			return BoundsVerdict{Kind: "alert", Why: cmp.Reading.Why}
		}
		if side.name == "min" && cmp.Sign < 0 {
			return BoundsVerdict{Kind: "refuted", Against: "min"}
		}
		if side.name == "max" && cmp.Sign > 0 {
			return BoundsVerdict{Kind: "refuted", Against: "max"}
		}
	}
	return BoundsVerdict{Kind: "proved"}
}

// BoundsImply is whether one annotation's bounds imply another's --
// what admits an annotation-typed FLOW at a bounded position without a
// literal. "alert" wherever implication is not proved.
func BoundsImply(ask CalendarAsk, flowing, target TemporalAnnotation) (result BoundsVerdict) {
	defer func() {
		if r := recover(); r != nil {
			result = BoundsVerdict{Kind: "alert", Why: panicMessage(r)}
		}
	}()
	if flowing.Chart != target.Chart {
		return BoundsVerdict{Kind: "alert", Why: "the charts differ"}
	}
	sides := []struct {
		name         string
		targetBound  string
		hasTarget    bool
		flowingBound string
		hasFlowing   bool
	}{
		{"min", target.Min, target.HasMin, flowing.Min, flowing.HasMin},
		{"max", target.Max, target.HasMax, flowing.Max, flowing.HasMax},
	}
	for _, side := range sides {
		if !side.hasTarget {
			continue
		}
		if !side.hasFlowing {
			return BoundsVerdict{Kind: "alert", Why: "the flowing type states no ." + side.name}
		}
		cmp := CompareOnChart(ask, target.Chart, side.flowingBound, side.targetBound)
		if !cmp.Ok {
			return BoundsVerdict{Kind: "alert", Why: cmp.Reading.Why}
		}
		if side.name == "min" && cmp.Sign < 0 {
			return BoundsVerdict{Kind: "alert", Why: "the flowing .min sits below"}
		}
		if side.name == "max" && cmp.Sign > 0 {
			return BoundsVerdict{Kind: "alert", Why: "the flowing .max sits above"}
		}
	}
	return BoundsVerdict{Kind: "proved"}
}

// chartNames is the display names: the Temporal classes for the
// class-backed types; time zone and calendar identifiers are strings
// at Stage 4 (no such classes), so they read bare.
var chartNames = map[TemporalChart]string{
	ChartPlainDate:      "Temporal.PlainDate",
	ChartPlainDateTime:  "Temporal.PlainDateTime",
	ChartPlainTime:      "Temporal.PlainTime",
	ChartPlainYearMonth: "Temporal.PlainYearMonth",
	ChartPlainMonthDay:  "Temporal.PlainMonthDay",
	ChartInstant:        "Temporal.Instant",
	ChartZonedDateTime:  "Temporal.ZonedDateTime",
	ChartDuration:       "Temporal.Duration",
	ChartTimeZone:       "TimeZone",
	ChartCalendar:       "Calendar",
}

// FormatTemporal is how a temporal statement reads in a message or
// hover: Temporal.PlainDate {2020-01-01 <= x <= 2024-12-31} -- the
// chart name as the type, the bounds in the braces; a two-sided window
// chains over the mathematical italic x, a lone bound keeps the bare
// comparison.
func FormatTemporal(temporal TemporalAnnotation) string {
	name := chartNames[temporal.Chart]
	var bounds string
	switch {
	case temporal.HasMin && temporal.HasMax:
		bounds = temporal.Min + " ≤ 𝑥 ≤ " + temporal.Max
	case temporal.HasMin:
		bounds = "≥ " + temporal.Min
	case temporal.HasMax:
		bounds = "≤ " + temporal.Max
	default:
		return name
	}
	return name + " {" + bounds + "}"
}
