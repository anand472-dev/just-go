package builtins

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// semver_matches as upstream computes it: the requirement and version parsers,
// the matching rules, and the error wording of the semver crate that upstream
// 1.59.0 uses (semver 1.0.28). It is pure. The parser follows the crate's
// parse.rs and the matcher its eval.rs, so each rule is the crate's.

// semverMatches reports, as "true" or "false", whether the version matches the
// requirement. Upstream parses the requirement before the version, and so
// does this.
func semverMatches(args []string) (string, error) {
	req, err := parseSemverReq(args[1])
	if err != nil {
		return "", fmt.Errorf("invalid semver requirement: %w", err)
	}
	v, err := parseSemverVersion(args[0])
	if err != nil {
		return "", fmt.Errorf("invalid semver version: %w", err)
	}
	return strconv.FormatBool(semverMatchesReq(req, v)), nil
}

// semverPos is the part of a version the parser is reading, for its errors.
type semverPos int

const (
	posMajor semverPos = iota
	posMinor
	posPatch
	posPre
	posBuild
)

func (p semverPos) String() string {
	return [...]string{"major version number", "minor version number", "patch version number", "pre-release identifier", "build metadata"}[p]
}

// semverVersion is a parsed version. Build metadata is parsed and dropped, as
// it is in matching.
type semverVersion struct {
	major, minor, patch uint64
	pre                 string
}

// semverOp is the operator of a comparator.
type semverOp int

const (
	opCaret semverOp = iota
	opTilde
	opExact
	opGreater
	opGreaterEq
	opLess
	opLessEq
	opWildcard
)

// semverComparator is one comparator of a requirement. A part it does not name
// is absent: a minor or patch that is a wildcard or missing.
type semverComparator struct {
	op                  semverOp
	major, minor, patch uint64
	hasMinor, hasPatch  bool
	pre                 string
}

var errSemverEmpty = errors.New("empty string, expected a semver version")

// parseSemverVersion parses a version of the form major.minor.patch with an
// optional pre-release and build metadata.
func parseSemverVersion(text string) (semverVersion, error) {
	if text == "" {
		return semverVersion{}, errSemverEmpty
	}
	v, pos, text, err := parseSemverCore(text)
	if err != nil {
		return v, err
	}
	if rest, ok := strings.CutPrefix(text, "-"); ok {
		pos = posPre
		if v.pre, text, err = nonEmptyIdentifier(rest, pos); err != nil {
			return v, err
		}
	}
	if rest, ok := strings.CutPrefix(text, "+"); ok {
		pos = posBuild
		if _, text, err = nonEmptyIdentifier(rest, pos); err != nil {
			return v, err
		}
	}
	if text != "" {
		return v, errCharAfter(pos, text)
	}
	return v, nil
}

// parseSemverCore reads major.minor.patch, returning the part it read last.
func parseSemverCore(text string) (semverVersion, semverPos, string, error) {
	var v semverVersion
	var err error
	pos := posMajor
	if v.major, text, err = numericIdentifier(text, pos); err != nil {
		return v, pos, text, err
	}
	if text, err = dot(text, pos); err != nil {
		return v, pos, text, err
	}
	pos = posMinor
	if v.minor, text, err = numericIdentifier(text, pos); err != nil {
		return v, pos, text, err
	}
	if text, err = dot(text, pos); err != nil {
		return v, pos, text, err
	}
	pos = posPatch
	v.patch, text, err = numericIdentifier(text, pos)
	return v, pos, text, err
}

// nonEmptyIdentifier reads an identifier that must not be empty.
func nonEmptyIdentifier(input string, pos semverPos) (string, string, error) {
	ident, rest, err := identifier(input, pos)
	if err == nil && ident == "" {
		err = errEmptySegment(pos)
	}
	return ident, rest, err
}

// numericIdentifier reads the digits at the start of input as a number, which
// must not have a leading zero and must fit in 64 bits.
func numericIdentifier(input string, pos semverPos) (uint64, string, error) {
	n := 0
	var value uint64
	for n < len(input) && input[n] >= '0' && input[n] <= '9' {
		d := uint64(input[n] - '0')
		if value == 0 && n > 0 {
			return 0, "", fmt.Errorf("invalid leading zero in %v", pos)
		}
		if value > (math.MaxUint64-d)/10 {
			return 0, "", fmt.Errorf("value of %v exceeds u64::MAX", pos)
		}
		value = value*10 + d
		n++
	}
	if n > 0 {
		return value, input[n:], nil
	}
	if input == "" {
		return 0, "", fmt.Errorf("unexpected end of input while parsing %v", pos)
	}
	return 0, "", fmt.Errorf("unexpected character %s while parsing %v", debugChar(firstRune(input)), pos)
}

// dot reads the dot that separates two parts of a version.
func dot(input string, pos semverPos) (string, error) {
	if rest, ok := strings.CutPrefix(input, "."); ok {
		return rest, nil
	}
	if input == "" {
		return "", fmt.Errorf("unexpected end of input while parsing %v", pos)
	}
	return "", errCharAfter(pos, input)
}

// identifier reads a dot-separated identifier made of letters, digits and
// hyphens. A segment of digits must not have a leading zero in a pre-release.
// Its result is the identifier and the rest of the input.
func identifier(input string, pos semverPos) (string, string, error) {
	accumulated, segment := 0, 0
	nondigit := false
	for {
		b := byteAt(input, accumulated+segment)
		switch {
		case isIdentLetter(b):
			segment++
			nondigit = true
		case b >= '0' && b <= '9':
			segment++
		case segment == 0:
			return emptyIdentifier(input, pos, accumulated, b)
		default:
			if err := checkLeadingZero(input[accumulated:], pos, segment, nondigit); err != nil {
				return "", "", err
			}
			accumulated += segment
			if b != '.' {
				return input[:accumulated], input[accumulated:], nil
			}
			accumulated++
			segment, nondigit = 0, false
		}
	}
}

// emptyIdentifier is the result of an identifier that has no segment: none at
// all when nothing precedes the input's end or a non-dot, else an error.
func emptyIdentifier(input string, pos semverPos, accumulated, b int) (string, string, error) {
	if accumulated == 0 && b != '.' {
		return "", input, nil
	}
	return "", "", errEmptySegment(pos)
}

// checkLeadingZero rejects a pre-release segment of digits with a leading zero.
func checkLeadingZero(segmentAndRest string, pos semverPos, segment int, nondigit bool) error {
	if pos == posPre && segment > 1 && !nondigit && strings.HasPrefix(segmentAndRest, "0") {
		return fmt.Errorf("invalid leading zero in %v", pos)
	}
	return nil
}

// byteAt is the byte at i, or -1 past the end of the input.
func byteAt(s string, i int) int {
	if i < len(s) {
		return int(s[i])
	}
	return -1
}

// isIdentLetter reports whether b, from byteAt, is a letter or a hyphen.
func isIdentLetter(b int) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '-'
}

// parseSemverReq parses a requirement: one or more comparators separated by
// commas, or a lone wildcard, which matches every version.
func parseSemverReq(text string) ([]semverComparator, error) {
	text = strings.TrimLeft(text, " ")
	if ch, rest, ok := semverWildcard(text); ok {
		rest = strings.TrimLeft(rest, " ")
		switch {
		case rest == "":
			return nil, nil
		case strings.HasPrefix(rest, ","):
			return nil, fmt.Errorf("wildcard req (%c) must be the only comparator in the version req", ch)
		default:
			return nil, errors.New("unexpected character after wildcard in version req")
		}
	}
	return parseSemverComparators(text, 0)
}

// parseSemverComparators reads the comparators of a requirement from depth, a
// count the crate caps at 32.
func parseSemverComparators(input string, depth int) ([]semverComparator, error) {
	c, pos, text, err := parseSemverComparator(input)
	if err != nil {
		if ch, rest, ok := semverWildcard(input); ok {
			rest = strings.TrimLeft(rest, " ")
			if rest == "" || strings.HasPrefix(rest, ",") {
				err = fmt.Errorf("wildcard req (%c) must be the only comparator in the version req", ch)
			}
		}
		return nil, err
	}
	if text == "" {
		return []semverComparator{c}, nil
	}
	text, ok := strings.CutPrefix(text, ",")
	if !ok {
		return nil, fmt.Errorf("expected comma after %v, found %s", pos, debugChar(firstRune(text)))
	}
	text = strings.TrimLeft(text, " ")
	if depth+1 == 32 {
		return nil, errors.New("excessive number of version comparators")
	}
	rest, err := parseSemverComparators(text, depth+1)
	if err != nil {
		return nil, err
	}
	return append([]semverComparator{c}, rest...), nil
}

// parseSemverComparator reads one comparator. It returns the part the parser
// reached, for the error about a comparator that is not followed by a comma.
func parseSemverComparator(input string) (semverComparator, semverPos, string, error) {
	c, text := parseSemverOp(input)
	defaultOp := len(input) == len(text)
	pos, text, wild, err := parseSemverNumbers(&c, strings.TrimLeft(text, " "))
	if err != nil {
		return c, pos, "", err
	}
	if defaultOp && wild {
		c.op = opWildcard
	}
	if c.hasPatch {
		if pos, text, err = parseSemverPreBuild(&c, text); err != nil {
			return c, pos, "", err
		}
	}
	return c, pos, strings.TrimLeft(text, " "), nil
}

// parseSemverNumbers reads the major, and then the minor and patch after their
// dots, which may be wildcards. It returns the part it read last, the text
// after it, and whether a wildcard was read.
func parseSemverNumbers(c *semverComparator, text string) (semverPos, string, bool, error) {
	pos := posMajor
	var err error
	if c.major, text, err = numericIdentifier(text, pos); err != nil {
		return pos, text, false, err
	}
	minorWild, patchWild := false, false
	if rest, ok := strings.CutPrefix(text, "."); ok {
		pos = posMinor
		if minorWild, text, err = parseSemverMinor(c, rest); err != nil {
			return pos, "", false, err
		}
	}
	if rest, ok := strings.CutPrefix(text, "."); ok {
		pos = posPatch
		if patchWild, text, err = parseSemverPatch(c, rest, minorWild); err != nil {
			return pos, "", false, err
		}
	}
	return pos, text, minorWild || patchWild, nil
}

// parseSemverMinor reads the minor part of a comparator, which is a number or a
// wildcard. It reports whether it was a wildcard.
func parseSemverMinor(c *semverComparator, input string) (bool, string, error) {
	if _, rest, ok := semverWildcard(input); ok {
		return true, rest, nil
	}
	var err error
	c.hasMinor = true
	c.minor, input, err = numericIdentifier(input, posMinor)
	return false, input, err
}

// parseSemverPatch reads the patch part, which is a number or a wildcard, but
// no number may follow a wildcard minor. It reports whether it was a wildcard.
func parseSemverPatch(c *semverComparator, input string, minorWild bool) (bool, string, error) {
	if _, rest, ok := semverWildcard(input); ok {
		return true, rest, nil
	}
	if minorWild {
		return false, "", errors.New("unexpected character after wildcard in version req")
	}
	var err error
	c.hasPatch = true
	c.patch, input, err = numericIdentifier(input, posPatch)
	return false, input, err
}

// parseSemverPreBuild reads the pre-release and build metadata that follow a
// patch. It returns the part it read last.
func parseSemverPreBuild(c *semverComparator, text string) (semverPos, string, error) {
	pos := posPatch
	var err error
	if rest, ok := strings.CutPrefix(text, "-"); ok {
		pos = posPre
		if c.pre, text, err = nonEmptyIdentifier(rest, pos); err != nil {
			return pos, "", err
		}
	}
	if rest, ok := strings.CutPrefix(text, "+"); ok {
		pos = posBuild
		if _, text, err = nonEmptyIdentifier(rest, pos); err != nil {
			return pos, "", err
		}
	}
	return pos, text, nil
}

// parseSemverOp reads the operator at the start of a comparator. A comparator
// with no operator is a caret requirement.
func parseSemverOp(input string) (semverComparator, string) {
	c := semverComparator{op: opCaret}
	ops := []struct {
		prefix string
		op     semverOp
	}{
		{">=", opGreaterEq}, {"<=", opLessEq}, {"=", opExact}, {">", opGreater},
		{"<", opLess}, {"~", opTilde}, {"^", opCaret},
	}
	for _, o := range ops {
		if rest, ok := strings.CutPrefix(input, o.prefix); ok {
			c.op = o.op
			return c, rest
		}
	}
	return c, input
}

// semverWildcard reads a wildcard part: *, x or X.
func semverWildcard(input string) (byte, string, bool) {
	if input != "" && (input[0] == '*' || input[0] == 'x' || input[0] == 'X') {
		return input[0], input[1:], true
	}
	return 0, "", false
}

// errCharAfter is the error for a character that may not follow the part pos.
func errCharAfter(pos semverPos, rest string) error {
	return fmt.Errorf("unexpected character %s after %v", debugChar(firstRune(rest)), pos)
}

// errEmptySegment is the error for an empty identifier.
func errEmptySegment(pos semverPos) error {
	return fmt.Errorf("empty identifier segment in %v", pos)
}

// firstRune is the first character of s, which must not be empty.
func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// debugChar quotes a character as Rust's Debug formatting does, which is how
// the crate's error messages show it.
func debugChar(r rune) string {
	switch r {
	case 0:
		return `'\0'`
	case '\t':
		return `'\t'`
	case '\n':
		return `'\n'`
	case '\r':
		return `'\r'`
	case '\'':
		return `'\''`
	case '\\':
		return `'\\'`
	}
	if unicode.IsPrint(r) {
		return "'" + string(r) + "'"
	}
	return fmt.Sprintf(`'\u{%x}'`, r)
}

// semverMatchesReq reports whether v matches every comparator. A pre-release
// version also matches only when some comparator names the same major, minor
// and patch with a pre-release of its own, as the crate's rule has it.
func semverMatchesReq(req []semverComparator, v semverVersion) bool {
	for _, c := range req {
		if !c.matches(v) {
			return false
		}
	}
	if v.pre == "" {
		return true
	}
	for _, c := range req {
		if c.preCompatible(v) {
			return true
		}
	}
	return false
}

// preCompatible reports whether the comparator names v's major, minor and patch
// with a pre-release, which lets a pre-release version match it.
func (c semverComparator) preCompatible(v semverVersion) bool {
	return c.major == v.major && c.hasMinor && c.minor == v.minor && c.hasPatch && c.patch == v.patch && c.pre != ""
}

// matches applies the comparator's operator to v.
func (c semverComparator) matches(v semverVersion) bool {
	switch c.op {
	case opExact, opWildcard:
		return c.exact(v)
	case opGreater:
		return c.ordered(v, 1)
	case opGreaterEq:
		return c.exact(v) || c.ordered(v, 1)
	case opLess:
		return c.ordered(v, -1)
	case opLessEq:
		return c.exact(v) || c.ordered(v, -1)
	case opTilde:
		return c.tilde(v)
	default:
		return c.caret(v)
	}
}

// exact reports whether v has the comparator's parts, and the same pre-release.
func (c semverComparator) exact(v semverVersion) bool {
	return v.major == c.major && (!c.hasMinor || v.minor == c.minor) &&
		(!c.hasPatch || v.patch == c.patch) && v.pre == c.pre
}

// ordered reports whether v is above (want 1) or below (want -1) the comparator,
// comparing the parts from the major down. A part the comparator does not name
// ends the comparison without a result.
func (c semverComparator) ordered(v semverVersion, want int) bool {
	if v.major != c.major {
		return cmp.Compare(v.major, c.major) == want
	}
	if !c.hasMinor {
		return false
	}
	if v.minor != c.minor {
		return cmp.Compare(v.minor, c.minor) == want
	}
	if !c.hasPatch {
		return false
	}
	if v.patch != c.patch {
		return cmp.Compare(v.patch, c.patch) == want
	}
	return semverComparePre(v.pre, c.pre) == want
}

// tilde reports whether v is at least the comparator and below the next minor,
// or the next major when only the major is named.
func (c semverComparator) tilde(v semverVersion) bool {
	if v.major != c.major {
		return false
	}
	if c.hasMinor && v.minor != c.minor {
		return false
	}
	if c.hasPatch && v.patch != c.patch {
		return v.patch > c.patch
	}
	return semverComparePre(v.pre, c.pre) >= 0
}

// caret reports whether v is compatible with the comparator: the same major
// when the major is above zero, else the same minor, and so on.
func (c semverComparator) caret(v semverVersion) bool {
	if v.major != c.major {
		return false
	}
	if !c.hasMinor {
		return true
	}
	if !c.hasPatch {
		if c.major > 0 {
			return v.minor >= c.minor
		}
		return v.minor == c.minor
	}
	return c.caretPatched(v)
}

// caretPatched is the caret rule for a comparator that names its minor and its
// patch: a difference in the first part that is not zero decides the match on
// its own, as the crate has it, and only a version that agrees on those parts
// is ordered by its pre-release.
func (c semverComparator) caretPatched(v semverVersion) bool {
	switch {
	case c.major > 0:
		if v.minor != c.minor {
			return v.minor > c.minor
		}
		if v.patch != c.patch {
			return v.patch > c.patch
		}
	case c.minor > 0:
		if v.minor != c.minor {
			return false
		}
		if v.patch != c.patch {
			return v.patch > c.patch
		}
	default:
		if v.minor != c.minor || v.patch != c.patch {
			return false
		}
	}
	return semverComparePre(v.pre, c.pre) >= 0
}

// semverComparePre orders two pre-release strings as the crate does: an empty
// pre-release, which is a release, sorts above every pre-release, and the
// identifiers of the two are compared in turn.
func semverComparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i, l := range as {
		if i >= len(bs) {
			return 1
		}
		if c := semverCompareIdent(l, bs[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// semverCompareIdent orders two identifiers: digits numerically (by length,
// then as text), digits below letters, and letters in ASCII order.
func semverCompareIdent(l, r string) int {
	ld, rd := allDigits(l), allDigits(r)
	switch {
	case ld && rd:
		if len(l) != len(r) {
			return cmp.Compare(len(l), len(r))
		}
		return strings.Compare(l, r)
	case ld:
		return -1
	case rd:
		return 1
	}
	return strings.Compare(l, r)
}

// allDigits reports whether every byte of s is an ASCII digit.
func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
