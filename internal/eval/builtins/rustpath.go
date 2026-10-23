package builtins

import (
	"runtime"
	"strings"
)

// This file reads a path as Rust's std::path::Path does, which is how upstream
// reads the path of a string. A path is a sequence of components: an optional
// prefix (Windows only), an optional root, a leading `.`, and the names between
// separators. Repeated separators and a trailing separator mean nothing, and a
// `.` that is not first is dropped. Rust reads a path by the system it was
// built for, so pathStyle names that system. On Windows both `/` and `\` are
// separators and a path may begin with a prefix (see winpath.go).

// pathStyle is the system whose rules read a path.
type pathStyle int

const (
	// unixPath reads `/` as the only separator.
	unixPath pathStyle = iota
	// windowsPath reads `/` and `\` as separators and knows prefixes.
	windowsPath
)

// hostPath is the style of the system this program runs on, as Rust's is the
// system it was built for. It is a build constant: it is set once and the
// builtins only read it.
var hostPath = hostPathStyle(runtime.GOOS)

// hostPathStyle returns the path style of the operating system goos. It is
// pure.
func hostPathStyle(goos string) pathStyle {
	if goos == "windows" {
		return windowsPath
	}
	return unixPath
}

// sepFunc returns the test for a separator byte in a path of style st. In a
// verbatim Windows path only `\` separates. It is pure.
func sepFunc(st pathStyle, verbatim bool) func(byte) bool {
	switch {
	case st == unixPath:
		return func(c byte) bool { return c == '/' }
	case verbatim:
		return func(c byte) bool { return c == '\\' }
	}
	return func(c byte) bool { return c == '/' || c == '\\' }
}

// kind is the kind of a component of a path.
type kind int

const (
	// prefixDir is the Windows prefix: a drive or a share.
	prefixDir kind = iota
	// rootDir is the leading separator of an absolute path.
	rootDir
	// curDir is a leading `.`.
	curDir
	// parentDir is a `..`.
	parentDir
	// normal is any other name.
	normal
)

// component is a component of a path, and where it lies in the path, as the
// bytes from start up to end.
type component struct {
	kind       kind
	start, end int
}

// components returns the components of path in order, read as style st does.
// It is pure.
func components(st pathStyle, path string) []component {
	var out []component
	n, verbatim := 0, false
	if st == windowsPath {
		n, verbatim = parsePrefix(path)
	}
	if n > 0 {
		out = append(out, component{prefixDir, 0, n})
	}
	isSep := sepFunc(st, verbatim)
	i, curDirAt := n, n
	if i < len(path) && isSep(path[i]) {
		out = append(out, component{rootDir, i, i + 1})
		i, curDirAt = i+1, -1
	}
	return appendNames(out, path, i, curDirAt, verbatim, isSep)
}

// appendNames appends to out the components of the names in path from byte i
// on. A `.` at byte curDirAt is a component. It is pure.
func appendNames(out []component, path string, i, curDirAt int, verbatim bool, isSep func(byte) bool) []component {
	for i < len(path) {
		if isSep(path[i]) {
			i++
			continue
		}
		end := i
		for end < len(path) && !isSep(path[end]) {
			end++
		}
		if c, ok := classify(path[i:end], i == curDirAt, verbatim); ok {
			c.start, c.end = i, end
			out = append(out, c)
		}
		i = end
	}
	return out
}

// classify says what kind of component the name between two separators is.
// A `.` is a component only at the very start of the path, where first is
// true, or in a verbatim path; elsewhere it is dropped, and ok is false. It is
// pure.
func classify(name string, first, verbatim bool) (c component, ok bool) {
	switch {
	case name == ".":
		return component{kind: curDir}, first || verbatim
	case name == "..":
		return component{kind: parentDir}, true
	}
	return component{kind: normal}, true
}

// pathFileName returns the last component of path if it is a name. It is
// pure.
func pathFileName(st pathStyle, path string) (string, bool) {
	cs := components(st, path)
	if len(cs) == 0 || cs[len(cs)-1].kind != normal {
		return "", false
	}
	last := cs[len(cs)-1]
	return path[last.start:last.end], true
}

// isAnchor reports whether k is the kind of a prefix or a root, which have no
// parent. It is pure.
func isAnchor(k kind) bool { return k == prefixDir || k == rootDir }

// pathParent returns path without its last component, and without the
// separators and the `.` that come before the component. It is empty for a
// path of one name. ok is false for a path that is a root or has no
// components. It is pure.
func pathParent(st pathStyle, path string) (string, bool) {
	cs := components(st, path)
	if len(cs) == 0 || isAnchor(cs[len(cs)-1].kind) {
		return "", false
	}
	if len(cs) == 1 {
		return "", true
	}
	return path[:cs[len(cs)-2].end], true
}

// pathJoin appends each of rest to base as PathBuf::push does: a separator is
// added if base is not empty and does not end in one, and an absolute path
// replaces everything before it. It is pure.
func pathJoin(st pathStyle, base string, rest ...string) string {
	for _, p := range rest {
		if st == windowsPath {
			base = pushWindows(base, p)
		} else {
			base = pushUnix(base, p)
		}
	}
	return base
}

// pushUnix is PathBuf::push on Unix. It is pure.
func pushUnix(base, p string) string {
	switch {
	case strings.HasPrefix(p, "/"):
		return p
	case base != "" && !strings.HasSuffix(base, "/"):
		return base + "/" + p
	}
	return base + p
}

// splitDot splits the name of a file at its last dot, as Rust's Path does to
// find a stem and an extension: before is the part before the dot, after is
// the part after, and either is absent if the name does not have it. A name
// that starts with its only dot has no part before it and so no extension, and
// `..` has no extension. It is pure.
func splitDot(name string) (before, after string, hasBefore, hasAfter bool) {
	dot := strings.LastIndexByte(name, '.')
	switch {
	case name == "..":
		return name, "", true, false
	case dot < 0:
		return "", name, false, true
	case dot == 0:
		return name, "", true, false
	}
	return name[:dot], name[dot+1:], true, true
}

// stemOf returns the name of a file without its extension. It is pure.
func stemOf(name string) string {
	before, after, hasBefore, _ := splitDot(name)
	if hasBefore {
		return before
	}
	return after
}

// extensionOf returns the extension of the name of a file. It is pure.
func extensionOf(name string) (string, bool) {
	_, after, hasBefore, hasAfter := splitDot(name)
	if !hasBefore || !hasAfter {
		return "", false
	}
	return after, true
}

// pathStem returns the stem of the file name of path. It is pure.
func pathStem(st pathStyle, path string) (string, bool) {
	name, ok := pathFileName(st, path)
	if !ok {
		return "", false
	}
	return stemOf(name), true
}

// pathExtension returns the extension of the file name of path. It is pure.
func pathExtension(st pathStyle, path string) (string, bool) {
	name, ok := pathFileName(st, path)
	if !ok {
		return "", false
	}
	return extensionOf(name)
}
