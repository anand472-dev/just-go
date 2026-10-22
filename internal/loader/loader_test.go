package loader

import (
	"errors"
	"io/fs"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestFindWalksUpward(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "a:\n\techo a\n"}
	got, err := Find(fsys, "/proj/sub/deep")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != "/proj/justfile" {
		t.Errorf("Find = %q, want /proj/justfile", got)
	}
}

func TestFindPrefersJustfileOverDotJustfile(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "", "/proj/.justfile": ""}
	got, err := Find(fsys, "/proj")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != "/proj/justfile" {
		t.Errorf("Find = %q, want /proj/justfile", got)
	}
}

func TestFindFallsBackToDotJustfile(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/.justfile": ""}
	got, err := Find(fsys, "/proj/x")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != "/proj/.justfile" {
		t.Errorf("Find = %q, want /proj/.justfile", got)
	}
}

func TestFindNearestWins(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "", "/proj/sub/.justfile": ""}
	got, err := Find(fsys, "/proj/sub")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != "/proj/sub/.justfile" {
		t.Errorf("Find = %q, want /proj/sub/.justfile", got)
	}
}

func TestFindNoJustfileIsError(t *testing.T) {
	for _, start := range []string{"/", "/a/b", "rel/dir", "", "."} {
		got, err := Find(sysfake.FakeFS{}, start)
		if !errors.Is(err, ErrNoJustfile) {
			t.Errorf("Find(%q) error = %v, want ErrNoJustfile", start, err)
		}
		if got != "" {
			t.Errorf("Find(%q) path = %q, want empty", start, got)
		}
	}
	// The CLI prints "error: " + this text; the corpus case search::not_found
	// expects "error: no justfile found".
	if ErrNoJustfile.Error() != "no justfile found" {
		t.Errorf("ErrNoJustfile text = %q", ErrNoJustfile.Error())
	}
}

func TestFindRelativeStart(t *testing.T) {
	fsys := sysfake.FakeFS{"justfile": ""}
	got, err := Find(fsys, "a/b")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != "justfile" {
		t.Errorf("Find = %q, want justfile", got)
	}
}

// errFS fails every call, to exercise the filesystem-error paths.
type errFS struct{ err error }

func (e errFS) ReadFile(string) ([]byte, error) { return nil, e.err }
func (e errFS) IsFile(string) (bool, error)     { return false, e.err }

func TestFindFilesystemErrorIsNotNoJustfile(t *testing.T) {
	boom := errors.New("boom")
	_, err := Find(errFS{boom}, "/proj")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if errors.Is(err, ErrNoJustfile) {
		t.Errorf("filesystem failure must not look like ErrNoJustfile")
	}
}

func TestLoadReportsParseErrorWithFilePath(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "a b c\n"}
	jf, err := Load(fsys, "/proj/justfile")
	if jf != nil {
		t.Errorf("Load returned a tree for bad syntax")
	}
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *diag.Error", err, err)
	}
	if de.File != "/proj/justfile" {
		t.Errorf("File = %q, want /proj/justfile", de.File)
	}
	if de.Kind != diag.KindParse {
		t.Errorf("Kind = %v, want KindParse", de.Kind)
	}
}

func TestLoadReportsAnalysisError(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/justfile": "a: nope\n\techo a\n"}
	_, err := Load(fsys, "/p/justfile")
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v, want *diag.Error", err)
	}
	if de.Kind != diag.KindAnalysis || de.File != "/p/justfile" {
		t.Errorf("got kind %v file %q, want analysis in /p/justfile", de.Kind, de.File)
	}
}

func TestLoadReturnsAnalyzedTree(t *testing.T) {
	fsys := sysfake.FakeFS{"/p/justfile": "x := 'v'\na:\n\techo {{x}}\n"}
	jf, err := Load(fsys, "/p/justfile")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if jf.File != "/p/justfile" || len(jf.Recipes) != 1 || jf.Recipes[0].Name != "a" {
		t.Errorf("unexpected tree: %+v", jf)
	}
}

func TestLoadMissingFileWrapsNotExist(t *testing.T) {
	_, err := Load(sysfake.FakeFS{}, "/p/justfile")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want fs.ErrNotExist", err)
	}
}

func TestNamesAreFreshAndOrdered(t *testing.T) {
	n := names()
	if len(n) != 2 || n[0] != "justfile" || n[1] != ".justfile" {
		t.Fatalf("names = %v", n)
	}
	n[0] = "mutated"
	if names()[0] != "justfile" {
		t.Errorf("names shares state between calls")
	}
}

func TestParent(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/a/b", "/a", true},
		{"/a", "/", true},
		{"/", "/", false},
		{"a/b", "a", true},
		{"a", ".", true},
		{".", ".", false},
	}
	for _, c := range cases {
		got, ok := parent(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parent(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCandidates(t *testing.T) {
	got := candidates("/a/b")
	if len(got) != 2 || got[0] != "/a/b/justfile" || got[1] != "/a/b/.justfile" {
		t.Errorf("candidates = %v", got)
	}
}

func BenchmarkFindDeep(b *testing.B) {
	fsys := sysfake.FakeFS{"/justfile": ""}
	const start = "/a/b/c/d/e/f/g/h/i/j"
	for i := 0; i < b.N; i++ {
		if _, err := Find(fsys, start); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoad(b *testing.B) {
	fsys := sysfake.FakeFS{"/p/justfile": "x := 'v'\na: b\n\techo {{x}}\nb:\n\techo b\n"}
	for i := 0; i < b.N; i++ {
		if _, err := Load(fsys, "/p/justfile"); err != nil {
			b.Fatal(err)
		}
	}
}
