package builtins

import (
	"strings"
	"testing"
)

// semverVectors are the results upstream 1.59.0 gives for semver_matches. Each
// row is a version, a requirement, and either the result ("true" or "false") or
// the error text, which is the message upstream prints after "error: ". The
// rows were read from the oracle, not written from the semver crate's docs.
// Some requirements and versions hold control characters, which the oracle reads
// from a file so that the bytes are exact.
var semverVectors = []struct {
	version, requirement string
	want, err            string
}{
	{"0.1.0", ">=0.1.0", "true", ""},
	{"0.1.0", "=0.0.1", "false", ""},
	{"1.0.0", ">=1.0.0, <2", "true", ""},
	{"1.0.0-alpha", ">=1.0.0", "false", ""},
	{"1.0.0-alpha", ">=0.9.0-alpha, <1", "false", ""},
	{"1.0.0-alpha", "1.0.0-alpha", "true", ""},
	{"1.0.0-beta", ">1.0.0-alpha, <1.0.0-gamma", "true", ""},
	{"1.0.0-alpha.1", ">1.0.0-alpha", "true", ""},
	{"1.0.0-alpha.beta", ">1.0.0-alpha.1", "true", ""},
	{"1.0.0-2", ">1.0.0-10", "false", ""},
	{"1.0.0-10", ">1.0.0-2", "true", ""},
	{"1.2.9", "^1.2", "true", ""},
	{"1.3.0", "^1.2", "true", ""},
	{"2.0.0", "^1.2", "false", ""},
	{"0.2.5", "^0.2.3", "true", ""},
	{"0.3.0", "^0.2.3", "false", ""},
	{"0.0.3", "^0.0.3", "true", ""},
	{"0.0.4", "^0.0.3", "false", ""},
	{"1.2.9", "~1.2", "true", ""},
	{"1.3.0", "~1.2", "false", ""},
	{"1.2.2", "~1.2.3", "false", ""},
	{"1.2.3", "~1.2.3", "true", ""},
	{"2.0.0", "1.*", "false", ""},
	{"1.9.9", "1.*", "true", ""},
	{"1.0.0", "~1.0", "true", ""},
	{"1.0.0", "=1.0.0+build.5", "true", ""},
	{"1.0.0", "*", "true", ""},
	{"1.0.0-rc.1", "*", "false", ""},
	{"1.0.0", "x", "true", ""},
	{"1.0.0", "X", "true", ""},
	{"1.0.0", "<1.0.0", "false", ""},
	{"1.2.3", "<=1.2.3", "true", ""},
	{"1.2.3", "<=1.2.2", "false", ""},
	{"1.0.0-rc.1", "1.0.0-rc.1", "true", ""},
	{"1.0.0-rc.1", ">=1.0.0-rc.0, <1.0.0", "true", ""},
	{"1.0.0+build", ">=1.0.0", "true", ""},
	{"1.2.3", "=1.2", "true", ""},
	{"1.3.0", "=1.2", "false", ""},
	{"1.2.3", "1.2.x", "true", ""},
	{"1.2.3", ">=1.2.*", "true", ""},
	{"1.0.0", "1.*.3", "", "invalid semver requirement: unexpected character after wildcard in version req"},
	{"1.0.0", "1.x.3", "", "invalid semver requirement: unexpected character after wildcard in version req"},
	{"1.0.0", "*, >=1", "", "invalid semver requirement: wildcard req (*) must be the only comparator in the version req"},
	{"1.0.0", "*.1", "", "invalid semver requirement: unexpected character after wildcard in version req"},
	{"1.0.0", "", "", "invalid semver requirement: unexpected end of input while parsing major version number"},
	{"1.0.0", ">=1.0.0 ,", "", "invalid semver requirement: unexpected end of input while parsing major version number"},
	{"1.0.0", ">=1.0.0 >2", "", "invalid semver requirement: expected comma after patch version number, found '>'"},
	{"1.0.0", "=1.0.0+", "", "invalid semver requirement: empty identifier segment in build metadata"},
	{"1.0.0", "=1.0.0-", "", "invalid semver requirement: empty identifier segment in pre-release identifier"},
	{"1.2", ">=1", "", "invalid semver version: unexpected end of input while parsing minor version number"},
	{"x", ">=1", "", "invalid semver version: unexpected character 'x' while parsing major version number"},
	{"", ">=1", "", "invalid semver version: empty string, expected a semver version"},
	{"1.0.0+", "1.0.0", "", "invalid semver version: empty identifier segment in build metadata"},
	{"01.0.0", ">=1", "", "invalid semver version: invalid leading zero in major version number"},
	{"1.0.0", ">=01.0.0", "", "invalid semver requirement: invalid leading zero in major version number"},
	{"1.0.0-01", ">=1.0.0", "", "invalid semver version: invalid leading zero in pre-release identifier"},
	{"1.0.0-", ">=1.0.0", "", "invalid semver version: empty identifier segment in pre-release identifier"},
	{"1.0.0.0", ">=1.0.0", "", "invalid semver version: unexpected character '.' after patch version number"},
	{"18446744073709551616.0.0", ">=1", "", "invalid semver version: value of major version number exceeds u64::MAX"},
	{"1.0.0", ">=18446744073709551616", "", "invalid semver requirement: value of major version number exceeds u64::MAX"},
	{"1.0.0", "> =1.0.0", "", "invalid semver requirement: unexpected character '=' while parsing major version number"},
	{"1.0.0", " >= 1.0.0 ", "true", ""},
	{"1.0.0", ">=1.0.0 ,  <2.0.0", "true", ""},
	{"1.0.0", "!=1.0.0", "", "invalid semver requirement: unexpected character '!' while parsing major version number"},
	{"1.0.0", "=^1.0.0", "", "invalid semver requirement: unexpected character '^' while parsing major version number"},
	{"1.0.0", "1.0.0 1.0.0", "", "invalid semver requirement: expected comma after patch version number, found '1'"},
	{"1.0.0", "1.0.0-a_b", "", "invalid semver requirement: expected comma after pre-release identifier, found '_'"},
	{"1.0.0-a-b", "1.0.0-a-b", "true", ""},
	{"1.0.0", "1..0", "", "invalid semver requirement: unexpected character '.' while parsing minor version number"},
	{"1.0.0", ">=1.0.0\t", "", "invalid semver requirement: expected comma after patch version number, found '\\t'"},
	{"1.0.0", "1.0.0,1.0.0,1.0.0", "true", ""},
	{"1.", ">=1", "", "invalid semver version: unexpected end of input while parsing minor version number"},
	{"1.x.0", ">=1", "", "invalid semver version: unexpected character 'x' while parsing minor version number"},
	{"1.0", ">=1", "", "invalid semver version: unexpected end of input while parsing minor version number"},
	{"1x", ">=1", "", "invalid semver version: unexpected character 'x' after major version number"},
	{"1.0.0-.a", ">=1", "", "invalid semver version: empty identifier segment in pre-release identifier"},
	{"1.0.0-a..b", ">=1", "", "invalid semver version: empty identifier segment in pre-release identifier"},
	{"1.0.0-a.", ">=1", "", "invalid semver version: empty identifier segment in pre-release identifier"},
	{"1.0.0-a-", ">=1", "false", ""},
	{"1.0.0-a+b.", ">=1", "", "invalid semver version: empty identifier segment in build metadata"},
	{"1.0.0+a..b", ">=1", "", "invalid semver version: empty identifier segment in build metadata"},
	{"1.0.0+a+", ">=1", "", "invalid semver version: unexpected character '+' after build metadata"},
	{"1.0.0-a+", ">=1", "", "invalid semver version: empty identifier segment in build metadata"},
	{"1.0.0 ", ">=1", "", "invalid semver version: unexpected character ' ' after patch version number"},
	{"1.0.0-a b", ">=1", "", "invalid semver version: unexpected character ' ' after pre-release identifier"},
	{"1.0.0", ">1", "false", ""},
	{"1.0.0", "<1.1", "true", ""},
	{"0.9.0", "<1.1", "true", ""},
	{"1.0.0", ">=1.0.1", "false", ""},
	{"1.0.0", ">1.0.0", "false", ""},
	{"1.0.1", ">1.0.0", "true", ""},
	{"1.0.0", "<1.0.0", "false", ""},
	{"0.1.0", "<0.2.0", "true", ""},
	{"0.2.0", "<0.2.0", "false", ""},
	{"2.0.0", "<2.0.0", "false", ""},
	{"1.2.0", "<1.2.1", "true", ""},
	{"1.2.2", "<1.2.1", "false", ""},
	{"2.0.0", "~1", "false", ""},
	{"1.9.0", "~1", "true", ""},
	{"1.0.0", "~1", "true", ""},
	{"1.2.0", "~1.2", "true", ""},
	{"1.2.0-a", "~1.2", "false", ""},
	{"0.0.0", "^0", "true", ""},
	{"5.0.0", "^0", "false", ""},
	{"0.0.5", "^0.0", "true", ""},
	{"0.1.0", "^0.0", "false", ""},
	{"0.1.9", "^0.1", "true", ""},
	{"0.2.0", "^0.1", "false", ""},
	{"1.5.0", "^1.0", "true", ""},
	{"2.0.0", "^1.0", "false", ""},
	{"0.0.4", "^0.0.4", "true", ""},
	{"0.0.5", "^0.0.4", "false", ""},
	{"0.1.4", "^0.1.3", "true", ""},
	{"0.1.2", "^0.1.3", "false", ""},
	{"0.2.0", "^0.1.3", "false", ""},
	{"1.2.4", "^1.2.3-alpha", "true", ""},
	{"1.2.3-beta", "^1.2.3-alpha", "true", ""},
	{"1.3.0", "^1.2.3", "true", ""},
	{"1.2.2", "^1.2.3", "false", ""},
	{"2.0.0", "^1.2.3", "false", ""},
	{"0.2.3", "^0.2.3-alpha", "true", ""},
	{"1.0.0-a", "^1.0.0", "false", ""},
	{"1.0.0-alpha", ">1.0.0-a", "true", ""},
	{"1.0.0-alpha", "<1.0.0-b", "true", ""},
	{"1.0.0-1", ">1.0.0-a", "false", ""},
	{"1.0.0-a", ">1.0.0-1", "true", ""},
	{"1.0.0-12", ">1.0.0-11", "true", ""},
	{"1.0.0-9", ">1.0.0-10", "false", ""},
	{"1.0.0-10", "<1.0.0-9", "false", ""},
	{"1.0.0-a", ">1.0.0", "false", ""},
	{"1.0.0", ">1.0.0-a", "true", ""},
	{"1.0.0-a", "<1.0.0-a.b", "true", ""},
	{"1.0.0-a.b", "<1.0.0-a", "false", ""},
	{"1.0.0-a.b", ">1.0.0-a.a", "true", ""},
	{"1.0.0-a", "<=1.0.0-a", "true", ""},
	{"1.0.0-1.a", ">1.0.0-1", "true", ""},
	{"1.0.0-a.1", ">1.0.0-a", "true", ""},
	{"1.0.0-alpha.1", "<1.0.0-alpha.2", "true", ""},
	{"1.0.0-ab", "<1.0.0-b", "true", ""},
	{"1.0.0-1", "<1.0.0-1.0", "true", ""},
	{"1.0.0\u0000", ">=1", "", "invalid semver version: unexpected character '\\0' after patch version number"},
	{"1.0.0\n", ">=1", "", "invalid semver version: unexpected character '\\n' after patch version number"},
	{"1.0.0\u0001", ">=1", "", "invalid semver version: unexpected character '\\u{1}' after patch version number"},
	{"1.0.0é", ">=1", "", "invalid semver version: unexpected character 'é' after patch version number"},
	{"1.0.0'", ">=1", "", "invalid semver version: unexpected character '\\'' after patch version number"},
	{"1.0.0", ">=1.0.0\u0000", "", "invalid semver requirement: expected comma after patch version number, found '\\0'"},
	{"1.0.0", ">=1.0.0\\", "", "invalid semver requirement: expected comma after patch version number, found '\\\\'"},
	{"1.0.0", ">=1.0.0'", "", "invalid semver requirement: expected comma after patch version number, found '\\''"},
	{"1.0.0", ">=1.0.0\r", "", "invalid semver requirement: expected comma after patch version number, found '\\r'"},
	{"1.0.0", ">=1.0.0\u0001", "", "invalid semver requirement: expected comma after patch version number, found '\\u{1}'"},
	{"1.0.0", ">=1.0.0é", "", "invalid semver requirement: expected comma after patch version number, found 'é'"},
	{"1.0.0", ">=1.0.0\n", "", "invalid semver requirement: expected comma after patch version number, found '\\n'"},
	{"1.0.0", "1.0.0 \u0001", "", "invalid semver requirement: expected comma after patch version number, found '\\u{1}'"},
	{"1.0.0", ">=1, *", "", "invalid semver requirement: wildcard req (*) must be the only comparator in the version req"},
	{"1.0.0", ">=1, x", "", "invalid semver requirement: wildcard req (x) must be the only comparator in the version req"},
	{"1.0.0", ">=1, *,  >=2", "", "invalid semver requirement: wildcard req (*) must be the only comparator in the version req"},
	{"1.0.0", "*x", "", "invalid semver requirement: unexpected character after wildcard in version req"},
	{"1.0.0", "1.*.*", "true", ""},
	{"1.2.5", ">1.2", "false", ""},
	{"1.2.5", "<1.2", "false", ""},
	{"1.2.5", ">1.2-a", "", "invalid semver requirement: expected comma after minor version number, found '-'"},
	{"1.2.0-a", ">1.2", "false", ""},
}

func TestSemverMatchesAgreesWithUpstream(t *testing.T) {
	for _, tc := range semverVectors {
		got, err := call(t, "semver_matches", []string{tc.version, tc.requirement})
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("semver_matches(%q, %q) error = %v; want %q", tc.version, tc.requirement, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("semver_matches(%q, %q) = %q, %v; want %q", tc.version, tc.requirement, got, err, tc.want)
		}
	}
}

func TestSemverMatchesFailsOnARequirementBeforeAVersion(t *testing.T) {
	// Upstream parses the requirement first, so a bad pair reports the requirement.
	_, err := call(t, "semver_matches", []string{"x", "*.1"})
	if err == nil || !strings.HasPrefix(err.Error(), "invalid semver requirement: ") {
		t.Errorf("semver_matches(x, *.1) error = %v; want the requirement error", err)
	}
}

func TestSemverMatchesAcceptsThirtyTwoComparatorsAndRefusesThirtyThree(t *testing.T) {
	// The oracle takes 32 comparators and refuses the 33rd.
	thirtyTwo := strings.TrimSuffix(strings.Repeat(">=0.0.0, ", 32), ", ")
	if got, err := call(t, "semver_matches", []string{"1.0.0", thirtyTwo}); err != nil || got != "true" {
		t.Errorf("32 comparators: got %q, %v; want true", got, err)
	}
	thirtyThree := thirtyTwo + ", >=0.0.0"
	if _, err := call(t, "semver_matches", []string{"1.0.0", thirtyThree}); err == nil || err.Error() != "invalid semver requirement: excessive number of version comparators" {
		t.Errorf("33 comparators: error = %v", err)
	}
}
