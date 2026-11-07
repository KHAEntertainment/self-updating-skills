package redirect

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Canon puts text in the form evidence is compared in: Unicode NFC (so
// canonically equivalent spellings compare equal), Markdown backslash
// escapes removed (`old\_key` documents `old_key`), and every whitespace
// run collapsed to one space (so line-wrapping cannot hide a mention).
func Canon(s string) string {
	return Normalize(unescapeMarkdown(norm.NFC.String(s)))
}

// unescapeMarkdown drops a backslash that escapes ASCII punctuation, the
// CommonMark backslash-escape rule.
func unescapeMarkdown(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isASCIIPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// identChar is a character that continues an identifier: a neighbour of
// this kind means the match is part of a longer identifier.
func identChar(r rune) bool {
	return r == '_' || r == '.' || r == '-' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

// wordChar is identChar without the punctuation that may also end a
// sentence or start a path.
func wordChar(r rune) bool { return identChar(r) && r != '.' && r != '-' }

// ContainsIdentifier reports whether text mentions ident as a whole: after
// Canon on both sides, an occurrence whose neighbours are not identifier
// characters ([A-Za-z0-9_.-]). A neighbouring "." or "-" still counts as a
// boundary when the character beyond it is not a word character, so
// "Use api.v2." mentions api.v2 while "api.v20" and "x.api.v2" do not.
func ContainsIdentifier(text, ident string) bool {
	id := Canon(ident)
	if id == "" {
		return false
	}
	t := Canon(text)
	for from := 0; from <= len(t); {
		k := strings.Index(t[from:], id)
		if k < 0 {
			return false
		}
		start, end := from+k, from+k+len(id)
		if boundaryBefore(t, start) && boundaryAfter(t, end) {
			return true
		}
		_, size := utf8.DecodeRuneInString(t[start:])
		from = start + size
	}
	return false
}

func boundaryBefore(t string, i int) bool {
	if i == 0 {
		return true
	}
	r, n := utf8.DecodeLastRuneInString(t[:i])
	if !identChar(r) {
		return true
	}
	if r == '.' || r == '-' {
		if i-n == 0 {
			return true
		}
		r2, _ := utf8.DecodeLastRuneInString(t[:i-n])
		return !wordChar(r2)
	}
	return false
}

func boundaryAfter(t string, i int) bool {
	if i >= len(t) {
		return true
	}
	r, n := utf8.DecodeRuneInString(t[i:])
	if !identChar(r) {
		return true
	}
	if r == '.' || r == '-' {
		if i+n >= len(t) {
			return true
		}
		r2, _ := utf8.DecodeRuneInString(t[i+n:])
		return !wordChar(r2)
	}
	return false
}
