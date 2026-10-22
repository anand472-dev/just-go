package builtins

import "strings"

// This file reads a path as Rust's std::path::Path does on Unix, which is how
// upstream reads the path of a string. A path is a sequence of components:
// an optional root, a leading `.`, and the names between separators. Repeated
// separators and a trailing separator mean nothing, and a `.` that is not
// first is dropped. Windows reads `\` as a separator and a drive as a prefix;
// that is not done here.

// kind is the kind of a component of a path.
type kind int

const (
	// rootDir is the leading separator of an absolute path.
	rootDir kind = iota
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

// components returns the components of path in order. It is pure.
func components(path string) []component {
	var out []component
	i := 0
	if strings.HasPrefix(path, "/") {
		out = append(out, component{rootDir, 0, 1})
		i = 1
	}
	for i < len(path) {
		if path[i] == '/' {
			i++
			continue
		}
		end := strings.IndexByte(path[i:], '/')
		if end < 0 {
			end = len(path)
		} else {
			end += i
		}
		if c, ok := classify(path[i:end], i == 0); ok {
			c.start, c.end = i, end
			out = append(out, c)
		}
		i = end
	}
	return out
}

// classify says what kind of component the name between two separators is.
// A `.` is a component only at the very start of the path, where first is
// true; elsewhere it is dropped, and ok is false. It is pure.
func classify(name string, first bool) (c component, ok bool) {
	switch {
	case name == ".":
		return component{kind: curDir}, first
	case name == "..":
		return component{kind: parentDir}, true
	}
	return component{kind: normal}, true
}

// pathFileName returns the last component of path if it is a name. It is
// pure.
func pathFileName(path string) (string, bool) {
	cs := components(path)
	if len(cs) == 0 || cs[len(cs)-1].kind != normal {
		return "", false
	}
	last := cs[len(cs)-1]
	return path[last.start:last.end], true
}

// pathParent returns path without its last component, and without the
// separators and the `.` that come before the component. It is empty for a
// path of one name. ok is false for a path that is a root or has no
// components. It is pure.
func pathParent(path string) (string, bool) {
	cs := components(path)
	if len(cs) == 0 || cs[len(cs)-1].kind == rootDir {
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
func pathJoin(base string, rest ...string) string {
	for _, p := range rest {
		switch {
		case strings.HasPrefix(p, "/"):
			base = p
			continue
		case base != "" && !strings.HasSuffix(base, "/"):
			base += "/"
		}
		base += p
	}
	return base
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
func pathStem(path string) (string, bool) {
	name, ok := pathFileName(path)
	if !ok {
		return "", false
	}
	return stemOf(name), true
}

// pathExtension returns the extension of the file name of path. It is pure.
func pathExtension(path string) (string, bool) {
	name, ok := pathFileName(path)
	if !ok {
		return "", false
	}
	return extensionOf(name)
}
