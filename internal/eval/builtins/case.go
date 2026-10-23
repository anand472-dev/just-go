package builtins

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file ports the case conversions of upstream. The case functions
// (kebabcase and the others) are the heck crate's, which splits a string into
// words and writes them with a separator; lowercase and uppercase are Rust's
// str::to_lowercase and str::to_uppercase. Rust reads a character by its
// Unicode properties and maps its case with the full mappings, in which one
// character can become several. Go's unicode package has the properties and
// the one-to-one mappings, so the rest is here.

// isAlphanumeric is Rust's char::is_alphanumeric: the Alphabetic property or
// a number. It is pure.
func isAlphanumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// isLowercase is Rust's char::is_lowercase: the Lowercase property. It is pure.
func isLowercase(r rune) bool {
	return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r)
}

// isUppercase is Rust's char::is_uppercase: the Uppercase property. It is pure.
func isUppercase(r rune) bool {
	return unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r)
}

// isCased is the Cased property: a character that has a case. It is pure.
func isCased(r rune) bool { return isLowercase(r) || isUppercase(r) || unicode.IsTitle(r) }

// isCaseIgnorable is the Case_Ignorable property: a character that does not
// stop the search for a cased letter around a capital sigma. It is the marks,
// the format characters, the modifier letters and symbols, and the
// punctuation that Unicode's word-break rules read as part of a word. It is
// pure.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027,
		0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// upperRune is Rust's char::to_uppercase: the full uppercase mapping of r. It
// is pure.
func upperRune(r rune) string {
	if r >= utf8.RuneSelf {
		if s, ok := specialUpper[r]; ok {
			return s
		}
	}
	return string(unicode.ToUpper(r))
}

// lowerRune is Rust's char::to_lowercase: the full lowercase mapping of r,
// which is more than one character only for U+0130. It is pure.
func lowerRune(r rune) string {
	if r == 0x130 {
		return "i̇"
	}
	return string(unicode.ToLower(r))
}

// uppercase is Rust's str::to_uppercase. It is pure.
func uppercase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteString(upperRune(r))
	}
	return b.String()
}

// lowercase is Rust's str::to_lowercase, in which a capital sigma is the final
// form of the small letter at the end of a word. It is pure.
func lowercase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		if r == 'Σ' {
			b.WriteRune(sigma(s, i))
		} else {
			b.WriteString(lowerRune(r))
		}
	}
	return b.String()
}

// sigma returns the small letter for the capital sigma at index i of s: the
// final form if a cased letter comes before it and none after it, not
// counting case-ignorable characters. It is pure.
func sigma(s string, i int) rune {
	if casedBefore(s[:i]) && !casedAfter(s[i+len("Σ"):]) {
		return 'ς'
	}
	return 'σ'
}

// casedBefore reports whether the last character of s that is not
// case-ignorable is cased. It is pure.
func casedBefore(s string) bool {
	for s != "" {
		r, n := utf8.DecodeLastRuneInString(s)
		if !isCaseIgnorable(r) {
			return isCased(r)
		}
		s = s[:len(s)-n]
	}
	return false
}

// casedAfter reports whether the first character of s that is not
// case-ignorable is cased. It is pure.
func casedAfter(s string) bool {
	for s != "" {
		r, n := utf8.DecodeRuneInString(s)
		if !isCaseIgnorable(r) {
			return isCased(r)
		}
		s = s[n:]
	}
	return false
}

// capitalize is the capitalize function of upstream: the first character in
// upper case and each other in lower case, one by one. It is pure.
func capitalize(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i == 0 {
			b.WriteString(upperRune(r))
		} else {
			b.WriteString(lowerRune(r))
		}
	}
	return b.String()
}

// mode is what the scan of a word of heck knows about the characters before:
// whether the last one that has a case is lower or upper.
type mode int

const (
	// boundary is a word with no cased character yet.
	boundary mode = iota
	lower
	upper
)

// words splits s into words as heck does: at each character that is not
// alphanumeric, and inside what is left between a lower case letter and an
// upper case one, and before the last of a run of upper case letters that a
// lower case letter follows. It is pure.
func words(s string) []string {
	var out []string
	for _, chunk := range strings.FieldsFunc(s, func(r rune) bool { return !isAlphanumeric(r) }) {
		out = append(out, splitChunk(chunk)...)
	}
	return out
}

// next returns the mode after the character c, given the mode before it. It
// is pure.
func next(m mode, c rune) mode {
	switch {
	case isLowercase(c):
		return lower
	case isUppercase(c):
		return upper
	}
	return m
}

// splitChunk splits a run of alphanumeric characters into the words of heck.
// It is pure.
func splitChunk(chunk string) []string {
	var out []string
	start, m := 0, boundary
	for i, c := range chunk {
		after := i + utf8.RuneLen(c)
		if after == len(chunk) {
			break
		}
		following, _ := utf8.DecodeRuneInString(chunk[after:])
		switch nm := next(m, c); {
		case nm == lower && isUppercase(following):
			out = append(out, chunk[start:after])
			start, m = after, boundary
		case m == upper && isUppercase(c) && isLowercase(following):
			out = append(out, chunk[start:i])
			start, m = i, boundary
		default:
			m = nm
		}
	}
	return append(out, chunk[start:])
}

// lowerWord is heck's lowercase of a word: a capital sigma at the end of the
// word is the final form. It is pure.
func lowerWord(w string) string {
	var b strings.Builder
	for i, r := range w {
		if r == 'Σ' && i+len("Σ") == len(w) {
			b.WriteRune('ς')
		} else {
			b.WriteString(lowerRune(r))
		}
	}
	return b.String()
}

// capitalWord is heck's capitalize of a word, which is not empty: the first
// character in upper case and the rest as lowerWord has it. It is pure.
func capitalWord(w string) string {
	r, n := utf8.DecodeRuneInString(w)
	return upperRune(r) + lowerWord(w[n:])
}

// convert writes the words of s, the first by first and the others by rest,
// with sep between them. It is pure.
func convert(s, sep string, first, rest func(string) string) string {
	ws := words(s)
	for i, w := range ws {
		if i == 0 {
			ws[i] = first(w)
		} else {
			ws[i] = rest(w)
		}
	}
	return strings.Join(ws, sep)
}

// kebabcase is `foo-bar`. It is pure.
func kebabcase(s string) string { return convert(s, "-", lowerWord, lowerWord) }

// snakecase is `foo_bar`. It is pure.
func snakecase(s string) string { return convert(s, "_", lowerWord, lowerWord) }

// shoutykebabcase is `FOO-BAR`. It is pure.
func shoutykebabcase(s string) string { return convert(s, "-", uppercase, uppercase) }

// shoutysnakecase is `FOO_BAR`. It is pure.
func shoutysnakecase(s string) string { return convert(s, "_", uppercase, uppercase) }

// lowercamelcase is `fooBar`. It is pure.
func lowercamelcase(s string) string { return convert(s, "", lowerWord, capitalWord) }

// uppercamelcase is `FooBar`. It is pure.
func uppercamelcase(s string) string { return convert(s, "", capitalWord, capitalWord) }

// titlecase is `Foo Bar`. It is pure.
func titlecase(s string) string { return convert(s, " ", capitalWord, capitalWord) }
