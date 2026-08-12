// The RFC 9557 / ISO 8601 grammar of the vendored Temporal
// specification (references/temporal/temporal.txt §13.31), compiled
// production by production into refined sets. The early errors of
// §13.31.1-13.31.3 are IN the sets: month-specific day ranges,
// February 29 admitted exactly in leap years (divisibility by 4, 100,
// and 400 is a regular fact of the digit string, sign-independent),
// the "-000000" year refused, and a time-only spelling that also reads
// as a valid year-month or month-day refused through the difference
// form. The compiled languages are the specification's accepted
// languages exactly.

package refinementsets

// tsgChars is one character drawn from the given alternatives.
func tsgChars(s string) RefinedSet {
	runes := []rune(s)
	points := make([]float64, len(runes))
	for i, r := range runes {
		points[i] = float64(r)
	}
	return MakeRefinedSet(OneOf(points))
}

func tsgLit(s string) RefinedSet {
	return StringTuple(s)
}

var tsgEps = MakeRefinedSet(EmptyTuple)

// tsgSeq is right-nested concatenation of the parts.
func tsgSeq(parts ...RefinedSet) RefinedSet {
	if len(parts) == 0 {
		return tsgEps
	}
	set := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		set = MakeRefinedSet(Concatenation(parts[i], set))
	}
	return set
}

func tsgAlt(parts ...RefinedSet) RefinedSet {
	set := parts[0]
	for _, part := range parts[1:] {
		set = MakeRefinedSet(Union(set, part))
	}
	return set
}

func tsgOpt(part RefinedSet) RefinedSet {
	return tsgAlt(tsgEps, part)
}

// tsgRep is lo..hi copies of a SCALAR character set (bounded
// repetition).
func tsgRep(part RefinedSet, lo int, hi *int) RefinedSet {
	return MakeRefinedSet(RepeatOf(part, lo, hi))
}

func tsgMany(part RefinedSet) RefinedSet {
	return MakeRefinedSet(Star(part))
}

func tsgMinus(a, b RefinedSet) RefinedSet {
	return MakeRefinedSet(Difference(a, b))
}

/* ── Lexical pieces ─────────────────────────────────────────────── */

var tsgDigit = tsgChars("0123456789")
var tsgNonZero = tsgChars("123456789")
var tsgSign = tsgChars("+-")
var tsgAlpha = tsgChars("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
var tsgLowercase = tsgChars("abcdefghijklmnopqrstuvwxyz")

// tsgFraction is TemporalDecimalFraction: a period or comma, then 1-9
// digits.
var tsgFraction = tsgSeq(tsgChars(".,"), tsgRep(tsgDigit, 1, intPtr(9)))

/* ── Years, with the leap rule as a digit-string fact ───────────── */

var tsgYear4 = tsgRep(tsgDigit, 4, intPtr(4))

// tsgYearExtended is DateYear's extended form, minus the prohibited
// "-000000".
var tsgYearExtended = tsgMinus(tsgSeq(tsgSign, tsgRep(tsgDigit, 6, intPtr(6))), tsgLit("-000000"))
var tsgDateYear = tsgAlt(tsgYear4, tsgYearExtended)

// tsgDiv4Pair is divisibility by 4 of the LAST TWO digits: tens even ->
// units 0/4/8, tens odd -> units 2/6.
var tsgDiv4Pair = tsgAlt(
	tsgSeq(tsgChars("02468"), tsgChars("048")),
	tsgSeq(tsgChars("13579"), tsgChars("26")),
)
var tsgDiv4PairNot00 = tsgMinus(tsgDiv4Pair, tsgLit("00"))

// tsgLeapYear is years whose value is a leap year: last two digits
// divisible by 4 and not "00", or last two "00" with the preceding two
// divisible by 4 -- sign-independent, so the signed form shares the
// rule.
var tsgLeapYear = tsgAlt(
	tsgSeq(tsgRep(tsgDigit, 2, intPtr(2)), tsgDiv4PairNot00),
	tsgSeq(tsgDiv4Pair, tsgLit("00")),
	tsgMinus(
		tsgAlt(
			tsgSeq(tsgSign, tsgRep(tsgDigit, 4, intPtr(4)), tsgDiv4PairNot00),
			tsgSeq(tsgSign, tsgRep(tsgDigit, 2, intPtr(2)), tsgDiv4Pair, tsgLit("00")),
		),
		tsgLit("-000000"),
	),
)

/* ── Months and month-specific days ─────────────────────────────── */

var tsgMonth31 = tsgAlt(
	tsgLit("01"), tsgLit("03"), tsgLit("05"), tsgLit("07"),
	tsgLit("08"), tsgLit("10"), tsgLit("12"),
)
var tsgMonth30 = tsgAlt(tsgLit("04"), tsgLit("06"), tsgLit("09"), tsgLit("11"))
var tsgDateMonth = tsgAlt(tsgSeq(tsgChars("0"), tsgNonZero), tsgLit("10"), tsgLit("11"), tsgLit("12"))

var tsgDay31 = tsgAlt(
	tsgSeq(tsgChars("0"), tsgNonZero),
	tsgSeq(tsgChars("12"), tsgDigit),
	tsgLit("30"),
	tsgLit("31"),
)
var tsgDay30 = tsgAlt(tsgSeq(tsgChars("0"), tsgNonZero), tsgSeq(tsgChars("12"), tsgDigit), tsgLit("30"))
var tsgDay29 = tsgAlt(tsgSeq(tsgChars("0"), tsgNonZero), tsgSeq(tsgChars("12"), tsgDigit))
var tsgDay28 = tsgAlt(
	tsgSeq(tsgChars("0"), tsgNonZero),
	tsgSeq(tsgChars("1"), tsgDigit),
	tsgSeq(tsgChars("2"), tsgChars("012345678")),
)

// tsgMonthDayNoLeap is month*day with the day valid for its month,
// February capped at 28 (the leap 29th is added at the date level,
// where the year is known). extended fixes the separator per part.
func tsgMonthDayNoLeap(extended bool) RefinedSet {
	sep := tsgEps
	if extended {
		sep = tsgLit("-")
	}
	return tsgAlt(
		tsgSeq(tsgMonth31, sep, tsgDay31),
		tsgSeq(tsgMonth30, sep, tsgDay30),
		tsgSeq(tsgLit("02"), sep, tsgDay28),
	)
}

// tsgDateSpec is DateSpec with §13.31.2 IsValidDate IN the language:
// any date-year with a month-valid day, plus February 29 exactly under
// a leap year.
func tsgDateSpec(extended bool) RefinedSet {
	sep := tsgEps
	if extended {
		sep = tsgLit("-")
	}
	return tsgAlt(
		tsgSeq(tsgDateYear, sep, tsgMonthDayNoLeap(extended)),
		tsgSeq(tsgLeapYear, sep, tsgLit("02"), sep, tsgLit("29")),
	)
}

// DateSet is dateSet in the TS source.
var DateSet = tsgAlt(tsgDateSpec(true), tsgDateSpec(false))

// DateSpecYearMonth is DateSpecYearMonth (both formats).
var DateSpecYearMonth = tsgAlt(
	tsgSeq(tsgDateYear, tsgLit("-"), tsgDateMonth),
	tsgSeq(tsgDateYear, tsgDateMonth),
)

// tsgMonthDayValid is DateSpecMonthDay with §13.31.1 IsValidMonthDay
// in the language (February 29 always admitted -- month-days organize
// into a leap year).
func tsgMonthDayValid(extended bool) RefinedSet {
	sep := tsgEps
	if extended {
		sep = tsgLit("-")
	}
	return tsgAlt(
		tsgSeq(tsgMonth31, sep, tsgDay31),
		tsgSeq(tsgMonth30, sep, tsgDay30),
		tsgSeq(tsgLit("02"), sep, tsgDay29),
	)
}

// DateSpecMonthDay is DateSpecMonthDay in the TS source.
var DateSpecMonthDay = tsgSeq(
	tsgOpt(tsgLit("--")),
	tsgAlt(tsgMonthDayValid(true), tsgMonthDayValid(false)),
)

/* ── Clock times and offsets ────────────────────────────────────── */

var tsgHour = tsgAlt(tsgSeq(tsgChars("01"), tsgDigit), tsgSeq(tsgChars("2"), tsgChars("0123")))
var tsgMinuteSecond = tsgSeq(tsgChars("012345"), tsgDigit)
var tsgTimeSecond = tsgAlt(tsgMinuteSecond, tsgLit("60"))

func tsgTimeSpec(extended bool) RefinedSet {
	sep := tsgEps
	if extended {
		sep = tsgLit(":")
	}
	return tsgAlt(
		tsgHour,
		tsgSeq(tsgHour, sep, tsgMinuteSecond),
		tsgSeq(tsgHour, sep, tsgMinuteSecond, sep, tsgTimeSecond, tsgOpt(tsgFraction)),
	)
}

// TimeSet is timeSet in the TS source.
var TimeSet = tsgAlt(tsgTimeSpec(true), tsgTimeSpec(false))

// OffsetMinutePrecision is UTCOffset without sub-minute precision
// (also the offset form of a time zone identifier).
var OffsetMinutePrecision = tsgAlt(
	tsgSeq(tsgSign, tsgHour),
	tsgSeq(tsgSign, tsgHour, tsgLit(":"), tsgMinuteSecond),
	tsgSeq(tsgSign, tsgHour, tsgMinuteSecond),
)

// tsgOffsetSubMinute is UTCOffset with sub-minute precision (offsets
// attached to times).
var tsgOffsetSubMinute = tsgAlt(
	OffsetMinutePrecision,
	tsgSeq(
		tsgSign, tsgHour, tsgLit(":"), tsgMinuteSecond, tsgLit(":"), tsgMinuteSecond, tsgOpt(tsgFraction),
	),
	tsgSeq(tsgSign, tsgHour, tsgMinuteSecond, tsgMinuteSecond, tsgOpt(tsgFraction)),
)

var tsgUtcDesignator = tsgChars("Zz")

// tsgDateTimeOffsetZ is DateTimeUTCOffset[+Z].
var tsgDateTimeOffsetZ = tsgAlt(tsgUtcDesignator, tsgOffsetSubMinute)

// tsgDateTimeOffsetNoZ is DateTimeUTCOffset[~Z].
var tsgDateTimeOffsetNoZ = tsgOffsetSubMinute

/* ── Annotations (RFC 9557 suffixes) ────────────────────────────── */

var tsgCritical = tsgOpt(tsgLit("!"))
var tsgTzLeadingChar = tsgAlt(tsgAlpha, tsgChars("._"))
var tsgTzChar = tsgAlt(tsgTzLeadingChar, tsgDigit, tsgChars("-+"))
var tsgTzNameComponent = tsgSeq(tsgTzLeadingChar, tsgMany(tsgTzChar))

// TimeZoneIANAName is TimeZoneIANAName: slash-separated components.
var TimeZoneIANAName = tsgSeq(
	tsgTzNameComponent,
	tsgMany(tsgSeq(tsgLit("/"), tsgTzNameComponent)),
)

// TimeZoneIdentifier is TimeZoneIdentifier: an offset (minute
// precision) or an IANA name.
var TimeZoneIdentifier = tsgAlt(OffsetMinutePrecision, TimeZoneIANAName)

var tsgTimeZoneAnnotation = tsgSeq(tsgLit("["), tsgCritical, TimeZoneIdentifier, tsgLit("]"))

var tsgAKeyLeadingChar = tsgAlt(tsgLowercase, tsgChars("_"))
var tsgAKeyChar = tsgAlt(tsgAKeyLeadingChar, tsgDigit, tsgChars("-"))
var tsgAnnotationKey = tsgSeq(tsgAKeyLeadingChar, tsgMany(tsgAKeyChar))
var tsgAnnotationValueComponent = tsgRep(tsgAlt(tsgAlpha, tsgDigit), 1, nil)

// AnnotationValue is AnnotationValue: dash-separated alphanumeric
// components -- also the calendar identifier grammar.
var AnnotationValue = tsgSeq(
	tsgAnnotationValueComponent,
	tsgMany(tsgSeq(tsgLit("-"), tsgAnnotationValueComponent)),
)
var tsgAnnotation = tsgSeq(tsgLit("["), tsgCritical, tsgAnnotationKey, tsgLit("="), AnnotationValue, tsgLit("]"))
var tsgAnnotations = tsgMany(tsgAnnotation)

/* ── The date-time assemblies ───────────────────────────────────── */

var tsgDateTimeSeparator = tsgChars("Tt ")

// tsgDateTime is DateTime[Z, TimeRequired].
func tsgDateTime(z, timeRequired bool) RefinedSet {
	offset := tsgDateTimeOffsetNoZ
	if z {
		offset = tsgDateTimeOffsetZ
	}
	withTime := tsgSeq(DateSet, tsgDateTimeSeparator, TimeSet, tsgOpt(offset))
	if timeRequired {
		return withTime
	}
	return tsgAlt(DateSet, withTime)
}

// tsgAnnotatedDateTime is AnnotatedDateTime[Zoned, TimeRequired].
func tsgAnnotatedDateTime(zoned, timeRequired bool) RefinedSet {
	if zoned {
		return tsgSeq(tsgDateTime(true, timeRequired), tsgTimeZoneAnnotation, tsgAnnotations)
	}
	return tsgSeq(tsgDateTime(false, timeRequired), tsgOpt(tsgTimeZoneAnnotation), tsgAnnotations)
}

/* ── The goal symbols ───────────────────────────────────────────── */

// PlainDateTimeString is TemporalDateTimeString[~Zoned] -- the
// PlainDate / PlainDateTime spelling (one shared language, per the
// specification).
var PlainDateTimeString = tsgAnnotatedDateTime(false, false)

// ZonedDateTimeString is TemporalDateTimeString[+Zoned] -- the
// ZonedDateTime spelling.
var ZonedDateTimeString = tsgAnnotatedDateTime(true, false)

// InstantString is TemporalInstantString: date, time, and a REQUIRED Z
// or offset.
var InstantString = tsgSeq(
	DateSet, tsgDateTimeSeparator, TimeSet, tsgDateTimeOffsetZ, tsgOpt(tsgTimeZoneAnnotation), tsgAnnotations,
)

// PlainTimeString is TemporalTimeString: an annotated time (the bare
// second alternative refuses spellings that also read as a valid
// year-month or month-day -- §13.31.3, the difference form), or a
// date-time with the time REQUIRED.
var PlainTimeString = tsgAlt(
	tsgSeq(
		tsgChars("Tt"), TimeSet, tsgOpt(tsgDateTimeOffsetNoZ), tsgOpt(tsgTimeZoneAnnotation), tsgAnnotations,
	),
	tsgSeq(
		tsgMinus(
			tsgSeq(TimeSet, tsgOpt(tsgDateTimeOffsetNoZ)),
			tsgAlt(DateSpecMonthDay, DateSpecYearMonth),
		),
		tsgOpt(tsgTimeZoneAnnotation),
		tsgAnnotations,
	),
	tsgAnnotatedDateTime(false, true),
)

// PlainYearMonthString is TemporalYearMonthString.
var PlainYearMonthString = tsgAlt(
	tsgSeq(DateSpecYearMonth, tsgOpt(tsgTimeZoneAnnotation), tsgAnnotations),
	tsgAnnotatedDateTime(false, false),
)

// PlainMonthDayString is TemporalMonthDayString.
var PlainMonthDayString = tsgAlt(
	tsgSeq(DateSpecMonthDay, tsgOpt(tsgTimeZoneAnnotation), tsgAnnotations),
	tsgAnnotatedDateTime(false, false),
)

/* ── Durations ──────────────────────────────────────────────────── */

var tsgDigits = tsgRep(tsgDigit, 1, nil)

var tsgSecondsPart = tsgSeq(tsgDigits, tsgOpt(tsgFraction), tsgChars("Ss"))
var tsgMinutesPart = tsgAlt(
	tsgSeq(tsgDigits, tsgFraction, tsgChars("Mm")),
	tsgSeq(tsgDigits, tsgChars("Mm"), tsgOpt(tsgSecondsPart)),
)
var tsgHoursPart = tsgAlt(
	tsgSeq(tsgDigits, tsgFraction, tsgChars("Hh")),
	tsgSeq(tsgDigits, tsgChars("Hh"), tsgMinutesPart),
	tsgSeq(tsgDigits, tsgChars("Hh"), tsgOpt(tsgSecondsPart)),
)
var tsgDurationTime = tsgAlt(
	tsgSeq(tsgChars("Tt"), tsgHoursPart),
	tsgSeq(tsgChars("Tt"), tsgMinutesPart),
	tsgSeq(tsgChars("Tt"), tsgSecondsPart),
)
var tsgDaysPart = tsgSeq(tsgDigits, tsgChars("Dd"))
var tsgWeeksPart = tsgSeq(tsgDigits, tsgChars("Ww"), tsgOpt(tsgDaysPart))
var tsgMonthsPart = tsgAlt(
	tsgSeq(tsgDigits, tsgChars("Mm"), tsgWeeksPart),
	tsgSeq(tsgDigits, tsgChars("Mm"), tsgOpt(tsgDaysPart)),
)
var tsgYearsPart = tsgAlt(
	tsgSeq(tsgDigits, tsgChars("Yy"), tsgMonthsPart),
	tsgSeq(tsgDigits, tsgChars("Yy"), tsgWeeksPart),
	tsgSeq(tsgDigits, tsgChars("Yy"), tsgOpt(tsgDaysPart)),
)
var tsgDurationDate = tsgAlt(
	tsgSeq(tsgYearsPart, tsgOpt(tsgDurationTime)),
	tsgSeq(tsgMonthsPart, tsgOpt(tsgDurationTime)),
	tsgSeq(tsgWeeksPart, tsgOpt(tsgDurationTime)),
	tsgSeq(tsgDaysPart, tsgOpt(tsgDurationTime)),
)

// DurationString is TemporalDurationString.
var DurationString = tsgSeq(tsgOpt(tsgSign), tsgChars("Pp"), tsgAlt(tsgDurationDate, tsgDurationTime))
