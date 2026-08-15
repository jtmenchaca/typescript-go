// split from ir_field_bundles.go — the scan's shared state and its notes

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// fieldCensusScan is the state ONE FieldCensusWith run carries while it
// walks a body: the checker the stable symbol key needs, the receiver
// spelling the walk answers to, the declared field set by name, the read
// and write names gathered in whatever order the body mentions them, the
// nodes an outer form already accounted for, and the census being filled.
type fieldCensusScan struct {
	c            *checker.Checker
	receiverName string
	byName       map[string]BundleField
	readNames    map[string]struct{}
	writeNames   map[string]struct{}
	// consumed marks the nodes an outer form already accounted for, so a
	// `this` inside a recognized `this.x` is not counted a second time as
	// a bare mention.
	consumed map[*ast.Node]struct{}
	census   FieldCensus
}

// newFieldCensusScan indexes the field set by name and hands back the
// empty state one body's walk fills.
func newFieldCensusScan(c *checker.Checker, receiverName string, fields []BundleField) *fieldCensusScan {
	byName := map[string]BundleField{}
	for _, field := range fields {
		byName[field.Name] = field
	}
	return &fieldCensusScan{
		c:            c,
		receiverName: receiverName,
		byName:       byName,
		readNames:    map[string]struct{}{},
		writeNames:   map[string]struct{}{},
		consumed:     map[*ast.Node]struct{}{},
	}
}

// isReceiver: does this node denote the object the census is about?
// `this` answers under the name "this"; any other name answers as its
// own identifier.
func (s *fieldCensusScan) isReceiver(node *ast.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == ast.KindThisKeyword {
		return s.receiverName == "this"
	}
	return ast.IsIdentifier(node) && node.Text() == s.receiverName
}

// noteRead / noteWrite record a field once each, whatever the body's
// order — the lists are rebuilt in declaration order at the end.
func (s *fieldCensusScan) noteRead(name string) bool {
	if _, declared := s.byName[name]; !declared {
		return false
	}
	s.readNames[name] = struct{}{}
	return true
}

func (s *fieldCensusScan) noteWrite(name string) bool {
	if _, declared := s.byName[name]; !declared {
		return false
	}
	s.writeNames[name] = struct{}{}
	return true
}

// noteDirectMethodCall records a receiver method the body calls in
// plain statement position, once, in first-mention order. The name is
// a dotted step's own identifier or a symbol-keyed member's `#sym:`
// spelling — the write-set closure resolves both back to one
// declaration, so one list holds them.
func (s *fieldCensusScan) noteDirectMethodCall(name string) {
	for _, held := range s.census.DirectMethodCalls {
		if held == name {
			return
		}
	}
	s.census.DirectMethodCalls = append(s.census.DirectMethodCalls, name)
}

// noteCapturedMethodCall records a receiver method some DEFERRED form
// calls — a nested closure or a `.bind(this)` — once, in first-mention
// order.
func (s *fieldCensusScan) noteCapturedMethodCall(method string) {
	for _, held := range s.census.CapturedMethodCalls {
		if held == method {
			return
		}
	}
	s.census.CapturedMethodCalls = append(s.census.CapturedMethodCalls, method)
}

// fieldAccessOf: is this a plain `<receiver>.<name>` property access,
// or a `<receiver>[S]` access under a STABLE SYMBOL const? Both name
// exactly one field, so both answer here and every read, write, and
// callee rule treats them alike.
//
// An optional step is neither — `this?.x` admits an absent receiver,
// which no slot spells, so it falls through to the escape rule.
func (s *fieldCensusScan) fieldAccessOf(node *ast.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	if ast.IsElementAccessExpression(node) {
		if !s.isReceiver(Unwrapped(node.AsElementAccessExpression().Expression)) {
			return "", false
		}
		// the symbol const IS the property key at runtime, so the access
		// names one field the way a dotted step does
		return SymbolKeyedFieldName(s.c, node)
	}
	if !ast.IsPropertyAccessExpression(node) {
		return "", false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	if !s.isReceiver(Unwrapped(access.Expression)) {
		return "", false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", false
	}
	return access.Name().Text(), true
}

// censusInDeclarationOrder finishes the report: the lists come back in
// the field set's own declaration order, never in the body's mention
// order — that is what lets the layout and the call sites build the
// same slot vector from the same census.
func (s *fieldCensusScan) censusInDeclarationOrder(fields []BundleField) FieldCensus {
	census := s.census
	for _, field := range fields {
		if _, read := s.readNames[field.Name]; read {
			census.Reads = append(census.Reads, field)
		}
	}
	for _, field := range fields {
		if _, written := s.writeNames[field.Name]; written {
			census.Writes = append(census.Writes, field)
		}
	}
	return census
}
