// split from effect_capture_census.go — the object captures and the walk state

package walk

// capturedObject is one OBJECT capture the census found: a captured
// name every use of which is a MEMBER step, with the members read and
// the members written through a declared path.
//
// The bundle is the record-parameter shape applied to a capture — a
// name worth several leaf entries rather than one. Members is source
// order of first use, so the layout's leaf order and the call site's
// fill order are one order for the same reason the scalar list is.
//
// MethodCalls names the members called AS METHODS on the capture
// (`disconnectSource.removeListener(…)`). They are not reads of a leaf
// value — a method name is a function the leaf vocabulary never held —
// and the layout decides what a call through one may move.
type capturedObject struct {
	Name        string
	Members     []string
	Written     map[string]struct{}
	MethodCalls []string
}

// captureCensus is the state closureCapturedCensus' walk carries over
// one closure body: the names the closure itself bound, the scalar
// reads in source order of first use beside the set that says which
// were already counted, the object captures in source order of their
// first member step, and the one flag that ends the census.
type captureCensus struct {
	bound       map[string]struct{}
	seen        map[string]struct{}
	reads       []string
	objectOrder []string
	objectOf    map[string]*capturedObject
	declined    bool
}

// note records one scalar read of a captured name, in source order of
// first use. A name the closure bound is its own local's, and a name
// already counted keeps its first position.
func (census *captureCensus) note(name string) {
	if _, isBound := census.bound[name]; isBound {
		return
	}
	if _, already := census.seen[name]; already {
		return
	}
	census.seen[name] = struct{}{}
	census.reads = append(census.reads, name)
}

// objectFor hands back the object capture for a name, opening one in
// source order of the first member step if this is that step.
func (census *captureCensus) objectFor(name string) *capturedObject {
	if held, has := census.objectOf[name]; has {
		return held
	}
	fresh := &capturedObject{Name: name, Written: map[string]struct{}{}}
	census.objectOf[name] = fresh
	census.objectOrder = append(census.objectOrder, name)
	return fresh
}

// noteMember records one declared member step on a captured object.
// A member seen twice keeps its first position — the leaf entry is
// one entry however many times the body reads it.
func (census *captureCensus) noteMember(name string, member string) {
	object := census.objectFor(name)
	for _, held := range object.Members {
		if held == member {
			return
		}
	}
	object.Members = append(object.Members, member)
}

// noteMethodCall records one member called as a method on a captured
// object, keeping the first position the same way.
func (census *captureCensus) noteMethodCall(name string, method string) {
	object := census.objectFor(name)
	for _, held := range object.MethodCalls {
		if held == method {
			return
		}
	}
	object.MethodCalls = append(object.MethodCalls, method)
}
