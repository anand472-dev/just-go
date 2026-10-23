package runner

import (
	"strings"

	"gitea.com/wimwian/just-go/internal/ansi"
)

// styleError styles a message written to Stderr when color is on: an error
// message is styled as ansi.ErrorLine styles it, and any other message is
// returned as it is. It is pure.
func styleError(color bool, msg string) string {
	body, ok := strings.CutPrefix(msg, "error: ")
	if !color || !ok {
		return msg
	}
	return ansi.ErrorLine(true, strings.TrimSuffix(body, "\n")) + "\n"
}

// styleCommand is an echoed command line as it is written: bold, or in the
// command color too, when color is on and --no-highlight is not given; else
// it is plain. It is pure.
func (inv *invocation) styleCommand(command string) string {
	if !inv.plan.Color || inv.plan.NoHighlight {
		return command
	}
	code := "1"
	if inv.plan.CommandColor != "" {
		code += ";" + inv.plan.CommandColor
	}
	return ansi.Paint(true, code, command)
}
