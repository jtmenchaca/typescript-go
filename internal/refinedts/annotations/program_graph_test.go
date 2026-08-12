// Interface tests for ImportedUserFiles/ReachableFiles: the entry's
// transitive import closure, imports first, and declaration files
// excluded.

package annotations

import "testing"

func TestReachableFiles_ClosureIsTheEntrysTransitiveImportsImportsFirst(t *testing.T) {
	p := newMultiFileTestProgram(t, map[string]string{
		"/helper.ts": "export const helperValue = 1;\n",
		"/main.ts": "import { helperValue } from \"./helper.ts\";\n" +
			"console.log(helperValue);\n",
	})
	files := ReachableFiles(p.program)
	var names []string
	for _, f := range files {
		names = append(names, f.FileName())
	}
	foundHelper := false
	foundMain := false
	helperIndex, mainIndex := -1, -1
	for i, name := range names {
		if name == "/helper.ts" {
			foundHelper = true
			helperIndex = i
		}
		if name == "/main.ts" {
			foundMain = true
			mainIndex = i
		}
	}
	if !foundHelper || !foundMain {
		t.Fatalf("ReachableFiles = %v, want both /helper.ts and /main.ts", names)
	}
	if helperIndex >= mainIndex {
		t.Errorf("helper.ts (%d) did not precede main.ts (%d) — imports-first order violated", helperIndex, mainIndex)
	}
	// the surface stand-in itself must NOT appear as a "user" file it
	// imports through -- ImportedUserFiles only follows import
	// specifiers actually present in the entry's own statements, and
	// this entry never imports /z.ts
	for _, name := range names {
		if name == "/z.ts" {
			t.Errorf("ReachableFiles included /z.ts, which /main.ts never imports")
		}
	}
}

func TestImportedUserFiles_FollowsOnlyImportAndExportDeclarations(t *testing.T) {
	p := newMultiFileTestProgram(t, map[string]string{
		"/a.ts": "export const a = 1;\n",
		"/b.ts": "export const b = 2;\n",
		"/main.ts": "import { a } from \"./a.ts\";\n" +
			"export * from \"./b.ts\";\n" +
			"console.log(a);\n",
	})
	imported := ImportedUserFiles(p.program, p.program.Entry)
	if len(imported) != 2 {
		t.Fatalf("ImportedUserFiles = %d files, want 2", len(imported))
	}
	names := map[string]bool{}
	for _, f := range imported {
		names[f.FileName()] = true
	}
	if !names["/a.ts"] || !names["/b.ts"] {
		t.Errorf("ImportedUserFiles = %v, want {/a.ts, /b.ts}", names)
	}
}
