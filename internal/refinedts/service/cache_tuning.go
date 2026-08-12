// Speed and measurement. Nothing here can change what the checker
// concludes.
//
// Every cache below is a straight trade of memory for
// re-computation: make one smaller and the checker does the same
// work twice, never different work. Every trace option below only
// observes.
//
// Read analysis_limits.go for the options that DO change conclusions.
//
// Ported 1:1 from service/cache_tuning.ts.
//
// NOTE for the reader landing later: two packages already carry
// inlined copies of values this file owns, both noted at their own
// site because service/ had no Go twin yet when they ported:
//   - internal/refinedts/tracing/trace_state.go's Grain type and the
//     TRACE.grain/enabled defaults (now this file's TraceOptions.Grain
//     and TraceOptions.Enabled)
//   - internal/refinedts/kernelbridge/question_cache.go's
//     questionCacheCapacity constant (now this file's AnswersKept)
//
// Do not edit those packages from this unit — repointing them at this
// file is a later pass's job, same as abstractdomain's
// TrustLevelAdmitted note in analysis_limits.go.
package service

/* ── what is kept in memory ──────────────────────────────────────── */

// AnswersKept: prover answers held in memory, oldest dropped first.
// An answer is a fact about the question's exact text, so reusing one
// is never a guess.
const AnswersKept = 16384

// ProjectsKept: parsed projects held in memory, oldest dropped first.
const ProjectsKept = 64

// CallResultsKept: per function, how many argument combinations the
// checker remembers a result for.
const CallResultsKept = 256

// KeyChars: how long an answer's filing name may get.
//
// Past this the answer is still used, but never filed — so that one
// question is asked again every time it comes up. This is the only
// entry here with a footnote: it is a speed setting, but a question
// that is never filed is also one that keeps paying the prover, and
// analysis_limits.go lists it among the places the checker quietly
// does extra work.
const KeyChars = 16384

// EnumeratedMembers: how wide an integral set may be before the
// checker stops asking which values it holds, one at a time, to say
// it more plainly.
//
// Past this the set is shown as it was built. Nothing is lost but
// brevity — the values are the same either way — and the questions
// are cached, so a set asked about once is free afterwards.
const EnumeratedMembers = 64

// NamedMembers: how many values a set may be shown AS, when they do
// not run consecutively.
//
// `0 | 2 | 4 | 6 | 8 | 10 | 12` is not an improvement on the forms it
// replaced, so past this the set keeps them. A consecutive run is
// exempt: it reads as a range however many values it holds.
const NamedMembers = 6

/* ── what is kept on disk ────────────────────────────────────────── */

// Answers outlive the process: an answer is true about its question
// on every machine, forever. These bound the file, not the truth.

// StoredAnswerBytes: an answer larger than this stays in memory and
// is never written.
const StoredAnswerBytes = 4096

// StoreFileBytes: a stored-answers file larger than this is ignored
// at startup rather than read into memory.
const StoreFileBytes = 32 * 1024 * 1024

/* ── measurement ─────────────────────────────────────────────────── */

// Grain is the TS Grain union.
type Grain string

const (
	// GrainPhase is the handful of stages a check runs through.
	GrainPhase Grain = "phase"
	// GrainStep is the steps inside each stage.
	GrainStep Grain = "step"
	// GrainNode is every expression and statement visited. Slow
	// enough to distort what it measures; the report says by how
	// much.
	GrainNode Grain = "node"
)

// TraceOptions is the TS TraceOptions interface.
type TraceOptions struct {
	// Enabled is the switch. Leave it false on any committed change.
	Enabled bool
	// Grain is how finely to record. Each setting includes the
	// coarser ones.
	Grain Grain
	// PerFile times each file separately, so they can be ranked and a
	// trend across a run can be seen.
	PerFile bool
	// SlowestFiles is how many of the slowest files to list.
	SlowestFiles int
	// WriteTo is a file path, or "" for the terminal (TS `string |
	// null`; the empty path is unambiguous here).
	WriteTo string
	// ReportOnExit prints the report when the run finishes.
	ReportOnExit bool
}

// Trace is the TS TRACE default configuration.
var Trace = TraceOptions{
	Enabled:      false,
	Grain:        GrainStep,
	PerFile:      true,
	SlowestFiles: 15,
	WriteTo:      "",
	ReportOnExit: true,
}
