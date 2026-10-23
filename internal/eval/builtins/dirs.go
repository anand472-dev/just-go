package builtins

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/sys"
)

// The directory functions give the user's directories as the `dirs` crate
// (6.0.0, which upstream 1.59.0 uses) gives them: the macOS folders, the XDG
// base directories on the other Unix systems, and the profile folders on
// Windows. A directory the system does not have is an error, and so is a
// directory whose path is not valid UTF-8. The rules are pure over the
// platform name and the environment, which the Host supplies.

// The directory functions, one per name the table holds. Each reads the Host's
// environment and platform, and does no I/O.

func cacheDirectory(h Host, _ []string) (string, error) {
	return directory(h, "cache", "cache")
}

func configDirectory(h Host, _ []string) (string, error) {
	return directory(h, "config", "config")
}

func configLocalDirectory(h Host, _ []string) (string, error) {
	return directory(h, "config_local", "local config")
}

func dataDirectory(h Host, _ []string) (string, error) {
	return directory(h, "data", "data")
}

func dataLocalDirectory(h Host, _ []string) (string, error) {
	return directory(h, "data_local", "local data")
}

func executableDirectory(h Host, _ []string) (string, error) {
	return directory(h, "executable", "executable")
}

func homeDirectory(h Host, _ []string) (string, error) {
	return directory(h, "home", "home")
}

func runtimeDirectory(h Host, _ []string) (string, error) {
	return directory(h, "runtime", "runtime")
}

// directory is the directory key on the Host's system, or upstream's error for
// it, which names the directory by label.
func directory(h Host, key, label string) (string, error) {
	path, ok := directoryPath(key, h.Platform.OS(), h.Env)
	if !ok {
		return "", fmt.Errorf("%s directory not found", label)
	}
	if !utf8.ValidString(path) {
		return "", fmt.Errorf("%s directory is not valid unicode: `%s`", label, replacementText(path))
	}
	return path, nil
}

// directoryPath is the path of the directory key on the system goos, and
// whether the system has it. Keys name the function without its suffix.
func directoryPath(key, goos string, env sys.Env) (string, bool) {
	switch goos {
	case "darwin":
		return macDirectory(key, env)
	case "windows":
		return windowsDirectory(key, env)
	}
	return unixDirectory(key, env)
}

// macDirectory is the macOS folder of key, under the home folder.
func macDirectory(key string, env sys.Env) (string, bool) {
	home, ok := lookupNonEmpty(env, "HOME")
	if !ok {
		return "", false
	}
	switch key {
	case "home":
		return home, true
	case "cache":
		return joinPath(home, "Library/Caches"), true
	case "config", "config_local", "data", "data_local":
		return joinPath(home, "Library/Application Support"), true
	}
	return "", false
}

// unixDirectory is the XDG directory of key: the variable's path when it is an
// absolute one, else the default under the home folder. The runtime directory
// has no default.
func unixDirectory(key string, env sys.Env) (string, bool) {
	if key == "home" {
		return lookupNonEmpty(env, "HOME")
	}
	if key == "runtime" {
		return absoluteVariable(env, "XDG_RUNTIME_DIR")
	}
	variable, rel := xdgVariable(key)
	if path, ok := absoluteVariable(env, variable); ok {
		return path, true
	}
	home, ok := lookupNonEmpty(env, "HOME")
	if !ok {
		return "", false
	}
	return joinPath(home, rel), true
}

// xdgVariable names the XDG variable of key and the default under the home
// folder that it falls back to. It is not called for home or runtime.
func xdgVariable(key string) (string, string) {
	switch key {
	case "cache":
		return "XDG_CACHE_HOME", ".cache"
	case "config", "config_local":
		return "XDG_CONFIG_HOME", ".config"
	case "data", "data_local":
		return "XDG_DATA_HOME", ".local/share"
	}
	return "XDG_BIN_HOME", ".local/bin"
}

// windowsDirectory is the profile folder of key. The folders are read from the
// variables that usually name them, not from the known-folder API.
func windowsDirectory(key string, env sys.Env) (string, bool) {
	switch key {
	case "home":
		return lookupNonEmpty(env, "USERPROFILE")
	case "cache", "config_local", "data_local":
		return lookupNonEmpty(env, "LOCALAPPDATA")
	case "config", "data":
		return lookupNonEmpty(env, "APPDATA")
	}
	return "", false
}

// lookupNonEmpty is the value of key, when it is set and not empty.
func lookupNonEmpty(env sys.Env, key string) (string, bool) {
	v, ok := env.LookupEnv(key)
	return v, ok && v != ""
}

// absoluteVariable is the value of key when it is set to an absolute path.
func absoluteVariable(env sys.Env, key string) (string, bool) {
	v, ok := env.LookupEnv(key)
	return v, ok && strings.HasPrefix(v, "/")
}

// joinPath is the path rel below dir, joined with one slash, as PathBuf::join
// does for a relative path.
func joinPath(dir, rel string) string {
	return strings.TrimSuffix(dir, "/") + "/" + rel
}

// replacementText is s with each byte that is not part of a valid UTF-8
// sequence replaced by U+FFFD, as the lossy display of a path shows it.
func replacementText(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		b.WriteRune(r)
		s = s[n:]
	}
	return b.String()
}
