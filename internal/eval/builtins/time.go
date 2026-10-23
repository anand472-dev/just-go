package builtins

import "gitea.com/wimwian/just-go/internal/timefmt"

// datetimeLocal formats the time the Host's clock reads, in the local time
// zone, as the format string says. It is impure: it reads Host.Clock.
func datetimeLocal(h Host, args []string) (string, error) {
	return timefmt.Format(h.Clock.Now(), args[0])
}

// datetimeUTC formats the time the Host's clock reads in UTC, whatever the
// local zone is. It is impure: it reads Host.Clock.
func datetimeUTC(h Host, args []string) (string, error) {
	t := h.Clock.Now()
	t.Offset, t.UTC = 0, true
	return timefmt.Format(t, args[0])
}
