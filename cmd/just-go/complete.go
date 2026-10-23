package main

import (
	"fmt"
	"io"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/completion"
	"gitea.com/wimwian/just-go/internal/sys"
)

// early handles the runs that print rather than run recipes: the candidates a
// shell asks for while it completes (JUST_COMPLETE set), and the script for
// --completions. done is false when neither applies. Impure: it reads the file
// system and writes to the streams.
func early(cfg config, env []string, workDir string, fsys sys.FS, stdout, stderr io.Writer) (status int, done bool) {
	if v, ok := envValue(env, "JUST_COMPLETE"); ok && v != "" {
		completeWords(cfg.words, workDir, fsys, stdout)
		return 0, true
	}
	if cfg.completions != "" {
		return writeCompletions(cfg, stdout, stderr), true
	}
	return 0, false
}

// completeWords prints the candidates for the words a shell is completing. The
// words are the command line after "--", the command name first and the word
// being completed last. The justfile is the one --justfile names, else the one
// found from the directory --working-directory names, else from workDir; with
// none, only the files and flags are offered. Impure: it reads the file system.
func completeWords(words []string, workDir string, fsys sys.FS, stdout io.Writer) {
	dir := workDir
	if wd, ok := completion.FlagValue(words, "working-directory"); ok {
		dir = resolve(workDir, wd)
	}
	path, _ := completion.FlagValue(words, "justfile")
	cat := completion.Catalog{Flags: flagNames()}
	if names, err := fsys.ReadDir(dir); err == nil {
		cat.Files = names
	}
	if jf, err := just.Load(just.Options{Justfile: resolve(dir, path), SearchFrom: dir, InvocationDir: dir, FS: fsys}); err == nil {
		cat.Variables = jf.Variables()
		for _, r := range jf.Recipes() {
			cat.Recipes = append(cat.Recipes, completion.Recipe{Name: r.Name, Doc: r.Doc, Groups: r.Groups, Private: r.Private})
		}
	}
	for _, line := range completion.Candidates(words, cat) {
		_, _ = fmt.Fprintln(stdout, line)
	}
}

// writeCompletions writes the completion script for the shell --completions
// names. It takes no word after the shell; a shell without a script is an
// error. Impure: it writes to stdout, or to stderr for an error.
func writeCompletions(cfg config, stdout, stderr io.Writer) int {
	if len(cfg.words) > 0 {
		return fail(stderr, cfg.quiet, usage("`--completions` used with unexpected argument: `%s`", cfg.words[0]))
	}
	script, ok := completion.Script(cfg.completions)
	if !ok {
		return fail(stderr, cfg.quiet, usage("completions for `%s` are not generated; the shells are bash, zsh and powershell", cfg.completions))
	}
	_, _ = io.WriteString(stdout, script)
	return 0
}
