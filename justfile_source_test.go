package just

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// Options.Source is a justfile given as text, such as one read from standard
// input: it is loaded as written, and the name is the one its errors use.
func TestLoadFromSourceUsesItsText(t *testing.T) {
	jf, err := Load(Options{Source: []byte("foo:\n  echo foo\n"), SourceName: "<stdin>", FS: sysfake.FakeFS{}})
	if err != nil || len(jf.Recipes()) != 1 || jf.Recipes()[0].Name != "foo" {
		t.Errorf("Load from Source = %v, %v; want the recipe foo", jf, err)
	}
}

// A justfile path with no parent, such as the root directory, is refused as
// upstream refuses it.
func TestLoadRefusesAJustfilePathWithNoParent(t *testing.T) {
	_, err := Load(Options{Justfile: "/", FS: sysfake.FakeFS{}})
	if err == nil || err.Error() != "justfile path had no parent: /" {
		t.Errorf("Load(/) error = %v", err)
	}
}

// A justfile from text that does not parse is an error, as a file's is.
func TestLoadFromSourceRefusesTextThatDoesNotParse(t *testing.T) {
	if _, err := Load(Options{Source: []byte("x :=\n"), SourceName: "justfile", FS: sysfake.FakeFS{}}); err == nil {
		t.Errorf("Load of bad text gave no error")
	}
}

// A justfile from text that uses an unstable feature is refused without --unstable.
func TestLoadFromSourceRefusesUnstableWithoutTheFlag(t *testing.T) {
	src := []byte("set lists\na:\n  echo\n")
	if _, err := Load(Options{Source: src, SourceName: "justfile", FS: sysfake.FakeFS{}}); err == nil {
		t.Errorf("Load of an unstable justfile without --unstable gave no error")
	}
}
