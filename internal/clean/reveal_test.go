package clean

import (
	"strings"
	"testing"
)

func TestRevealMarksWhatCannotBeSeen(t *testing.T) {
	// The invisible characters are built from their code points: written
	// literally they would be as hard to review here as anywhere else.
	r := func(code rune) string { return string(code) }
	cases := []struct {
		name, in, want string
		marked         int
	}{
		{"plain text is untouched", "go test ./...\n\tok", "go test ./...\n\tok", 0},
		{"vietnamese is untouched", "Dòng 1: xin chào", "Dòng 1: xin chào", 0},
		{"a carriage return that hides a second statement",
			"# note" + r(0x0D) + "import os", "# note␍import os", 1},
		{"escape and bell", "a" + r(0x1B) + "[2Kb" + r(0x07), "a␛[2Kb␇", 2},
		{"delete", "a" + r(0x7F), "a␡", 1},
		{"zero-width space inside a command", "rm" + r(0x200B) + " -rf", "rm⟨U+200B⟩ -rf", 1},
		{"right-to-left override in a file name", "invoice" + r(0x202E) + "fdp.exe", "invoice⟨U+202E⟩fdp.exe", 1},
		{"line separator", "one" + r(0x2028) + "two", "one⟨U+2028⟩two", 1},
		{"tag characters", "x" + r(0xE0041) + r(0xE007F), "x⟨U+E0041⟩⟨U+E007F⟩", 2},
		{"byte-order mark and soft hyphen", r(0xFEFF) + "pass" + r(0x00AD) + "word", "⟨U+FEFF⟩pass⟨U+00AD⟩word", 2},
		{"a C1 control", "a" + r(0x85) + "b", "a⟨U+0085⟩b", 1},
		{"bytes that are not text", "ok\xff\xfe", "ok⟨?⟩⟨?⟩", 2},
		{"empty", "", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, marked := Reveal(c.in)
			if got != c.want || marked != c.marked {
				t.Fatalf("Reveal(%q) = %q, %d\nwant        %q, %d", c.in, got, marked, c.want, c.marked)
			}
		})
	}
}

// Everything visible must come through exactly: Reveal may add marks, never
// take text away.
func TestRevealKeepsEveryVisibleCharacter(t *testing.T) {
	in := "SELECT * FROM orders WHERE id = 1; -- " + string(rune(0x200D)) + "\nDROP TABLE orders;\r\nCOMMIT;\rROLLBACK;"
	got, marked := Reveal(in)
	if marked != 2 {
		t.Fatalf("marked = %d, want the joiner and the lone carriage return", marked)
	}
	// A Windows line ending is an ordinary line break and is not marked; a
	// carriage return on its own is.
	for _, part := range []string{"SELECT * FROM orders WHERE id = 1; -- ", "\nDROP TABLE orders;\nCOMMIT;", "COMMIT;␍ROLLBACK;"} {
		if !strings.Contains(got, part) {
			t.Errorf("%q is missing from %q", part, got)
		}
	}
}
