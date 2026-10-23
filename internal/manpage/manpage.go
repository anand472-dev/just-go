// Package manpage holds the man page that `--man` prints. The page is troff
// text embedded at build time, so the output does not depend on the machine.
package manpage

import _ "embed"

//go:embed just-go.1
var text string

// Text is the man page, in troff.
func Text() string { return text }
