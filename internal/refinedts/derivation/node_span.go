// A node's two span attributes: refinery.construct — the
// sub-expression's OWN source spelling, never the whole statement's —
// and refinery.range — path:line:col-line:col of that sub-expression.
//
// Both are computed only when a trace is running: the callers guard on
// derivation.Active() before they build either string, so the off path
// never reads a source text or scans a line map.

package derivation

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// constructTextCap keeps a construct attribute to the sub-expression's
// own spelling rather than a pasted screenful. A node whose text runs
// longer than this is elided in the middle — a function expression
// handed to a callback reader is one node, and its whole body is not
// its spelling.
const constructTextCap = 120

// Construct is the node's own source spelling, leading trivia skipped
// and newlines folded to single spaces, so one span's construct sits
// on one line of the rendered tree.
func Construct(node *ast.Node) string {
	if node == nil {
		return ""
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		return ""
	}
	text := sourceFile.Text()
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	end := node.End()
	if start < 0 || end > len(text) || start >= end {
		return ""
	}
	spelling := strings.Join(strings.Fields(text[start:end]), " ")
	if len(spelling) > constructTextCap {
		spelling = spelling[:constructTextCap/2] + " … " + spelling[len(spelling)-constructTextCap/2:]
	}
	return spelling
}

// Range is path:line:col-line:col of the node, 1-based line and
// column — the schema's refinery.range pattern.
func Range(node *ast.Node) string {
	if node == nil {
		return ""
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		return ""
	}
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	startLine, startChar := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, start)
	endLine, endChar := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, node.End())
	var out strings.Builder
	out.WriteString(sourceFile.FileName())
	out.WriteByte(':')
	out.WriteString(strconv.Itoa(startLine + 1))
	out.WriteByte(':')
	out.WriteString(strconv.Itoa(int(startChar) + 1))
	out.WriteByte('-')
	out.WriteString(strconv.Itoa(endLine + 1))
	out.WriteByte(':')
	out.WriteString(strconv.Itoa(int(endChar) + 1))
	return out.String()
}

// LineOf is the node's 1-based start line — what the -explain request
// is matched against.
func LineOf(node *ast.Node) int {
	if node == nil {
		return 0
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		return 0
	}
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	line, _ := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, start)
	return line + 1
}

// PositionOf is the judged position a trace explains: path:line:col.
func PositionOf(node *ast.Node) string {
	if node == nil {
		return ""
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		return ""
	}
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	line, character := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, start)
	return sourceFile.FileName() + ":" + strconv.Itoa(line+1) + ":" + strconv.Itoa(int(character)+1)
}

// BeginNode is Begin with the node's construct and range already
// read — the spelling every dispatcher seam uses.
//
// GATING IS PER POSITION (DERIVATION-TRACE.md, "Gating"): the walk runs
// normally over the whole file, and spans are recorded only where the
// current range INTERSECTS the requested one. A node on some other line
// opens nothing at all while nothing is open; once a root IS open, every
// sub-read under it records regardless of its own line, because a
// derivation reaches wherever the values came from — a guard three lines
// up is part of the answer at the judged position.
func BeginNode(name string, node *ast.Node) *Handle {
	recorder := current()
	if recorder == nil {
		return nil
	}
	// A NARROWING IS EXEMPT FROM THE LINE GATE. A guard proves a fact
	// that a later read carries, and it sits on its own line — usually
	// above the judged position, never inside it. Gating it out would
	// leave every answered trace stating a set with nothing to say where
	// the set came from, so the guard ledger (trace.go's RecordGuard)
	// needs the span to exist even though its line was not asked about.
	// Its trace is dropped unless a judged position reclaims it, so the
	// exemption costs a span and never an extra trace.
	// A BINDING'S PRODUCER IS EXEMPT FROM THE LINE GATE, for the same
	// reason a narrowing is. The binding ledger (binding_ledger.go)
	// reclaims the derivation of the statement that BOUND a name when a
	// read stops at that bare name, and that statement sits on its own
	// line — above the judged position, never inside it. Gating it out
	// would leave the ledger with nothing to remember, and the read would
	// leaf at the name with no chain, which is exactly the gap the ledger
	// exists to close.
	//
	// The exemption is structural and narrow: only the node a write takes
	// its value FROM — a declaration's initializer, an assignment's
	// right-hand side. Its trace is dropped unless a read reclaims it, so
	// the exemption costs a subtree per binding and never an extra trace.
	if !recorder.Open() && name != NarrowingName && !recorder.WantsLine(LineOf(node)) &&
		!producesABinding(node) {
		return nil
	}
	return Begin(name, Construct(node), Range(node))
}

// producesABinding answers whether this node is the value side of an
// environment write — a variable declaration's initializer, or the
// right-hand side of an assignment.
func producesABinding(node *ast.Node) bool {
	if node == nil {
		return false
	}
	parent := node.Parent
	if parent == nil {
		return false
	}
	if ast.IsVariableDeclaration(parent) {
		return parent.Initializer() == node
	}
	if ast.IsBinaryExpression(parent) {
		binary := parent.AsBinaryExpression()
		return binary.Right == node &&
			ast.IsAssignmentOperator(binary.OperatorToken.Kind)
	}
	return false
}
