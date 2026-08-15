// Node's hashing chain: crypto.createHash / createHmac, the update
// links that hand the same object back, and the digest that ends the
// chain in a string.
//
// Every row here reads @types/node's own crypto declarations —
// `function createHash(algorithm: string, options?): Hash`,
// `update(data, inputEncoding?): Hash`, `digest(): NonSharedBuffer`
// and `digest(encoding: BufferEncoding): string` (crypto.d.ts:126,
// 285, 296-297; Hmac's twin at 377, 388-389). Those are a package's
// declarations, not ECMA-262, so every answer wears the LIBRARY grade
// (TRUST.md): the claim is only as good as @types/node's transcription
// of Node's own behaviour.
//
// Recognition is by the receiver's STATIC type name, the same standing
// the web-platform rows and the Date fallback rows rest on: Hash and
// Hmac are package-provided classes with private constructors, so a
// value of that type came from createHash/createHmac and nothing else.
// That is what lets a chain thread: `createHash(a).update(v)` has
// static type Hash at each link whether or not the walk holds a value
// for the link before it.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// nodeDigestClasses: the two hashing classes whose update/digest pair
// this file models. Both spell the same chain — update hands the
// receiver back, digest ends it.
var nodeDigestClasses = map[string]bool{"Hash": true, "Hmac": true}

// declaredInNodeTypes: whether a symbol's declaration lives in the
// @types/node package. Node's crypto is not in the checker's default
// library, so resolvesToDefaultLib says nothing about it; the
// declaration file's own path is the provenance, the same way the
// library adapters recognize a dialect by the file that declares it.
func declaredInNodeTypes(symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	for _, declaration := range symbol.Declarations {
		source := ast.GetSourceFileOfNode(declaration)
		if source == nil {
			continue
		}
		if strings.Contains(source.FileName(), "@types/node/") {
			return true
		}
	}
	return false
}

// nodeDigestClassOf: the receiver's hashing-class name, or "" (with
// ok=false) when its static type is neither Hash nor Hmac, or when the
// class it names was not declared by @types/node — a user's own class
// called Hash answers nothing here.
func nodeDigestClassOf(ctx *FlowContext, receiver *ast.Node) (string, bool) {
	t := ctx.P.Checker.GetTypeAtLocation(receiver)
	if t == nil {
		return "", false
	}
	// an intersection is still the class — a branded member changes no
	// method (the web rows read intersections the same way)
	var members []*checker.Type
	if t.IsIntersection() {
		members = t.Types()
	} else {
		members = []*checker.Type{t}
	}
	for _, member := range members {
		symbol := member.Symbol()
		if symbol != nil && nodeDigestClasses[symbol.Name] && declaredInNodeTypes(symbol) {
			return symbol.Name, true
		}
	}
	return "", false
}

// nodeDigestLengths: how many hex characters each algorithm's digest
// spells — the digest byte length doubled, since hex writes two
// characters per byte. The byte lengths are each algorithm's own
// published output size (SHA-2 in FIPS 180-4, SHA-3/SHAKE in FIPS 202,
// MD5 in RFC 1321, SHA-1 in RFC 3174); Node hands the algorithm
// straight to OpenSSL, so the size is the algorithm's, not Node's.
// An algorithm not named here (or a variable-output XOF like shake256,
// whose length the outputLength option sets) keeps the unbounded
// answer below.
var nodeDigestLengths = map[string]int{
	"md5":        32,
	"sha1":       40,
	"sha224":     56,
	"sha256":     64,
	"sha384":     96,
	"sha512":     128,
	"sha3-224":   56,
	"sha3-256":   64,
	"sha3-384":   96,
	"sha3-512":   128,
	"ripemd160":  40,
	"sha512-224": 56,
	"sha512-256": 64,
}

// hexDigits: the sixteen characters a lowercase hex digest is written
// with — 0-9 and a-f, as codepoints.
func hexDigits() refinementsets.RefinedSet {
	var points []float64
	for c := '0'; c <= '9'; c++ {
		points = append(points, float64(c))
	}
	for c := 'a'; c <= 'f'; c++ {
		points = append(points, float64(c))
	}
	return refinementsets.MakeRefinedSet(refinementsets.OneOf(points))
}

// base64Digits: the sixty-five characters standard base64 is written
// with — A-Z, a-z, 0-9, + and / for the alphabet, = for the padding
// (RFC 4648 §4).
func base64Digits() refinementsets.RefinedSet {
	var points []float64
	for c := 'A'; c <= 'Z'; c++ {
		points = append(points, float64(c))
	}
	for c := 'a'; c <= 'z'; c++ {
		points = append(points, float64(c))
	}
	for c := '0'; c <= '9'; c++ {
		points = append(points, float64(c))
	}
	for _, c := range []rune{'+', '/', '='} {
		points = append(points, float64(c))
	}
	return refinementsets.MakeRefinedSet(refinementsets.OneOf(points))
}

// digestWords: the strings one encoding's digest can be. A hex digest
// of a NAMED algorithm is exactly that many hex characters; a hex
// digest of an unnamed one is some run of them; base64 is a run of the
// base64 alphabet. Every other BufferEncoding writes bytes the
// encoding may not round-trip (latin1, binary) or codepoints outside
// any alphabet this file spells (utf8 over raw digest bytes), so those
// answer nothing and the caller declines.
func digestWords(encoding string, hexLength int, hasHexLength bool) (abstractdomain.AbstractValue, bool) {
	switch encoding {
	case "hex":
		if hasHexLength {
			length := hexLength
			set := refinementsets.MakeRefinedSet(refinementsets.RepeatOf(hexDigits(), length, &length))
			return abstractdomain.KnownSet(set, nil, abstractdomain.TrustLibrary, abstractdomain.SetKindTagNone), true
		}
		set := refinementsets.MakeRefinedSet(refinementsets.Star(hexDigits()))
		return abstractdomain.KnownSet(set, nil, abstractdomain.TrustLibrary, abstractdomain.SetKindTagNone), true
	case "base64", "base64url":
		set := refinementsets.MakeRefinedSet(refinementsets.Star(base64Digits()))
		return abstractdomain.KnownSet(set, nil, abstractdomain.TrustLibrary, abstractdomain.SetKindTagNone), true
	}
	return abstractdomain.AbstractValue{}, false
}

// digestAlgorithmOf: the algorithm name the chain was created with,
// walked back through the update links to the createHash call that
// started it. `createHash('sha256').update(v).digest('hex')` reaches
// the literal in one step from update's own receiver; a chain broken
// by a name the walk does not follow answers "" and the digest keeps
// its unbounded run of hex characters.
func digestAlgorithmOf(ctx *FlowContext, receiver *ast.Node) (string, bool) {
	node := receiver
	// each update link hands the same object back, so the algorithm is
	// whatever the receiver BEFORE the update was created with
	for range [8]struct{}{} {
		if !ast.IsCallExpression(node) {
			return "", false
		}
		call := node.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			// `createHash('sha256')` — a bare callee: the first argument
			// names the algorithm
			if !ast.IsIdentifier(call.Expression) {
				return "", false
			}
			if !nodeDigestFactories[call.Expression.Text()] {
				return "", false
			}
			if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
				return "", false
			}
			first := call.Arguments.Nodes[0]
			if !ast.IsStringLiteral(first) {
				return "", false
			}
			return strings.ToLower(first.Text()), true
		}
		pa := call.Expression.AsPropertyAccessExpression()
		name := pa.Name().Text()
		if name == "update" {
			node = pa.Expression
			continue
		}
		// `crypto.createHash('sha256')` — the factory reached through
		// its module namespace
		if nodeDigestFactories[name] {
			if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
				return "", false
			}
			first := call.Arguments.Nodes[0]
			if !ast.IsStringLiteral(first) {
				return "", false
			}
			return strings.ToLower(first.Text()), true
		}
		return "", false
	}
	return "", false
}

// nodeDigestFactories: the calls that start a hashing chain.
var nodeDigestFactories = map[string]bool{"createHash": true, "createHmac": true}

// readNodeDigestMethods: the hashing chain's two links. `update(...)`
// hands its own receiver back (crypto.d.ts:285, 377 — the return type
// IS Hash / Hmac), so the value the walk holds for the receiver rides
// through unchanged and a longer chain keeps whatever the first link
// determined. `digest(encoding)` ends the chain in a string over the
// encoding's own alphabet — for hex, of the algorithm's own length.
// A bare `digest()` returns a Buffer, which no row here reads, so it
// declines with the sentence naming the link. Nil when the receiver is
// neither Hash nor Hmac.
func readNodeDigestMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, e, receiverExpression, receiver, method := site.Ctx, site.E, site.ReceiverExpression, site.Receiver, site.Method
	if method != "update" && method != "digest" {
		return nil
	}
	className, ok := nodeDigestClassOf(ctx, receiverExpression)
	if !ok {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if method == "update" {
		// the data argument still runs — its own reads and effects
		// happen whether or not the hash carries them
		for _, argument := range arguments {
			evaluateExpression(ctx, site.Env, argument)
		}
		// update returns the receiver itself: the chain's next link sees
		// exactly what this one did
		out := receiver
		return &out
	}
	for _, argument := range arguments {
		evaluateExpression(ctx, site.Env, argument)
	}
	if len(arguments) == 0 {
		// digest with no encoding returns a Buffer — bytes, not a
		// string, and nothing here reads a Buffer's contents
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        strings.ToLower(className) + ".digest() with no encoding returns a Buffer, whose bytes this file does not read",
				Unsupported: true,
			})
		}
		return nil
	}
	encodingKnown := evaluateExpression(ctx, site.Env, arguments[0])
	encoding := ""
	if encodingKnown.Kind == abstractdomain.KindValues && encodingKnown.KindTag == abstractdomain.PrimitiveString {
		encoding = strings.ToLower(stringOf(encodingKnown.Values))
	}
	if encoding == "" {
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        strings.ToLower(className) + ".digest() writes the alphabet its encoding argument names, and this argument's word is not determined",
				Unsupported: true,
			})
		}
		return nil
	}
	hexLength := 0
	hasHexLength := false
	if algorithm, named := digestAlgorithmOf(ctx, receiverExpression); named {
		if length, sized := nodeDigestLengths[algorithm]; sized {
			hexLength, hasHexLength = length, true
		}
	}
	words, spelled := digestWords(encoding, hexLength, hasHexLength)
	if !spelled {
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        strings.ToLower(className) + ".digest(\"" + encoding + "\") writes an encoding whose alphabet this file does not spell",
				Unsupported: true,
			})
		}
		return nil
	}
	return &words
}

// ReadNodeDigestConstruction: `createHash(alg)` / `createHmac(alg,
// key)` — the value that starts a chain. The object itself carries no
// readable field (its state is the digest in progress, which no
// property exposes), so it answers the opaque tier: the type is
// everything this file determines about it, and the update/digest rows
// above thread the chain by the STATIC type rather than through this
// value. Nil when the call is not one of the two factories.
func ReadNodeDigestConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	var callee *ast.Node
	if ast.IsIdentifier(call.Expression) {
		callee = call.Expression
	} else if ast.IsPropertyAccessExpression(call.Expression) {
		callee = call.Expression.AsPropertyAccessExpression().Name()
	} else {
		return nil
	}
	if !nodeDigestFactories[callee.Text()] {
		return nil
	}
	if !declaredInNodeTypes(ctx.P.Checker.GetSymbolAtLocation(call.Expression)) {
		return nil
	}
	// the arguments still run
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			evaluateExpression(ctx, env, argument)
		}
	}
	out := abstractdomain.Opaque
	return &out
}
