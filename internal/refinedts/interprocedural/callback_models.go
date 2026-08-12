// The synchronous callbacks: map, filter, reduce, forEach, flatMap,
// find with an inline arrow — TYPE ONLY so far: Callback is the hub
// type FlowContext's callableParams carries. The model functions
// (callbackOf, the fold runner) land with the interprocedural
// directory's own port; this file is completed then, 1:1 against
// callback_models.ts.

package interprocedural

import "github.com/microsoft/typescript-go/internal/ast"

// Callback is a function value handed as a callback: an inline
// arrow, or a NAME resolving to a const-bound arrow / function
// expression / function declaration — `xs.map(double)` reads the
// same as the inline form. (TS: ArrowFunction | FunctionExpression |
// FunctionDeclaration; the Go node is any of those three kinds.)
type Callback = *ast.Node
