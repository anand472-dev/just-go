package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// Every value flag and switch sets its field, so each setter and switch is run.
func TestParseArgsSetsEachSwitchAndValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want config
	}{
		{"groups", []string{"--groups"}, config{groups: true}},
		{"list-prefix", []string{"--list-prefix", "> "}, config{listPrefix: "> ", listPrefixGiven: true}},
		{"list-submodules", []string{"--list", "--list-submodules"}, config{list: true, listSubmodules: true}},
		{"show", []string{"--show", "foo"}, config{show: "foo"}},
		{"summary", []string{"--summary"}, config{summary: true}},
		{"unsorted", []string{"--unsorted"}, config{unsorted: true}},
		{"usage", []string{"--usage", "foo"}, config{usage: "foo"}},
		{"variables", []string{"--variables"}, config{variables: true}},
		{"completions", []string{"--completions", "bash"}, config{completions: "bash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestListPrefixIsFourSpacesUnlessGiven(t *testing.T) {
	if got := listPrefix(config{}); got != "    " {
		t.Errorf("listPrefix = %q; want four spaces", got)
	}
	if got := listPrefix(config{listPrefix: "...", listPrefixGiven: true}); got != "..." {
		t.Errorf("listPrefix given = %q; want ...", got)
	}
}

func TestFormatGroupsSortsByNameUnlessUnsorted(t *testing.T) {
	infos := []just.RecipeInfo{
		{Name: "bar", Groups: []string{"B"}},
		{Name: "foo", Groups: []string{"A", "B"}},
		{Name: "_hidden", Private: true, Groups: []string{"Z"}},
	}
	if got := formatGroups(infos, false, "    "); got != "Recipe groups:\n    A\n    B\n" {
		t.Errorf("sorted = %q", got)
	}
	infos[0], infos[1] = just.RecipeInfo{Name: "bar", Groups: []string{"B"}}, just.RecipeInfo{Name: "foo", Groups: []string{"A", "B"}}
	if got := formatGroups(infos, true, "> "); got != "Recipe groups:\n> B\n> A\n" {
		t.Errorf("unsorted = %q", got)
	}
}

func TestRunShowPrintsTheRecipeAsItIs(t *testing.T) {
	files := map[string]string{"/w/justfile": "# doc\nfoo:\n  echo\n"}
	got := runFake(t, files, nil, "--show", "foo")
	if got.code != 0 || got.stdout != "# doc\nfoo:\n    echo\n" {
		t.Errorf("run = %d, %q; want the recipe", got.code, got.stdout)
	}
}

func TestRunShowOfAMissingRecipeExitsOne(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "foo:\n"}, nil, "--show", "nope")
	if got.code != 1 || got.stderr != "error: justfile does not contain recipe `nope`\n" {
		t.Errorf("run = %d, %q; want status 1 and the missing-recipe error", got.code, got.stderr)
	}
}

func TestRunVariablesPrintsPublicNamesOnOneLine(t *testing.T) {
	got := runFake(t, map[string]string{"/w/justfile": "z := 'a'\na := 'z'\n_p := 'x'\n"}, nil, "--variables")
	if got.code != 0 || got.stdout != "a z\n" {
		t.Errorf("run = %d, %q; want a z", got.code, got.stdout)
	}
}

func TestRunGroupsPrintsTheGroupsOfPublicRecipes(t *testing.T) {
	files := map[string]string{"/w/justfile": "[group('B')]\nbar:\n\n[group('A')]\nfoo:\n"}
	got := runFake(t, files, nil, "--groups", "--list-prefix", "-")
	if got.code != 0 || got.stdout != "Recipe groups:\n-A\n-B\n" {
		t.Errorf("run = %d, %q; want the sorted groups", got.code, got.stdout)
	}
}

func TestDescribeInfoIsSilentForOtherRuns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, done := describeInfo(config{}, nil, nil, &stdout, &stderr); done {
		t.Error("describeInfo handled a run that asked for none of its modes")
	}
}

func TestRunListSubmodulesNeedsList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--list-submodules"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{}, &sysfake.FakeProcess{})
	if code != 2 || !strings.Contains(stderr.String(), "requires '--list'") {
		t.Errorf("run = %d, %q; want exit 2 and the requires error", code, stderr.String())
	}
}
