package gsync

import (
	"context"
	"log"
	"sync"
	"time"
)

// StartScheduler runs due pairs once a minute until ctx is cancelled.
// ponytail: results land in gsync_last_run and the log, not the job engine —
// add job-engine integration only if scheduled syncs need to appear on /jobs.
func StartScheduler(ctx context.Context, svc *Service) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				svc.Tick(ctx)
			}
		}
	}()
}

// Tick runs all currently-due pairs sequentially (one rclone at a time keeps
// bandwidth predictable and the code lock-free). Returns the number run.
func (s *Service) Tick(ctx context.Context) int {
	due, err := s.DuePairs(ctx)
	if err != nil {
		log.Printf("[gsync] scheduler: listing due pairs: %v", err)
		return 0
	}
	if len(due) == 0 {
		return 0
	}
	var mu sync.Mutex
	var running bool // one Tick at a time; overlapping ticks skip silently
	mu.Lock()
	if running {
		mu.Unlock()
		return 0
	}
	running = true
	mu.Unlock()

	n := 0
	for _, p := range due {
		if ctx.Err() != nil {
			break
		}
		res, err := s.RunNow(ctx, p.ID)
		n++
		if err != nil {
			log.Printf("[gsync] scheduled sync %q failed: %v", p.Name, err)
		} else {
			log.Printf("[gsync] scheduled sync %q finished in %s: %s",
				p.Name, res.FinishedAt.Sub(res.StartedAt).Round(time.Second), firstLine(res.Summary))
		}
	}
	mu.Lock()
	running = false
	mu.Unlock()
	return n
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
