package builtins

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// dirHost is a host on the system goos, whose environment is env.
func dirHost(goos string, env sysfake.FakeEnv) Host {
	return Host{Env: env, Platform: sysfake.FakePlatform{GOOS: goos}}
}

// The macOS rows are the oracle's answers with HOME set to /Users/pancha, which
// is what the corpus records (directories::*#1).
func TestDirectoriesOnMacOSAreUpstreamsPaths(t *testing.T) {
	h := dirHost("darwin", sysfake.FakeEnv{"HOME": "/Users/pancha"})
	cases := map[string]string{
		"cache_directory":        "/Users/pancha/Library/Caches",
		"config_directory":       "/Users/pancha/Library/Application Support",
		"config_local_directory": "/Users/pancha/Library/Application Support",
		"data_directory":         "/Users/pancha/Library/Application Support",
		"data_local_directory":   "/Users/pancha/Library/Application Support",
		"home_directory":         "/Users/pancha",
	}
	for name, want := range cases {
		got, err := hostCall(t, h, name)
		if err != nil || got != want {
			t.Errorf("%s() = %q, %v; want %q", name, got, err, want)
		}
	}
}

// The oracle has no executable or runtime directory on macOS, and says so.
func TestDirectoriesMissingOnMacOSFailAsUpstreamDoes(t *testing.T) {
	h := dirHost("darwin", sysfake.FakeEnv{"HOME": "/Users/pancha"})
	for name, want := range map[string]string{
		"executable_directory": "executable directory not found",
		"runtime_directory":    "runtime directory not found",
	} {
		_, err := hostCall(t, h, name)
		if err == nil || err.Error() != want {
			t.Errorf("%s() error = %v; want %q", name, err, want)
		}
	}
}

// The XDG rules are dirs 6.0.0's lin.rs, read from its source; the oracle here
// runs on macOS, so these rows are not checked against it.
func TestDirectoriesOnUnixFollowTheXDGRules(t *testing.T) {
	home := map[string]string{"HOME": "/home/u"}
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"cache_directory", map[string]string{"XDG_CACHE_HOME": "/x/c"}, "/x/c"},
		{"cache_directory", map[string]string{"XDG_CACHE_HOME": "relative"}, "/home/u/.cache"},
		{"config_local_directory", map[string]string{"XDG_CONFIG_HOME": "/x/cfg"}, "/x/cfg"},
		{"config_directory", nil, "/home/u/.config"},
		{"data_local_directory", map[string]string{"XDG_DATA_HOME": "/x/d"}, "/x/d"},
		{"data_directory", nil, "/home/u/.local/share"},
		{"executable_directory", map[string]string{"XDG_BIN_HOME": "/x/bin"}, "/x/bin"},
		{"executable_directory", nil, "/home/u/.local/bin"},
		{"runtime_directory", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, "/run/user/1000"},
	}
	for _, tc := range cases {
		env := sysfake.FakeEnv{}
		for k, v := range home {
			env[k] = v
		}
		for k, v := range tc.env {
			env[k] = v
		}
		got, err := hostCall(t, dirHost("linux", env), tc.name)
		if err != nil || got != tc.want {
			t.Errorf("%s with %v = %q, %v; want %q", tc.name, tc.env, got, err, tc.want)
		}
	}
}

func TestRuntimeDirectoryOnUnixNeedsItsVariable(t *testing.T) {
	_, err := hostCall(t, dirHost("linux", sysfake.FakeEnv{"HOME": "/home/u"}), "runtime_directory")
	if err == nil || err.Error() != "runtime directory not found" {
		t.Errorf("runtime_directory() without XDG_RUNTIME_DIR: error = %v", err)
	}
}

// The Windows folders are read from the variables that usually hold them. The
// known-folder API may place a folder elsewhere, which is not checked here.
func TestDirectoriesOnWindowsUseTheProfileVariables(t *testing.T) {
	env := sysfake.FakeEnv{
		"USERPROFILE":  `C:\Users\u`,
		"APPDATA":      `C:\Users\u\AppData\Roaming`,
		"LOCALAPPDATA": `C:\Users\u\AppData\Local`,
	}
	h := dirHost("windows", env)
	cases := map[string]string{
		"home_directory":         `C:\Users\u`,
		"cache_directory":        `C:\Users\u\AppData\Local`,
		"config_directory":       `C:\Users\u\AppData\Roaming`,
		"config_local_directory": `C:\Users\u\AppData\Local`,
		"data_directory":         `C:\Users\u\AppData\Roaming`,
		"data_local_directory":   `C:\Users\u\AppData\Local`,
	}
	for name, want := range cases {
		got, err := hostCall(t, h, name)
		if err != nil || got != want {
			t.Errorf("%s() = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestHomeDirectoryNeedsTheVariableToBeSetAndNonEmpty(t *testing.T) {
	for _, env := range []sysfake.FakeEnv{{}, {"HOME": ""}} {
		_, err := hostCall(t, dirHost("darwin", env), "home_directory")
		if err == nil || err.Error() != "home directory not found" {
			t.Errorf("home_directory() with %v: error = %v", env, err)
		}
	}
}

func TestDirectoryNotValidUnicodeIsSaidWithReplacements(t *testing.T) {
	// Each byte that is not part of a valid sequence becomes one replacement
	// character, as the oracle's message has it for the same bytes.
	h := dirHost("darwin", sysfake.FakeEnv{"HOME": "\xe0\x80\x80"})
	_, err := hostCall(t, h, "home_directory")
	if err == nil || err.Error() != "home directory is not valid unicode: `���`" {
		t.Errorf("home_directory() error = %v", err)
	}
}

// Windows has no executable or runtime folder in dirs 6.0.0, so both are missing.
func TestDirectoriesMissingOnWindowsFailAsUpstreamDoes(t *testing.T) {
	h := dirHost("windows", sysfake.FakeEnv{"USERPROFILE": `C:\Users\u`})
	for name, want := range map[string]string{
		"executable_directory": "executable directory not found",
		"runtime_directory":    "runtime directory not found",
	} {
		_, err := hostCall(t, h, name)
		if err == nil || err.Error() != want {
			t.Errorf("%s() on Windows: error = %v; want %q", name, err, want)
		}
	}
}
