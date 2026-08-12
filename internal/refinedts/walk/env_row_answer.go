// from control_flow/answers/env_row_answer.ts
//
// Exits 7-9: a reached position. NoAnswer is the closing sequence
// for a held unknown (exit 8). A held value with words is exit 9.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func reasonNoteWithinStatement(p *program.CheckerProgram, note assignability.ReasonNote, statement *ast.Node) bool {
	if statement == nil || note.Site != "expression" {
		return false
	}
	if ast.GetSourceFileOfNode(note.Node) != p.Entry {
		return false
	}
	return nodeStart(note.Node) >= nodeStart(statement) && note.Node.End() <= statement.End()
}

// NoAnswer is noAnswer in the TS source.
func NoAnswer(p *program.CheckerProgram, token *ast.Node, declaration *ast.Node, hostType *checker.Type, notes []assignability.ReasonNote) Answer {
	statement := StatementOf(token)
	within := func(note assignability.ReasonNote) bool {
		return reasonNoteWithinStatement(p, note, statement)
	}
	var spoken *assignability.ReasonNote
	for i := range notes {
		if notes[i].Unsupported && within(notes[i]) {
			spoken = &notes[i]
			break
		}
	}
	if spoken == nil {
		for i := range notes {
			if within(notes[i]) {
				spoken = &notes[i]
				break
			}
		}
	}
	if spoken != nil {
		return No(Unknown{Why: "noted", Said: spoken.Said, Unsupported: spoken.Unsupported})
	}
	if declaration != nil && ast.IsVariableDeclaration(declaration) {
		decl := declaration.AsVariableDeclaration()
		if decl.Initializer == nil && (ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsAmbient) != 0 {
			return No(Unknown{Why: "noted", Said: Sentence.Ambient, Unsupported: false})
		}
	}
	if p.Checker.SymbolInDefaultLib(hostType.Symbol()) {
		return No(Unknown{Why: "noted", Said: Sentence.HostClass, Unsupported: false})
	}
	return No(Unknown{Why: "noted", Said: Sentence.NothingPins, Unsupported: true})
}

// AnswerHeldRow is answerHeldRow in the TS source.
func AnswerHeldRow(
	p *program.CheckerProgram,
	kernel *kernelbridge.RefinedTSKernel,
	token *ast.Node,
	declaration *ast.Node,
	hostType *checker.Type,
	notes []assignability.ReasonNote,
	known abstractdomain.AbstractValue,
	shownByHost bool,
) Answer {
	plain := known
	if known.Kind == abstractdomain.KindSet {
		plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{kernel}, known.Set)
	}
	shown, hasShown := abstractdomain.FormatAbstractValue(plain)
	determinedNote := func() (string, bool) {
		statement := StatementOf(token)
		for _, note := range notes {
			if !note.Unsupported && reasonNoteWithinStatement(p, note, statement) {
				return note.Said, true
			}
		}
		return "", false
	}
	if !hasShown {
		switch plain.Kind {
		case abstractdomain.KindSet, abstractdomain.KindPossiblyNaN, abstractdomain.KindPossiblyUndefined, abstractdomain.KindObject, abstractdomain.KindCollection:
			said := Sentence.WalkStatesNothing
			if note, ok := determinedNote(); ok {
				said = note
			}
			return No(Unknown{Why: "noted", Said: said, Unsupported: false})
		}
		if plain.Kind == abstractdomain.KindUnknown && plain.Opaque {
			said := Sentence.FromOutside
			if note, ok := determinedNote(); ok {
				said = note
			}
			return No(Unknown{Why: "noted", Said: said, Unsupported: false})
		}
		if plain.Kind == abstractdomain.KindUnknown {
			if seeded, ok := TypeSeedAnswer(p, kernel, token, declaration); ok {
				return seeded
			}
		}
		return NoAnswer(p, token, declaration, hostType, notes)
	}
	return Claim(shown, abstractdomain.TrustLevelOf(plain), shownByHost)
}
