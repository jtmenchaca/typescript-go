// from assignability/temporal_membership.ts
//
// Temporal calendar lens on a checked position: an exact string
// already inside the grammar against chart validity and stated
// min/max, or flowing annotation bounds implying the target's.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// calendarAskOf adapts refinementsets' map-shaped CalendarAsk seam
// (the TS source's `{ op: string; [key]: unknown }`, and its cast
// `ctx.kernel.calendar(question as never)`) onto
// kernelbridge.RefinedTSKernel.Calendar's TYPED CalendarQuestion
// struct — the two layers ported independently and settled on
// different shapes for the same wire message; this is the one place
// they meet. Every op refinementsets' CalendarAsk closures actually
// pose (epochDays, compareDuration, validDuration) is covered; an
// unrecognized op panics, mirroring the TS source's own unchecked
// `as never` cast finding no matching kernel case.
func calendarAskOf(ctx *FlowContext) refinementsets.CalendarAsk {
	return func(question refinementsets.CalendarQuestion) refinementsets.CalendarAnswer {
		op, _ := question["op"].(string)
		var typed kernelbridge.CalendarQuestion
		switch op {
		case "epochDays":
			typed = kernelbridge.CalendarQuestion{
				Op:    kernelbridge.CalendarOpEpochDays,
				Year:  intOfAny(question["year"]),
				Month: intOfAny(question["month"]),
				Day:   intOfAny(question["day"]),
			}
		case "compareDuration":
			typed = kernelbridge.CalendarQuestion{
				Op: kernelbridge.CalendarOpCompareDuration,
				A:  floatsOfAny(question["a"]),
				B:  floatsOfAny(question["b"]),
			}
		case "validDuration":
			typed = kernelbridge.CalendarQuestion{
				Op:     kernelbridge.CalendarOpValidDuration,
				Fields: floatsOfAny(question["fields"]),
			}
		default:
			panic("calendarAskOf: unrecognized calendar question op " + op)
		}
		return refinementsets.CalendarAnswer(ctx.Kernel.Calendar(typed))
	}
}

func intOfAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func floatsOfAny(v any) []float64 {
	if f, ok := v.([]float64); ok {
		return f
	}
	return nil
}

// CheckTemporalExact is checkTemporalExact in the TS source: chart
// validity and stated bounds on one exact ISO spelling.
func CheckTemporalExact(
	ctx *FlowContext,
	temporal refinementsets.TemporalAnnotation,
	text string,
	node *ast.Node,
	what string,
	spelledTarget string,
	boundsStated bool,
) {
	calendarAsk := calendarAskOf(ctx)
	validity := refinementsets.ChartValidity(calendarAsk, temporal.Chart, text)
	if validity.Kind == "refuted" {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+jsonQuote(text)+"' is not "+
				"assignable to type '"+spelledTarget+"' — "+validity.Why,
		))
		return
	}
	if !boundsStated {
		return
	}
	verdict := refinementsets.BoundsVerdictOf(calendarAsk, temporal, text)
	if verdict.Kind == "refuted" {
		ctx.Report(assignability.At(
			node,
			7001,
			what+" of type '"+jsonQuote(text)+"' is not "+
				"assignable to type '"+spelledTarget+"'",
		))
	} else if verdict.Kind == "alert" {
		ctx.Report(assignability.At(node, 7002, assignability.AlertText+" "+verdict.Why+"."))
	}
}

// CheckTemporalBounds is checkTemporalBounds in the TS source:
// flowing chart bounds against a bounded temporal target. The
// language already fits; bounds never pass on that alone.
func CheckTemporalBounds(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	temporal refinementsets.TemporalAnnotation,
	node *ast.Node,
	spelledTarget string,
) {
	calendarAsk := calendarAskOf(ctx)
	flowing := known.Temporal
	if flowing == nil {
		ctx.Report(assignability.At(
			node,
			7002,
			assignability.AlertText+" The position states '"+spelledTarget+"' and "+
				"the value's chart bounds are not known here.",
		))
		return
	}
	verdict := refinementsets.BoundsImply(calendarAsk, *flowing, temporal)
	if verdict.Kind != "proved" {
		message := assignability.AlertText + " The position states '" + spelledTarget + "'"
		if verdict.Kind == "alert" {
			message += " — " + verdict.Why + "."
		} else {
			message += "."
		}
		ctx.Report(assignability.At(node, 7002, message))
	}
}
