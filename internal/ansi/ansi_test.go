package ansi

import "testing"

// Paint wraps text in the code and a reset when color is on, and returns the
// text as it is when it is off or empty.
func TestPaintWrapsOnlyWhenOn(t *testing.T) {
	if got := Paint(true, "1;36", "x"); got != "\x1b[1;36mx\x1b[0m" {
		t.Errorf("Paint on = %q", got)
	}
	if got := Paint(false, "1;36", "x"); got != "x" {
		t.Errorf("Paint off = %q", got)
	}
	if got := Paint(true, "1;36", ""); got != "" {
		t.Errorf("Paint of empty text = %q", got)
	}
}

// ErrorLine styles the word error in bold red and the message in bold; the
// lines after the first, such as a snippet, are kept as they are.
func TestErrorLineStylesTheFirstLine(t *testing.T) {
	if got := ErrorLine(true, "boom"); got != "\x1b[1;31merror\x1b[0m: \x1b[1mboom\x1b[0m" {
		t.Errorf("ErrorLine = %q", got)
	}
	if got := ErrorLine(true, "boom\n snippet"); got != "\x1b[1;31merror\x1b[0m: \x1b[1mboom\x1b[0m\n snippet" {
		t.Errorf("ErrorLine with a snippet = %q", got)
	}
	if got := ErrorLine(false, "boom"); got != "error: boom" {
		t.Errorf("ErrorLine off = %q", got)
	}
}
