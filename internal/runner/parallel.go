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

// ran records the calls made in one list of dependencies, and the calls that
// were requested: a call that is made again is not run again, but waited
// for, since another branch of a fork may still be running it. It is safe for
// the branches of a fork to share one.
type ran struct {
	mu    sync.Mutex
	gates map[string]*gate
}

// newRan returns a ran with no calls in it. It is pure.
func newRan() *ran { return &ran{gates: make(map[string]*gate)} }

// claim takes the call called key if no one has, and says so; the gate is the
// one its owner opens or abandons when it is done with the call, and so is the
// one that anyone else who makes the call waits at. Impure: it takes a lock.
func (r *ran) claim(key string) (g *gate, mine bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gates[key]; ok {
		return g, false
	}
	g = newGate()
	r.gates[key] = g
	return g, true
}

// callChain is the calls that are being run on the way to a call, the
// innermost first. A call made again within itself is a cycle the analyzer
// missed, and is not run again. The zero chain is empty.
type callChain struct {
	key string
	up  *callChain
}

// has reports whether the call called key is in the chain. It is pure.
func (c *callChain) has(key string) bool {
	for ; c != nil; c = c.up {
		if c.key == key {
			return true
		}
	}
	return false
}

// claimed is a call of a fork's branch, taken in order before any branch
// starts, so that the first branch to name a recipe is the one that runs it.
type claimed struct {
	call dependencyCall
	key  string
	gate *gate
	mine bool
	// cycle is true for a call that is already being run on the way here.
	cycle bool
}

// fork runs the calls at once, each in a goroutine of its own, and waits for
// all of them, as upstream joins its threads. A branch that fails does not
// stop the others. The result is that of the first branch, in order, that
// failed, and only its failure message is written: upstream returns that
// error alone. Impure: it runs commands.
func (inv *invocation) fork(calls []dependencyCall, shared *ran, chain *callChain) (int, error) {
	taken := make([]claimed, len(calls))
	for i, c := range calls {
		key := callKey(c.callee.Name, c.args)
		taken[i] = claimed{call: c, key: key, cycle: chain.has(key)}
		if !taken[i].cycle {
			taken[i].gate, taken[i].mine = shared.claim(key)
		}
	}
	results := make([]branchResult, len(taken))
	started := make([]chan struct{}, len(taken))
	for i := range started {
		started[i] = make(chan struct{})
	}
	var wg sync.WaitGroup
	for i, c := range taken {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i > 0 {
				<-started[i-1]
			}
			close(started[i])
			branch := *inv
			branch.held = new([]string)
			code, err := branch.branch(c, shared, chain)
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

// branch runs one call of a fork, if it owns it, and else waits for the
// branch that does. Impure: it runs commands.
func (inv *invocation) branch(c claimed, shared *ran, chain *callChain) (int, error) {
	switch {
	case c.cycle:
		return 0, nil
	case !c.mine:
		return inv.await(c.gate)
	}
	code, err := inv.execute(c.call.callee, c.call.args, shared, &callChain{key: c.key, up: chain})
	return settle(c.gate, code, err)
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
