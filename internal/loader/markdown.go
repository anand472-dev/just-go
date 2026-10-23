package loader

import (
	"bytes"
	"errors"
	"strings"

	"gitea.com/wimwian/just-go/internal/diag"
)

// A justfile named like a Markdown file (ending in .md) is read from its
// ```just fences alone, as upstream does: the text of the other lines, and of
// the other fences, is not part of the justfile.

// isMarkdown reports whether the file at path is a Markdown justfile, by its
// name. It is pure.
func isMarkdown(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".md")
}

// renameFile gives the errors that name the file at path the name shown instead:
// a Markdown justfile's errors are shown as `justfile`, as upstream does. Other
// errors are returned as they are. It is pure apart from updating err.
func renameFile(err error, path, shown string) error {
	var de *diag.Error
	if errors.As(err, &de) && de.File == path {
		de.File = shown
	}
	return err
}

// tangle returns the justfile source of the file at path. A file whose name
// does not end in .md is returned as it is. Otherwise each line of a ```just
// fence is kept, and every other line becomes empty, so that a line number is
// the Markdown file's. A fence of more backticks holds shorter ones as text, and
// it closes only on a line of at least as many. It is pure.
func tangle(path string, src []byte) []byte {
	if !isMarkdown(path) {
		return src
	}
	var out bytes.Buffer
	var f fence
	for _, line := range strings.SplitAfter(string(src), "\n") {
		if line == "" {
			continue // after the last line break there is no line
		}
		out.WriteString(f.line(line))
	}
	return out.Bytes()
}

// fence is the state of the reader of a Markdown file: the backticks of the
// fence it is in, open, and whether that fence is a ```just one.
type fence struct {
	open string
	just bool
}

// line is what the justfile keeps of one line of the Markdown: the line itself
// inside a ```just fence, and an empty line, with its break, anywhere else. It
// updates f for the fences the line opens or closes.
func (f *fence) line(line string) string {
	ticks, info := fenceOf(strings.TrimRight(line, "\r\n"))
	switch {
	case f.open == "" && ticks != "":
		f.open, f.just = ticks, info == "just"
	case f.open != "" && ticks != "" && info == "" && len(ticks) >= len(f.open):
		f.open = ""
	case f.open != "" && f.just:
		return line
	}
	return "\n"
}

// fenceOf reports the backticks that open or close a code fence on the line
// text, and the info string after them. A line with fewer than three backticks
// is not a fence, and its ticks are "". It is pure.
func fenceOf(text string) (ticks, info string) {
	t := strings.TrimSpace(text)
	n := len(t) - len(strings.TrimLeft(t, "`"))
	if n < 3 {
		return "", ""
	}
	return t[:n], strings.TrimSpace(t[n:])
}
