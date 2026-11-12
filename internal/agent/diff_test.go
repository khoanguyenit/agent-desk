package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func render(lines []DiffLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Op + l.Text + "\n")
	}
	return b.String()
}

// side rebuilds the lines of one of the two texts from a diff: those that are
// not of the other side's operation.
func side(lines []DiffLine, drop string) string {
	var out []string
	for _, l := range lines {
		if l.Op != drop && l.Op != DiffNote {
			out = append(out, l.Text)
		}
	}
	return strings.Join(out, "\n")
}

func noted(lines []DiffLine) bool {
	return len(lines) > 0 && lines[len(lines)-1].Op == DiffNote
}

func TestLineDiff(t *testing.T) {
	cases := []struct{ name, before, after, want string }{
		{"one line changed in the middle",
			"func a() {\n\tx := 1\n\treturn x\n}", "func a() {\n\tx := 2\n\treturn x\n}",
			" func a() {\n-\tx := 1\n+\tx := 2\n \treturn x\n }\n"},
		{"lines added", "a\nc", "a\nb\nc", " a\n+b\n c\n"},
		{"lines removed", "a\nb\nc", "a\nc", " a\n-b\n c\n"},
		{"nothing in common", "old", "new", "-old\n+new\n"},
		{"a new file", "", "one\ntwo", "+one\n+two\n"},
		{"emptied", "one\ntwo", "", "-one\n-two\n"},
		{"identical", "same\nsame", "same\nsame", " same\n same\n"},
		{"both empty", "", "", ""},
		{"a run of changes lists removals first", "a\nb\nc\nd", "a\nB\nC\nd", " a\n-b\n-c\n+B\n+C\n d\n"},
		{"a blank line is a line", "a\n\nb", "a\nb", " a\n-\n b\n"},
		// The line break that ends a file ends its last line; it is not one
		// more, empty line.
		{"a new file that ends with a line break", "", "one\ntwo\n", "+one\n+two\n"},
		{"both end with a line break", "a\nb\n", "a\nc\n", " a\n-b\n+c\n"},
		{"only the final line break is taken away", "a\n", "a", " a\n!Bản cũ kết thúc bằng một dấu xuống dòng, bản mới thì không.\n"},
		{"only the final line break is added", "a", "a\n", " a\n!Bản mới kết thúc bằng một dấu xuống dòng, bản cũ thì không.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lineDiff(c.before, c.after)
			if render(got) != c.want {
				t.Fatalf("diff:\n%s\nwant:\n%s", render(got), c.want)
			}
		})
	}
}

// The property that makes a diff safe to approve from: both texts can be read
// back out of it whole, whatever they are. Their lines are all there, and the
// one thing lines cannot show, a difference in the final line break, is noted.
func TestLineDiffLosesNothing(t *testing.T) {
	texts := []string{
		"", "x", "x\n", "\n", "\n\n", "a\nb\nc", "a\nb\nc\n", "c\nb\na", "a\na\na\nb", "b\na\na\na",
		"tab\there", strings.Repeat("line\n", 50) + "end", "α\nβ\nγ",
	}
	body := func(s string) string { return strings.TrimSuffix(s, "\n") }
	for _, before := range texts {
		for _, after := range texts {
			d := lineDiff(before, after)
			if got := side(d, DiffAdded); got != body(before) {
				t.Errorf("before %q, after %q: the old text reads back as %q", before, after, got)
			}
			if got := side(d, DiffRemoved); got != body(after) {
				t.Errorf("before %q, after %q: the new text reads back as %q", before, after, got)
			}
			differs := before != "" && after != "" && strings.HasSuffix(before, "\n") != strings.HasSuffix(after, "\n")
			if noted(d) != differs {
				t.Errorf("before %q, after %q: final line break differs=%v, noted=%v", before, after, differs, noted(d))
			}
		}
	}

	// Too big to match line by line: still complete.
	var a, b strings.Builder
	for i := range 900 {
		fmt.Fprintf(&a, "old %d\n", i)
		fmt.Fprintf(&b, "new %d\n", i)
	}
	d := lineDiff("first\n"+a.String()+"last", "first\n"+b.String()+"last")
	if side(d, DiffAdded) != "first\n"+a.String()+"last" || side(d, DiffRemoved) != "first\n"+b.String()+"last" {
		t.Fatal("a change too large to match up lost lines")
	}
	if d[0].Op != DiffSame || d[len(d)-1].Op != DiffSame {
		t.Errorf("shared first and last lines should be shown as shared: %q … %q", d[0], d[len(d)-1])
	}
}

func TestChangeOf(t *testing.T) {
	input := func(v any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	c, _, ok := changeOf("Edit", input(map[string]any{"file_path": `D:\app\a.go`, "old_string": "x := 1", "new_string": "x := 2"}), false)
	if !ok || c.Path != `D:\app\a.go` || c.Note != "" || render(c.Lines) != "-x := 1\n+x := 2\n" {
		t.Fatalf("edit: ok=%v change=%+v", ok, c)
	}
	c, _, ok = changeOf("Edit", input(map[string]any{"file_path": "a.go", "old_string": "a", "new_string": "b", "replace_all": true}), false)
	if !ok || !strings.Contains(c.Note, "mọi chỗ") {
		t.Fatalf("replace_all must be said out loud: ok=%v note=%q", ok, c.Note)
	}
	c, _, ok = changeOf("Write", input(map[string]any{"file_path": "notes.txt", "content": "one\ntwo"}), false)
	if !ok || c.Note == "" || render(c.Lines) != "+one\n+two\n" {
		t.Fatalf("write: ok=%v change=%+v", ok, c)
	}

	// Anything not fully understood is not drawn as a diff: the plain view
	// shows such an input field by field instead.
	refused := map[string]json.RawMessage{
		"another tool":             input(map[string]any{"file_path": "a", "old_string": "a", "new_string": "b"}),
		"a field it does not know": input(map[string]any{"file_path": "a", "old_string": "a", "new_string": "b", "also_run": "rm -rf /"}),
		"no path":                  input(map[string]any{"old_string": "a", "new_string": "b"}),
		"missing new text":         input(map[string]any{"file_path": "a", "old_string": "a"}),
		"wrong type":               input(map[string]any{"file_path": "a", "old_string": 1, "new_string": "b"}),
		"replace_all not a bool":   input(map[string]any{"file_path": "a", "old_string": "a", "new_string": "b", "replace_all": "yes"}),
		"not an object":            json.RawMessage(`"text"`),
		"empty":                    nil,
	}
	for name, raw := range refused {
		tool := "Edit"
		if name == "another tool" {
			tool = "Bash"
		}
		if c, _, ok := changeOf(tool, raw, false); ok {
			t.Errorf("%s: drawn as a diff: %+v", name, c)
		}
	}

	huge := strings.Repeat("line\n", maxDiffLines)
	if _, _, ok := changeOf("Write", input(map[string]any{"file_path": "big.txt", "content": huge}), false); ok {
		t.Error("a change past the size limit was drawn as a diff")
	}
}
