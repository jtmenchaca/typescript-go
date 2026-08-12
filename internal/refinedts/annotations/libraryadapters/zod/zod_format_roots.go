// Format-root def rows: regex-backed, functional, argful verified
// formats, hash, and custom stringFormat. Spread into ZodDefRoots.
//
// Ported 1:1 from annotations/library_adapters/zod/zod_format_roots.ts.
package zod

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
)

// ZodFormatRoots is ZOD_FORMAT_ROOTS in the TS source.
var ZodFormatRoots = buildZodFormatRoots()

func buildZodFormatRoots() map[string]DefRootReader {
	out := map[string]DefRootReader{}

	// -- $ZodStringFormatDef, regex-backed rows (regexes.ts) --
	for name, pattern := range RegexFormatRoots {
		name, pattern := name, pattern
		out[name] = func(ctx DefReadContext) *compiledshape.Compiled {
			var result compiledshape.Compiled
			if len(ctx.Args) == 0 {
				result = PatternOrUnread(pattern, name)
			} else {
				result = UnreadStringWord(name)
			}
			return &result
		}
	}

	// -- $ZodStringFormatDef, functional rows: the validator runs
	//    code, so the value is a string and the rest is unread --
	for _, name := range FunctionalFormatRoots {
		name := name
		out[name] = func(DefReadContext) *compiledshape.Compiled {
			result := UnreadStringWord(name)
			return &result
		}
	}

	// -- the ten verified format roots (V4FormatPatterns) spelled
	//    WITH arguments -- version pins, error params: the value is a
	//    string, the rest is unread; the argless spellings fall
	//    through to the exact rows --
	for _, name := range []string{
		"guid", "uuid", "email", "cuid", "cuid2", "ulid", "xid", "ksuid",
		"nanoid", "ipv4",
	} {
		name := name
		out[name] = func(ctx DefReadContext) *compiledshape.Compiled {
			if len(ctx.Args) == 0 {
				return nil
			}
			result := UnreadStringWord(name)
			return &result
		}
	}

	// -- the hash family (classic/schemas.ts:1013): alg picks the
	//    digest length, the enc option the alphabet -- exact per pair --
	out["hash"] = func(ctx DefReadContext) *compiledshape.Compiled {
		var alg string
		hasAlg := false
		if len(ctx.Args) >= 1 {
			alg, hasAlg = stringArg(ctx.Args[0])
		}
		byAlg, algKnown := HashPatterns[alg]
		if !hasAlg || !algKnown {
			result := UnreadStringWord("hash")
			return &result
		}
		enc := "hex"
		if len(ctx.Args) >= 2 {
			params := ctx.Args[1]
			if !ast.IsObjectLiteralExpression(params) {
				result := UnreadStringWord(alg + " hash")
				return &result
			}
			for _, property := range params.AsObjectLiteralExpression().Properties.Nodes {
				if !ast.IsPropertyAssignment(property) {
					continue
				}
				assignment := property.AsPropertyAssignment()
				if !ast.IsIdentifier(assignment.Name()) || assignment.Name().AsIdentifier().Text != "enc" {
					continue
				}
				chosen, ok := stringArg(assignment.Initializer)
				if !ok {
					result := UnreadStringWord(alg + " hash")
					return &result
				}
				enc = chosen
			}
		}
		pattern, ok := byAlg[enc]
		text := alg + " hash"
		if enc != "hex" {
			text = alg + " hash, " + enc
		}
		if !ok {
			result := UnreadStringWord(text)
			return &result
		}
		result := PatternOrUnread(pattern, text)
		return &result
	}

	// -- $ZodCustomStringFormatDef (classic/schemas.ts:997): a regex
	//    argument is the format; a predicate function is unread --
	out["stringFormat"] = func(ctx DefReadContext) *compiledshape.Compiled {
		text := "custom format"
		if len(ctx.Args) >= 1 {
			if name, ok := stringArg(ctx.Args[0]); ok {
				text = name
			}
		}
		if len(ctx.Args) >= 2 {
			spec := ctx.Args[1]
			if ast.IsRegularExpressionLiteral(spec) {
				raw := spec.AsRegularExpressionLiteral().Text
				lastSlash := strings.LastIndex(raw, "/")
				pattern := raw[1:lastSlash]
				result := PatternOrUnread(pattern, text)
				return &result
			}
		}
		result := UnreadStringWord(text)
		return &result
	}

	return out
}
