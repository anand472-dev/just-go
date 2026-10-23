package just

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/parser"
)

func TestDumpReturnsTheSourceOfTheJustfile(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("a := 'x'\nfoo:\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	j := &Justfile{tree: jf}
	if got := j.Dump("  "); got != "a := 'x'\nfoo:\n  echo\n" {
		t.Errorf("Dump = %q", got)
	}
}
