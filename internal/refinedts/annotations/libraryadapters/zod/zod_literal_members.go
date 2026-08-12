// Ported 1:1 from annotations/library_adapters/zod/zod_literal_members.ts.
package zod

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LiteralPieceResult is the {set} | {absent} union literalPiece
// returns, plus an Ok discriminant for "neither" (null in the TS
// source).
type LiteralPieceResult struct {
	Set    refinementsets.RefinedSet
	Absent bool
	IsSet  bool
	Ok     bool
}

// LiteralPiece is literalPiece in the TS source: one literal value as
// a set piece -- a string is its codepoint tuple, a number its
// singleton, true/false the boolean codes, a safe-window bigint its
// exact value; null/undefined ride the absent flag instead.
func LiteralPiece(p *program.CheckerProgram, e *ast.Node) LiteralPieceResult {
	if s, ok := stringArg(e); ok {
		return LiteralPieceResult{Set: refinementsets.StringTuple(s), IsSet: true, Ok: true}
	}
	if v, ok := numberArg(p, e); ok {
		return LiteralPieceResult{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v})), IsSet: true, Ok: true}
	}
	if e.Kind == ast.KindTrueKeyword {
		return LiteralPieceResult{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})), IsSet: true, Ok: true}
	}
	if e.Kind == ast.KindFalseKeyword {
		return LiteralPieceResult{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})), IsSet: true, Ok: true}
	}
	if e.Kind == ast.KindNullKeyword {
		return LiteralPieceResult{Absent: true, Ok: true}
	}
	if ast.IsIdentifier(e) && e.AsIdentifier().Text == "undefined" {
		return LiteralPieceResult{Absent: true, Ok: true}
	}
	if ast.IsBigIntLiteral(e) {
		text := e.AsBigIntLiteral().Text
		digits := text
		if len(digits) > 0 && digits[len(digits)-1] == 'n' {
			digits = digits[:len(digits)-1]
		}
		if value, err := strconv.ParseFloat(digits, 64); err == nil {
			if value >= -(9007199254740991) && value <= 9007199254740991 && value == float64(int64(value)) {
				return LiteralPieceResult{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{value})), IsSet: true, Ok: true}
			}
		}
	}
	return LiteralPieceResult{}
}

// NativeEnumMembers is nativeEnumMembers in the TS source: the
// members of a TS enum declaration the identifier resolves to --
// string initializers, numeric initializers, and the auto-increment
// defaults. (nil, false) when a member is out of reach.
//
// EnumMember mirrors the TS source's `string | number` union member:
// exactly one of IsString/otherwise is meaningful, discriminated by
// IsString.
type EnumMember struct {
	IsString bool
	Str      string
	Num      float64
}

func NativeEnumMembers(p *program.CheckerProgram, e *ast.Node) ([]EnumMember, bool) {
	if !ast.IsIdentifier(e) {
		return nil, false
	}
	symbol := symbolAt(p.Checker, e)
	if symbol == nil {
		return nil, false
	}
	var declaration *ast.Node
	for _, d := range symbol.Declarations {
		if ast.IsEnumDeclaration(d) {
			declaration = d
			break
		}
	}
	if declaration == nil {
		declaration = symbol.ValueDeclaration
	}
	if declaration == nil || !ast.IsEnumDeclaration(declaration) {
		return nil, false
	}
	var values []EnumMember
	next := 0.0
	for _, member := range declaration.AsEnumDeclaration().Members.Nodes {
		initializer := member.AsEnumMember().Initializer
		if initializer == nil {
			values = append(values, EnumMember{Num: next})
			next++
			continue
		}
		if s, ok := stringArg(initializer); ok {
			values = append(values, EnumMember{IsString: true, Str: s})
			continue
		}
		v, ok := numberArg(p, initializer)
		if !ok {
			return nil, false
		}
		values = append(values, EnumMember{Num: v})
		next = v + 1
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

// UnionOfPieces is unionOfPieces in the TS source: fold literal
// members into one union set (+ the absent flag). A word carries the
// members' own spellings so single-character strings hover as their
// quotes, never their codepoints.
func UnionOfPieces(pieces []LiteralPieceResult, wordText string, hasWordText bool) compiledshape.Compiled {
	var set *refinementsets.RefinedSet
	absent := false
	for _, piece := range pieces {
		if !piece.IsSet {
			absent = true
			continue
		}
		if set == nil {
			s := piece.Set
			set = &s
		} else {
			joined := refinementsets.MakeRefinedSet(refinementsets.Union(*set, piece.Set))
			set = &joined
		}
	}
	if set == nil {
		return compiledshape.Compiled{
			Annotation: &compiledshape.AnnotationValue{Set: Bottom, Absent: true},
		}
	}
	out := &compiledshape.AnnotationValue{Set: *set, Absent: absent}
	if hasWordText {
		out.Word = &compiledshape.WordSpelling{Text: wordText, Covers: len(set.Forms)}
	}
	return compiledshape.Compiled{Annotation: out}
}

// LiteralLabel is literalLabel in the TS source: a literal member's
// own spelling for the hover word.
func LiteralLabel(p *program.CheckerProgram, e *ast.Node) (string, bool) {
	if s, ok := stringArg(e); ok {
		return strconv.Quote(s), true
	}
	if v, ok := numberArg(p, e); ok {
		return jsnum.Number(v).String(), true
	}
	if e.Kind == ast.KindTrueKeyword {
		return "true", true
	}
	if e.Kind == ast.KindFalseKeyword {
		return "false", true
	}
	if ast.IsBigIntLiteral(e) {
		return e.AsBigIntLiteral().Text, true
	}
	return "", false
}
