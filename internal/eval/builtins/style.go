package builtins

import (
	"fmt"
	"strings"
)

// style is upstream's style function for one style name, which is the only
// form a value takes without `set lists`. A name is a colour, a property, a
// role, or a fixed or RGB colour, and the function gives the escape sequence
// that starts the style, or that style painted over the text. It is pure.
//
// The stdout and stderr names depend on --color, which the command line owns,
// so they are refused until that phase lands rather than guessed. The command
// role's colour is --command-color, also the command line's; without it the
// role is bold, which is what upstream prints.
func style(args []string) (string, error) {
	var s ansiStyle
	if err := s.apply(args[0]); err != nil {
		return "", err
	}
	if len(args) == 1 {
		return s.prefix(), nil
	}
	return s.prefix() + args[1] + ansiReset, nil
}

// ansiReset ends a painted text, which is how nu_ansi_term ends one.
const ansiReset = "\x1b[0m"

// ansiColors are the eight colour names, in the order of their SGR offsets:
// the foreground code is 30 plus the index, the background 40 plus it.
var ansiColors = [8]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// ansiAttrs are the properties, by name, with their SGR codes, in the order
// the escape sequence lists them.
var ansiAttrs = [8]struct {
	name, code string
}{
	{"bold", "1"}, {"dim", "2"}, {"italic", "3"}, {"underline", "4"},
	{"blink", "5"}, {"reverse", "7"}, {"hidden", "8"}, {"strikethrough", "9"},
}

// ansiStyle is a style being built: the properties that are on, and the colour
// parameters of its foreground and background, empty when unset.
type ansiStyle struct {
	attrs  [8]bool
	fg, bg string
}

// apply sets the properties and colour that name means, or fails on a name it
// does not know.
func (s *ansiStyle) apply(name string) error {
	if s.applyColour(name) || s.applyAttr(name) || s.applyRole(name) {
		return nil
	}
	if s.applyNumbered(name) {
		return nil
	}
	if name == "stdout" || name == "stderr" {
		return fmt.Errorf("style name `%s` is not implemented yet", name)
	}
	return fmt.Errorf("invalid style: `%s`", name)
}

// applyColour sets a named colour, in the foreground with or without the fg:
// prefix, and in the background only with the bg: prefix.
func (s *ansiStyle) applyColour(name string) bool {
	if rest, ok := strings.CutPrefix(name, "bg:"); ok {
		i := indexOf(ansiColors[:], rest)
		if i >= 0 {
			s.bg = fmt.Sprint(40 + i)
		}
		return i >= 0
	}
	rest, _ := strings.CutPrefix(name, "fg:")
	i := indexOf(ansiColors[:], rest)
	if i >= 0 {
		s.fg = fmt.Sprint(30 + i)
	}
	return i >= 0
}

// applyAttr turns on a property, which takes no prefix.
func (s *ansiStyle) applyAttr(name string) bool {
	for i, a := range ansiAttrs {
		if a.name == name {
			s.attrs[i] = true
			return true
		}
	}
	return false
}

// applyRole sets a role: error and warning are bold in red and yellow, and
// command is bold, its colour being the command line's.
func (s *ansiStyle) applyRole(name string) bool {
	switch name {
	case "error":
		s.fg = "31"
		s.attrs[0] = true
	case "warning":
		s.fg = "33"
		s.attrs[0] = true
	case "command":
		s.attrs[0] = true
	default:
		return false
	}
	return true
}

// applyNumbered sets a fixed colour of 0 to 255, or an RGB colour written as
// three or six hex digits after a #, in the layer its prefix names. A number
// out of range, or any other shape, is not a colour this function knows.
func (s *ansiStyle) applyNumbered(name string) bool {
	layer, rest := "fg", name
	if after, ok := strings.CutPrefix(name, "bg:"); ok {
		layer, rest = "bg", after
	} else if after, ok := strings.CutPrefix(name, "fg:"); ok {
		rest = after
	}
	code, ok := numberedCode(rest, layer)
	if !ok {
		return false
	}
	if layer == "bg" {
		s.bg = code
	} else {
		s.fg = code
	}
	return true
}

// numberedCode is the SGR parameters of a fixed colour, a 0 or a number from 1
// to 255 with no leading zero, or of an RGB colour, #rgb or #rrggbb, in layer.
func numberedCode(rest, layer string) (string, bool) {
	base := "38"
	if layer == "bg" {
		base = "48"
	}
	if hex, ok := strings.CutPrefix(rest, "#"); ok {
		if len(hex) != 3 && len(hex) != 6 || !allHex(hex) {
			return "", false
		}
		r, g, b := rgbOf(hex)
		return fmt.Sprintf("%s;2;%d;%d;%d", base, r, g, b), true
	}
	n, ok := fixedNumber(rest)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s;5;%d", base, n), true
}

// fixedNumber reads a fixed colour number: "0", or one to three digits with no
// leading zero, that is at most 255.
func fixedNumber(s string) (int, bool) {
	if s == "" || len(s) > 3 || s[0] == '0' && len(s) > 1 || !allDigits(s) {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		n = n*10 + int(s[i]-'0')
	}
	return n, n <= 255
}

// rgbOf is the colour of a hex string of three or six digits. A short digit
// stands for itself repeated, so #f80 is #ff8800.
func rgbOf(hex string) (int, int, int) {
	if len(hex) == 3 {
		return hexValue(hex[0]) * 17, hexValue(hex[1]) * 17, hexValue(hex[2]) * 17
	}
	return hexValue(hex[0])<<4 | hexValue(hex[1]), hexValue(hex[2])<<4 | hexValue(hex[3]), hexValue(hex[4])<<4 | hexValue(hex[5])
}

// hexValue is the value of one hex digit; the callers have checked the digit.
func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// allHex reports whether every byte of s is a hex digit.
func allHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// prefix is the escape sequence that starts the style: the properties that are
// on, then the background, then the foreground, in one SGR sequence.
func (s ansiStyle) prefix() string {
	var codes []string
	for i, on := range s.attrs {
		if on {
			codes = append(codes, ansiAttrs[i].code)
		}
	}
	if s.bg != "" {
		codes = append(codes, s.bg)
	}
	if s.fg != "" {
		codes = append(codes, s.fg)
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// indexOf is the index of s in list, or -1.
func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
