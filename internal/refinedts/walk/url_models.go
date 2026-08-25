// The URL class over an EXACTLY-KNOWN url string (url.bs, §URL class):
// parsing is a pure function of the input string, so a `new URL(s)`
// with s exactly known has every component getter exactly known too,
// and its query parameters exactly known with them.
//
// The generic shape — `new URL(someString)` — keeps web_api_models.go's
// own rows: an object with unstated keys, whose getters answer the
// spec's string sort. This file only adds the exact reading for the one
// input the walk can pin, the same "the host running the check is the
// host that will run the program" standing the exact string-method
// reads rest on. Go's net/url implements RFC 3986 parsing; the WHATWG
// basic URL parser agrees with it on the absolute http(s) URLs this
// reader admits, and any input it disagrees on — a relative reference,
// a non-special scheme, a URL carrying userinfo or a non-ASCII host —
// is refused here rather than answered from the weaker parser.

package walk

import (
	"net/url"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// urlSearchParamsKey is the name the exact-URL object carries its
// parsed query under. It is the getter's own name (url.bs
// dom-url-searchparams), so an ordinary `u.searchParams` read finds it
// through the plain object-key path with no extra reader.
const urlSearchParamsKey = "searchParams"

// exactUrlObject is the object a `new URL(s)` builds for an exactly-
// known, absolute http(s) string s: each component getter's own exact
// serialization (url.bs §URL class), plus searchParams as a COMPLETE
// object of the query's own names. Complete on both levels: parsing
// fixes the component set and the parameter set exactly, so a name the
// query does not carry reads as absent rather than as the walk's gap.
// (AbstractValue{}, false) for any input outside the admitted shape.
func exactUrlObject(text string, grade abstractdomain.TrustLevel) (abstractdomain.AbstractValue, bool) {
	parsed, err := url.Parse(text)
	if err != nil || parsed == nil {
		return abstractdomain.AbstractValue{}, false
	}
	// only the special schemes whose serialization net/url and the
	// WHATWG parser agree on, with no userinfo and an ASCII host — the
	// two places the parsers diverge
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return abstractdomain.AbstractValue{}, false
	}
	if parsed.User != nil || parsed.Host == "" || !isASCIIText(parsed.Host) {
		return abstractdomain.AbstractValue{}, false
	}
	if !isASCIIText(text) {
		return abstractdomain.AbstractValue{}, false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return abstractdomain.AbstractValue{}, false
	}
	word := func(s string) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(s), abstractdomain.PrimitiveString, grade)
	}
	pathname := parsed.EscapedPath()
	if pathname == "" {
		pathname = "/"
	}
	search := ""
	if parsed.RawQuery != "" {
		search = "?" + parsed.RawQuery
	}
	hash := ""
	if parsed.Fragment != "" {
		hash = "#" + parsed.EscapedFragment()
	}
	// the query's own names, each holding the FIRST value the query
	// lists for it — get answers the first (url.bs dom-urlsearchparams-get)
	var paramKeys []abstractdomain.ObjectKey
	for name, values := range query {
		if len(values) == 0 {
			continue
		}
		paramKeys = append(paramKeys, abstractdomain.ObjectKey{Name: name, Value: word(values[0])})
	}
	params := abstractdomain.KnownObject(paramKeys, nil, true, grade, false)
	keys := []abstractdomain.ObjectKey{
		{Name: "href", Value: word(parsed.String())},
		{Name: "protocol", Value: word(parsed.Scheme + ":")},
		{Name: "origin", Value: word(parsed.Scheme + "://" + parsed.Host)},
		{Name: "host", Value: word(parsed.Host)},
		{Name: "hostname", Value: word(parsed.Hostname())},
		{Name: "port", Value: word(parsed.Port())},
		{Name: "pathname", Value: word(pathname)},
		{Name: "search", Value: word(search)},
		{Name: "hash", Value: word(hash)},
		{Name: urlSearchParamsKey, Value: params},
	}
	return abstractdomain.KnownObject(keys, nil, true, grade, false), true
}

func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// ExactUrlNew reads `new URL(<exact string>)` — the one `new URL` shape
// this file answers exactly. Nil for every other shape, which leaves
// web_api_models.go's own generic URL row to answer.
func ExactUrlNew(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsNewExpression(e) {
		return nil
	}
	newExpr := e.AsNewExpression()
	callee := newExpr.Expression
	name := ""
	if ast.IsIdentifier(callee) {
		name = callee.Text()
	} else if ast.IsPropertyAccessExpression(callee) {
		pa := callee.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "globalThis" {
			name = pa.Name().Text()
		}
	}
	if name != "URL" {
		return nil
	}
	t := typereading.TypeAtLocation(ctx.P.Checker, e)
	if t == nil || t.Symbol() == nil || t.Symbol().Name != "URL" {
		return nil
	}
	// the ONE-argument form only: a base argument resolves the first
	// against the second, a composition this reader does not perform
	if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 1 {
		return nil
	}
	argument := evaluateExpression(ctx, env, newExpr.Arguments.Nodes[0])
	text, ok := exactStringOf(argument)
	if !ok {
		return nil
	}
	built, builtOk := exactUrlObject(text, abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(argument)))
	if !builtOk {
		return nil
	}
	return &built
}

// readExactSearchParamsGet reads `<params>.get(name)` where the
// receiver is the COMPLETE parameter object exactUrlObject built: the
// name's own exact value, or null where the query does not carry it
// (url.bs dom-urlsearchparams-get returns null for a name with no
// entry). Nil for every other receiver, which leaves
// web_api_models.go's own URLSearchParams rows to answer the sort.
func readExactSearchParamsGet(site MethodCallSite) *abstractdomain.AbstractValue {
	if site.Method != "get" {
		return nil
	}
	receiver := site.Receiver
	if receiver.Kind != abstractdomain.KindObject || !receiver.Complete {
		return nil
	}
	// the receiver must be an exact-URL parameter object — recognized by
	// its own expression naming the searchParams getter, so an ordinary
	// complete object with a `get` method is never mistaken for one
	if !ast.IsPropertyAccessExpression(site.ReceiverExpression) ||
		site.ReceiverExpression.AsPropertyAccessExpression().Name().Text() != urlSearchParamsKey {
		return nil
	}
	call := site.E.AsCallExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil
	}
	wanted, ok := exactStringOf(evaluateExpression(site.Ctx, site.Env, call.Arguments.Nodes[0]))
	if !ok {
		return nil
	}
	if idx, found := objectKeyIndex(receiver, wanted); found {
		out := receiver.Keys[idx].Value
		return &out
	}
	out := abstractdomain.Null
	return &out
}

// urlEncodedComponentText is encodeURIComponent's own exact output for
// an exactly-known input (sec-encodeuricomponent → Encode with the
// unreserved set): every code point outside the unreserved set becomes
// its UTF-8 bytes spelled as uppercase %XX. Reported (text, false) for
// an input carrying a lone surrogate, which Encode throws on.
func urlEncodedComponentText(text string) (string, bool) {
	if strings.ContainsRune(text, 0xFFFD) {
		return "", false
	}
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var out strings.Builder
	for _, b := range []byte(text) {
		if b < 0x80 && strings.IndexByte(unreserved, b) >= 0 {
			out.WriteByte(b)
			continue
		}
		const hex = "0123456789ABCDEF"
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&0xF])
	}
	return out.String(), true
}
