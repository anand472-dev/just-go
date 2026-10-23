package runner

import (
	"fmt"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// checkOverrides reports an override whose name is not a module path, or that
// names no assignment of jf, which upstream refuses before anything runs. The
// missing names are reported together, sorted, as upstream lists them. It is
// pure.
func CheckOverrides(jf *ast.Justfile, overrides map[string]string) error {
	var missing []string
	known := make(map[string]bool)
	for _, a := range jf.UniqueAssignments() {
		known[a.Name] = true
	}
	for _, name := range sortedKeys(overrides) {
		if !ast.ValidPath(name) {
			return overrideError(fmt.Sprintf("invalid override path `%s`", name))
		}
		if !known[name] {
			missing = append(missing, name)
		}
	}
	switch len(missing) {
	case 0:
		return nil
	case 1:
		return overrideError(fmt.Sprintf("variable `%s` overridden on the command line but not present in justfile", missing[0]))
	}
	quoted := make([]string, len(missing))
	for i, m := range missing {
		quoted[i] = "`" + m + "`"
	}
	return overrideError(fmt.Sprintf("variables %s overridden on the command line but not present in justfile", strings.Join(quoted, " and ")))
}

// sortedKeys returns the keys of m in byte order. Pure.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// overrideError is a usage diagnostic with msg, which is not a format.
func overrideError(msg string) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: msg}
}
