package builtins

import (
	"fmt"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/sys"
)

// This file holds the builtins that name a path or read one. The pure
// functions here work on the text of a path as Rust's Path does (see
// rustpath.go). The ones that take a Host are kept apart: those that touch the
// filesystem are impure and go through Host.FS, and the rest only read the
// Host's fields.

// pathClean returns path with its `.` and `..` removed where it can be done
// without looking at the filesystem, as upstream's Clean does: a `..` removes
// the name before it, and one after a root is dropped. An empty result is `.`.
// It is pure.
func pathClean(st pathStyle, path string) string {
	var kept []component
	for _, c := range components(st, path) {
		switch {
		case c.kind == curDir:
		case c.kind != parentDir:
			kept = append(kept, c)
		case len(kept) == 0 || kept[len(kept)-1].kind == parentDir:
			kept = append(kept, c)
		case kept[len(kept)-1].kind == normal:
			kept = kept[:len(kept)-1]
		}
	}
	if len(kept) == 0 {
		return "."
	}
	return pathJoin(st, "", componentTexts(st, path, kept)...)
}

// componentTexts returns the text of each of kept, which are components of
// path: the bytes of a name or a prefix, `..`, or the separator of a root. It
// is pure.
func componentTexts(st pathStyle, path string, kept []component) []string {
	texts := make([]string, len(kept))
	for i, c := range kept {
		switch c.kind {
		case rootDir:
			texts[i] = rootText(st)
		case parentDir:
			texts[i] = ".."
		default:
			texts[i] = path[c.start:c.end]
		}
	}
	return texts
}

// rootText is the text of the root component of a path of style st. It is
// pure.
func rootText(st pathStyle) string {
	if st == windowsPath {
		return `\`
	}
	return "/"
}

// clean returns path without its `.` and `..` that can be removed lexically.
// It is pure.
func clean(path string) string { return pathClean(hostPath, path) }

// parentDirectory returns path without its last component, or `.` if that is
// empty. It is an error if the path has no parent, such as a root or an empty
// path. It is pure.
func parentDirectory(path string) (string, error) {
	parent, ok := pathParent(hostPath, path)
	switch {
	case !ok:
		return "", fmt.Errorf("could not extract parent directory from `%s`", path)
	case parent == "":
		return ".", nil
	}
	return parent, nil
}

// resolve returns path as a path from dir: path itself if it is absolute,
// else dir followed by path. It is pure.
func resolve(dir, path string) string { return pathJoin(hostPath, dir, path) }

// absolutePath returns path from the working directory of h, cleaned. It does
// no I/O.
func absolutePath(h Host, args []string) (string, error) {
	return pathClean(hostPath, resolve(h.WorkDir, args[0])), nil
}

// pathExists says, as "true" or "false", whether a file or a directory is at
// path from the working directory of h. An empty path is none. Impure: it
// looks at the filesystem through h.FS.
func pathExists(h Host, args []string) (string, error) {
	found := args[0] != "" && h.FS.Exists(resolve(h.WorkDir, args[0]))
	return fmt.Sprint(found), nil
}

// canonicalize returns the absolute path of what path names from the working
// directory of h, with symbolic links resolved. A failure is worded without
// the path, as upstream words it. Impure: it looks at the filesystem through
// h.FS.
func canonicalize(h Host, args []string) (string, error) {
	got, err := h.FS.Canonicalize(resolve(h.WorkDir, args[0]))
	if err != nil {
		return "", fmt.Errorf("I/O error canonicalizing `%s`: %s", args[0], sys.ErrorText(err))
	}
	return got, nil
}

// read returns the text of the file at path from the working directory of h.
// A file that cannot be read, or that is not UTF-8, is an error. Impure: it
// reads the file through h.FS.
func read(h Host, args []string) (string, error) {
	data, err := h.FS.ReadFile(resolve(h.WorkDir, args[0]))
	if err != nil {
		return "", readFailure(args[0], sys.ErrorText(err))
	}
	if !utf8.Valid(data) {
		return "", readFailure(args[0], "stream did not contain valid UTF-8")
	}
	return string(data), nil
}

// readFailure is the error for a file called name that could not be read, for
// the reason why. It is pure.
func readFailure(name, why string) error {
	return fmt.Errorf("I/O error reading `%s`: %s", name, why)
}

// parentOf returns the directory that holds path, which is empty for a path of
// one name and for one that has no parent. It is pure.
func parentOf(path string) string {
	parent, _ := pathParent(hostPath, path)
	return parent
}

// field makes a builtin of a function that returns one of the paths or names
// that h holds. It does no I/O.
func field(f func(Host) string) HostFunc {
	return func(h Host, _ []string) (string, error) { return f(h), nil }
}

// The paths and names of the places a function is called from. They do no I/O.
var (
	justfile                  = field(func(h Host) string { return h.Justfile })
	justfileDirectory         = field(func(h Host) string { return parentOf(h.Justfile) })
	sourceFile                = field(func(h Host) string { return h.SourceFile })
	sourceDirectory           = field(func(h Host) string { return parentOf(h.SourceFile) })
	moduleFile                = field(func(h Host) string { return h.ModuleFile })
	moduleDirectory           = field(func(h Host) string { return parentOf(h.ModuleFile) })
	modulePath                = field(func(h Host) string { return h.ModulePath })
	invocationDirectory       = field(Host.unixInvocationDir)
	invocationDirectoryNative = field(func(h Host) string { return h.InvocationDir })
)
