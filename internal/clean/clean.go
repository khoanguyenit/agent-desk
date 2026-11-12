// Package clean prepares text taken from transcripts and registry files for
// display. Such text can be anything a tool printed or a repository contained,
// so it is shortened, stripped of characters that could disguise it, and has
// values that look like credentials masked.
package clean

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mask replaces a value that should not be shown.
const Mask = "•••"

// Text collapses whitespace, removes control and bidirectional-override
// characters, and shortens s to at most max runes.
func Text(s string, max int) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError, isHidden(r):
			continue
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if max <= 0 || utf8.RuneCountInString(out) <= max {
		return out
	}
	r := []rune(out)
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}

// Block prepares multi-line text for display: line breaks are kept, tabs
// become spaces, control and bidirectional-override characters are removed,
// and the result is limited to max runes. Unlike Text it does not collapse
// whitespace, so code and command output keep their shape, and unlike Snippet
// it masks nothing: it is for showing a person exactly what an agent wrote or
// is asking to run.
func Block(s string, max int) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for _, r := range s {
		switch {
		case r == '\r' || r == utf8.RuneError:
			continue
		case r == '\t':
			b.WriteString("    ")
			count += 4
			continue
		case r == '\n':
		case isHidden(r) || unicode.IsControl(r):
			continue
		}
		if max > 0 && count >= max {
			b.WriteString("\n…")
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.Trim(b.String(), "\n")
}

// isHidden reports characters that have no place in a label: control codes
// (which a terminal would act on) and the bidirectional controls that can
// make a path or name read differently from what it is.
func isHidden(r rune) bool {
	if unicode.IsControl(r) && !unicode.IsSpace(r) {
		return true
	}
	switch {
	case r >= 0x202A && r <= 0x202E, // embeddings and overrides
		r >= 0x2066 && r <= 0x2069,            // isolates
		r == 0x200E, r == 0x200F, r == 0x061C, // direction marks
		r == 0xFEFF: // byte-order mark
		return true
	}
	return false
}

type rule struct {
	re   *regexp.Regexp
	repl string
}

// The rules are best-effort pattern matching. They catch the common ways a
// credential ends up in a command line, a URL or pasted text; they cannot
// recognise a secret that has no telling shape.
var rules = []rule{
	// Private key blocks.
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*`), Mask},
	// user:password@host in a URL.
	{regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`), "${1}" + Mask + "@"},
	// Authorization headers and bearer tokens.
	{regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(?:(?:bearer|basic|token)\s+)?[^\s"',;]+`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Mask},
	// Secrets passed in a URL query string.
	{regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api_?key|key|sig|signature|password|passwd|secret|client_secret|code)=)[^&\s#"']+`), "${1}" + Mask},
	// Environment-style assignments: DB_PASSWORD=..., API_TOKEN="...".
	{regexp.MustCompile(`\b([A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIALS?)[A-Z0-9_]*\s*=\s*)("[^"]*"|'[^']*'|[^\s;&|]+)`), "${1}" + Mask},
	// JSON members with a telling name and a string value.
	{regexp.MustCompile(`(?i)("[a-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential)[a-z0-9_-]*"\s*:\s*)"[^"]*"`), "${1}\"" + Mask + "\""},
	// key: value or key=value with a telling key.
	{regexp.MustCompile(`(?i)\b((?:password|passwd|pwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret|(?:auth|access|refresh|session|bearer)[_-]?token)\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&|]+)`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)\b(token=)[^\s&"',;|]{8,}`), "${1}" + Mask},
	// Command-line flags that take a secret as the next argument.
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|pwd|token|secret|api-?key|access-?key)[ =]+)("[^"]*"|'[^']*'|[^\s;&|]+)`), "${1}" + Mask},
	// mysql -pSECRET and friends.
	{regexp.MustCompile(`(?i)\b((?:mysql|mysqldump|mysqladmin|mariadb|sshpass)\b[^|;&\n]*?\s-p)\s?[^\s;&|]+`), "${1}" + Mask},
	// Tokens with a recognisable shape.
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Mask},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), Mask},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), Mask},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), Mask},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}`), Mask},
}

// Redact masks values in s that look like credentials. It is applied to
// everything shown from a transcript; see the rules for what it recognises.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Snippet is Redact followed by Text: the form in which transcript text is
// allowed to reach the screen.
func Snippet(s string, max int) string {
	return Text(Redact(s), max)
}
