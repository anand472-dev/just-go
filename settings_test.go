package just

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestDefaultListAndFallbackReportTheSettings(t *testing.T) {
	plain := mustParse(t, "foo:\n")
	if plain.DefaultList() || plain.Fallback() {
		t.Error("a justfile without the settings reports them")
	}
	set := mustParse(t, "set default-list\nset fallback := true\nfoo:\n")
	if !set.DefaultList() || !set.Fallback() {
		t.Error("a justfile with the settings does not report them")
	}
	off := mustParse(t, "set default-list := false\nset fallback := false\nfoo:\n")
	if off.DefaultList() || off.Fallback() {
		t.Error("a justfile with the settings off reports them")
	}
}

func TestRunNoCdUsesTheInvocationDirectory(t *testing.T) {
	j := mustParse(t, "set no-cd\nfoo:\n\techo\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", InvocationDir: "/w/sub", Exec: proc}); err != nil {
		t.Fatal(err)
	}
	if got := proc.Specs[0].Dir; got != "/w/sub" {
		t.Errorf("Dir = %q, want /w/sub", got)
	}
}

func TestRunWorkingDirectorySettingIsRelativeToWorkDir(t *testing.T) {
	j := mustParse(t, "set working-directory := 'bar'\nfoo:\n\techo\n")
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", InvocationDir: "/w/sub", Exec: proc}); err != nil {
		t.Fatal(err)
	}
	if got := proc.Specs[0].Dir; got != "/w/bar" {
		t.Errorf("Dir = %q, want /w/bar", got)
	}
}

func TestRecipesListsTheLastOfDuplicateRecipes(t *testing.T) {
	j := mustParse(t, "set allow-duplicate-recipes\na:\n\techo 1\nb:\na x:\n\techo 2\n")
	var got [][]string
	for _, r := range j.Recipes() {
		got = append(got, append([]string{r.Name}, r.Params...))
	}
	if want := [][]string{{"b"}, {"a", "x"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Recipes = %v, want %v", got, want)
	}
}

func TestParseReportsSettingErrors(t *testing.T) {
	for src, msg := range map[string]string{
		"set minimum-version := '999.0.0'\n":                   "justfile requires just 999.0.0 or later, but using 1.59.0",
		"set no-cd\nset working-directory := 'x'\n":            "`no-cd` set on line 1 is incompatible with `working-directory`",
		"set tempdir := nope\n":                                "variable `nope` not defined",
		"set quiet\nset quiet\n":                               "setting `quiet` first set on line 1 is redefined on line 2",
		"set guards\nfoo:\n\t-?true\n":                         "the guard `?` and infallible `-` sigils may not be used together",
		"set export := maybe\n":                                "expected keyword `true` or `false` but found identifier `maybe`",
		"set indentation := 'x'\n":                             "indentation must be spaces or tabs",
		"set windows-shell := [nope]\nfoo:\n":                  "variable `nope` not defined",
		"set script-interpreter := ['sh', arch()]\nfoo:\n":     "cannot call functions in const context",
		"set allow-duplicate-recipes := false\na:\na:\n":       "recipe `a` first defined on line 2 is redefined on line 3",
		"set allow-duplicate-variables := false\na:=''\na:=''": "variable `a` has multiple definitions",
	} {
		_, err := Parse("justfile", []byte(src))
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, msg)
		}
	}
}

// gateMessage is upstream's error for a feature that needs unstable features
// enabled (oracle/src/error.rs, UnstableFeature).
func gateMessage(feature string) string {
	return feature + ", invoke `just` with `--unstable`, set the `JUST_UNSTABLE` environment variable, or add `set unstable` to your `justfile` to enable unstable features"
}

const listsGate = "the `lists` setting is currently unstable"

func TestParseAcceptsUnstableFeaturesAndRunRefusesThem(t *testing.T) {
	// Parsing never refuses: Parse has no options to say they are enabled.
	j := mustParse(t, "set lists\nfoo:\n\t@echo hi\n")
	_, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: &sysfake.FakeProcess{}})
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Line != 0 || de.Msg != gateMessage(listsGate) {
		t.Fatalf("Run = %v, want the unstable error", err)
	}
	proc := &sysfake.FakeProcess{}
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Unstable: true, Exec: proc}); err != nil {
		t.Errorf("Run with Unstable = %v", err)
	}
	if len(proc.Specs) != 1 {
		t.Errorf("ran %d commands, want 1", len(proc.Specs))
	}
	// Nothing ran when it was refused.
	refused := &sysfake.FakeProcess{}
	_, _ = j.Run(context.Background(), Options{WorkDir: "/w", Exec: refused})
	if len(refused.Specs) != 0 {
		t.Errorf("a refused run ran %d commands", len(refused.Specs))
	}
}

func TestRunSetUnstableEnablesUnstableFeatures(t *testing.T) {
	for src, enabled := range map[string]bool{
		"set unstable\nset lists\nfoo:\n\t@echo hi\n":          true,
		"set unstable := true\nset lists\nfoo:\n\t@echo hi\n":  true,
		"set lists\nset unstable\nfoo:\n\t@echo hi\n":          true,
		"set unstable := false\nset lists\nfoo:\n\t@echo hi\n": false,
		"set lists\nfoo:\n\t@echo hi\n":                        false,
	} {
		j := mustParse(t, src)
		_, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: &sysfake.FakeProcess{}})
		if (err == nil) != enabled {
			t.Errorf("Run(%q) = %v, want enabled = %v", src, err, enabled)
		}
	}
	// A false `set lists` is still the setting.
	j := mustParse(t, "set lists := false\nfoo:\n\t@echo hi\n")
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: &sysfake.FakeProcess{}}); err == nil {
		t.Error("`set lists := false` is not refused")
	}
}

func TestRunWithoutUnstableFeaturesNeedsNoSetting(t *testing.T) {
	j := mustParse(t, "set quiet\nfoo:\n\t@echo hi\n")
	if _, err := j.Run(context.Background(), Options{WorkDir: "/w", Exec: &sysfake.FakeProcess{}}); err != nil {
		t.Errorf("Run = %v", err)
	}
}

// Load refuses too, so that listing a justfile that needs unstable features
// is refused as upstream refuses it.
func TestLoadRefusesUnstableFeaturesUnlessEnabled(t *testing.T) {
	files := sysfake.FakeFS(map[string]string{"/w/justfile": "set lists\nfoo:\n\t@echo hi\n"})
	_, err := Load(Options{Justfile: "/w/justfile", FS: files})
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != gateMessage(listsGate) {
		t.Fatalf("Load = %v, want the unstable error", err)
	}
	if _, err := Load(Options{Justfile: "/w/justfile", FS: files, Unstable: true}); err != nil {
		t.Errorf("Load with Unstable = %v", err)
	}
	plain := sysfake.FakeFS(map[string]string{"/w/justfile": "foo:\n"})
	if _, err := Load(Options{Justfile: "/w/justfile", FS: plain}); err != nil {
		t.Errorf("Load of a justfile that needs nothing = %v", err)
	}
}

// Errors found while parsing and analyzing come before the unstable error, as
// upstream's compile comes before its check.
func TestLoadReportsCompileErrorsBeforeTheUnstableError(t *testing.T) {
	files := sysfake.FakeFS(map[string]string{"/w/justfile": "set lists\nfoo: nope\n"})
	_, err := Load(Options{Justfile: "/w/justfile", FS: files})
	if err == nil || strings.Contains(err.Error(), "unstable") {
		t.Errorf("Load = %v, want the unknown dependency", err)
	}
}

func TestParseReportsDotenvSettingErrors(t *testing.T) {
	for src, msg := range map[string]string{
		"set dotenv-command := 'x'\nset dotenv-path := 'y'\n": "`dotenv-command` set on line 1 is incompatible with `dotenv-path`",
		"set dotenv-load\nset dotenv-command := 'x'\n":        "`dotenv-load` set on line 1 is incompatible with `dotenv-command`",
		"set dotenv-filename := nope\n":                       "variable `nope` not defined",
		"set dotenv-load := 'x'\n":                            "expected identifier, but found string",
		"x := bool('t')\n":                                    "the `bool()` function requires `set lists`",
		"set dotenv-path := ['a', 'b']\n":                     "list literals require `set lists`",
	} {
		_, err := Parse("justfile", []byte(src))
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, msg)
		}
	}
	// With the settings they are fine: dotenv loading is a later task, so
	// they are accepted and have no effect yet.
	for _, src := range []string{
		"set dotenv-load\nset dotenv-override\nset dotenv-required\nset dotenv-filename := '.env'\nset dotenv-path := '.'\n",
		"set dotenv-command := 'echo A=b'\nset dotenv-override := false\n",
		"set unstable\nset dotenv-load := false\n",
	} {
		if _, err := Parse("justfile", []byte(src)); err != nil {
			t.Errorf("Parse(%q) = %v", src, err)
		}
	}
}
