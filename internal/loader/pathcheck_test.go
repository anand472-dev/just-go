package loader

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// links resolves the paths in the map to their targets, and any other path to
// itself, as a file system does for symbolic links.
func links(m map[string]string) func(string) (string, error) {
	return func(p string) (string, error) {
		if t, ok := m[p]; ok {
			return t, nil
		}
		return p, nil
	}
}

func TestCheckPathAllowsPathsInsideTheRoot(t *testing.T) {
	for _, p := range []string{"/proj", "/proj/sub/x.just", "/proj/a/../b"} {
		if err := CheckPath("/proj", p, links(nil)); err != nil {
			t.Errorf("CheckPath(/proj, %s) = %v; want none", p, err)
		}
	}
}

func TestCheckPathRefusesPathsOutsideTheRoot(t *testing.T) {
	for _, p := range []string{"/other/x", "/proj/../x", "/projx/y"} {
		err := CheckPath("/proj", p, links(nil))
		if err == nil || !strings.Contains(err.Error(), "outside the project directory `/proj`") || !strings.Contains(err.Error(), "--allow-outside-project-paths") {
			t.Errorf("CheckPath(/proj, %s) = %v; want the strict-path refusal", p, err)
		}
	}
}

// A symbolic link is judged by what it points to, not by where it sits.
func TestCheckPathFollowsSymbolicLinks(t *testing.T) {
	inside := map[string]string{"/proj/link": "/proj/real"}
	if err := CheckPath("/proj", "/proj/link", links(inside)); err != nil {
		t.Errorf("a link inside to a file inside: %v", err)
	}
	escape := map[string]string{"/proj/link": "/etc/passwd"}
	if err := CheckPath("/proj", "/proj/link", links(escape)); err == nil {
		t.Error("a link inside to a file outside was allowed")
	}
}

// A path or root that cannot be resolved is not refused here.
func TestCheckPathLeavesMissingPathsToTheReader(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("missing") }
	if err := CheckPath("/proj", "/elsewhere", missing); err != nil {
		t.Errorf("a missing path was refused: %v", err)
	}
	okPath := func(p string) (string, error) {
		if p == "/proj" {
			return "", errors.New("no root")
		}
		return p, nil
	}
	if err := CheckPath("/proj", "/elsewhere", okPath); err != nil {
		t.Errorf("a path with an unresolvable root was refused: %v", err)
	}
}

// An import outside the project is refused at its string, and allowed with the
// opt-out.
func TestImportOutsideTheProjectIsRefusedUnlessAllowed(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "import '../lib.just'\n", "/lib.just": "a:\n  echo\n"}
	_, err := Load(fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Line != 1 || !strings.Contains(de.Msg, "outside the project directory") {
		t.Errorf("Load = %+v; want the strict-path refusal at the import", err)
	}
	jf, err := LoadWith(fsys, "/proj/justfile", Options{AllowOutsidePaths: true})
	if err != nil || len(jf.Recipes) != 1 {
		t.Errorf("LoadWith allowed = %v, %v; want the import loaded", jf, err)
	}
}

// A module outside the project is refused at its name, and allowed with the
// opt-out.
func TestModuleOutsideTheProjectIsRefusedUnlessAllowed(t *testing.T) {
	fsys := sysfake.FakeFS{"/proj/justfile": "mod foo '../foo.just'\n", "/foo.just": "a:\n  echo\n"}
	_, err := Load(fsys, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Line != 1 || !strings.Contains(de.Msg, "outside the project directory") {
		t.Errorf("Load = %+v; want the strict-path refusal at the module", err)
	}
	if _, err := LoadWith(fsys, "/proj/justfile", Options{AllowOutsidePaths: true}); err != nil {
		t.Errorf("LoadWith allowed = %v; want the module loaded", err)
	}
}

// A file that is not there is reported as missing, not as outside the project.
func TestMissingImportOutsideTheProjectIsStillMissing(t *testing.T) {
	_, err := Load(sysfake.FakeFS{"/proj/justfile": "import '../nope'\n"}, "/proj/justfile")
	var de *diag.Error
	if !asDiag(err, &de) || de.Msg != "could not find source file for import" {
		t.Errorf("Load = %+v; want the missing-import error", err)
	}
}
