//go:build unix

package sys

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// recorder collects what Signals does through its exit and forward hooks.
type recorder struct {
	exits    []int
	forwards [][2]int
}

func (r *recorder) signals() *Signals {
	return newSignals(func(code int) { r.exits = append(r.exits, code) },
		func(pid, sig int) { r.forwards = append(r.forwards, [2]int{pid, sig}) })
}

func TestSignalsExitWithoutChildren(t *testing.T) {
	var r recorder
	s := r.signals()
	s.handle(2)
	s.handle(15)
	if !reflect.DeepEqual(r.exits, []int{130, 143}) || r.forwards != nil {
		t.Fatalf("exits %v, forwards %v; want [130 143] and none", r.exits, r.forwards)
	}
}

// While a child runs, the first fatal signal is remembered, and only
// SIGTERM is forwarded, as upstream's SignalHandler::handle.
func TestSignalsRememberTheFirstAndForwardSIGTERM(t *testing.T) {
	var r recorder
	s := r.signals()
	s.children[41], s.children[42] = struct{}{}, struct{}{}
	s.handle(2)
	s.handle(1)
	s.handle(15)
	if r.exits != nil || len(r.forwards) != 2 || r.forwards[0][1] != 15 || r.forwards[1][1] != 15 {
		t.Fatalf("exits %v, forwards %v; want no exit and SIGTERM to both", r.exits, r.forwards)
	}
	if got := s.finish(41); got != 2 {
		t.Fatalf("finish(41) = %d; want 2", got)
	}
	if got := s.finish(42); got != 2 {
		t.Fatalf("finish(42) = %d; want 2, kept while a child ran", got)
	}
	if got := s.finish(43); got != 0 {
		t.Fatalf("caught = %d after the last child; want it forgotten", got)
	}
}

func TestWithCaught(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		err    error
		caught int
		want   error
	}{
		{nil, 0, nil},
		{boom, 0, boom},
		{boom, 2, boom},
		{nil, 2, &SignalError{Caught: 2}},
		{&SignalError{Signal: 9}, 15, &SignalError{Signal: 9, Caught: 15}},
	}
	for _, c := range cases {
		if got := withCaught(c.err, c.caught); !reflect.DeepEqual(got, c.want) {
			t.Errorf("withCaught(%v, %d) = %v; want %v", c.err, c.caught, got, c.want)
		}
	}
}

func TestSignalErrorAndNames(t *testing.T) {
	cases := map[string]string{
		(&SignalError{Signal: 15}).Error():             "terminated by signal 15",
		(&SignalError{Signal: 2, Caught: 2}).Error():   "terminated by signal 2",
		(&SignalError{Caught: 2}).Error():              "interrupted by SIGINT",
		SignalName(1) + SignalName(3) + SignalName(15): "SIGHUPSIGQUITSIGTERM",
		SignalName(29): "signal 29",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	}
}

func TestOSProcessReportsATerminatingSignal(t *testing.T) {
	code, err := OSProcess{}.Run(context.Background(), ProcessSpec{Argv: []string{"/bin/sh", "-c", "kill -TERM $$"}})
	if code != -1 || !reflect.DeepEqual(err, &SignalError{Signal: 15}) {
		t.Fatalf("Run = %d, %v; want -1 and signal 15", code, err)
	}
}

// With Signals, the child shares the caller's process group, so it is in
// the terminal's foreground group and can read the terminal (FINAL-1).
func TestOSProcessWithSignalsKeepsTheCallersGroup(t *testing.T) {
	var r recorder
	for _, c := range []struct {
		p    OSProcess
		same bool
	}{{OSProcess{Signals: r.signals()}, true}, {OSProcess{}, false}} {
		var out bytes.Buffer
		spec := ProcessSpec{Argv: []string{"/bin/sh", "-c", "ps -o pgid= -p $$"}, Env: []string{"PATH=/bin:/usr/bin"}, Stdout: &out}
		if code, err := c.p.Run(context.Background(), spec); code != 0 || err != nil {
			t.Fatalf("Run = %d, %v", code, err)
		}
		pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
		if err != nil || (pgid == syscall.Getpgrp()) != c.same {
			t.Fatalf("child group %q, ours %d; want same=%v", out.String(), syscall.Getpgrp(), c.same)
		}
	}
}

// runTracked runs argv through s in the background and, once the child is
// tracked, applies sig to s. It returns Run's results.
func runTracked(t *testing.T, s *Signals, sig int, argv ...string) (int, error) {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := OSProcess{Signals: s}.Run(context.Background(), ProcessSpec{Argv: argv})
		done <- result{code, err}
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.children)
		s.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child was never tracked")
		}
	}
	s.handle(sig)
	res := <-done
	return res.code, res.err
}

func TestOSProcessWithSignalsForwardsSIGTERM(t *testing.T) {
	var r recorder
	s := newSignals(func(code int) { r.exits = append(r.exits, code) }, forwardSignal)
	code, err := runTracked(t, s, 15, "/bin/sleep", "5")
	if code != -1 || !reflect.DeepEqual(err, &SignalError{Signal: 15, Caught: 15}) || r.exits != nil {
		t.Fatalf("Run = %d, %v, exits %v; want -1, signal 15 caught 15, no exit", code, err, r.exits)
	}
}

func TestOSProcessWithSignalsReportsTheCaughtSignal(t *testing.T) {
	var r recorder
	code, err := runTracked(t, r.signals(), 2, "/bin/sleep", "0.2")
	if code != 0 || !reflect.DeepEqual(err, &SignalError{Caught: 2}) {
		t.Fatalf("Run = %d, %v; want 0 and caught SIGINT", code, err)
	}
}

func TestOSProcessWithSignalsStartFailure(t *testing.T) {
	var r recorder
	s := r.signals()
	code, err := OSProcess{Signals: s}.Run(context.Background(), ProcessSpec{Argv: []string{t.TempDir()}})
	if code != -1 || err == nil || len(s.children) != 0 {
		t.Fatalf("Run = %d, %v with %d children; want -1, an error and none", code, err, len(s.children))
	}
}

func TestNotifySignalsHandlesTheProcessSignals(t *testing.T) {
	exits := make(chan int, 1)
	s := NotifySignals(func(code int) { exits <- code })
	defer s.Stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exits:
		if code != 129 {
			t.Fatalf("exit(%d); want 129", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP was not handled")
	}
}
