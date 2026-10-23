package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestSplitSearchSplitsAtTheLastSlash(t *testing.T) {
	cases := []struct {
		word, dir, recipe string
		ok                bool
	}{
		{"a/", "a", "", true},
		{"../default", "..", "default", true},
		{"./a/b/default", "./a/b", "default", true},
		{"/abs/x", "/abs", "x", true},
		{"/x", "/", "x", true},
		{"plain", "", "", false},
	}
	for _, tc := range cases {
		dir, recipe, ok := splitSearch(tc.word)
		if dir != tc.dir || recipe != tc.recipe || ok != tc.ok {
			t.Errorf("splitSearch(%q) = %q, %q, %v; want %q, %q, %v", tc.word, dir, recipe, ok, tc.dir, tc.recipe, tc.ok)
		}
	}
}

func TestSearchDirectoryMovesToTheNamedDirectory(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/sub/justfile": "x:\n"}
	cfg := config{words: []string{"sub/x", "arg"}}
	dir, err := searchDirectory(&cfg, "/w", fsys)
	if err != nil || dir != "/w/sub" || !reflect.DeepEqual(cfg.words, []string{"x", "arg"}) {
		t.Errorf("searchDirectory = %q, %v with words %q", dir, err, cfg.words)
	}
}

func TestSearchDirectoryWithAPlainFirstWordLeavesItAlone(t *testing.T) {
	cfg := config{words: []string{"build"}}
	if dir, err := searchDirectory(&cfg, "/w", sysfake.FakeFS{}); err != nil || dir != "/w" || !reflect.DeepEqual(cfg.words, []string{"build"}) {
		t.Errorf("searchDirectory = %q, %v with words %q", dir, err, cfg.words)
	}
	if dir, err := searchDirectory(&config{}, "/w", sysfake.FakeFS{}); err != nil || dir != "/w" {
		t.Errorf("searchDirectory with no words = %q, %v", dir, err)
	}
}

func TestSearchDirectoryForListTakesADirectoryArgument(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/child/justfile": "x:\n"}
	cfg := config{list: true, words: []string{"child"}}
	if dir, err := searchDirectory(&cfg, "/w", fsys); err != nil || dir != "/w/child" || len(cfg.words) != 0 {
		t.Errorf("searchDirectory for --list = %q, %v with words %q", dir, err, cfg.words)
	}
}

func TestSearchDirectoryForShowAndUsageNeedARecipe(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/child/justfile": "x:\n"}
	cfg := config{show: "child/x"}
	if dir, err := searchDirectory(&cfg, "/w", fsys); err != nil || dir != "/w/child" || cfg.show != "x" {
		t.Errorf("--show child/x = %q, %v with show %q", dir, err, cfg.show)
	}
	cfg = config{usage: "child/"}
	if _, err := searchDirectory(&cfg, "/w", fsys); err == nil {
		t.Error("--usage of a directory with no recipe was accepted")
	}
	cfg = config{show: "child"}
	var de *diag.Error
	if _, err := searchDirectory(&cfg, "/w", fsys); !errors.As(err, &de) || de.Msg != "`--show` requires recipe" {
		t.Errorf("--show of a directory = %v; want the requires-recipe error", err)
	}
	cfg = config{usage: "child"}
	if _, err := searchDirectory(&cfg, "/w", fsys); err == nil {
		t.Error("--usage of a directory was accepted")
	}
}

func TestSearchDirectoryForShowWithAPlainNameIsUnchanged(t *testing.T) {
	cfg := config{show: "plain"}
	if dir, err := searchDirectory(&cfg, "/w", sysfake.FakeFS{}); err != nil || dir != "/w" || cfg.show != "plain" {
		t.Errorf("--show plain = %q, %v with show %q", dir, err, cfg.show)
	}
}

func TestRunFindsTheJustfileNamedByJustfileName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--justfile-name", "foo,bar", "--list"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/bar": "x:\n"}, &sysfake.FakeProcess{})
	if code != 0 || !strings.Contains(stdout.String(), "x") {
		t.Errorf("run = %d, %q, %q; want the bar justfile listed", code, stdout.String(), stderr.String())
	}
}

func TestRunRunsARecipeInANamedSearchDirectory(t *testing.T) {
	proc := &sysfake.FakeProcess{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"sub/x"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/sub/justfile": "x:\n  echo\n"}, proc)
	if code != 0 || len(proc.Specs) != 1 {
		t.Errorf("run = %d, %q, %q", code, stdout.String(), stderr.String())
	}
}

func TestRunInitWritesToTheSearchDirectory(t *testing.T) {
	fsys := sysfake.FakeFS{"/w/sub/.keep": ""}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--init", "sub/"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, fsys, &sysfake.FakeProcess{})
	if code != 0 || fsys["/w/sub/justfile"] != starterJustfile {
		t.Errorf("--init sub/ = %d; files %v", code, fsys)
	}
}

func TestRunReportsAShowOfADirectoryWithNoRecipe(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--show", "sub"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/sub/justfile": "x:\n"}, &sysfake.FakeProcess{})
	if code != 1 || !strings.Contains(stderr.String(), "`--show` requires recipe") {
		t.Errorf("run = %d, %q; want the requires-recipe error", code, stderr.String())
	}
}
