package loader

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
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

func TestFindBothCandidatesInOneDirIsError(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "", "/proj/.justfile": ""}
	got, err := Find(fsys, "/proj")
	if got != "" {
		t.Errorf("Find path = %q, want empty", got)
	}
	if !errors.Is(err, ErrMultipleCandidates) {
		t.Fatalf("error = %v, want ErrMultipleCandidates", err)
	}
	var mc *MultipleCandidatesError
	if !errors.As(err, &mc) {
		t.Fatalf("error %T is not *MultipleCandidatesError", err)
	}
	if mc.Dir != "/proj" || !slices.Equal(mc.Names, []string{".justfile", "justfile"}) {
		t.Errorf("got Dir %q Names %v", mc.Dir, mc.Names)
	}
	// Corpus search::dot_justfile_conflicts_with_justfile.
	const want = "multiple candidate justfiles found in `/proj`: `.justfile` and `justfile`"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if errors.Is(err, ErrNoJustfile) {
		t.Errorf("multiple candidates must not look like ErrNoJustfile")
	}
}

func TestFindMultipleCandidatesFoundWhileWalkingUp(t *testing.T) {
	fsys := sysfake.FakeFS{"/a/justfile": "", "/a/JUSTFILE": "", "/a/.Justfile": ""}
	_, err := Find(fsys, "/a/b/c")
	const want = "multiple candidate justfiles found in `/a`: `.Justfile`, `JUSTFILE`, and `justfile`"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestFindMatchesNamesIgnoringASCIICase(t *testing.T) {
	// Corpus search::test_capitalized_justfile_search and found_spongebob_case.
	for _, name := range []string{"Justfile", "JuStFiLe", "JUSTFILE", ".JustFile"} {
		fsys := sysfake.FakeFS{"/a/" + name: ""}
		got, err := Find(fsys, "/a/b/c/d")
		if err != nil || got != "/a/"+name {
			t.Errorf("Find with %q = %q, %v; want /a/%s", name, got, err, name)
		}
	}
}

func TestFindIgnoresNearMissNames(t *testing.T) {
	// "ſ" (U+017F) folds to "s" under Unicode rules but not under ASCII rules.
	for _, name := range []string{"juſtfile", "justfile.txt", "my-justfile", "justfil"} {
		_, err := Find(sysfake.FakeFS{"/a/" + name: ""}, "/a")
		if !errors.Is(err, ErrNoJustfile) {
			t.Errorf("Find with %q: error = %v, want ErrNoJustfile", name, err)
		}
	}
}

// listFS lists names but reports only the files in regular as regular files,
// so a directory named like a justfile can be modelled.
type listFS struct {
	entries []string
	regular map[string]bool
	isErr   error
}

func (l listFS) ReadFile(string) ([]byte, error)  { return nil, errors.New("unused") }
func (l listFS) ReadDir(string) ([]string, error) { return l.entries, nil }
func (l listFS) IsFile(p string) (bool, error)    { return l.regular[p], l.isErr }

func TestFindSkipsDirectoriesNamedLikeJustfiles(t *testing.T) {
	fsys := listFS{
		entries: []string{".justfile", "justfile"},
		regular: map[string]bool{"/p/.justfile": true},
	}
	got, err := Find(fsys, "/p")
	if err != nil || got != "/p/.justfile" {
		t.Errorf("Find = %q, %v; want /p/.justfile", got, err)
	}
}

func TestFindIsFileErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	_, err := Find(listFS{entries: []string{"justfile"}, isErr: boom}, "/p")
	if !errors.Is(err, boom) || errors.Is(err, ErrNoJustfile) {
		t.Errorf("error = %v, want boom and not ErrNoJustfile", err)
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

func (e errFS) ReadFile(string) ([]byte, error)  { return nil, e.err }
func (e errFS) IsFile(string) (bool, error)      { return false, e.err }
func (e errFS) ReadDir(string) ([]string, error) { return nil, e.err }

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
	if de.Source != "a b c" {
		t.Errorf("Source = %q, want the line of the error", de.Source)
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
	if de.Source != "a: nope" || de.Col != 4 {
		t.Errorf("Source = %q at col %d, want the dependency's line and column", de.Source, de.Col)
	}
}

// The tree keeps the text it was read from, so that a failure while a recipe
// runs can quote its line.
func TestLoadKeepsTheSource(t *testing.T) {
	jf, err := Load(sysfake.FakeFS{"/p/justfile": "x := `pwd`\n"}, "/p/justfile")
	if err != nil || string(jf.Source) != "x := `pwd`\n" {
		t.Errorf("Load = %v, %v; want a tree that holds the text", jf, err)
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

func TestFindOnRealFilesystem(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a", "justfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Justfile")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Find(sys.OSFS{}, deep)
	if err != nil || got != want {
		t.Fatalf("Find = %q, %v; want %q", got, err, want)
	}
	other := filepath.Join(root, ".justfile")
	if err := os.WriteFile(other, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(sys.OSFS{}, deep); !errors.Is(err, ErrMultipleCandidates) {
		t.Fatalf("error = %v, want ErrMultipleCandidates", err)
	}
}

func TestMatching(t *testing.T) {
	got := matching([]string{"Justfile", "a", ".JUSTFILE", "justfile.bak", "justfile"})
	want := []string{"Justfile", ".JUSTFILE", "justfile"}
	if !slices.Equal(got, want) {
		t.Errorf("matching = %v, want %v", got, want)
	}
	if got := matching(nil); len(got) != 0 {
		t.Errorf("matching(nil) = %v, want empty", got)
	}
}

func TestEqualFoldASCII(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"justfile", "JUSTFILE", true},
		{".JuSt", ".jUsT", true},
		{"", "", true},
		{"a", "ab", false},
		{"a", "b", false},
		{"juſtfile", "justfile", false},
		{"@", "`", false}, // the bytes just outside A-Z and a-z must not fold
		{"[", "{", false},
	}
	for _, c := range cases {
		if got := equalFoldASCII(c.a, c.b); got != c.want {
			t.Errorf("equalFoldASCII(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAndTicked(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "`a`"},
		{[]string{"a", "b"}, "`a` and `b`"},
		{[]string{"a", "b", "c"}, "`a`, `b`, and `c`"},
	}
	for _, c := range cases {
		if got := andTicked(c.in); got != c.want {
			t.Errorf("andTicked(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMultipleCandidatesIsOnlyItsSentinel(t *testing.T) {
	err := &MultipleCandidatesError{Dir: "d", Names: []string{"a", "b"}}
	if errors.Is(err, ErrNoJustfile) || !errors.Is(err, ErrMultipleCandidates) {
		t.Errorf("Is mismatch for %v", err)
	}
}

func TestFindReadDirErrorIsReported(t *testing.T) {
	_, err := Find(errFS{fs.ErrPermission}, "/p")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error = %v, want permission error", err)
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
