// from service/hover_provider.ts
//
// The quick-info provider: what the hover position is known to be,
// spelled — a stated refinement first, the flow walk's knowledge
// where nothing is stated, and the annotate-from-inference action.
// AnswerAt carries the reasons for silence, which coverage reads.
//
// Every entry point takes the LIVE program plus the entry path — the
// LS seam — and holds liveCheckMu for its walk half, because the flow
// walk shares the same package-level kernel hooks the diagnostics
// walk sets (check.go's liveCheckMu comment).

package service

import (
	"context"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// FormatRefinementAt is formatRefinementAt in the TS source: what the
// hover position is known to be, spelled, plus the plain sort word of
// the known value ("number", "string", "boolean", "bigint") where the
// walk has it in hand. A STATED refinement first; where nothing is
// stated, what the flow walk KNOWS at the position. ok=false where
// neither says anything. A claim whose words the type line already
// displays is stated and counted, but never repeated in the hover.
func FormatRefinementAt(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	position int,
	surfacePaths []string,
) (string, string, bool) {
	answer := AnswerAt(ctx, prog, entryPath, position, surfacePaths)
	if !answer.HasKnown || answer.ShownByHost {
		return "", "", false
	}
	// a bare function claim only restates the signature line the hover
	// already shows — `function after(t: Cutoff): number` gains nothing
	// from `{a function}` after it, so the tooltip drops it
	if answer.Known == "{a function}" || answer.Known == "a function" {
		return "", "", false
	}
	return answer.Known, answer.SortWord, true
}

// AnnotateAt is annotateAt in the TS source: the
// annotate-from-inference action at a position, over the live program
// — a plain parameter of a non-exported function, wearing the join of
// what its call sites pass, gains a STATED contract. ok=false
// anywhere the position is not such a parameter.
func AnnotateAt(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	position int,
	surfacePaths []string,
) (walk.AnnotateAction, bool) {
	p, err := ProgramFromExisting(ctx, prog, entryPath, surfacePaths)
	if err != nil {
		return walk.AnnotateAction{}, false
	}
	if p.Done != nil {
		defer p.Done()
	}
	token := TokenAt(p.Entry, position)
	if token == nil || !ast.IsIdentifier(token) {
		return walk.AnnotateAction{}, false
	}
	liveCheckMu.Lock()
	defer liveCheckMu.Unlock()
	facts := programFactsCached(p, nil, nil)
	return walk.AnnotateActionAt(p, facts.registry, facts.objects, facts.contracts, token)
}

// admitted is the strictness dial's answer gate: a claim whose grade
// the dial does not admit is not shown — its position says the dial
// held it back (a no-op at "full", where every grade is admitted).
func admitted(answer walk.Answer) walk.Answer {
	if !answer.HasKnown {
		return answer
	}
	if !answer.HasGrade || abstractdomain.TrustLevelAdmitted(answer.Grade) {
		return answer
	}
	return walk.No(walk.Unknown{
		Why: "noted",
		Said: "held back by the strictness dial — the claim's boundary " +
			"is not admitted",
	})
}

// AnswerAt is answerAt in the TS source: the same question as
// FormatRefinementAt, answered with its reasons intact — where there
// is nothing to show, WHY there is nothing.
func AnswerAt(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	position int,
	surfacePaths []string,
) walk.Answer {
	return admitted(answerAtRaw(ctx, prog, entryPath, position, surfacePaths))
}

func answerAtRaw(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	position int,
	surfacePaths []string,
) (answer walk.Answer) {
	// a hover must never take the request down: a walk panic answers
	// as "broke", the same honest exit answerFlowAt itself uses
	defer func() {
		if r := recover(); r != nil {
			message := "panic"
			if err, ok := r.(error); ok {
				message = err.Error()
			} else if s, ok := r.(string); ok {
				message = s
			}
			answer = walk.No(walk.Unknown{Why: "broke", Error: message})
		}
	}()

	p, err := ProgramFromExisting(ctx, prog, entryPath, surfacePaths)
	if err != nil {
		return walk.No(walk.Unknown{Why: "not-a-name"})
	}
	if p.Done != nil {
		defer p.Done()
	}
	// the cheap gates FIRST: a hover on whitespace, a keyword, or a
	// literal pays nothing — the facts sweep below walks every user
	// file in the program
	token := TokenAt(p.Entry, position)
	if token == nil || !ast.IsIdentifier(token) {
		return walk.No(walk.Unknown{Why: "not-a-name"})
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := p.Checker.GetSymbolAtLocation(token)
	if symbol == nil {
		return walk.No(walk.Unknown{Why: "not-a-name"})
	}
	if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		symbol = p.Checker.GetAliasedSymbol(symbol)
	}

	// the flow walk and the facts compile share the diagnostics
	// seam's package-level kernel hooks — one walk at a time
	// (check.go's liveCheckMu)
	liveCheckMu.Lock()
	defer liveCheckMu.Unlock()

	// the annotation statements across every user file — the same
	// per-file facts a check uses
	facts := programFactsCached(p, nil, nil)

	// the statement compiled and admits no less than the host type —
	// OUR silence. Never "already-shown": TypeScript prints
	// `ZodString` for the binding, which says nothing about admitted
	// values, so claiming TypeScript covered it would be false.
	for _, declaration := range symbol.Declarations {
		// the annotation itself: `const zPct = z.number()...`
		if ast.IsVariableDeclaration(declaration) {
			varDecl := declaration.AsVariableDeclaration()
			if held := facts.registry[symbol]; held != nil {
				words, ok := AnnotationWords(held)
				if !ok {
					return answerSaysNoMore()
				}
				if held.LibraryAdapter != "" {
					return walk.Claim(SchemaTypeName(p, varDecl.Name())+words, abstractdomain.TrustLibrary, false)
				}
				return walk.Claim(words, abstractdomain.TrustProved, false)
			}
			// an OBJECT statement: `const zRow = z.object({...})` —
			// its keys are the annotation, and the hover shows them
			if heldObject := facts.objects[symbol]; heldObject != nil {
				words, ok := FormatObjectAnnotation(heldObject)
				if !ok {
					return answerSaysNoMore()
				}
				if heldObject.LibraryAdapter != "" {
					return walk.Claim(SchemaTypeName(p, varDecl.Name())+words, abstractdomain.TrustLibrary, false)
				}
				return walk.Claim(words, abstractdomain.TrustProved, false)
			}
			// a typed binding: `const p: Pct = …` — an unread
			// annotation here falls through to the flow walk, which
			// may know the value
			if varDecl.Type != nil {
				if stated := StatedAnswer(p, varDecl.Type, facts.registry, facts.objects); stated.HasKnown {
					return stated
				}
			}
		}
		// the alias: `type Pct = z.infer<typeof zPct>`
		if ast.IsTypeAliasDeclaration(declaration) {
			return StatedAnswer(p, declaration.AsTypeAliasDeclaration().Type, facts.registry, facts.objects)
		}
		// a typed parameter: hovering `p` in `fee(p: Pct)`
		if ast.IsParameterDeclaration(declaration) && declaration.AsParameterDeclaration().Type != nil {
			return StatedAnswer(p, declaration.AsParameterDeclaration().Type, facts.registry, facts.objects)
		}
		// an object-literal key holding an INLINE OBJECT schema — its
		// keys are the statement, the way a top-level z.object reads
		if ast.IsPropertyAssignment(declaration) {
			initializer := declaration.AsPropertyAssignment().Initializer
			if initializer != nil && annotations.RootsInObject(p, initializer) {
				compiled := annotations.CompileObject(p, initializer, facts.registry, facts.objects)
				if compiled.Object != nil {
					words, ok := FormatObjectAnnotation(compiled.Object)
					if !ok {
						return answerSaysNoMore()
					}
					if annotations.RootsInLibraryAdapter(p, initializer) {
						return walk.Claim(SchemaTypeName(p, initializer)+words, abstractdomain.TrustLibrary, false)
					}
					return walk.Claim(words, abstractdomain.TrustProved, false)
				}
			}
			// an object-literal key holding an INLINE schema — the
			// wild's createEnv/.input spelling: the key's hover is the
			// statement
			if initializer != nil && annotations.RootsInSurface(p, initializer) {
				compiled := annotations.CompileAnnotation(p, initializer, facts.registry)
				if !annotations.IsUnsupported(compiled) && compiled.Annotation != nil {
					words, ok := AnnotationWords(compiled.Annotation)
					if ok {
						if compiled.Annotation.LibraryAdapter != "" {
							return walk.Claim(SchemaTypeName(p, initializer)+words, abstractdomain.TrustLibrary, false)
						}
						return walk.Claim(words, abstractdomain.TrustProved, false)
					}
					return answerSaysNoMore()
				}
			}
		}
	}
	// hovering INSIDE an inline chain (`z.string().url()` written as
	// an argument): the outermost enclosing chain that compiles IS the
	// statement at this position
	{
		var chain *ast.Node
		for cursor := token; cursor.Parent != nil; cursor = cursor.Parent {
			parent := cursor.Parent
			if ast.IsPropertyAccessExpression(parent) || ast.IsCallExpression(parent) ||
				ast.IsParenthesizedExpression(parent) {
				chain = parent
				continue
			}
			break
		}
		if chain != nil && annotations.RootsInSurface(p, chain) {
			compiled := annotations.CompileAnnotation(p, chain, facts.registry)
			if !annotations.IsUnsupported(compiled) && compiled.Annotation != nil {
				words, ok := AnnotationWords(compiled.Annotation)
				if ok {
					if compiled.Annotation.LibraryAdapter != "" {
						return walk.Claim(SchemaTypeName(p, chain)+words, abstractdomain.TrustLibrary, false)
					}
					return walk.Claim(words, abstractdomain.TrustProved, false)
				}
			}
		}
	}
	return FlowAnswerAt(p, facts.registry, facts.objects, facts.contracts, token, symbol)
}
