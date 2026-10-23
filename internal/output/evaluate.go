// Package output renders what the command line prints for each mode. Its
// functions are pure: they take the values and return the text to write.
package output

import (
	"fmt"
	"strings"

	just "gitea.com/wimwian/just-go"
)

// escaper escapes a value as upstream's --evaluate does inside its quotes: a
// backslash, a double quote, and the control characters \n, \r and \t. Other
// characters, including a single quote, are written as they are.
var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

// Evaluate is what --evaluate prints for bindings, given the name it was asked
// for, or "" for none. A name that is one assignment is printed as that value,
// raw, with no newline, as upstream does. Otherwise each binding is one line,
// `name := "value"`, with the names padded to the longest so that the values
// line up; a module's name lists its assignments this way. Pure.
func Evaluate(bindings []just.Binding, name string) string {
	if name != "" && len(bindings) == 1 && bindings[0].Name == name {
		return bindings[0].Value
	}
	width := 0
	for _, b := range bindings {
		width = max(width, len(b.Name))
	}
	var sb strings.Builder
	for _, b := range bindings {
		fmt.Fprintf(&sb, "%-*s := \"%s\"\n", width, b.Name, escaper.Replace(b.Value))
	}
	return sb.String()
}
