package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"meshium/internal/mod/transport"
)

// SnapshotCollector is the interface for a single discovery collector.
// Each collector gathers one category of information from a server
// (OS, hardware, Docker, services, databases, Nginx, disk, ports).
//
// Collectors are independent and run in parallel. If one collector fails,
// the others continue — the snapshot will contain partial results.
type SnapshotCollector interface {
	// Name returns the collector name (e.g., "os", "docker", "nginx").
	Name() string

	// Collect runs the collector and returns its result.
	// The result is a pointer to the collector's sub-struct (e.g., *OSInfo).
	// If the collector finds nothing (e.g., Docker not installed), it should
	// return nil, nil — not an error.
	Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error)

	// Timeout returns the maximum duration for this collector.
	// If the collector exceeds this timeout, it is cancelled.
	Timeout() time.Duration
}

// CollectorResult holds the result of a single collector execution.
type CollectorResult struct {
	Name   string
	Result interface{}
	Error  error
}

// CollectorRunner runs multiple SnapshotCollectors in parallel.
// Each collector runs in its own goroutine with its own timeout.
// One collector failure does not stop other collectors.
type CollectorRunner struct {
	collectors []SnapshotCollector
}

// NewCollectorRunner creates a CollectorRunner with the given collectors.
func NewCollectorRunner(collectors ...SnapshotCollector) *CollectorRunner {
	return &CollectorRunner{collectors: collectors}
}

// Run executes all collectors in parallel and assembles the results
// into a ServerSnapshot. Collectors that fail or timeout are recorded
// in the snapshot's CollectionErrors field — the snapshot may be partial.
func (r *CollectorRunner) Run(ctx context.Context, exec transport.SSHExecuter) (*ServerSnapshot, error) {
	if exec == nil {
		return nil, fmt.Errorf("SSH executer is nil")
	}

	results := make([]CollectorResult, len(r.collectors))
	var wg sync.WaitGroup

	for i, c := range r.collectors {
		wg.Add(1)
		go func(idx int, collector SnapshotCollector) {
			defer wg.Done()
			results[idx] = r.runCollector(ctx, exec, collector)
		}(i, c)
	}

	wg.Wait()

	// Assemble snapshot from results
	snapshot := &ServerSnapshot{
		CapturedAt: time.Now(),
	}

	for _, res := range results {
		if res.Error != nil {
			snapshot.CollectionErrors = append(snapshot.CollectionErrors, CollectorError{
				Collector: res.Name,
				Error:     res.Error.Error(),
			})
			continue
		}
		r.applyResult(snapshot, res)
	}

	return snapshot, nil
}

// runCollector runs a single collector with its own timeout.
func (r *CollectorRunner) runCollector(ctx context.Context, exec transport.SSHExecuter, c SnapshotCollector) CollectorResult {
	timeout := c.Timeout()
	collectorCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		data interface{}
		err  error
	}

	done := make(chan result, 1)
	go func() {
		data, err := c.Collect(collectorCtx, exec)
		done <- result{data, err}
	}()

	select {
	case res := <-done:
		return CollectorResult{Name: c.Name(), Result: res.data, Error: res.err}
	case <-collectorCtx.Done():
		if ctx.Err() != nil {
			// Parent context cancelled — don't blame the collector
			return CollectorResult{Name: c.Name(), Error: ctx.Err()}
		}
		return CollectorResult{Name: c.Name(), Error: fmt.Errorf("collector %s timed out after %s", c.Name(), timeout)}
	}
}

// applyResult applies a collector's result to the snapshot.
func (r *CollectorRunner) applyResult(snapshot *ServerSnapshot, res CollectorResult) {
	if res.Result == nil {
		return
	}

	switch v := res.Result.(type) {
	case *OSInfo:
		if v != nil {
			snapshot.OS = *v
		}
	case *HardwareInfo:
		if v != nil {
			snapshot.Hardware = *v
		}
	case *DockerInfo:
		snapshot.Docker = v
	case []SystemService:
		snapshot.Services = v
	case []DatabaseInfo:
		snapshot.Databases = v
	case *NginxInfo:
		snapshot.Nginx = v
	case []DiskPartition:
		snapshot.DiskUsage = v
	case []OpenPort:
		snapshot.NetworkPorts = v
	}
}

// DefaultCollectors returns the standard set of 8 collectors.
func DefaultCollectors() []SnapshotCollector {
	return []SnapshotCollector{
		&OSCollector{},
		&HardwareCollector{},
		&DockerCollector{},
		&ServiceCollector{},
		&DatabaseCollector{},
		&NginxCollector{},
		&DiskCollector{},
		&PortCollector{},
	}
}
