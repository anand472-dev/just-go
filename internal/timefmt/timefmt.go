// Package timefmt formats a time as upstream does: with the strftime syntax of
// the chrono library, in English, and with the same errors for a format it
// does not know.
//
// The package is pure. It reads a sys.Time and a format and returns a string,
// and it imports neither os nor time: the civil date comes from the seconds
// since the epoch by arithmetic.
package timefmt

import (
	"fmt"
	"strings"

	"gitea.com/wimwian/just-go/internal/sys"
)

// Error is a failure to use a format string. Parse is true when the format
// is not valid, and false when it is valid but cannot be applied.
type Error struct {
	Format string
	Parse  bool
}

// Error words the failure as upstream does. Pure.
func (e *Error) Error() string {
	if e.Parse {
		return fmt.Sprintf("failed to parse time format string `%s`: bad or unsupported format string", e.Format)
	}
	return fmt.Sprintf("failed to format time with format string `%s`", e.Format)
}

// Format formats t as the format string layout says. The whole layout is
// checked before anything is formatted, so a bad specifier late in it is an
// error even when the ones before it are fine. Pure.
func Format(t sys.Time, layout string) (string, error) {
	items, ok := parse(layout)
	if !ok {
		return "", &Error{Format: layout, Parse: true}
	}
	c := newCivil(t)
	var b strings.Builder
	for _, it := range items {
		if !it.write(&b, c) {
			return "", &Error{Format: layout}
		}
	}
	return b.String(), nil
}

// pad is how a number is padded to its width.
type pad int

const (
	padNone pad = iota
	padZero
	padSpace
)

// padOverrides are the characters after a percent sign that choose a padding
// other than a specifier's own.
var padOverrides = map[byte]pad{'-': padNone, '0': padZero, '_': padSpace}

// item is one piece of a parsed format: text to copy, or a specifier to
// format.
type item struct {
	text    string
	numeric *numeric
	pad     pad
	fixed   func(c civil) (string, bool)
}

// write appends the item for c to b. It reports false when the item cannot be
// formatted. Pure apart from writing to b.
func (it item) write(b *strings.Builder, c civil) bool {
	switch {
	case it.numeric != nil:
		b.WriteString(it.numeric.format(c, it.pad))
		return true
	case it.fixed != nil:
		s, ok := it.fixed(c)
		b.WriteString(s)
		return ok
	}
	b.WriteString(it.text)
	return true
}

// parse splits layout into items. It reports false for a format chrono
// refuses. Pure.
func parse(layout string) ([]item, bool) {
	var items []item
	for i := 0; i < len(layout); {
		if layout[i] != '%' {
			j := strings.IndexByte(layout[i:], '%')
			if j < 0 {
				j = len(layout) - i
			}
			items = append(items, item{text: layout[i : i+j]})
			i += j
			continue
		}
		it, n, ok := specifier(layout[i+1:])
		if !ok {
			return nil, false
		}
		items = append(items, it...)
		i += 1 + n
	}
	return items, true
}

// specifier parses the specifier at the start of rest, the text after a
// percent sign. It returns the items it stands for, how many bytes of rest it
// used, and whether it is valid. Pure.
func specifier(rest string) ([]item, int, bool) {
	override, alternate, used := modifiers(rest)
	if used >= len(rest) {
		return nil, 0, false
	}
	spec := rest[used]
	used++
	items, n, ok := decode(spec, alternate, rest[used:])
	if !ok || (override != nil && !overridePad(items, *override)) {
		return nil, 0, false
	}
	return items, used + n, true
}

// decode parses the specifier character spec, after the `#` marker if it had
// one, whose following text is after. The marker is valid only on `z`, where
// it asks for an offset that can be parsed but not formatted. Pure.
func decode(spec byte, alternate bool, after string) ([]item, int, bool) {
	switch {
	case alternate && spec == 'z':
		return []item{{fixed: func(civil) (string, bool) { return "", false }}}, 0, true
	case alternate:
		return nil, 0, false
	}
	return body(spec, after)
}

// overridePad gives the only item of items the padding p, and reports false
// when a padding cannot be changed: for anything but a single number. Pure
// apart from updating items.
func overridePad(items []item, p pad) bool {
	if len(items) != 1 || items[0].numeric == nil {
		return false
	}
	items[0].pad = p
	return true
}

// modifiers reads the padding override and the alternate marker that may
// follow a percent sign, and returns how many bytes they take. Pure.
func modifiers(rest string) (override *pad, alternate bool, used int) {
	if rest == "" {
		return nil, false, 0
	}
	if p, ok := padOverrides[rest[0]]; ok {
		return &p, false, 1
	}
	return nil, rest[0] == '#', btoi(rest[0] == '#')
}

// btoi is 1 for true and 0 for false. Pure.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// body parses the specifier character spec, whose following text is after.
// It returns the items, how many bytes of after it used, and whether it is
// valid. Pure.
func body(spec byte, after string) ([]item, int, bool) {
	if n, ok := numerics[spec]; ok {
		return []item{{numeric: n, pad: n.pad}}, 0, true
	}
	if f, ok := fixeds[spec]; ok {
		return []item{{fixed: f}}, 0, true
	}
	if macro, ok := macros[spec]; ok {
		items, ok := parse(macro)
		return items, 0, ok
	}
	return special(spec, after)
}

// special parses the specifiers that are neither plain numbers, names nor
// macros: the literals, the fractions of a second and the offsets with
// colons. Pure.
func special(spec byte, after string) ([]item, int, bool) {
	switch spec {
	case '%', 'n', 't':
		return []item{{text: literal(spec)}}, 0, true
	case ':':
		return colonOffset(after)
	case '.':
		return fraction(after, true)
	case '3', '6', '9':
		items, n, ok := fraction(string(spec)+after, false)
		return items, n - 1, ok
	}
	return nil, 0, false
}

// literal is the text of the specifiers %%, %n and %t. Pure.
func literal(spec byte) string {
	switch spec {
	case 'n':
		return "\n"
	case 't':
		return "\t"
	}
	return "%"
}

// digitCount is the number of digits that the specifier digit 3, 6 or 9 asks
// for, and 0 for any other byte. Pure.
func digitCount(b byte) int {
	switch b {
	case '3':
		return 3
	case '6':
		return 6
	case '9':
		return 9
	}
	return 0
}

// fraction parses what follows `%.` (dotted) or `%` (not): `f`, or a digit 3,
// 6 or 9 and `f`. The bare `f` without a dot is the numeric `%f`, so only the
// dotted form takes it. Pure.
func fraction(after string, dotted bool) ([]item, int, bool) {
	if after == "" {
		return nil, 0, false
	}
	if after[0] == 'f' && dotted {
		return []item{{fixed: fractionOf(0, true)}}, 1, true
	}
	digits := digitCount(after[0])
	if digits == 0 || len(after) < 2 || after[1] != 'f' {
		return nil, 0, false
	}
	return []item{{fixed: fractionOf(digits, dotted)}}, 2, true
}

// fractionOf is the fixed formatter of the fraction of a second: with
// digits 0 as many digits as the value needs, in groups of three, and
// nothing for a whole second; else exactly digits digits. dotted puts a
// point first. Pure.
func fractionOf(digits int, dotted bool) func(civil) (string, bool) {
	return func(c civil) (string, bool) {
		nano := fmt.Sprintf("%09d", c.nano)
		if digits == 0 {
			digits := autoDigits(c.nano)
			if digits == 0 {
				return "", true
			}
			return "." + nano[:digits], true
		}
		if dotted {
			return "." + nano[:digits], true
		}
		return nano[:digits], true
	}
}

// autoDigits is how many digits of the fraction of a second show the
// nanoseconds n: none for 0, else 3, 6 or 9. Pure.
func autoDigits(n int) int {
	switch {
	case n == 0:
		return 0
	case n%1_000_000 == 0:
		return 3
	case n%1_000 == 0:
		return 6
	}
	return 9
}

// colonOffset parses what follows `%:`: `z`, `:z` or `::z`. Pure.
func colonOffset(after string) ([]item, int, bool) {
	switch {
	case strings.HasPrefix(after, "::z"):
		return []item{{fixed: offsetOf(offsetHours)}}, 3, true
	case strings.HasPrefix(after, ":z"):
		return []item{{fixed: offsetOf(offsetSeconds)}}, 2, true
	case strings.HasPrefix(after, "z"):
		return []item{{fixed: offsetOf(offsetColon)}}, 1, true
	}
	return nil, 0, false
}

// macros are the specifiers that stand for a longer format.
var macros = map[byte]string{
	'D': "%m/%d/%y",
	'F': "%Y-%m-%d",
	'R': "%H:%M",
	'T': "%H:%M:%S",
	'X': "%H:%M:%S",
	'c': "%a %b %e %H:%M:%S %Y",
	'r': "%I:%M:%S %p",
	'v': "%e-%b-%Y",
	'x': "%m/%d/%y",
	'+': "%Y-%m-%dT%H:%M:%S%.f%:z",
}

// fixeds are the specifiers that print a name, a zone or the part of a
// second, which take no padding.
var fixeds = map[byte]func(civil) (string, bool){
	'A': func(c civil) (string, bool) { return weekdays[c.weekday], true },
	'B': func(c civil) (string, bool) { return months[c.month-1], true },
	'P': func(c civil) (string, bool) { return strings.ToLower(c.meridiem()), true },
	'Z': func(c civil) (string, bool) { return c.zone(), true },
	'a': func(c civil) (string, bool) { return weekdays[c.weekday][:3], true },
	'b': func(c civil) (string, bool) { return months[c.month-1][:3], true },
	'h': func(c civil) (string, bool) { return months[c.month-1][:3], true },
	'p': func(c civil) (string, bool) { return c.meridiem(), true },
	'z': offsetOf(offsetPlain),
}

// weekdays and months are the English names, from Sunday and from January.
var (
	weekdays = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	months   = [...]string{"January", "February", "March", "April", "May", "June", "July", "August",
		"September", "October", "November", "December"}
)

// offsetStyle is how an offset from UTC is written.
type offsetStyle int

const (
	offsetPlain   offsetStyle = iota // +0930
	offsetColon                      // +09:30
	offsetSeconds                    // +09:30:00
	offsetHours                      // +09
)

// offsetOf is the fixed formatter of the offset from UTC in the given style.
// Pure.
func offsetOf(style offsetStyle) func(civil) (string, bool) {
	return func(c civil) (string, bool) { return formatOffset(c.offset, style), true }
}

// formatOffset writes the offset of seconds east of UTC as chrono does: to
// the nearest minute, except for the style with seconds and the one with
// hours only, which truncates. Pure.
func formatOffset(seconds int, style offsetStyle) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	switch style {
	case offsetHours:
		return fmt.Sprintf("%s%02d", sign, seconds/3600)
	case offsetSeconds:
		return fmt.Sprintf("%s%02d:%02d:%02d", sign, seconds/3600, seconds/60%60, seconds%60)
	}
	minutes := (seconds + 30) / 60
	colon := ""
	if style == offsetColon {
		colon = ":"
	}
	return fmt.Sprintf("%s%02d%s%02d", sign, minutes/60, colon, minutes%60)
}
