package changelog

import (
	"strings"
	"testing"
)

// The changelog is the upstream one, which starts with its title.
func TestTextIsTheUpstreamChangelog(t *testing.T) {
	if !strings.HasPrefix(Text(), "Changelog\n=========\n") {
		t.Errorf("Text() does not start with the changelog title")
	}
}
