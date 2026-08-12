// Zod recognition: a declaration file inside the zod package —
// hoisted (node_modules/zod/…) or pnpm's nested store layout
// (node_modules/.pnpm/zod@…/node_modules/zod/…). Recognition is by
// PACKAGE PATH, never by a name spelled `z`: the root identifier's
// declarations must resolve into these files.
//
// Ported 1:1 from annotations/library_adapters/zod/package_recognition.ts.
package zod

import "regexp"

var zodPath = regexp.MustCompile(`/node_modules/(?:\.pnpm/[^/]+/node_modules/)?zod/`)

// DeclaresZod is declaresZod in the TS source.
func DeclaresZod(fileName string) bool {
	return zodPath.MatchString(fileName)
}
