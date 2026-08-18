// from evaluation/web_api_models.ts
//
// What the checker knows about the web platform objects the field
// actually reads: Request, Response, Headers, URLSearchParams, URL.
// Every row transcribes the vendored WHATWG documents —
// tmp/whatwg-fetch/fetch.bs and tmp/whatwg-url/url.bs — the same
// discipline the ecma262 rows follow, applied to the web's own
// specs. Recognition is by the receiver's STATIC type name: these
// classes are host-provided, so the type IS the identity (the same
// standing the Date fallback rows rest on).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

var webClasses = map[string]bool{
	"Request": true, "Response": true, "Headers": true,
	"URLSearchParams": true, "URL": true,
}

// WebClassOf is webClassOf in the TS source: the receiver's
// web-platform class name, or "" (with ok=false) when its static type
// is not one of the five modeled classes.
func WebClassOf(p *program.CheckerProgram, receiver *ast.Node) (string, bool) {
	t := typereading.TypeAtLocation(p.Checker, receiver)
	if t == nil {
		return "", false
	}
	// an intersection (`URLSearchParams & {…}`) is still the class —
	// the branded member changes no getter
	var members []*checker.Type
	if t.IsIntersection() {
		members = t.Types()
	} else {
		members = []*checker.Type{t}
	}
	for _, member := range members {
		symbol := member.Symbol()
		if symbol != nil && webClasses[symbol.Name] {
			return symbol.Name, true
		}
	}
	return "", false
}

func specString() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

func specBool() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

// statusWindow is the TS source's statusWindow: a status is an
// integer in the range 0 to 999 (fetch.bs §Statuses, concept-status).
func statusWindow() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(999)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
}

// stringGetters is STRING_GETTERS in the TS source: the string-valued
// getters of each class. Request: url and method serialize the
// request's URL and method (fetch.bs dom-request-url,
// dom-request-method). Response: url, statusText (fetch.bs
// dom-response attributes). URL: every component getter serializes a
// piece of the record (url.bs §URL class).
var stringGetters = map[string][]string{
	"Request":  {"url", "method", "referrer", "integrity", "destination"},
	"Response": {"url", "statusText", "type"},
	"URL": {
		"href", "origin", "protocol", "username", "password", "host",
		"hostname", "port", "pathname", "search", "hash",
	},
}

var boolGetters = map[string][]string{
	"Request":  {"bodyUsed", "keepalive"},
	"Response": {"ok", "redirected", "bodyUsed"},
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// webPropertyNames is WEB_PROPERTY_NAMES in the TS source: every
// property name a row below models, across all five classes — the
// gate that keeps WebClassOf's type question off receivers whose
// member could never match (`.length`, `.map`, any user key).
var webPropertyNames = buildWebPropertyNames()

func buildWebPropertyNames() map[string]bool {
	out := map[string]bool{"status": true}
	for _, list := range stringGetters {
		for _, name := range list {
			out[name] = true
		}
	}
	for _, list := range boolGetters {
		for _, name := range list {
			out[name] = true
		}
	}
	return out
}

// webMethodNames is WEB_METHOD_NAMES in the TS source: the same gate
// for method calls.
var webMethodNames = map[string]bool{
	"text": true, "json": true, "append": true, "set": true, "delete": true,
	"sort": true, "get": true, "has": true, "getAll": true, "toString": true,
}

// WebPropertyRead is webPropertyRead in the TS source: a property
// read off a web-typed receiver the walk holds no exact knowledge
// about: the getter's spec shape. Nil where no row speaks.
func WebPropertyRead(p *program.CheckerProgram, e *ast.Node) *abstractdomain.AbstractValue {
	pa := e.AsPropertyAccessExpression()
	if !webPropertyNames[pa.Name().Text()] {
		return nil
	}
	className, ok := WebClassOf(p, pa.Expression)
	if !ok {
		return nil
	}
	property := pa.Name().Text()
	if containsString(stringGetters[className], property) {
		out := specString()
		return &out
	}
	if containsString(boolGetters[className], property) {
		out := specBool()
		return &out
	}
	if className == "Response" && property == "status" {
		out := statusWindow()
		return &out
	}
	return nil
}

// WebMethodCall is webMethodCall in the TS source: a method call on a
// web-typed receiver. The Body mixin's readers resolve per fetch.bs
// §Body mixin: text() to the body's UTF-8 decoding (a string), json()
// to whatever JSON value the body spells — which is everything any
// file determines about it. The Headers and URLSearchParams mutators
// return undefined (fetch.bs dom-headers-append/set/delete, url.bs
// §URLSearchParams); their lookups answer a string or null, their
// tests a boolean.
func WebMethodCall(p *program.CheckerProgram, e *ast.Node, receiver *ast.Node, method string) *abstractdomain.AbstractValue {
	if !webMethodNames[method] {
		return nil
	}
	className, ok := WebClassOf(p, receiver)
	if !ok {
		return nil
	}
	call := e.AsCallExpression()
	argCount := 0
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if className == "Request" || className == "Response" {
		if method == "text" && argCount == 0 {
			inner := specString()
			out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
			return &out
		}
		if method == "json" && argCount == 0 {
			if assignability.CollectingReasons() {
				assignability.NoteReason(assignability.ReasonNote{
					Site: "expression",
					Node: e,
					Said: "json() resolves to whatever JSON value the body " +
						"spells — the type is everything this file determines",
					Unsupported: false,
				})
			}
			inner := abstractdomain.Opaque
			out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
			return &out
		}
		return nil
	}
	if className == "Headers" || className == "URLSearchParams" {
		if method == "append" || method == "set" || method == "delete" ||
			(className == "URLSearchParams" && method == "sort") {
			out := abstractdomain.Undef
			return &out
		}
		if method == "get" && argCount == 1 {
			out := abstractdomain.PossiblyUndefined(specString(), "", false, false)
			return &out
		}
		if method == "has" {
			out := specBool()
			return &out
		}
		if className == "URLSearchParams" && method == "getAll" {
			out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Strings)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			return &out
		}
		if method == "toString" && argCount == 0 {
			out := specString()
			return &out
		}
		return nil
	}
	if className == "URL" && method == "toString" {
		out := specString()
		return &out
	}
	return nil
}

// WebIterationElement is webIterationElement in the TS source: what
// one element of a for-of over a web collection is. Headers is
// `iterable<ByteString, ByteString>` (fetch.bs, Headers interface)
// and URLSearchParams is `iterable<USVString, USVString>` (url.bs,
// URLSearchParams interface): entries() — and the default iterator,
// which IS entries — yield [name, value] string pairs; keys() and
// values() yield strings. Nil where the iterable is neither class.
func WebIterationElement(p *program.CheckerProgram, iterable *ast.Node) *abstractdomain.AbstractValue {
	base := iterable
	piece := "entries"
	if ast.IsCallExpression(iterable) {
		call := iterable.AsCallExpression()
		argCount := 0
		if call.Arguments != nil {
			argCount = len(call.Arguments.Nodes)
		}
		if argCount == 0 && ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			method := pa.Name().Text()
			if method == "entries" || method == "keys" || method == "values" {
				base = pa.Expression
				piece = method
			} else {
				return nil
			}
		}
	}
	className, ok := WebClassOf(p, base)
	if !ok || (className != "Headers" && className != "URLSearchParams") {
		return nil
	}
	var out abstractdomain.AbstractValue
	if piece == "entries" {
		out = abstractdomain.KnownList([]abstractdomain.AbstractValue{specString(), specString()}, abstractdomain.TrustSpec)
	} else {
		out = specString()
	}
	return &out
}

// WebNew is webNew in the TS source: a `new` over a web class —
// spelled bare or through globalThis. Response carries its exact
// birth facts: a constructed response's URL is null, so the url
// getter answers "" and redirected answers false (fetch.bs
// dom-response url/redirected getter steps); status is the init's,
// default 200 (fetch.bs ResponseInit), and out of 200..599 the
// constructor throws (initialize a response step 1) — a run that
// throws never reaches the read. ok is status in 200..299 (fetch.bs
// ok status). The other classes read as objects with unstated keys;
// their getters answer through the rows above.
func WebNew(p *program.CheckerProgram, e *ast.Node, statusOf func(init *ast.Node, hasInit bool) *abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	callee := newExpr.Expression
	var className string
	hasClassName := false
	if ast.IsIdentifier(callee) {
		className, hasClassName = callee.Text(), true
	} else if ast.IsPropertyAccessExpression(callee) {
		pa := callee.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "globalThis" {
			className, hasClassName = pa.Name().Text(), true
		}
	}
	if !hasClassName || !webClasses[className] {
		return nil
	}
	t := typereading.TypeAtLocation(p.Checker, e)
	if t == nil || t.Symbol() == nil || t.Symbol().Name != className {
		return nil
	}
	if className != "Response" {
		out := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
		return &out
	}
	var init *ast.Node
	hasInit := false
	if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 1 {
		init, hasInit = newExpr.Arguments.Nodes[1], true
	}
	bare := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(200), refinementsets.AtMost(599)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	var status abstractdomain.AbstractValue
	if !hasInit {
		status = abstractdomain.KnownValues([]float64{200}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
	} else if fromStatus := statusOf(init, hasInit); fromStatus != nil {
		status = *fromStatus
	} else {
		status = bare
	}
	var exact float64
	hasExact := false
	if status.Kind == abstractdomain.KindValues && status.KindTag == abstractdomain.PrimitiveNumber && len(status.Values) == 1 {
		exact, hasExact = status.Values[0], true
	}
	var ok abstractdomain.AbstractValue
	if hasExact {
		v := float64(0)
		if exact >= 200 && exact <= 299 {
			v = 1
		}
		ok = abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
	} else {
		ok = specBool()
	}
	statusField := bare
	if hasExact {
		statusField = status
	}
	out := abstractdomain.KnownObject([]abstractdomain.ObjectKey{
		{Name: "status", Value: statusField},
		{Name: "ok", Value: ok},
		{Name: "url", Value: abstractdomain.KnownValues(refinementsets.CodepointsOf(""), abstractdomain.PrimitiveString, abstractdomain.TrustSpec)},
		{Name: "redirected", Value: abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)},
	}, nil, false, abstractdomain.TrustSpec, false)
	return &out
}
