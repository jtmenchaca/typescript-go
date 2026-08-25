// Scope brackets a span of work with one enter line and one exit
// line, so a sibling lane can mark "a compile started here" and "it
// ended here" without hand-writing both Log calls and keeping their
// field lists in sync.

package diagnose

// Scope logs "<event>.enter" with fields now, and returns a closer
// that logs "<event>.exit" with the same entry fields followed by
// whatever the caller passes at close — so an exit line always shows
// both what started the scope and what it produced.
//
// When diagnosis is off, Scope does no formatting and returns a
// no-op closer: the cost of an unused Scope call is one bool test.
func Scope(event string, fields ...any) func(...any) {
	if !enabled {
		return func(...any) {}
	}
	Log(event+".enter", fields...)
	entryFields := append([]any(nil), fields...)
	return func(exitFields ...any) {
		Log(event+".exit", append(entryFields, exitFields...)...)
	}
}
