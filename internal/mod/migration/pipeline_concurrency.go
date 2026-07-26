package migration

import "context"

// Pipeline concurrency control: the three independent guards that keep two
// runs from colliding.
//
//   runningPipelines — one migration cannot run twice
//   sem              — caps how many migrations run at once, globally
//   resourceLocks    — two DISTINCT migrations cannot touch the same server
//
// The resource lock is the one that protects a host: migration A (3→4) and
// migration B (4→5) would otherwise fork both sides of host 4 at once.
// --- Internal helpers ---

// GracefulDrain stops accepting new pipeline runs and waits for in-flight runs
// to reach a safe checkpoint, up to the registry's timeout. If the timeout is
// exceeded, remaining runs are force-cancelled; their Execute loop then calls
// interruptPipeline, which persists the checkpoint via context.Background() so
// it survives cancellation. It returns the number of runs that were
// force-cancelled. This is the shutdown-lifecycle entry point for the pipeline
// (Jalur B), mirroring the Job Engine's Stop() drain.
func (p *Pipeline) GracefulDrain(ctx context.Context) (int, error) {
	return p.lifecycle.drain(ctx)
}

func (p *Pipeline) tryAcquire(migrationID int) bool {
	_, loaded := p.runningPipelines.LoadOrStore(migrationID, struct{}{})
	return !loaded
}

func (p *Pipeline) release(migrationID int) {
	p.runningPipelines.Delete(migrationID)
}

// SetMaxConcurrent changes the global ceiling on simultaneously executing
// migrations. The change applies to new acquisitions only; in-flight runs are
// unaffected. n must be >= 1; values <= 0 are ignored (keep the current cap).
func (p *Pipeline) SetMaxConcurrent(n int) {
	if n < 1 {
		return
	}
	p.mu.Lock()
	p.maxConcurrent = n
	p.sem = make(chan struct{}, n)
	p.mu.Unlock()
}

// acquireSlot blocks until a global execution slot is free or ctx is done.
// Returns true if a slot was acquired (caller must call releaseSlot).
func (p *Pipeline) acquireSlot(ctx context.Context) bool {
	p.ensureSem()
	select {
	case p.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Pipeline) releaseSlot() {
	p.ensureSem()
	<-p.sem
}

// ensureSem lazily initializes the concurrency semaphore. Tests that build
// *Pipeline directly (bypassing NewPipeline) have a nil sem; without this they
// would block forever on a nil channel. Guarded so concurrent first-use is safe.
func (p *Pipeline) ensureSem() {
	if p.sem != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sem == nil {
		if p.maxConcurrent < 1 {
			p.maxConcurrent = DefaultMaxConcurrentMigrations
		}
		p.sem = make(chan struct{}, p.maxConcurrent)
	}
}

// acquireResourceLocks claims exclusive ownership of every server this
// migration will mutate (source and target). Two DISTINCT migrations may not
// hold the same server simultaneously — that is the gap runningPipelines and
// sem leave open. Refcounted so a migration whose source==target (rejected at
// the API, but defense-in-depth) or a retry does not deadlock itself.
// Returns the list of serverIDs it claimed; releaseResourceLocks frees them.
func (p *Pipeline) acquireResourceLocks(servers ...int) []int {
	p.resourceMu.Lock()
	defer p.resourceMu.Unlock()
	if p.resourceLocks == nil {
		p.resourceLocks = make(map[int]int)
	}
	claimed := make([]int, 0, len(servers))
	for _, s := range servers {
		if s == 0 {
			continue
		}
		p.resourceLocks[s]++
		claimed = append(claimed, s)
	}
	return claimed
}

// releaseResourceLocks decrements the holder count for each server, deleting
// the entry once it returns to zero so a later migration may claim it.
func (p *Pipeline) releaseResourceLocks(servers []int) {
	if len(servers) == 0 {
		return
	}
	p.resourceMu.Lock()
	defer p.resourceMu.Unlock()
	for _, s := range servers {
		if p.resourceLocks[s] > 1 {
			p.resourceLocks[s]--
		} else {
			delete(p.resourceLocks, s)
		}
	}
}

// canAcquireResource reports whether NO one else currently holds any of the
// given servers. Used before blocking so a retry/bump sees the live state.
func (p *Pipeline) canAcquireResource(servers ...int) bool {
	p.resourceMu.Lock()
	defer p.resourceMu.Unlock()
	for _, s := range servers {
		if s != 0 && p.resourceLocks[s] > 0 {
			return false
		}
	}
	return true
}

// tryAcquireResource atomically checks AND claims every server in one critical
// section, so two goroutines cannot both pass the check and then both claim the
// same host (a check-then-acquire TOCTOU). Returns the claimed server list, or
// nil if any one is already held by another migration (nothing is claimed).
func (p *Pipeline) tryAcquireResource(servers ...int) []int {
	p.resourceMu.Lock()
	defer p.resourceMu.Unlock()
	if p.resourceLocks == nil {
		p.resourceLocks = make(map[int]int)
	}
	for _, s := range servers {
		if s != 0 && p.resourceLocks[s] > 0 {
			return nil
		}
	}
	claimed := make([]int, 0, len(servers))
	for _, s := range servers {
		if s == 0 {
			continue
		}
		p.resourceLocks[s]++
		claimed = append(claimed, s)
	}
	return claimed
}
