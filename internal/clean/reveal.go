package clean

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Reveal prepares text that a person is about to approve: a command, or the
// contents of a file to be written. Where Block quietly drops characters that
// have no place on screen, Reveal puts a visible mark in their place, because
// here a character that cannot be seen is a character that can change what
// runs without the reader knowing. A carriage return on its own, which some
// interpreters take as the end of a line, becomes ␍; other control codes
// become their control pictures; and characters that draw nothing or reorder
// the text around them (zero-width, bidirectional, line-separator and tag
// characters) become ⟨U+XXXX⟩. Line feeds and tabs are kept as they are.
//
// marked is how many characters were replaced.
func Reveal(s string) (out string, marked int) {
	plain := true
	for _, r := range s {
		if r != '\n' && r != '\t' && (r == utf8.RuneError || needsMark(r)) {
			plain = false
			break
		}
	}
	if plain {
		return s, 0
	}

	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r' && i < len(s) && s[i] == '\n':
			// The first half of a Windows line ending. It hides nothing: the
			// line break that follows is shown.
		case r == utf8.RuneError && size == 1:
			b.WriteString("⟨?⟩")
			marked++
		case r < 0x20:
			b.WriteRune(0x2400 + r) // ␀ … ␟
			marked++
		case r == 0x7F:
			b.WriteRune(0x2421) // ␡
			marked++
		case needsMark(r):
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
			marked++
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), marked
}

// needsMark reports characters that are invisible or that change how the text
// around them is laid out.
func needsMark(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F:
		return true
	case r >= 0x80 && r <= 0x9F: // C1 controls
		return true
	case r == 0x2028, r == 0x2029: // line and paragraph separators
		return true
	case r >= 0xE0000 && r <= 0xE007F: // tag characters
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF: // variation selectors
		return true
	}
	// Format characters: zero-width spaces and joiners, bidirectional
	// controls, the byte-order mark, the soft hyphen, and the like.
	return unicode.Is(unicode.Cf, r)
}
