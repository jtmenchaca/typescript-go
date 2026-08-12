package zod

import "testing"

func TestDeclaresZod_HoistedAndPnpmLayouts(t *testing.T) {
	cases := []struct {
		fileName string
		want     bool
	}{
		{"/repo/node_modules/zod/dist/index.d.ts", true},
		{"/repo/node_modules/.pnpm/zod@4.4.3/node_modules/zod/dist/index.d.ts", true},
		{"/repo/node_modules/zod-utils/index.d.ts", false},
		{"/repo/src/zod.ts", false},
	}
	for _, c := range cases {
		if got := DeclaresZod(c.fileName); got != c.want {
			t.Errorf("DeclaresZod(%q) = %v, want %v", c.fileName, got, c.want)
		}
	}
}
