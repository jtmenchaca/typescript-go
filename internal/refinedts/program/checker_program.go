// The program a check runs against. Ported from the CheckerProgram
// interface in service/program_host.ts, with the port's one
// structural change applied: the TS `host` field carried the eight
// CheckerHost questions so an out-of-process oracle could stand in —
// in Go the checker IS in-process, so the field is the
// *checker.Checker itself and the whole oracle/adapter layer
// (service/tsgo/) has no twin.
//
// The program CONSTRUCTION half of program_host.ts (virtual files,
// the surface module served from fixed paths, the disk program
// cache) ports with service/ later; this package is the type the
// walk threads through, so every earlier directory can compile.

package program

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
)

type CheckerProgram struct {
	Program *compiler.Program
	// Checker answers the questions the TS CheckerHost carried —
	// getTypeAtLocation and its seven siblings — as direct method
	// calls.
	Checker *checker.Checker
	Entry   *ast.SourceFile
	// SurfacePaths: a name is "the surface" exactly when its
	// declaration lives in one of these paths.
	SurfacePaths map[string]bool
	// Done releases the checker back to the Program's checker pool.
	// compiler.Program.GetTypeChecker returns the checker together
	// with this release function, and the checker is only valid to
	// question while the lease is held — a caller building a
	// CheckerProgram must hold it open for the whole time the walk
	// questions the checker (typereading/read_type_test.go's
	// t.Cleanup(done) is the same convention) and call Done when the
	// program is discarded. nil for a CheckerProgram built directly
	// against a checker whose lease belongs to someone else.
	Done func()
}
