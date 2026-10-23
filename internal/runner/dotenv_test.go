package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// dotenvPlan is a plan for the justfile /proj/justfile with the settings given.
func dotenvPlan(s ast.Settings, env ...string) Plan {
	return Plan{Justfile: &ast.Justfile{File: "/proj/justfile", Settings: s}, Env: env}
}

// dotenvValue is the environment's value of key, and whether it is set.
func dotenvValue(env []string, key string) (string, bool) {
	v, ok := envMap(env)[key]
	return v, ok
}

func TestDotenvIsNotLoadedWithoutASetting(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/.env": "A=1"}
	got, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{}, "B=2"), fsys, &sysfake.FakeProcess{}, settings.Resolved{})
	if err != nil || len(got) != 1 || got[0] != "B=2" {
		t.Errorf("loadDotenv = %v, %v; want the environment unchanged", got, err)
	}
}

// dotenv-load finds .env beside the justfile, or above it, and adds its values
// without changing those already set.
func TestDotenvLoadFindsTheFileAndKeepsTheEnvironment(t *testing.T) {
	fsys := sysfake.FakeFS{"/root/.env": "A=file\nB=file", "/root/proj/justfile": ""}
	plan := dotenvPlan(ast.Settings{DotenvLoad: true}, "B=env")
	plan.Justfile.File = "/root/proj/justfile"
	got, err := loadDotenv(context.Background(), plan, fsys, &sysfake.FakeProcess{}, settings.Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := dotenvValue(got, "A"); a != "file" {
		t.Errorf("A = %q; want the file's value", a)
	}
	if b, _ := dotenvValue(got, "B"); b != "env" {
		t.Errorf("B = %q; want the environment's value kept", b)
	}
}

// dotenv-override replaces a value that is set.
func TestDotenvOverrideReplacesSetValues(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/.env": "B=file"}
	plan := dotenvPlan(ast.Settings{DotenvOverride: true}, "B=env")
	got, err := loadDotenv(context.Background(), plan, fsys, &sysfake.FakeProcess{}, settings.Resolved{})
	if b, _ := dotenvValue(got, "B"); err != nil || b != "file" {
		t.Errorf("B = %q, %v; want the file's value", b, err)
	}
}

func TestDotenvRequiredWithNoFileIsAnError(t *testing.T) {
	_, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{DotenvRequired: true}),
		sysfake.FakeFS{}, &sysfake.FakeProcess{}, settings.Resolved{})
	if err == nil || err.Error() != "dotenv file not found" {
		t.Errorf("error = %v; want dotenv file not found", err)
	}
}

// dotenv-filename names the file, and dotenv-path gives its path, relative to the
// justfile. A path that is not a file is searched from, as a directory.
func TestDotenvFilenameAndPathChooseTheFile(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/special.env": "S=yes", "/proj/sub/.env": "P=yes"}
	plan := dotenvPlan(ast.Settings{})
	got, err := loadDotenv(context.Background(), plan, fsys, &sysfake.FakeProcess{},
		settings.Resolved{DotenvFilename: []string{"special.env"}})
	if s, _ := dotenvValue(got, "S"); err != nil || s != "yes" {
		t.Errorf("filename: S = %q, %v", s, err)
	}
	got, err = loadDotenv(context.Background(), plan, fsys, &sysfake.FakeProcess{},
		settings.Resolved{DotenvPath: []string{"sub"}})
	if p, _ := dotenvValue(got, "P"); err != nil || p != "yes" {
		t.Errorf("path to a directory: P = %q, %v", p, err)
	}
	got, err = loadDotenv(context.Background(), plan, fsys, &sysfake.FakeProcess{},
		settings.Resolved{DotenvPath: []string{"/proj/special.env"}})
	if s, _ := dotenvValue(got, "S"); err != nil || s != "yes" {
		t.Errorf("absolute path: S = %q, %v", s, err)
	}
}

// A file that cannot be parsed is an error that names the file.
func TestDotenvParseErrorNamesTheFile(t *testing.T) {
	plan := dotenvPlan(ast.Settings{DotenvLoad: true})
	_, err := loadDotenv(context.Background(), plan, sysfake.FakeFS{"/proj/.env": "FOO=bar baz"},
		&sysfake.FakeProcess{}, settings.Resolved{})
	if err == nil || !strings.Contains(err.Error(), "failed to load environment file from `/proj/.env`") {
		t.Errorf("error = %v; want the file named", err)
	}
}

// A file that cannot be read is an error that names it.
func TestDotenvReadErrorNamesTheFile(t *testing.T) {
	boom := errors.New("boom")
	fsys := readErrFS{sysfake.FakeFS{"/proj/.env": "A=1"}, boom}
	_, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{DotenvLoad: true}), fsys,
		&sysfake.FakeProcess{}, settings.Resolved{})
	if err == nil || !strings.Contains(err.Error(), "failed to load environment file from `/proj/.env`") {
		t.Errorf("error = %v; want the read failure named", err)
	}
}

// readErrFS is a file system whose files cannot be read.
type readErrFS struct {
	sysfake.FakeFS
	err error
}

func (f readErrFS) ReadFile(string) ([]byte, error) { return nil, f.err }

// A failing search is reported, not taken for absence.
func TestDotenvSearchErrorIsReported(t *testing.T) {
	boom := errors.New("boom")
	_, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{DotenvLoad: true}),
		isErrFS{sysfake.FakeFS{}, boom}, &sysfake.FakeProcess{}, settings.Resolved{})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v; want the search failure", err)
	}
}

type isErrFS struct {
	sysfake.FakeFS
	err error
}

func (f isErrFS) IsFile(string) (bool, error) { return false, f.err }

// dotenv-command runs its command through the justfile's shell, in the justfile's
// directory, and reads the output as a dotenv file.
func TestDotenvCommandOutputIsTheDotenvFile(t *testing.T) {
	proc := &sysfake.FakeProcess{Outputs: []string{"K=v\n"}}
	got, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{}), sysfake.FakeFS{}, proc,
		settings.Resolved{DotenvCommand: []string{"echo K=v"}, Shell: []string{"sh", "-c"}})
	if k, _ := dotenvValue(got, "K"); err != nil || k != "v" {
		t.Errorf("command: K = %q, %v", k, err)
	}
	if spec := proc.Specs[0]; spec.Dir != "/proj" || spec.Argv[0] != "sh" || spec.Argv[len(spec.Argv)-1] != "echo K=v" {
		t.Errorf("command spec = %+v; want the shell in /proj", spec)
	}
}

// A command that exits with a status is a diagnostic that carries the status, and
// one that cannot start is an error.
func TestDotenvCommandFailureCarriesItsStatus(t *testing.T) {
	proc := &sysfake.FakeProcess{Codes: []int{42}}
	_, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{}), sysfake.FakeFS{}, proc,
		settings.Resolved{DotenvCommand: []string{"exit 42"}})
	var de *diag.Error
	if !errors.As(err, &de) || de.ExitCode != 42 || !strings.Contains(de.Msg, "process exited with status code 42") {
		t.Errorf("error = %+v; want the status 42 carried", err)
	}
	boom := errors.New("cannot start")
	_, err = loadDotenv(context.Background(), dotenvPlan(ast.Settings{}), sysfake.FakeFS{}, &sysfake.FakeProcess{Err: boom},
		settings.Resolved{DotenvCommand: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), "dotenv command `nope` failed") {
		t.Errorf("start failure: error = %v", err)
	}
}

func TestShellOfDefaultsWhenNoneIsSet(t *testing.T) {
	if got := shellOf(settings.Resolved{}); strings.Join(got, " ") != "sh -cu" {
		t.Errorf("shellOf() = %v; want sh -cu", got)
	}
	if got := shellOf(settings.Resolved{Shell: []string{"bash"}}); got[0] != "bash" {
		t.Errorf("shellOf(bash) = %v", got)
	}
}

func TestEnvHelpersKeepTheLastEntryAndSortByKey(t *testing.T) {
	m := envMap([]string{"B=1", "A=2", "A=3", "no-equals"})
	if m["A"] != "3" || m["B"] != "1" || len(m) != 2 {
		t.Errorf("envMap = %v", m)
	}
	if got := envList(m); strings.Join(got, ",") != "A=3,B=1" {
		t.Errorf("envList = %v; want sorted", got)
	}
}

// dotenv-load with no file to load leaves the environment as it is.
func TestDotenvLoadWithNoFileLeavesTheEnvironment(t *testing.T) {
	got, err := loadDotenv(context.Background(), dotenvPlan(ast.Settings{DotenvLoad: true}, "A=1"),
		sysfake.FakeFS{}, &sysfake.FakeProcess{}, settings.Resolved{})
	if err != nil || len(got) != 1 || got[0] != "A=1" {
		t.Errorf("loadDotenv = %v, %v; want the environment unchanged", got, err)
	}
}

// Run refuses to start when the dotenv file it must load is missing.
func TestRunFailsWhenARequiredDotenvFileIsMissing(t *testing.T) {
	plan := Plan{
		Justfile: &ast.Justfile{File: "/proj/justfile", Settings: ast.Settings{DotenvRequired: true},
			Recipes: []ast.Recipe{{Name: "a"}}},
		FS:   sysfake.FakeFS{},
		Exec: &sysfake.FakeProcess{},
	}
	if _, err := Run(context.Background(), plan); err == nil || err.Error() != "dotenv file not found" {
		t.Errorf("Run = %v; want dotenv file not found", err)
	}
}
