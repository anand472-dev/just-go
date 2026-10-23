// Package ansi holds the terminal styling that --color turns on: the escape
// sequences upstream writes, and the first line of an error in them.
package ansi

import "strings"

// reset ends a styled text.
const reset = "\x1b[0m"

// Paint is text in the SGR code when on is true, and text as it is otherwise.
// Empty text is never styled. It is pure.
func Paint(on bool, code, text string) string {
	if !on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + reset
}

// ErrorLine is an error message as upstream prints it. Without styling it is
// "error: " and the message. With styling, the word error is bold red and the
// message bold; a message with lines after the first, such as a snippet, keeps
// those lines as they are. It is pure.
func ErrorLine(on bool, msg string) string {
	if !on {
		return "error: " + msg
	}
	first, rest, more := strings.Cut(msg, "\n")
	out := Paint(true, "1;31", "error") + ": " + Paint(true, "1", first)
	if more {
		out += "\n" + rest
	}
	return out
}
