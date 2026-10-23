package main

import (
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// splitSearch splits a word of the form dir/recipe at its last slash: the
// directory, which keeps its leading "./" or "../" parts, and the recipe, which
// may be empty. ok is false for a word with no slash. Pure.
func splitSearch(word string) (dir, recipe string, ok bool) {
	i := strings.LastIndex(word, "/")
	if i < 0 {
		return "", "", false
	}
	dir = word[:i]
	if dir == "" {
		dir = "/"
	}
	return dir, word[i+1:], true
}

// isDirectory reports whether path names a directory with something in it. An
// empty directory is not one here, so that a recipe name is never taken for one
// that has nothing in it. Impure: it reads fsys.
func isDirectory(fsys sys.FS, path string) bool {
	names, err := fsys.ReadDir(path)
	return err == nil && len(names) > 0
}

// searchDirectory moves the search for the justfile to the directory the command
// line names, and returns the directory the command runs from, which is workDir
// unless the command line names another. A first word of the form dir/recipe
// (or a directory) starts the search in dir, and the recipe is put back as the
// first word; a --list argument that is a directory does so too. A --show or
// --usage argument of the form dir/recipe does the same, and naming a directory
// there is an error, since it needs a recipe. Impure: it reads the file system.
func searchDirectory(cfg *config, workDir string, fsys sys.FS) (string, error) {
	if cfg.show != "" {
		return searchForRecipe(&cfg.show, workDir, fsys, "--show")
	}
	if cfg.usage != "" {
		return searchForRecipe(&cfg.usage, workDir, fsys, "--usage")
	}
	if cfg.list && len(cfg.words) > 0 && isDirectory(fsys, resolve(workDir, cfg.words[0])) {
		dir := resolve(workDir, cfg.words[0])
		cfg.words = cfg.words[1:]
		return dir, nil
	}
	if len(cfg.words) == 0 {
		return workDir, nil
	}
	dir, recipe, ok := splitSearch(cfg.words[0])
	if !ok {
		return workDir, nil
	}
	cfg.words = cfg.words[1:]
	if recipe != "" {
		cfg.words = append([]string{recipe}, cfg.words...)
	}
	return resolve(workDir, dir), nil
}

// searchForRecipe is searchDirectory for the recipe that a flag names: word is
// rewritten to the recipe, and the directory is returned. A word that names a
// directory is an error, since --show and --usage need a recipe. Impure.
func searchForRecipe(word *string, workDir string, fsys sys.FS, flag string) (string, error) {
	if isDirectory(fsys, resolve(workDir, *word)) {
		return "", usage("`%s` requires recipe", flag)
	}
	dir, recipe, ok := splitSearch(*word)
	if !ok {
		return workDir, nil
	}
	*word = recipe
	return resolve(workDir, dir), nil
}
