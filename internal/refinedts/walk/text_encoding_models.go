// TextEncoder / TextDecoder (WHATWG Encoding, §Interface TextEncoder
// and §Interface TextDecoder).
//
// Both are pure functions of their input, so an exactly-known input
// gives an exactly-known output and the host running the check computes
// it — the same standing the exact string-method reads rest on:
//
//   - `new TextEncoder().encode(s)` runs the UTF-8 encoder over s and
//     answers a Uint8Array of the resulting bytes (encoding.bs
//     dom-textencoder-encode). Go strings are already UTF-8, so the
//     bytes ARE the Go bytes of the same text.
//   - `new TextDecoder().decode(bytes)` runs the UTF-8 decoder with
//     error mode "replacement" (the default: dom-textdecoder-decode
//     with fatal false), so every byte sequence that is not a valid
//     UTF-8 scalar encoding contributes exactly one U+FFFD REPLACEMENT
//     CHARACTER — never a throw.
//
// A TextEncoder/TextDecoder carries no state a call can observe at the
// default settings this file admits (no `fatal`, no `ignoreBOM`, no
// non-UTF-8 label), so the receiver is recognized by its own
// construction expression rather than tracked as a value. Any other
// shape answers nil and the unmodeled tail speaks instead.

package walk

import (
	"unicode/utf8"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// textCodecReceiver reports whether an expression constructs a
// DEFAULT-SETTINGS TextEncoder or TextDecoder of the named kind: a bare
// `new TextEncoder()` / `new TextDecoder()` with no arguments. A label
// or an options bag changes what decode does (a non-UTF-8 encoding, or
// fatal mode, which throws instead of substituting), so those are not
// this file's rows.
func textCodecReceiver(ctx *FlowContext, expression *ast.Node, want string) bool {
	if expression == nil || !ast.IsNewExpression(expression) {
		return false
	}
	newExpr := expression.AsNewExpression()
	if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) != 0 {
		return false
	}
	callee := newExpr.Expression
	if !ast.IsIdentifier(callee) || callee.Text() != want {
		return false
	}
	if !resolvesToDefaultLib(ctx, callee) {
		return false
	}
	t := typereading.TypeAtLocation(ctx.P.Checker, expression)
	return t != nil && t.Symbol() != nil && t.Symbol().Name == want
}

// readTextEncoderEncode reads `new TextEncoder().encode(s)`.
//
// On an EXACT string the bytes are exact — a Uint8Array spelled the
// same way typed_array_models.go spells one, so `.length` and every
// element read answer off it with no further row. On a string the walk
// has not pinned, the RESULT is still a byte array whose length is a
// non-negative integer: encode never throws (encoding.bs's UTF-8
// encoder has no error path for a well-formed JS string, and a lone
// surrogate is encoded as U+FFFD rather than raising), so the sort-level
// claim holds on every run. That claim rides on the object the generic
// row builds, whose `length` key states the window.
func readTextEncoderEncode(site MethodCallSite) *abstractdomain.AbstractValue {
	if site.Method != "encode" || !textCodecReceiver(site.Ctx, site.ReceiverExpression, "TextEncoder") {
		return nil
	}
	call := site.E.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if len(arguments) != 1 || ast.IsSpreadElement(arguments[0]) {
		return nil
	}
	argument := evaluateExpression(site.Ctx, site.Env, arguments[0])
	if text, ok := exactStringOf(argument); ok {
		bytes := []byte(text)
		values := make([]float64, len(bytes))
		for i, b := range bytes {
			values[i] = float64(b)
		}
		out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray,
			abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(argument)))
		return &out
	}
	// the byte count of an unpinned string: a non-negative integer, and
	// nothing narrower — one code point encodes to between one and four
	// bytes, so no bound rides on the string's own length window either
	length := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	out := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "length", Value: length}},
		nil, false, abstractdomain.TrustSpec, false,
	)
	return &out
}

// readTextDecoderDecode reads `new TextDecoder().decode(bytes)`.
//
// On an EXACT byte array the text is exact: the UTF-8 decoder with
// error mode "replacement" maps each maximal ill-formed subsequence to
// one U+FFFD (encoding.bs §UTF-8 decoder, via the decoder error
// handling that emits a replacement code point), which is what Go's
// own rune decoding reports as RuneError over one byte. On anything
// less exact the result is still a string, which is the sort-level
// claim decode always keeps — it never throws at the non-fatal default.
func readTextDecoderDecode(site MethodCallSite) *abstractdomain.AbstractValue {
	if site.Method != "decode" || !textCodecReceiver(site.Ctx, site.ReceiverExpression, "TextDecoder") {
		return nil
	}
	call := site.E.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	grade := abstractdomain.TrustSpec
	// decode() with no argument is the empty string (the input defaults
	// to an empty buffer)
	if len(arguments) == 0 {
		out := abstractdomain.KnownValues(refinementsets.CodepointsOf(""), abstractdomain.PrimitiveString, grade)
		return &out
	}
	if len(arguments) != 1 || ast.IsSpreadElement(arguments[0]) {
		return nil
	}
	argument := evaluateExpression(site.Ctx, site.Env, arguments[0])
	if argument.Kind == abstractdomain.KindValues && argument.KindTag == abstractdomain.PrimitiveArray {
		bytes := make([]byte, 0, len(argument.Values))
		for _, value := range argument.Values {
			if value < 0 || value > 255 || value != float64(int(value)) {
				return nil
			}
			bytes = append(bytes, byte(int(value)))
		}
		out := abstractdomain.KnownValues(
			refinementsets.CodepointsOf(decodeUTF8Replacing(bytes)),
			abstractdomain.PrimitiveString,
			abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(argument)),
		)
		return &out
	}
	out := abstractdomain.KnownSet(refinementsets.Strings, nil, grade, abstractdomain.SetKindTagNone)
	return &out
}

// decodeUTF8Replacing is the UTF-8 decoder at error mode
// "replacement": each maximal ill-formed byte subsequence contributes
// exactly one U+FFFD. Go's DecodeRune reports RuneError with size 1 for
// an ill-formed lead byte and for each stray continuation byte, which
// is the same per-subsequence substitution the standard's decoder
// makes for the byte sequences this reader admits.
func decodeUTF8Replacing(bytes []byte) string {
	out := make([]rune, 0, len(bytes))
	for len(bytes) > 0 {
		r, size := utf8.DecodeRune(bytes)
		out = append(out, r)
		bytes = bytes[size:]
	}
	return string(out)
}
