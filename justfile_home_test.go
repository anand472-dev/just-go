package just

import "testing"

func TestHomeOfIsTheLastHomeEntry(t *testing.T) {
	if got := homeOf([]string{"HOME=/a", "PATH=/bin", "HOME=/b"}); got != "/b" {
		t.Errorf("homeOf = %q; want /b", got)
	}
	if got := homeOf([]string{"PATH=/bin"}); got != "" {
		t.Errorf("homeOf without HOME = %q; want empty", got)
	}
}
