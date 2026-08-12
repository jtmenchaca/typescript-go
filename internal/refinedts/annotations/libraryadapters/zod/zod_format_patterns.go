// Ported 1:1 from annotations/library_adapters/zod/zod_format_patterns.ts.
package zod

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// PatternOrUnread is patternOrUnread in the TS source: a pattern
// row -- zod's exact regex where the format grammar speaks it, the
// unread string claim where it does not -- never silence. Either way
// the hover speaks the format's word, never the grammar's algebra.
func PatternOrUnread(pattern string, text string) compiledshape.Compiled {
	compiled := refinementsets.FormatGrammar(pattern, "")
	if !compiled.Ok {
		return UnreadStringWord(text)
	}
	return compiledshape.Compiled{
		Annotation: &compiledshape.AnnotationValue{
			Set:  compiled.Set,
			Word: &compiledshape.WordSpelling{Text: text, Covers: len(compiled.Set.Forms)},
		},
	}
}

// UUIDVersioned is UUID_VERSIONED in the TS source.
func UUIDVersioned(version int) string {
	return fmt.Sprintf(
		"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-%d[0-9a-fA-F]{3}-"+
			"[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12})$",
		version,
	)
}

// V4FormatPatterns is V4_FORMAT_PATTERNS in the TS source: the ten
// verified v4 root formats, verbatim from the vendored library (
// tmp/zod-src packages/zod/src/v4/core/regexes.ts) -- the model is
// zod's exact regex or nothing. The ROOT rows read them in
// annotations.ts; the CHAIN rows below read the same table.
var V4FormatPatterns = map[string]string{
	"guid": "^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-" +
		"[0-9a-fA-F]{12})$",
	"uuid": "^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-" +
		"[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|" +
		"00000000-0000-0000-0000-000000000000|" +
		"ffffffff-ffff-ffff-ffff-ffffffffffff)$",
	"cuid":   "^[cC][0-9a-z]{6,}$",
	"cuid2":  "^[0-9a-z]+$",
	"ulid":   "^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{26}$",
	"xid":    "^[0-9a-vA-V]{20}$",
	"ksuid":  "^[A-Za-z0-9]{27}$",
	"nanoid": "^[a-zA-Z0-9_-]{21}$",
	"ipv4": "^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\\.){3}" +
		"(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$",
	"email": "^(?!\\.)(?!.*\\.\\.)([A-Za-z0-9_'+\\-\\.]*)[A-Za-z0-9_+-]@" +
		"([A-Za-z0-9][A-Za-z0-9\\-]*\\.)+[A-Za-z]{2,}$",
}

// RegexFormatRoots is REGEX_FORMAT_ROOTS in the TS source: format
// roots whose v4 validator is the regex itself (regexes.ts lines
// cited per row). The ten already-verified rows stay in
// V4FormatPatterns; these are the REST of the regex-backed family.
var RegexFormatRoots = map[string]string{
	"uuidv4":    UUIDVersioned(4),      // regexes.ts:36
	"uuidv6":    UUIDVersioned(6),      // regexes.ts:37
	"uuidv7":    UUIDVersioned(7),      // regexes.ts:38
	"base64url": "^[A-Za-z0-9_-]*$",    // regexes.ts:80
	"e164":      "^\\+[1-9]\\d{6,14}$", // regexes.ts:93
	"hex":       "^[0-9a-fA-F]*$",      // regexes.ts:154
	"cidrv4": "^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\\.){3}" +
		"(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])" +
		"\\/([0-9]|[1-2][0-9]|3[0-2])$", // regexes.ts:74
	// base64 and mac formatAt top-level anchored alternations, and
	// hostname leads with a length lookahead -- each compiles if the
	// format grammar speaks it and rides the unread string claim if
	// not, decided at build, never silent
	"base64": "^$|^(?:[0-9a-zA-Z+/]{4})*(?:(?:[0-9a-zA-Z+/]{2}==)|" +
		"(?:[0-9a-zA-Z+/]{3}=))?$", // regexes.ts:79
	"mac": "^(?:[0-9A-F]{2}:){5}[0-9A-F]{2}$|" +
		"^(?:[0-9a-f]{2}:){5}[0-9a-f]{2}$", // regexes.ts:69-72, ":" default
	"hostname": "^(?=.{1,253}\\.?$)[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}" +
		"[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[-0-9a-zA-Z]{0,61}" +
		"[0-9a-zA-Z])?)*\\.?$", // regexes.ts:84
	"emoji": "^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$", // :60
	"duration": "^P(?:(\\d+W)|(?!.*W)(?=\\d|T\\d)(\\d+Y)?(\\d+M)?(\\d+D)?" +
		"(T(?=\\d)(\\d+H)?(\\d+M)?(\\d+([.,]\\d+)?S)?)?)$", // regexes.ts:17
}

// FunctionalFormatRoots is FUNCTIONAL_FORMAT_ROOTS in the TS source:
// format roots whose v4 validator RUNS CODE -- new URL() for url/
// httpUrl (schemas.ts:504) and for ipv6/cidrv6 (schemas.ts:812, 898),
// segment decoding for jwt -- so no regex states them.
var FunctionalFormatRoots = []string{
	"url",     // schemas.ts:504 -- new URL(trimmed)
	"httpUrl", // classic/schemas.ts:722 -- url + protocol/hostname pins
	"ipv6",    // schemas.ts:812 -- new URL(`http://[${value}]`)
	"cidrv6",  // schemas.ts:898 -- the same probe on the address half
	"jwt",     // header segment base64-decoded and JSON-parsed
}

// HashPatterns is HASH_PATTERNS in the TS source: the hash family
// (regexes.ts:168-190): alg × encoding, exact.
var HashPatterns = map[string]map[string]string{
	"md5": {
		"hex":       "^[0-9a-fA-F]{32}$",
		"base64":    "^[A-Za-z0-9+/]{22}==$",
		"base64url": "^[A-Za-z0-9_-]{22}$",
	},
	"sha1": {
		"hex":       "^[0-9a-fA-F]{40}$",
		"base64":    "^[A-Za-z0-9+/]{27}=$",
		"base64url": "^[A-Za-z0-9_-]{27}$",
	},
	"sha256": {
		"hex":       "^[0-9a-fA-F]{64}$",
		"base64":    "^[A-Za-z0-9+/]{43}=$",
		"base64url": "^[A-Za-z0-9_-]{43}$",
	},
	"sha384": {
		"hex":       "^[0-9a-fA-F]{96}$",
		"base64":    "^[A-Za-z0-9+/]{64}$",
		"base64url": "^[A-Za-z0-9_-]{64}$",
	},
	"sha512": {
		"hex":       "^[0-9a-fA-F]{128}$",
		"base64":    "^[A-Za-z0-9+/]{86}==$",
		"base64url": "^[A-Za-z0-9_-]{86}$",
	},
}

// FormatMethodPatterns is FORMAT_METHOD_PATTERNS in the TS source:
// the deprecated-but-live string format METHODS (classic ZodString
// keeps `.email()` and family): the same patterns as the roots,
// intersected onto the chain.
var FormatMethodPatterns = map[string]string{
	"base64":    RegexFormatRoots["base64"],
	"base64url": RegexFormatRoots["base64url"],
	"e164":      RegexFormatRoots["e164"],
	"duration":  RegexFormatRoots["duration"],
	"emoji":     RegexFormatRoots["emoji"],
	"cidrv4":    RegexFormatRoots["cidrv4"],
}
