// The RUNTIME vocabulary — calls that operate on values rather than
// build schemas. These stay in the value world (parse-eval and flow
// read them); everything else zod spells compiles, exact or unread,
// never silent.
//
// Ported 1:1 from annotations/library_adapters/zod/zod_runtime_vocabulary.ts.
package zod

// ZodRuntimeRoots is ZOD_RUNTIME_ROOTS in the TS source.
var ZodRuntimeRoots = map[string]bool{
	"parse":           true,
	"parseAsync":      true,
	"safeParse":       true,
	"safeParseAsync":  true,
	"decode":          true,
	"decodeAsync":     true,
	"safeDecode":      true,
	"safeDecodeAsync": true,
	"encode":          true,
	"encodeAsync":     true,
	"safeEncode":      true,
	"safeEncodeAsync": true,
	"registry":        true,
	"globalRegistry":  true,
	"config":          true,
	"toJSONSchema":    true,
	"treeifyError":    true,
	"prettifyError":   true,
	"flattenError":    true,
	"formatError":     true,
	"setErrorMap":     true,
	"getErrorMap":     true,
}

// ZodRuntimeMethods is ZOD_RUNTIME_METHODS in the TS source.
var ZodRuntimeMethods = map[string]bool{
	"parse":           true,
	"parseAsync":      true,
	"safeParse":       true,
	"safeParseAsync":  true,
	"spa":             true,
	"decode":          true,
	"decodeAsync":     true,
	"safeDecode":      true,
	"safeDecodeAsync": true,
	"encode":          true,
	"encodeAsync":     true,
	"safeEncode":      true,
	"safeEncodeAsync": true,
	"register":        true,
	"clone":           true,
	"implement":       true,
	"implementAsync":  true,
	"isOptional":      true,
	"isNullable":      true,
	// .transform is OWNED BY THE VALUE WORLD: the checker computes
	// the image of the callback over the input set (parse-eval and
	// the coverage reader), which is richer than any static row -- an
	// annotation here would shadow that exact answer with unknown
	"transform": true,
}
