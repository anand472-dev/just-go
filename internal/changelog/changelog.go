// Package changelog holds the changelog that `--changelog` prints: the changelog
// of the upstream release this port follows, embedded at build time.
package changelog

import _ "embed"

//go:embed CHANGELOG.md
var text string

// Text is the changelog, in Markdown.
func Text() string { return text }
