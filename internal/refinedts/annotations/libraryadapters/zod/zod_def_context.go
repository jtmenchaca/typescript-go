// Ported 1:1 from annotations/library_adapters/zod/zod_def_context.ts.
// CheckerProgram and the Compiled/Annotation shapes read through the
// libraryadapters package's own mirror (see compiled.go one directory
// up) rather than annotations directly -- a true import cycle in the
// TS source (isUnsupported and Annotation values cross both ways),
// broken the way objectgraphs/kernelbridge break theirs.
package zod

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// DefReadContext is DefReadContext in the TS source: what a def
// reader sees -- the call site, its arguments, and the compiler for
// inner schemas.
type DefReadContext struct {
	P        *program.CheckerProgram
	At       *ast.Node
	Args     []*ast.Node
	Registry compiledshape.AnnotationRegistry
	Compile  func(e *ast.Node) compiledshape.Compiled
}

// DefRootReader is DefRootReader in the TS source.
type DefRootReader func(ctx DefReadContext) *compiledshape.Compiled

// DefChainReader is DefChainReader in the TS source: DefReadContext
// plus the inner annotation the chain method extends.
type DefChainReaderContext struct {
	DefReadContext
	Inner compiledshape.AnnotationValue
}

type DefChainReader func(ctx DefChainReaderContext) *compiledshape.Compiled

// Bottom is bottom in the TS source: the empty set -- a ray past +∞
// admits nothing. z.never denotes it, and the absence-only schemas
// ride it with the absent flag.
var Bottom = refinementsets.MakeRefinedSet(refinementsets.Above(math.Inf(1)))

// UnreadUnknown is unreadUnknown in the TS source: the widest claim
// there is, marked unread -- any value may come out, and the parse
// checked things the sets cannot state.
var UnreadUnknown = compiledshape.Compiled{
	Annotation: &compiledshape.AnnotationValue{Set: refinementsets.MakeRefinedSet(), Unread: true},
}

// UnreadWord is unreadWord in the TS source: the unread unknown
// carrying the surface's word, so the hover can say WHICH statement
// rides unread.
func UnreadWord(text string) compiledshape.Compiled {
	set := refinementsets.MakeRefinedSet()
	return compiledshape.Compiled{
		Annotation: &compiledshape.AnnotationValue{
			Set:    set,
			Unread: true,
			Word:   &compiledshape.WordSpelling{Text: text, Covers: 0},
		},
	}
}

// UnreadString is unreadString in the TS source: a string family
// whose exact grammar the model cannot compile -- the value IS a
// string, and the rest is unread.
var UnreadString = compiledshape.Compiled{
	Annotation: &compiledshape.AnnotationValue{Set: refinementsets.Strings, Unread: true},
}

// UnreadStringWord is unreadStringWord in the TS source.
func UnreadStringWord(text string) compiledshape.Compiled {
	return compiledshape.Compiled{
		Annotation: &compiledshape.AnnotationValue{
			Set:    refinementsets.Strings,
			Unread: true,
			Word:   &compiledshape.WordSpelling{Text: text, Covers: len(refinementsets.Strings.Forms)},
		},
	}
}
