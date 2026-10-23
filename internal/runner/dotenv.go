package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/dotenv"
	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Dotenv variables are added to the environment that the recipes and the
// expression functions see. They come from a dotenv file, found by its name in
// the justfile's directory or one above it, or from the output of
// dotenv-command. A variable already in the environment keeps its value unless
// dotenv-override is set.

// defaultDotenvFilename is the name a dotenv file has when none is set.
const defaultDotenvFilename = ".env"

// loadDotenv returns env with the dotenv variables of plan's justfile added. It
// does nothing when no dotenv setting asks for a file. It runs commands through
// exec and reads files through fsys, which are impure.
func loadDotenv(ctx context.Context, plan Plan, fsys sys.FS, exec sys.Process, set settings.Resolved) ([]string, error) {
	jf := plan.Justfile
	dir := filepath.Dir(jf.File)
	if plan.Dotenv.NoDotenv {
		return plan.Env, nil
	}
	set = plan.Dotenv.apply(set)
	if set.DotenvCommand != nil {
		return loadDotenvCommand(ctx, plan, exec, dir, set)
	}
	if !wantsDotenv(jf.Settings, set) {
		return plan.Env, nil
	}
	path, found, err := findDotenv(fsys, dir, set)
	if err != nil {
		return nil, err
	}
	if !found {
		if jf.Settings.DotenvRequired {
			return nil, fmt.Errorf("dotenv file not found")
		}
		return plan.Env, nil
	}
	return loadDotenvFile(fsys, plan, path)
}

// wantsDotenv reports whether a setting asks for a dotenv file: loading, a
// required or overriding file, or a name or path for it. It is pure.
func wantsDotenv(s ast.Settings, set settings.Resolved) bool {
	return s.DotenvLoad || s.DotenvRequired || s.DotenvOverride || set.DotenvFilename != nil || set.DotenvPath != nil
}

// loadDotenvFile reads the dotenv file at path and merges it into plan's
// environment.
func loadDotenvFile(fsys sys.FS, plan Plan, path string) ([]string, error) {
	if !plan.AllowOutsidePaths {
		if err := loader.CheckPath(filepath.Dir(plan.Justfile.File), path, fsys.Canonicalize); err != nil {
			return nil, err
		}
	}
	src, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment file from `%s`: %s", path, sys.ErrorText(err))
	}
	return mergeDotenv(plan, src, path)
}

// loadDotenvCommand runs the dotenv-command through the justfile's shell and
// reads its output as a dotenv file.
func loadDotenvCommand(ctx context.Context, plan Plan, exec sys.Process, dir string, set settings.Resolved) ([]string, error) {
	env := plan.Env
	for _, command := range set.DotenvCommand {
		argv := append(append([]string(nil), shellOf(set)...), command)
		plan.Env = env
		out, err := sys.Capture(ctx, exec, sys.ProcessSpec{Argv: argv, Dir: dir, Env: env})
		if err != nil {
			return nil, fmt.Errorf("dotenv command `%s` failed: %s", command, sys.ErrorText(err))
		}
		if out.ExitCode != 0 {
			return nil, &diag.Error{
				Kind: diag.KindRuntime, ExitCode: out.ExitCode,
				Msg: fmt.Sprintf("dotenv command `%s` failed: process exited with status code %d", command, out.ExitCode),
			}
		}
		if env, err = mergeDotenv(plan, out.Stdout, command); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// DotenvFlags are the dotenv choices the command line makes, which replace the
// justfile's settings for the run: a filename, a path, or commands, each given
// once; and NoDotenv, which loads no file or command at all.
type DotenvFlags struct {
	Filename, Path, Command []string
	NoDotenv                bool
}

// apply is set with the flags' values in place of the justfile's, for each the
// flags give. It is pure.
func (f DotenvFlags) apply(set settings.Resolved) settings.Resolved {
	if f.Filename != nil {
		set.DotenvFilename = f.Filename
	}
	if f.Path != nil {
		set.DotenvPath = f.Path
	}
	if f.Command != nil {
		set.DotenvCommand = f.Command
	}
	return set
}

// shellOf is the shell the justfile names, or the default one.
func shellOf(set settings.Resolved) []string {
	if len(set.Shell) > 0 {
		return set.Shell
	}
	return []string{defaultShell, defaultShellFlag}
}

// findDotenv is the file the dotenv settings name. With dotenv-path, that path
// relative to dir, if it is a file; else the search starts there. Without it, the
// search starts at dir, and the file is dotenv-filename, or .env. The search
// looks in the start directory and then in each directory above it. found is
// false when there is none.
func findDotenv(fsys sys.FS, dir string, set settings.Resolved) (string, bool, error) {
	start, name := dir, defaultDotenvFilename
	if set.DotenvFilename != nil {
		name = set.DotenvFilename[0]
	}
	if len(set.DotenvPath) > 0 {
		path := set.DotenvPath[0]
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		ok, err := fsys.IsFile(path)
		if err != nil || ok {
			return path, ok, err
		}
		start = path
	}
	return searchUp(fsys, start, name)
}

// searchUp looks for name in start and then in each directory above it, and
// returns the first it finds. found is false when there is none.
func searchUp(fsys sys.FS, start, name string) (string, bool, error) {
	for d := start; ; d = filepath.Dir(d) {
		path := filepath.Join(d, name)
		ok, err := fsys.IsFile(path)
		if err != nil || ok {
			return path, ok, err
		}
		if filepath.Dir(d) == d {
			return "", false, nil
		}
	}
}

// mergeDotenv parses src, a dotenv file named by from, against the environment of
// plan, and returns that environment with the variables added. A variable that
// the environment already has keeps its value unless dotenv-override is set.
func mergeDotenv(plan Plan, src []byte, from string) ([]string, error) {
	env := envMap(plan.Env)
	vals, err := dotenv.ParseWithEnv(src, func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load environment file from `%s`: %s", from, err)
	}
	override := plan.Justfile.Settings.DotenvOverride
	for k, v := range vals {
		if _, have := env[k]; have && !override {
			continue
		}
		env[k] = v
	}
	return envList(env), nil
}

// envMap is the environment as a map, the last entry of a key winning.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// envList is the environment as "KEY=VALUE" entries, sorted by key.
func envList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + env[k]
	}
	return out
}
