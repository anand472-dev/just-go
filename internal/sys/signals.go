package sys

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// Signals is the command line's handling of the fatal signals, SIGHUP,
// SIGINT, SIGQUIT and SIGTERM, as upstream's SignalHandler does it. With no
// child running, a fatal signal ends the process at once with status 128
// plus the signal's number. While a child runs, the signal is remembered for
// the runner and otherwise ignored: the terminal sends SIGHUP, SIGINT and
// SIGQUIT to its whole foreground process group, so the child gets them
// too, and just waits for it. SIGTERM, which usually reaches only the
// parent, is forwarded to the children. Use NotifySignals to make one.
type Signals struct {
	mu       sync.Mutex
	children map[int]struct{} // the pids of the running children
	caught   int              // the first fatal signal caught while children ran
	exit     func(int)
	forward  func(pid, sig int)
	ch       chan os.Signal
}

// newSignals returns Signals that end the process through exit and send a
// signal to a child through forward. Pure.
func newSignals(exit func(int), forward func(pid, sig int)) *Signals {
	return &Signals{children: map[int]struct{}{}, exit: exit, forward: forward}
}

// NotifySignals starts handling the fatal signals for the rest of the
// process's life, or until Stop, and returns the handler for OSProcess.
// exit ends the process. Impure: it changes the process's signal
// disposition and starts a goroutine.
func NotifySignals(exit func(int)) *Signals {
	s := newSignals(exit, forwardSignal)
	s.ch = make(chan os.Signal, 8)
	signal.Notify(s.ch, fatalSignals()...)
	go func(ch <-chan os.Signal) {
		for sig := range ch {
			s.handle(int(sig.(syscall.Signal)))
		}
	}(s.ch)
	return s
}

// Stop restores the default handling of the fatal signals. Impure: it
// changes the process's signal disposition.
func (s *Signals) Stop() {
	signal.Stop(s.ch)
	close(s.ch)
}

// handle applies one fatal signal: exit when no child runs, else remember
// it, and forward SIGTERM to the children. Impure: it may end the process
// or signal the children, through the functions s was made with.
func (s *Signals) handle(sig int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.children) == 0 {
		s.exit(128 + sig)
		return
	}
	if s.caught == 0 {
		s.caught = sig
	}
	if sig == int(syscall.SIGTERM) {
		for pid := range s.children {
			s.forward(pid, sig)
		}
	}
}

// run starts cmd, tracked as a child, waits for it, and returns its status
// with the signal caught while it ran. The start happens under the lock, so
// a signal is never handled between the start and the tracking. Impure: it
// runs a process.
func (s *Signals) run(ctx context.Context, cmd *exec.Cmd) (int, error) {
	if err := s.start(cmd); err != nil {
		return exitStatus(ctx, err)
	}
	waitErr := cmd.Wait()
	caught := s.finish(cmd.Process.Pid)
	code, err := exitStatus(ctx, waitErr)
	return code, withCaught(err, caught)
}

// start starts cmd and tracks it. Impure: it runs a process.
func (s *Signals) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	s.children[cmd.Process.Pid] = struct{}{}
	return nil
}

// finish stops tracking the child pid and returns the signal caught while
// it ran, which it forgets, as upstream clears it once the runner has it.
// Pure apart from updating s.
func (s *Signals) finish(pid int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.children, pid)
	caught := s.caught
	if len(s.children) == 0 {
		s.caught = 0
	}
	return caught
}

// withCaught adds the caught signal to err, the status error of a child run.
// A failure to run the child or a cancellation stays as it is. Pure.
func withCaught(err error, caught int) error {
	if caught == 0 {
		return err
	}
	var se *SignalError
	if errors.As(err, &se) {
		se.Caught = caught
		return se
	}
	if err != nil {
		return err
	}
	return &SignalError{Caught: caught}
}
