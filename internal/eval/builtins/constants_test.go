package builtins

import "testing"

// constantsVectors are upstream 1.59.0's constants on Unix, read from its
// constants.rs.
var constantsVectors = map[string]string{
	"HEX": "0123456789abcdef", "HEXLOWER": "0123456789abcdef", "HEXUPPER": "0123456789ABCDEF",
	"PATH_SEP": "/", "PATH_VAR_SEP": ":", "CLEAR": "\x1bc",
	"NORMAL": "\x1b[0m", "BOLD": "\x1b[1m", "ITALIC": "\x1b[3m", "UNDERLINE": "\x1b[4m",
	"INVERT": "\x1b[7m", "HIDE": "\x1b[8m", "STRIKETHROUGH": "\x1b[9m",
	"BLACK": "\x1b[30m", "RED": "\x1b[31m", "GREEN": "\x1b[32m", "YELLOW": "\x1b[33m",
	"BLUE": "\x1b[34m", "MAGENTA": "\x1b[35m", "CYAN": "\x1b[36m", "WHITE": "\x1b[37m",
	"BG_BLACK": "\x1b[40m", "BG_RED": "\x1b[41m", "BG_GREEN": "\x1b[42m", "BG_YELLOW": "\x1b[43m",
	"BG_BLUE": "\x1b[44m", "BG_MAGENTA": "\x1b[45m", "BG_CYAN": "\x1b[46m", "BG_WHITE": "\x1b[47m",
}

func TestBuiltinConstantsHaveUpstreamsUnixValues(t *testing.T) {
	for name, want := range constantsVectors {
		got, ok := ConstantValue(name, "linux")
		if !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", name, got, ok, want)
		}
	}
	if len(ConstantsFor("linux")) != len(constantsVectors) {
		t.Errorf("ConstantsFor has %d names; want %d", len(ConstantsFor("linux")), len(constantsVectors))
	}
}

func TestWindowsChangesOnlyThePathSeparators(t *testing.T) {
	for _, name := range []string{"PATH_SEP", "PATH_VAR_SEP"} {
		unix, _ := ConstantValue(name, "linux")
		win, _ := ConstantValue(name, "windows")
		if win == unix {
			t.Errorf("%s is %q on Windows and on Unix; want a difference", name, win)
		}
	}
	if got, _ := ConstantValue("PATH_SEP", "windows"); got != `\` {
		t.Errorf("PATH_SEP on Windows = %q; want a backslash", got)
	}
	if got, _ := ConstantValue("RED", "windows"); got != "\x1b[31m" {
		t.Errorf("RED on Windows = %q; want the same as on Unix", got)
	}
}

func TestConstantsAreKnownOnlyByTheirNames(t *testing.T) {
	if !IsConstant("HEX") || IsConstant("hex") || IsConstant("NOPE") {
		t.Error("IsConstant does not answer for the names of constants alone")
	}
	if _, ok := ConstantValue("NOPE", "linux"); ok {
		t.Error("ConstantValue(NOPE) found a value")
	}
}

func TestConstantsForIsAFreshMap(t *testing.T) {
	m := ConstantsFor("linux")
	m["HEX"] = "changed"
	if got, _ := ConstantValue("HEX", "linux"); got != "0123456789abcdef" {
		t.Errorf("ConstantsFor shares storage with the table: HEX = %q", got)
	}
}
