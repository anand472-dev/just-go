package manpage

import (
	"strings"
	"testing"
)

// The page is troff for just-go: its title names the program.
func TestTextIsTheJustGoManPage(t *testing.T) {
	if !strings.Contains(Text(), ".TH just-go 1") || !strings.Contains(Text(), "\n.SH SYNOPSIS\n") {
		t.Errorf("Text() does not look like a man page for just-go")
	}
}
