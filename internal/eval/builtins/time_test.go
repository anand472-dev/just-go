package builtins

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// clockAt is a host whose clock reads the given instant.
func clockAt(at sys.Time) Host {
	return Host{Clock: sysfake.FakeClock{At: at}}
}

func TestDatetimeFormatsTheClockInItsLocalZone(t *testing.T) {
	// Unix 0 is the epoch; an offset of one hour east makes it 01:00 local.
	h := clockAt(sys.Time{Unix: 0, Offset: 3600})
	got, err := hostCall(t, h, "datetime", "%Y-%m-%d %H:%M:%S %z")
	if err != nil || got != "1970-01-01 01:00:00 +0100" {
		t.Errorf("datetime = %q, %v; want the local time with its offset", got, err)
	}
}

func TestDatetimeUTCFormatsInUTCWhateverTheLocalZone(t *testing.T) {
	h := clockAt(sys.Time{Unix: 0, Offset: 3600})
	got, err := hostCall(t, h, "datetime_utc", "%H:%M %z")
	if err != nil || got != "00:00 +0000" {
		t.Errorf("datetime_utc = %q, %v; want 00:00 +0000", got, err)
	}
	got, err = hostCall(t, h, "datetime_utc", "%Z")
	if err != nil || got != "UTC" {
		t.Errorf("datetime_utc zone name = %q, %v; want UTC", got, err)
	}
}

func TestDatetimeFailsWithUpstreamWording(t *testing.T) {
	h := clockAt(sys.Time{})
	// The oracle prints these two messages for the two kinds of bad format.
	_, err := hostCall(t, h, "datetime", "%!")
	if err == nil || err.Error() != "failed to parse time format string `%!`: bad or unsupported format string" {
		t.Errorf("datetime(%%!) error = %v", err)
	}
	_, err = hostCall(t, h, "datetime_utc", "%#z")
	if err == nil || err.Error() != "failed to format time with format string `%#z`" {
		t.Errorf("datetime_utc(%%#z) error = %v", err)
	}
}
