// from evaluation/coercion_models.ts
//
// The URI handling functions (sec-uri-handling-functions):
// decodeURI/decodeURIComponent/encodeURI/encodeURIComponent, mirroring
// the spec's percent-encoding grammar closely enough for this file's
// exact reads. Split from coercion_models.go per file-length
// discipline.

package walk

import (
	"errors"
	"net/url"
	"strings"
)

// errURIMalformed is the URIError sec-encode step 4 raises for a lone
// surrogate — the caller reads any error here as "this call throws, so
// it carries no value", exactly as it already did for a bad decode.
var errURIMalformed = errors.New("URI malformed")

// jsURIFunction runs decodeURI/decodeURIComponent/encodeURI/
// encodeURIComponent, mirroring the spec's percent-encoding grammar
// closely enough for this file's exact reads. Go's net/url quotes
// differently (space becomes "+" in QueryEscape, not "%20"), so
// PathEscape/PathUnescape stand in, with the reserved-character sets
// adjusted to each function's own list
// (sec-uri-handling-functions).
func jsURIFunction(name string, text string) (string, error) {
	switch name {
	case "decodeURIComponent", "decodeURI":
		return url.QueryUnescape(strings.ReplaceAll(text, "+", "%2B"))
	case "encodeURIComponent":
		// NOT url.QueryEscape: that spells a space "+", while
		// sec-encodeuricomponent's Encode spells every code point outside
		// the unreserved set as its UTF-8 bytes in %XX form, so a space is
		// "%20". urlEncodedComponentText (url_models.go) walks the
		// unreserved set the spec itself lists.
		encoded, ok := urlEncodedComponentText(text)
		if !ok {
			return "", errURIMalformed
		}
		return encoded, nil
	case "encodeURI":
		// encodeURI leaves reserved/unescaped characters alone
		// (;/?:@&=+$,#), unlike encodeURIComponent
		var b strings.Builder
		for _, r := range text {
			if strings.ContainsRune(";/?:@&=+$,#-_.!~*'()", r) ||
				(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
				continue
			}
			b.WriteString(url.QueryEscape(string(r)))
		}
		return b.String(), nil
	}
	return "", nil
}
