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
)

// CalendarQuestion is the TS CalendarQuestion discriminated union,
// collapsed to one struct with an Op tag per the port's convention:
// only the fields the Op uses are meaningful.
//
//	epochDays / validDate: Year, Month, Day
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
