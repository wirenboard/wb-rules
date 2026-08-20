package wbrules

import "testing"

// tsgo transpile diagnostics carry UTF-16 code-unit offsets; byte-based
// counting drifts one unit per non-ASCII char (Cyrillic comments are the
// norm in wild rules).
func TestLineForUTF16Pos(t *testing.T) {
	src := "// первая строка\n// вторая строка\nvar x = ;\n"
	utf16units := 0
	line := 1
	for _, r := range src {
		if line == 3 {
			break
		}
		if r == '\n' {
			line++
		}
		if r > 0xFFFF {
			utf16units += 2
		} else {
			utf16units++
		}
	}
	if got := lineForUTF16Pos(src, utf16units); got != 3 {
		t.Fatalf("UTF-16 pos %d: got line %d, want 3", utf16units, got)
	}
	if got := lineForUTF16Pos(src, 0); got != 1 {
		t.Fatalf("pos 0: got line %d, want 1", got)
	}
}

func TestPathsRefSameFile(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/etc/wb-rules/x.ts", "etc/wb-rules/x.ts", true},
		{"/etc/wb-rules/x.ts", "/etc/wb-rules/x.ts", true},
		{"/srv/mother/bar.ts", "other/bar.ts", false},
		{"/a/b.ts", "c.ts", false},
	}
	for _, c := range cases {
		if got := pathsRefSameFile(c.a, c.b); got != c.want {
			t.Fatalf("pathsRefSameFile(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
