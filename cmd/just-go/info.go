package main

import (
	"fmt"
	"io"

	"gitea.com/wimwian/just-go/internal/changelog"
	"gitea.com/wimwian/just-go/internal/manpage"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/request"
)

// answer prints what --version, --man, --changelog or --request asks for, and
// reports done when one of them was given; the run then stops without loading a
// justfile. --version names this program with the upstream version it follows.
// Impure: it writes to stdout, and reads the environment for --request.
func answer(cfg config, env []string, stdout, stderr io.Writer) (status int, done bool) {
	switch {
	case cfg.version:
		_, _ = fmt.Fprintf(stdout, "just-go %s\n", parser.UpstreamVersion)
	case cfg.man:
		_, _ = io.WriteString(stdout, manpage.Text())
	case cfg.changelog:
		_, _ = io.WriteString(stdout, changelog.Text())
	case cfg.requestGiven:
		out, err := request.Answer(cfg.request, func(name string) (string, bool) { return envValue(env, name) })
		if err != nil {
			return fail(stderr, cfg.quiet, err), true
		}
		_, _ = io.WriteString(stdout, out)
	default:
		return 0, false
	}
	return 0, true
}
