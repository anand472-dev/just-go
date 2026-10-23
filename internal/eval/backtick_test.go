package eval

import (
	"errors"
	"io/fs"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// scripted is a Commander that answers each command with the next scripted
// result and records the commands it was asked to run.
type scripted struct {
	out []string
	err error
	ran []string
}

func (s *scripted) Backtick(command string) (string, error) {
	s.ran = append(s.ran, command)
	if s.err != nil {
		return "", s.err
	}
	out := s.out[0]
	s.out = s.out[1:]
	return out, nil
}

var tick = ast.Backtick{Command: "echo hi", Line: 3, Col: 7, Width: 9}

func TestEvaluateWithRunsTheCommandOfABacktick(t *testing.T) {
	cmd := &scripted{out: []string{"hi", "!"}}
	e := ast.Concat{Left: ast.Concat{Left: tick, Right: ast.StringLit{Value: " "}}, Right: ast.Backtick{Command: "echo !"}}
	got, err := EvaluateWith(e, nil, cmd)
	if err != nil || got != "hi !" {
		t.Fatalf("got %q, %v; want %q", got, err, "hi !")
	}
	if want := []string{"echo hi", "echo !"}; !reflect.DeepEqual(cmd.ran, want) {
		t.Errorf("ran %q, want %q", cmd.ran, want)
	}
}

// The commands run left to right, and a failure ends the evaluation before
// the commands after it run.
func TestEvaluateWithStopsAtTheFirstFailingBacktick(t *testing.T) {
	cmd := &scripted{err: &CommandError{Kind: ExitStatus, N: 3}}
	_, err := EvaluateWith(ast.Concat{Left: tick, Right: tick}, nil, cmd)
	if err == nil || len(cmd.ran) != 1 {
		t.Errorf("error %v after %d commands; want an error after one", err, len(cmd.ran))
	}
}

func TestEvaluateAllWithRunsEachCommandInOrder(t *testing.T) {
	cmd := &scripted{out: []string{"a", "b"}}
	got, err := EvaluateAllWith([]ast.Expr{tick, ast.StringLit{Value: "-"}, tick}, nil, cmd)
	if err != nil || !reflect.DeepEqual(got, []string{"a", "-", "b"}) {
		t.Errorf("got %q, %v", got, err)
	}
}

// A backtick has no command to run it without a Commander, and says so where
// upstream does for a const context.
func TestEvaluateRefusesABacktickWithoutACommander(t *testing.T) {
	_, err := Evaluate(tick, nil)
	requireRuntime(t, err, "cannot call backticks in const context")
	var de *diag.Error
	if errors.As(err, &de); de.Line != 3 || de.Col != 7 || de.Width != 9 || de.ExitCode != 0 {
		t.Errorf("error = %+v, want it at 3:7 covering 9 runes with no exit code", de)
	}
}

func TestCommandFailuresAreWordedAsUpstreamWordsThem(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *CommandError
		msg  string
		code int
	}{
		{"status", &CommandError{Kind: ExitStatus, N: 200}, "backtick failed with exit code 200", 200},
		{"signal", &CommandError{Kind: Signaled, N: 9}, "backtick was terminated by signal 9", 137},
		{"no status", &CommandError{Kind: NoStatus}, "backtick failed for an unknown reason", 1},
		{"not utf8", &CommandError{Kind: NotUTF8, Detail: "invalid utf-8 sequence of 1 bytes from index 0"},
			"backtick succeeded but stdout was not utf8: invalid utf-8 sequence of 1 bytes from index 0", 1},
		{"interrupted", &CommandError{Kind: Interrupted, N: 2, Detail: "SIGINT"},
			"backtick succeeded but `just` was interrupted by signal SIGINT", 130},
		{"no shell", &CommandError{Kind: NotRun, Cause: fs.ErrNotExist},
			"backtick could not be run because just could not find the shell:\nfile does not exist", 1},
		{"shell not runnable", &CommandError{Kind: NotRun, Cause: fs.ErrPermission},
			"backtick could not be run because just could not run the shell:\npermission denied", 1},
		{"shell failed", &CommandError{Kind: NotRun, Cause: errors.New("boom")},
			"backtick could not be run because of an IO error while launching the shell:\nboom", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EvaluateWith(tick, nil, &scripted{err: tc.err})
			requireRuntime(t, err, tc.msg)
			var de *diag.Error
			errors.As(err, &de)
			if de.ExitCode != tc.code || de.Line != 3 || de.Col != 7 || de.Width != 9 || de.File != "" {
				t.Errorf("error = %+v, want exit code %d at 3:7 covering 9 runes, no file", de, tc.code)
			}
		})
	}
}

func TestCommandErrorUnwrapsToItsCause(t *testing.T) {
	err := error(&CommandError{Kind: NotRun, Cause: fs.ErrNotExist})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("a command that could not be run does not unwrap to its cause")
	}
}

// What is not a command failure, such as the end of the run, is not reworded.
func TestEvaluateWithPassesOtherErrorsOn(t *testing.T) {
	boom := errors.New("cancelled")
	_, err := EvaluateWith(tick, nil, &scripted{err: boom})
	if err != boom {
		t.Errorf("error = %v, want it unchanged", err)
	}
}

func BenchmarkEvaluateBacktick(b *testing.B) {
	cmd := fixed("value")
	e := ast.Concat{Left: tick, Right: ast.Concat{Left: ast.Var{Name: "v"}, Right: tick}}
	scope := Scope{"v": "x"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := EvaluateWith(e, scope, cmd); err != nil {
			b.Fatal(err)
		}
	}
}

// fixed is a Commander whose commands all print the same thing.
type fixed string

func (f fixed) Backtick(string) (string, error) { return string(f), nil }

// The wording is Rust's Utf8Error, which the oracle prints.
func TestCheckUTF8WordsInvalidOutputAsRustDoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  []byte
		want string // "" means valid
	}{
		{"empty", nil, ""},
		{"ascii", []byte("hello"), ""},
		{"multibyte", []byte("héllo ✓ 😬"), ""},
		{"stray continuation", []byte("ab\x80"), "invalid utf-8 sequence of 1 bytes from index 2"},
		{"not a lead byte", []byte("\xff"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"overlong lead", []byte("a\xc0\x80"), "invalid utf-8 sequence of 1 bytes from index 1"},
		{"bad second byte", []byte("é\xe2\x28"), "invalid utf-8 sequence of 1 bytes from index 2"},
		{"bad third byte", []byte("\xe2\x82\x28"), "invalid utf-8 sequence of 2 bytes from index 0"},
		{"bad fourth byte", []byte("\xf0\x9f\x98\x28"), "invalid utf-8 sequence of 3 bytes from index 0"},
		{"surrogate", []byte("\xed\xa0\x80"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"too big", []byte("\xf4\x90\x80\x80"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"overlong three bytes", []byte("\xe0\x80\x80"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"overlong four bytes", []byte("\xf0\x80\x80\x80"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"bad third byte of the lowest four-byte range", []byte("\xf0\x90\x28"), "invalid utf-8 sequence of 2 bytes from index 0"},
		{"bad third byte of the highest four-byte range", []byte("\xf4\x8f\x28"), "invalid utf-8 sequence of 2 bytes from index 0"},
		{"bad second byte of a two-byte character", []byte("\xc3\x28"), "invalid utf-8 sequence of 1 bytes from index 0"},
		{"ends in a character", []byte("ab\xe2\x82"), "incomplete utf-8 byte sequence from index 2"},
		{"ends after a lead", []byte("\xf0"), "incomplete utf-8 byte sequence from index 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckUTF8(tc.out)
			if tc.want == "" {
				if got != nil {
					t.Errorf("CheckUTF8 = %v, want valid", got)
				}
				return
			}
			if got == nil || got.Kind != NotUTF8 || got.Detail != tc.want {
				t.Errorf("CheckUTF8 = %+v, want %q", got, tc.want)
			}
		})
	}
}

func BenchmarkCheckUTF8(b *testing.B) {
	out := []byte("some output that is valid, with ✓ and 日本\n")
	b.ReportAllocs()
	for b.Loop() {
		if CheckUTF8(out) != nil {
			b.Fatal("valid output refused")
		}
	}
}
