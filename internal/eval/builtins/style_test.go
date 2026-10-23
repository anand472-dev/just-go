package builtins

import (
	"strings"
	"testing"
)

// styleVectors are the results upstream 1.59.0 gives for style, with one name
// and no text (the prefix) and with the text "foo" (the painted text). They
// were read from the oracle. Each row is the name, the prefix, and the paint.
var styleVectors = []struct {
	name, prefix, paint string
}{
	{"black", "\u001b[30m", "\u001b[30mfoo\u001b[0m"},
	{"blue", "\u001b[34m", "\u001b[34mfoo\u001b[0m"},
	{"cyan", "\u001b[36m", "\u001b[36mfoo\u001b[0m"},
	{"green", "\u001b[32m", "\u001b[32mfoo\u001b[0m"},
	{"magenta", "\u001b[35m", "\u001b[35mfoo\u001b[0m"},
	{"red", "\u001b[31m", "\u001b[31mfoo\u001b[0m"},
	{"white", "\u001b[37m", "\u001b[37mfoo\u001b[0m"},
	{"yellow", "\u001b[33m", "\u001b[33mfoo\u001b[0m"},
	{"fg:black", "\u001b[30m", "\u001b[30mfoo\u001b[0m"},
	{"fg:red", "\u001b[31m", "\u001b[31mfoo\u001b[0m"},
	{"fg:white", "\u001b[37m", "\u001b[37mfoo\u001b[0m"},
	{"bg:black", "\u001b[40m", "\u001b[40mfoo\u001b[0m"},
	{"bg:blue", "\u001b[44m", "\u001b[44mfoo\u001b[0m"},
	{"bg:cyan", "\u001b[46m", "\u001b[46mfoo\u001b[0m"},
	{"bg:green", "\u001b[42m", "\u001b[42mfoo\u001b[0m"},
	{"bg:magenta", "\u001b[45m", "\u001b[45mfoo\u001b[0m"},
	{"bg:red", "\u001b[41m", "\u001b[41mfoo\u001b[0m"},
	{"bg:white", "\u001b[47m", "\u001b[47mfoo\u001b[0m"},
	{"bg:yellow", "\u001b[43m", "\u001b[43mfoo\u001b[0m"},
	{"blink", "\u001b[5m", "\u001b[5mfoo\u001b[0m"},
	{"bold", "\u001b[1m", "\u001b[1mfoo\u001b[0m"},
	{"dim", "\u001b[2m", "\u001b[2mfoo\u001b[0m"},
	{"hidden", "\u001b[8m", "\u001b[8mfoo\u001b[0m"},
	{"italic", "\u001b[3m", "\u001b[3mfoo\u001b[0m"},
	{"reverse", "\u001b[7m", "\u001b[7mfoo\u001b[0m"},
	{"strikethrough", "\u001b[9m", "\u001b[9mfoo\u001b[0m"},
	{"underline", "\u001b[4m", "\u001b[4mfoo\u001b[0m"},
	{"error", "\u001b[1;31m", "\u001b[1;31mfoo\u001b[0m"},
	{"warning", "\u001b[1;33m", "\u001b[1;33mfoo\u001b[0m"},
	{"command", "\u001b[1m", "\u001b[1mfoo\u001b[0m"},
	{"0", "\u001b[38;5;0m", "\u001b[38;5;0mfoo\u001b[0m"},
	{"1", "\u001b[38;5;1m", "\u001b[38;5;1mfoo\u001b[0m"},
	{"15", "\u001b[38;5;15m", "\u001b[38;5;15mfoo\u001b[0m"},
	{"16", "\u001b[38;5;16m", "\u001b[38;5;16mfoo\u001b[0m"},
	{"255", "\u001b[38;5;255m", "\u001b[38;5;255mfoo\u001b[0m"},
	{"fg:0", "\u001b[38;5;0m", "\u001b[38;5;0mfoo\u001b[0m"},
	{"bg:0", "\u001b[48;5;0m", "\u001b[48;5;0mfoo\u001b[0m"},
	{"bg:255", "\u001b[48;5;255m", "\u001b[48;5;255mfoo\u001b[0m"},
	{"fg:17", "\u001b[38;5;17m", "\u001b[38;5;17mfoo\u001b[0m"},
	{"bg:200", "\u001b[48;5;200m", "\u001b[48;5;200mfoo\u001b[0m"},
	{"#ff8800", "\u001b[38;2;255;136;0m", "\u001b[38;2;255;136;0mfoo\u001b[0m"},
	{"bg:#ff8800", "\u001b[48;2;255;136;0m", "\u001b[48;2;255;136;0mfoo\u001b[0m"},
	{"#f80", "\u001b[38;2;255;136;0m", "\u001b[38;2;255;136;0mfoo\u001b[0m"},
	{"bg:#f80", "\u001b[48;2;255;136;0m", "\u001b[48;2;255;136;0mfoo\u001b[0m"},
	{"fg:#abcdef", "\u001b[38;2;171;205;239m", "\u001b[38;2;171;205;239mfoo\u001b[0m"},
	{"#ABCDEF", "\u001b[38;2;171;205;239m", "\u001b[38;2;171;205;239mfoo\u001b[0m"},
	{"#FFF", "\u001b[38;2;255;255;255m", "\u001b[38;2;255;255;255mfoo\u001b[0m"},
	{"#000", "\u001b[38;2;0;0;0m", "\u001b[38;2;0;0;0mfoo\u001b[0m"},
	{"bg:#123", "\u001b[48;2;17;34;51m", "\u001b[48;2;17;34;51mfoo\u001b[0m"},
	{"fg:#fff", "\u001b[38;2;255;255;255m", "\u001b[38;2;255;255;255mfoo\u001b[0m"},
}

// styleErrors are the errors upstream gives for names it does not know.
var styleErrors = []struct {
	name, err string
}{
	{"256", "invalid style: `256`"},
	{"1000", "invalid style: `1000`"},
	{"0255", "invalid style: `0255`"},
	{"007", "invalid style: `007`"},
	{"#12g", "invalid style: `#12g`"},
	{"#1234", "invalid style: `#1234`"},
	{"#12", "invalid style: `#12`"},
	{"", "invalid style: ``"},
	{"Red", "invalid style: `Red`"},
	{"bold red", "invalid style: `bold red`"},
	{"fg:", "invalid style: `fg:`"},
	{"bg:", "invalid style: `bg:`"},
	{"#", "invalid style: `#`"},
}

func TestStyleAgreesWithUpstream(t *testing.T) {
	for _, tc := range styleVectors {
		if got, err := call(t, "style", []string{tc.name}); err != nil || got != tc.prefix {
			t.Errorf("style(%q) = %q, %v; want %q", tc.name, got, err, tc.prefix)
		}
		if got, err := call(t, "style", []string{tc.name, "foo"}); err != nil || got != tc.paint {
			t.Errorf("style(%q, foo) = %q, %v; want %q", tc.name, got, err, tc.paint)
		}
	}
}

func TestStyleRefusesWhatUpstreamRefuses(t *testing.T) {
	for _, tc := range styleErrors {
		got, err := call(t, "style", []string{tc.name})
		if got != "" || err == nil || err.Error() != tc.err {
			t.Errorf("style(%q) = %q, %v; want the error %q", tc.name, got, err, tc.err)
		}
	}
}

func TestStyleOfAStreamNeedsColourSettingsNotYetSupported(t *testing.T) {
	// stdout and stderr depend on --color, which the command line owns, so
	// they are refused rather than guessed until that phase lands.
	for _, name := range []string{"stdout", "stderr"} {
		_, err := call(t, "style", []string{name})
		if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("style(%q) error = %v; want a not-implemented error", name, err)
		}
	}
}

func BenchmarkStyle(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := style([]string{"#ff8800", "foo"}); err != nil {
			b.Fatal(err)
		}
	}
}
