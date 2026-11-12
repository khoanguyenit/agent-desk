package agent

import (
	"encoding/json"
	"strings"

	"monitor-agent/internal/clean"
)

// Operations of a DiffLine.
const (
	DiffRemoved = "-"
	DiffAdded   = "+"
	DiffSame    = " "
	// DiffNote is not a line of either text but a remark about the change.
	DiffNote = "!"
)

const (
	// maxDiffLines is how many lines, old and new together, a change may have
	// and still be drawn as a diff. A larger one is shown as plain text.
	maxDiffLines = 1500
	// maxDiffCells bounds the table used to match lines up. Past it the
	// middle of the change is shown as all of the old lines, then all of the
	// new ones: still complete, only less finely matched.
	maxDiffCells = 600_000
)

// DiffLine is one line of a change to a file.
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Change is an edit to a file laid out for reading: which file, and its lines
// before and after.
type Change struct {
	Path string `json:"path"`
	// Note says what kind of change it is when the lines alone do not.
	Note  string     `json:"note"`
	Lines []DiffLine `json:"lines"`
}

// changeOf lays out the input of a tool that edits a file as a Change. It
// answers only for inputs it understands completely: a field it does not know
// could alter what the tool does, so such an input is left to be shown as it
// is, field by field, rather than as a diff that would hide the field.
//
// With reveal set the change is being put up for approval: characters that
// cannot be seen are replaced by visible marks instead of dropped, and marked
// is how many there were.
func changeOf(tool string, raw json.RawMessage, reveal bool) (c Change, marked int, ok bool) {
	c, ok = editOf(tool, raw, func(s string) string {
		if !reveal {
			return clean.Block(s, 0)
		}
		shown, n := clean.Reveal(s)
		marked += n
		return shown
	})
	return c, marked, ok
}

func editOf(tool string, raw json.RawMessage, show func(string) string) (Change, bool) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return Change{}, false
	}
	str := func(key string) (string, bool) {
		v, ok := fields[key]
		if !ok {
			return "", false
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return "", false
		}
		delete(fields, key)
		return s, true
	}

	var c Change
	var before, after string
	path, ok := str("file_path")
	if !ok || strings.TrimSpace(path) == "" {
		return Change{}, false
	}
	switch tool {
	case "Edit":
		var okOld, okNew bool
		before, okOld = str("old_string")
		after, okNew = str("new_string")
		if !okOld || !okNew {
			return Change{}, false
		}
		if v, has := fields["replace_all"]; has {
			var all bool
			if json.Unmarshal(v, &all) != nil {
				return Change{}, false
			}
			delete(fields, "replace_all")
			if all {
				c.Note = "Thay ở mọi chỗ khớp trong file"
			}
		}
	case "Write":
		if after, ok = str("content"); !ok {
			return Change{}, false
		}
		c.Note = "Ghi toàn bộ nội dung file"
	default:
		return Change{}, false
	}
	if len(fields) != 0 {
		return Change{}, false
	}

	if strings.Count(before, "\n")+strings.Count(after, "\n")+2 > maxDiffLines {
		return Change{}, false
	}
	c.Path = show(path)
	c.Lines = lineDiff(show(before), show(after))
	return c, true
}

// splitLines cuts a text into its lines. A line break at the very end ends
// the last line; it does not start one more, empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineDiff compares two texts line by line. Every line of both texts is in
// the result, in order and unshortened, so the diff can stand in for the
// texts themselves when a change is put to the user for approval. The one
// thing lines do not show, whether the text ends with a line break, is said
// in a note when the two texts differ in it.
func lineDiff(before, after string) []DiffLine {
	out := diffLines(before, after)
	if before != "" && after != "" && strings.HasSuffix(before, "\n") != strings.HasSuffix(after, "\n") {
		note := "Bản mới kết thúc bằng một dấu xuống dòng, bản cũ thì không."
		if strings.HasSuffix(before, "\n") {
			note = "Bản cũ kết thúc bằng một dấu xuống dòng, bản mới thì không."
		}
		out = append(out, DiffLine{DiffNote, note})
	}
	return out
}

func diffLines(before, after string) []DiffLine {
	a, b := splitLines(before), splitLines(after)
	out := make([]DiffLine, 0, len(a)+len(b)+1)

	// What the two share at either end needs no matching.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		out = append(out, DiffLine{DiffSame, a[head]})
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	ma, mb := a[head:len(a)-tail], b[head:len(b)-tail]

	if len(ma)*len(mb) > maxDiffCells {
		for _, l := range ma {
			out = append(out, DiffLine{DiffRemoved, l})
		}
		for _, l := range mb {
			out = append(out, DiffLine{DiffAdded, l})
		}
	} else {
		out = append(out, matchLines(ma, mb)...)
	}

	for i := len(a) - tail; i < len(a); i++ {
		out = append(out, DiffLine{DiffSame, a[i]})
	}
	return out
}

// matchLines is the classic longest-common-subsequence diff. Within a run of
// changes the removed lines come before the added ones.
func matchLines(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	out := make([]DiffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{DiffSame, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{DiffRemoved, a[i]})
			i++
		default:
			out = append(out, DiffLine{DiffAdded, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{DiffRemoved, a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{DiffAdded, b[j]})
	}
	return out
}
