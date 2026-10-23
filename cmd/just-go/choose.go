package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/sys"
)

// chooseRecipes runs the chooser over the recipes that can be run without an
// argument, and then runs each recipe it selects, one per line of its output. The
// chooser is the --chooser value, else JUST_CHOOSER, else upstream's default; it
// runs through the shell's `-c`, as upstream runs it, and it sees the candidates
// one per line on its standard input. Impure: it runs the chooser and the
// selected recipes.
func chooseRecipes(cfg config, env []string, jf *just.Justfile, opts just.Options, stdout, stderr io.Writer) int {
	candidates := choosable(jf.Recipes(), cfg.group)
	if len(candidates) == 0 {
		return fail(stderr, cfg, usage("justfile contains no choosable recipes"))
	}
	chooser := chooserOf(cfg, env)
	var selected bytes.Buffer
	code, err := opts.Exec.Run(context.Background(), sys.ProcessSpec{
		Argv:   []string{"sh", "-c", chooser},
		Dir:    opts.InvocationDir,
		Env:    env,
		Stdin:  strings.NewReader(strings.Join(candidates, "\n") + "\n"),
		Stdout: &selected,
		Stderr: stderr,
	})
	if status, failed := chooserFailed(chooser, code, err, cfg.quiet, stderr); failed {
		return status
	}
	opts.Calls = callsOf(selected.String(), candidates)
	res, err := jf.Run(context.Background(), opts)
	if err != nil {
		return fail(stderr, cfg, err)
	}
	return res.ExitCode
}

// defaultChooser is upstream's chooser when none is given: the user's shell,
// running fzf with a preview of each recipe. Pure.
func defaultChooser(env []string) string {
	shell, _ := envValue(env, "SHELL")
	if shell == "" {
		shell = "/"
	}
	return shell + " -cu fzf --multi --preview 'just --unstable --color always --show {}'"
}

// chooserOf is the chooser to run: --chooser, else JUST_CHOOSER, else the default.
// Pure.
func chooserOf(cfg config, env []string) string {
	if cfg.chooser != "" {
		return cfg.chooser
	}
	if v, ok := envValue(env, "JUST_CHOOSER"); ok && v != "" {
		return v
	}
	return defaultChooser(env)
}

// choosable are the candidate lines for the chooser: each public recipe that
// needs no argument, in name order, a module's recipe written with spaces for its
// path, and limited to the --group values when there are any. Pure.
func choosable(infos []just.RecipeInfo, groups []string) []string {
	var names []string
	for _, info := range infos {
		if info.Private || needsArgument(info) || len(groups) > 0 && !inAnyGroup(info.Groups, groups) {
			continue
		}
		names = append(names, strings.ReplaceAll(info.Name, "::", " "))
	}
	sort.Strings(names)
	return names
}

// needsArgument reports whether a recipe cannot run without an argument: a single
// or variadic `+` parameter with no default. Pure.
func needsArgument(info just.RecipeInfo) bool {
	for i := range info.Params {
		if i < len(info.Defaults) && info.Defaults[i] != "" {
			continue
		}
		if i < len(info.Kinds) && info.Kinds[i] == "*" {
			continue
		}
		return true
	}
	return false
}

// inAnyGroup reports whether one of have is one of want. Pure.
func inAnyGroup(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}

// callsOf are the recipes a chooser selected, one per non-empty line. A line that
// is a candidate is that recipe, a module's written with spaces; any other line is
// a list of recipe names, each a call. Pure.
func callsOf(output string, candidates []string) []just.Call {
	known := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		known[c] = true
	}
	var calls []just.Call
	for _, line := range strings.Split(output, "\n") {
		text := strings.Join(strings.Fields(line), " ")
		if text == "" {
			continue
		}
		if known[text] {
			calls = append(calls, just.Call{Recipe: strings.ReplaceAll(text, " ", "::")})
			continue
		}
		for _, word := range strings.Fields(line) {
			calls = append(calls, just.Call{Recipe: word})
		}
	}
	return calls
}

// chooserFailed reports whether the chooser did not finish cleanly, and if so
// writes its message and returns the status to end with: the signal's status
// (128 plus its number) when a signal ended it, else 1. Impure: it writes to
// stderr unless quiet.
func chooserFailed(chooser string, code int, err error, quiet bool, stderr io.Writer) (int, bool) {
	var sig *sys.SignalError
	switch {
	case errors.As(err, &sig) && sig.Signal != 0:
		if !quiet {
			_, _ = fmt.Fprintf(stderr, "error: chooser `%s` failed: signal: %d (%s)\n", chooser, sig.Signal, sys.SignalName(sig.Signal))
		}
		return 128 + sig.Signal, true
	case err != nil || code != 0:
		if !quiet {
			_, _ = fmt.Fprintf(stderr, "error: chooser `%s` invocation failed: %s\n", chooser, chooserError(code, err))
		}
		return exitFailure, true
	}
	return 0, false
}

// chooserError says how the chooser ended, for its message: its exit code, or the
// signal that ended it. Pure.
func chooserError(code int, err error) string {
	if err != nil {
		return sys.ErrorText(err)
	}
	return fmt.Sprintf("exit status %d", code)
}
