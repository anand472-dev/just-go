package runner

import (
	"io"
	"sync"
)

// gate is a latch that a recipe's steps open when the recipe is done, for the
// branches of a fork that would have run it themselves but find another
// branch has.
type gate struct {
	done chan struct{}
	once sync.Once
	// abandoned is true when the branch that was to open the gate failed
	// first. It is read only after done is closed.
	abandoned bool
}

// newGate returns a gate that is shut until it is opened or abandoned. It is
// pure.
func newGate() *gate { return &gate{done: make(chan struct{})} }

// open lets every step that waits at g go on. A gate opens or is abandoned
// once, whichever comes first. Impure: it wakes other goroutines.
func (g *gate) open() { g.once.Do(func() { close(g.done) }) }

// abandon lets the steps that wait at g go on, to fail, because the branch
// that was to open it did not get to. It does nothing for a gate that is
// open. Impure: it wakes other goroutines.
func (g *gate) abandon() {
	g.once.Do(func() {
		g.abandoned = true
		close(g.done)
	})
}

// branchResult is the outcome of one branch of a fork: the exit code and
// error of its steps, and the failure messages it held back.
type branchResult struct {
	code int
	err  error
	held []string
}

// failed reports whether the branch did not succeed. It is pure.
func (b branchResult) failed() bool { return b.err != nil || b.code != 0 }

// sequence runs steps in order and returns the exit code of the first that
// fails, with its error. Whatever happens, the gates of the steps that signal
// are done with when it returns, so a branch that waits for them does not
// wait forever. Impure: it runs commands.
func (inv *invocation) sequence(steps []step) (int, error) {
	defer abandonGates(steps)
	for _, s := range steps {
		if code, err := inv.step(s); err != nil || code != 0 {
			return code, err
		}
	}
	return 0, nil
}

// abandonGates abandons the gate of each step of steps that has one to
// signal and has not opened it. Impure: it wakes other goroutines.
func abandonGates(steps []step) {
	for _, s := range steps {
		if s.flow != nil && s.flow.signal != nil {
			s.flow.signal.abandon()
		}
	}
}

// flow runs a step that is about branches: it runs them, waits for a gate, or
// opens one. Impure: it runs commands.
func (inv *invocation) flow(c *control) (int, error) {
	switch {
	case c.fork != nil:
		return inv.fork(c.fork)
	case c.wait != nil:
		return inv.await(c.wait)
	}
	c.signal.open()
	return 0, nil
}

// fork runs the branches at once, each in a goroutine of its own, and waits
// for all of them, as upstream joins its threads. A branch that fails does not
// stop the others. The result is that of the first branch, in order, that
// failed, and only its failure message is written: upstream returns that
// error alone. Impure: it runs commands.
func (inv *invocation) fork(branches [][]step) (int, error) {
	results := make([]branchResult, len(branches))
	started := make([]chan struct{}, len(branches))
	for i := range started {
		started[i] = make(chan struct{})
	}
	var wg sync.WaitGroup
	for i, steps := range branches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i > 0 {
				<-started[i-1]
			}
			close(started[i])
			branch := *inv
			branch.held = new([]string)
			code, err := branch.sequence(steps)
			results[i] = branchResult{code: code, err: err, held: *branch.held}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.failed() {
			for _, msg := range r.held {
				inv.say(msg)
			}
			return r.code, r.err
		}
	}
	return 0, nil
}

// await waits for g to open, or for the invocation to be cancelled. A gate
// that was abandoned fails the branch that waited, with no message: the
// failure of the branch that abandoned it is the one reported. Impure: it
// blocks.
func (inv *invocation) await(g *gate) (int, error) {
	select {
	case <-g.done:
		if g.abandoned {
			return 1, nil
		}
		return 0, nil
	case <-inv.ctx.Done():
		return 0, inv.ctx.Err()
	}
}

// lockedWriter writes to w one call at a time, under a lock it shares with
// the other streams of its run.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

// Write writes p to the underlying stream. Impure: it writes.
func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// lockStreams returns the writers made safe to write to from several
// goroutines at once, which is what the steps of a fork do, under one lock
// for all of them, as they may be the same stream. A nil writer stays nil: the
// commands discard what they write to it. It is pure.
func lockStreams(writers ...io.Writer) []io.Writer {
	mu := new(sync.Mutex)
	locked := make([]io.Writer, len(writers))
	for i, w := range writers {
		if w != nil {
			locked[i] = lockedWriter{mu: mu, w: w}
		}
	}
	return locked
}
