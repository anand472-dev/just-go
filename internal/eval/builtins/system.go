package builtins

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/sys"
)

// This file holds the builtins that read the environment the caller passed in,
// the machine and the process (os, arch, just_pid), the places a program is
// searched for (which, require), the shell (shell) and chance (choose). The
// ones that read only a field of the Host, or are functions of their arguments
// alone, do no I/O. The ones that ask the Platform, the Random or the Shell of
// the Host go through the interfaces of internal/sys.

// failWith is the error function: its value is an error whose text is the
// argument. It is pure.
func failWith(args []string) (string, error) { return "", errors.New(args[0]) }

// envLookup returns the value of the variable key in e. A variable that is not
// there is def, if the call gave one, else an error. A value that is not
// Unicode is an error. It is pure apart from reading e.
func envLookup(e sys.Env, key string, def *string) (string, error) {
	if value, ok := e.LookupEnv(key); ok {
		if !utf8.ValidString(value) {
			return "", fmt.Errorf("environment variable `%s` is not Unicode: `%s`", key, lossy(value))
		}
		return value, nil
	}
	if def != nil {
		return *def, nil
	}
	return "", fmt.Errorf("environment variable `%s` not present", key)
}

// envFunc is env: the value of the variable named by the first argument, else
// the second, if there is one. It reads h.Env.
func envFunc(h Host, args []string) (string, error) {
	var def *string
	if len(args) > 1 {
		def = &args[1]
	}
	return envLookup(h.Env, args[0], def)
}

// envVar is env_var: the value of the variable the argument names, which must
// be set. It reads h.Env.
func envVar(h Host, args []string) (string, error) { return envLookup(h.Env, args[0], nil) }

// envVarOrDefault is env_var_or_default: the value of the variable the first
// argument names, else the second argument. It reads h.Env.
func envVarOrDefault(h Host, args []string) (string, error) {
	return envLookup(h.Env, args[0], &args[1])
}

// rustOS is the name Rust's std::env::consts::OS gives the operating system
// Go calls goos. It is the name itself unless the two differ.
var rustOS = map[string]string{"darwin": "macos", "wasip1": "wasi"}

// rustArch is the name Rust's std::env::consts::ARCH gives the architecture
// Go calls goarch. It is the name itself unless the two differ.
var rustArch = map[string]string{
	"386": "x86", "amd64": "x86_64", "arm64": "aarch64", "loong64": "loongarch64", "mipsle": "mips",
	"mips64le": "mips64", "ppc64": "powerpc64", "ppc64le": "powerpc64", "wasm": "wasm32",
}

// rustFamily is the name Rust's std::env::consts::FAMILY gives the operating
// system Go calls goos, when it is not "unix".
var rustFamily = map[string]string{"windows": "windows", "js": "wasm", "wasip1": "wasm"}

// renamed returns names[name], or name if names does not have it. It is pure.
func renamed(names map[string]string, name string) string {
	if other, ok := names[name]; ok {
		return other
	}
	return name
}

// osName is os: the operating system, named as Rust names it. It asks h.Platform.
func osName(h Host, _ []string) (string, error) {
	return renamed(rustOS, h.Platform.OS()), nil
}

// osFamily is os_family: `unix`, `windows` or `wasm`. It asks h.Platform.
func osFamily(h Host, _ []string) (string, error) {
	if family, ok := rustFamily[h.Platform.OS()]; ok {
		return family, nil
	}
	return "unix", nil
}

// arch is arch: the processor architecture, named as Rust names it. It asks
// h.Platform.
func arch(h Host, _ []string) (string, error) {
	return renamed(rustArch, h.Platform.Arch()), nil
}

// numCPUs is num_cpus: the number of logical processors. It asks h.Platform.
func numCPUs(h Host, _ []string) (string, error) {
	return strconv.Itoa(h.Platform.NumCPU()), nil
}

// justPID is just_pid: the number of the running process. It asks h.Platform.
func justPID(h Host, _ []string) (string, error) {
	return strconv.Itoa(h.Platform.PID()), nil
}

// justExecutable is just_executable: the path of the running program, which
// must be Unicode. It asks h.Platform.
func justExecutable(h Host, _ []string) (string, error) {
	path, err := h.Platform.Executable()
	switch {
	case err != nil:
		return "", fmt.Errorf("could not get current `just` executable: %s", sys.ErrorText(err))
	case !utf8.ValidString(path):
		return "", fmt.Errorf("executable path is not valid Unicode: `%s`", lossy(path))
	}
	return path, nil
}

// justVersion is just_version: the version of upstream whose behavior is
// followed. It does no I/O.
var justVersion = field(func(h Host) string { return h.Version })

// numJobs is num_jobs: the number of recipes allowed to run at once, or the
// empty string when no limit was set. It does no I/O.
var numJobs = field(func(h Host) string {
	if h.Jobs == 0 {
		return ""
	}
	return strconv.Itoa(h.Jobs)
})

// isDependency is is_dependency: whether the recipe the call is part of runs
// as the dependency of another. It does no I/O.
var isDependency = field(func(h Host) string { return strconv.FormatBool(h.IsDependency) })

// recipeName is recipe_name: the name of the recipe the call is part of, an
// error outside a recipe. It does no I/O.
func recipeName(h Host, _ []string) (string, error) {
	if h.Recipe == "" {
		return "", errors.New("`recipe_name()` can only be used within a recipe")
	}
	return h.Recipe, nil
}

// shell is shell: it runs the first argument through the shell of the
// justfile, with the others as its arguments, and returns what it wrote. It
// runs a process through h.Shell.
func shell(h Host, args []string) (string, error) { return h.Shell(args[0], args[1:]) }

// which is which: the path of the first file the name names that can be run,
// or the empty string if there is none. It asks h.Platform.
func which(h Host, args []string) (string, error) {
	return findExecutable(h, args[0])
}

// require is require: what which gives, except that no file is an error. It
// asks h.Platform.
func require(h Host, args []string) (string, error) {
	found, err := findExecutable(h, args[0])
	if err == nil && found == "" {
		return "", fmt.Errorf("could not find executable `%s`", args[0])
	}
	return found, err
}

// findExecutable returns the first path, in the order upstream tries them, that
// h.Platform says can be run, or the empty string if none can. A name of one
// component is looked for in every directory of PATH, and a name of several is
// the path itself. A relative path starts from the working directory of h, and
// every path is cleaned. It asks h.Platform.
func findExecutable(h Host, name string) (string, error) {
	st := hostPathStyle(h.Platform.OS())
	paths, err := searchPaths(h, st, name)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		candidates, err := withExtensions(h, st, pathClean(st, pathJoin(st, h.WorkDir, path)))
		if err != nil {
			return "", err
		}
		for _, candidate := range candidates {
			if h.Platform.IsExecutable(candidate) {
				return candidate, nil
			}
		}
	}
	return "", nil
}

// searchPaths returns the paths to try for name, in order: for a name of one
// component, the name in each directory of PATH, else name itself. It is an
// error for a name of no components, or when PATH is not set or not Unicode.
// It reads h.Env.
func searchPaths(h Host, st pathStyle, name string) ([]string, error) {
	switch n := len(components(st, name)); {
	case n == 0:
		return nil, errors.New("empty command")
	case n > 1:
		return []string{name}, nil
	}
	list, ok := h.Env.LookupEnv("PATH")
	switch {
	case !ok:
		return nil, errors.New("failed to retrieve `PATH` environment variable: environment variable not found")
	case !utf8.ValidString(list):
		return nil, fmt.Errorf("failed to retrieve `PATH` environment variable: "+
			"environment variable was not valid unicode: %s", debugText(list))
	}
	dirs := splitSearchPath(st, list)
	paths := make([]string, len(dirs))
	for i, dir := range dirs {
		paths[i] = pathJoin(st, dir, name)
	}
	return paths, nil
}

// splitSearchPath splits the value of PATH into its directories, as Rust's
// env::split_paths does: at every `:` on Unix, and on Windows at every `;`
// that is not inside double quotes, which are dropped. It is pure.
func splitSearchPath(st pathStyle, list string) []string {
	if st == unixPath {
		return strings.Split(list, ":")
	}
	var dirs []string
	var dir strings.Builder
	quoted := false
	for i := 0; i < len(list); i++ {
		switch c := list[i]; {
		case c == '"':
			quoted = !quoted
		case c == ';' && !quoted:
			dirs = append(dirs, dir.String())
			dir.Reset()
		default:
			dir.WriteByte(c)
		}
	}
	return append(dirs, dir.String())
}

// withExtensions returns path and, on Windows, when path has no extension,
// path with each extension of PATHEXT. It is an error for PATHEXT to be not
// Unicode or to have an entry that does not start with `.`. It reads h.Env.
func withExtensions(h Host, st pathStyle, path string) ([]string, error) {
	candidates := []string{path}
	if _, has := pathExtension(st, path); st != windowsPath || has {
		return candidates, nil
	}
	list, ok := h.Env.LookupEnv("PATHEXT")
	if !ok {
		return candidates, nil
	}
	if !utf8.ValidString(list) {
		return nil, fmt.Errorf("`PATHEXT` environment variable is not valid Unicode: %s", lossy(list))
	}
	for _, entry := range strings.Split(list, ";") {
		ext, dotted := strings.CutPrefix(entry, ".")
		if !dotted {
			return nil, fmt.Errorf("`PATHEXT` entry `%s` does not start with `.`", entry)
		}
		candidates = append(candidates, withExtension(st, path, ext))
	}
	return candidates, nil
}

// withExtension returns path with the extension ext, which path does not have,
// as Rust's with_extension does: a path with no file name, and an empty
// extension, change nothing. It is pure.
func withExtension(st pathStyle, path, ext string) string {
	if _, ok := pathFileName(st, path); !ok || ext == "" {
		return path
	}
	return path + "." + ext
}

// choose is choose: a string of as many characters as the first argument
// says, each drawn from the second, which must have no character twice. It
// reads h.Random.
func choose(h Host, args []string) (string, error) {
	alphabet, err := alphabetOf(args[1])
	if err != nil {
		return "", err
	}
	count, reason := parseUsize(args[0])
	if reason != "" {
		return "", fmt.Errorf("failed to parse `%s` as positive integer: %s", args[0], reason)
	}
	return draw(h.Random, alphabet, count)
}

// alphabetOf returns the characters of s, which must be some and different. It
// is pure.
func alphabetOf(s string) ([]rune, error) {
	if s == "" {
		return nil, errors.New("empty alphabet")
	}
	seen := make(map[rune]bool, len(s))
	alphabet := make([]rune, 0, len(s))
	for _, c := range s {
		if seen[c] {
			return nil, fmt.Errorf("alphabet contains repeated character `%c`", c)
		}
		seen[c] = true
		alphabet = append(alphabet, c)
	}
	return alphabet, nil
}

// parseUsize reads s as Rust's usize::from_str does: decimal digits with an
// optional leading `+`. A string it cannot read comes back with the reason in
// Rust's words, in the second result. It is pure.
func parseUsize(s string) (uint64, string) {
	if s == "" {
		return 0, "cannot parse integer from empty string"
	}
	digits := s
	if len(s) > 1 {
		digits = strings.TrimPrefix(s, "+")
	}
	var n uint64
	for i := 0; i < len(digits); i++ {
		d := uint64(digits[i]) - '0'
		if d > 9 {
			return 0, "invalid digit found in string"
		}
		if n > (math.MaxUint64-d)/10 {
			return 0, "number too large to fit in target type"
		}
		n = n*10 + d
	}
	return n, ""
}

// draw returns count characters, each chosen at random from alphabet. It
// reads r.
func draw(r sys.Random, alphabet []rune, count uint64) (string, error) {
	var out strings.Builder
	for ; count > 0; count-- {
		i, err := randomBelow(r, uint64(len(alphabet)))
		if err != nil {
			return "", fmt.Errorf("failed to read random bytes: %s", sys.ErrorText(err))
		}
		out.WriteRune(alphabet[i])
	}
	return out.String(), nil
}

// randomBelow returns a number from 0 up to but not including n, each equally
// likely: it reads 8 bytes at a time and tries again when they fall in the
// last, short, stretch of numbers. n is not zero. It reads r.
func randomBelow(r sys.Random, n uint64) (uint64, error) {
	zone := math.MaxUint64 / n * n
	var b [8]byte
	for {
		if _, err := r.Read(b[:]); err != nil {
			return 0, err
		}
		var v uint64
		for _, c := range b {
			v = v<<8 | uint64(c)
		}
		if v < zone {
			return v % n, nil
		}
	}
}

// lossy returns s with each invalid sequence of bytes replaced by U+FFFD, as
// Rust's to_string_lossy does: a sequence is the bytes that begin a character
// and then go wrong. It is pure.
func lossy(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			size = invalidLength(s[i:])
			r = utf8.RuneError
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// continuation is the range a byte after the first of a multibyte character
// may take, by that first byte; a byte outside it ends the invalid sequence.
// It is pure.
func continuation(first byte) (lo, hi byte) {
	switch first {
	case 0xE0:
		return 0xA0, 0xBF
	case 0xED:
		return 0x80, 0x9F
	case 0xF0:
		return 0x90, 0xBF
	case 0xF4:
		return 0x80, 0x8F
	}
	return 0x80, 0xBF
}

// width is how many bytes a character that begins with first takes, or 1 for
// a byte that cannot begin one. It is pure.
func width(first byte) int {
	switch {
	case first >= 0xC2 && first <= 0xDF:
		return 2
	case first >= 0xE0 && first <= 0xEF:
		return 3
	case first >= 0xF0 && first <= 0xF4:
		return 4
	}
	return 1
}

// invalidLength is how many bytes at the start of s, which begins with a
// sequence that is not UTF-8, Rust counts as one invalid sequence: the bytes
// that could be the start of a character. It is pure.
func invalidLength(s string) int {
	lo, hi := continuation(s[0])
	n := 1
	for n < width(s[0]) && n < len(s) && s[n] >= lo && s[n] <= hi {
		n++
		lo, hi = 0x80, 0xBF
	}
	return n
}

// debugText writes s as Rust's Debug does for a string of bytes: in double
// quotes, a byte that is not UTF-8 as `\xFF`, and the characters Rust escapes
// as it does. It is pure.
func debugText(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02X`, s[i])
		default:
			b.WriteString(debugRune(r))
		}
		i += size
	}
	b.WriteByte('"')
	return b.String()
}

// debugRune writes the character r as Rust's char::escape_debug does, except
// that a single quote is not escaped, as in a string. It is pure.
func debugRune(r rune) string {
	switch r {
	case '\t':
		return `\t`
	case '\r':
		return `\r`
	case '\n':
		return `\n`
	case '\\', '"':
		return `\` + string(r)
	case 0:
		return `\0`
	}
	if strconv.IsPrint(r) {
		return string(r)
	}
	return fmt.Sprintf(`\u{%x}`, r)
}
