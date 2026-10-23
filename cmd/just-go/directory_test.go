package main

import (
	"bytes"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestStartDirAndRunDirFollowWorkingDirectory(t *testing.T) {
	if got := startDir(config{}, "/w"); got != "/w" {
		t.Errorf("startDir without the flag = %q", got)
	}
	if got := startDir(config{workingDirectory: "sub"}, "/w"); got != "/w/sub" {
		t.Errorf("startDir with the flag = %q", got)
	}
	if got := runDir(config{}, "/w"); got != "" {
		t.Errorf("runDir without the flag = %q; want none, so the justfile's directory is used", got)
	}
	if got := runDir(config{workingDirectory: "sub"}, "/w/sub"); got != "/w/sub" {
		t.Errorf("runDir with the flag = %q", got)
	}
}

func TestRunCeilingStopsTheSearchForTheJustfile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ceiling", "/w/foo"}, nil, "/w/foo/bar", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/justfile": "x:\n"}, &sysfake.FakeProcess{})
	if code != 1 || !strings.Contains(stderr.String(), "no justfile") {
		t.Errorf("run = %d, %q; want no justfile within the ceiling", code, stderr.String())
	}
}

func TestRunWorkingDirectoryRunsRecipesThere(t *testing.T) {
	proc := &sysfake.FakeProcess{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--working-directory", "bar", "foo"}, nil, "/w", strings.NewReader(""), &stdout, &stderr, sysfake.FakeFS{"/w/bar/justfile": "foo:\n  echo\n"}, proc)
	if code != 0 || len(proc.Specs) != 1 || proc.Specs[0].Dir != "/w/bar" {
		t.Errorf("run = %d, %q; want the recipe run in /w/bar", code, stderr.String())
	}
}
