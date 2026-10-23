package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/sys"
)

// starterJustfile is the justfile --init writes, as upstream's is.
const starterJustfile = "# https://just.systems\n\ndefault:\n    echo 'Hello, world!'\n"

// writeInit writes the starter justfile to the directory the command runs in,
// under the --justfile-name, else "justfile". An existing justfile is an error,
// and so is a file that cannot be written. Impure: it writes the file system and
// reports on stderr unless quiet.
func writeInit(cfg config, workDir string, fsys sys.FS, stderr io.Writer) int {
	name := "justfile"
	if cfg.justfileName != "" {
		name = cfg.justfileName
	}
	path := filepath.Join(workDir, name)
	if exists, err := fsys.IsFile(path); err != nil || exists {
		if !cfg.quiet {
			_, _ = fmt.Fprintf(stderr, "error: justfile `%s` already exists\n", path)
		}
		return exitFailure
	}
	if err := fsys.WriteFile(path, []byte(starterJustfile), 0o644); err != nil {
		if !cfg.quiet {
			_, _ = fmt.Fprintf(stderr, "error: failed to write justfile to `%s`: %s\n", path, sys.ErrorText(err))
		}
		return exitFailure
	}
	if !cfg.quiet {
		_, _ = fmt.Fprintf(stderr, "wrote justfile to `%s`\n", path)
	}
	return 0
}

// editJustfile runs the editor, VISUAL or else EDITOR, on the justfile the
// command finds from workDir, and returns the editor's status. The editor runs
// with the path as an argument, never through a shell. Impure: it runs a
// process and reads the file system.
func editJustfile(cfg config, env []string, workDir string, fsys sys.FS, exec sys.Process, stderr io.Writer) int {
	editor, ok := envValue(env, "VISUAL")
	if !ok || editor == "" {
		editor, _ = envValue(env, "EDITOR")
	}
	if editor == "" {
		if !cfg.quiet {
			_, _ = fmt.Fprintln(stderr, "error: no editor set: set VISUAL or EDITOR")
		}
		return exitFailure
	}
	path, err := loader.Find(fsys, workDir)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	code, err := exec.Run(context.Background(), sys.ProcessSpec{Argv: []string{editor, path}, Dir: workDir, Env: env})
	if err != nil {
		if !cfg.quiet {
			_, _ = fmt.Fprintf(stderr, "error: could not run editor `%s`: %s\n", editor, sys.ErrorText(err))
		}
		return exitFailure
	}
	return code
}
