// from interprocedural/callback_writes.ts
//
// What a callback body can write through an owner parameter, the
// exact-sequence items a fold may run over, and the only prebound
// arguments a consumption site may evaluate (syntactic literals —
// the bind ran in an environment that site does not hold).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// SyntacticLiteral is a literal whose value is the same in every
// environment — the only prebound argument a consumption site may
// evaluate, since the bind ran in an environment that site does not
// hold.
func SyntacticLiteral(e *ast.Node) bool {
	if ast.IsNumericLiteral(e) || ast.IsStringLiteral(e) || ast.IsNoSubstitutionTemplateLiteral(e) {
		return true
	}
	if e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword || e.Kind == ast.KindNullKeyword {
		return true
	}
	if ast.IsPrefixUnaryExpression(e) {
		pue := e.AsPrefixUnaryExpression()
		if pue.Operator == ast.KindMinusToken && ast.IsNumericLiteral(pue.Operand) {
			return true
		}
	}
	return false
}

// writesThroughAccessWrites is the writesAt closure of the TS
// source: does the WRITE happen at this access node itself — an
// assignment target, or a stepped update?
func writesThroughAccessWrites(access *ast.Node) bool {
	grand := access.Parent
	if grand == nil {
		return false
	}
	if ast.IsBinaryExpression(grand) {
		be := grand.AsBinaryExpression()
		if be.Left == access && be.OperatorToken.Kind >= ast.KindFirstAssignment && be.OperatorToken.Kind <= ast.KindLastAssignment {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(grand) {
		operator := grand.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(grand) {
		operator := grand.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return true
		}
	}
	return false
}

// WritesThrough: can the body write through `name`? True unless
// every occurrence of the name is a read: a property or element
// READ, or a call of a method the read-only lists vouch for. An
// assignment target, a writing method, or the name handed anywhere
// else (an argument, a store) counts as a write — escape is a write
// until proved otherwise.
func WritesThrough(body *ast.Node, name string) bool {
	found := false
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found {
			return
		}
		if ast.IsIdentifier(node) && node.Text() == name {
			parent := node.Parent
			// a property NAME is not a use of the binding
			if ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Name() == node {
				return
			}
			if ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Expression == node {
				// reading a property is safe unless the access is assigned
				// to or stepped
				if writesThroughAccessWrites(parent) {
					found = true
					return
				}
				// calling a method: only the read-only lists vouch
				grand := parent.Parent
				if grand != nil && ast.IsCallExpression(grand) && grand.AsCallExpression().Expression == parent {
					methodName := parent.AsPropertyAccessExpression().Name().Text()
					_, readArray := dataflowfacts.ReadOnlyArrayMethods[methodName]
					_, readString := dataflowfacts.StringReadMethods[methodName]
					if !readArray && !readString {
						found = true
					}
				}
				return
			}
			if ast.IsElementAccessExpression(parent) && parent.AsElementAccessExpression().Expression == node {
				if writesThroughAccessWrites(parent) {
					found = true
				}
				return
			}
			// any other use — an argument, a store, a bare return — may
			// hand the reference to something that writes
			found = true
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(body)
	return found
}

// ItemsOf is an exact sequence's items — a list's own, a flat
// tuple's singletons (carrying the tuple's grade); nil where the
// receiver is not an exact sequence.
func ItemsOf(receiver abstractdomain.AbstractValue) []abstractdomain.AbstractValue {
	if receiver.Kind == abstractdomain.KindList {
		return receiver.Items
	}
	if receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray {
		out := make([]abstractdomain.AbstractValue, len(receiver.Values))
		for i, v := range receiver.Values {
			out[i] = abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(receiver))
		}
		return out
	}
	return nil
}
