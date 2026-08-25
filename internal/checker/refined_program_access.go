// The refined reader's access to the checker's program: syntactic
// name resolution (refinedts/nameresolution) follows an import
// specifier to its resolved source file through the module-resolution
// tables the program already holds, without asking the checker's own
// resolver.
package checker

// BoundProgram is the program this checker was constructed over.
func (c *Checker) BoundProgram() Program { return c.program }
