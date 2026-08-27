// from service/hover_annotation.ts
//
// Annotation helpers that only the hover answer path uses: unread
// chain detection, schema-name prefixes, worded annotation text, and
// the stated answer a type node gives.

package service

import (
	"regexp"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// valueMethods are the chain methods that produce a VALUE, not a
// schema — a binding holding one of these is a parsed result, which
// the flow layer may answer, never an unread annotation.
var valueMethods = map[string]bool{
	"parse":     true,
	"safeParse": true,
	"decode":    true,
	"encode":    true,
}

// UnreadAnnotationChain is unreadAnnotationChain in the TS source:
// whether an initializer is a schema chain rooted in an annotation
// module — `z.something(...).more(...)`. Reaching this test at all
// means neither registry compiled it.
func UnreadAnnotationChain(p *program.CheckerProgram, e *ast.Node) bool {
	node := e
	for {
		if ast.IsCallExpression(node) {
			node = node.AsCallExpression().Expression
			continue
		}
		if ast.IsPropertyAccessExpression(node) {
			if valueMethods[node.AsPropertyAccessExpression().Name().Text()] {
				return false
			}
			node = node.AsPropertyAccessExpression().Expression
			continue
		}
		break
	}
	if !ast.IsIdentifier(node) {
		return false
	}
	return annotations.ResolvesToAnnotationRoot(p, node)
}

var schemaTypeNamePattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// SchemaTypeName is schemaTypeName in the TS source: the binding's
// own TYPE NAME — "ZodUUID", "ZodEnum" — the cue that the value IS a
// schema and the braces are what it STATES.
func SchemaTypeName(p *program.CheckerProgram, name *ast.Node) string {
	tracing.CountBy("host.typeAtLocation.direct", 1)
	t := p.Checker.GetTypeAtLocation(name)
	if t == nil {
		return ""
	}
	symbol := t.Symbol()
	if symbol == nil {
		return ""
	}
	text := symbol.Name
	if text == "" || strings.HasPrefix(text, "__") || !schemaTypeNamePattern.MatchString(text) {
		return ""
	}
	return text + " "
}

// AnnotationWords is annotationWords in the TS source: an
// annotation's own hover words — the worded set (or temporal
// spelling), with absence riding beside it the way a maybe known says
// it. ok=false is the TS null.
func AnnotationWords(held *annotations.Annotation) (string, bool) {
	var base string
	ok := false
	if held.Temporal != nil {
		base = refinementsets.FormatTemporal(*held.Temporal)
		ok = base != ""
	} else if held.Set != nil {
		wordText, wordCovers, hasWord := "", 0, false
		if held.Word != nil {
			wordText, wordCovers, hasWord = held.Word.Text, held.Word.Covers, true
		}
		base, ok = abstractdomain.FormatWordedSet(*held.Set, wordText, wordCovers, hasWord, held.Unread)
	}
	if !ok {
		return "", false
	}
	if held.Absent {
		return "{" + bareBraces(base) + ", or absent}", true
	}
	return base, true
}

func answerSaysNoMore() walk.Answer {
	return walk.No(walk.Unknown{Why: "adds-nothing"})
}

func answerNoRefinement() walk.Answer {
	return walk.No(walk.Unknown{
		Why:  "noted",
		Said: "the type states no refinement to read",
	})
}

// restatesTypeLine is restatesTypeLine in the TS source: whether a
// type-read claim's words would only RESTATE the type line — an
// object read from a shape, a function value, the bare boolean pair.
func restatesTypeLine(worn abstractdomain.AbstractValue) bool {
	if worn.Kind == abstractdomain.KindPossiblyUndefined && worn.Inner != nil {
		return restatesTypeLine(*worn.Inner)
	}
	if worn.Kind == abstractdomain.KindObject || worn.Kind == abstractdomain.KindHostFunction {
		return true
	}
	if worn.Kind == abstractdomain.KindSet && worn.SetKindTag == abstractdomain.SetKindTagNone {
		if len(worn.Set.Forms) == 1 {
			only := worn.Set.Forms[0]
			if only.Form == refinementsets.FormOneOf && len(only.W) == 2 &&
				((only.W[0] == 0 && only.W[1] == 1) || (only.W[0] == 1 && only.W[1] == 0)) {
				return true
			}
		}
	}
	return false
}

// StatedAnswer is statedAnswer in the TS source: what a type node
// STATES as a refinement, spelled for hover.
func StatedAnswer(
	p *program.CheckerProgram,
	typeNode *ast.Node,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
) walk.Answer {
	read := annotations.AnnotationOfType(p, typeNode, registry, objects)
	if read.Stated == nil && read.Unsupported == "" {
		// type-reading seam first: never skip host because the node
		// looks "plain" — plain is a wording fact, not a skip
		worn, wornOk := typereading.ReadDeclaredType(p.Checker, typeNode, typeNode)
		if wornOk {
			if restatesTypeLine(worn) {
				return answerNoRefinement()
			}
			words, ok := abstractdomain.FormatAbstractValue(worn)
			if !ok {
				return answerNoRefinement()
			}
			return walk.Claim(words, abstractdomain.TrustProved, false)
		}
		if PlainTypeNode(p, typeNode, map[*ast.Node]bool{}) {
			return answerNoRefinement()
		}
		tracing.CountBy("host.typeAtLocation.direct", 1)
		if !LiteralBearingType(p, p.Checker.GetTypeAtLocation(typeNode), 0, map[*checker.Type]bool{}, typeNode) {
			return answerNoRefinement()
		}
		return walk.No(walk.Unknown{Why: "annotation-not-read"})
	}
	// the kernel declined the question — a failure to answer, not an
	// absence of anything to say
	if read.Unsupported != "" {
		return walk.No(walk.Unknown{Why: "kernel-declined"})
	}
	stated := read.Stated
	switch stated.Kind {
	case annotations.DeclaredObject:
		words, ok := FormatObjectAnnotation(stated.Object)
		if !ok {
			return answerSaysNoMore()
		}
		if stated.Object != nil && stated.Object.LibraryAdapter != "" {
			return walk.Claim(words, abstractdomain.TrustLibrary, false)
		}
		return walk.Claim(words, abstractdomain.TrustProved, false)
	case annotations.DeclaredVariable:
		// an unconstrained refinement variable states only itself —
		// nothing the reader could have read beyond the type
		if !stated.BoundGrounded || stated.Bound == nil {
			return answerNoRefinement()
		}
		// a grounded bound is a UNIVERSAL claim: every instantiation
		// of T is a subset of it — spoken at library grade, never
		// stronger than the annotation it came from
		set := *stated.Bound
		for depth := 0; depth < stated.StarDepth; depth++ {
			set = refinementsets.MakeRefinedSet(refinementsets.Star(set))
		}
		words, ok := refinementsets.FormatForHover(set)
		if !ok {
			return answerSaysNoMore()
		}
		return walk.Claim(words, abstractdomain.TrustLibrary, false)
	case annotations.DeclaredPossiblyUndefined:
		// `X | undefined`: the inner statement with absence riding
		// beside it, said the way a maybe known is said
		if stated.Inner != nil && stated.Inner.Kind == annotations.DeclaredSet && stated.Inner.Set != nil {
			inner := stated.Inner
			words, ok := abstractdomain.FormatAbstractValue(
				abstractdomain.PossiblyUndefined(
					abstractdomain.KnownSet(*inner.Set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
					"", false, false,
				),
			)
			if !ok {
				return answerSaysNoMore()
			}
			if inner.LibraryAdapter != "" {
				return walk.Claim(words, abstractdomain.TrustLibrary, false)
			}
			return walk.Claim(words, abstractdomain.TrustProved, false)
		}
		return walk.No(walk.Unknown{Why: "annotation-not-read"})
	case annotations.DeclaredTuple:
		// the slots, spoken in order — the list the declaration seeds
		// (walk.AbstractValueOfDeclared's tuple arm) already formats as
		// `[a, b]`, so the hover says the same thing the walk holds
		// rather than a second spelling of the same statement.
		words, ok := abstractdomain.FormatAbstractValue(walk.AbstractValueOfDeclared(*stated))
		if !ok {
			return answerSaysNoMore()
		}
		return walk.Claim(words, abstractdomain.TrustProved, false)
	case annotations.DeclaredSet:
		var words string
		wordsOk := false
		if stated.Temporal != nil {
			words = refinementsets.FormatTemporal(*stated.Temporal)
			wordsOk = words != ""
		} else if stated.Set != nil {
			wordText, wordCovers, hasWord := "", 0, false
			if stated.Word != nil {
				wordText, wordCovers, hasWord = stated.Word.Text, stated.Word.Covers, true
			}
			words, wordsOk = abstractdomain.FormatWordedSet(*stated.Set, wordText, wordCovers, hasWord, stated.Unread)
		}
		// a DEPENDENT bound joins the brace vocabulary — the same 𝑥
		// the other facts already speak about, related to the sibling
		// by name
		dependentWords := dependentBoundWords(stated.Depends)
		var spoken string
		spokenOk := false
		switch {
		case dependentWords == "":
			spoken, spokenOk = words, wordsOk
		case !wordsOk:
			spoken, spokenOk = "{"+dependentWords+"}", true
		case strings.HasPrefix(words, "{"):
			spoken, spokenOk = words[:len(words)-1]+", "+dependentWords+"}", true
		default:
			spoken, spokenOk = words+", "+dependentWords, true
		}
		if !spokenOk {
			return answerSaysNoMore()
		}
		if stated.LibraryAdapter != "" {
			return walk.Claim(spoken, abstractdomain.TrustLibrary, false)
		}
		return walk.Claim(spoken, abstractdomain.TrustProved, false)
	}
	return walk.No(walk.Unknown{Why: "annotation-not-read"})
}
