// A shared constructor for the Unsupported half of Compiled — every
// chain reader builds one of these at a refusal, and the TS source's
// object-literal `{ unsupported: "...", at: e }` spelling recurs
// enough (chain_method.ts, chain_root_constructor.ts,
// chain_numeric_method.ts, object_key_compiler.ts,
// object_schema_compiler.ts, type_node_*.ts) that one formatted
// constructor replaces the repeated struct literal — not a TS
// function (there is none to mirror 1:1) but a Go-only ergonomics
// helper, since Compiled is a struct here rather than a union.
package annotations

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
)

// unsupportedf builds the Unsupported half of a Compiled, with a
// formatted message.
func unsupportedf(at *ast.Node, format string, args ...any) *Compiled {
	return &Compiled{Unsupported: &Unsupported{Unsupported: fmt.Sprintf(format, args...), At: at}}
}
