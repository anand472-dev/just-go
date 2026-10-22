package builtins

import "strings"

// This file holds what is particular to a Windows path, as Rust's std::path
// reads it: the prefix that may begin a path, and PathBuf::push. The prefix
// forms are `C:` (a drive), `\\server\share` (UNC), `\\.\name` (device),
// `\\?\C:` (verbatim drive), `\\?\UNC\server\share` (verbatim UNC) and
// `\\?\name` (verbatim). In a verbatim path only `\` separates, and `.` and
// `..` are names.

// isBackslash reports whether c is a backslash. It is pure.
func isBackslash(c byte) bool { return c == '\\' }

// isWindowsSep reports whether c separates the names of a Windows path. It is
// pure.
func isWindowsSep(c byte) bool { return c == '/' || c == '\\' }

// isDrive reports whether s begins with a drive letter and a colon. It is
// pure.
func isDrive(s string) bool {
	return len(s) >= 2 && s[1] == ':' && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z')
}

// head returns the bytes of s before the first byte for which isSep is true,
// and all of s if there is none. It is pure.
func head(s string, isSep func(byte) bool) string {
	for i := 0; i < len(s); i++ {
		if isSep(s[i]) {
			return s[:i]
		}
	}
	return s
}

// parsePrefix returns the length in bytes of the prefix that begins path, which
// is zero if there is none, and whether the prefix is verbatim. It is pure.
func parsePrefix(path string) (n int, verbatim bool) {
	switch {
	case strings.HasPrefix(path, `\\?\`):
		return parseVerbatim(path[4:]), true
	case strings.HasPrefix(path, `\\.\`):
		return 4 + len(head(path[4:], isWindowsSep)), false
	case strings.HasPrefix(path, `\\`):
		return parseUNC(path[2:]), false
	case isDrive(path):
		return 2, false
	}
	return 0, false
}

// parseVerbatim returns the length of the verbatim prefix whose text after
// `\\?\` is rest. It is pure.
func parseVerbatim(rest string) int {
	if strings.HasPrefix(rest, `UNC\`) {
		server := head(rest[4:], isBackslash)
		share := ""
		if len(server)+4 < len(rest) {
			share = head(rest[5+len(server):], isBackslash)
		}
		n := 8 + len(server)
		if share != "" {
			n += 1 + len(share)
		}
		return n
	}
	name := head(rest, isBackslash)
	if len(name) == 2 && isDrive(name) {
		return 6
	}
	return 4 + len(name)
}

// parseUNC returns the length of the UNC prefix whose text after `\\` is rest,
// which is zero unless a server and a share, neither empty, follow. It is pure.
func parseUNC(rest string) int {
	server := head(rest, isWindowsSep)
	if len(server) == len(rest) {
		return 0
	}
	share := head(rest[len(server)+1:], isWindowsSep)
	if server == "" || share == "" {
		return 0
	}
	return 2 + len(server) + 1 + len(share)
}

// pushWindows is PathBuf::push on Windows. A path with a prefix replaces
// base. A path with a root and no prefix keeps the prefix of base. Any other
// path is added after a separator, unless base is only a drive. A verbatim
// base is joined name by name. It is pure.
func pushWindows(base, p string) string {
	if n, _ := parsePrefix(p); n > 0 {
		return p
	}
	bn, verbatim := parsePrefix(base)
	switch {
	case verbatim && p != "":
		return pushVerbatim(base, p)
	case p != "" && isWindowsSep(p[0]):
		return base[:bn] + p
	case endsApart(base, bn):
		return base + p
	}
	return base + `\` + p
}

// endsApart reports whether a name can follow base, whose prefix is bn bytes
// long, without a separator: base is empty, ends in a separator, or is only a
// drive. It is pure.
func endsApart(base string, bn int) bool {
	return base == "" || isWindowsSep(base[len(base)-1]) || (bn == 2 && len(base) == 2)
}

// piece is a component of a path together with its text.
type piece struct {
	kind kind
	text string
}

// pieces returns the components of path, read as a Windows path, with their
// text. It is pure.
func pieces(path string) []piece {
	var out []piece
	for _, c := range components(windowsPath, path) {
		out = append(out, piece{c.kind, path[c.start:c.end]})
	}
	return out
}

// pushVerbatim joins p to the verbatim path base, dropping `.` and letting
// `..` remove a name, as PathBuf::push does. It is pure.
func pushVerbatim(base, p string) string {
	buf := pieces(base)
	for _, c := range pieces(p) {
		switch c.kind {
		case rootDir:
			buf = append(buf[:1], piece{rootDir, `\`})
		case parentDir:
			buf = popName(buf)
		case normal:
			buf = append(buf, c)
		}
	}
	return render(buf)
}

// popName removes the last piece of buf if it is a name. It is pure.
func popName(buf []piece) []piece {
	if buf[len(buf)-1].kind == normal {
		return buf[:len(buf)-1]
	}
	return buf
}

// render writes pieces as a verbatim path: a backslash goes between two
// pieces, except next to the root. It is pure.
func render(buf []piece) string {
	var b strings.Builder
	for i, c := range buf {
		if i > 0 && c.kind != rootDir && buf[i-1].kind != rootDir {
			b.WriteByte('\\')
		}
		b.WriteString(c.text)
	}
	return b.String()
}
