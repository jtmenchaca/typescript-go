// The ISO calendar questions (the vendored Temporal spec,
// §13.1–13.3 and §7.5). Marshaled as JSON of this shape.
package kernelbridge

// CalendarQuestionOp is the tag of a CalendarQuestion.
type CalendarQuestionOp string

const (
	CalendarOpEpochDays       CalendarQuestionOp = "epochDays"
	CalendarOpIsoDate         CalendarQuestionOp = "isoDate"
	CalendarOpValidDate       CalendarQuestionOp = "validDate"
	CalendarOpValidDuration   CalendarQuestionOp = "validDuration"
	CalendarOpCompareDuration CalendarQuestionOp = "compareDuration"
	// CalendarOpWeekday, CalendarOpToOrdinal, and CalendarOpIsoCalendar
	// ask arms the compiled dylib already carries (boundary/
	// exports_calendar.lean's "weekday"/"toordinal"/"isoCalendar" cases,
	// confirmed present in the built .lake/build/ir/boundary/
	// exports_calendar.c string table) and refinedpy's expressions.rs
	// already asks (date_weekday_value/date_toordinal_value/
	// date_isocalendar_value) — this file is the Go side's own binding
	// to the SAME three arms, not a new kernel question.
	CalendarOpWeekday     CalendarQuestionOp = "weekday"
	CalendarOpToOrdinal   CalendarQuestionOp = "toordinal"
	CalendarOpIsoCalendar CalendarQuestionOp = "isoCalendar"
)

// CalendarQuestion is the TS CalendarQuestion discriminated union,
// collapsed to one struct with an Op tag per the port's convention:
// only the fields the Op uses are meaningful.
//
//	epochDays / validDate / weekday / toordinal / isoCalendar: Year, Month, Day
//	isoDate: Days
//	validDuration: Fields
//	compareDuration: A, B
type CalendarQuestion struct {
	Op CalendarQuestionOp

	Year  int
	Month int
	Day   int

	Days int

	Fields []float64

	A []float64
	B []float64
}
