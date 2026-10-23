// Package completion offers the words a shell may complete a command line
// with, and writes the scripts that ask for them. It is pure: the caller
// gathers the Catalog from the justfile and the directory.
//
// The candidates follow the order of the corpus: after the flag that takes
// them, the names it wants; otherwise the recipes, the variables as `name=`,
// `.`, the files of the directory, and the long flags. Each line is a candidate,
// with a tab and its description when it has one.
package completion

import (
	"fmt"
	"sort"
	"strings"
)

// Recipe is a recipe as completion sees it.
type Recipe struct {
	Name    string
	Doc     string
	Groups  []string
	Private bool
}

// Catalog is what the command can complete: its recipes, its public variables,
// the names in the working directory, and its long flags, without the "--".
type Catalog struct {
	Recipes   []Recipe
	Variables []string
	Files     []string
	Flags     []string
}

// Candidates returns the lines to offer for the last of words, the command
// name first and the word being completed last, given the flag before it.
// Pure.
func Candidates(words []string, c Catalog) []string {
	cur, prev := "", ""
	if n := len(words); n > 0 {
		cur = words[n-1]
	}
	if n := len(words); n > 1 {
		prev = words[n-2]
	}
	switch prev {
	case "--set":
		return plain(cur, sorted(c.Variables))
	case "--show":
		return recipes(cur, c.Recipes)
	case "--group":
		return plain(cur, groups(c.Recipes))
	case "--usage":
		return append(recipes(cur, c.Recipes), positional(cur, c)...)
	}
	return positional(cur, c)
}

// positional is the candidates for a word that names no flag's value: the
// recipes, the variables, `.`, the files and the long flags that start with cur.
// Pure.
func positional(cur string, c Catalog) []string {
	out := recipes(cur, c.Recipes)
	for _, v := range sorted(c.Variables) {
		if strings.HasPrefix(v+"=", cur) {
			out = append(out, v+"=")
		}
	}
	if strings.HasPrefix(".", cur) {
		out = append(out, ".")
	}
	out = append(out, plain(cur, c.Files)...)
	for _, f := range c.Flags {
		if strings.HasPrefix("--"+f, cur) {
			out = append(out, "--"+f)
		}
	}
	return out
}

// recipes are the public recipes whose names start with cur, sorted by name,
// each with its doc comment after a tab. Pure.
func recipes(cur string, rs []Recipe) []string {
	var public []Recipe
	for _, r := range rs {
		if !r.Private && strings.HasPrefix(r.Name, cur) {
			public = append(public, r)
		}
	}
	sort.SliceStable(public, func(i, j int) bool { return public[i].Name < public[j].Name })
	out := make([]string, len(public))
	for i, r := range public {
		out[i] = r.Name
		if r.Doc != "" {
			out[i] += "\t" + r.Doc
		}
	}
	return out
}

// groups are the distinct groups of the recipes, sorted. Pure.
func groups(rs []Recipe) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		for _, g := range r.Groups {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	sort.Strings(out)
	return out
}

// plain are the names in list that start with cur, in the order given. Pure.
func plain(cur string, list []string) []string {
	var out []string
	for _, n := range list {
		if strings.HasPrefix(n, cur) {
			out = append(out, n)
		}
	}
	return out
}

// sorted is a sorted copy of list. Pure.
func sorted(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}

// Script is the completion script for shell, which asks the command named
// just-go for its candidates by running it with JUST_COMPLETE set. The shells
// it covers are bash, zsh and powershell; ok is false for any other. Pure.
func Script(shell string) (string, bool) {
	switch strings.ToLower(shell) {
	case "bash":
		return bashScript, true
	case "zsh":
		return zshScript, true
	case "powershell":
		return powershellScript, true
	}
	return "", false
}

const bashScript = `_just_go_complete() {
    local IFS=$'\n'
    COMPREPLY=($(JUST_COMPLETE=bash "${COMP_WORDS[0]}" -- "${COMP_WORDS[@]}" 2>/dev/null | cut -f1))
}
complete -F _just_go_complete just-go
`

const zshScript = `#compdef just-go

_just_go() {
    local -a candidates
    candidates=("${(@f)$(JUST_COMPLETE=zsh "${words[1]}" -- "${words[@]}" 2>/dev/null)}")
    compadd -- "${candidates[@]%%$'\t'*}"
}

compdef _just_go just-go
`

const powershellScript = `Register-ArgumentCompleter -Native -CommandName just-go -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $words = @($commandAst.CommandElements | ForEach-Object { $_.ToString() })
    if ($wordToComplete -eq '') { $words += '' }
    $env:JUST_COMPLETE = 'powershell'
    try {
        $candidates = @(& just-go -- @words 2>$null)
    } finally {
        Remove-Item Env:JUST_COMPLETE -ErrorAction SilentlyContinue
    }
    foreach ($line in $candidates) {
        $parts = $line -split "` + "`" + `t", 2
        $text = $parts[0]
        $tip = if ($parts.Count -gt 1) { $parts[1] } else { $text }
        [System.Management.Automation.CompletionResult]::new($text, $text, 'ParameterValue', $tip)
    }
}
`

// FlagValue is the word after the last occurrence of the flag name (given
// without dashes) in words, and whether there is one. Pure.
func FlagValue(words []string, name string) (string, bool) {
	value, found := "", false
	flag := fmt.Sprintf("--%s", name)
	for i := 0; i+1 < len(words); i++ {
		if words[i] == flag {
			value, found = words[i+1], true
		}
	}
	return value, found
}
