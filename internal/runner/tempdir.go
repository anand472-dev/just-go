package runner

import "gitea.com/wimwian/just-go/internal/settings"

// tempdirOf is the directory script recipes write their files in: --tempdir when
// the plan gives one, else the set tempdir setting, or nil for the default. It
// is pure.
func tempdirOf(plan Plan, set settings.Resolved) *string {
	if plan.Tempdir != "" {
		dir := plan.Tempdir
		return &dir
	}
	return set.Tempdir
}
