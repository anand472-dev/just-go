package loader

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// A Markdown file keeps only the lines of its ```just fences, and its other
// lines become empty, so that a line number is the Markdown's: here the seven
// lines before the fence are empty, then the recipe, then five empty lines.
func TestTangleKeepsTheJustFencesAtTheirLines(t *testing.T) {
	src := "intro\n\n```sh\necho no\n```\n\n```just\na:\n  echo a\n```\n\n```\nc:\n```\n"
	want := strings.Repeat("\n", 7) + "a:\n  echo a\n" + strings.Repeat("\n", 5)
	if got := string(tangle("notes.md", []byte(src))); got != want {
		t.Errorf("tangle = %q; want %q", got, want)
	}
}

// A file whose name does not end in .md is not tangled, whatever it holds.
func TestTangleLeavesOtherFilesAlone(t *testing.T) {
	src := []byte("```just\na:\n```\n")
	if got := tangle("justfile", src); string(got) != string(src) {
		t.Errorf("tangle of a plain file = %q", got)
	}
}

// A Markdown justfile is loaded from its tangled source, and its recipes are
// found.
func TestLoadTanglesAMarkdownJustfile(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/notes.md": "# Title\n\n```just\nfoo:\n  echo foo\n```\n"}
	jf, err := Load(fsys, "/w/notes.md")
	if err != nil || len(jf.Recipes) != 1 || jf.Recipes[0].Name != "foo" {
		t.Errorf("Load = %+v, %v; want the recipe foo", jf, err)
	}
}

// The errors of a Markdown justfile name it `justfile`, not the Markdown file.
func TestLoadNamesAMarkdownJustfileItsErrorsAsJustfile(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/notes.md": "```just\ngarbage[\n```\n"}
	_, err := Load(fsys, "/w/notes.md")
	var de *diag.Error
	if !errors.As(err, &de) || de.File != "justfile" || de.Line != 2 {
		t.Errorf("error = %+v; want it in justfile at line 2", err)
	}
}

// A fence of four backticks holds a ```just fence as text, which is not kept.
func TestTangleKeepsALongerFenceWhole(t *testing.T) {
	src := "````\n```just\nnot:\n```\n````\n\n```just\nreal:\n  echo\n```\n"
	want := strings.Repeat("\n", 7) + "real:\n  echo\n" + "\n" // seven lines before the fence
	if got := string(tangle("notes.md", []byte(src))); got != want {
		t.Errorf("tangle = %q; want %q", got, want)
	}
}

// A private module's recipes are private, as the attribute on the module says.
func TestPrivateModuleMakesItsRecipesPrivate(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/justfile": "[private]\nmod foo\n\nbar:\n", "/w/foo.just": "baz:\n  echo\n"}
	jf, err := Load(fsys, "/w/justfile")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, r := range jf.Recipes {
		if r.Name == "foo::baz" && !r.Private() {
			t.Errorf("foo::baz is public, but its module is private")
		}
		if r.Name == "bar" && r.Private() {
			t.Errorf("bar is private, but the root has no private attribute")
		}
	}
}
