package builtins

import (
	"fmt"
	"strings"
	"unicode"

	"gitea.com/wimwian/just-go/internal/rustre"
)

// trimStart returns s without the white space at its start, as Rust's
// str::trim_start does. Go's unicode.IsSpace and Rust's White_Space are the
// same set. It is pure.
func trimStart(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }

// trimEnd returns s without the white space at its end, as Rust's
// str::trim_end does. It is pure.
func trimEnd(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

// trimStartMatches returns s without the copies of prefix at its start, as
// Rust's str::trim_start_matches does. An empty prefix removes nothing. It is
// pure.
func trimStartMatches(s, prefix string) string {
	for prefix != "" && strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	return s
}

// trimEndMatches returns s without the copies of suffix at its end, as Rust's
// str::trim_end_matches does. An empty suffix removes nothing. It is pure.
func trimEndMatches(s, suffix string) string {
	for suffix != "" && strings.HasSuffix(s, suffix) {
		s = s[:len(s)-len(suffix)]
	}
	return s
}

// replaceRegex replaces each match in args[0] of the pattern args[1] with
// args[2], in which `$1` and `${name}` stand for a group and `$$` for a
// dollar sign, as the regex crate's Regex::replace_all does. A pattern that
// does not compile is an error worded as the crate words it. It is pure.
func replaceRegex(args []string) (string, error) {
	re, err := rustre.Compile(args[1])
	if err != nil {
		return "", err
	}
	return re.ReplaceAllString(args[0], args[2]), nil
}

// quote returns s in single quotes, with each single quote in it as `'\”`,
// which a POSIX shell reads as the text of s. It is pure.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appendEach returns the words of s, which are split at white space, each
// followed by suffix, separated by single spaces. It is pure.
func appendEach(suffix, s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = w + suffix
	}
	return strings.Join(words, " ")
}

// prependEach returns the words of s, which are split at white space, each
// preceded by prefix, separated by single spaces. It is pure.
func prependEach(prefix, s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = prefix + w
	}
	return strings.Join(words, " ")
}

// length is the number of elements of a value. Without `set lists` a value is
// a string, which is one element, even if it is empty. It is pure.
func length(string) string { return "1" }

// encodeURIComponent percent-encodes the bytes of s that JavaScript's
// encodeURIComponent does: all but the ASCII letters and digits and the
// characters -_.!~*'(). The hexadecimal digits are upper case. It is pure.
func encodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURIUnreserved(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}

// isURIUnreserved reports whether encodeURIComponent leaves the byte c as it
// is. It is pure.
func isURIUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("-_.!~*'()", c) >= 0
}

// join joins its arguments as paths, as Rust's Path::join does for the first
// two and PathBuf::push does for the others: an argument that is an absolute
// path replaces what comes before it. It is pure.
func join(args []string) (string, error) {
	return pathJoin(args[0], args[1:]...), nil
}

// fileName returns the last component of path. It is an error if the path ends
// in `..` or has no components. It is pure.
func fileName(path string) (string, error) {
	name, ok := pathFileName(path)
	if !ok {
		return "", fmt.Errorf("could not extract file name from `%s`", path)
	}
	return name, nil
}

// fileStem returns the file name of path without its extension. It is pure.
func fileStem(path string) (string, error) {
	stem, ok := pathStem(path)
	if !ok {
		return "", fmt.Errorf("could not extract file stem from `%s`", path)
	}
	return stem, nil
}

// extension returns the extension of the file name of path, without the dot,
// which may be empty. It is an error if the file name has none. It is pure.
func extension(path string) (string, error) {
	ext, ok := pathExtension(path)
	if !ok {
		return "", fmt.Errorf("could not extract extension from `%s`", path)
	}
	return ext, nil
}

// withoutExtension returns path with the extension of its file name removed.
// It is pure.
func withoutExtension(path string) (string, error) {
	parent, ok := pathParent(path)
	if !ok {
		return "", fmt.Errorf("could not extract parent from `%s`", path)
	}
	stem, ok := pathStem(path)
	if !ok {
		return "", fmt.Errorf("could not extract file stem from `%s`", path)
	}
	return pathJoin(parent, stem), nil
}
