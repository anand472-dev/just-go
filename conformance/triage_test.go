package conformance

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
)

// triagePath is the triage of the failing cases, written by
// scripts/triage.py (see triage/README.md).
const triagePath = "triage/groups.json"

// triage mirrors triage/groups.json: the counts of one full conformance run
// and every failing case of it, by group, with the reason it is in the group.
type triage struct {
	Counts struct {
		Pass  int `json:"pass"`
		Fail  int `json:"fail"`
		Skip  int `json:"skip"`
		Total int `json:"total"`
	} `json:"counts"`
	GroupSizes map[string]int               `json:"group_sizes"`
	Groups     map[string]map[string]string `json:"groups"`
}

// groupName is the closed set of group names scripts/triage.py assigns.
var groupName = regexp.MustCompile(`^(grammar|attribute|conditional|shebang|` +
	`module|dotenv|completions|core-defect|other|(setting|builtin|cli|unstable):[a-z0-9-]+)$`)

// parseTriage decodes groups.json, rejecting unknown fields. Pure.
func parseTriage(data []byte) (triage, error) {
	var doc triage
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return triage{}, fmt.Errorf("%s: %w", triagePath, err)
	}
	return doc, nil
}

// checkTriage requires that the triage partitions the run: every failing case
// is in exactly one group under a valid name with a reason, the groups hold
// exactly the failing count, and pass, fail, and skip add up to the corpus.
// A failing case must be a corpus case that is neither an oracle exception
// (those are skipped) nor in the baseline (those pass). Pure.
func checkTriage(doc triage, corpus []string, exceptions map[string]string, baseline []string) error {
	known := make(map[string]bool, len(corpus))
	for _, id := range corpus {
		known[id] = true
	}
	passing := make(map[string]bool, len(baseline))
	for _, id := range baseline {
		passing[id] = true
	}
	c := doc.Counts
	errs := []error{checkCounts(c.Pass, c.Fail, c.Skip, c.Total, len(corpus), len(exceptions), len(baseline))}
	group := map[string]string{}
	for name, cases := range doc.Groups {
		errs = append(errs, checkGroup(name, cases, doc.GroupSizes[name]))
		for id := range cases {
			errs = append(errs, checkFailing(id, name, group[id], known[id], exceptions[id] != "", passing[id]))
			group[id] = name
		}
	}
	if len(doc.GroupSizes) != len(doc.Groups) {
		errs = append(errs, fmt.Errorf("group_sizes names %d groups, groups has %d", len(doc.GroupSizes), len(doc.Groups)))
	}
	if len(group) != c.Fail {
		errs = append(errs, fmt.Errorf("the groups hold %d cases, but %d failed", len(group), c.Fail))
	}
	return errors.Join(errs...)
}

// checkCounts requires pass, fail, and skip to add up to total, total to be
// the corpus size, skip to be the number of oracle exceptions, and pass to be
// the number of baseline cases. Pure.
func checkCounts(pass, fail, skip, total, corpus, exceptions, baseline int) error {
	switch {
	case pass+fail+skip != total:
		return fmt.Errorf("counts: %d pass + %d fail + %d skip is not the total %d", pass, fail, skip, total)
	case total != corpus:
		return fmt.Errorf("counts: total %d, but the corpus has %d cases", total, corpus)
	case skip != exceptions:
		return fmt.Errorf("counts: %d skipped, but there are %d oracle exceptions", skip, exceptions)
	case pass != baseline:
		return fmt.Errorf("counts: %d pass, but the baseline has %d cases", pass, baseline)
	}
	return nil
}

// checkGroup requires a valid name, a size that matches group_sizes, and a
// reason for every case. Pure.
func checkGroup(name string, cases map[string]string, size int) error {
	if !groupName.MatchString(name) {
		return fmt.Errorf("group %q is not a triage group name", name)
	}
	if len(cases) == 0 || len(cases) != size {
		return fmt.Errorf("group %q holds %d cases, group_sizes says %d", name, len(cases), size)
	}
	for id, reason := range cases {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("case %q in group %q has no reason", id, name)
		}
	}
	return nil
}

// checkFailing requires the failing case id, in group name, to be in no
// other group (previous is its earlier group, or ""), to be a corpus case,
// and to be neither an oracle exception nor a baseline case. Pure.
func checkFailing(id, name, previous string, known, exception, baseline bool) error {
	switch {
	case previous != "":
		return fmt.Errorf("case %q is in groups %q and %q", id, previous, name)
	case !known:
		return fmt.Errorf("case %q in group %q is not a corpus case", id, name)
	case exception:
		return fmt.Errorf("case %q in group %q is an oracle exception, which is skipped", id, name)
	case baseline:
		return fmt.Errorf("case %q in group %q is in the baseline, which passes", id, name)
	}
	return nil
}

// TestTriageCoversEveryFailure checks the committed triage against the corpus,
// the oracle exceptions, and the baseline: every failing case is in exactly
// one group, and the groups hold exactly the failing count.
func TestTriageCoversEveryFailure(t *testing.T) {
	fsys := sys.OSFS{}
	data, err := fsys.ReadFile(triagePath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseTriage(data)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range loadCorpus(t, fsys) {
		ids = append(ids, c.ID)
	}
	baseline, err := fsys.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTriage(doc, ids, loadExceptions(t, fsys), strings.Fields(string(baseline))); err != nil {
		t.Fatal(err)
	}
	if doc.GroupSizes["core-defect"] != 0 {
		t.Errorf("%d core-defect cases: the implemented subset has defects to fix", doc.GroupSizes["core-defect"])
	}
}

func TestParseTriageRejectsUnknownFields(t *testing.T) {
	if _, err := parseTriage([]byte(`{"counts":{},"extra":1}`)); err == nil {
		t.Fatal("want an error for an unknown field")
	}
}

// TestCheckTriage feeds checkTriage one valid triage and one broken copy per
// rule.
func TestCheckTriage(t *testing.T) {
	corpus := []string{"a#1", "b#1", "c#1", "d#1"}
	exceptions := map[string]string{"d#1": "fifo: x"}
	baseline := []string{"a#1"}
	valid := func() triage {
		var doc triage
		doc.Counts.Pass, doc.Counts.Fail, doc.Counts.Skip, doc.Counts.Total = 1, 2, 1, 4
		doc.GroupSizes = map[string]int{"grammar": 1, "cli:dump": 1}
		doc.Groups = map[string]map[string]string{"grammar": {"b#1": "why"}, "cli:dump": {"c#1": "why"}}
		return doc
	}
	if err := checkTriage(valid(), corpus, exceptions, baseline); err != nil {
		t.Fatalf("valid triage: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*triage)
		want   string
	}{
		{"counts do not add up", func(d *triage) { d.Counts.Pass = 2 }, "is not the total"},
		{"pass is not the baseline", func(d *triage) { d.Counts.Pass, d.Counts.Fail = 2, 1 }, "the baseline has"},
		{"total is not the corpus", func(d *triage) { d.Counts.Total, d.Counts.Pass = 5, 2 }, "the corpus has"},
		{"skip is not the exceptions", func(d *triage) { d.Counts.Skip, d.Counts.Pass = 0, 2 }, "oracle exceptions"},
		{"bad group name", func(d *triage) { rename(d, "grammar", "Grammar") }, "not a triage group name"},
		{"size mismatch", func(d *triage) { d.GroupSizes["grammar"] = 3 }, "group_sizes says"},
		{"empty reason", func(d *triage) { d.Groups["grammar"]["b#1"] = " " }, "has no reason"},
		{"case in two groups", func(d *triage) {
			d.Groups["cli:dump"]["b#1"] = "why"
			d.GroupSizes["cli:dump"] = 2
		}, "is in groups"},
		{"unknown case", func(d *triage) { move(d, "b#1", "z#1") }, "not a corpus case"},
		{"exception", func(d *triage) { move(d, "b#1", "d#1") }, "oracle exception"},
		{"baseline", func(d *triage) { move(d, "b#1", "a#1") }, "in the baseline"},
		{"missing size", func(d *triage) { delete(d.GroupSizes, "grammar"); d.GroupSizes["other"] = 1 }, "group_sizes says"},
		{"extra size", func(d *triage) { d.GroupSizes["other"] = 1 }, "group_sizes names"},
		{"fail count", func(d *triage) { d.Counts.Fail, d.Counts.Pass = 3, 0 }, "but 3 failed"},
	} {
		doc := valid()
		tc.mutate(&doc)
		if err := checkTriage(doc, corpus, exceptions, baseline); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

// rename moves group from to group to, size included.
func rename(d *triage, from, to string) {
	d.Groups[to], d.GroupSizes[to] = d.Groups[from], d.GroupSizes[from]
	delete(d.Groups, from)
	delete(d.GroupSizes, from)
}

// move replaces case from with case to in the grammar group.
func move(d *triage, from, to string) {
	d.Groups["grammar"][to] = d.Groups["grammar"][from]
	delete(d.Groups["grammar"], from)
}
