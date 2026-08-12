package objectgraphs

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestPathsAndObjectsDefaultToNone(t *testing.T) {
	S := SpecificationOf(SpecificationParts{
		Nodes: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Integer)},
	})
	if len(S.Paths) != 0 {
		t.Errorf("S.Paths = %v, want empty", S.Paths)
	}
	if len(S.Objects) != 0 {
		t.Errorf("S.Objects = %v, want empty", S.Objects)
	}
	if len(S.Nodes) != 1 {
		t.Errorf("len(S.Nodes) = %d, want 1", len(S.Nodes))
	}
}
