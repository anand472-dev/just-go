package main

import (
	"io"
	"os"
	"strings"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/ansi"
)

// commandCodes are the --command-color names and the SGR foreground code each
// stands for.
var commandCodes = map[string]string{
	"black": "30", "red": "31", "green": "32", "yellow": "33",
	"blue": "34", "purple": "35", "cyan": "36",
}

// colorOf reports whether the output written to w is styled: --color always
// styles it, never does not, and auto, the default, styles a terminal only.
// Impure: auto asks w whether it is a terminal.
func colorOf(cfg config, w io.Writer) bool {
	switch cfg.color {
	case "always":
		return true
	case "never":
		return false
	}
	return terminal(w)
}

// terminal reports whether w is a character device, as a terminal is. Impure:
// it asks the operating system about an *os.File.
func terminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// styledSignature is the signature of a recipe in a listing, styled when on: a
// parameter's name in cyan, its `+` or `*` in purple, and its default in green.
// Without styling it is the same text as signature. It is pure.
func styledSignature(info just.RecipeInfo, on bool) string {
	words := []string{info.Name}
	for i, name := range info.Params {
		word := ansi.Paint(on, "36", name)
		if i < len(info.Kinds) && info.Kinds[i] != "" {
			word = ansi.Paint(on, "35", info.Kinds[i]) + word
		}
		if i < len(info.Defaults) && info.Defaults[i] != "" {
			word += "=" + ansi.Paint(on, "32", info.Defaults[i])
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

// styledDoc is a doc comment in a listing, styled when on: its text in blue, and
// each backtick span, backticks included, in cyan. A span with no closing
// backtick runs to the end of the comment. It is pure.
func styledDoc(doc string, on bool) string {
	if !on {
		return doc
	}
	var b strings.Builder
	for doc != "" {
		if doc[0] == '`' {
			span := doc
			if end := strings.IndexByte(doc[1:], '`'); end >= 0 {
				span = doc[:end+2]
			}
			b.WriteString(ansi.Paint(true, "36", span))
			doc = doc[len(span):]
			continue
		}
		text := doc
		if i := strings.IndexByte(doc, '`'); i >= 0 {
			text = doc[:i]
		}
		b.WriteString(ansi.Paint(true, "34", text))
		doc = doc[len(text):]
	}
	return b.String()
}
