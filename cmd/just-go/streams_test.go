package main

import (
	"bytes"
	"io"
	"testing"
)

func TestChildStreamsAreTheCommandsOwnUnlessQuiet(t *testing.T) {
	var out, errOut bytes.Buffer
	o, e := childStreams(false, &out, &errOut)
	if o != &out || e != &errOut {
		t.Error("childStreams without quiet did not return the command's streams")
	}
	o, e = childStreams(true, &out, &errOut)
	if o != io.Discard || e != io.Discard {
		t.Error("childStreams with quiet did not discard the output")
	}
}
