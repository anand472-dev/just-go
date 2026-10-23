package loader

import (
	"fmt"
	"path/filepath"
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// The strict path rule refuses a file that an import, a module or a dotenv
// setting names when, after symbolic links are resolved, it lies outside the
// directory of the root justfile. Upstream allows it: this is the documented
// divergence the opt-out lifts (--allow-outside-project-paths, and
// Options.AllowOutsidePaths). A file that does not exist is not refused here;
// the loader reports it as missing.

// loadEnv is what loading a justfile needs from outside: the file system, the
// home directory an import under `~/` starts from, the directory of the root
// justfile for the strict path rule, and whether that rule is lifted.
type loadEnv struct {
	fsys  sys.FS
	home  string
	root  string
	allow bool
}

// check refuses path under the strict path rule, unless the rule is lifted.
func (e *loadEnv) check(path string) error {
	if e.allow {
		return nil
	}
	return CheckPath(e.root, path, e.fsys.Canonicalize)
}

// CheckPath is the strict path rule for one path: an error when path, with its
// symbolic links resolved, is not inside root, which is resolved the same way.
// A path or root that cannot be resolved is not refused, since the reader reports
// it. resolve is the file system's Canonicalize. Pure apart from resolve.
func CheckPath(root, path string, resolve func(string) (string, error)) error {
	real, err := resolve(path)
	if err != nil {
		return nil
	}
	realRoot, err := resolve(root)
	if err != nil {
		return nil
	}
	if within(realRoot, real) {
		return nil
	}
	return fmt.Errorf("path `%s` is outside the project directory `%s`; "+
		"--allow-outside-project-paths lifts this rule", path, root)
}

// within reports whether path is dir or inside it. Both are cleaned absolute
// paths. It is pure.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
