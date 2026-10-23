package analyzer

import (
	"errors"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
)

// conflicts lists, for each setting, the settings it cannot be set along
// with while it is on (upstream's Setting::conflicts). A setting that takes a
// value is always on; a boolean one is on when it is true.
var conflicts = map[string][]string{
	"dotenv-command":    {"dotenv-filename", "dotenv-load", "dotenv-path", "dotenv-required"},
	"dotenv-filename":   {"dotenv-command"},
	"dotenv-load":       {"dotenv-command"},
	"dotenv-path":       {"dotenv-command"},
	"dotenv-required":   {"dotenv-command"},
	"no-cd":             {"working-directory"},
	"working-directory": {"no-cd"},
}

// settingChecker checks `set` statements one at a time, in source order,
// against those it has seen.
type settingChecker struct {
	jf   *ast.Justfile
	seen map[string]ast.SettingSite
}

func newSettingChecker(jf *ast.Justfile) *settingChecker {
	return &settingChecker{jf: jf, seen: make(map[string]ast.SettingSite, len(jf.Settings.Sites))}
}

// check reports site if it sets a setting a second time, or one that
// conflicts with an earlier setting, as upstream's analyze_set does. The
// error is located at the name of the later setting. It is pure apart from
// updating c.
func (c *settingChecker) check(site ast.SettingSite) *diag.Error {
	at := name(site.Line, site.Col, site.Name)
	if first, ok := c.seen[site.Name]; ok {
		return fail(c.jf, at, "setting `%s` first set on line %d is redefined on line %d", site.Name, first.Line, site.Line)
	}
	for _, other := range conflictsOf(site) {
		earlier, ok := c.seen[other]
		if ok && contains(conflictsOf(earlier), site.Name) {
			return fail(c.jf, at, "`%s` set on line %d is incompatible with `%s`", other, earlier.Line, site.Name)
		}
	}
	c.seen[site.Name] = site
	return nil
}

// conflictsOf returns the settings site cannot be set along with. It is
// pure.
func conflictsOf(site ast.SettingSite) []string {
	if !site.Active {
		return nil
	}
	return conflicts[site.Name]
}

// resolveSettings checks the expression settings as upstream does once the
// assignments are resolved: every call must name a function that takes that
// many arguments and every variable must be an assignment; then the
// expressions are evaluated in a const context, and the first that cannot be
// is reported. It is pure.
func resolveSettings(jf *ast.Justfile, overrides map[string]string) *diag.Error {
	globals := make(map[string]struct{}, len(jf.Assignments))
	for _, a := range jf.Assignments {
		globals[a.Name] = struct{}{}
	}
	check := func(ref ast.Expr) *diag.Error { return checkReference(jf, ref, globals, nil) }
	for _, e := range settings.Expressions(jf) {
		if err := references(e, check); err != nil {
			return err
		}
	}
	_, err := settings.ResolveWith(jf, overrides)
	var listed *settings.ListError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &listed):
		// Without `set lists` the file is refused for its first list feature,
		// as upstream does; with it, the list is not implemented.
		if refusal := checkListFeatures(jf); refusal != nil {
			return refusal
		}
		return listed.Diag
	}
	return err.(*diag.Error)
}

// checkListFeatures reports the first use of a feature of `set lists` unless
// the justfile sets it, as upstream does once the settings are evaluated. The
// uses are in the order the parser met them. It is pure.
func checkListFeatures(jf *ast.Justfile) *diag.Error {
	if jf.Settings.Lists || len(jf.ListUses) == 0 {
		return nil
	}
	use := jf.ListUses[0]
	return fail(jf, use.At, "%s", use.Feature)
}
