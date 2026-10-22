// Package eval turns expressions into strings.
//
// It is pure: every function reads the expression and scope it is given and
// returns a value. None performs I/O, none mutates its input, and there is no
// package-level state. The evaluator knows nothing of recipes, shells, or the
// environment. A backtick is the one expression whose value comes from outside:
// the caller hands over a Commander, which runs the command, and the evaluator
// only words the failure. Strings are Go strings, which are byte sequences,
// and no conversion is applied, so multibyte text passes through unchanged.
package eval

import (
	"errors"
	"fmt"
	"io/fs"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Commander runs the command of a backtick. It is the evaluator's only way
// out of purity, and the caller's to implement.
type Commander interface {
	// Backtick runs command and returns what it wrote to its standard
	// output, without the line break that ends it. A command that fails is a
	// *CommandError; any other error, such as the end of the run, is passed on
	// as it is.
	Backtick(command string) (string, error)
}

// FailureKind says how the command of a backtick failed.
type FailureKind int

// Ways a command fails, in the order upstream checks them.
const (
	// ExitStatus is a non-zero exit status, in CommandError.N.
	ExitStatus FailureKind = iota + 1
	// Signaled is the end of the command by a signal, whose number is in
	// CommandError.N.
	Signaled
	// NoStatus is a command that ended with neither a status nor a signal.
	NoStatus
	// NotUTF8 is output that is not UTF-8; CommandError.Detail says where.
	NotUTF8
	// Interrupted is a command that succeeded while the run caught a fatal
	// signal; CommandError.N is its number and Detail its name.
	Interrupted
	// NotRun is a command that could not be started; CommandError.Cause is
	// the reason.
	NotRun
)

// CommandError is the failure of the command of a backtick.
type CommandError struct {
	Kind FailureKind
	// N is the exit status or the signal number, for the kinds that have one.
	N int
	// Detail is the name of the signal of Interrupted, or the description of
	// the invalid byte sequence of NotUTF8.
	Detail string
	// Cause is why a command of kind NotRun could not be started.
	Cause error
}

// Error words the failure as upstream's Error::Backtick does, without the
// location. It is pure.
func (e *CommandError) Error() string {
	switch e.Kind {
	case ExitStatus:
		return fmt.Sprintf("backtick failed with exit code %d", e.N)
	case Signaled:
		return fmt.Sprintf("backtick was terminated by signal %d", e.N)
	case NotUTF8:
		return fmt.Sprintf("backtick succeeded but stdout was not utf8: %s", e.Detail)
	case Interrupted:
		return fmt.Sprintf("backtick succeeded but `just` was interrupted by signal %s", e.Detail)
	case NotRun:
		return e.notRun()
	}
	return "backtick failed for an unknown reason"
}

// notRun words a failure to start the shell by whether it was missing, not
// runnable, or failed otherwise. It is pure.
func (e *CommandError) notRun() string {
	const what = "backtick could not be run because "
	switch {
	case errors.Is(e.Cause, fs.ErrNotExist):
		return fmt.Sprintf("%sjust could not find the shell:\n%v", what, e.Cause)
	case errors.Is(e.Cause, fs.ErrPermission):
		return fmt.Sprintf("%sjust could not run the shell:\n%v", what, e.Cause)
	}
	return fmt.Sprintf("%sof an IO error while launching the shell:\n%v", what, e.Cause)
}

// Unwrap is the reason a command could not be run. It is pure.
func (e *CommandError) Unwrap() error { return e.Cause }

// ExitCode is the status the run ends with for the failure, as upstream's
// Error::code gives it: the command's own status, or 128 more than the
// number of the signal that ended it or that the run caught. A failure with
// neither ends the run with status 1. It is pure.
func (e *CommandError) ExitCode() int {
	switch e.Kind {
	case ExitStatus:
		return e.N
	case Signaled, Interrupted:
		return 128 + e.N
	}
	return 1
}

// Scope holds evaluated assignment values, by name.
type Scope map[string]string

// Evaluate returns the string value of e, looking variables up in scope. A
// variable with no entry in scope, a list (which belongs to the unstable
// `lists` setting), a backtick (there is no Commander to run it), or an
// expression of a type the evaluator does not know, is a *diag.Error of
// KindRuntime. A conditional evaluates only the branch its comparison picks;
// an assert is the empty string, or an error when its comparison fails. An undefined variable is located at its name, as the syntax
// tree records it, but without a file, which the caller fills in. Evaluate is
// pure.
func Evaluate(e ast.Expr, scope Scope) (string, error) {
	return EvaluateWith(e, scope, nil)
}

// EvaluateWith is Evaluate for an expression that may hold backticks, whose
// commands cmd runs. A backtick whose command fails is a *diag.Error of
// KindRuntime located at the whole backtick, with the status the run ends
// with in ExitCode; an error of cmd that is not a *CommandError is returned
// as it is. With no cmd a backtick is an error. It is pure apart from what
// cmd does.
func EvaluateWith(e ast.Expr, scope Scope, cmd Commander) (string, error) {
	switch x := e.(type) {
	case ast.Text:
		return x.Value, nil
	case ast.StringLit:
		return x.Value, nil
	case ast.Backtick:
		return backtick(x, cmd)
	case ast.Var:
		return lookup(x, scope)
	case ast.Concat:
		return concat(x, scope, cmd)
	case ast.List, ast.Comparison:
		return "", errLists()
	case ast.Conditional:
		return conditional(x, scope, cmd)
	case ast.Assert:
		return assertion(x, scope, cmd)
	default:
		return "", fail("cannot evaluate expression of type %T", e)
	}
}

// EvaluateAll returns the string value of each of exprs, in order, looking
// variables up in scope. It stops at the first error Evaluate reports. With no
// expressions it returns nil. EvaluateAll is pure.
func EvaluateAll(exprs []ast.Expr, scope Scope) ([]string, error) {
	return EvaluateAllWith(exprs, scope, nil)
}

// EvaluateAllWith is EvaluateAll for expressions that may hold backticks,
// which cmd runs, in order. It is pure apart from what cmd does.
func EvaluateAllWith(exprs []ast.Expr, scope Scope, cmd Commander) ([]string, error) {
	if len(exprs) == 0 {
		return nil, nil
	}
	values := make([]string, len(exprs))
	for i, e := range exprs {
		v, err := EvaluateWith(e, scope, cmd)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	return values, nil
}

// lookup returns the value of v in scope, or an error if it has none. It is
// pure.
func lookup(v ast.Var, scope Scope) (string, error) {
	val, ok := scope[v.Name]
	if !ok {
		err := fail("variable `%s` not defined", v.Name)
		err.Line, err.Col, err.Width = v.Line, v.Col, len(v.Name)
		return "", err
	}
	return val, nil
}

// backtick runs the command of b through cmd. It is pure apart from what cmd
// does.
func backtick(b ast.Backtick, cmd Commander) (string, error) {
	if cmd == nil {
		return "", locateBacktick(fail("cannot call backticks in const context"), b)
	}
	out, err := cmd.Backtick(b.Command)
	var failed *CommandError
	if !errors.As(err, &failed) {
		return out, err
	}
	de := locateBacktick(fail("%s", failed.Error()), b)
	de.ExitCode = failed.ExitCode()
	return "", de
}

// locateBacktick puts err at the whole of b, without a file, which the caller
// fills in. It is pure apart from updating err.
func locateBacktick(err *diag.Error, b ast.Backtick) *diag.Error {
	err.Line, err.Col, err.Width = b.Line, b.Col, b.Width
	return err
}

// concat evaluates both sides of c and joins them. It is pure apart from what
// cmd does.
func concat(c ast.Concat, scope Scope, cmd Commander) (string, error) {
	left, err := EvaluateWith(c.Left, scope, cmd)
	if err != nil {
		return "", err
	}
	right, err := EvaluateWith(c.Right, scope, cmd)
	if err != nil {
		return "", err
	}
	return left + right, nil
}

// fail builds a runtime error. It is pure.
func fail(format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf(format, args...)}
}

// CheckUTF8 returns the failure for command output that is not valid UTF-8,
// worded as Rust words its Utf8Error: where the valid text ends and how long
// the invalid sequence is, or that the output ends in the middle of a
// character. It returns nil for valid output. It is pure.
func CheckUTF8(out []byte) *CommandError {
	if utf8.Valid(out) {
		return nil
	}
	at := validPrefix(out)
	bad := out[at:]
	detail := fmt.Sprintf("invalid utf-8 sequence of %d bytes from index %d", invalidLength(bad), at)
	if !utf8.FullRune(bad) {
		detail = fmt.Sprintf("incomplete utf-8 byte sequence from index %d", at)
	}
	return &CommandError{Kind: NotUTF8, Detail: detail}
}

// validPrefix returns the length of the longest valid UTF-8 prefix of b. It is
// pure.
func validPrefix(b []byte) int {
	n := 0
	for n < len(b) {
		r, size := utf8.DecodeRune(b[n:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		n += size
	}
	return n
}

// continuation is the range a byte after the first of a multibyte character
// may take, by that first byte; a byte outside it ends the invalid sequence.
// Pure.
func continuation(first byte) (lo, hi byte) {
	switch first {
	case 0xE0:
		return 0xA0, 0xBF
	case 0xED:
		return 0x80, 0x9F
	case 0xF0:
		return 0x90, 0xBF
	case 0xF4:
		return 0x80, 0x8F
	}
	return 0x80, 0xBF
}

// width is how many bytes a character that begins with first takes, or 1 for
// a byte that cannot begin one. It is pure.
func width(first byte) int {
	switch {
	case first >= 0xC2 && first <= 0xDF:
		return 2
	case first >= 0xE0 && first <= 0xEF:
		return 3
	case first >= 0xF0 && first <= 0xF4:
		return 4
	}
	return 1
}

// invalidLength is how many bytes of b, which starts with an invalid or
// incomplete sequence, Rust counts as the sequence: the bytes that could be
// the start of a character. It is pure.
func invalidLength(b []byte) int {
	lo, hi := continuation(b[0])
	n := 1
	for n < width(b[0]) && n < len(b) && b[n] >= lo && b[n] <= hi {
		n++
		lo, hi = 0x80, 0xBF
	}
	return n
}
