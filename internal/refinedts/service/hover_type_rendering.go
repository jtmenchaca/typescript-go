// from service/hover_type_rendering.ts
//
// Type rendering for hover: inferred literal unions and
// transform-schema images.
//
// TransformImage's one gap against the TS source: an
// EXPRESSION-BODIED callback needs walk's own expression evaluator
// inside InlineCallback, and that evaluator (walk.evaluateExpression)
// is unexported with no exported analyzer bundle. A BLOCK-bodied
// callback rides walk.AnalyzeStatement, which is exported, so block
// bodies answer today; expression bodies decline until walk exports
// its standard analyzer triple (the proposed one-line
// walk.StandardAnalyzers() — see the port report).

package service

import (
	"math"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// LiteralUnionOfType is literalUnionOfType in the TS source: the set
// an INFERRED literal-union type states at this name, or not-ok where
// the type is not one. The claim rests on tsc's own inference — the
// same trust every symbol resolution carries.
func LiteralUnionOfType(p *program.CheckerProgram, token *ast.Node) (walk.Answer, bool) {
	tracing.CountBy("host.typeAtLocation.direct", 1)
	t := p.Checker.GetTypeAtLocation(token)
	if t == nil {
		return walk.Answer{}, false
	}
	isUnion := (t.Flags() & checker.TypeFlagsUnion) != 0
	members := []*checker.Type{t}
	if isUnion {
		members = t.Types()
	}
	if len(members) > 24 {
		return walk.Answer{}, false // a widened blob, not a statement
	}
	type piece struct {
		set   refinementsets.RefinedSet
		label string
	}
	var pieces []piece
	for _, member := range members {
		flags := member.Flags()
		if (flags & checker.TypeFlagsNumberLiteral) != 0 {
			value, ok := literalNumberValueOf(member)
			if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
				return walk.Answer{}, false
			}
			pieces = append(pieces, piece{
				set:   refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{value})),
				label: refinementsets.FormatNumber(value),
			})
			continue
		}
		if (flags & checker.TypeFlagsStringLiteral) != 0 {
			value, ok := member.AsLiteralType().Value().(string)
			if !ok {
				return walk.Answer{}, false
			}
			pieces = append(pieces, piece{
				set:   refinementsets.StringTuple(value),
				label: strconv.Quote(value),
			})
			continue
		}
		return walk.Answer{}, false // a non-literal member: plain TypeScript
	}
	if len(pieces) == 0 || (!isUnion && len(pieces) != 1) {
		return walk.Answer{}, false
	}
	// a SINGLE literal is tsc's own narrowing — the host already
	// prints it, so the hover adds nothing
	if len(pieces) == 1 {
		return walk.Answer{}, false
	}
	set := pieces[0].set
	labels := make([]string, 0, len(pieces))
	for _, held := range pieces {
		labels = append(labels, held.label)
	}
	for _, held := range pieces[1:] {
		set = refinementsets.MakeRefinedSet(refinementsets.Union(set, held.set))
	}
	words, ok := abstractdomain.FormatWordedSet(set, strings.Join(labels, " | "), len(set.Forms), true, false)
	if !ok {
		return walk.Answer{}, false
	}
	return walk.Claim(words, abstractdomain.TrustProved, false), true
}

// literalNumberValueOf reads a number-literal type's value — tsgo
// stores it as jsnum.Number (a named float64), which a plain float64
// assertion never matches.
func literalNumberValueOf(t *checker.Type) (float64, bool) {
	switch value := t.AsLiteralType().Value().(type) {
	case jsnum.Number:
		return float64(value), true
	case float64:
		return value, true
	}
	return 0, false
}

// TransformImage is transformImage in the TS source: the image of a
// transform schema — `A.transform(cb)` answers with cb inlined over
// A's compiled set, and `z.codec(A, B, {decode})` reads like a
// transform whose callback is decode. Not-ok where the initializer is
// not a transform tail, the receiver does not compile, or the body
// does not read.
func TransformImage(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	initializer *ast.Node,
) (walk.Answer, bool) {
	e := initializer
	if ast.IsParenthesizedExpression(e) {
		e = e.AsParenthesizedExpression().Expression
	}
	if !ast.IsCallExpression(e) || !ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		return walk.Answer{}, false
	}
	call := e.AsCallExpression()
	access := call.Expression.AsPropertyAccessExpression()
	arguments := call.Arguments.Nodes

	// `z.codec(A, B, {decode, encode})`: the decode output is what a
	// parse produces, unvalidated
	var codecDecode *ast.Node
	if access.Name().Text() == "codec" && len(arguments) == 3 && ast.IsObjectLiteralExpression(arguments[2]) {
		for _, property := range arguments[2].AsObjectLiteralExpression().Properties.Nodes {
			if ast.IsPropertyAssignment(property) {
				name := property.AsPropertyAssignment().Name()
				if ast.IsIdentifier(name) && name.Text() == "decode" {
					codecDecode = property.AsPropertyAssignment().Initializer
					break
				}
			}
		}
	}
	if codecDecode == nil && (access.Name().Text() != "transform" || len(arguments) != 1) {
		return walk.Answer{}, false
	}
	callback := codecDecode
	if callback == nil {
		callback = arguments[0]
	}
	if !ast.IsArrowFunction(callback) && !ast.IsFunctionExpression(callback) {
		return walk.Answer{}, false
	}
	if callback.Body() == nil {
		return walk.Answer{}, false
	}
	kernel := kernelbridge.KernelIfLoaded()
	if kernel == nil {
		return walk.Answer{}, false
	}
	walk.SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	abstractdomain.SetLatticeKernel(kernel)
	facts := programFactsCached(p, nil, nil)
	// a codec's input is its FIRST argument; a transform's is its
	// receiver chain
	inputSource := access.Expression
	if codecDecode != nil {
		inputSource = arguments[0]
	}
	// an OBJECT-schema input: the callback runs over the shape the
	// schema states, key by key
	objectInput := annotations.CompileObject(p, inputSource, facts.registry, facts.objects)
	var input annotations.Compiled
	haveObjectInput := objectInput.Object != nil
	if !haveObjectInput {
		input = annotations.CompileAnnotation(p, inputSource, facts.registry)
		if annotations.IsUnsupported(input) || input.Annotation == nil {
			return walk.Answer{}, false
		}
	}
	ctx := &walk.FlowContext{
		P:         p,
		Kernel:    kernel,
		Registry:  facts.registry,
		Objects:   facts.objects,
		Contracts: facts.contracts,
		Report:    func(d assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	libraryRuntime := haveObjectInput || input.Annotation.LibraryAdapter != ""
	var argument abstractdomain.AbstractValue
	if haveObjectInput {
		argument = walk.WornOfObject(objectInput.Object)
	} else {
		argument = walk.WornOfAnnotation(*input.Annotation)
	}
	result := walk.InlineCallback(ctx, walk.NewEnv(), callback, argument, walk.StandardAnalyzers(), nil)
	graded := result
	if libraryRuntime {
		graded = abstractdomain.AtTrustLevel(result, abstractdomain.TrustLibrary)
	}
	if graded.Kind == abstractdomain.KindUnknown {
		return walk.Answer{}, false
	}
	plain := graded
	if plain.Kind == abstractdomain.KindSet {
		plain.Set = refinementsets.SimplifyScalar(walk.SimplificationKernelOf(kernel), plain.Set)
	}
	words, ok := abstractdomain.FormatAbstractValue(plain)
	if !ok {
		return walk.Answer{}, false
	}
	grade := abstractdomain.TrustLevelOf(plain)
	return walk.Claim(words, grade, false), true
}
