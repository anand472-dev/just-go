package main

import (
	"context"
	"fmt"
	"io"

	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/sys"
)

// starterJustfile is the justfile --init writes, as upstream's is.
const starterJustfile = "# https://just.systems\n\ndefault:\n    echo 'Hello, world!'\n"

// writeInit writes the starter justfile to the directory the command runs in,
// at the --justfile, else under the --justfile-name, else as "justfile". A
// justfile from standard input cannot be written. An existing justfile is an error,
// and so is a file that cannot be written. Impure: it writes the file system and
// reports on stderr unless quiet.
func writeInit(cfg config, workDir string, fsys sys.FS, stderr io.Writer) int {
	if cfg.justfile == "-" {
		return initFailed(cfg, stderr, "cannot use justfile from standard input with `--init`")
	}
	path := resolve(workDir, initName(cfg))
	if exists, err := fsys.IsFile(path); err != nil || exists {
		return initFailed(cfg, stderr, fmt.Sprintf("justfile `%s` already exists", path))
	}
	if err := fsys.WriteFile(path, []byte(starterJustfile), 0o644); err != nil {
		return initFailed(cfg, stderr, fmt.Sprintf("failed to write justfile to `%s`: %s", path, sys.ErrorText(err)))
	}
	if !cfg.quiet {
		_, _ = fmt.Fprintf(stderr, "wrote justfile to `%s`\n", path)
	}
	return 0
}

// initName is the name --init writes: the --justfile if given, else the
// --justfile-name, else "justfile". Pure.
func initName(cfg config) string {
	switch {
	case cfg.justfile != "":
		return cfg.justfile
	case cfg.justfileName != "":
		return cfg.justfileName
	}
	return "justfile"
}

// initFailed reports why --init did not write, unless quiet, and returns the
// failure status. Impure: it writes to stderr.
func initFailed(cfg config, stderr io.Writer, msg string) int {
	if !cfg.quiet {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", msg)
	}
	return exitFailure
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
		return fail(stderr, cfg, err)
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
