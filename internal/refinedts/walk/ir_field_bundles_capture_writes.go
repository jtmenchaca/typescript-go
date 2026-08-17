// split from ir_field_bundles.go — the capture write set

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// CaptureWriteSet answers every field the collected capture-called
// methods can WRITE, transitively through the class's own methods — the
// havoc set a consumer applies at every call statement when it admits a
// method-calling capture.
//
// The closure is over the class's own declared members. Each visited
// method's census must itself be tame: no escape, no computed write;
// its Writes accumulate, and its own captured and direct method calls
// join the worklist. Any name the class does not declare at all (an
// inherited method, an accessor, an overload signature) makes the whole
// set incomputable and the answer is (nil, false) — the caller then
// keeps the escape.
//
// A name that declares a FIELD rather than a method is a call through a
// stored function value, and it answers here too. The class's own
// assignments to that field are the only sources of what it holds
// (a field the census let out would have escaped already), so:
//
//   - every assignment a function LITERAL this walk can read — the
//     initializer arrow, a constructor-assigned arrow — contributes its
//     body's write set, and the field's call moves their UNION;
//   - any assignment it cannot read (an imported handler, a parameter, a
//     call's result) leaves the stored body unknown, and the answer is
//     EVERY field of the class with ok true — the declaration bounds
//     which fields exist, so whole-bundle havoc says exactly what is
//     true. It is strictly stronger than refusing: refusing costs the
//     class every field READING as well, and no reading is wrong here.
//
// A worklist name may be a plain identifier or a `#sym:` spelling. The
// second resolves through symbolKeyedMethodOf: a computed name is not
// unreadable when its key is a STABLE SYMBOL const, because that key
// names one declaration, and one declaration is all this closure needs
// to walk a body. Only a computed name whose key is NOT stable is
// unreadable, and no such name ever reaches this worklist.
func CaptureWriteSet(classLike *ast.Node, fields []BundleField, methods []string) ([]BundleField, bool) {
	return CaptureWriteSetWith(nil, classLike, fields, methods)
}

// CaptureWriteSetWith is CaptureWriteSet carrying the checker the stable
// symbol key needs, so a captured method writing `this[S]` contributes
// that field to the havoc set instead of making the whole set
// incomputable through a computed write.
func CaptureWriteSetWith(c *checker.Checker, classLike *ast.Node, fields []BundleField, methods []string) ([]BundleField, bool) {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, false
	}
	spelled := BundleFieldsAs("this", fields)
	written := map[string]struct{}{}
	seen := map[string]struct{}{}
	worklist := append([]string{}, methods...)
	for len(worklist) > 0 {
		name := worklist[0]
		worklist = worklist[1:]
		if _, visited := seen[name]; visited {
			continue
		}
		seen[name] = struct{}{}
		var body *ast.Node
		for _, member := range classLike.ClassLikeData().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			memberName := member.Name()
			// a PRIVATE-NAMED method (`#helper`) reaches this worklist the
			// same way a plain one does — the census's own private arm
			// recognizes `this.#helper()` — so it resolves here too
			if memberName == nil ||
				(!ast.IsIdentifier(memberName) && !ast.IsPrivateIdentifier(memberName)) ||
				memberName.Text() != name {
				continue
			}
			body = member.Body()
			break
		}
		if body == nil {
			// a SYMBOL-KEYED method (`[S]() { … }`), named on the worklist
			// under its `#sym:` spelling. The key identity resolves it to
			// one declaration the same way it resolves an access, so the
			// closure walks its body exactly as it walks a dotted method's.
			if method := symbolKeyedMethodOf(c, classLike, name); method != nil {
				body = method.Body()
			}
		}
		if body == nil {
			// the name declares no METHOD — but a class field can HOLD a
			// function (`private readonly handler = (x: number) => { … }`,
			// or a constructor-assigned one), and `this.handler()` calls
			// through that stored value. The class's own assignments to the
			// field are the only sources of what it holds, so where every one
			// of them is a function literal this walk can read, the union of
			// their bodies' write sets is what the call can move.
			literals, everyAssignmentWalkable, isField := fieldValuedFunctionBodies(c, classLike, name)
			switch {
			case !isField:
				// an inherited method, an accessor, a bodyless overload — the
				// name resolves to no declaration of this class at all, so
				// nothing bounds its writes
				return nil, false
			case !everyAssignmentWalkable:
				// the field holds something this walk cannot read — an
				// imported handler, a constructor parameter, a value returned
				// by a call. The DECLARATION still bounds which fields exist,
				// so every field of the class joins the havoc set: the call
				// through the stored closure may write any of them, and none
				// of them may be believed past it. That is strictly stronger
				// than refusing the bundle, which costs the class every
				// READING too — a field this body reads before the call keeps
				// its answer either way, and refusing throws it away for
				// nothing.
				return append([]BundleField{}, fields...), true
			default:
				for _, literal := range literals {
					literalCensus := FieldCensusWith(c, literal, "this", spelled)
					if literalCensus.Escapes || literalCensus.ComputedWrite ||
						len(literalCensus.AccessorStores) > 0 || len(literalCensus.AccessorReads) > 0 {
						return nil, false
					}
					for _, field := range literalCensus.Writes {
						written[field.Name] = struct{}{}
					}
					worklist = append(worklist, literalCensus.CapturedMethodCalls...)
					worklist = append(worklist, literalCensus.DirectMethodCalls...)
				}
				continue
			}
		}
		census := FieldCensusWith(c, body, "this", spelled)
		// an accessor store or read inside a walked body keeps the whole
		// set incomputable — this closure has no accessor fold, and
		// admitting the body would drop the setter's own writes from the
		// havoc set (these occurrences WERE escapes before the census
		// learned to defer them)
		if census.Escapes || census.ComputedWrite ||
			len(census.AccessorStores) > 0 || len(census.AccessorReads) > 0 {
			return nil, false
		}
		for _, field := range census.Writes {
			written[field.Name] = struct{}{}
		}
		worklist = append(worklist, census.CapturedMethodCalls...)
		worklist = append(worklist, census.DirectMethodCalls...)
	}
	out := make([]BundleField, 0, len(written))
	for _, field := range fields {
		if _, wrote := written[field.Name]; wrote {
			out = append(out, field)
		}
	}
	return out, true
}

// fieldValuedFunctionBodies answers what a call through a class FIELD
// holding a function can move: the BODIES of every function literal the
// class assigns to that field, and whether the class's assignments were
// ALL such literals.
//
// THE CLOSED-SET ARGUMENT, which is what makes the union sound. A field
// holds whatever was last stored into it, so the write set of a call
// through it is the union over the possible stored values. The class's
// OWN TEXT bounds that set: the field's initializer and every
// `this.<field> = …` in its members are the only stores, because a store
// from anywhere else needs the instance, and an instance the class let
// out is an ESCAPE the census already reported (Escapes kills the bundle
// before this walk runs). So enumerating the class's assignments
// enumerates the sources.
//
// WHAT COUNTS AS WALKABLE: an arrow function or a function expression
// with a body. Its body is then read by the caller with the SAME census
// machinery a method's body is read by — a stored closure writing
// `this.count` writes the field a method writing it writes, and the
// arrow keeps the enclosing `this`, which is the instance.
//
// A function-expression assignment is walkable on the same terms with
// one difference the caller does not have to know: `function () { … }`
// rebinds `this`, so a `this.count` inside it denotes some other
// receiver and the census reads nothing of this bundle from it. That is
// a body contributing no writes, not a body whose writes are unknown.
//
// Answers (bodies, everyAssignmentWalkable, isField). isField false means
// the name declares no field of this class either, so the caller keeps
// its refusal.
func fieldValuedFunctionBodies(
	c *checker.Checker,
	classLike *ast.Node,
	name string,
) (bodies []*ast.Node, everyAssignmentWalkable bool, isField bool) {
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, false, false
	}
	members := classLike.ClassLikeData().Members.Nodes
	// the DECLARATION: a non-static property whose name spells this field,
	// dotted or under a stable symbol key
	var declared *ast.Node
	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		if fieldMemberName(c, member.Name()) != name {
			continue
		}
		declared = member
		break
	}
	if declared == nil {
		return nil, false, false
	}
	everyAssignmentWalkable = true
	note := func(value *ast.Node) {
		value = Unwrapped(value)
		if value == nil {
			everyAssignmentWalkable = false
			return
		}
		if !ast.IsArrowFunction(value) && !ast.IsFunctionExpression(value) {
			// an imported handler, a parameter, a call's result, another
			// field's value — this walk cannot say what body it holds
			everyAssignmentWalkable = false
			return
		}
		body := value.Body()
		if body == nil {
			everyAssignmentWalkable = false
			return
		}
		bodies = append(bodies, body)
	}
	if initializer := declared.AsPropertyDeclaration().Initializer; initializer != nil {
		note(initializer)
	}
	// every `this.<field> = …` the class's own members spell — the
	// constructor's assignment is the common one, a method re-assigning
	// the handler is the same kind of store, and a store inside another
	// field's initializer is one too. A member with neither a body nor an
	// initializer spells no assignment.
	for _, member := range members {
		scanned := member.Body()
		if scanned == nil && ast.IsPropertyDeclaration(member) {
			scanned = member.AsPropertyDeclaration().Initializer
		}
		if scanned == nil {
			continue
		}
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node == nil {
				return false
			}
			if ast.IsBinaryExpression(node) {
				binary := node.AsBinaryExpression()
				operator := binary.OperatorToken.Kind
				if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
					if target := Unwrapped(binary.Left); thisFieldStoreNameOf(c, target) == name {
						if operator == ast.KindEqualsToken {
							note(binary.Right)
						} else {
							// a COMPOUND store into a function-valued field
							// (`this.handler ||= f`) leaves a value this reading
							// cannot name
							everyAssignmentWalkable = false
						}
						visit(binary.Right)
						return false
					}
				}
			}
			node.ForEachChild(visit)
			return false
		}
		visit(scanned)
	}
	return bodies, everyAssignmentWalkable, true
}

// fieldMemberName spells a class member's name the way the field set
// spells it — a plain or private identifier under its own text, a
// computed name under its `#sym:` name when the key is a stable symbol
// const — or "" when the name spells no field.
func fieldMemberName(c *checker.Checker, name *ast.Node) string {
	if name == nil {
		return ""
	}
	if ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name) {
		return name.Text()
	}
	if spelled, isSymbolKey := symbolMemberFieldName(c, name); isSymbolKey {
		return spelled
	}
	return ""
}

// thisFieldStoreNameOf spells the field a `this.<name>` or `this[S]`
// STORE TARGET names — the two spellings the census reads a field under
// — or "" for anything else. Both spellings answer here so the
// assignment scan reads `this.handler = …` and `this[S] = …` as stores
// into one field. (thisFieldNameOf, kernel_summaries.go, is the same
// question over a slot's PATH STRING rather than over a target node.)
func thisFieldStoreNameOf(c *checker.Checker, node *ast.Node) string {
	if node == nil {
		return ""
	}
	if ast.IsElementAccessExpression(node) {
		element := node.AsElementAccessExpression()
		if Unwrapped(element.Expression) == nil ||
			Unwrapped(element.Expression).Kind != ast.KindThisKeyword {
			return ""
		}
		if spelled, isSymbolKey := SymbolKeyedFieldName(c, node); isSymbolKey {
			return spelled
		}
		return ""
	}
	if !ast.IsPropertyAccessExpression(node) {
		return ""
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return ""
	}
	receiver := Unwrapped(access.Expression)
	if receiver == nil || receiver.Kind != ast.KindThisKeyword {
		return ""
	}
	if !ast.IsIdentifier(access.Name()) && !ast.IsPrivateIdentifier(access.Name()) {
		return ""
	}
	return access.Name().Text()
}
