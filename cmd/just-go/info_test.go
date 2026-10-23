package main

import (
	"bytes"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
)

func TestAnswerPrintsVersionManAndChangelog(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  config
		want string
	}{
		{"version", config{version: true}, "just-go 1.59.0\n"},
		{"changelog", config{changelog: true}, "Changelog\n=========\n"},
		{"man", config{man: true}, ".TH just-go 1"},
	} {
		var out, errOut bytes.Buffer
		status, done := answer(c.cfg, nil, &out, &errOut)
		if status != 0 || !done || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: answer = %d, %v; output lacks %q", c.name, status, done, c.want)
		}
	}
}

func TestAnswerRequestReadsTheEnvironment(t *testing.T) {
	env := []string{"BAR=ab"}
	var out, errOut bytes.Buffer
	status, done := answer(config{requestGiven: true, request: `{"environment-variable": "BAR"}`}, env, &out, &errOut)
	if status != 0 || !done || out.String() != `{"environment-variable":{"Unix":[97,98]}}` {
		t.Errorf("request = %d, %v, out %q", status, done, out.String())
	}
	out.Reset()
	status, _ = answer(config{requestGiven: true, request: `{"environment-variable": "NO"}`}, env, &out, &errOut)
	if status != 0 || out.String() != `{"environment-variable":null}` {
		t.Errorf("missing variable: status %d, out %q", status, out.String())
	}
}

func TestAnswerRefusesAnUnsupportedRequest(t *testing.T) {
	var out, errOut bytes.Buffer
	status, done := answer(config{requestGiven: true, request: `{}`}, nil, &out, &errOut)
	if status != 1 || !done || errOut.String() != "error: unsupported request\n" {
		t.Errorf("answer = %d, %v, stderr %q", status, done, errOut.String())
	}
}

func TestAnswerIsSilentWithoutAFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if status, done := answer(config{}, nil, &out, &errOut); status != 0 || done || out.Len() != 0 {
		t.Errorf("answer with no flag = %d, %v, out %q", status, done, out.String())
	}
}

func TestCeilingOfPrefersTheFlagThenTheEnvironment(t *testing.T) {
	env := []string{"JUST_CEILING=/env"}
	if got := ceilingOf(config{ceiling: "/flag"}, env); got != "/flag" {
		t.Errorf("ceilingOf with --ceiling = %q", got)
	}
	if got := ceilingOf(config{}, env); got != "/env" {
		t.Errorf("ceilingOf from JUST_CEILING = %q", got)
	}
	if got := ceilingOf(config{}, nil); got != "" {
		t.Errorf("ceilingOf with neither = %q", got)
	}
}

// --allow-missing skips a word that names no recipe, and runs the rest.
func TestSelectRecipesWithAllowMissingSkipsUnknownWords(t *testing.T) {
	infos := []just.RecipeInfo{{Name: "a"}, {Name: "b", Params: []string{"x"}}}
	got, err := selectRecipesWith(infos, []string{"nope", "a", "zz", "b", "1"}, true)
	if err != nil || len(got) != 2 || got[0].Recipe != "a" || got[1].Recipe != "b" || got[1].Args[0] != "1" {
		t.Errorf("selectRecipesWith = %+v, %v", got, err)
	}
	if _, err := selectRecipesWith(infos, []string{"nope"}, false); err == nil {
		t.Errorf("without --allow-missing an unknown recipe was accepted")
	}
}

// early stops before anything else when an informational flag is given.
func TestEarlyAnswersInformationalFlagsFirst(t *testing.T) {
	var out, errOut bytes.Buffer
	status, done := early(config{version: true}, nil, "/w", nil, nil, nil, &out, &errOut)
	if status != 0 || !done || out.String() != "just-go 1.59.0\n" {
		t.Errorf("early = %d, %v, out %q", status, done, out.String())
	}
}
