package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestParseArgsSetsTheListingFlags(t *testing.T) {
	got, err := parseArgs([]string{"--list", "--group", "a", "--group", "b", "--list-heading", "H"})
	want := config{list: true, group: []string{"a", "b"}, listHeading: "H", listHeadingGiven: true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseArgs = %+v, %v; want %+v", got, err, want)
	}
}

func TestListOptionsOfReadTheListingFlags(t *testing.T) {
	if got := listOptionsOf(config{}); got.heading != listHeading || got.prefix != listIndent {
		t.Errorf("default listOptionsOf = %+v", got)
	}
	got := listOptionsOf(config{listHeading: "H", listHeadingGiven: true, listPrefix: "> ", listPrefixGiven: true, unsorted: true, group: []string{"g"}})
	want := listOptions{unsorted: true, heading: "H", prefix: "> ", groups: []string{"g"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listOptionsOf = %+v; want %+v", got, want)
	}
}

func TestFormatListWithHeadingPrefixAndGroupFilter(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "a", Groups: []string{"alpha"}},
		{Name: "b", Groups: []string{"alpha", "beta"}},
		{Name: "c"},
		{Name: "d", Groups: []string{"beta"}},
	}
	got := formatListWith(infos, listOptions{heading: "H\n", prefix: "> ", groups: []string{"alpha"}})
	if want := "H\n> [alpha]\n> a\n> b\n"; got != want {
		t.Errorf("formatListWith = %q; want %q", got, want)
	}
}

func TestListSectionsUnsortedKeepsTheOrderOfFirstAppearance(t *testing.T) {
	entries := []listEntry{
		{signature: "x", groups: []string{"z"}},
		{signature: "y", groups: []string{"a"}},
		{signature: "w"},
	}
	sections := listSections(entries, true, nil)
	got := []string{}
	for _, s := range sections {
		got = append(got, s.group)
	}
	if !reflect.DeepEqual(got, []string{"", "z", "a"}) {
		t.Errorf("sections (unsorted) = %q; want the ungrouped section, then z and a", got)
	}
}

func TestListSectionsWithAGroupFilterKeepsTheGivenNames(t *testing.T) {
	given := []string{"b", "a"}
	entries := []listEntry{{signature: "x", groups: []string{"a"}}}
	sections := listSections(entries, false, given)
	if len(sections) != 2 || sections[0].group != "a" || sections[1].group != "b" {
		t.Errorf("sections = %+v; want the groups a then b, sorted", sections)
	}
	if !reflect.DeepEqual(given, []string{"b", "a"}) {
		t.Errorf("listSections reordered the caller's groups: %q", given)
	}
}

func TestMissingGroupNamesTheFirstAbsentGroup(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "a", Groups: []string{"x"}}, {Name: "_p", Private: true, Groups: []string{"hidden"}}}
	if g, ok := missingGroup(infos, []string{"x", "y", "z"}); !ok || g != "y" {
		t.Errorf("missingGroup = %q, %v; want y", g, ok)
	}
	if _, ok := missingGroup(infos, []string{"x"}); ok {
		t.Error("missingGroup reported a group that exists")
	}
	if _, ok := missingGroup(infos, []string{"hidden"}); !ok {
		t.Error("missingGroup accepted the group of a private recipe")
	}
}

func TestRunListWithAMissingGroupExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "[group('x')]\na:\n"}, nil, "--list", "--group", "y")
	if got.code != 1 || got.stderr != "error: justfile does not contain group `y`\n" {
		t.Errorf("run = %d, %q; want status 1 and the group error", got.code, got.stderr)
	}
}

func TestRunListWithAGroupPrintsOnlyIt(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "[group('x')]\na:\nb:\n"}, nil, "--list", "--group", "x", "--list-heading", "H:\n", "--list-prefix", "+")
	if got.code != 0 || got.stdout != "H:\n+[x]\n+a\n" {
		t.Errorf("run = %d, %q", got.code, got.stdout)
	}
}

func TestRunUsageAndSummaryPrintTheirTexts(t *testing.T) {
	files := map[string]string{"/w/justfile": "foo bar:\nbaz:\n"}
	if got := runFake(t, files, nil, "--usage", "foo"); got.stdout != "Usage: just foo bar\n\nArguments:\n  bar\n" {
		t.Errorf("--usage = %q", got.stdout)
	}
	if got := runFake(t, files, nil, "--summary"); got.stdout != "baz foo\n" {
		t.Errorf("--summary = %q", got.stdout)
	}
	if got := runFake(t, files, nil, "--usage", "nope"); got.code != 1 {
		t.Errorf("--usage of a missing recipe = %d; want 1", got.code)
	}
}

func TestRunSummaryOfNoRecipesWarnsUnlessQuiet(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": ""}, nil, "--summary")
	if got.code != 0 || got.stdout != "\n" || got.stderr != "justfile contains no recipes\n" {
		t.Errorf("run = %d, %q, %q", got.code, got.stdout, got.stderr)
	}
	quiet := runFake(t, map[string]string{"/w/justfile": ""}, nil, "--summary", "--quiet")
	if quiet.stdout != "\n" || quiet.stderr != "" {
		t.Errorf("quiet run = %q, %q; want the blank line only", quiet.stdout, quiet.stderr)
	}
}

func TestDescribeInfoHandlesGroupsAndShow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	jf, err := just.Parse("justfile", []byte("[group('g')]\na:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if status, done := describeInfo(config{groups: true}, nil, jf, &stdout, &stderr); !done || status != 0 || !strings.Contains(stdout.String(), "    g") {
		t.Errorf("--groups = %d, %v, %q", status, done, stdout.String())
	}
	stdout.Reset()
	if status, done := describeInfo(config{show: "a"}, nil, jf, &stdout, &stderr); !done || status != 0 || stdout.String() != "[group('g')]\na:\n" {
		t.Errorf("--show = %d, %v, %q", status, done, stdout.String())
	}
}

func TestSetterRejectsASecondShell(t *testing.T) {
	cfg := config{shell: "bash", shellGiven: true}
	if err := setFlag(&cfg, "shell", "zsh"); err == nil {
		t.Error("setFlag accepted a second --shell")
	}
	if err := setFlag(&cfg, "no-such-flag", "x"); err != nil {
		t.Errorf("setFlag of an unknown flag = %v; want none", err)
	}
}

func TestRunListSubmodulesWithListIsAccepted(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--list", "--list-submodules"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "a:\n"}, &sysfake.FakeProcess{})
	if code != 0 {
		t.Errorf("run = %d, %q; want success", code, stderr.String())
	}
}
